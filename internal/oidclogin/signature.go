package oidclogin

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
)

// verifySignedToken uses only a bounded, explicitly fetched issuer key set.
// Untrusted JWT headers cannot trigger network resolution or choose a MAC key.
func verifySignedToken(ctx context.Context, issuer, clientID, algorithm, keyID, raw string, data []byte) (*oidc.IDToken, error) {
	if ctx == nil || (algorithm != "RS256" && algorithm != "ES256") || len(keyID) > 256 || len(raw) > MaxTokenBytes {
		return nil, ErrVerification
	}
	var set jose.JSONWebKeySet
	if decodeJSON(ctx, data, &set) != nil || len(set.Keys) == 0 || len(set.Keys) > 8 {
		return nil, ErrVerification
	}
	keys := []crypto.PublicKey{}
	for _, key := range set.Keys {
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
