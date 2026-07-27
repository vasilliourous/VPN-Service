//go:build linux

package activation

import (
	"log"
	"os"
	"strings"
)

func init() {
	platformCollector = collectLinuxSources
}

// collectLinuxSources gathers hardware info on Linux.
// Sources:
//   - MAC address: first non-loopback physical interface
//   - Disk serial: /sys/block/<device>/device/serial or udevadm
//   - Motherboard UUID: /sys/class/dmi/id/product_uuid
//   - Machine ID: /etc/machine-id or /var/lib/dbus/machine-id
func collectLinuxSources() fingerprintSources {
	var sources fingerprintSources

	// MAC address — iterate all interfaces, preferring physical ones
	// Try common interface names first (bare metal, VPS, cloud)
	commonIfaces := []string{
		"eth0", "enp0s3", "enp0s8", "enp1s0", "ens3", "ens5", "eno1",
	}
	for _, name := range commonIfaces {
		if addr, err := readFile("/sys/class/net/" + name + "/address"); err == nil {
			addr = strings.TrimSpace(addr)
			if addr != "" && addr != "00:00:00:00:00:00" {
				sources.MAC = addr
				break
			}
		}
	}
	if sources.MAC == "" {
		// Fallback: scan all interfaces
		interfaces, err := os.ReadDir("/sys/class/net")
		if err == nil {
			for _, iface := range interfaces {
				name := iface.Name()
				if name == "lo" || strings.HasPrefix(name, "docker") ||
					strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "br-") ||
					strings.HasPrefix(name, "tun") || strings.HasPrefix(name, "wg") ||
					strings.HasPrefix(name, "lxc") || strings.HasPrefix(name, "cali") {
					continue
				}
				if addr, err := readFile("/sys/class/net/" + name + "/address"); err == nil {
					addr = strings.TrimSpace(addr)
					if addr != "" && addr != "00:00:00:00:00:00" {
						sources.MAC = addr
						break
					}
				}
			}
		}
	}

	// Disk serial — try primary disk (sda, nvme0n1, vda, xvda)
	for _, disk := range []string{"sda", "nvme0n1", "vda", "xvda", "sdb"} {
		if serial, err := readFile("/sys/block/" + disk + "/device/serial"); err == nil {
			serial = strings.TrimSpace(serial)
			if serial != "" {
				sources.DiskSerial = serial
				break
			}
		}
	}

	// Motherboard UUID
	if uuid, err := readFile("/sys/class/dmi/id/product_uuid"); err == nil {
		sources.Motherboard = strings.TrimSpace(uuid)
	} else {
		log.Printf("INFO: motherboard UUID not available (common in containers/VMs): %v", err)
	}

	// Hostname
	if hostname, err := os.Hostname(); err == nil {
		sources.Hostname = hostname
	}

	// Machine ID
	if id, err := readFile("/etc/machine-id"); err == nil {
		sources.MachineID = strings.TrimSpace(id)
	} else if id, err := readFile("/var/lib/dbus/machine-id"); err == nil {
		sources.MachineID = strings.TrimSpace(id)
	}

	return sources
}

// readFile reads a file and returns its contents, or an error.
func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
