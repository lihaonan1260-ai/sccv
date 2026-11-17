package gui

// BG-004 page lifecycle (0号裁决 S4): the GUI process outlives a browser tab
// only while a page is plausibly watching it. Two presence channels feed one
// decision loop:
//
//   - GET /api/events (SSE): the page opens exactly one stream; a closed tab
//     drops it and the server notices in well under a second (the request
//     context is canceled when the peer goes away).
//   - GET /api/heartbeat: a 20s JS ping refreshing last-seen — the slow
//     fallback that only matters when SSE is abnormally disconnected.
//
// Rule: when the SSE connection count falls to zero the decision arms a 15s
// grace (a refresh reconnects within 1~3s and cancels it; so does a heartbeat
// arriving inside the window). Once the grace elapses, an idle job slot
// triggers the same graceful shutdown chain as the /api/shutdown button, with
// the event reason "page-idle" instead of "manual". A running job defers the
// exit until the job reaches a terminal state plus a 30s result review
// window, so closing the page mid-analysis never kills the work. Independent
// of the grace, a last-seen older than 120s with an idle slot exits too —
// the fallback for pages whose stream never established at all (the frontend
// keeps heartbeating in that state, so only a truly gone page goes stale).
//
// A page that never opened a stream and never heartbeated (API-only clients,
// probes) never arms the decision: the process stays up exactly as before
// BG-004. The manual exit keeps priority, and both triggers funnel through
// one CAS-guarded requestShutdown, so a double shutdown is unrepresentable
// and exactly one shutdown event with the first trigger's reason is logged.

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/boyter/scc/v4/gui/logger"
)

// Shutdown event reasons (OPT-006 hook payload): which trigger won the
// single-shot exit funnel.
const (
	shutdownReasonManual   = "manual"
	shutdownReasonPageIdle = "page-idle"
)

// Lifecycle timings (0号裁决 S4). The grace covers an EventSource reconnect
// (1~3s); the review window lets a reopened page read a finished result; the
// stale bound stays far above the frontend's 20s heartbeat period so only a
// dead page can trip it.
const (
	pageIdleGrace         = 15 * time.Second
	jobReviewWindow       = 30 * time.Second
	heartbeatStaleAfter   = 120 * time.Second
	sseKeepAliveInterval  = 15 * time.Second
	lifecycleTickInterval = 1 * time.Second
)

// pageLifecycle accumulates the presence signals and derives the exit
// decision one tick at a time. All state is guarded by mu; now and jobBusy
// are injectable so the tests drive time and slot occupancy without sleeps.
type pageLifecycle struct {
	mu sync.Mutex

	// seams: now supplies the clock, jobBusy reports whether the single R2
	// slot is occupied (analyze.go read-only seam).
	now     func() time.Time
	jobBusy func() bool

	// stop is closed once by halt when the server is done deciding.
	stop     chan struct{}
	haltOnce sync.Once

	// armed flips on with the first presence signal (SSE connect or
	// heartbeat); until then no exit decision ever fires.
	armed bool

	// sse is the live /api/events stream count; sseEver records whether a
	// stream existed at all, so only a page that had one can grace-exit.
	sse     int
	sseEver bool

	// lastSeen is the freshest presence signal of any kind.
	lastSeen time.Time

	// zeroSince is the start of the current SSE-zero window (zero value
	// while any stream is open, or when no stream ever opened). A heartbeat
	// inside the window restarts it ("heartbeat cancels the grace").
	zeroSince time.Time

	// busyDuringZero records that a job ran while no page was watching;
	// when it ends, reviewDeadline = end + jobReviewWindow gates the exit.
	busyDuringZero bool
	reviewDeadline time.Time
}

// newPageLifecycle assembles the production lifecycle: real clock, the
// process-global job slot seam, a fresh unarmed state.
func newPageLifecycle() *pageLifecycle {
	return &pageLifecycle{
		now:     time.Now,
		jobBusy: jobSlotBusy,
		stop:    make(chan struct{}),
	}
}

// sseConnected registers one open /api/events stream: the strongest presence
// signal there is (the page is rendering right now).
func (l *pageLifecycle) sseConnected() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.armed = true
	l.sse++
	l.sseEver = true
	l.lastSeen = l.now()
	l.zeroSince = time.Time{}
}

// sseDisconnected drops one stream. When the count reaches zero the grace
// window starts here.
func (l *pageLifecycle) sseDisconnected() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.sse > 0 {
		l.sse--
	}
	if l.sse == 0 {
		l.zeroSince = l.now()
	}
}

