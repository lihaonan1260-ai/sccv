package gui

// HTTP layer of the report job (T-033): POST /api/report validates a request,
// claims the shared R2 job slot and answers 202; GET /api/download/{jobId}
// streams the finished report from processor.RenderReportTo — fully
// in-memory, zero temp files (DEC-025/DEC-306). The job state machine and the
// flag discipline live in report.go.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/boyter/scc/v4/processor"
)

// handleReport validates the request, claims the single job slot and answers
// 202 with the job id; the report itself runs on its own goroutine.
// Validation happens before the TryLock so a bad path fails 400 without
// touching the slot — the same order handleAnalyze uses (DEC-305).
func (s *server) handleReport(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeReportRequest(w, r)
	if !ok {
		return
	}
	absPath, err := normalizeAnalysisPath(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	extensions, err := resolveLanguageAllowList(req.Languages)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !analyzeMu.TryLock() {
		writeError(w, http.StatusConflict, "busy")
		return
	}
	job := startReportJob(absPath, req.toOptions(absPath), extensions)
	writeJSON(w, http.StatusAccepted, jobAcceptPayload{JobID: job.id, Status: string(jobStateRunning)})
}

// decodeReportRequest reads the JSON body under the same 1MB cap as analyze
// (§7). Unset booleans carry the contract defaults: includeUnknown false
// (DEC-016) and includeHistory false (DEC-013).
func decodeReportRequest(w http.ResponseWriter, r *http.Request) (reportRequest, bool) {
	var req reportRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxAnalysisBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "请求体超过 1MB 上限")
			return req, false
		}
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return req, false
	}
	return req, true
}

// handleDownload streams the finished report of a done report job into the
// response body. Running, errored, superseded and unknown ids all answer 404 —
// the contract's way of saying "nothing to download here" (§5 row 10).
func (s *server) handleDownload(w http.ResponseWriter, r *http.Request) {
	data, ok := doneReportData(strings.TrimPrefix(r.URL.Path, downloadPathPrefix))
	if !ok {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	// Render before touching the headers so a render failure surfaces as a
	// clean 500 instead of a truncated attachment. The bytes never touch disk
	// (DEC-025).
	var report bytes.Buffer
	if err := processor.RenderReportTo(&report, *data); err != nil {
		writeError(w, http.StatusInternalServerError, "报告渲染失败: "+err.Error())
		return
	}
	w.Header().Set(contentTypeHeader, "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", reportAttachmentHeader)
	_, _ = w.Write(report.Bytes())
}
