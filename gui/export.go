package gui

// Export serialization for the two attachment endpoints (T-032, DEC-012):
// both render the most recently completed analysis snapshot and never touch
// live processor state (R1/R2 — an export only reads, it never triggers
// statistics).
//
// The JSON document is isomorphic to the frozen snapshot schema (test plan
// §0.2-4), so the types below cannot carry a ULOC field (R5, by
// construction). The CSV mirrors the scc --format csv machine format
// (DEC-211/DEC-022): UTF-8 with BOM, the golden header order, one row per
// language; the snapshot carries no ULOC, so that column is always 0
// (test plan §1.4).

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// csvByteOrderMark prefixes every CSV download (Q6/TC-060): three raw bytes
// so spreadsheet tools detect UTF-8 without relying on response headers.
var csvByteOrderMark = []byte{0xEF, 0xBB, 0xBF}

// exportCSVHeader is the golden column order of scc --format csv (DEC-211);
// the frozen export tests compare it byte for byte.
const exportCSVHeader = "Language,Lines,Code,Comments,Blanks,Complexity,Bytes,Files,ULOC"

// contentTypeCSV declares the CSV download type; the charset is explicit
// because the body is UTF-8 with BOM.
const contentTypeCSV = "text/csv; charset=utf-8"

// contentDispositionHeader carries the attachment name on both endpoints.
const contentDispositionHeader = "Content-Disposition"

// Attachment filenames of the export contract (Q5/Q6, test plan §0.2-5).
const (
	exportJSONFilename = "sccv-export.json"
	exportCSVFilename  = "sccv-export.csv"
)

// exportBodyLimit caps the export request body: the only field is a boolean,
// so anything larger is garbage and refusing it early keeps the guard cheap.
const exportBodyLimit = 4 << 10

// exportRequest is the optional POST body of both endpoints; an empty body
// means the zero request (summary only, no file detail).
type exportRequest struct {
	IncludeFiles bool `json:"includeFiles"`
}

// exportTotals is the seven-number summary shared by the totals block and
// every language row (implementation plan §5 / test plan §0.2-4). The key
// order and names are frozen by the oracle tests.
type exportTotals struct {
	Files      int64 `json:"files"`
	Lines      int64 `json:"lines"`
	Code       int64 `json:"code"`
	Comment    int64 `json:"comment"`
	Blank      int64 `json:"blank"`
	Complexity int64 `json:"complexity"`
	Bytes      int64 `json:"bytes"`
}

// exportLanguageRow is one language of the snapshot summary: the name plus
// the seven numbers, nothing else (§0.2-4 isomorphism, R5).
type exportLanguageRow struct {
	Name string `json:"name"`
	exportTotals
}

// exportFileRow is one file of the snapshot detail: identity plus the six
// per-file numbers (a single file carries no file count, §0.2-4). Path and
// name are scanned repository content — untrusted data for any consumer that
// re-serializes them (the CSV path never includes file rows; encoding/csv
// quotes hostile delimiters in the JSON-free CSV fields).
type exportFileRow struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Language   string `json:"language"`
	Lines      int64  `json:"lines"`
	Code       int64  `json:"code"`
	Comment    int64  `json:"comment"`
	Blank      int64  `json:"blank"`
	Complexity int64  `json:"complexity"`
	Bytes      int64  `json:"bytes"`
}

// snapshotView is the export-facing projection of the most recently completed
// analysis snapshot (§0.2-4). The shape is fixed by the type, so an export
// cannot leak fields the snapshot does not carry (R5: no ULOC, by
// construction).
type snapshotView struct {
	JobID       string              `json:"jobId"`
	Type        string              `json:"type"`
	RootPath    string              `json:"rootPath"`
	GeneratedAt string              `json:"generatedAt"`
	DurationMs  int64               `json:"durationMs"`
	Options     json.RawMessage     `json:"options"`
	Totals      exportTotals        `json:"totals"`
	Languages   []exportLanguageRow `json:"languages"`
	Files       []exportFileRow     `json:"files"`
}

