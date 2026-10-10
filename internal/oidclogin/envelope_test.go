package oidclogin

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/laojianzi/aster/internal/testoidc"
)

func TestOIDCKeyOperationsRestrictVerification(t *testing.T) {
	p := testoidc.New(t)
	token := p.Sign(p.Claims("nonce"), nil)
	cases := []struct {
		name       string
		operations any
		wantOK     bool
	}{
		{"verify", []string{"verify"}, true},
		{"encrypt-only", []string{"encrypt"}, false},
		{"sign-only", []string{"sign"}, false},
		{"empty", []string{}, false},
		{"null", nil, false},
		{"wrong-type", "verify", false},
		{"wrong-element", []any{"verify", 42}, false},
		{"duplicate", []string{"verify", "verify"}, false},
		{"budget", []string{"verify", "1", "2", "3", "4", "5", "6", "7", "8"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var set map[string][]map[string]any
			if err := json.Unmarshal(p.Keys(), &set); err != nil {
				t.Fatal(err)
			}
			set["keys"][0]["key_ops"] = tc.operations
			data, err := json.Marshal(set)
			if err != nil {
				t.Fatal(err)
			}
			_, err = verifySignedToken(context.Background(), p.Server.URL, "aster-test", "ES256", "fixture-key", token, data)
			if (err == nil) != tc.wantOK {
				t.Fatalf("verification accepted=%v; want=%v", err == nil, tc.wantOK)
			}
		})
	}
}

func TestOIDCTokenResponseRequiresSuccessfulEnvelope(t *testing.T) {
	cases := map[string]func(map[string]any){
		"missing-access": func(m map[string]any) { delete(m, "access_token") },
		"empty-access":   func(m map[string]any) { m["access_token"] = "" },
		"null-access":    func(m map[string]any) { m["access_token"] = nil },
		"numeric-access": func(m map[string]any) { m["access_token"] = 42 },
		"control-access": func(m map[string]any) { m["access_token"] = "do-not-leak\n" },
		"error-present":  func(m map[string]any) { m["error"] = nil },
		"error-empty":    func(m map[string]any) { m["error"] = "" },
		"missing-id":     func(m map[string]any) { delete(m, "id_token") },
		"wrong-type":     func(m map[string]any) { m["token_type"] = "MAC" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			p := testoidc.New(t)
			p.EditTokenResponse = edit
			r := reviewed(t, p)
			identity, err := r.Login(context.Background(), p.Open)
			if identity != nil {
				identity.Close()
			}
			if err == nil || identity != nil {
				t.Fatal("malformed token success response accepted")
			}
			if p.TokenRequests.Load() != 1 || p.KeyRequests.Load() != 0 {
				t.Fatal("malformed response retried or reached key verification")
			}
		})
	}
}
