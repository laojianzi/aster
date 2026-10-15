# go-jose v4.1.6 security and compatibility review

Date: 2026-10-15 UTC. Scope: the existing Renovate PR #73 and Dashboard #11.
Base: accepted main `a8422d80af4e434558639d1893d1406f60f8c922`.
Continue the sole `renovate/github.com-go-jose-go-jose-v4-4.x` branch; do not
combine the independent pending MyGo v0.5.1 update or create another JOSE PR.

## Reason and application reachability

The upstream [security advisory](https://github.com/go-jose/go-jose/security/advisories/GHSA-mgcc-cww5-fp2q)
identifies excessive CPU in RSA JWK private-key validation, with v4.1.6 fixing
the affected v4 versions through v4.1.5. The [release notes](https://github.com/go-jose/go-jose/releases/tag/v4.1.6)
explicitly introduce a default 4096-bit RSA JWK modulus limit. This security
update takes priority over starting another Helm lifecycle feature.

Aster's supported OIDC path already rejects private/symmetric JWK fields
`d`, `p`, `q`, `dp`, `dq`, `qi`, `oth`, `k` before invoking JOSE. It projects a
bounded public-field allowlist, not certificate chains. Before verification,
`validateBoundIdentity` permits only `alg`, `kid`, `typ` JWT headers; embedded
`jwk`, `jku` and `x5c` never reach its JOSE header parser. These are relevant
pre-existing defenses, not a demonstrated exploit or a whole-product claim of
non-exposure. Keep them, the current issuer transport and private error contract.
No expensive denial-of-service payload is run against any live provider.

## Intentional RSA compatibility change

RS256 now accepts **2048–4096-bit RSA** public keys in both login and renewal.
The previously advertised upper bound was 8192 bits. Issuers relying on larger
keys will be refused; there is no silent compatibility fallback. ES256 remains
P-256. Do not increase `jose.MaxRSAModulusBits` at initialization or runtime.

The application independently caps encoded `n` material at 683 bytes before
JOSE parsing, and checks the resulting bit length against its own 2048/4096
constants. This cap is the unpadded base64url length of a 512-byte modulus.
It retains the prior JSON escape/input budgets and does not normalize key
material or change issuer, audience, nonce, subject or algorithm validation.
Both flows return the existing private `ErrVerification` on refusal.

## Source and checksum provenance

The [upstream comparison](https://github.com/go-jose/go-jose/compare/v4.1.5...v4.1.6)
contains four commits and 13 changed paths. The v4.1.6 tag resolves to
`a0111f82742fa92bceec9ede40845e73319e6230`. Besides JWK bounds and private-key
checks, the changed JWS/JWE headers propagate public-only parsing errors;
JWE validates content-encryption-key lengths before decryption. Aster's login
path uses signed tokens, not JWE decryption. The `jwt/errors.go` wording change
is not in the vendored package closure used by this application; the container,
threat model and upstream tests are not application behavior changes.

Local sources come from the existing pinned, read-only
[offline-kit run 38678900684](https://github.com/laojianzi/aster/actions/runs/38678900684),
for bot head `6fa6abc3ca476d763bde584967594d3c5b5a3862`. That workflow runs
`go mod download all`, `go mod verify`, then vendors the selected public graph,
recording resolved metadata separately from the exact tracked source archive.
Artifact 11947797096 has 80,857,907 bytes and SHA-256
`2d1d45118b2441c2a5400bce85cc0d1f7c7c9f74c3f21c952994fdf7a85f7ec1`.
The enclosed kit tar has SHA-256
`4fc0814126e37ccf468d416e7e4244c9df355a4ac3363652d6fb73275db53126`.
ZIP length/digest/CRC, tar checksum, recorded head and Go 1.27.2 were checked.

The two v4.1.6 checksums from that verified resolved go.sum are:

```text
github.com/go-jose/go-jose/v4 v4.1.6 h1:sNRkftxFJxwsILiQukqp5z83QBSmntVwmgRY7AqBDO4=
github.com/go-jose/go-jose/v4 v4.1.6/go.mod h1:x4oUasVrzR7071A4TnHLGSPpNOm2a21K9Kf04k1rs08=
```

The final committed metadata matches the exact bot head's independent
[Source run 38678907189](https://github.com/laojianzi/aster/actions/runs/38678907189)
`go mod tidy` output, not the kit's larger `go mod download all` checksum set.
Its original artifact 11947267439 has 730,846 bytes and SHA-256
`1436834f5eb03d83f56f8768f8c45cce9fa6baaecc37b8fbfc442bd72c9a0298`.
The source checkout `9ea3b1e8018c561600d383d0bb85d2ba294c2c74`, archive checksum
and all tracked blobs match the bot's one-line module update. Tidy replaces the
two obsolete v4.1.5 entries with v4.1.6; every other checksum is unchanged.
The five changed production files in the compiled root package
were also independently Git-blob-hashed against the exact upstream tree:

| File | Exact upstream Git blob |
| --- | --- |
| `jwk.go` | `8b7aa05ec2ba1b1e160af3bf515f2f1f5b34e114` |
| `jws.go` | `18224df7d92efadad678e3504a603b9ca054d961` |
| `shared.go` | `1e8bf88210e6530195c6d5e52d2f53ff4986e7d6` |
| `jwe.go` | `a5169010787d90f60ee9731991ede442828dc2f9` |
| `crypter.go` | `53636c57b33533faaa73eb1f6ecf256fb4076af5` |

This is verified CI module provenance plus exact changed compiled-file identity,
not a claimed separate full module-ZIP byte audit. Direct module/diff downloads
were unavailable in this environment. No vendor directory, toolchain, native
library, credentials or generated runtime files are committed. Other dependency,
MyGo/native/Helm pins and workflow permissions remain unchanged.

## Regression and completion requirements

`TestOIDCJOSE416RSAAndES256Compatibility` uses independent standard-library RSA
signatures: supported 2048/4096-bit keys in initial and bound renewal validation,
1024-bit refusal, bounded synthetic 4097/8192-bit public-material refusal in both
paths, ES256 compatibility and the unchanged upstream global limit. It does not
generate over-budget private keys. Existing protocol renewal tests continue to
cover token rotation, nonce omission, single-flight, cancellation and identity.

`TestOIDCJOSE416RejectsPrivateAndEmbeddedKeys` verifies private/symmetric field
presence (including null) is refused; certificate fields are not projected;
and validly signed tokens containing small public/private embedded JWKs or
other unreviewed headers are rejected. It uses ephemeral bounded test keys.

Both new tests are additive mandatory names in unit and OIDC qualification.
All existing native, Helm, migration, real-cluster and security gates remain.
Python scope tests check the exact pin/sums, mandatory names, lack of a direct
global-limit override and the registered PR73 branch's rejection guards. The
retirement registration also requires the fifth exact-main Renovate gate.
If Renovate removes the branch first, record `already-absent`, preserve the
merge history and do not invent an applied archive or a maintenance tag.

The bot-only head failed OIDC setup with missing go.sum, before authentication
tests ran. Preserve that failed result. Local testing uses the matching offline
Go 1.27.2 kit with `-mod=vendor`; a first full-suite tool invocation was interrupted
by its execution limit and is not a pass. Record successful subsequent full,
OIDC-race, Python and E2E-compilation results on the actual PR. Compilation alone
is not cluster execution; the local race run is OIDC-only, not full application.

Final corrected-head CI/Source/native-migration/OIDC/Renovate and independently
qualified fresh main remain required. Old PR72 results qualify its old dependency
graph only. This document is not a claim of merge, completed release certification
or a vulnerability-free product. Dashboard #11 stays open while any real update
(including independent MyGo) remains; tracker #13 and its remaining MVP/release
criteria remain open. Enterprise authority stays P3 deferred/outside first MVP.
