//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

type helmRecoveryRequest struct{ method, path string }

// This independent observer records every request method. A POST/PUT or a
// dry-run mutation cannot disappear from the no-write recovery assertion.
type helmRecoveryTransport struct {
	base  http.RoundTripper
	mu    sync.Mutex
	calls []helmRecoveryRequest
}

func (a *helmRecoveryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	a.mu.Lock()
	a.calls = append(a.calls, helmRecoveryRequest{req.Method, req.URL.Path})
	a.mu.Unlock()
	return a.base.RoundTrip(req)
}

func (a *helmRecoveryTransport) observed() []helmRecoveryRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]helmRecoveryRequest(nil), a.calls...)
}

func helmRecoveryBackend(t *testing.T, cfg *rest.Config, audit *helmRecoveryTransport) *kube.Backend {
	t.Helper()
	copy := rest.CopyConfig(cfg)
	copy.Wrap(func(base http.RoundTripper) http.RoundTripper { audit.base = base; return audit })
	b, err := kube.New(copy)
	if err != nil {
		t.Fatal("fresh recovery backend", err)
	}
	return b
}

func reopenHelmExecutionHistory(t *testing.T, f *helmExecutionFixture, result *kube.HelmUninstallExecution) *kube.HelmUninstallRecovery {
	t.Helper()
	ids, err := f.journal.IDs()
	if err != nil || len(ids) != 1 {
		t.Fatal("execution did not leave exactly one bounded journal record", err)
	}
	if err := f.journal.Close(); err != nil {
		t.Fatal("close original journal handle", err)
	}
	reopened, err := kube.OpenHelmUninstallJournal(f.journalDir)
	if err != nil {
		t.Fatal("reopen persistent journal with a fresh handle", err)
	}
	f.journal = reopened
	recovered, err := f.journal.Recover(ids[0])
	// The local attempt returned and recorded its terminal frame. That
	// completeness must coexist with Unknown remote effect attribution.
	if err != nil || recovered == nil || recovered.Corrupt() || !recovered.Complete() || recovered.ID() != ids[0] ||
		recovered.PreparationID() != result.PreparationID() || recovered.Namespace() != f.Namespace ||
		recovered.Outcome() != result.Outcome() || recovered.Revision() != result.Revision() ||
		!reflect.DeepEqual(recovered.Targets(), result.Targets()) || !reflect.DeepEqual(recovered.Effects(), result.Effects()) {
		t.Fatal("reopened history changed execution identity, effects or uncertainty", err)
	}
	return recovered
}

