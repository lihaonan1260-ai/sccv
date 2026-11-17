//go:build windows

package gui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
)

// browseDialogTimeout bounds how long the native dialog may stay open. The
// browse flow holds no analysis slot (R2), so this is pure resource
// protection against a dialog abandoned forever, not a UX deadline.
const browseDialogTimeout = 120 * time.Second

// browseScript drives the Windows shell folder dialog through the
// Shell.Application COM object. The dialog title is Chinese (F2 product
// language). Any infrastructure failure — COM unavailable, COM method error
// — exits non-zero with the reason on stderr, so the Go side never has to
// guess. A cancel and a selection without a filesystem path (virtual
// locations such as "This PC") both exit 0 without output.
//
// Foreground raise (BG-003): a windowsgui process has no console host to
// hand the foreground right down to the dialog, so the folder picker used
// to open behind the browser and unfocused. Before BrowseForFolder the
// script arms a System.Windows.Forms.Timer — the STA modal loop of the
// dialog pumps WM_TIMER, so the tick runs as soon as the dialog exists.
// The first tick that finds a #32770 owned by this process stops the timer
// and applies the raise sequence. The SHOW step is load-bearing, not
// cosmetic: when the PowerShell child is launched console-less
// (SysProcAttr HideWindow), the shell skips its own ShowWindow and the
// dialog sits created-but-invisible forever (observed 2026-10-31: same
// script visible in a console-attached run, invisible under HideWindow) —
// so the tick shows it first (SW_SHOWNA, no activation), then applies
// HWND_TOPMOST for visibility and the AttachThreadInput bridge around
// BringWindowToTop + SetForegroundWindow for focus. The timer also stops
// in a finally when BrowseForFolder returns, so no timer survives the
// dialog.
const browseScript = `$ErrorActionPreference = 'Stop'
try {
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
    Add-Type -AssemblyName System.Windows.Forms
    Add-Type -TypeDefinition @"
using System;
using System.Text;
using System.Runtime.InteropServices;
using System.Diagnostics;
public static class SccvForeground {
    [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetClassName(IntPtr h, StringBuilder sb, int max);
    [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
    [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int cmd);
    [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint pid);
    [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
    [DllImport("user32.dll")] public static extern bool AttachThreadInput(uint a, uint b, bool attach);
    [DllImport("user32.dll")] public static extern bool BringWindowToTop(IntPtr h);
    [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr h, IntPtr after, int x, int y, int cx, int cy, uint flags);
    public delegate bool EnumProc(IntPtr h, IntPtr l);
    [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr l);
    public static IntPtr FindOwnDialog() {
        IntPtr found = IntPtr.Zero; uint me = (uint)Process.GetCurrentProcess().Id;
        EnumWindows(delegate(IntPtr h, IntPtr l) {
            uint p; GetWindowThreadProcessId(h, out p);
            if (p == me) {
                var sb = new StringBuilder(64); GetClassName(h, sb, 64);
                if (sb.ToString() == "#32770") { found = h; return false; }
            }
            return true;
        }, IntPtr.Zero);
        return found;
    }
}
"@
    $script:sccvRaised = $false
    $script:sccvTimer = New-Object System.Windows.Forms.Timer
    $script:sccvTimer.Interval = 200
    $script:sccvTimer.Add_Tick({
        if ($script:sccvRaised) { return }
        $dlg = [SccvForeground]::FindOwnDialog()
        if ($dlg -eq [IntPtr]::Zero) { return }
        $script:sccvRaised = $true
        $script:sccvTimer.Stop()
        if (-not [SccvForeground]::IsWindowVisible($dlg)) {
            [void][SccvForeground]::ShowWindow($dlg, 8) # SW_SHOWNA: show without stealing activation
        }
        [void][SccvForeground]::SetWindowPos($dlg, [IntPtr](-1), 0, 0, 0, 0, 0x3)
        $fg = [SccvForeground]::GetForegroundWindow()
        $fgPid = 0
        $fgThread = [SccvForeground]::GetWindowThreadProcessId($fg, [ref]$fgPid)
        $dlgPid = 0
        $dlgThread = [SccvForeground]::GetWindowThreadProcessId($dlg, [ref]$dlgPid)
        if ([SccvForeground]::AttachThreadInput($dlgThread, $fgThread, $true)) {
            [void][SccvForeground]::BringWindowToTop($dlg)
            [void][SccvForeground]::SetForegroundWindow($dlg)
            [void][SccvForeground]::AttachThreadInput($dlgThread, $fgThread, $false)
        }
    })
    $script:sccvTimer.Start()
    try {
        $folder = (New-Object -ComObject Shell.Application).BrowseForFolder(0, '选择要分析的文件夹', 0)
    } finally {
        $script:sccvTimer.Stop()
    }
    if ($null -eq $folder) { exit 0 }
    $path = $folder.Self.Path
    if ([string]::IsNullOrEmpty($path)) { exit 0 }
    [Console]::Out.Write($path)
} catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
}`

