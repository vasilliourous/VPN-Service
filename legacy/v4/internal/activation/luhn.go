// Package activation provides activation code validation and device fingerprinting.
//
// Code Format: MYVPN-XXXX-XXXX-XXXX-C
//   - MYVPN: Static prefix
//   - XXXX: Random base-32 segments (charset: ABCDEFGHJKLMNPQRSTUVWXYZ23456789)
//   - C: Luhn-mod-N checksum character
//
// The Luhn-mod-N checksum is validated client-side before any server request,
// preventing accidental typos and reducing server load from invalid codes.
package activation

const (
	// CodeCharset is the character set for activation codes.
	// Excludes I, O, 0, 1 to avoid readability ambiguity.
	CodeCharset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

	// codeBase is the length of the charset (32 characters).
	codeBase = len(CodeCharset)

	// CodePrefix is the MyVPN code prefix.
	CodePrefix = "MYVPN"

	// CodeSegments is the number of random segments.
	CodeSegments = 3

	// CodeSegmentLen is the length of each random segment.
	CodeSegmentLen = 4

	// CodeTotalLen is the total length of the code without separators:
	// prefix (5) + segments (3*4) + checksum (1) = 18
	CodeTotalLen = len(CodePrefix) + CodeSegments*CodeSegmentLen + 1
)

// luhnModNCheck validates a code using the Luhn-mod-N algorithm.
// It returns true if the checksum character matches the computed value.
func luhnModNCheck(code string) bool {
	cleaned := stripFormatting(code)
	if len(cleaned) < 2 {
		return false
	}

	sum := 0
	double := false

	for i := len(cleaned) - 2; i >= 0; i-- {
		idx := charIndex(cleaned[i])
		if idx < 0 {
			return false
		}

		val := idx
		if double {
			val *= 2
			if val >= codeBase {
				val = val - codeBase + 1
			}
		}
		sum += val
		double = !double
	}

	// The last character is the checksum — verify it matches
	expectedIdx := (codeBase - (sum % codeBase)) % codeBase
	lastIdx := charIndex(cleaned[len(cleaned)-1])

	return lastIdx == expectedIdx
}

// GenerateCheckChar computes the Luhn-mod-N checksum character for a code body.
func GenerateCheckChar(body string) (byte, error) {
	for _, c := range body {
		if charIndex(byte(c)) < 0 {
			return 0, ErrInvalidCharacter
		}
	}

	sum := 0
	double := false

	for i := len(body) - 1; i >= 0; i-- {
		val := charIndex(body[i])
		if double {
			val *= 2
			if val >= codeBase {
				val = val - codeBase + 1
			}
		}
		sum += val
		double = !double
	}

	idx := (codeBase - (sum % codeBase)) % codeBase
	return CodeCharset[idx], nil
}

// FormatCode formats a raw code string with hyphens.
// Input: "MYVPNXXXX...C" → Output: "MYVPN-XXXX-XXXX-XXXX-C"
func FormatCode(raw string) string {
	if len(raw) != CodeTotalLen {
		return raw
	}

	p := len(CodePrefix)
	s := CodeSegmentLen

	return raw[:p] + "-" +
		raw[p:p+s] + "-" +
		raw[p+s:p+2*s] + "-" +
		raw[p+2*s:p+3*s] + "-" +
		raw[len(raw)-1:]
}

// stripFormatting removes hyphens and converts to uppercase.
func stripFormatting(code string) string {
	result := make([]byte, 0, len(code))
	for _, c := range code {
		if c >= 'a' && c <= 'z' {
			result = append(result, byte(c-'a'+'A'))
		} else if c >= 'A' && c <= 'Z' {
			result = append(result, byte(c))
		} else if c >= '0' && c <= '9' {
			result = append(result, byte(c))
		}
		// Skip hyphens and other non-alphanumeric chars
	}
	return string(result)
}

// charIndex returns the index of a character in the charset, or -1 if not found.
func charIndex(c byte) int {
	switch {
	case c >= 'A' && c <= 'Z':
		// Map to our charset (which is not sequential A-Z)
		for i := 0; i < codeBase; i++ {
			if CodeCharset[i] == c {
				return i
			}
		}
	case c >= '2' && c <= '9':
		// Digits in our charset start at index 0
		return int(c - '2')
	}
	return -1
}
