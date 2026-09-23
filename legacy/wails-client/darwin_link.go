//go:build darwin

// Package main — macOS linker compatibility shim.
//
// Wails v2.9.1's darwin frontend uses UTType (drag & drop, file URLs) but only
// links Foundation, Cocoa and WebKit. On older macOS SDKs the
// UniformTypeIdentifiers framework was re-exported transitively, so the symbol
// resolved implicitly. Xcode 26 / macOS 26 SDK removed that implicit linkage,
// and the final link fails with:
//
//	Undefined symbols for architecture arm64: "_OBJC_CLASS_$_UTType"
//
// This cgo directive adds the missing framework to the final link. cgo flags
// from the main package are passed to the final link step, so this fixes both
// `wails build` and manual `go build` on all darwin architectures (amd64/arm64).
// The file is excluded on non-darwin platforms via the build tag above.
package main

/*
#cgo LDFLAGS: -framework UniformTypeIdentifiers
*/
import "C"
