//go:build !frontend

package main

import "embed"

// assets is an empty FS when the frontend is not embedded.
// Build with `-tags frontend` to embed the real frontend/dist.
var assets embed.FS
