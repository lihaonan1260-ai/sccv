// Package gui serves the sccv single-page web GUI on the loopback interface.
//
// Consumption contract (DEC-203/DEC-204): the main package pre-parses
// --port/SCCV_PORT into main.guiPort and SCCV_NO_BROWSER into
// main.guiNoBrowser before cobra sees argv. T-005's integration then starts
// the service with
//
//	gui.Start(gui.ServerOptions{Port: guiPort, Lang: lang, NoBrowser: guiNoBrowser})
//
// A zero Port means "unset" and binds a random free port, keeping an explicit
// port unrepresentable as unset. The language is applied process-wide through
// i18n.SetLang (DEC-008: entry points set it explicitly, the package default
// stays "en"); an empty ServerOptions.Lang falls back to Chinese, the product
// default.
//
// Processor entry points are locked down per R1/DEC-002: no Process (it calls
// os.Exit), only the quiet/lazy setup, the read-only language snapshot and
// the read-only version string for /api/health and /api/config.
package gui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/boyter/scc/v4/gui/logger"
	"github.com/boyter/scc/v4/i18n"
	"github.com/boyter/scc/v4/processor"
)

// defaultLanguage is the product default (F2/DEC-008): an unset
// ServerOptions.Lang means Chinese.
const defaultLanguage = "zh"

// shutdownGrace bounds how long Shutdown may wait on in-flight requests.
// The listener itself closes immediately, so callers observe the port going
// dark well inside the 5s budget TC-071 allows.
const shutdownGrace = 3 * time.Second

// Every JSON body of the API carries this content type.
const (
	contentTypeHeader = "Content-Type"
	contentTypeJSON   = "application/json"
)

// errorPayload is the single error shape of the API: {"error":"..."} with a
// byte-exact body, which the frozen tests compare literally.
type errorPayload struct {
	Error string `json:"error"`
}

// writeError emits the shared error shape. Constructing the payload here
// keeps every call site free of shared mutable state.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorPayload{Error: message})
}

// ServerOptions carries the launch choices staged by the main package.
// Zero values are meaningful: Port 0 = random free port, Lang "" = Chinese,
// NoBrowser false = open the browser after the listener is bound.
type ServerOptions struct {
	Port      int
	Lang      string
	NoBrowser bool
}

// languageEntry is one row of the /api/languages snapshot (implementation
// plan §5): the engine language name plus its extension and exact-file-name
// matchers, sorted by name.
type languageEntry struct {
	Name       string   `json:"name"`
	Extensions []string `json:"extensions"`
	FileNames  []string `json:"fileNames"`
}

type healthPayload struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Lang    string `json:"lang"`
	Pid     int    `json:"pid"`
}

type configPayload struct {
	Lang    string `json:"lang"`
	Version string `json:"version"`
}

type shutdownPayload struct {
	Ok bool `json:"ok"`
}

// appLog is the exe-directory JSONL event logger (OPT-006/DEC-O09), assembled
// by Start only. A nil handle — the state every direct-server test runs in —
// or a logger degraded inside the package makes each hook a silent no-op, so
// the event log can never influence the API surface (BL-004 log-on/log-off
// byte-identical parity) nor stdout (R3).
var appLog *logger.Logger

// server holds the per-instance state created by Start. No package-level
// mutable state besides analyzeMu (frozen-test seam owned by T-004): every
// instance carries its own token, port and snapshot explicitly.
type server struct {
	token      string
	port       int
	version    string
	languages  []languageEntry
	httpServer *http.Server

	// BG-004 page lifecycle: presence signals (SSE streams, heartbeats) and
	// the single-shot exit funnel. stopping makes a second shutdown
	// unrepresentable — the manual button and the page-idle auto exit race
	// for one CAS, the winner's reason is the one logged.
	lc       *pageLifecycle
	stopping atomic.Bool
}

