//go:build !windows

package main

// showFatalError is a no-op on non-Windows platforms (stderr is visible there).
func showFatalError(message string) {}
