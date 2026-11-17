// SPDX-License-Identifier: MIT

//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// Console plumbing for the CLI branch of a windowsgui-subsystem binary
// (DEC-103). The release build hides the console window (RD-003: no flash on
// double-click), so when sccv runs as a CLI from an existing terminal it
// starts detached from that terminal with dead standard handles; the only way
// to reach the caller's console is AttachConsole(ATTACH_PARENT_PROCESS)
// followed by reopening CONIN$ and CONOUT$ and pointing the standard handles
// at them. All three handles are covered: stdin from CONIN$, stdout and
// stderr from CONOUT$.
//
// Everything here is best effort and fails silently by design: spawned with
// redirected pipes there is no parent console to attach to at all, which is
// the normal scripting case, so every failure degrades to whatever handles
// the caller already wired up and must never abort the CLI run.

const (
	// ATTACH_PARENT_PROCESS, the AttachConsole dwFlags value.
	attachParentProcess = 0x00000400

	// nStdHandle codes of GetStdHandle/SetStdHandle, kept as the DWORD bit
	// patterns they travel in (signed -10/-11/-12).
	stdInputHandle  = 0xFFFFFFF6 // (DWORD)-10
	stdOutputHandle = 0xFFFFFFF5 // (DWORD)-11
	stdErrorHandle  = 0xFFFFFFF4 // (DWORD)-12

	// CreateFileW arguments for reopening the console devices: GENERIC_READ
	// and GENERIC_WRITE, FILE_SHARE_READ and FILE_SHARE_WRITE, OPEN_EXISTING.
	consoleAccess    = 0xC0000000
	consoleShareMode = 0x00000003
	openExisting     = 0x00000003

	// GetFileType kinds that mean "a redirection made by the caller".
	fileTypeDisk = 0x0001
	fileTypePipe = 0x0003
)

var (
	modkernel32        = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole  = modkernel32.NewProc("AttachConsole")
	procCreateFileW    = modkernel32.NewProc("CreateFileW")
	procGetConsoleMode = modkernel32.NewProc("GetConsoleMode")
	procGetFileType    = modkernel32.NewProc("GetFileType")
	procGetStdHandle   = modkernel32.NewProc("GetStdHandle")
	procSetStdHandle   = modkernel32.NewProc("SetStdHandle")
)

// setupConsole reattaches the CLI to the console of the parent process and
// replaces every dead standard handle with a fresh console handle. Handles
// the caller redirected to a pipe or file are live and stay untouched:
// overwriting them would silently drop the redirected stream.
func setupConsole() {
	if attached, _, _ := procAttachConsole.Call(attachParentProcess); attached == 0 {
		// No parent console (mintty pipes, detached spawn) or already
		// attached: whatever the caller set up keeps working.
		return
	}

	conin, conout, ok := reopenConsoleHandles()
	if !ok {
		return
	}

	// Replace each dead slot at both layers: the process handle table via
	// SetStdHandle (what spawned children inherit) and the Go wrappers in
	// os/syscall (what fmt, cobra and the processor resolve at call time —
	// they captured the dead startup handles).
	if !stdHandleUsable(stdInputHandle) {
		procSetStdHandle.Call(stdInputHandle, conin)
		syscall.Stdin = syscall.Handle(conin)
		os.Stdin = os.NewFile(conin, "/dev/stdin")
	}
	if !stdHandleUsable(stdOutputHandle) {
		procSetStdHandle.Call(stdOutputHandle, conout)
		syscall.Stdout = syscall.Handle(conout)
		os.Stdout = os.NewFile(conout, "/dev/stdout")
	}
	if !stdHandleUsable(stdErrorHandle) {
		procSetStdHandle.Call(stdErrorHandle, conout)
		syscall.Stderr = syscall.Handle(conout)
		os.Stderr = os.NewFile(conout, "/dev/stderr")
	}
}

// reopenConsoleHandles opens fresh handles for the attached console. A single
// failure degrades the whole call: half-bound handles would only move the
// confusion elsewhere.
func reopenConsoleHandles() (conin, conout uintptr, ok bool) {
	conin, ok = openConsoleDevice("CONIN$")
	if !ok {
		return 0, 0, false
	}
	conout, ok = openConsoleDevice("CONOUT$")
	if !ok {
		return 0, 0, false
	}
	return conin, conout, true
}

// openConsoleDevice opens CONIN$ or CONOUT$ through CreateFileW.
func openConsoleDevice(name string) (uintptr, bool) {
	ptr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, false
	}
	handle, _, _ := procCreateFileW.Call(
		uintptr(unsafe.Pointer(ptr)),
		consoleAccess,
		consoleShareMode,
		0,
		openExisting,
		0,
		0,
	)
	if handle == 0 || handle == uintptr(syscall.InvalidHandle) {
		return 0, false
	}
	return handle, true
}

// stdHandleUsable reports whether the standard handle in nStdHandle's slot is
// one the process can use right now: a pipe or file the caller redirected to,
// or a console handle of the console this process is attached to. Unset and
// INVALID_HANDLE_VALUE slots, plus console handles inherited from a console
// this windowsgui process never belonged to, are dead and need replacing.
func stdHandleUsable(nStdHandle uintptr) bool {
	handle, _, _ := procGetStdHandle.Call(nStdHandle)
	if handle == 0 || handle == uintptr(syscall.InvalidHandle) {
		return false
	}
	switch kind, _, _ := procGetFileType.Call(handle); kind {
	case fileTypeDisk, fileTypePipe:
		return true
	default:
		// FILE_TYPE_CHAR: a console handle, usable only if its console is the
		// one we are attached to (GetConsoleMode succeeds exactly then).
		mode, _, _ := procGetConsoleMode.Call(handle)
		return mode != 0
	}
}
