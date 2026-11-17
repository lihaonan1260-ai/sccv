// SPDX-License-Identifier: MIT

//go:build !windows

package main

// setupConsole is a no-op off Windows: POSIX binaries inherit working
// standard handles from their parent, so there is no console to attach to and
// nothing a redirected pipe or file could lose.
func setupConsole() {}
