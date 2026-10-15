package oidclogin

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/laojianzi/aster/internal/testoidc"
)

// Sign with the standard library rather than using the JOSE implementation
// under review. Keys are bounded, ephemeral test inputs, never external input.
func rsaIdentityFixture(t *testing.T, bits int, extra map[string]any) (string, map[string]any) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal("generate bounded RSA fixture:", err)
	}
	header := map[string]any{"alg": "RS256", "kid": "fixture-key", "typ": "JWT"}
	for k, v := range extra {
		header[k] = v
	}
	h, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := json.Marshal(map[string]any{
		"iss": "https://issuer.invalid", "sub": "fixture-subject", "aud": "aster-test",
		"nonce": "nonce", "iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(claims)
	hash := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatal("sign bounded RSA fixture:", err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature), map[string]any{
		"kty": "RSA", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e": "AQAB", "kid": "fixture-key", "alg": "RS256", "use": "sig",
	}
}

func rsaIdentityKeys(t *testing.T, key map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"keys": []any{key}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOIDCJOSE416RSAAndES256Compatibility(t *testing.T) {
	// A future dependency or application change must not silently raise this
	// reviewed limit. Do not modify the global setting, including in tests.
	if jose.MaxRSAModulusBits != 4096 || minRSAModulusBits != 2048 || maxRSAModulusBits != 4096 || maxEncodedRSAModulusBytes != 683 {
		t.Fatal("unreviewed RSA parser policy")
	}
	for _, bits := range []int{1024, 2048, 4096} {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			raw, key := rsaIdentityFixture(t, bits, nil)
			info, binding, err := validateBoundIdentity(context.Background(), "https://issuer.invalid", "aster-test", "nonce", raw, "access", rsaIdentityKeys(t, key), time.Now(), nil)
			if bits == 1024 {
				if !errors.Is(err, ErrVerification) {
					t.Fatal("below-minimum signed RSA token was not privately refused")
				}
			} else if err != nil || info.Subject != "fixture-subject" {
				t.Fatal("supported signed RSA token refused:", err)
			}
			if bits >= minRSAModulusBits {
				renewed, _, err := validateBoundIdentity(context.Background(), "https://issuer.invalid", "aster-test", "nonce", raw, "access", rsaIdentityKeys(t, key), time.Now(), &binding)
				if err != nil || renewed.Subject != info.Subject {
					t.Fatal("supported RSA renewal verification refused:", err)
				}
			}
			if bits == 4096 {
				// 4097-bit and former 8192-bit public material is refused without
				// generating large private keys or timing an expensive DoS.
				for _, width := range []int{513, 1024} {
					t.Run(fmt.Sprintf("reject-%d-bytes", width), func(t *testing.T) {
						modulus := make([]byte, width)
						modulus[0] = 0x80
						if width == 513 {
							modulus[0] = 1
						}
						modulus[width-1] = 1
						changed := map[string]any{}
						for k, v := range key {
							changed[k] = v
						}
						changed["n"] = base64.RawURLEncoding.EncodeToString(modulus)
						if _, err := validateIdentity(context.Background(), "https://issuer.invalid", "aster-test", "nonce", raw, "access", rsaIdentityKeys(t, changed), time.Now()); !errors.Is(err, ErrVerification) {
							t.Fatal("over-budget RSA material was not privately refused")
						}
						if _, _, err := validateBoundIdentity(context.Background(), "https://issuer.invalid", "aster-test", "nonce", raw, "access", rsaIdentityKeys(t, changed), time.Now(), &binding); !errors.Is(err, ErrVerification) {
							t.Fatal("over-budget RSA renewal material was not privately refused")
						}
					})
				}
			}
		})
	}
	t.Run("ES256", func(t *testing.T) {
		p := testoidc.New(t)
		info, err := validateIdentity(context.Background(), p.Server.URL, "aster-test", "nonce", p.Sign(p.Claims("nonce"), nil), "access", p.Keys(), time.Now())
		if err != nil || info.Subject != "fixture-subject" {
			t.Fatal("ES256 compatibility lost:", err)
		}
	})
}

func TestOIDCJOSE416RejectsPrivateAndEmbeddedKeys(t *testing.T) {
	raw, key := rsaIdentityFixture(t, 2048, nil)
	for _, field := range []string{"d", "p", "q", "dp", "dq", "qi", "oth", "k"} {
		t.Run("private-"+field, func(t *testing.T) {
			for _, value := range []any{nil, "AQAB"} {
				changed := map[string]any{}
				for k, v := range key {
					changed[k] = v
				}
				changed[field] = value
				if _, err := validateIdentity(context.Background(), "https://issuer.invalid", "aster-test", "nonce", raw, "access", rsaIdentityKeys(t, changed), time.Now()); !errors.Is(err, ErrVerification) {
					t.Fatal("private/symmetric parameter was not privately refused")
				}
			}
		})
	}
	t.Run("certificate-not-parsed", func(t *testing.T) {
		key["x5c"] = []string{"not-a-certificate"}
		if _, err := validateIdentity(context.Background(), "https://issuer.invalid", "aster-test", "nonce", raw, "access", rsaIdentityKeys(t, key), time.Now()); err != nil {
			t.Fatal("certificate chain entered public-key projection")
		}
		delete(key, "x5c")
	})
	// The token itself is validly signed. Even small embedded public/private
	// JWKs are refused as headers before JOSE; no expensive RSA fixture is used.
	for _, extra := range []map[string]any{
		{"jwk": map[string]any{"kty": "RSA", "n": "AQAB", "e": "AQAB", "d": "AQAB", "p": "Aw", "q": "Aw"}},
		{"jwk": map[string]any{"kty": "RSA", "n": "AQAB", "e": "AQAB"}},
		{"jku": "https://unreviewed.invalid/keys"},
		{"x5c": []string{"not-a-certificate"}},
	} {
		for field := range extra {
			t.Run("header-"+field, func(t *testing.T) {
				token, public := rsaIdentityFixture(t, 2048, extra)
				if _, err := validateIdentity(context.Background(), "https://issuer.invalid", "aster-test", "nonce", token, "access", rsaIdentityKeys(t, public), time.Now()); !errors.Is(err, ErrVerification) {
					t.Fatal("unreviewed JOSE header was not privately refused")
				}
			})
		}
	}
}
