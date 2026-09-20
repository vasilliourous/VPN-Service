// Package winres reads version metadata out of a compiled Windows resource
// object (.syso).
//
// # WHY THIS EXISTS
//
// The Locus client ships rsrc_windows_{amd64,arm64}.syso — the compiled
// VS_VERSION_INFO block that fills in the Windows Properties tab. They are
// checked in, and NOTHING regenerates them automatically: `go generate` is
// manual. So the directive in main.go can be perfectly correct while the
// committed artifacts are stale.
//
// That is exactly what happened. Releases 2.1.0 shipped .syso files stamped
// "2.0.0" and product name "MyVPN", because the guard that existed only grepped
// the go:generate DIRECTIVE for the right number and never looked at its
// output. A student right-clicking locus.exe saw a version that was two
// releases old, under the product's former name.
//
// This package reads the bytes, so a stale artifact is caught instead of a
// stale comment.
//
// # FORMAT NOTE
//
// go-winres emits the version block as UTF-16LE. A plain byte scan will not
// find the strings, which is why the earlier guards could not simply grep the
// file. We decode both alignments (the block is 4-byte aligned, and the
// alignment depends on the length of everything preceding it) and keep
// whichever yields more plausible strings.
package winres

import (
	"os"
	"strings"
	"unicode/utf16"
)

// Version is the subset of VS_VERSION_INFO that Locus asserts on.
type Version struct {
	FileVersion    string
	ProductVersion string
	ProductName    string
	FileDesc       string
}

// stringsFromUTF16LE decodes raw as UTF-16LE at the given byte offset and
// returns the runs of printable ASCII at least 2 characters long. Offsets are
// tried at 0 and 1 because the resource block's alignment inside the object
// file is not fixed.
func stringsFromUTF16LE(raw []byte, off int) []string {
	if off >= len(raw) {
		return nil
	}
	tail := raw[off:]
	if len(tail)%2 == 1 {
		tail = tail[:len(tail)-1]
	}
	units := make([]uint16, len(tail)/2)
	for i := range units {
		units[i] = uint16(tail[2*i]) | uint16(tail[2*i+1])<<8
	}

	var (
		out []string
		cur []rune
	)
	flush := func() {
		if len(cur) >= 2 {
			out = append(out, string(cur))
		}
		cur = cur[:0]
	}
	for _, r := range utf16.Decode(units) {
		if r >= 32 && r < 127 {
			cur = append(cur, r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// ReadFile extracts version metadata from a .syso file.
//
// It returns ok=false when no version strings could be found at all, which
// means the resource format changed and the caller's guard is now blind — a
// condition that must fail loudly rather than pass silently.
func ReadFile(path string) (Version, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Version{}, false, err
	}
	return Parse(raw)
}

// Parse extracts version metadata from raw .syso bytes.
func Parse(raw []byte) (Version, bool, error) {
	// The version block is a flat key/value sequence:
	//   "FileVersion", "2.1.0", "ProductName", "Locus", ...
	// so pair each known key with the string that follows it. Pick the alignment
	// that produced the most strings; the wrong one yields fragments.
	var best []string
	for _, off := range []int{0, 1} {
		if s := stringsFromUTF16LE(raw, off); len(s) > len(best) {
			best = s
		}
	}

	var v Version
	found := false
	for i, s := range best {
		if i+1 >= len(best) {
			break
		}
		switch s {
		case "FileVersion":
			v.FileVersion, found = best[i+1], true
		case "ProductVersion":
			v.ProductVersion, found = best[i+1], true
		case "ProductName":
			v.ProductName, found = best[i+1], true
		case "FileDescription":
			v.FileDesc, found = best[i+1], true
		}
	}
	return v, found, nil
}

// Contains reports whether the decoded resource mentions s, for guards that
// need to check a string that is not one of the four tracked fields.
func Contains(raw []byte, s string) bool {
	for _, off := range []int{0, 1} {
		for _, got := range stringsFromUTF16LE(raw, off) {
			if strings.Contains(got, s) {
				return true
			}
		}
	}
	return false
}
