# MyGo 0.3.7 incremental dependency review

Reviewed 2026-10-10 against the official release tag and v0.3.6...v0.3.7 comparison.
The tag was published on 2026-10-09 at 19:22 UTC, after the v0.3.6 upgrade in #16.
This is a new incremental update, not a repeat of #16/#17. Coordination remains
in Dependency Dashboard #11; the sole branch is `feat/mygo-0.3.7-upgrade`.

## Scope

- Move the linked MyGo module and terminal-source specification together to v0.3.7.
- Review Go module download/verification results and commit exact go.sum entries.
- Retain the existing 28 terminal input digests, hardening patch and Ghostty library
  hashes: the upstream tag comparison changes none of those files.
- Native layout, tooltip, table/list behaviour and CLI/DMG changes require the full
  existing regression matrix. The new TrackLines API is not yet an Aster feature.
- Read the application module version in the terminal devkit instead of silently
  downloading a previous hardcoded tag.
- Preserve resolved module metadata in `kit/evidence/resolved` separately from
  the exact git-source archive. Module verification/checksum protections remain enabled.

No other product dependencies change here. Go1.27.2, Kubernetes0.37.1, x/sys0.49.0
and the earlier reviewed updates are already on main. No floating latest,
unreviewed major upgrades, automatic merges or runtime native-code downloads.

## Verification gate

Validate module tidy without a tracked diff, go vet, MyGo native-lifetime vet,
Python build/evidence tests, full race/native interactions, real OS credential
roundtrips and locked/unavailable-provider cases, three pinned Kubernetes
versions, independent two-cluster isolation and three OS window smokes.
Verify source tree/checkout, generated adapter and actual native library digests.
Only the final commit's successful matrix can justify merging. Inspect main CI
again afterwards; a green regression matrix is not production certification.

## Recovery baseline

Vault #19 is merged as `929a27b8b426fdf72def12a6871d976a25f1b7fc` after final
CI #95 (`38015085505`) and source/artifact verification. The feature head was
`27cae6f7486b36411f0dfa70e6d2c2685bf9a001`, tree
`50c59fe1e7f508f23d4b97c94cf362e865a0fed5`. It includes default-Keychain binding,
locked-provider rejection, maximum binary blobs, password Undo erasure and CA-byte
review invalidation. Do not reopen or duplicate the completed vault branch.
OIDC/PKCE, rotation/revocation, enterprise control-plane and release qualification
are not implemented by that token-storage milestone and remain in #13.

Official sources:
- https://github.com/egoist/mygo/releases/tag/v0.3.7
- https://github.com/egoist/mygo/compare/v0.3.6...v0.3.7