// newSnapshotView decodes the analyzer's stored snapshot into the export
// projection. Any value that marshals to the §0.2-4 snapshot JSON is
// accepted, which keeps this seam independent of the analyzer's Go types
// (T-004, analyze.go). Decode failures fail fast: a snapshot that cannot be
// represented must not degrade into a silently truncated export.
func newSnapshotView(snapshot any) (*snapshotView, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	var view snapshotView
	if err := json.Unmarshal(raw, &view); err != nil {
		return nil, err
	}
	return &view, nil
}

// exportDocument is the JSON attachment body: the snapshot itself (§0.2-5
// isomorphism) with the file detail attached only when requested (Q5). With
// includeFiles unset the files key is dropped entirely, so the summary export
// cannot carry a non-empty files array.
type exportDocument struct {
	JobID       string              `json:"jobId"`
	Type        string              `json:"type"`
	RootPath    string              `json:"rootPath"`
	GeneratedAt string              `json:"generatedAt"`
	DurationMs  int64               `json:"durationMs"`
	Options     json.RawMessage     `json:"options"`
	Totals      exportTotals        `json:"totals"`
	Languages   []exportLanguageRow `json:"languages"`
	Files       []exportFileRow     `json:"files,omitempty"`
}

// renderExportJSON serializes the snapshot as the JSON attachment body.
func renderExportJSON(view *snapshotView, includeFiles bool) ([]byte, error) {
	doc := exportDocument{
		JobID:       view.JobID,
		Type:        view.Type,
		RootPath:    view.RootPath,
		GeneratedAt: view.GeneratedAt,
		DurationMs:  view.DurationMs,
		Options:     view.Options,
		Totals:      view.Totals,
		Languages:   view.Languages,
	}
	if includeFiles {
		doc.Files = view.Files
	}
	return json.Marshal(doc)
}

// renderExportCSV serializes the language summary in the scc --format csv
// shape (DEC-211): BOM, golden header, one row per language ordered by name,
// the ULOC column pinned to 0 because the snapshot carries no ULOC (R5, test
// plan §1.4). encoding/csv applies RFC 4180 quoting, so a value containing
// commas, quotes or newlines cannot break the row shape (CSV injection
// guard). The input view is never mutated.
func renderExportCSV(view *snapshotView) ([]byte, error) {
	rows := slices.Clone(view.Languages)
	slices.SortFunc(rows, func(a, b exportLanguageRow) int {
		return strings.Compare(a.Name, b.Name)
	})

	buf := &bytes.Buffer{}
	buf.Write(csvByteOrderMark)
	w := csv.NewWriter(buf)
	if err := w.Write(strings.Split(exportCSVHeader, ",")); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if err := w.Write([]string{
			row.Name,
			strconv.FormatInt(row.Lines, 10),
			strconv.FormatInt(row.Code, 10),
			strconv.FormatInt(row.Comment, 10),
			strconv.FormatInt(row.Blank, 10),
			strconv.FormatInt(row.Complexity, 10),
			strconv.FormatInt(row.Bytes, 10),
			strconv.FormatInt(row.Files, 10),
			"0",
		}); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// latestSnapshotView returns the export projection of the most recently
// completed analysis snapshot accepted by this instance, or false when none
// has completed yet — the frozen 409 state (TC-064: an instance that never
// analyzed has nothing to export, even if a sibling instance has). Exports
// only ever read a finished snapshot; they never start or wait for one (R2).
// The analyzer publishes the immutable per-instance snapshot (T-004,
// analyze.go); newSnapshotView stays the single projection point so the
// export types cannot grow fields the snapshot does not carry.
func (s *server) latestSnapshotView() (*snapshotView, bool) {
	stored, ok := latestSnapshotFor(s)
	if !ok {
		return nil, false
	}
	view, err := newSnapshotView(stored)
	if err != nil {
		// A published snapshot that cannot be represented is a defect, not an
		// exportable state: fail fast instead of emitting a truncated file.
		return nil, false
	}
	return view, true
}
