//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/testcluster"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

type realInstallRecoveryFixture struct {
	testcluster.HelmMetadataFixture
	dir, id  string
	history  *kube.HelmInstallRecovery
	snapshot helmjournal.Snapshot
	wal      []byte
	writes   *atomic.Int32
}

// The real executor establishes all effects. Faults discard a successful
// persistent server response, never substitute a fabricated success response.
func newRealInstallRecoveryFixture(t *testing.T, ctx context.Context, lostWrite int) *realInstallRecoveryFixture {
	t.Helper()
	f := &realInstallRecoveryFixture{HelmMetadataFixture: testcluster.NewHelmMetadata(t, 0), writes: &atomic.Int32{}}
	var bad, lost atomic.Bool
	cfg := rest.CopyConfig(f.Config)
	cfg.Wrap(func(next http.RoundTripper) http.RoundTripper {
		return installExecutionTransport{next: next, namespace: f.Namespace, writes: f.writes, bad: &bad,
			after: func(n int, response *http.Response) error {
				if n == lostWrite {
					want := http.StatusCreated
					if n == 3 {
						want = http.StatusOK
					}
					if response.StatusCode != want {
						bad.Store(true)
					}
					lost.Store(true)
					return errors.New("PRIVATE_REAL_INSTALL_RECOVERY_LOSS")
				}
				return nil
			}}
	})
	b, err := kube.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	stamp := now.Add(-time.Second).UTC().Format(time.RFC3339Nano)
	raw, err := json.Marshal(map[string]any{
		"name": "execute-install", "namespace": f.Namespace, "version": 1, "apply_method": "ssa",
		"info":     map[string]any{"status": "pending-install", "description": "Dry run complete", "first_deployed": stamp, "last_deployed": stamp},
		"chart":    map[string]any{"metadata": map[string]any{"name": "recovery-chart", "apiVersion": "v2", "version": "0.1.0"}},
		"manifest": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: execute-target\ndata:\n  private: RECOVERY_PAYLOAD_CANARY\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := helmrelease.DecodeInstallCapture(ctx, raw, f.Namespace, "execute-install", now)
	clear(raw)
	if err != nil {
		t.Fatal(err)
	}
	prep, err := b.PrepareHelmInstall(ctx, candidate, "real-install-recovery", true)
	if err != nil {
		t.Fatal(err)
	}
	dry, err := b.AcknowledgeHelmInstallDryRun(ctx, prep, candidate, "real-install-recovery", true)
	if err != nil {
		t.Fatal(err)
	}
	preflight, err := b.PreflightHelmInstall(ctx, dry, candidate, "real-install-recovery")
	if err != nil {
		t.Fatal(err)
	}
	f.dir = filepath.Join(t.TempDir(), "journal")
	journal, err := kube.OpenHelmUninstallJournal(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	grant, err := b.AcknowledgeHelmInstallExecution(ctx, preflight, candidate, "real-install-recovery", true, journal)
	if err != nil {
		t.Fatal(err)
	}
	result, executeErr := b.ExecuteHelmInstall(ctx, grant, candidate, "real-install-recovery")
	grant.Discard()
	if result == nil || bad.Load() {
		t.Fatal("real installation or fault boundary invalid", executeErr)
	}
	wantWrites, wantOutcome := int32(3), helmjournal.Completed
	if lostWrite != 0 {
		wantWrites, wantOutcome = int32(lostWrite), helmjournal.Unknown
		if !lost.Load() || !errors.Is(executeErr, kube.ErrHelmInstallExecutionUnknown) {
			t.Fatal("response was not lost after actual persistent acceptance", executeErr)
		}
	} else if executeErr != nil {
		t.Fatal(executeErr)
	}
	if f.writes.Load() != wantWrites || result.Outcome() != wantOutcome || !result.JournalComplete() {
		t.Fatal("execution continued, discarded uncertainty or omitted its terminal record")
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	f.id = result.JournalID()
	store, err := helmjournal.OpenExisting(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	f.snapshot, err = store.Load(f.id)
	closeErr := store.Close()
	if err != nil || closeErr != nil || f.snapshot.Install == nil {
		t.Fatal("reopen original WAL", err, closeErr)
	}
	f.history = f.reload(t)
	if f.history.Outcome() != wantOutcome || !f.history.Complete() || f.history.Corrupt() ||
		!reflect.DeepEqual(f.history.Effects(), result.Effects()) {
		t.Fatal("offline recovery altered actual historical facts")
	}
	f.wal, err = os.ReadFile(filepath.Join(f.dir, f.id+".wal"))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *realInstallRecoveryFixture) reload(t *testing.T) *kube.HelmInstallRecovery {
	t.Helper()
	j, err := kube.OpenHelmUninstallJournal(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := j.RecoverInstall(f.id, false)
	closeErr := j.Close()
	if err != nil || closeErr != nil || r == nil {
		t.Fatal("typed offline recovery", err, closeErr)
	}
	return r
}

func (f *realInstallRecoveryFixture) unchanged(t *testing.T, ctx context.Context) {
	t.Helper()
	store, err := helmjournal.OpenExisting(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	again, err := store.Load(f.id)
	if err != nil || !reflect.DeepEqual(again, f.snapshot) {
		t.Fatal("observation rewrote checked history", err)
	}
	ids, err := store.List()
	if err != nil || len(ids) != 1 || ids[0] != f.id {
		t.Fatal("observation created additional WAL", err)
	}
	if f.history.Outcome() == helmjournal.Unknown {
		report, err := store.ReviewAdmission(ctx, helmjournal.AdmissionScope{
			Binding: f.snapshot.Install.Binding, Namespace: f.Namespace, Release: f.history.Release(),
		})
		if !errors.Is(err, helmjournal.ErrUnresolved) || report.Reason != helmjournal.AdmissionUnresolved {
			t.Fatal("later GETs cleared the original unresolved admission guard", err, report.Reason)
		}
		w, err := store.BeginInstall(*f.snapshot.Install)
		if w != nil {
			_ = w.Abort()
		}
		if !errors.Is(err, helmjournal.ErrUnresolved) || w != nil {
			t.Fatal("later GETs enabled another installation", err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(f.dir, f.id+".wal"))
	if err != nil || !bytes.Equal(raw, f.wal) {
		t.Fatal("original WAL bytes changed", err)
	}
}

type realInstallRecoveryReads struct {
	base      http.RoundTripper
	namespace string
	mu        sync.Mutex
	calls     []helmRecoveryRequest
	bad       bool
}

func (a *realInstallRecoveryReads) RoundTrip(r *http.Request) (*http.Response, error) {
	a.mu.Lock()
	a.calls = append(a.calls, helmRecoveryRequest{r.Method, r.URL.Path})
	prefix := "/api/v1/namespaces/" + a.namespace
	allowed := r.URL.Path == prefix || r.URL.Path == prefix+"/secrets" ||
		r.URL.Path == prefix+"/secrets/sh.helm.release.v1.execute-install.v1" ||
		r.URL.Path == prefix+"/configmaps/execute-target"
	accept, query := "application/json;as=PartialObjectMetadata;g=meta.k8s.io;v=v1", ""
	if r.URL.Path == prefix+"/secrets" {
		accept, query = helmrelease.Accept, "labelSelector=owner%3Dhelm&limit=64"
	}
	valid := r.Method == http.MethodGet && allowed && r.Header.Get("Accept") == accept &&
		r.URL.RawQuery == query && r.Header.Get("Accept-Encoding") == "identity" &&
		(r.Body == nil || r.Body == http.NoBody) && r.GetBody == nil
	if !valid {
		a.bad = true
	}
	a.mu.Unlock()
	if !valid {
		return nil, errors.New("forbidden recovery transport request")
	}
	return a.base.RoundTrip(r)
}

func (a *realInstallRecoveryReads) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

func (a *realInstallRecoveryReads) assert(t *testing.T, requireReads bool) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	prefix := "/api/v1/namespaces/" + a.namespace
	expected := []string{prefix, prefix + "/secrets",
		prefix + "/secrets/sh.helm.release.v1.execute-install.v1", prefix + "/configmaps/execute-target",
		prefix + "/secrets/sh.helm.release.v1.execute-install.v1", prefix + "/secrets", prefix}
	if a.bad || requireReads && len(a.calls) != len(expected) {
		t.Fatal("missing metadata reads or attempted mutation/fallback")
	}
	for index, call := range a.calls {
		if call.method != http.MethodGet {
			t.Fatal("recovery attempted mutation", call.method)
		}
		if requireReads && call.path != expected[index] {
			t.Fatal("metadata reads lost their namespace/history bracketing or retried")
		}
	}
}

func (f *realInstallRecoveryFixture) reader(t *testing.T) (*kube.Backend, *realInstallRecoveryReads) {
	t.Helper()
	audit := &realInstallRecoveryReads{namespace: f.Namespace}
	cfg := rest.CopyConfig(f.Config)
	cfg.Wrap(func(base http.RoundTripper) http.RoundTripper { audit.base = base; return audit })
	b, err := kube.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return b, audit
}

func (f *realInstallRecoveryFixture) observe(t *testing.T, ctx context.Context) *kube.HelmInstallRecoveryObservation {
	t.Helper()
	b, audit := f.reader(t)
	session := "fresh-real-install-recovery"
	if grant, err := b.AcknowledgeHelmInstallReconciliation(f.history, session, f.Namespace, false); grant != nil || err == nil {
		t.Fatal("history silently authorized a network read")
	}
	grant, err := b.AcknowledgeHelmInstallReconciliation(f.history, session, f.Namespace, true)
	if err != nil {
		t.Fatal("independent fresh read confirmation", err)
	}
	defer grant.Discard()
	if audit.count() != 0 {
		t.Fatal("minting read consent contacted cluster")
	}
	writesBefore := f.writes.Load()
	out, err := b.ObserveHelmInstallRecovery(ctx, grant, f.history, session, f.Namespace)
	if err != nil || out == nil || out.ID() != f.id || out.Namespace() != f.Namespace ||
		out.HistoryOutcome() != f.history.Outcome() || len(out.Items()) != 1 {
		t.Fatal("fresh metadata observation lost its historical identity", err)
	}
	audit.assert(t, true)
	count := audit.count()
	if _, err := b.ObserveHelmInstallRecovery(ctx, grant, f.history, session, f.Namespace); err == nil || audit.count() != count {
		t.Fatal("consumed read consent issued another request")
	}
	if f.writes.Load() != writesBefore {
		t.Fatal("recovery revived the old execution")
	}
	f.unchanged(t, ctx)
	return out
}

func TestRealHelmInstallRecoveryCompleted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	f := newRealInstallRecoveryFixture(t, ctx, 0)
	secret, err := f.Client.CoreV1().Secrets(f.Namespace).Get(ctx, f.history.StorageName(), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cm, err := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, "execute-target", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	out := f.observe(t, ctx)
	if out.HistoryOutcome() != helmjournal.Completed || out.Storage().State != kube.HelmInstallRecoveryOriginal ||
		out.Storage().UID != string(secret.UID) || out.Storage().HistoricalResourceVersion != secret.ResourceVersion ||
		out.Items()[0].State != kube.HelmInstallRecoveryOriginal || out.Items()[0].UID != string(cm.UID) ||
		secret.Labels["status"] != "deployed" || cm.Data["private"] != "RECOVERY_PAYLOAD_CANARY" {
		t.Fatal("completed install recovery did not use actual accepted final identities")
	}
}

func TestRealHelmInstallRecoveryAfterCommittedResponseLoss(t *testing.T) {
	for index, stage := range []string{"secret-create", "target-create", "final-patch"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			f := newRealInstallRecoveryFixture(t, ctx, index+1)
			secret, err := f.Client.CoreV1().Secrets(f.Namespace).Get(ctx, f.history.StorageName(), metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			cm, cmErr := f.Client.CoreV1().ConfigMaps(f.Namespace).Get(ctx, "execute-target", metav1.GetOptions{})
			out := f.observe(t, ctx)
			if out.HistoryOutcome() != helmjournal.Unknown || f.history.Outcome() != helmjournal.Unknown {
				t.Fatal("fresh GET promoted Unknown to historical success")
			}
			switch stage {
			case "secret-create":
				if out.Storage().State != kube.HelmInstallRecoveryUnattributed || out.Storage().HistoricalUID != "" ||
					out.Storage().UID != string(secret.UID) || out.Items()[0].State != kube.HelmInstallRecoveryAbsent ||
					!apierrors.IsNotFound(cmErr) || secret.Labels["status"] != "pending-install" {
					t.Fatal("lost Secret response inferred identity or a later write")
				}
			case "target-create":
				if cmErr != nil || out.Storage().State != kube.HelmInstallRecoveryOriginal ||
					out.Items()[0].State != kube.HelmInstallRecoveryUnattributed || out.Items()[0].HistoricalUID != "" ||
					out.Items()[0].UID != string(cm.UID) || secret.Labels["status"] != "pending-install" {
					t.Fatal("lost target response was adopted or deployed")
				}
			case "final-patch":
				originalRV := f.snapshot.Effects[0].ObservedResourceVersion
				if cmErr != nil || secret.Labels["status"] != "deployed" || secret.ResourceVersion == originalRV ||
					out.Storage().State != kube.HelmInstallRecoveryModified || out.Storage().HistoricalResourceVersion != originalRV ||
					out.Storage().ResourceVersion != secret.ResourceVersion || out.Items()[0].State != kube.HelmInstallRecoveryOriginal {
					t.Fatal("final PATCH precondition RV was treated as a post-PATCH acknowledgement")
				}
			}
		})
	}
}

func TestRealHelmInstallRecoveryChangedObjects(t *testing.T) {
	for _, mode := range []string{"target-modified", "target-replacement", "target-absent", "storage-unlabeled", "storage-replacement", "storage-absent"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			f := newRealInstallRecoveryFixture(t, ctx, 0)
			want := kube.HelmInstallRecoveryModified
			if strings.HasPrefix(mode, "target-") {
				client := f.Client.CoreV1().ConfigMaps(f.Namespace)
				current, err := client.Get(ctx, "execute-target", metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				uid, rv := current.UID, current.ResourceVersion
				switch mode {
				case "target-modified":
					current.Data["private"] = "EXTERNAL_UNREVIEWED_CHANGE"
					_, err = client.Update(ctx, current, metav1.UpdateOptions{})
				case "target-replacement", "target-absent":
					err = client.Delete(ctx, current.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}})
					if err == nil {
						_, absent := client.Get(ctx, current.Name, metav1.GetOptions{})
						if !apierrors.IsNotFound(absent) {
							t.Fatal("independent target deletion was not complete", absent)
						}
					}
					if err == nil && mode == "target-replacement" {
						replacement := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: current.Name, Labels: current.Labels, Annotations: current.Annotations}, Data: current.Data}
						replacement, err = client.Create(ctx, replacement, metav1.CreateOptions{})
						if err == nil && replacement.UID == uid {
							t.Fatal("target replacement retained old UID")
						}
						want = kube.HelmInstallRecoveryReplacement
					} else {
						want = kube.HelmInstallRecoveryAbsent
					}
				}
				if err != nil {
					t.Fatal("independent target change", err)
				}
			} else {
				client := f.Client.CoreV1().Secrets(f.Namespace)
				current, err := client.Get(ctx, f.history.StorageName(), metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				uid, rv := current.UID, current.ResourceVersion
				switch mode {
				case "storage-unlabeled":
					delete(current.Labels, "owner")
					_, err = client.Update(ctx, current, metav1.UpdateOptions{})
				case "storage-replacement", "storage-absent":
					err = client.Delete(ctx, current.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}})
					if err == nil {
						_, absent := client.Get(ctx, current.Name, metav1.GetOptions{})
						if !apierrors.IsNotFound(absent) {
							t.Fatal("independent storage deletion was not complete", absent)
						}
					}
					if err == nil && mode == "storage-replacement" {
						replacement := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: current.Name, Labels: current.Labels, Annotations: current.Annotations}, Type: current.Type, Data: current.Data}
						replacement, err = client.Create(ctx, replacement, metav1.CreateOptions{})
						if err == nil && replacement.UID == uid {
							t.Fatal("storage replacement retained old UID")
						}
						want = kube.HelmInstallRecoveryReplacement
					} else {
						want = kube.HelmInstallRecoveryAbsent
					}
				}
				if err != nil {
					t.Fatal("independent storage change", err)
				}
			}
			out := f.observe(t, ctx)
			item := out.Storage()
			if strings.HasPrefix(mode, "target-") {
				item = out.Items()[0]
			}
			if item.State != want || out.HistoryOutcome() != helmjournal.Completed {
				t.Fatal("current external change rewrote history or was misclassified", mode, item.State)
			}
			if mode == "storage-unlabeled" && (out.Storage().Markers != "missing" || len(out.Revisions()) != 0 || out.Storage().UID == "") {
				t.Fatal("owner-filtered LIST hid the exact unlabeled reserved Secret")
			}
		})
	}
}

func TestRealHelmInstallRecoveryGrantScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	f := newRealInstallRecoveryFixture(t, ctx, 0)
	for _, mode := range []string{"wrong-backend", "wrong-session", "wrong-namespace", "different-snapshot", "cancelled", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			b, audit := f.reader(t)
			originalBackend := b
			session, namespace, history := "new-real-recovery", f.Namespace, f.history
			grant, err := b.AcknowledgeHelmInstallReconciliation(history, session, namespace, true)
			if err != nil {
				t.Fatal(err)
			}
			defer grant.Discard()
			callContext := ctx
			var otherAudit *realInstallRecoveryReads
			switch mode {
			case "wrong-backend":
				b, otherAudit = f.reader(t)
			case "wrong-session":
				session = "different-session"
			case "wrong-namespace":
				namespace += "-other"
			case "different-snapshot":
				history = f.reload(t)
			case "cancelled":
				var stop context.CancelFunc
				callContext, stop = context.WithCancel(ctx)
				stop()
			case "revoked":
				grant.Discard()
			}
			if out, err := b.ObserveHelmInstallRecovery(callContext, grant, history, session, namespace); err == nil || out != nil {
				t.Fatal("invalid read authority was accepted", mode)
			}
			if _, err := originalBackend.ObserveHelmInstallRecovery(ctx, grant, f.history, "new-real-recovery", f.Namespace); err == nil {
				t.Fatal("failed attempt did not consume authority", mode)
			}
			if audit.count() != 0 || otherAudit != nil && otherAudit.count() != 0 {
				t.Fatal("unauthorized attempt contacted cluster")
			}
			f.unchanged(t, ctx)
		})
	}
}

func TestRealHelmInstallRecoveryNamespaceAdvance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	f := newRealInstallRecoveryFixture(t, ctx, 0)
	ns, err := f.Client.CoreV1().Namespaces().Get(ctx, f.Namespace, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if ns.Annotations == nil {
		ns.Annotations = map[string]string{}
	}
	ns.Annotations["aster.test/later-metadata"] = "not-a-new-namespace"
	ns, err = f.Client.CoreV1().Namespaces().Update(ctx, ns, metav1.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	oldUID, oldRV := f.history.NamespaceIdentity()
	if string(ns.UID) != oldUID || ns.ResourceVersion == oldRV {
		t.Fatal("fixture did not advance only namespace RV")
	}
	out := f.observe(t, ctx)
	uid, rv := out.NamespaceIdentity()
	if uid != oldUID || rv != ns.ResourceVersion {
		t.Fatal("safe namespace RV advancement was confused with replacement")
	}
}
