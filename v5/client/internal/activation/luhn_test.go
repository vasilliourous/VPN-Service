package activation

import "testing"

// TestCodeFormatRQ locks in the RQ code format: RQ-XXXX-XXXX-XXXX-C
// (2-char prefix + 3×4 segments + Luhn-mod-N checksum over the whole body).
// Migrated from MYVPN-XXXX-XXXX-XXXX-C on 2026-08-17 — the checksum covers
// the prefix, so every code's check digit changed with the prefix.
func TestCodeFormatRQ(t *testing.T) {
	if CodePrefix != "RQ" {
		t.Fatalf("CodePrefix = %q, want RQ", CodePrefix)
	}
	if CodeTotalLen != 15 {
		t.Fatalf("CodeTotalLen = %d, want 15 (RQ + 3×4 + checksum)", CodeTotalLen)
	}
}

// TestValidateRQExamples validates representative codes in the new format,
// including the doc example and migrated codes.json entries (random segments
// preserved from the old MYVPN codes, checksum recomputed over the RQ body).
func TestValidateRQExamples(t *testing.T) {
	valid := []string{
		"RQ-ABCD-EFGH-JKMN-T", // doc example (used in README/API docs)
		"RQ-AAAA-BBBB-CCCC-C", // storage tests
		"RQ-8FAU-DJBA-DBA7-N", // codes.json eco #1 (migrated)
		"RQ-TKUQ-KFEW-5W8B-3", // codes.json stealth #1 (migrated)
		"RQ-U2ZP-R4YK-XDM8-M", // codes.json strike #1 (migrated)
		"RQ-6SJN-UZM2-QAYE-2", // codes.json strike (migrated)
	}
	for _, code := range valid {
		if err := ValidateCodeFormat(code); err != nil {
			t.Errorf("ValidateCodeFormat(%q) = %v, want nil", code, err)
		}
	}
}

// TestOldMYVPNPrefixRejected ensures legacy-format codes fail client-side
// validation (prefix check) instead of reaching the server.
func TestOldMYVPNPrefixRejected(t *testing.T) {
	if err := ValidateCodeFormat("MYVPN-AAAA-BBBB-CCCC-D"); err == nil {
		t.Error("legacy MYVPN-format code must fail validation with RQ prefix")
	}
}

// TestNormalizeCodeRQ verifies NormalizeCode canonicalises every input
// variant to the hyphenated RQ form.
func TestNormalizeCodeRQ(t *testing.T) {
	cases := []struct{ in, want string }{
		{"RQ-ABCD-EFGH-JKMN-T", "RQ-ABCD-EFGH-JKMN-T"},
		{"rqabcdefghjkmnt", "RQ-ABCD-EFGH-JKMN-T"}, // lowercase, unformatted
		{" rq abcd efgh jkmn t ", "RQ-ABCD-EFGH-JKMN-T"},
		{"RQABCD-EFGHJKMN-T", "RQ-ABCD-EFGH-JKMN-T"}, // misplaced hyphens
		{"not-a-code", "not-a-code"},                 // too short → unchanged
	}
	for _, c := range cases {
		if got := NormalizeCode(c.in); got != c.want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormatCodeRQ checks the hyphenation layout for the 15-char body.
func TestFormatCodeRQ(t *testing.T) {
	if got := FormatCode("RQABCDEFGHJKMNT"); got != "RQ-ABCD-EFGH-JKMN-T" {
		t.Fatalf("FormatCode = %q, want RQ-ABCD-EFGH-JKMN-T", got)
	}
}
