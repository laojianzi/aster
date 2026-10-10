package oidclogin

import (
	"context"
	"encoding/json"
	"github.com/laojianzi/aster/internal/testoidc"
	"strings"
	"testing"
)

func TestOIDCKeyMaterialBudgetsBeforeCryptoParsing(t *testing.T) {
	p := testoidc.New(t)
	token := p.Sign(p.Claims("nonce"), nil)
	for _, fields := range []map[string]any{
		{"kty": "RSA", "n": strings.Repeat("A", 1367), "e": "AQAB"},
		{"kty": "RSA", "n": "AQAB", "e": "AQAB", "d": strings.Repeat("A", 1024)},
		{"kty": "oct", "k": "c2VjcmV0"},
		{"kty": "EC", "crv": "P-521", "x": "AQAB", "y": "AQAB"},
	} {
		b, _ := json.Marshal(map[string]any{"keys": []any{fields}})
		if _, e := verifySignedToken(context.Background(), p.Server.URL, "aster-test", "ES256", "fixture-key", token, b); e == nil {
			t.Fatal("unsafe key accepted")
		}
	}
	if _, e := Discover(context.Background(), Options{Issuer: p.Server.URL, ClientID: "aster-test", TargetKey: strings.Repeat("a", 64), CAPEM: p.CA, Port: 80}); e == nil {
		t.Fatal("privileged callback port accepted")
	}
}
