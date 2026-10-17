package helmrender

// These tests run the approved external Helm executable, not an imitation of
// its action implementation. The API server here is intentionally synthetic;
// disposable real Kubernetes coverage lives in uninstall_contract_e2e_test.go.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmfixture"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
)

const contractRelease = "contract"
const contractNamespace = "aster-contract"

// Test-only: never exported as a credentialed product CLI capability. Config is
// created by these tests; no ambient HOME, plugins, proxy or kubeconfig survives.
type contractTool struct {
	root, exe string
	env       []string
}

func approvedContractExecutable(t *testing.T) string {
	t.Helper()
	path := os.Getenv("ASTER_HELM_ARCHIVE")
	if path == "" {
		t.Skip("approved Helm archive required; mandatory CI evidence rejects this skip")
	}
	data, err := readLocal(context.Background(), path, MaxDistributionBytes)
	if err != nil {
		t.Fatal("read approved archive", err)
	}
	d, version, ok := approved()
	sum := sha256.Sum256(data)
	if !ok || version != "v4.3.0" || hex.EncodeToString(sum[:]) != d.SHA256 {
		t.Fatal("unapproved Helm distribution")
	}
	root := t.TempDir()
	exe, err := unpackExecutable(context.Background(), data, d.File, root)
	if err != nil {
		t.Fatal(err)
	}
	return exe
}
func newContractTool(t *testing.T, config []byte, exe string) *contractTool {
	t.Helper()
	root := t.TempDir()
	env, err := isolatedEnvironment(root)
	if err != nil {
		t.Fatal(err)
	}
	filtered := []string{}
	for _, s := range env {
		if !strings.HasPrefix(s, "HELM_DRIVER=") && !strings.HasPrefix(s, "HELM_MEMORY_DRIVER_DATA=") {
			filtered = append(filtered, s)
		}
	}
	filtered = append(filtered, "HELM_DRIVER=secret")
	if err := os.WriteFile(filepath.Join(root, "kubeconfig"), config, 0600); err != nil {
		t.Fatal(err)
	}
	tool := &contractTool{root, exe, filtered}
	out, err := tool.run(t, "version", "--template={{.Version}}")
	if err != nil || out != "v4.3.0" {
		t.Fatal("wrong executable version", err)
	}
	return tool
}

type contractOutput struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *contractOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.b.Len()+len(p) > 64<<10 {
		return 0, fmt.Errorf("test CLI output budget")
	}
	return w.b.Write(p)
}
func (c *contractTool) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := c.runContext(ctx, args...)
	if ctx.Err() != nil {
		t.Fatal("bounded fixture process deadline", ctx.Err())
	}
	return output, err
}
func (c *contractTool) runContext(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.exe, args...)
	cmd.Dir, cmd.Env = c.root, c.env
	cmd.WaitDelay = time.Second
	out := &contractOutput{}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return out.b.String(), err
}

func (c *contractTool) uninstall(t *testing.T, ns string, flags ...string) (string, error) {
	t.Helper()
	args := []string{"uninstall", contractRelease, "--namespace", ns, "--kube-context", "contract", "--wait=hookOnly", "--timeout=5s", "--cascade=orphan"}
	return c.run(t, append(args, flags...)...)
}
func loopbackConfig(endpoint string) []byte {
	// endpoint is returned by httptest, never obtained from caller input.
	return []byte("apiVersion: v1\nkind: Config\ncurrent-context: contract\nclusters:\n- name: contract\n  cluster:\n    server: " + endpoint + "\nusers:\n- name: contract\n  user: {}\ncontexts:\n- name: contract\n  context:\n    cluster: contract\n    user: contract\n")
}
func contractManifest(name string, keep bool) string {
	text := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + name + "\n"
	if keep {
		text += "  annotations:\n    helm.sh/resource-policy: keep\n"
	}
	return text + "data:\n  fixture: harmless\n"
}
func contractSecret(manifest string) corev1.Secret {
	payload := map[string]any{"name": contractRelease, "namespace": contractNamespace, "version": 1, "info": map[string]any{"status": "deployed"}, "chart": map[string]any{"metadata": map[string]any{"apiVersion": "v2", "name": "contract", "version": "0.1.0"}}, "manifest": manifest, "hooks": []any{}}
	data, _ := json.Marshal(payload)
	return corev1.Secret{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"}, ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.contract.v1", Namespace: contractNamespace, UID: "release-uid", ResourceVersion: "1", Labels: map[string]string{"owner": "helm", "name": contractRelease, "version": "1", "status": "deployed"}}, Type: "helm.sh/release.v1", Data: map[string][]byte{"release": helmfixture.Stored(data)}}
}
func contractMap(name string) corev1.ConfigMap {
	return corev1.ConfigMap{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}, ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: contractNamespace, UID: types.UID("original-" + name), ResourceVersion: "1", Labels: map[string]string{"app.kubernetes.io/managed-by": "Helm"}, Annotations: map[string]string{"meta.helm.sh/release-name": contractRelease, "meta.helm.sh/release-namespace": contractNamespace}}, Data: map[string]string{"fixture": "harmless"}}
}

