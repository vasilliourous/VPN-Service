package activation

import "testing"

func TestLuhnModN_Valid(t *testing.T) {
	// Valid code: MYVPN-A3X9-K7M2-Q5P1-C
	valid := luhnModN("MYVPN-A3X9-K7M2-Q5P1-C")
	if !valid {
		t.Error("expected valid code to pass Luhn check")
	}
}

func TestLuhnModN_InvalidCheckDigit(t *testing.T) {
	invalid := luhnModN("MYVPN-A3X9-K7M2-Q5P1-X")
	if invalid {
		t.Error("expected invalid check digit to fail")
	}
}

func TestLuhnModN_WrongLength(t *testing.T) {
	invalid := luhnModN("MYVPN-ABCD-EFGH-IJKL")
	if invalid {
		t.Error("expected short code to fail")
	}
}

func TestLuhnModN_InvalidChars(t *testing.T) {
	invalid := luhnModN("MYVPN-A3X9-K7M2-Q5O1-C") // contains 'O' which is excluded
	if invalid {
		t.Error("expected code with excluded chars to fail")
	}
}

func TestLuhnModN_Lowercase(t *testing.T) {
	valid := luhnModN("myvpn-a3x9-k7m2-q5p1-c")
	if !valid {
		t.Error("expected lowercase code to pass Luhn check")
	}
}
