package main

import (
	"flag"
	"fmt"
	"myvpn/internal/gui"
	"myvpn/internal/storage"
	"myvpn/internal/updater"
)

var (
	version     = "1.0.0"
	adminHubURL = flag.String("hub", "https://api.yourdomain.com", "Admin hub URL")
	revertFlag  = flag.Bool("revert", false, "Revert to previous version")
)

func main() {
	flag.Parse()

	storage.Init()

	rolledBack, err := updater.CheckOnStartup(*revertFlag)
	if err != nil {
		fmt.Printf("Update check failed: %v\n", err)
	}
	if rolledBack {
		fmt.Println("Reverted to previous version after update crash")
	}

	// GUI handles activation + heartbeat + main window
	gui.Run(*adminHubURL)
}
