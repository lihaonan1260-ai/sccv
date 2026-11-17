package gui

// POST /api/tree (OPT-001, T-201): the lightweight directory listing behind
// the wizard's first step. WalkDir metadata only — file contents are never
// read — bounded depth and directory budget, the CLI's default exclusion
// trio skipped, symlink entries never followed. The endpoint holds no R2
// slot (nothing here touches the processor globals the single job slot
// guards) and reuses normalizeAnalysisPath so the UNC/device-path policy is
// inherited unchanged (交接协议 Notes-3).

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// treeScanBounds are the BG-005 revised guardrails: the wizard tree shows
// every directory regardless of depth (depth is effectively unlimited) with
// a generous 100k-directory floor kept only as a runaway guard against
// pathological filesystems; hitting it sets the truncated flag (一期不支持
// 子目录重扫, P2). The previous 8-level / 4000-dir caps starved real code
// directories that sort alphabetically after node_modules subtrees.
const (
	maxTreeDepth = 1_000_000
	maxTreeDirs  = 100_000
)

// treeSkipDirs is the trio the engine also skips by default; invisible to
// the wizard means unselectable, which keeps them excluded exactly as the
// CLI default does.
var treeSkipDirs = map[string]bool{".git": true, ".hg": true, ".svn": true}

// treeRequest is the POST /api/tree body.
type treeRequest struct {
	Path string `json:"path"`
}

// treeNode is one directory of the wizard tree. Children arrive fully
// populated (pre-scanned); the front end decides what to render lazily.
type treeNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	Children []*treeNode `json:"children"`
}

// treePayload is the POST /api/tree response: root plus its direct children.
type treePayload struct {
	Root      string      `json:"root"`
	Nodes     []*treeNode `json:"nodes"`
	Truncated bool        `json:"truncated"`
}

// treeBuilder assembles the node tree during the WalkDir pre-order and
// carries the budget counters.
type treeBuilder struct {
	nodesByPath map[string]*treeNode
	top         []*treeNode
	dirCount    int
	truncated   bool
}

// handleTree answers POST /api/tree. Validation failures answer the shared
// 400 copy; a scan that fails midway degrades to skipping the unreadable
// subtree, so only a vanished root can produce a 500.
func (s *server) handleTree(w http.ResponseWriter, r *http.Request) {
	var req treeRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	root, err := normalizeAnalysisPath(req.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	nodes, truncated, err := scanDirectoryTree(root, maxTreeDepth, maxTreeDirs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "目录树读取失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, treePayload{Root: root, Nodes: nodes, Truncated: truncated})
}

// scanDirectoryTree walks the directory metadata under root and returns the
// top-level nodes plus the truncated marker. Depth and directory caps are
// parameters so the tests can exercise the budget paths on tiny fixtures.
func scanDirectoryTree(root string, maxDepth, maxDirs int) ([]*treeNode, bool, error) {
	builder := &treeBuilder{nodesByPath: map[string]*treeNode{}}
	rootNode := &treeNode{Name: filepath.Base(root), Path: root, Children: []*treeNode{}}
	builder.nodesByPath[root] = rootNode

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree degrades to a skip: the engine walk would
			// count nothing in it either, so the wizard shows the same scope.
			return fs.SkipDir
		}
		if !d.IsDir() {
			return nil // files are not listed; symlinked dirs fail IsDir (Lstat) and are never entered
		}
		if p == root {
			return nil
		}
		if treeSkipDirs[d.Name()] {
			return fs.SkipDir
		}
		if builder.dirCount >= maxDirs {
			builder.truncated = true
			return fs.SkipDir
		}
		parent, ok := builder.nodesByPath[filepath.Dir(p)]
		if !ok {
			// WalkDir guarantees parents first; an orphaned entry means the
			// parent sat in a skipped subtree, so skip the branch wholesale.
			return fs.SkipDir
		}
		node := &treeNode{Name: d.Name(), Path: p, Children: []*treeNode{}}
		parent.Children = append(parent.Children, node)
		builder.nodesByPath[p] = node
		builder.dirCount++
		if dirDepth(root, p) >= maxDepth {
			if hasVisibleSubDir(p) {
				builder.truncated = true
			}
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return rootNode.Children, builder.truncated, nil
}

// dirDepth returns the directory's depth relative to root (root's direct
// children are depth 1).
func dirDepth(root, dir string) int {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return 0
	}
	depth := 0
	for _, part := range splitPath(rel) {
		if part == "" || part == "." {
			continue
		}
		depth++
	}
	return depth
}

// splitPath splits on the platform separator.
func splitPath(p string) []string {
	return strings.Split(p, string(filepath.Separator))
}

// hasVisibleSubDir reports whether dir contains a listable subdirectory, so
// a depth-cut can set the truncated marker honestly instead of on every
// boundary directory.
func hasVisibleSubDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && !treeSkipDirs[e.Name()] {
			return true
		}
	}
	return false
}
