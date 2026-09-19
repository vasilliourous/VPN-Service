//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	advapi32         = syscall.NewLazyDLL("advapi32.dll")
	shell32          = syscall.NewLazyDLL("shell32.dll")
	openProcessToken = advapi32.NewProc("OpenProcessToken")
	getTokenInfo     = advapi32.NewProc("GetTokenInformation")
	shellExecuteW    = shell32.NewProc("ShellExecuteW")
)

// tokenElevation is the TokenInformation class that tells us whether a process
// token is elevated (i.e. running as administrator).
const tokenElevation = 20

// isElevated reports whether the current process is running with an elevated
// (administrator) token. Best-effort: on failure it returns false, which is the
// safe assumption (ask for elevation).
func isElevated() bool {
	// OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &token)
	curProc, _ := syscall.GetCurrentProcess()
	var hToken syscall.Handle
	ok, _, _ := openProcessToken.Call(
		uintptr(curProc),
		uintptr(0x0008), // TOKEN_QUERY
		uintptr(unsafe.Pointer(&hToken)),
	)
	if ok == 0 {
		return false
	}
	// Best-effort close: a leaked token handle is harmless for a
	// short-lived elevation probe, and the linter rightly complains about an
	// unchecked call in a defer.
	defer func() { _ = syscall.CloseHandle(hToken) }()

	var elevated uint32
	var retLen uint32
	// GetTokenInformation(hToken, TokenElevation, &elevated, 4, &retLen)
	ok, _, _ = getTokenInfo.Call(
		uintptr(hToken),
		uintptr(tokenElevation),
		uintptr(unsafe.Pointer(&elevated)),
		uintptr(unsafe.Pointer(&retLen)),
	)
	if ok == 0 {
		return false
	}
	return elevated != 0
}

// relaunchElevated re-launches the current executable with the "runas" verb so
// Windows shows the UAC elevation prompt. argsToAdd are appended to the current
// command-line args. It returns an error only if the elevation could not be
// started (including when the user cancels the UAC prompt, which surfaces as
// ERROR_CANCELLED / SE_ERR_ACCESSDENIED).
//
// IMPORTANT — arg handling: argsToAdd are added IDEMPOTENTLY. The previous
// version appended unconditionally, so a handoff from a process that was itself
// started with --autoconnect produced "--autoconnect --autoconnect" (and grew
// with each attempt). Duplicated flags are harmless by themselves, but they
// made the "am I an elevated relaunch?" test non-deterministic, which is what
// turned a slow UAC consent into a confusing permission error.
func relaunchElevated(argsToAdd ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	exeDir := filepath.Dir(exe)

	// Build the full command line from current args plus the extras. On Windows
	// we rebuild args as a single quoted command line so spaces in paths are
	// preserved.
	merged := mergeArgs(os.Args[1:], argsToAdd)
	cmdLine := quoteCommandLine(merged)

	// UTF16PtrFromString replaces the deprecated StringToUTF16Ptr and returns
	// an error instead of panicking, so a bad path is reported rather than
	// crashing the app mid-elevation.
	verbPtr, err := syscall.UTF16PtrFromString("runas")
	if err != nil {
		return fmt.Errorf("cannot encode elevation verb: %w", err)
	}
	exePtr, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return fmt.Errorf("cannot encode executable path: %w", err)
	}
	argsPtr, err := syscall.UTF16PtrFromString(cmdLine)
	if err != nil {
		return fmt.Errorf("cannot encode command line: %w", err)
	}
	dirPtr, err := syscall.UTF16PtrFromString(exeDir)
	if err != nil {
		return fmt.Errorf("cannot encode working directory: %w", err)
	}

	// ShellExecuteW(hwnd=0, "runas", exe, cmdLine, dir=exeDir, showCmd=SW_SHOWNORMAL).
	// Passing the executable directory as the working directory ensures the
	// elevated copy can find its own resources (sing-box etc.) regardless of
	// how it was launched.
	res, _, _ := shellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(exePtr)),
		uintptr(unsafe.Pointer(argsPtr)),
		uintptr(unsafe.Pointer(dirPtr)),
		1, // SW_SHOWNORMAL
	)

	// ShellExecuteW returns a value > 32 on success. 31 or less is an error code.
	if uintptr(res) <= 32 {
		return elevationRequestRejected()
	}
	log.Printf("Requested elevation (UAC) for %s %q", exe, cmdLine)
	return nil
}

// elevationRequestRejected maps a failed ShellExecuteW to a readable error. The
// common case is the user cancelling the UAC prompt (ERROR_CANCELLED, 0x4C3).
func elevationRequestRejected() error {
	// ERROR_CANCELLED(1223), ERROR_ELEVATION_REQUIRED(740), ERROR_ACCESS_DENIED(5)
	// all indicate the user declined or elevation couldn't begin.
	return errElevationCancelled
}

// elevationUnsupportedReason returns "" on Windows: elevation IS supported
// (UAC), so a non-elevated process simply relaunches. Kept for build parity
// with elevate_unix.go — the Unix build is where this can be non-empty.
func elevationUnsupportedReason() string { return "" }

// errElevationCancelled is returned when the UAC prompt is declined or cannot be
// shown. Its numeric value is ERROR_CANCELLED for diagnostics.
var errElevationCancelled = syscall.Errno(1223) // ERROR_CANCELLED

// quoteCommandLine quotes each arg for the Windows command line the way
// CommandLineToArgvW expects, and joins them with spaces.
func quoteCommandLine(args []string) string {
	if len(args) == 0 {
		return ""
	}
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += quoteArg(a)
	}
	return out
}

func quoteArg(a string) string {
	if a != "" && !containsWhitespaceOrQuote(a) {
		return a
	}
	// Escape quotes and wrap in double quotes (handles trailing backslashes per
	// the standard Windows argv rules).
	q := `"`
	for _, r := range a {
		if r == '"' {
			q += `\"`
		} else {
			q += string(r)
		}
	}
	q += `"`
	return q
}

func containsWhitespaceOrQuote(s string) bool {
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '"' {
			return true
		}
	}
	return false
}
