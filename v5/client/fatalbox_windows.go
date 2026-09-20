//go:build windows

package main

import (
	"log"
	"syscall"
	"unsafe"
)

// showFatalError displays a native message box on Windows so startup failures
// are never invisible (GUI builds have no console).
//
// This is the LAST-RESORT path — it runs when the app is already failing — so
// it must not itself fail silently. The UTF16 conversions are checked and every
// failure is logged: a message box that shows nothing is indistinguishable from
// the app doing nothing, which is the exact symptom this function exists to
// prevent. The box is always shown, falling back to a static message if the
// real one cannot be encoded.
func showFatalError(message string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBox := user32.NewProc("MessageBoxW")

	text, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		log.Printf("showFatalError: cannot encode message (%v); showing a generic one", err)
		fallback, ferr := syscall.UTF16PtrFromString("Locus failed to start. See locus.log for details.")
		if ferr != nil {
			log.Printf("showFatalError: cannot encode fallback message either: %v", ferr)
			return
		}
		text = fallback
	}

	title, err := syscall.UTF16PtrFromString("Locus — Startup Error")
	if err != nil {
		// A missing title is cosmetic; the body is what matters.
		title, _ = syscall.UTF16PtrFromString("Locus")
	}

	// MB_OK | MB_ICONERROR. The return value is the user's choice; nothing to do
	// with it, but the call is checked so a broken user32 shows up in the log.
	if ret, _, callErr := messageBox.Call(
		0,
		uintptr(unsafe.Pointer(text)),
		uintptr(unsafe.Pointer(title)),
		0x00000010,
	); ret == 0 {
		log.Printf("showFatalError: MessageBoxW failed: %v", callErr)
	}
}