type contractRequest struct {
	Method, Path string
	Options      metav1.DeleteOptions
}
type uninstallAPI struct {
	committed  chan struct{}
	mu         sync.Mutex
	mode       string
	secret     *corev1.Secret
	objects    map[string]corev1.ConfigMap
	requests   []contractRequest
	unexpected []string
	removed    []types.UID
}

func newUninstallAPI(t *testing.T, mode string, exe string) (*uninstallAPI, *contractTool) {
	t.Helper()
	manifest := contractManifest("target", mode == "keep")
	objects := map[string]corev1.ConfigMap{"target": contractMap("target")}
	if mode == "partial" {
		manifest += "---\n" + contractManifest("other", false)
		objects["other"] = contractMap("other")
	}
	if mode == "malformed" {
		manifest = "apiVersion: v1\nkind: ConfigMap\nmetadata: [\n"
	}
	if mode == "missing-marker" || mode == "mismatch-marker" {
		cm := objects["target"]
		if mode == "missing-marker" {
			cm.Labels = nil
		} else {
			cm.Annotations["meta.helm.sh/release-name"] = "different"
		}
		objects["target"] = cm
	}
	s := contractSecret(manifest)
	f := &uninstallAPI{mode: mode, secret: &s, objects: objects, committed: make(chan struct{})}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	return f, newContractTool(t, loopbackConfig(server.URL), exe)
}
func (f *uninstallAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	send := func(value any) {
		if err := json.NewEncoder(w).Encode(value); err != nil {
			f.unexpected = append(f.unexpected, "response encoding")
		}
	}
	status := func(code int, reason metav1.StatusReason, name, resource string) {
		w.WriteHeader(code)
		send(metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: "Failure", Reason: reason, Code: int32(code), Details: &metav1.StatusDetails{Name: name, Kind: resource}})
	}
	entry := contractRequest{Method: r.Method, Path: r.URL.Path}
	if r.Method == "DELETE" {
		data, e := io.ReadAll(io.LimitReader(r.Body, 8193))
		if e != nil || len(data) > 8192 || json.Unmarshal(data, &entry.Options) != nil {
			f.unexpected = append(f.unexpected, "invalid delete body")
		}
	}
	f.requests = append(f.requests, entry)
	if len(f.requests) > 128 || r.Header.Get("Authorization") != "" {
		f.unexpected = append(f.unexpected, "unexpected credential/request budget")
		status(403, metav1.StatusReasonForbidden, "", "")
		return
	}
	if r.Method == "GET" {
		switch r.URL.Path {
		case "/version":
			send(map[string]string{"major": "1", "minor": "37", "gitVersion": "v1.37.0"})
			return
		case "/api":
			send(metav1.APIVersions{TypeMeta: metav1.TypeMeta{Kind: "APIVersions", APIVersion: "v1"}, Versions: []string{"v1"}})
			return
		case "/apis":
			send(metav1.APIGroupList{TypeMeta: metav1.TypeMeta{Kind: "APIGroupList", APIVersion: "v1"}, Groups: []metav1.APIGroup{}})
			return
		case "/api/v1":
			send(metav1.APIResourceList{TypeMeta: metav1.TypeMeta{Kind: "APIResourceList"}, GroupVersion: "v1", APIResources: []metav1.APIResource{{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: metav1.Verbs{"get", "list", "create", "delete", "update", "patch"}}, {Name: "secrets", Kind: "Secret", Namespaced: true, Verbs: metav1.Verbs{"get", "list", "create", "delete", "update"}}}})
			return
		}
	}
	prefix := "/api/v1/namespaces/" + contractNamespace + "/"
	if r.URL.Path == prefix+"secrets" && r.Method == "GET" {
		list := corev1.SecretList{TypeMeta: metav1.TypeMeta{Kind: "SecretList", APIVersion: "v1"}, Items: []corev1.Secret{}}
		sel, e := labels.Parse(r.URL.Query().Get("labelSelector"))
		if e != nil {
			f.unexpected = append(f.unexpected, "selector")
		} else if f.secret != nil && sel.Matches(labels.Set(f.secret.Labels)) {
			list.Items = append(list.Items, *f.secret)
		}
		send(list)
		return
	}
	if r.URL.Path == prefix+"secrets/sh.helm.release.v1.contract.v1" {
		if f.secret == nil {
			status(404, metav1.StatusReasonNotFound, "sh.helm.release.v1.contract.v1", "secrets")
			return
		}
		switch r.Method {
		case "GET":
			send(f.secret)
			return
		case "PUT":
			if f.mode == "storage-denied" {
				status(403, metav1.StatusReasonForbidden, f.secret.Name, "secrets")
				return
			}
			var s corev1.Secret
			body, readErr := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
			_, _, decodeErr := scheme.Codecs.UniversalDeserializer().Decode(body, nil, &s)
			if readErr != nil || len(body) > 1<<20 || decodeErr != nil {
				f.unexpected = append(f.unexpected, "invalid storage update")
				status(400, metav1.StatusReasonBadRequest, "", "")
				return
			}
			f.secret = &s
			send(f.secret)
			return
		case "DELETE":
			f.secret = nil
			send(metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: "Success", Code: 200})
			return
		}
	}
	if strings.HasPrefix(r.URL.Path, prefix+"configmaps/") {
		name := strings.TrimPrefix(r.URL.Path, prefix+"configmaps/")
		cm, exists := f.objects[name]
		if !exists {
			status(404, metav1.StatusReasonNotFound, name, "configmaps")
			return
		}
		switch r.Method {
		case "GET":
			if f.mode == "read-denied" {
				status(403, metav1.StatusReasonForbidden, name, "configmaps")
				return
			}
			send(cm)
			if f.mode == "replace-after-read" {
				cm.UID = "replacement-uid"
				cm.ResourceVersion = "2"
				f.objects[name] = cm
			}
			return
		case "DELETE":
			if f.mode == "partial" && name == "other" {
				status(403, metav1.StatusReasonForbidden, name, "configmaps")
				return
			}
			// A real API server enforces preconditions. Preserve that behavior so
			// the race test cannot pass just because a fake ignored them.
			if p := entry.Options.Preconditions; p != nil && ((p.UID != nil && *p.UID != cm.UID) || (p.ResourceVersion != nil && *p.ResourceVersion != cm.ResourceVersion)) {
				status(409, metav1.StatusReasonConflict, name, "configmaps")
				return
			}
			delete(f.objects, name)
			f.removed = append(f.removed, cm.UID)
			if f.mode == "cancel-after-delete" {
				close(f.committed)
				<-r.Context().Done() // Commit occurred, but no response is acknowledged.
				return
			}
			send(&cm)
			return
		}
	}
	f.unexpected = append(f.unexpected, r.Method+" "+r.URL.Path)
	status(404, metav1.StatusReasonNotFound, "", "")
}
func (f *uninstallAPI) checked(t *testing.T, fn func()) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.unexpected) != 0 {
		t.Fatal("unexpected fixture requests", f.unexpected)
	}
	fn()
}

