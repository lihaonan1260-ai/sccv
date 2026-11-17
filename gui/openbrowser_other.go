//go:build !windows

package gui

// openBrowser is a no-op off Windows: the GUI product targets Windows
// (RD-003) and the linux/darwin CI matrices only need this side to compile
// (DEC-018).
func openBrowser(url string) error {
	return nil
}
