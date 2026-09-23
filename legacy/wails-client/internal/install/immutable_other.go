//go:build !linux

package install

// immutableMount reports whether this process runs from a read-only mount.
//
// Only Linux has the concept as a first-class deployment shape (AppImage).
// macOS .app bundles installed in Applications are writable by the installing
// user, and Windows installs are writable because the client runs elevated, so
// both report false here and go through the normal ownedInstallDir rules.
func immutableMount() bool { return false }
