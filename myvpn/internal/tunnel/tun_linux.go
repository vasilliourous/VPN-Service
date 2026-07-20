//go:build linux

package tunnel

import (
	"fmt"
	"os/exec"
)

func Setup() error {
	out, err := exec.Command("ip", "tuntap", "add", "dev", "tun0", "mode", "tun").CombinedOutput()
	if err != nil {
		return fmt.Errorf("tun setup failed: %s: %w", string(out), err)
	}
	out, err = exec.Command("ip", "link", "set", "dev", "tun0", "up").CombinedOutput()
	if err != nil {
		return fmt.Errorf("tun link up failed: %s: %w", string(out), err)
	}
	out, err = exec.Command("ip", "addr", "add", "10.0.0.2/24", "dev", "tun0").CombinedOutput()
	if err != nil {
		return fmt.Errorf("tun addr failed: %s: %w", string(out), err)
	}
	return nil
}

func Teardown() error {
	out, err := exec.Command("ip", "link", "delete", "dev", "tun0").CombinedOutput()
	if err != nil {
		return fmt.Errorf("tun teardown failed: %s: %w", string(out), err)
	}
	return nil
}

func AddRoute(dest, gateway string) error {
	out, err := exec.Command("ip", "route", "add", dest, "via", gateway, "dev", "tun0").CombinedOutput()
	if err != nil {
		return fmt.Errorf("add route failed: %s: %w", string(out), err)
	}
	return nil
}

func RemoveRoute(dest string) error {
	out, err := exec.Command("ip", "route", "del", dest).CombinedOutput()
	if err != nil {
		return fmt.Errorf("remove route failed: %s: %w", string(out), err)
	}
	return nil
}