// pickDirectory opens the native folder dialog and returns the user's
// choice. The PowerShell child carries SysProcAttr{HideWindow: true} (K6
// hard requirement): powershell.exe is a console application and would
// otherwise flash a console window over the windowsgui binary on every open.
//
// DEC-304 discipline: user-controlled strings must never be concatenated
// into the PowerShell command line. This endpoint has no body and the
// script is a compile-time constant, so nothing user-controlled reaches
// PowerShell here. The script travels through -EncodedCommand (Base64
// UTF-16LE), which is immune to the console code page and keeps the Chinese
// title intact on any locale. The P2 "initial directory" evolution must push
// its user-controlled value through stdin (`powershell ... -Command -`),
// never by appending arguments — the launcher below is the single place to
// extend.
func pickDirectory() (browseResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), browseDialogTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell",
		"-NoProfile", "-STA", "-NonInteractive", "-EncodedCommand", encodePowerShellCommand(browseScript))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return browseResult{}, fmt.Errorf("directory dialog timed out after %s", browseDialogTimeout)
		}
		return browseResult{}, browseChildError(err, stderr.String())
	}
	return parseBrowseOutput(stdout.String(), nil)
}

// browseChildError turns a failed PowerShell run into one bounded single
// line error that carries the child's own diagnosis (fail fast with
// context), keeping stderr summaries out of the JSON response.
func browseChildError(runErr error, stderr string) error {
	var plain []string
	for _, l := range strings.Split(stderr, "\n") {
		l = strings.TrimSpace(l)
		// Skip the PowerShell CLIXML envelope ("#< CLIXML" and the XML
		// records that follow it) so the summary keeps the plain text the
		// script itself wrote via [Console]::Error.WriteLine.
		if l == "" || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "<") {
			continue
		}
		plain = append(plain, l)
	}
	summary := strings.Join(plain, "; ")
	const maxStderrSummary = 256
	if len(summary) > maxStderrSummary {
		summary = summary[:maxStderrSummary]
	}
	if summary == "" {
		return fmt.Errorf("directory dialog process failed: %w", runErr)
	}
	return fmt.Errorf("directory dialog process failed: %w: %s", runErr, summary)
}

// parseBrowseOutput classifies the PowerShell outcome into a browseResult:
// a filesystem path, a cancel (empty output, exit 0) or an error (child
// failure, multi-line chatter, or a virtual location that has no filesystem
// path). Kept pure and child-free so every shape is unit-testable.
func parseBrowseOutput(out string, runErr error) (browseResult, error) {
	if runErr != nil {
		return browseResult{}, runErr
	}
	text := strings.TrimSpace(out)
	if strings.ContainsAny(text, "\r\n") {
		return browseResult{}, errors.New("directory dialog produced unexpected output")
	}
	path := strings.Trim(text, `"`)
	if path == "" {
		return browseResult{Canceled: true}, nil
	}
	if !looksLikeVolumePath(path) {
		return browseResult{}, fmt.Errorf("directory dialog returned a non filesystem location: %s", path)
	}
	return browseResult{Path: filepath.Clean(path)}, nil
}

// looksLikeVolumePath reports whether s is rooted at a filesystem volume: a
// drive letter (C:\x) or a UNC share (\\server\share). The shell can also
// hand back virtual locations ("::{clsid}" for "This PC" and friends);
// filepath.VolumeName is not enough to reject those, because a leading "::"
// satisfies its colon check too.
func looksLikeVolumePath(s string) bool {
	if len(s) >= 2 && s[1] == ':' {
		drive := s[0]
		return (drive >= 'A' && drive <= 'Z') || (drive >= 'a' && drive <= 'z')
	}
	return strings.HasPrefix(s, `\\`)
}

// encodePowerShellCommand wraps the script the way -EncodedCommand expects:
// Base64 over UTF-16LE, independent of the console code page.
func encodePowerShellCommand(script string) string {
	units := utf16.Encode([]rune(script))
	raw := make([]byte, 0, len(units)*2)
	for _, unit := range units {
		raw = binary.LittleEndian.AppendUint16(raw, unit)
	}
	return base64.StdEncoding.EncodeToString(raw)
}
