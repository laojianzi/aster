# Explicit OS token storage

This development milestone implements Issue #18, alongside the existing bounded
exec authentication. It is not OIDC, refresh-token rotation, enterprise revocation,
or production release qualification.

## User workflow

Use a dedicated HTTPS context with a credential-free named user, for example:

```yaml
apiVersion: v1
kind: Config
current-context: team-vault
clusters:
- name: team
  cluster:
    server: https://your-api-server:6443
    certificate-authority-data: <your-base64-CA-bundle>
contexts:
- name: team-vault
  context:
    cluster: team
    user: team-vault-user
    namespace: team
users:
- name: team-vault-user
  user: {}
```

Leave the CA field out only when the server uses a trusted system CA. A CA file
reference requires explicit in-memory trust and is bounded/snapshotted. Never
put a token into this profile to enable the vault: mixed token, exec, certificate,
basic, impersonated, proxy or insecure configurations are refused, not replaced.
Existing kubeconfig credential flows remain unchanged.

1. Disconnect this workspace, open **Credentials**, and **Review credential target**.
   Check the HTTPS server, context and user alias. Review sends no cluster request
   and performs no OS-store lookup. It does not persist a trust decision.
2. Enter a token in the masked input, select a local lifetime, type the exact context
   name and choose **Store token**. Input and confirmation are cleared on submission.
   Storage does not log in or rewrite kubeconfig.
3. Confirm the context again and choose **Connect with stored token**. The profile
   is reread and the target digest must still match; no silent load or fallback.
4. After disconnecting, review and confirm the target to **Forget stored token**.
   This removes one OS slot, not the token at its issuer. Other workspaces and
   applications are not remotely revoked by this action.

Tokens are limited to 1,800 ASCII bearer-token characters so the versioned envelope
fits Windows' 2,560-byte generic credential blob limit. Local storage lifetimes are
15 minutes, 1 hour, 8 hours or 24 hours. This is a user-chosen *upper bound*, not proof
of the token's server-side expiry, validity or permissions. Tokens may fail earlier.
Connections are limited to the earlier of stored expiry and 15 minutes. Existing
connection cancellation, resource/stream cleanup and transport expiry guards apply;
there is no silent renewal.

## Isolation and storage

The slot digest binds the exact HTTPS URL, TLS server name, SHA-256 of the loaded CA
bytes (or the system-trust choice), context and user alias. Namespace is intentionally
not part of an identity: switching namespace never upgrades Kubernetes RBAC. The
profile is revalidated before every store/load/forget. Configuration files are never
rewritten. Changed or mixed identities require a new explicit review.

Providers use local OS APIs: Windows Credential Manager, macOS file-based Keychain,
and Linux libsecret/Secret Service. Linux requires `libsecret-1.so.0` and an existing
unlocked default collection; Aster does not create a fallback store or request an
unlock. macOS helper interaction is disabled; inaccessible/locked items fail closed.
A Linux provider can change state or require a prompt after the lock precheck; it
is not auto-approved. The helper deadline may then produce an uncertain result.

These stores protect against ordinary plaintext persistence, not malware already
running as the same OS user. Classic macOS Keychain is not the entitlement-backed
Data Protection Keychain. Signed application access/upgrade behaviour remains a
release-qualification requirement; this milestone does not relax OS ACLs.

## Helper and cancellation contract

All native calls run in a same-binary helper before GUI or kubeconfig initialization.
Only a fixed flag appears in argv. Token records travel through private stdin/stdout
pipes, never command arguments or environment. Inherited environment is restricted;
provider errors and stderr are not copied into UI messages. There is no shell,
credential logging, plaintext fallback or runtime download of credential code.

The request/response limit is 4 KiB, native record limit 2,560 bytes, helper deadline
20 seconds, and global helper admission two. Each workspace permits one pending
operation until its worker joins. Process groups / Windows Job Objects reuse the
existing authentication lifecycle controls. These limits do not constitute a sandbox
or bound native-library RSS/CPU, and cancellation cannot undo an OS write/delete that
already committed. Indeterminate results require explicit review, never automatic
retry. A changed or closed view cannot publish an old result into a new identity.

Input strings and Go/native temporary allocations are not guaranteed securely erased.
The app clears UI references and mutable byte buffers where possible; OS policy and
disk encryption remain relevant. No logs, resource bodies or kubeconfig credentials
are migrated to the vault automatically.

## Verification

Unit tests cover mixed-identity rejection, binding changes, record parsing and expiry,
helper budgets, sanitized failures and Native confirmation/cancellation. Native GUI
unit tests use explicit test storage and are not OS credential integration.

CI additionally runs `-tags=osvault` under `scripts/with_test_vault.py`: a private
D-Bus/ephemeral keyring on Linux; an ephemeral keychain with restored search/default
on macOS; random per-test slots with mandatory cleanup on Windows hosted runners.
`TestOSCredentialStoreRoundTripAcrossProcesses` checks create/replace/isolation/read/
delete using the real OS APIs through separate helper processes.

The existing three kind versions also run `e2e,osvault`, including
`TestNativeOSStoredTokenIdentityAndExpiryAgainstRealCluster`: native store and login,
real namespace-only RBAC, live logs, expiry cleanup and unchanged kubeconfig. Real
provider/cluster tests fail rather than skip when required setup is absent. Exact
final CI/head and post-merge verification are recorded in the implementation PR and
#18; compilation or widget tests alone never count as these native integrations.
