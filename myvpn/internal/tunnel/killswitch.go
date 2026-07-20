package tunnel

import (
	"os/exec"
	"runtime"
)

func Engage() error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("netsh", "interface", "ip", "delete", "route", "0.0.0.0/0").Run()
	case "darwin":
		return exec.Command("sh", "-c", `echo "block drop out all" | pfctl -a com.myvpn/killswitch -ef -`).Run()
	case "linux":
		return exec.Command("sh", "-c", "iptables -A OUTPUT -j DROP").Run()
	}
	return nil
}

func Disengage() error {
	switch runtime.GOOS {
	case "windows":
		return nil // Routes restored automatically on TUN teardown
	case "darwin":
		return exec.Command("sh", "-c", "pfctl -a com.myvpn/killswitch -F all").Run()
	case "linux":
		return exec.Command("sh", "-c", "iptables -F OUTPUT").Run()
	}
	return nil
}