// heartbeat refreshes the slow-fallback presence signal. Inside an SSE-zero
// window it restarts the grace (定稿规则：期间任一 SSE 重连或心跳即取消); a
// page whose stream never established only refreshes last-seen — its exit is
// governed by the 120s stale bound alone, never by the grace.
func (l *pageLifecycle) heartbeat() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.armed = true
	l.lastSeen = l.now()
	if l.sse == 0 && !l.zeroSince.IsZero() {
		l.zeroSince = l.now()
	}
}

// halt stops the decision loop exactly once. Idempotent and safe to call
// from both the shutdown winner and Start's deferred cleanup.
func (l *pageLifecycle) halt() {
	l.haltOnce.Do(func() { close(l.stop) })
}

// evaluate applies the lifecycle rule once and reports whether the process
// should begin its graceful exit (with the reason for the shutdown event).
// Called from the decision loop only; the injected busy seam keeps it
// testable without a real analysis.
func (l *pageLifecycle) evaluate() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.armed {
		return "", false
	}
	if l.sse > 0 {
		// A page is watching: any pending exit state is obsolete.
		l.zeroSince = time.Time{}
		l.busyDuringZero = false
		l.reviewDeadline = time.Time{}
		return "", false
	}
	// SSE zero from here on.
	if jobBusy := l.jobBusy(); jobBusy {
		// The slot is occupied (R2): never exit mid-work; the review window
		// re-arms from the job's terminal transition instead.
		l.busyDuringZero = true
		l.reviewDeadline = time.Time{}
		return "", false
	}
	if l.busyDuringZero {
		// Busy→idle transition observed while no page was watching: open the
		// result review window (顺延至作业 done 后 +30s).
		l.busyDuringZero = false
		l.reviewDeadline = l.now().Add(jobReviewWindow)
	}
	if !l.reviewDeadline.IsZero() {
		if l.now().Before(l.reviewDeadline) {
			return "", false // the review window is still open
		}
		return shutdownReasonPageIdle, true
	}
	// Grace path: only a page that once held a stream can grace-exit, and
	// heartbeats keep restarting the window while a page might still be out
	// there (heartbeat-only pages rely on the stale bound below instead).
	if !l.zeroSince.IsZero() && l.now().Sub(l.zeroSince) >= pageIdleGrace {
		return shutdownReasonPageIdle, true
	}
	// Stale fallback: no presence signal of any kind for over two minutes
	// with an idle slot — the page is gone no matter what SSE reported.
	if l.now().Sub(l.lastSeen) >= heartbeatStaleAfter {
		return shutdownReasonPageIdle, true
	}
	return "", false
}

// handleEvents serves the SSE presence stream (BG-004). Token-gated like
// every route (the gate wraps the mux), flushed immediately so EventSource
// turns open, and counted by the lifecycle for as long as the request
// context lives — a closed tab cancels it in well under a second.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set(contentTypeHeader, "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	// Reconnect hint for EventSource (1~3s typical, covering the refresh
	// gap inside the 15s grace) — flushed before anything else so the
	// browser marks the stream open right away.
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()

	s.lc.sseConnected()
	defer s.lc.sseDisconnected()

	keepAlive := time.NewTicker(sseKeepAliveInterval)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return // tab/page closed (or client vanished): count drops now
		case <-keepAlive.C:
			// SSE comment line: ignored by EventSource, keeps intermediaries
			// from reaping an idle-looking connection. A write error means
			// the peer is gone; the context closes moments later.
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handleHeartbeat refreshes the page last-seen (BG-004 slow fallback). 204
// with no body: the ping carries no data and must stay cheap.
func (s *server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	s.lc.heartbeat()
	w.WriteHeader(http.StatusNoContent)
}

// requestShutdown is the single graceful-exit funnel (BG-004): the manual
// /api/shutdown path and the page-idle auto exit both land here. The CAS
// admits exactly one winner — a second trigger is a no-op, the process can
// never shut down twice, and the logged event carries the winner's reason.
func (s *server) requestShutdown(reason string) {
	if !s.stopping.CompareAndSwap(false, true) {
		return
	}
	// OPT-006 hook: shutdown logged after the winner is decided; best-effort,
	// silent on any internal failure (degradation contract of the package).
	appLog.Event(logger.EventShutdown, map[string]any{"reason": reason})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		_ = s.httpServer.Shutdown(ctx)
	}()
}

// runLifecycle is the BG-004 decision loop: one evaluate per tick until the
// lifecycle halts or the rule fires the (single-shot) page-idle exit.
func (s *server) runLifecycle() {
	ticker := time.NewTicker(lifecycleTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.lc.stop:
			return
		case <-ticker.C:
			if s.stopping.Load() {
				return // manual exit won the race; stop deciding
			}
			if reason, ok := s.lc.evaluate(); ok {
				s.requestShutdown(reason)
				return
			}
		}
	}
}
