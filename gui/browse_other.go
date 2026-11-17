//go:build !windows

package gui

// pickDirectory is the non-Windows half of the /api/browse seam: it always
// fails with errBrowseUnsupported, and the handler owns the user-facing 501
// wording. This side exists to compile for the linux/darwin CI matrices only
// (DEC-018).
func pickDirectory() (browseResult, error) {
	return browseResult{}, errBrowseUnsupported
}
