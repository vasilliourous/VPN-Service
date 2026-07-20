package branding

import "testing"

func TestProtocolDisplayName(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"hysteria2", "Speed Mode"},
		{"usque", "Lite Mode"},
		{"unknown", "unknown"},
	}
	for _, tt := range tests {
		got := ProtocolDisplayName(tt.id)
		if got != tt.want {
			t.Errorf("ProtocolDisplayName(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestBinaryName(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"hysteria2", "speedmode"},
		{"usque", "litemode"},
		{"unknown", "unknown"},
	}
	for _, tt := range tests {
		got := BinaryName(tt.id)
		if got != tt.want {
			t.Errorf("BinaryName(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestPlanDisplayName(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"warp_lite", "Warp Lite"},
		{"gaming_max", "Gaming Max"},
		{"unknown", "unknown"},
	}
	for _, tt := range tests {
		got := PlanDisplayName(tt.id)
		if got != tt.want {
			t.Errorf("PlanDisplayName(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}