// The old execution tests still independently qualify all three committed
// response-loss boundaries. These additional cases qualify reopening the actual
// journal and making fresh authorized GET observations against those real
// persisted effects. Actual process termination is separately exercised by the
// journal subprocess tests on all three OS runners.
func TestRealHelmUninstallRecoveryAfterCommittedResponseLoss(t *testing.T) {
	for _, stage := range []string{"begin", "delete", "finish"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			f := newHelmExecutionFixture(t, ctx, 2)
			executionAudit := &helmExecutionTransport{lose: stage}
			originalBackend := helmExecutionBackend(t, f.Config, executionAudit)
			consent, originalRow := prepareHelmExecution(t, ctx, f, originalBackend)
			result, err := originalBackend.ExecuteHelmUninstall(ctx, consent, helmExecutionSession, f.Namespace, originalRow)
			writes := executionAudit.observed(t)
			if err == nil || result == nil || result.Outcome() != kube.HelmExecutionUnknown || !executionAudit.lost {
				t.Fatal("real committed response loss did not remain Unknown", err)
			}
			count, deleted, status := 1, 0, "uninstalling"
			if stage == "delete" {
				count, deleted = 2, 1
			} else if stage == "finish" {
				count, deleted, status = 4, 2, "uninstalled"
			}
			if len(writes) != count || writes[len(writes)-1].status < 200 || writes[len(writes)-1].status >= 300 {
				t.Fatal("fault was not after actual server acceptance or execution continued")
			}
			// This unwrapped client is independent of both product backends and
			// observes the server's actual retained state before reconciliation.
			f.retained(t, ctx, status)
			for index := range f.targets {
				if index < deleted {
					f.absent(t, ctx, index)
				} else {
					f.unchanged(t, ctx, index)
				}
			}
			assertHelmExecutionNoReplay(t, ctx, f, originalBackend, consent, originalRow, executionAudit)
			historyEffects, historyTargets := result.Effects(), result.Targets()
			consent.Discard()
			recovered := reopenHelmExecutionHistory(t, f, result)

			reads := &helmRecoveryTransport{}
			freshBackend := helmRecoveryBackend(t, f.Config, reads)
			session := "fresh-explicit-recovery-" + stage
			metadata, err := freshBackend.HelmReleaseMetadata(ctx, f.Namespace)
			if err != nil || len(metadata.Revisions) != 1 {
				t.Fatal("fresh backend metadata", err)
			}
			currentRow := metadata.Revisions[0]
			if currentRow.UID != originalRow.UID || currentRow.ResourceVersion == originalRow.ResourceVersion || currentRow.Status != status {
				t.Fatal("recovery was not bound to the independently observed current storage")
			}
			beforeConsent := reads.observed()
			if grant, err := freshBackend.AcknowledgeHelmUninstallReconciliation(recovered, session, f.Namespace, currentRow, false); err == nil || grant != nil {
				t.Fatal("reopened history silently authorized network observation")
			}
			grant, err := freshBackend.AcknowledgeHelmUninstallReconciliation(recovered, session, f.Namespace, currentRow, true)
			if err != nil {
				t.Fatal("fresh explicit GET-only recovery consent", err)
			}
			t.Cleanup(grant.Discard)
			if !reflect.DeepEqual(beforeConsent, reads.observed()) {
				t.Fatal("minting observation consent sent network requests")
			}
			observation, err := freshBackend.ObserveHelmUninstallRecovery(ctx, grant, session, f.Namespace, currentRow)
			if err != nil || observation == nil || observation.ID() != recovered.ID() || observation.Namespace() != f.Namespace ||
				observation.Revision() != currentRow || observation.HistoryOutcome() != kube.HelmExecutionUnknown {
				t.Fatal("fresh GET recovery did not keep history separate from current observation", err)
			}
			items := observation.Items()
			if len(items) != len(historyTargets) {
				t.Fatal("recovery omitted an original ordered target")
			}
			for index, item := range items {
				if item.Target != historyTargets[index] {
					t.Fatal("recovery retargeted the immutable historical inventory")
				}
				if index < deleted {
					if item.State != kube.HelmRecoveryTargetAbsent {
						t.Fatal("actual disappeared target was not observed absent", index, item.State)
					}
				} else if item.State != kube.HelmRecoveryTargetOriginal || item.UID != string(f.targets[index].UID) ||
					item.ResourceVersion != f.targets[index].ResourceVersion || item.Terminating || item.Finalizers || item.Owners || item.Keep || item.Hook {
					t.Fatal("unsent target was changed or misclassified during recovery", index, item.State)
				}
			}
			afterObservation := reads.observed()
			for _, request := range afterObservation {
				if request.method != http.MethodGet {
					t.Fatal("recovery attempted a mutation or dry-run", request.method)
				}
			}
			seenTargets := make(map[string]int)
			for _, request := range afterObservation[len(beforeConsent):] {
				seenTargets[request.path]++
			}
			for _, target := range historyTargets {
				path := "/api/v1/namespaces/" + f.Namespace + "/configmaps/" + target.Name
				if seenTargets[path] != 1 {
					t.Fatal("fresh recovery did not observe each exact target once")
				}
			}
			if _, err := freshBackend.ObserveHelmUninstallRecovery(ctx, grant, session, f.Namespace, currentRow); err == nil {
				t.Fatal("GET recovery consent could be consumed again")
			}
			if _, err := freshBackend.ExecuteHelmUninstall(ctx, consent, session, f.Namespace, currentRow); err == nil {
				t.Fatal("fresh backend recovered the old execution authority")
			}
			if !reflect.DeepEqual(afterObservation, reads.observed()) || !reflect.DeepEqual(writes, executionAudit.observed(t)) {
				t.Fatal("historical or consumed consent sent further requests")
			}
			again, err := f.journal.Recover(recovered.ID())
			if err != nil || again == nil || !again.Complete() || again.Outcome() != kube.HelmExecutionUnknown || result.Outcome() != kube.HelmExecutionUnknown ||
				!reflect.DeepEqual(again.Effects(), historyEffects) || !reflect.DeepEqual(result.Effects(), historyEffects) {
				t.Fatal("successful later reads rewrote the immutable Unknown history", err)
			}
			f.retained(t, ctx, status)
			for index := range f.targets {
				if index < deleted {
					f.absent(t, ctx, index)
				} else {
					f.unchanged(t, ctx, index)
				}
			}
		})
	}
}

