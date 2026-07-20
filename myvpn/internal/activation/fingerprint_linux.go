//go:build !windows && !darwin

package activation

func getMAC() string      { return "linux-test-mac" }
func getDiskSerial() string { return "linux-test-disk" }
func getMoboSerial() string { return "linux-test-mobo" }
