//go:build frontend

package main

import "embed"

//go:embed all:frontend/dist
var assets embed.FS
