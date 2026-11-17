//go:build windows

package gui

import (
	"os/exec"
	"syscall"
)

// openBrowser asks the shell to open the URL through rundll32, a system
// component, so no extra dependency is involved. HideWindow keeps the helper
// from flashing a console over the windowsgui binary (RD-003); the call is
// fire-and-forget: the browser outlives the server on purpose. Whether the
// launch is suppressed at all is the caller's decision (DEC-204,
// SCCV_NO_BROWSER).
func openBrowser(url string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}