// Start locks the processor into its quiet GUI defaults, binds the loopback
// listener, prints the single SCCV_READY line (R3/DEC-207: exactly one line
// per process lifetime, only after the bind succeeds) and serves until
// Shutdown is requested through /api/shutdown. It blocks the caller and
// returns nil after a graceful shutdown, so the process can exit cleanly
// instead of calling os.Exit.
func Start(opts ServerOptions) error {
	lockProcessorQuietDefaults()
	i18n.SetLang(languageOrDefault(opts.Lang))

	token, err := newToken()
	if err != nil {
		return fmt.Errorf("gui: mint access token: %w", err)
	}

	listener, err := net.Listen("tcp", net.JoinHostPort(loopbackHost, strconv.Itoa(opts.Port)))
	if err != nil {
		return fmt.Errorf("gui: bind %s: %w", listenerAddress(opts.Port), err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	srv := newServer(token, port)
	srv.httpServer = &http.Server{Handler: srv.handler()}

	// OPT-006: exe-directory JSONL event log. Assembly is side-effect-free
	// for the API surface — every failure inside the package degrades to a
	// silent no-op (R3: nothing here ever reaches stdout).
	appLog = logger.New(logger.Config{})
	defer appLog.Close()

	// BG-004: the page-idle decision loop runs for the lifetime of the
	// server; once Serve returns (any exit path) the loop stops with it.
	go srv.runLifecycle()
	defer srv.lc.halt()

	// DEC-207: the URL and token go out only once the listener is bound, so
	// the readiness probe that reads this line can never connect too early.
	// The token makes this line a short-lived credential (DEC-310) — treat
	// captured stdout accordingly.
	baseURL := fmt.Sprintf("http://%s:%d/", loopbackHost, port)
	fmt.Fprintf(os.Stdout, "SCCV_READY url=%s token=%s\n", baseURL, token)

	// OPT-006 hook: startup facts only — version/port/lang, never the token
	// or the ready URL (DEC-O09 content boundary).
	appLog.Event(logger.EventStartup, map[string]any{
		"version": srv.version,
		"port":    port,
		"lang":    i18n.Lang,
	})

	if !opts.NoBrowser {
		if err := openBrowser(baseURL + "?" + tokenQuery + "=" + token); err != nil {
			// Losing the browser is a convenience failure, not a service
			// failure: the URL is already on stdout. Report on stderr only,
			// keeping stdout to the single R3 line.
			fmt.Fprintf(os.Stderr, "sccv: open browser: %v\n", err)
		}
	}

	if err := srv.httpServer.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// lockProcessorQuietDefaults pins the engine state the GUI requires before
// any request is served (DEC-002): no stdout chatter (R3 — printWarn/Debug/
// Trace all write to stdout), lazy file loading, and the precomputed language
// constants that also populate LanguageDatabase.
func lockProcessorQuietDefaults() {
	processor.Verbose = false
	processor.Debug = false
	processor.Trace = false
	processor.ConfigureLazy(true)
	processor.ProcessConstants()
}

// languageOrDefault resolves the process language: an unset option falls back
// to the product default (DEC-008), anything else is passed through verbatim.
func languageOrDefault(lang string) string {
	if lang == "" {
		return defaultLanguage
	}
	return lang
}

// listenerAddress renders the bind address for error messages.
func listenerAddress(port int) string {
	return net.JoinHostPort(loopbackHost, strconv.Itoa(port))
}

// newServer builds the per-instance state. The languages snapshot is taken
// here because ProcessConstants has already run inside Start; the database is
// immutable from then on, but the payload still carries copies so no response
// aliases engine state.
func newServer(token string, port int) *server {
	return &server{
		token:     token,
		port:      port,
		version:   processor.Version,
		languages: snapshotLanguages(),
		lc:        newPageLifecycle(),
	}
}

// snapshotLanguages copies the engine language database into the stable
// /api/languages payload (implementation plan §5): name, extensions and exact
// file names, sorted by name.
func snapshotLanguages() []languageEntry {
	database := processor.LanguageDatabase()
	entries := make([]languageEntry, 0, len(database))
	for name, lang := range database {
		entries = append(entries, languageEntry{
			Name:       name,
			Extensions: orEmptySlice(lang.Extensions),
			FileNames:  orEmptySlice(lang.FileNames),
		})
	}
	slices.SortFunc(entries, func(a, b languageEntry) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return entries
}

// orEmptySlice keeps absent matchers JSON-encoded as [] instead of null.
func orEmptySlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return slices.Clone(values)
}

// handler wires the full route table (implementation plan §5, 13 endpoints,
// plus the two BG-004 presence channels) behind the token gate, so every
// response — errors included — passes the same defense stack.
func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /static/", s.handleStatic)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/languages", s.handleLanguages)
	mux.HandleFunc("GET /api/explain", s.handleExplain)     // T-301/OPT-003：口径说明（handlers_explain.go，同一 token 门禁）
	mux.HandleFunc("GET /api/events", s.handleEvents)       // BG-004：页面在场 SSE 流（lifecycle.go，同一 token 门禁）
	mux.HandleFunc("GET /api/heartbeat", s.handleHeartbeat) // BG-004：慢心跳兜底（lifecycle.go，同一 token 门禁）
	// 501 seams below: T-004 replaces handleAnalyze/handleJob (analyze.go),
	// T-031 handleBrowse (handlers_browse.go), T-032 handleExportJSON/
	// handleExportCSV (handlers_export.go), T-033 handleReport/handleDownload
	// (handlers_report.go). Route table stays as-is.
	mux.HandleFunc("POST /api/analyze", s.handleAnalyze)
	mux.HandleFunc("POST /api/tree", s.handleTree)             // OPT-001/T-201：向导目录树（handlers_tree.go，同一 token 门禁，不占 R2 槽）
	mux.HandleFunc("POST /api/extensions", s.handleExtensions) // OPT-002/T-202：扩展名盘点（handlers_extensions.go，同一 token 门禁，不占 R2 槽）
	mux.HandleFunc("GET /api/job/", s.handleJob)
	mux.HandleFunc("POST /api/browse", s.handleBrowse)
	mux.HandleFunc("POST /api/report", s.handleReport)
	mux.HandleFunc("GET /api/download/", s.handleDownload)
	mux.HandleFunc("POST /api/export/json", s.handleExportJSON)
	mux.HandleFunc("POST /api/export/csv", s.handleExportCSV)
	mux.HandleFunc("POST /api/shutdown", s.handleShutdown)
	return s.gate(mux)
}

// handleHealth answers the readiness probe with presence/type/range fields
// (DEC-210), never with timing or count data.
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthPayload{
		Status:  "ok",
		Version: s.version,
		Lang:    i18n.Lang,
		Pid:     os.Getpid(),
	})
}

// handleConfig exposes the two facts the SPA needs before first paint.
func (s *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, configPayload{
		Lang:    i18n.Lang,
		Version: s.version,
	})
}

// handleLanguages serves the startup snapshot of the engine language database.
func (s *server) handleLanguages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.languages)
}

// handleShutdown acknowledges the exit request, flushes the response, and
// funnels into the shared single-shot exit (K9: graceful exit, no os.Exit
// from the handler; BG-004: the same funnel the page-idle auto exit uses, so
// the two triggers can never double-shutdown).
func (s *server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, shutdownPayload{Ok: true})
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	s.requestShutdown(shutdownReasonManual)
}

// writeJSON marshals first so a payload that cannot encode still produces a
// clean status instead of a half-written response, then writes the body
// without a trailing newline: error bodies are compared byte-exact by the
// frozen tests.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, `{"error":"encoding failure"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set(contentTypeHeader, contentTypeJSON)
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
