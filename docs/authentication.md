# Bounded connection-time exec authentication

Aster resolves an explicitly trusted kubeconfig `exec` program once when **Connect** is chosen.
The program receives `KUBERNETES_EXEC_INFO` using its declared v1 or v1beta1 API, with
`interactive: false` and cluster information only when requested. `Always` interactive mode and
legacy `auth-provider` configurations are rejected. v1 must declare an interactive mode.

## Trust and process contract

Loading contexts does not start authentication. Trust binds to the exact selected configuration;
changed commands, arguments, environment, credentials or endpoint settings invalidate the decision.
Aster passes an argv vector, not a shell command line. A command name resolves through the desktop
process PATH to an absolute installed executable; a relative PATH/current-directory result is rejected.
An exec.env PATH override affects the launched program, not Aster's initial executable lookup.
Windows requires an executable `.exe`, not a batch/PowerShell script. A trusted program itself can
of course spawn a shell or read files: **this is lifecycle isolation, not an execution sandbox**.

Each workspace admits at most one outstanding authentication worker, including canceled workers
that have not yet joined. Disconnect remains available while authenticating. The command has a
30-second deadline, no stdin, at most 128 arguments/16 KiB total argument bytes, 256 KiB stdout,
16 KiB stderr, and 128 explicit environment entries/24 KiB total environment. Stdout is parsed only
as a credential; stderr is counted but never retained/displayed. Failures do not echo either stream,
arguments, returned tokens or keys. These are logical buffer bounds, not process RSS/CPU limits.

On Linux/macOS a dedicated process group is killed on timeout, cancellation and normal return,
including non-detached descendants; programs that deliberately escape the group are not contained.
On Windows the executable is created suspended, assigned to a kill-on-close Job Object, then resumed;
job setup failure fails closed. Its job allows at most 32 active processes. Root process and pipe
workers are joined. This is not an OS security sandbox or a protection from a compromised trusted CLI.

Only home/profile, temporary directory, PATH, OS directory and locale variables are inherited.
Cloud profile/region settings must be explicit in the trusted exec.env or the trusted tool's normal
configuration files. Arbitrary parent cloud secrets, proxy variables and application variables are
not copied. Duplicate/invalid environment names, KUBERNETES_EXEC_INFO override and dynamic-loader/
runtime injection settings are rejected. This intentional compatibility restriction is visible:
there is no silent fallback to an unrestricted runner. Proxy use for the API remains a separately
trusted kubeconfig decision; CLI network/environment configuration is not implicitly copied.

## Snapshot lifetime and reconnection

A response must contain one strict JSON ExecCredential of the declared version, exactly one token
or a matching certificate/private-key pair, and a future expiry when supplied. Duplicate/unknown
fields, trailing documents, control characters in tokens, malformed/expired/not-yet-valid
certificates and ambiguous credential sources are rejected. Credentials stay in memory and are not
written by Aster to configuration/history/logs. This does not guarantee secure erasure of Go heap
copies or prevent the trusted CLI from using its own cache.

The connection ends at the earliest of the response expiry, certificate NotAfter, and **15 minutes**.
Without a returned expirationTimestamp, the same 15-minute cap applies. All resource subscriptions,
logs, terminal/forward sessions and operations inherit that deadline. Expiry cancels the connection,
clears private UI state and prepared plans, and presents **Credentials expired**. Old backend
configuration wrappers reject new calls after expiry or closure. These wrappers are an application
contract, not a defense against hostile in-process code that can extract tokens and replace them.

There is **no silent credential renewal** and no automatic retry of authentication after an error.
Choose Connect again explicitly. This starts a new identity/session and requires new operation
previews. In-flight write cancellation may still mean the server committed: existing Unknown/no-replay
semantics apply. Canceling a remote exec connection does not guarantee remote process termination.

This is not OIDC/PKCE, a system keychain, enterprise revocation or a managed-identity gateway. Static
non-exec kubeconfig credentials retain their existing behavior; no artificial 15-minute cap is added.
Provider-specific cloud CLIs and interactive browser login require separate qualification.

## Verification

`internal/credentialexec` runs real child-process tests on Linux/macOS/Windows, including descendants,
output overflow, timeout/cancel, sanitized environment, literal arguments, strict token/certificate
parsing, real mutual TLS and expiration/closed-transport refusal. Native tests prove trust-before-run,
single admission, Disconnect while authenticating, expired stream teardown and epoch isolation.

Real kind E2E obtains a namespace-scoped ServiceAccount token, returns it through the actual child
process and exercises Aster's read-only identity, forbidden write/no-created-object assertion, and
expired-call refusal. A native real-cluster scenario connects via trust controls, watches a Pod,
reads its logs, and verifies expiry clears both streams and UI state. These do not substitute for
OIDC provider certification, system input testing or a penetration test.