func TestRealHelmUninstallRecoveryChangedTargets(t *testing.T) {
	for _, mode := range []string{"modified", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			f := newHelmExecutionFixture(t, ctx, 2)
			executionAudit := &helmExecutionTransport{lose: "begin"}
			originalBackend := helmExecutionBackend(t, f.Config, executionAudit)
			consent, originalRow := prepareHelmExecution(t, ctx, f, originalBackend)
			result, err := originalBackend.ExecuteHelmUninstall(ctx, consent, helmExecutionSession, f.Namespace, originalRow)
			writes := executionAudit.observed(t)
			if err == nil || result == nil || result.Outcome() != kube.HelmExecutionUnknown || !executionAudit.lost ||
				len(writes) != 1 || writes[0].status < 200 || writes[0].status >= 300 {
				t.Fatal("fixture did not stop after a real accepted begin response was lost", err)
			}
			f.retained(t, ctx, "uninstalling")
			consent.Discard()
			recovered := reopenHelmExecutionHistory(t, f, result)
			historyTargets, historyEffects := recovered.Targets(), recovered.Effects()

			// Change exactly one fixture target through an independent admin
			// client. This is an external fact, never an action of recovery.
			configs := f.Client.CoreV1().ConfigMaps(f.Namespace)
			current := f.targets[0].DeepCopy()
			oldUID, oldRV := current.UID, current.ResourceVersion
			wantState := kube.HelmRecoveryTargetModified
			if mode == "modified" {
				current.Annotations["aster.test/changed-after-attempt"] = "retained"
				current, err = configs.Update(ctx, current, metav1.UpdateOptions{})
			} else {
				wantState = kube.HelmRecoveryTargetReplacement
				err = configs.Delete(ctx, current.Name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &oldUID, ResourceVersion: &oldRV}})
				if err != nil {
					t.Fatal("independent replacement deletion", err)
				}
				f.absent(t, ctx, 0)
				current.ObjectMeta = metav1.ObjectMeta{Name: current.Name, Labels: current.Labels, Annotations: current.Annotations}
				current, err = configs.Create(ctx, current, metav1.CreateOptions{})
			}
			if err != nil || current.ResourceVersion == oldRV ||
				mode == "modified" && current.UID != oldUID || mode == "replacement" && current.UID == oldUID {
				t.Fatal("independent fixture did not establish the requested RV or UID change", err)
			}
			f.targets[0] = current

			reads := &helmRecoveryTransport{}
			freshBackend := helmRecoveryBackend(t, f.Config, reads)
			session := "fresh-changed-recovery-" + mode
			metadata, err := freshBackend.HelmReleaseMetadata(ctx, f.Namespace)
			if err != nil || len(metadata.Revisions) != 1 {
				t.Fatal("current recovery metadata", err)
			}
			currentRow := metadata.Revisions[0]
			grant, err := freshBackend.AcknowledgeHelmUninstallReconciliation(recovered, session, f.Namespace, currentRow, true)
			if err != nil {
				t.Fatal("fresh changed-target GET consent", err)
			}
			t.Cleanup(grant.Discard)
			observation, err := freshBackend.ObserveHelmUninstallRecovery(ctx, grant, session, f.Namespace, currentRow)
			if err != nil || observation == nil || observation.HistoryOutcome() != kube.HelmExecutionUnknown {
				t.Fatal("independent target change was not a readable current fact", err)
			}
			items := observation.Items()
			if len(items) != 2 || items[0].Target != historyTargets[0] || items[0].State != wantState ||
				items[0].UID != string(current.UID) || items[0].ResourceVersion != current.ResourceVersion ||
				items[1].Target != historyTargets[1] || items[1].State != kube.HelmRecoveryTargetOriginal ||
				items[1].UID != string(f.targets[1].UID) || items[1].ResourceVersion != f.targets[1].ResourceVersion {
				t.Fatal("recovery confused the original target with current modified/replacement identity")
			}
			for _, request := range reads.observed() {
				if request.method != http.MethodGet {
					t.Fatal("changed-target recovery attempted a write or dry-run", request.method)
				}
			}
			if !reflect.DeepEqual(writes, executionAudit.observed(t)) {
				t.Fatal("current target change resumed the original execution")
			}
			again, err := f.journal.Recover(recovered.ID())
			if err != nil || again == nil || again.Outcome() != kube.HelmExecutionUnknown ||
				!reflect.DeepEqual(again.Targets(), historyTargets) || !reflect.DeepEqual(again.Effects(), historyEffects) ||
				!reflect.DeepEqual(result.Targets(), historyTargets) || !reflect.DeepEqual(result.Effects(), historyEffects) {
				t.Fatal("reading modified/replacement metadata rewrote the historical target or effects", err)
			}
			f.retained(t, ctx, "uninstalling")
			f.unchanged(t, ctx, 0)
			f.unchanged(t, ctx, 1)
		})
	}
}
