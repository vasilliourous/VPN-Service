//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// showFatalError displays a native message box on Windows so startup failures
// are never invisible (GUI builds have no console).
func showFatalError(message string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBox := user32.NewProc("MessageBoxW")
	title, _ := syscall.UTF16PtrFromString("MyVPN — Startup Error")
	text, _ := syscall.UTF16PtrFromString(message)
	// MB_OK | MB_ICONERROR
	messageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x00000010)
}
