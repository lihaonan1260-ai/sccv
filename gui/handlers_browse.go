package gui

import (
	"errors"
	"fmt"
	"net/http"
	"os"
)

// Fixed user-facing wordings of /api/browse: the Windows infrastructure
// failure is a 500, the unsupported platform is a 501 seam answer (matching
// the T-003 stub semantics). Response bodies stay stable regardless of the
// underlying cause, whose detail goes to stderr instead.
const (
	browseUnavailableMessage = "系统无法打开目录选择对话框，请手动输入路径"
	browseUnsupportedMessage = "目录对话框仅 Windows 支持，请手动输入路径"
)

// errBrowseUnsupported marks the platform seam: off Windows there is no
// native folder dialog (K6). Defined platform-neutral so both halves of the
// seam stay comparable through errors.Is.
var errBrowseUnsupported = errors.New("directory dialog is only supported on windows")

// browseResult is the outcome of one native directory picker attempt: either
// a filesystem path chosen by the user or a clean cancel.
type browseResult struct {
	Path     string
	Canceled bool
}

// browsePayload is the /api/browse response body (implementation plan §5):
// {"path":"D:\\x"} on success, {"path":null,"canceled":true} on cancel.
type browsePayload struct {
	Path     *string `json:"path"`
	Canceled bool    `json:"canceled,omitempty"`
}

// handleBrowse serves POST /api/browse (K6): it opens the native folder
// dialog through the platform seam and reports the picked path. The token
// gate already covered this route (T-003); the request has no body by
// contract. Canceling is a success-shaped answer, not an error. Failures map
// to two stable answers — 501 off Windows, 500 for dialog infrastructure on
// Windows — that steer the user to the manual path input; the underlying
// detail goes to stderr, keeping stdout to the single R3 line.
func (s *server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	result, err := pickDirectory()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sccv: browse: %v\n", err)
		if errors.Is(err, errBrowseUnsupported) {
			writeError(w, http.StatusNotImplemented, browseUnsupportedMessage)
			return
		}
		writeError(w, http.StatusInternalServerError, browseUnavailableMessage)
		return
	}
	if result.Canceled {
		writeJSON(w, http.StatusOK, browsePayload{Canceled: true})
		return
	}
	writeJSON(w, http.StatusOK, browsePayload{Path: &result.Path})
}