// Helm's --wait=hookOnly does not establish synchronous object disappearance.
// Treat a successful DELETE acknowledgement as an intermediate observation:
// only typed NotFound for the exact reviewed UID is success. No mutation/retry
// is issued by this helper, and persistent/replaced/denied targets fail closed.
func observeHelmUninstallTargetAbsent(ctx context.Context, limit time.Duration, expectedName string, expected types.UID, getUID func(context.Context) (types.UID, error)) error {
	if expectedName == "" || expected == "" || getUID == nil || limit <= 0 {
		return fmt.Errorf("invalid deletion observation parameters")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, limit, true, func(ctx context.Context) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		current, err := getUID(ctx)
		// A condition returned after cancellation/deadline is not fresh evidence,
		// even when the transport reports NotFound. Polling does not check this
		// on our behalf after a successful condition result.
		if stopped := ctx.Err(); stopped != nil {
			return false, stopped
		}
		if apierrors.IsNotFound(err) {
			// client-go can synthesize a typed 404 for an unrecognized proxy body.
			// Require an exact Kubernetes failure envelope, not just its reason.
			var status apierrors.APIStatus
			if errors.As(err, &status) {
				s := status.Status()
				if s.Status == metav1.StatusFailure && s.Code == http.StatusNotFound && s.Reason == metav1.StatusReasonNotFound && s.Details != nil && s.Details.Name == expectedName && s.Details.Kind == "configmaps" && s.Details.Group == "" && len(s.Details.Causes) == 0 {
					return true, nil
				}
			}
			return false, fmt.Errorf("ConfigMap absence response does not match the reviewed target")
		}
		if err != nil {
			return false, err
		}
		if current != expected {
			return false, fmt.Errorf("reviewed ConfigMap identity changed before deletion was observed")
		}
		return false, nil
	})
}

