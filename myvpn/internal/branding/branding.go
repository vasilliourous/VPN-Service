package branding

import "sync"

var (
	mu sync.RWMutex

	names = map[string]string{
		"hysteria2": "Speed Mode",
		"usque":     "Lite Mode",
		"xray":      "Stealth Mode",
		"tuic":      "Game Mode",
	}

	binaryNames = map[string]string{
		"hysteria2": "speedmode",
		"usque":     "litemode",
		"xray":      "stealthmode",
		"tuic":      "gamemode",
	}

	plans = map[string]string{
		"warp_lite":      "Warp Lite",
		"stealth_browse": "Stealth Browse",
		"gaming_mid":     "Gaming Mid",
		"gaming_max":     "Gaming Max",
	}

	logCodes = map[string]string{
		"hysteria2": "[01]",
		"usque":     "[02]",
		"xray":      "[03]",
		"tuic":      "[04]",
	}
)

func ProtocolDisplayName(protocolID string) string {
	mu.RLock()
	defer mu.RUnlock()
	if name, ok := names[protocolID]; ok {
		return name
	}
	return protocolID
}

func BinaryName(protocolID string) string {
	mu.RLock()
	defer mu.RUnlock()
	if name, ok := binaryNames[protocolID]; ok {
		return name
	}
	return protocolID
}

func PlanDisplayName(planID string) string {
	mu.RLock()
	defer mu.RUnlock()
	if name, ok := plans[planID]; ok {
		return name
	}
	return planID
}

func LogCode(protocolID string) string {
	mu.RLock()
	defer mu.RUnlock()
	if code, ok := logCodes[protocolID]; ok {
		return code
	}
	return "[??]"
}
