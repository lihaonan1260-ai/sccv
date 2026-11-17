package gui

// POST /api/extensions (OPT-002, T-202): the lightweight extension inventory
// behind the wizard's second step. WalkDir metadata only — file contents are
// never read — bounded depth (6) and file budget (50000), the CLI's default
// exclusion trio skipped, symlink entries never counted. Like /api/tree it
// holds no R2 slot and reuses normalizeAnalysisPath (交接协议 Notes-3).
//
// Extension extraction mirrors what the engine will match against: dot-less
// lowercase last-dot suffix. Files with no dot, dot-leading names (.gitignore
// style, where the engine's notion of "extension" is the whole name and can
// never match a dot-less list entry) and trailing-dot names are counted as
// extension-less: they are not listed, because no allow/exclude selection can
// ever reach them (DEC-O05, UI 注记口径).

import (
	"cmp"
	"io/fs"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
)

// extensionScanBounds: BG-006 revised guardrails. Depth is effectively
// unlimited (the previous cap of 6 silently dropped every file below
// mod/src/main/java/... -- exactly the code users come to count); the 50k
// file budget stays as the runaway guard and the UI note for it is honest.
const (
	maxExtensionScanDepth = 1_000_000
	maxExtensionScanFiles = 50000
)

// extensionsRequest is the POST /api/extensions body.
type extensionsRequest struct {
	Path string `json:"path"`
}

// extensionEntry is one listed extension and its file count.
type extensionEntry struct {
	Ext   string `json:"ext"`
	Files int64  `json:"files"`
}

// extensionsPayload is the POST /api/extensions response. Scanned counts
// every regular file examined (capped), extensionless the subset no type
// selection can ever affect.
type extensionsPayload struct {
	Extensions    []extensionEntry `json:"extensions"`
	Truncated     bool             `json:"truncated"`
	Scanned       int64            `json:"scanned"`
	Extensionless int64            `json:"extensionless"`
}

// handleExtensions answers POST /api/extensions. Validation failures answer
// the shared 400 copy; unreadable subtrees degrade to a skip, so only a
// vanished root can produce a 500.
func (s *server) handleExtensions(w http.ResponseWriter, r *http.Request) {
	var req extensionsRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	root, err := normalizeAnalysisPath(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	payload, err := scanExtensions(root, maxExtensionScanDepth, maxExtensionScanFiles)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "文件类型盘点失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// scanExtensions inventories the extensions under root without reading any
// file content. Depth and file caps are parameters so the tests can exercise
// the budget paths on tiny fixtures.
func scanExtensions(root string, maxDepth, maxFiles int) (extensionsPayload, error) {
	payload := extensionsPayload{Extensions: []extensionEntry{}}
	counts := map[string]int64{}
	sep := string(filepath.Separator)

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fs.SkipDir
		}
		if d.IsDir() {
			if p == root {
				return nil
			}
			if treeSkipDirs[d.Name()] {
				return fs.SkipDir
			}
			// Depth cut: a directory at relative depth maxDepth (rel holds
			// maxDepth-1 separators) is entered no deeper, so the deepest
			// counted files sit maxDepth levels below the root.
			rel, relErr := filepath.Rel(root, p)
			if relErr == nil && strings.Count(rel, sep) >= maxDepth-1 {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // symlinks/devices are never counted
		}
		if payload.Scanned >= int64(maxFiles) {
			payload.Truncated = true
			return fs.SkipAll
		}
		payload.Scanned++
		name := d.Name()
		dot := strings.LastIndex(name, ".")
		if dot <= 0 { // no dot at all, or dot-leading hidden name
			payload.Extensionless++
			return nil
		}
		ext := strings.ToLower(name[dot+1:])
		if ext == "" { // trailing dot: no selectable extension
			payload.Extensionless++
			return nil
		}
		counts[ext]++
		return nil
	})
	if err != nil {
		return payload, err
	}
	for ext, files := range counts {
		payload.Extensions = append(payload.Extensions, extensionEntry{Ext: ext, Files: files})
	}
	slices.SortFunc(payload.Extensions, func(a, b extensionEntry) int {
		if c := cmp.Compare(b.Files, a.Files); c != 0 {
			return c // files 降序
		}
		return strings.Compare(a.Ext, b.Ext) // 同值扩展名字典序
	})
	return payload, nil
}
