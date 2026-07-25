// MyVPN — Secure VPN for school networks.
//
// Usage:
//   myvpn                    Run the desktop app
//   myvpn --hub URL          Use a custom hub URL
//   myvpn --revert           Revert to previous version after failed update
//   myvpn --version          Show version and exit
//
// Build:
//   go build -ldflags="-s -w -X main.version=1.0.0" -o myvpn ./cmd/myvpn/
package main

import (
	"flag"
	"fmt"
	"os"

	"myvpn/internal/gui"
	"myvpn/internal/updater"
)

// version is set at build time via -ldflags.
var version = "1.0.0"

func main() {
	// Parse flags
	hubURL := flag.String("hub", "https://api.yourdomain.com", "Admin hub URL")
	revertFlag := flag.Bool("revert", false, "Revert to previous version after failed update")
	showVersion := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("MyVPN version %s\n", version)
		os.Exit(0)
	}

	// Run update recovery check BEFORE anything else
	// This handles the two-phase sentinel handshake:
	//   - If .update-pending exists but .update-confirmed doesn't, auto-revert
	//   - If --revert passed, restore backup binary
	//
	// MUST run before GUI initialization because the update may
	// replace the current binary.
	rolledBack, err := updater.CheckOnStartup(*revertFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Update recovery warning: %v\n", err)
	}
	if rolledBack {
		fmt.Println("MyVPN: Reverted to previous version after failed update.")
	}

	// Initialize and run the desktop application
	gui.Run(*hubURL, version)
}
