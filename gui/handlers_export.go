package gui

// Export endpoints (T-032, DEC-012): both download the most recently
// completed analysis snapshot — JSON as the §0.2-4 isomorphic document, CSV
// in the scc --format csv machine shape (DEC-211). Without a completed
// snapshot there is nothing to export and the frozen answer is 409 with
// {"error":"no snapshot"} (TC-064).

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// handleExportJSON serves the JSON attachment sccv-export.json (Q5).
func (s *server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	s.serveExport(w, r, exportJSONFilename, contentTypeJSON, renderExportJSON)
}

// handleExportCSV serves the CSV attachment sccv-export.csv (Q6): UTF-8 with
// BOM and the golden column order; the file detail is a JSON-only concern, so
// the CSV renderer ignores the includeFiles flag.
func (s *server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	s.serveExport(w, r, exportCSVFilename, contentTypeCSV,
		func(view *snapshotView, includeFiles bool) ([]byte, error) {
			return renderExportCSV(view)
		})
}

// serveExport is the shared download path: guard the snapshot state, parse
// the optional body, render fully before touching the response (a failed
// render must never leave a half-written attachment), then emit the exact
// frozen headers (test plan §0.2-5: unquoted attachment filename) and bytes.
func (s *server) serveExport(w http.ResponseWriter, r *http.Request, filename, contentType string, render func(*snapshotView, bool) ([]byte, error)) {
	view, ok := s.latestSnapshotView()
	if !ok {
		writeError(w, http.StatusConflict, "no snapshot")
		return
	}
	includeFiles, err := parseExportRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	body, err := render(view, includeFiles)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "export rendering failure")
		return
	}
	header := w.Header()
	header.Set(contentTypeHeader, contentType)
	header.Set(contentDispositionHeader, "attachment; filename="+filename)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// parseExportRequest reads the optional JSON body of an export request; an
// empty body is the zero request. Bodies beyond exportBodyLimit are rejected
// unread (fail fast).
func parseExportRequest(r *http.Request) (bool, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, exportBodyLimit))
	if err != nil {
		return false, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return false, nil
	}
	var req exportRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return false, err
	}
	return req.IncludeFiles, nil
}
