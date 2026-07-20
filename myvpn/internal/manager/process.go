package manager

import (
	"encoding/json"
	"fmt"
	"myvpn/internal/branding"
	"myvpn/internal/storage"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

var (
	activeCmd    *exec.Cmd
	activeID     string
	lastExitCode int
	mu           sync.Mutex
	running      bool
)

func IsRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	return running
}

func LastExitCode() int {
	mu.Lock()
	defer mu.Unlock()
	return lastExitCode
}

func StartEngine(cfg storage.ProtocolConfig, planTier string, bandwidthBps int) error {
	mu.Lock()
	defer mu.Unlock()

	stopLocked()

	binaryName := branding.BinaryName(cfg.ID)
	logName := branding.LogCode(cfg.ID)

	binaryPath := storage.GetCachedEnginePath(cfg.ID)
	if binaryPath == "" {
		binDir := storage.EngineDir()
		ext := ""
		if runtime.GOOS == "windows" {
			ext = ".exe"
		}
		binaryPath = filepath.Join(binDir, cfg.BinaryName+ext)
	}

	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		return fmt.Errorf("engine binary not found for %s (%s)", cfg.DisplayName, binaryPath)
	}

	fmt.Printf("%s Starting %s (%s)...\n", logName, cfg.DisplayName, binaryPath)

	cmd := buildCommand(binaryPath, cfg.ID, cfg.ConfigJSON, planTier, bandwidthBps)

	disguiseProcess(cmd, binaryName)

	sandbox(cmd)

	logFile := openEngineLog(cfg.ID)
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("engine start failed: %w", err)
	}

	activeCmd = cmd
	activeID = cfg.ID
	running = true

	go func() {
		err := cmd.Wait()
		mu.Lock()
		running = false
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				lastExitCode = exitErr.ExitCode()
			}
		} else {
			lastExitCode = 0
		}
		mu.Unlock()
	}()

	return nil
}

func StopEngine() error {
	mu.Lock()
	defer mu.Unlock()
	return stopLocked()
}

func stopLocked() error {
	if activeCmd != nil && activeCmd.Process != nil {
		if runtime.GOOS == "windows" {
			activeCmd.Process.Kill()
		} else {
			activeCmd.Process.Signal(os.Interrupt)
			go func() {
				activeCmd.Process.Kill()
			}()
		}
		activeCmd.Wait()
		activeCmd = nil
		activeID = ""
		running = false
	}
	return nil
}

func buildCommand(binaryPath, protocolID string, configJSON json.RawMessage, planTier string, bandwidthBps int) *exec.Cmd {
	switch protocolID {
	case "hysteria2":
		args := []string{
			"client",
			"-c", string(configJSON),
		}
		if bandwidthBps > 0 {
			args = append(args, "--speed", fmt.Sprintf("%d", bandwidthBps))
		}
		return exec.Command(binaryPath, args...)
	case "usque":
		return exec.Command(binaryPath, "-c", string(configJSON))
	default:
		return exec.Command(binaryPath, "-c", string(configJSON))
	}
}

func disguiseProcess(_ *exec.Cmd, _ string) {}

func sandbox(cmd *exec.Cmd) {
	// Platform-specific sandboxing can be added here
}

func openEngineLog(protocolID string) *os.File {
	logDir := storage.LogDir()
	logPath := filepath.Join(logDir, fmt.Sprintf("engine-%s.log", protocolID))
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil
	}
	return f
}

func ActiveProtocolID() string {
	mu.Lock()
	defer mu.Unlock()
	return activeID
}
