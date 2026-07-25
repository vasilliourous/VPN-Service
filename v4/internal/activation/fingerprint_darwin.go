//go:build darwin

package activation

import (
	"os"
	"os/exec"
	"strings"
)

func init() {
	platformCollector = collectDarwinSources
}

// collectDarwinSources gathers hardware info on macOS.
// Sources:
//   - MAC address: en0/en1 interface
//   - Disk serial: ioreg IOPlatformSerialNumber
//   - Motherboard UUID: ioreg IOPlatformUUID
//   - Hostname
func collectDarwinSources() fingerprintSources {
	var sources fingerprintSources

	// MAC address — en0 (Wi-Fi) or en1 (Ethernet)
	for _, iface := range []string{"en0", "en1"} {
		if mac, err := exec.Command("networksetup", "-getmacaddress", iface).Output(); err == nil {
			parts := strings.Split(string(mac), " ")
			if len(parts) >= 3 {
				sources.MAC = parts[2]
				break
			}
		}
	}

	// Disk serial + motherboard UUID from ioreg
	if output, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output(); err == nil {
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.Contains(line, "\"IOPlatformSerialNumber\"") {
				parts := strings.Split(line, "\"")
				if len(parts) >= 4 {
					sources.DiskSerial = parts[3]
				}
			}
			if strings.Contains(line, "\"IOPlatformUUID\"") {
				parts := strings.Split(line, "\"")
				if len(parts) >= 4 {
					sources.Motherboard = parts[3]
				}
			}
		}
	}

	// Hostname
	if hostname, err := os.Hostname(); err == nil {
		sources.Hostname = hostname
	}

	return sources
}
