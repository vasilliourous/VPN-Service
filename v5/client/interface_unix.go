//go:build !windows

package main

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// defaultInterface returns the physical interface holding the default IPv4
// route (e.g. "eth0", "en0"), or "" if undetectable. On Unix, sing-box's
// auto_detect_interface usually suffices; the explicit bind is a Windows
// fix (TUN capture), so "" is an acceptable fallback here.
func defaultInterface() string {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "ip", "route", "show", "default").Output()
	if err != nil {
		return ""
	}
	fields := strings.Fields(string(out))
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			name := fields[i+1]
			if strings.Contains(name, "myvpn") || strings.Contains(name, "tun") {
				return ""
			}
			return name
		}
	}
	return ""
}
