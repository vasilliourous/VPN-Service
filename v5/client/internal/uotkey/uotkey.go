// Package uotkey holds the single definition of the UDP-over-TCP (UoT)
// endpoint field name used between the Locus hub and the client.
//
// # WHY THIS PACKAGE EXISTS
//
// The hub sends the UoT endpoint as "uot_port", inside the tier's config JSON,
// which both /api/activate and /api/heartbeat pass through verbatim:
//
//	"server_config": { "server": "...", "server_port": 8445,
//	                   "password": "...", "method": "aes-256-gcm",
//	                   "uot_port": 8446 }
//
// The client historically declared only "server_port_uot". Go's encoding/json
// silently ignores unknown keys, so the value never landed, ServerPortUOT was
// permanently 0, and manager/process.go's `uotEnabled := cfg.UDPRelay &&
// cfg.ServerPortUOT > 0` was therefore always false. UDP-over-TCP — the entire
// mechanism behind the Strike gaming tier — was never active on any build, on
// any platform, while the server correctly advertised a live listener.
//
// Nothing caught it: no test asserted that the client parses the key the server
// emits, and both sides were individually correct. That is the failure mode
// this package removes. There is now ONE place that knows the wire name.
//
// # THE WIRE KEY IS FROZEN
//
// "uot_port" cannot be renamed: already-deployed clients are in the field
// reading it, and an unknown-to-them key means a silent no-op, not an error.
// The legacy "server_port_uot" is still accepted so a future server-side rename
// cannot break this build the same way in reverse.
package uotkey

import "encoding/json"

// Canonical is the key the hub emits today. Frozen — do not rename.
// Every other spelling is tolerated, never preferred.
const Canonical = "uot_port"

// Legacy is the key this client used to require and never received. Accepted
// for forward-compatibility only; nothing on the server sends it.
const Legacy = "server_port_uot"

// Port extracts the UoT endpoint from an already-decoded server config map.
// Returns 0 when the tier has no UoT endpoint, which is the normal case for the
// eco and stealth tiers (and for the whole fleet until the server enables one).
//
// Both keys are probed. Canonical wins; Legacy is a fallback so a future
// server-side rename in the other direction is survivable.
func Port(raw map[string]any) int {
	for _, k := range []string{Canonical, Legacy} {
		switch v := raw[k].(type) {
		case float64: // JSON numbers decode to float64 by default
			if int(v) > 0 {
				return int(v)
			}
		case int:
			if v > 0 {
				return v
			}
		}
	}
	return 0
}

// ServerConfig is the shape every consumer of a tier's server_config shares.
// It is embedded (or aliased) by activation.ServerConfig, heartbeat.ServerConfig
// and storage.ServerConfig so the three cannot drift apart again.
//
// ServerPortUOT is deliberately tagged with the LEGACY name so that a payload
// which happens to use it still works via plain json.Unmarshal; the canonical
// name is resolved by Decode below.
type ServerConfig struct {
	Server        string `json:"server"`
	ServerPort    int    `json:"server_port"`
	Password      string `json:"password"`
	Method        string `json:"method"`
	ServerPortUOT int    `json:"server_port_uot,omitempty"`
}

// Decode unmarshals a tier's server_config, resolving the UoT endpoint
// regardless of which of the two spellings the hub used.
//
// This is the only sanctioned way to read a server config. Calling
// json.Unmarshal directly on a struct that merely declares `server_port_uot`
// reintroduces the original bug: the hub's "uot_port" is ignored without error.
func Decode(data []byte) (ServerConfig, error) {
	var cfg ServerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ServerConfig{}, err
	}
	// Second pass: the key names are not known to the struct tags, so read the
	// raw map to find them.
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return ServerConfig{}, err
	}
	// Resolve CANONICALLY, overwriting whatever json.Unmarshal produced from
	// the legacy tag. Probing the map is order-independent, so a payload that
	// carries both keys resolves the same way every time — whereas letting the
	// struct tag participate would make precedence depend on Go's map iteration
	// order. If neither key is present, this is 0, which is the correct
	// "no UoT endpoint" answer.
	cfg.ServerPortUOT = Port(raw)
	return cfg, nil
}

// DecodeInto unmarshals a server_config into dst AND resolves the UoT endpoint,
// for callers that need their own richer struct (with extra fields or methods).
//
// dst must have a ServerPortUOT int field. It is addressed through the
// ServerConfig value returned by Decode rather than by reflection, so the
// caller does the assignment explicitly and the compiler checks it.
func DecodeInto(data []byte, dst *ServerConfig) error {
	cfg, err := Decode(data)
	if err != nil {
		return err
	}
	*dst = cfg
	return nil
}
