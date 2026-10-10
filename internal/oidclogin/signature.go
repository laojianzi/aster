package oidclogin

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/json"
	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
)

// verifySignedToken uses only a bounded, explicitly fetched issuer key set.
// Untrusted JWT headers cannot trigger network resolution or choose a MAC key.
func verifySignedToken(ctx context.Context, issuer, clientID, algorithm, keyID, raw string, data []byte) (*oidc.IDToken, error) {
	if ctx == nil || (algorithm != "RS256" && algorithm != "ES256") || len(keyID) > 256 || len(raw) > MaxTokenBytes {
		return nil, ErrVerification
	}
	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if decodeJSON(ctx, data, &set) != nil || len(set.Keys) == 0 || len(set.Keys) > 8 {
		return nil, ErrVerification
	}
	keys := []crypto.PublicKey{}
	for _, rawKey := range set.Keys {
		// Bound integer material before the cryptographic parser and never let
		// private factors or certificate chains select expensive parsing paths.
		var fields map[string]json.RawMessage
		if decodeJSON(ctx, rawKey, &fields) != nil {
			return nil, ErrVerification
		}
		for _, name := range []string{"d", "p", "q", "dp", "dq", "qi", "oth", "k"} {
			if _, exists := fields[name]; exists {
				return nil, ErrVerification
			}
		}
		var kind string
		if json.Unmarshal(fields["kty"], &kind) != nil || (kind != "RSA" && kind != "EC") {
			return nil, ErrVerification
		}
		bounded := map[string]string{"kty": kind}
		for name, limit := range map[string]int{"kid": 256, "alg": 16, "use": 8, "n": 1366, "e": 8, "crv": 8, "x": 43, "y": 43} {
			if value, exists := fields[name]; exists {
				var text string
				if len(value) > limit*6+2 || json.Unmarshal(value, &text) != nil || len(text) > limit {
					return nil, ErrVerification
				}
				bounded[name] = text
			}
		}
		if kind == "EC" && bounded["crv"] != "P-256" {
			return nil, ErrVerification
		}
		projected, _ := json.Marshal(bounded)
		var key jose.JSONWebKey
		if json.Unmarshal(projected, &key) != nil {
			return nil, ErrVerification
		}
		if len(key.KeyID) > 256 || !key.IsPublic() {
			return nil, ErrVerification
		}
		if keyID != "" && key.KeyID != keyID {
			continue
		}
		if key.Use != "" && key.Use != "sig" {
			continue
		}
		if key.Algorithm != "" && key.Algorithm != algorithm {
			continue
		}
		switch pub := key.Key.(type) {
		case *rsa.PublicKey:
			if algorithm == "RS256" && pub.N != nil && pub.N.BitLen() >= 2048 && pub.N.BitLen() <= 8192 && pub.E >= 3 && pub.E <= 1<<31-1 && pub.E%2 == 1 {
				keys = append(keys, pub)
			}
		case *ecdsa.PublicKey:
			if algorithm == "ES256" && pub.Curve == elliptic.P256() && pub.X != nil && pub.Y != nil && pub.Curve.IsOnCurve(pub.X, pub.Y) {
				keys = append(keys, pub)
			}
		}
	}
	if len(keys) == 0 {
		return nil, ErrVerification
	}
	verifier := oidc.NewVerifier(issuer, &oidc.StaticKeySet{PublicKeys: keys}, &oidc.Config{ClientID: clientID, SupportedSigningAlgs: []string{algorithm}})
	verified, e := verifier.Verify(ctx, raw)
	if e != nil {
		return nil, ErrVerification
	}
	return verified, nil
}
