//go:build !windows && !darwin && !linux

package install

// ownedInstallDir is a no-op on platforms without a defined install location.
// Every supported Locus platform has a real implementation; this stub exists so
// the package still compiles if the client is built for an unexpected GOOS
// (BSD, for instance) instead of failing with an undefined symbol.
func ownedInstallDir(dir string) (string, string, bool) {
	return "", "", false
}
