//go:build !frontend

package main

import "embed"

// assets is an empty FS when the frontend is not embedded.
// Build with `-tags "frontend desktop production"` to embed the real UI and
// the real Wails desktop implementation.
var assets embed.FS
