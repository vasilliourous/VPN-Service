package tunnel

import (
	"os/exec"
	"runtime"
)

func Guard() error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("netsh", "interface", "ip", "set", "dns", "MyVPN", "static", "10.0.0.1").Run()
	case "darwin":
		return exec.Command("sh", "-c", "networksetup -setdnsservers MyVPN 10.0.0.1").Run()
	case "linux":
		return exec.Command("sh", "-c", "echo 'nameserver 10.0.0.1' > /etc/resolv.conf").Run()
	}
	return nil
}

func UnGuard() error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("netsh", "interface", "ip", "set", "dns", "MyVPN", "dhcp").Run()
	case "darwin":
		return exec.Command("sh", "-c", "networksetup -setdnsservers MyVPN Empty").Run()
	case "linux":
		return nil
	}
	return nil
}