func TestHelmUninstallDeletionObservationFailsClosed(t *testing.T) {
	const expected types.UID = "fixture-uid"
	notFound := apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, "target")
	t.Run("eventual-exact-absence", func(t *testing.T) {
		calls := 0
		err := observeHelmUninstallTargetAbsent(context.Background(), time.Second, "target", expected, func(context.Context) (types.UID, error) {
			calls++
			if calls <= 2 {
				return expected, nil
			}
			return "", notFound
		})
		if err != nil || calls != 3 {
			t.Fatalf("bounded exact-absence observation: calls=%d, err=%v", calls, err)
		}
	})
	t.Run("persistent-object", func(t *testing.T) {
		calls := 0
		err := observeHelmUninstallTargetAbsent(context.Background(), 120*time.Millisecond, "target", expected, func(context.Context) (types.UID, error) {
			calls++
			return expected, nil
		})
		if err == nil || calls < 2 {
			t.Fatalf("persistent resource was accepted: calls=%d err=%v", calls, err)
		}
	})
	t.Run("replaced-uid", func(t *testing.T) {
		calls := 0
		err := observeHelmUninstallTargetAbsent(context.Background(), time.Second, "target", expected, func(context.Context) (types.UID, error) {
			calls++
			return "new-uid", nil
		})
		if err == nil || calls != 1 {
			t.Fatalf("replacement was accepted or retried: calls=%d err=%v", calls, err)
		}
	})
	t.Run("denied-not-absent", func(t *testing.T) {
		calls := 0
		denied := apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, "target", fmt.Errorf("fixture denied"))
		err := observeHelmUninstallTargetAbsent(context.Background(), time.Second, "target", expected, func(context.Context) (types.UID, error) {
			calls++
			return "", denied
		})
		if !apierrors.IsForbidden(err) || calls != 1 {
			t.Fatalf("denial was misclassified or retried: calls=%d err=%v", calls, err)
		}
	})
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		calls := 0
		err := observeHelmUninstallTargetAbsent(ctx, time.Second, "target", expected, func(context.Context) (types.UID, error) {
			calls++
			return "", notFound
		})
		if err == nil || calls != 0 {
			t.Fatalf("canceled observation succeeded: calls=%d err=%v", calls, err)
		}
	})
}

