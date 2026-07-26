// MyVPN Hardened Client — Secure VPN for school networks.
//
// Usage:
//   myvpn                    Run the desktop app
//   myvpn --hub URL          Use a custom hub URL
//   myvpn --revert           Revert to previous version after failed update
//   myvpn --version          Show version and exit
//
// Build:
//   go build -ldflags="-s -w -X main.version=2.0.0" -o myvpn ./cmd/myvpn/
//
// Hardening: signal handling, panic recovery, context propagation.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"myvpn/internal/activation"
	"myvpn/internal/gui"
	"myvpn/internal/updater"
)

// version is set at build time via -ldflags.
var version = "2.0.0"

func main() {
	// Global panic recovery — prevents the app from disappearing on unexpected errors
	defer func() {
		if r := recover(); r != nil {
			log.Printf("CRASH RECOVERY: panic in main: %v\n%s", r, debug.Stack())
			// Show a visible error to the user before exiting
			fmt.Fprintf(os.Stderr, "MyVPN encountered an unexpected error and must close.\n")
			fmt.Fprintf(os.Stderr, "Please report this to support with the details above.\n")
			os.Exit(1)
		}
	}()

	// Parse flags
	hubURL := flag.String("hub", "https://networkingguides.duckdns.org", "Admin hub URL")
	revertFlag := flag.Bool("revert", false, "Revert to previous version after failed update")
	showVersion := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("MyVPN version %s\n", version)
		os.Exit(0)
	}

	// Validate hub URL is well-formed
	if err := activation.ValidateHubURL(*hubURL); err != nil {
		log.Fatalf("Invalid hub URL: %v", err)
	}

	// Create a cancellable context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("Received signal %v, initiating graceful shutdown...", sig)
		cancel()
		// Give it 5 seconds then force exit
		time.Sleep(5 * time.Second)
		log.Fatal("Forced shutdown after timeout")
	}()

	// ── Two-phase update recovery ──
	// This runs BEFORE anything else because the update may replace the current binary.
	//
	// Flow:
	//   1. Check for .update-pending sentinel
	//   2. If .update-confirmed is missing → auto-revert (update crashed)
	//   3. If --revert flag → restore backup binary
	//   4. Clean up stale update markers older than 48h
	//   5. Create .update-confirmed sentinel if .update-pending exists (we survived!)
	execPath, err := os.Executable()
	if err == nil {
		appDir := filepath.Dir(execPath)
		// Clean up stale markers (older than 48 hours)
		updater.CleanStaleMarkers(appDir, 48*time.Hour)

		rolledBack, err := updater.CheckOnStartup(*revertFlag)
		if err != nil {
			log.Printf("Update recovery warning: %v", err)
		}
		if rolledBack {
			log.Println("MyVPN: Reverted to previous version after failed update.")
			fmt.Println("MyVPN: Reverted to previous version after failed update.")
		} else {
			// No revert needed — confirm update if this is a new binary
			if err := updater.ConfirmIfPending(appDir); err != nil {
				log.Printf("Update confirmation warning: %v", err)
			}
		}
	}

	// Initialize and run the desktop application with context
	gui.RunWithContext(ctx, *hubURL, version)
}
