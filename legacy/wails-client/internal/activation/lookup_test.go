package activation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// validCode returns a checksum-correct code for the shared test charset.
func validCode(t *testing.T) string {
	t.Helper()
	// Build a code with a correct Luhn-mod-N check character so LookupCode's
	// local precheck passes and we exercise the HTTP path.
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	body := "RQABCD2345EFGH"[:14]
	sum, alt := 0, false
	for i := len(body) - 1; i >= 0; i-- {
		idx := strings.IndexByte(charset, body[i])
		if idx < 0 {
			t.Fatalf("test body has non-charset byte %q", body[i])
		}
		v := idx
		if alt {
			v *= 2
			if v >= len(charset) {
				v = v - len(charset) + 1
			}
		}
		sum += v
		alt = !alt
	}
	check := charset[(len(charset)-(sum%len(charset)))%len(charset)]
	return body + string(check)
}

// TestLookupCodeParsesStatuses locks in the client/server contract for the
// read-only code lookup: each server status must map onto the LookupStatus the
// UI switches on.
func TestLookupCodeParsesStatuses(t *testing.T) {
	cases := []struct {
		wire   string
		expect LookupStatus
	}{
		{"unbound", LookupUnbound},
		{"ok", LookupOK},
		{"bound_this_device", LookupBoundThisDevice},
		{"bound_other", LookupBoundOther},
		{"suspended", LookupSuspended},
		{"expired", LookupExpired},
		{"not_found", LookupNotFound},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/code-lookup" {
				t.Errorf("unexpected path %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":  tc.wire,
				"tier":    "strike",
				"message": "test",
			})
		}))
		c := NewClient(srv.URL)
		resp, err := c.LookupCode(context.Background(), validCode(t), "fingerprint-abcdef0123456789")
		srv.Close()
		if err != nil {
			t.Fatalf("status %q: LookupCode: %v", tc.wire, err)
		}
		if resp.Status != tc.expect {
			t.Errorf("status %q -> %q, want %q", tc.wire, resp.Status, tc.expect)
		}
		if resp.Tier != "strike" {
			t.Errorf("status %q: tier = %q, want strike", tc.wire, resp.Tier)
		}
	}
}

// TestLookupCodeRejectsBadFormat ensures malformed codes never reach the network.
func TestLookupCodeRejectsBadFormat(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	c := NewClient(srv.URL)

	if _, err := c.LookupCode(context.Background(), "RQ-ABCD-EFGH-IJKL-M", "fingerprint-abcdef0123456789"); err == nil {
		t.Fatal("expected an error for a bad checksum")
	}
	if called {
		t.Fatal("malformed code must not hit the network")
	}
}

// TestLookupCodeMissingEndpointIsUnreachable ensures a hub that predates the
// endpoint yields ErrServerUnreachable (which the app maps to Known=false), not
// a false "code not found".
func TestLookupCodeMissingEndpointIsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)

	_, err := c.LookupCode(context.Background(), validCode(t), "fingerprint-abcdef0123456789")
	if err == nil {
		t.Fatal("expected an error for a 404 endpoint")
	}
	if !strings.Contains(err.Error(), ErrServerUnreachable.Error()) {
		t.Fatalf("error %q should wrap ErrServerUnreachable", err.Error())
	}
}
