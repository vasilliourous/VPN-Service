package uotkey

import (
	"encoding/json"
	"testing"
)

// The payload below is verbatim from the live hub (170.64.196.179), captured
// 2026-09-19 with:
//
//	curl -s -X POST https://.../api/heartbeat -d '{"code":"RQ-...","fingerprint":"..."}'
//
// It is embedded as a literal on purpose. If the wire format ever changes, this
// test must be updated deliberately — that is the point. Deriving it from the
// struct would have made the original bug invisible.
const liveStrikeHeartbeat = `{
  "server_config": {
    "method": "aes-256-gcm",
    "password": "3467ce5ae756c185f45be91fed2dbeb6",
    "server": "networkingguides.duckdns.org",
    "server_port": 8445,
    "uot_port": 8446
  },
  "server_time": "2026-09-19T10:09:42.945Z",
  "status": "ok",
  "tier": "strike",
  "udp_relay": true
}`

// TestDecodeReadsTheKeyTheHubActuallySends is the regression guard for the bug
// that shipped UoT disabled on every platform: the hub emits "uot_port", the
// client declared "server_port_uot", and encoding/json ignores unknown keys
// without error, so the port never landed.
//
// The test runs a REAL envelope — the server_config object nested inside a full
// heartbeat response — rather than a bare config, because that nesting is how
// the value travels in production and is where a struct-tag-only decode fails.
func TestDecodeReadsTheKeyTheHubActuallySends(t *testing.T) {
	var env struct {
		ServerConfig json.RawMessage `json:"server_config"`
		UDPRelay     bool            `json:"udp_relay"`
		Tier         string          `json:"tier"`
	}
	if err := json.Unmarshal([]byte(liveStrikeHeartbeat), &env); err != nil {
		t.Fatalf("live payload no longer parses: %v", err)
	}

	cfg, err := Decode(env.ServerConfig)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// Guard the whole strike contract, not just the port: the client gates UoT
	// on (udp_relay AND port > 0), so a regression in either half disables the
	// feature and must fail here rather than silently in the field.
	if cfg.ServerPortUOT != 8446 {
		t.Errorf("ServerPortUOT = %d, want 8446.\n"+
			"The hub sends %q; a decode that does not resolve it silently "+
			"disables UDP-over-TCP for the entire fleet.", cfg.ServerPortUOT, Canonical)
	}
	if !env.UDPRelay {
		t.Error("udp_relay decoded false from the live strike payload, want true — " +
			"process.go gates UoT on this flag")
	}
	if cfg.ServerPort != 8445 {
		t.Errorf("ServerPort = %d, want 8445 (the TCP port must be unaffected by UoT resolution)", cfg.ServerPort)
	}
}

// TestRawStructTagAloneDoesNotResolve guards against "simplifying" the fix by
// adding a `json:"uot_port"` tag to the shared struct and dropping Decode.
// That would break the documented precedence and the legacy tolerance, so it is
// asserted as deliberately insufficient.
func TestRawStructTagAloneDoesNotResolve(t *testing.T) {
	var bare ServerConfig
	if err := json.Unmarshal([]byte(`{"`+Canonical+`":8446}`), &bare); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if bare.ServerPortUOT != 0 {
		t.Fatalf("a bare struct tag now resolves %s — precedence and legacy "+
			"tolerance must be asserted in Decode/Port, not left to tag order", Canonical)
	}
}

// TestDecodeEmptyMeansNoUoT pins the "absent" case. eco and stealth carry no
// UoT endpoint, and 0 is what process.go gates on — it must not become a
// non-zero sentinel.
func TestDecodeEmptyMeansNoUoT(t *testing.T) {
	const eco = `{"server":"example.org","server_port":8443,"password":"p","method":"aes-256-gcm"}`
	cfg, err := Decode([]byte(eco))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if cfg.ServerPortUOT != 0 {
		t.Errorf("ServerPortUOT = %d, want 0 for a tier with no UoT endpoint", cfg.ServerPortUOT)
	}
	if cfg.ServerPort != 8443 || cfg.Password != "p" {
		t.Errorf("other fields were not decoded: %+v", cfg)
	}
}

// TestDecodeAcceptsBothSpellings covers the forward-compatibility direction:
// if the server is ever renamed, an already-built client must still work.
func TestDecodeAcceptsBothSpellings(t *testing.T) {
	for _, key := range []string{Canonical, Legacy} {
		t.Run(key, func(t *testing.T) {
			payload := `{"server":"s","server_port":8445,"password":"p","method":"m","` + key + `":9999}`
			cfg, err := Decode([]byte(payload))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if cfg.ServerPortUOT != 9999 {
				t.Errorf("key %q: ServerPortUOT = %d, want 9999", key, cfg.ServerPortUOT)
			}
		})
	}
}

// TestCanonicalBeatsLegacy fixes the precedence, so a payload carrying both
// (e.g. a transitional server) resolves deterministically rather than by map
// iteration order.
func TestCanonicalBeatsLegacy(t *testing.T) {
	payload := `{"` + Canonical + `":8446,"` + Legacy + `":9999}`
	cfg, err := Decode([]byte(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if cfg.ServerPortUOT != 8446 {
		t.Errorf("ServerPortUOT = %d, want 8446 (%s must win over %s)",
			cfg.ServerPortUOT, Canonical, Legacy)
	}
}

// TestNonPositiveIsTreatedAsAbsent: a 0 or negative port is not an endpoint.
// process.go's guard is `> 0`, so anything else must normalise to 0 rather than
// leak a nonsense port into a generated sing-box config.
func TestNonPositiveIsTreatedAsAbsent(t *testing.T) {
	for _, raw := range []string{"0", "-1"} {
		payload := `{"` + Canonical + `":` + raw + `}`
		cfg, err := Decode([]byte(payload))
		if err != nil {
			t.Fatalf("Decode(%s): %v", raw, err)
		}
		if cfg.ServerPortUOT != 0 {
			t.Errorf("port %s resolved to %d, want 0", raw, cfg.ServerPortUOT)
		}
	}
}
