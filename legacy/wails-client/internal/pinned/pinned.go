// Package pinned enforces TLS certificate public-key pinning on the hub
// connections Locus makes (activation, heartbeat, update downloads).
//
// Why: Locus talks only to its own hub (networkingguides.duckdns.org), whose
// TLS is issued by a public CA. Without pinning, a compromised CA or forced
// DNS could MITM those calls. Pinning the leaf's Subject Public Key Info
// (SPKI) defeats CA-compromise MITM while staying robust to Let's Encrypt
// renewing the *certificate body* — the key is pinned, not the cert.
//
// Behaviour:
//   - When >=1 pin is configured, connections are FAIL-CLOSED: a peer whose
//     leaf SPKI is not on the allow-list is rejected. Go's normal certificate
//     chain validation is never disabled; the pin check is additive.
//   - When no pin is configured yet (fresh/factory build), the extra check is
//     disabled (normal TLS still enforced) and a one-time warning is logged so
//     a build keeps working until an operator captures and sets the pin.
//   - LOCUS_SKIP_PINNING=1 disables the extra pin check (capture/emergency).
//
// Capturing the pin for the live host (run from a trusted machine):
//
//	echo -n | openssl s_client -connect networkingguides.duckdns.org:443 \
//	  -servername networkingguides.duckdns.org 2>/dev/null \
//	  | openssl x509 -pubkey -noout \
//	  | openssl pkey -pubin -outform der \
//	  | openssl dgst -sha256 -binary \
//	  | openssl base64
//
// then pass that base64 string to pinned.Load(<list>, skipEnv).
package pinned

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"log"
	"strings"
	"sync"
)

var (
	mu       sync.RWMutex
	pins     []string
	skip     bool
	warnOnce sync.Once
)

// Skip reports whether the pin check was disabled via LOCUS_SKIP_PINNING.
func Skip() bool { mu.RLock(); defer mu.RUnlock(); return skip }

// Load sets the SPKI allow-list from a comma/newline separated string of
// base64 SHA-256 SPKI hashes. skipDisabled forces the check off.
func Load(list string, skipDisabled bool) {
	mu.Lock()
	defer mu.Unlock()
	skip = skipDisabled
	pins = parseList(list)
	configured := len(pins) > 0
	switch {
	case skip:
		log.Printf("pinned: LOCUS_SKIP_PINNING set — hub TLS pin check disabled")
	case !configured:
		warnOnce.Do(func() {
			log.Printf("pinned: no hub SPKI pins configured — extra TLS pin check disabled (fail-open until set)")
		})
	default:
		log.Printf("pinned: hub TLS pinning active (%d pin(s))", len(pins))
	}
}

// parseList splits on commas/newlines/whitespace, trims, and drops malformed
// base64 entries (each must decode as a 32-byte SHA-256 digest).
func parseList(s string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '\n' || r == ' '
	}) {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		// Tolerate a missing trailing '='.
		for _, cand := range []string{p, p + "="} {
			b, err := base64.StdEncoding.DecodeString(cand)
			if err == nil && len(b) == sha256.Size {
				out = append(out, base64.StdEncoding.EncodeToString(b))
				break
			}
		}
	}
	return out
}

// AddPinTLSVerification clones cfg so that, in addition to Go's normal chain
// validation, the presented leaf's SPKI must be on the pin allow-list. When no
// pins are configured the clone behaves normally (fail-open), preserving TLS
// security. Return the clone for use as the transport's TLSClientConfig.
func AddPinTLSVerification(cfg *tls.Config) *tls.Config {
	c := cfg.Clone()
	base := c.VerifyPeerCertificate
	c.VerifyPeerCertificate = func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
		if Skip() {
			return nil
		}
		mu.RLock()
		p := append([]string(nil), pins...)
		mu.RUnlock()
		if len(p) == 0 || len(rawCerts) == 0 {
			return nil // fail-open until configured / nothing presented
		}
		// Hash the leaf's Subject Public Key Info (SPKI), not the whole cert — the
		// operator captures the same value via "openssl x509 -pubkey … | sha256".
		leaf, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("pinned: cannot parse server certificate: %w", err)
		}
		sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
		got := base64.StdEncoding.EncodeToString(sum[:])
		for _, want := range p {
			if got == want {
				return nil
			}
		}
		return fmt.Errorf("pinned: server certificate SPKI (%s…) not in allow-list — possible MITM", got[:min(12, len(got))])
	}
	_ = base
	return c
}

// TLSClientConfig returns a *tls.Config with the hub SPKI pin check attached,
// safe to reuse across http.Transports (pin logic reads the shared state at
// connection time). Empty until Load() is called.
func TLSClientConfig() *tls.Config {
	return AddPinTLSVerification(&tls.Config{})
}
