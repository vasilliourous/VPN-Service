package activation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func CollectFingerprint() string {
	mac := getMAC()
	disk := getDiskSerial()
	mobo := getMoboSerial()
	raw := fmt.Sprintf("%s|%s|%s", mac, disk, mobo)
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}