func TestHelmUninstallBinaryDryRunAndRetention(t *testing.T) {
	exe := approvedContractExecutable(t)
	// Hostile ambient values must never be passed to the executable.
	t.Setenv("KUBECONFIG", "PRIVATE_AMBIENT_CONFIG")
	t.Setenv("HELM_DRIVER", "sql")
	t.Setenv("HTTP_PROXY", "http://private.invalid")
	for _, mode := range []string{"owned", "keep", "missing-marker", "mismatch-marker", "read-denied", "storage-denied", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			f, tool := newUninstallAPI(t, mode, exe)
			original, _ := json.Marshal(f.secret)
			out, e := tool.uninstall(t, contractNamespace, "--dry-run", "--keep-history", "--no-hooks")
			if e != nil {
				t.Fatalf("fixture dry-run failed: %v %s", e, out)
			}
			f.checked(t, func() {
				now, _ := json.Marshal(f.secret)
				if !bytes.Equal(original, now) || len(f.objects) != 1 {
					t.Fatal("dry-run mutated fixture")
				}
				for _, r := range f.requests {
					if r.Method != "GET" {
						t.Fatal("dry-run sent a non-GET", r.Method)
					}
				}
			})
			out, e = tool.uninstall(t, contractNamespace, "--keep-history", "--no-hooks")
			f.checked(t, func() {
				if mode == "malformed" {
					if e == nil || f.secret.Labels["status"] != "uninstalling" || len(f.objects) != 1 {
						t.Fatal("malformed execution boundary", e)
					}
					return
				}
				if e != nil {
					t.Fatalf("fixture execution failed: %v %s", e, out)
				}
				retained := mode != "owned" && mode != "storage-denied"
				if (len(f.objects) == 1) != retained {
					t.Fatal("unexpected object retention", mode)
				}
				want := "uninstalled"
				if mode == "storage-denied" {
					want = "deployed"
				}
				if f.secret == nil || f.secret.Labels["status"] != want {
					t.Fatal("history outcome", mode)
				}
			})
		})
	}
}
func TestHelmUninstallBinaryRaceAndPartialEffects(t *testing.T) {
	exe := approvedContractExecutable(t)
	for _, mode := range []string{"replace-after-read", "partial"} {
		t.Run(mode, func(t *testing.T) {
			f, tool := newUninstallAPI(t, mode, exe)
			out, e := tool.uninstall(t, contractNamespace, "--keep-history", "--no-hooks")
			f.checked(t, func() {
				if mode == "partial" {
					if e == nil || len(f.objects) != 1 || len(f.removed) != 1 || f.removed[0] != "original-target" || f.secret.Labels["status"] != "uninstalling" {
						t.Fatalf("partial effects: %v %s", e, out)
					}
					return
				}
				if e != nil || len(f.removed) != 1 || f.removed[0] != "replacement-uid" {
					t.Fatalf("replacement behavior: %v %s", e, out)
				}
				deletes := 0
				for _, r := range f.requests {
					if r.Method == "DELETE" {
						deletes++
						if r.Options.Preconditions != nil {
							t.Fatal("pinned Helm now uses preconditions: review contract")
						}
					}
				}
				if deletes != 1 {
					t.Fatal("unexpected mutation count")
				}
			})
		})
	}
}

func TestHelmUninstallBinaryCancellationIsNotRollback(t *testing.T) {
	f, tool := newUninstallAPI(t, "cancel-after-delete", approvedContractExecutable(t))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := tool.runContext(ctx, "uninstall", contractRelease, "--namespace", contractNamespace, "--kube-context", "contract", "--wait=hookOnly", "--timeout=5s", "--cascade=orphan", "--keep-history", "--no-hooks")
		done <- err
	}()
	select {
	case <-f.committed:
		cancel()
	case <-ctx.Done():
		t.Fatal("fixture did not reach committed delete")
	}
	if err := <-done; err != context.Canceled {
		t.Fatal("canceled process result", err)
	}
	f.checked(t, func() {
		if len(f.objects) != 0 || len(f.removed) != 1 || f.secret == nil || f.secret.Labels["status"] != "uninstalling" {
			t.Fatal("cancellation was incorrectly treated as rollback")
		}
	})
}
