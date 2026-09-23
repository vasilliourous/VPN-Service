package uotkey_test

// End-to-end guard for the UoT wire contract, exercised against the ACTUAL
// structs the client deserialises into: activation.ServerConfig,
// heartbeat.Response/ServerConfig and storage.ServerConfig.
//
// WHY THIS IS AN EXTERNAL TEST (package uotkey_test)
//
// The original bug survived because each side was verified in isolation:
// uotkey's own unit tests pass while the three consumer structs still drop the
// value. Only an assertion against the real types catches that.
//
// It lives here, not in package main, because package main imports internal/tray
// and therefore getlantern/systray, which needs GTK/appindicator headers to
// build on Linux. None of the three structs under test pull in systray (checked
// via `go list -deps`), so this location runs natively on any machine — and
// still compiles for GOOS=windows, which is the real release target.
//
// The payload is the verbatim live hub response (170.64.196.179), captured
// 2026-09-19. See FIXES.md entry 29.

import (
	"encoding/json"
	"testing"

	"locus/internal/activation"
	"locus/internal/heartbeat"
	"locus/internal/storage"
	"locus/internal/uotkey"
)

const liveHeartbeatResponse = `{
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

// TestHeartbeatResponseResolvesLiveUoTPort covers the heartbeat path, which is
// how an activated client refreshes its tunnel config every 5 minutes.
func TestHeartbeatResponseResolvesLiveUoTPort(t *testing.T) {
	var resp heartbeat.Response
	if err := json.Unmarshal([]byte(liveHeartbeatResponse), &resp); err != nil {
		t.Fatalf("live heartbeat payload no longer parses: %v", err)
	}
	if resp.ServerConfig == nil {
		t.Fatal("server_config did not decode — the client would have no tunnel settings")
	}
	if resp.ServerConfig.ServerPortUOT != 8446 {
		t.Errorf("heartbeat ServerPortUOT = %d, want 8446 — UoT is disabled for every "+
			"client by this regression (process.go gates on it)", resp.ServerConfig.ServerPortUOT)
	}
	if !resp.UDPRelay {
		t.Error("heartbeat UDPRelay = false, want true (the other half of the UoT gate)")
	}
	if resp.ServerConfig.ServerPort != 8445 {
		t.Errorf("ServerPort = %d, want 8445 — TCP must be unaffected", resp.ServerConfig.ServerPort)
	}
}

// TestActivationResponseResolvesLiveUoTPort covers the first-activation path. A
// client that activates onto strike must come up with UoT already enabled, not
// wait for the first heartbeat.
func TestActivationResponseResolvesLiveUoTPort(t *testing.T) {
	var resp activation.ActivateResponse
	if err := json.Unmarshal([]byte(`{
      "code": 200,
      "message": "Activation successful",
      "tier": "strike",
      "device_fingerprint": "abc",
      "udp_relay": true,
      "server_config": {
        "server": "networkingguides.duckdns.org",
        "server_port": 8445,
        "password": "3467ce5ae756c185f45be91fed2dbeb6",
        "method": "aes-256-gcm",
        "uot_port": 8446
      }
    }`), &resp); err != nil {
		t.Fatalf("activation payload no longer parses: %v", err)
	}
	if resp.ServerCfg == nil {
		t.Fatal("server_config did not decode")
	}
	if resp.ServerCfg.ServerPortUOT != 8446 {
		t.Errorf("activation ServerPortUOT = %d, want 8446", resp.ServerCfg.ServerPortUOT)
	}
}

// TestStorageRoundTripsUoTPort covers the on-disk path. storage.ServerConfig is
// the type the value is PERSISTED as; if it drops the field, an activated
// client connects without UoT even though activation returned it.
func TestStorageRoundTripsUoTPort(t *testing.T) {
	in := storage.ServerConfig{
		Server:        "networkingguides.duckdns.org",
		ServerPort:    8445,
		Password:      "p",
		Method:        "aes-256-gcm",
		ServerPortUOT: 8446,
	}
	blob, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out storage.ServerConfig
	if err := json.Unmarshal(blob, &out); err != nil {
		t.Fatalf("unmarshal own output: %v", err)
	}
	if out.ServerPortUOT != 8446 {
		t.Errorf("storage round trip lost the UoT port: %d, want 8446", out.ServerPortUOT)
	}

	// A config persisted with the HUB's spelling (hand-edited, or written by a
	// future build) must also be readable — this is the direction that broke.
	var fromHubKey storage.ServerConfig
	if err := json.Unmarshal([]byte(`{"server_port":8445,"uot_port":8446}`), &fromHubKey); err != nil {
		t.Fatalf("unmarshal hub-spelling config: %v", err)
	}
	if fromHubKey.ServerPortUOT != 8446 {
		t.Errorf("storage cannot read the hub's %q spelling: got %d, want 8446",
			uotkey.Canonical, fromHubKey.ServerPortUOT)
	}
}

// TestUoTOffForOrdinaryTiers pins that resolving UoT did not turn it on
// everywhere. eco and stealth must stay at 0, or the client would emit a UoT
// outbound aimed at a port with no sing-box listener.
func TestUoTOffForOrdinaryTiers(t *testing.T) {
	const eco = `{"server":"networkingguides.duckdns.org","server_port":8443,
	              "password":"340f1e1e4ca75e552bc406300c46c8a2","method":"aes-256-gcm"}`
	var resp heartbeat.Response
	if err := json.Unmarshal([]byte(`{"status":"ok","server_config":`+eco+`}`), &resp); err != nil {
		t.Fatalf("eco payload no longer parses: %v", err)
	}
	if resp.ServerConfig == nil {
		t.Fatal("eco server_config did not decode")
	}
	if resp.ServerConfig.ServerPortUOT != 0 {
		t.Errorf("eco ServerPortUOT = %d, want 0 — UoT must stay off for tiers "+
			"with no sing-box listener", resp.ServerConfig.ServerPortUOT)
	}
}
