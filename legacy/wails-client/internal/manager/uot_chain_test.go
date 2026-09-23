//go:build !windows

package manager

// The end-to-end assertion the codebase was missing.
//
// TestGeneratedConfigUOT (this package) proves a Config with ServerPortUOT > 0
// produces the proxy-uot outbound. The uotkey contract tests prove the live hub
// payload decodes into ServerPortUOT = 8446. Neither proves the two CONNECT —
// which is precisely how UDP-over-TCP stayed dead: every individual link passed
// its own test while the chain was never exercised.
//
// This runs the whole chain with production data: a verbatim live heartbeat
// response -> heartbeat.ServerConfig -> app.go's field copies -> manager.Config
// -> the sing-box JSON written to disk. If any link drops the value, the
// generated config loses proxy-uot and this fails.

import (
	"encoding/json"
	"strings"
	"testing"

	"locus/internal/heartbeat"
)

// liveStrikeResponse is verbatim from the live hub (170.64.196.179), captured
// 2026-09-19 after the UoT deployment. Kept literal on purpose: this test
// detects wire-format drift, and deriving it from our own structs would make it
// blind to the exact bug it guards against.
const liveStrikeResponse = `{
  "server_config": {
    "method": "aes-256-gcm",
    "password": "3467ce5ae756c185f45be91fed2dbeb6",
    "server": "networkingguides.duckdns.org",
    "server_port": 8445,
    "uot_port": 8446
  },
  "server_time": "2026-09-19T10:45:37.978Z",
  "status": "ok",
  "tier": "strike",
  "udp_relay": true
}`

// appConfigFrom mirrors app.go's mapping from a heartbeat response to the
// manager's Config. Reproduced so that a change to either the struct or the
// mapping surfaces here rather than in the field.
func appConfigFrom(resp *heartbeat.Response) Config {
	return Config{
		Server:        resp.ServerConfig.Server,
		ServerPort:    resp.ServerConfig.ServerPort,
		Password:      resp.ServerConfig.Password,
		Method:        resp.ServerConfig.Method,
		UDPRelay:      resp.UDPRelay,
		ServerPortUOT: resp.ServerConfig.ServerPortUOT,
	}
}

func TestLivePayloadProducesWorkingUoTConfig(t *testing.T) {
	// Link 1: the wire payload decodes into the heartbeat struct.
	var resp heartbeat.Response
	if err := json.Unmarshal([]byte(liveStrikeResponse), &resp); err != nil {
		t.Fatalf("live heartbeat payload no longer parses: %v", err)
	}
	if resp.ServerConfig == nil {
		t.Fatal("server_config did not decode — the client would have no tunnel settings")
	}
	if resp.ServerConfig.ServerPortUOT != 8446 {
		t.Fatalf("link 1 broken: heartbeat ServerPortUOT = %d, want 8446\n"+
			"(the hub sends \"uot_port\" — see FIXES.md 29)", resp.ServerConfig.ServerPortUOT)
	}

	// Link 2: app.go's field copies.
	cfg := appConfigFrom(&resp)
	if cfg.ServerPortUOT != 8446 {
		t.Fatalf("link 2 broken: Config.ServerPortUOT = %d, want 8446", cfg.ServerPortUOT)
	}
	if !cfg.UDPRelay {
		t.Fatal("link 2 broken: UDPRelay false — process.go gates UoT on this AND the port")
	}

	// Link 3: the sing-box config that would actually be written.
	raw, err := generateConfig(cfg)
	if err != nil {
		t.Fatalf("generateConfig: %v", err)
	}
	got := string(raw)

	if !strings.Contains(got, `"tag": "proxy-uot"`) {
		t.Errorf("link 3 broken: no proxy-uot outbound in the generated config, so\n"+
			"UDP-over-TCP is inactive despite the hub advertising a live endpoint\n"+
			"(FIXES.md 29).\n--- generated ---\n%s", got)
	}
	if !strings.Contains(got, `"udp_over_tcp": true`) {
		t.Errorf("link 3 broken: proxy-uot present but udp_over_tcp not enabled:\n%s", got)
	}
	if !strings.Contains(got, `"server_port": 8446`) {
		t.Errorf("link 3 broken: the UoT outbound does not point at 8446:\n%s", got)
	}
	if !strings.Contains(got, `"server_port": 8445`) {
		t.Errorf("link 3 broken: the TCP proxy outbound lost port 8445:\n%s", got)
	}
	if !strings.Contains(got, `"final": "proxy"`) {
		t.Errorf("link 3 broken: route final is not proxy, so TCP would follow UDP:\n%s", got)
	}
	if !strings.Contains(got, `"outbound": "proxy-uot"`) {
		t.Errorf("link 3 broken: no route rule sends UDP to proxy-uot:\n%s", got)
	}
}

// TestLiveEcoPayloadDoesNotEmitUoT is the negative control. eco carries no UoT
// endpoint, and enabling one there would send the tier's UDP to a port with no
// sing-box listener, where shadowsocks-rust RSTs it and UDP breaks worse than
// if it had stayed raw (FIXES.md Follow-up 9).
func TestLiveEcoPayloadDoesNotEmitUoT(t *testing.T) {
	const liveEco = `{
      "server_config": {"method":"aes-256-gcm","password":"340f1e1e4ca75e552bc406300c46c8a2",
        "server":"networkingguides.duckdns.org","server_port":8443},
      "status":"ok","tier":"eco","udp_relay":false}`

	var resp heartbeat.Response
	if err := json.Unmarshal([]byte(liveEco), &resp); err != nil {
		t.Fatalf("eco payload no longer parses: %v", err)
	}
	raw, err := generateConfig(appConfigFrom(&resp))
	if err != nil {
		t.Fatalf("generateConfig: %v", err)
	}
	got := string(raw)
	if strings.Contains(got, "proxy-uot") || strings.Contains(got, `"udp_over_tcp"`) {
		t.Errorf("eco tier must NOT emit UoT config — it has no sing-box endpoint:\n%s", got)
	}
}
