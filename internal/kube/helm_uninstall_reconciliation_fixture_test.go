package kube

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/flowcontrol"
)

type helmReconciliationFixture struct {
	mu                        sync.Mutex
	b                         *Backend
	config                    *rest.Config
	recovery                  *HelmUninstallRecovery
	row                       helmrelease.Revision
	targets                   []helmrelease.UninstallTarget
	mode                      string
	lists, objects, redirects int
	requests, bad             []string
	onRead                    func(string)
}

// These fixtures reopen actual durable bytes through the product recovery API.
// Their original authority expired an hour ago; only a newly acknowledged GET
// grant can observe them. No execution preparation or consent is reconstructed.
func reconciliationFixtureRecovery(t *testing.T, b *Backend, targets []helmrelease.UninstallTarget) *HelmUninstallRecovery {
	t.Helper()
	return reconciliationFixtureHistory(t, b, targets, "closed")
}

func reconciliationFixtureHistory(t *testing.T, b *Backend, targets []helmrelease.UninstallTarget, mode string) *HelmUninstallRecovery {
	t.Helper()
	binding, err := b.helmUninstallJournalBinding()
	if err != nil {
		t.Fatal("journal binding", err)
	}
	dir := filepath.Join(t.TempDir(), "journal")
	store, err := helmjournal.Open(dir)
	if err != nil {
		t.Fatal("create private journal", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	started := time.Now().Add(-time.Hour)
	plan := helmjournal.Plan{Binding: binding, PreparationID: strings.Repeat("a", 32), Namespace: "team", Revision: selectedHelmRevision(),
		Targets: append([]helmrelease.UninstallTarget(nil), targets...), Started: started, Expires: started.Add(30 * time.Second)}
	writer, err := store.Begin(plan)
	if err != nil {
		t.Fatal("durable historical plan", err)
	}
	if err := writer.Intent(0, plan.Revision.ResourceVersion); err != nil {
		t.Fatal("durable historical intent", err)
	}
	if mode == "incomplete" {
		if err := writer.Abort(); err != nil {
			t.Fatal("interrupted historical attempt", err)
		}
	} else {
		if err := writer.Finish(started.Add(5 * time.Second)); err != nil {
			t.Fatal("finished historical attempt", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal("close original store", err)
	}
	if mode == "corrupt" {
		file, err := os.OpenFile(filepath.Join(dir, writer.ID()+".wal"), os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write([]byte("PRIVATE_CORRUPT_TAIL")); err != nil {
			t.Fatal(err)
		}
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := OpenHelmUninstallJournal(dir)
	if err != nil {
		t.Fatal("reopen historical store", err)
	}
	defer reopened.Close()
	r, err := reopened.Recover(writer.ID())
	if mode == "corrupt" {
		if err == nil || r == nil || !r.Corrupt() {
			t.Fatal("corrupt history was trusted", err)
		}
		return r
	}
	if err != nil || r == nil || r.Corrupt() || r.Outcome() != HelmExecutionUnknown || r.Complete() != (mode != "incomplete") {
		t.Fatal("reopened Unknown history", err)
	}
	return r
}

func newHelmReconciliationFixture(t *testing.T, count int) *helmReconciliationFixture {
	t.Helper()
	f := &helmReconciliationFixture{row: selectedHelmRevision()}
	f.row.Status, f.row.ResourceVersion = "uninstalling", "observed-storage-rv"
	for i := 0; i < count; i++ {
		f.targets = append(f.targets, helmrelease.UninstallTarget{APIVersion: "v1", Kind: "ConfigMap", Resource: "configmaps", Namespace: "team",
			Name: fmt.Sprintf("target-%02d", i), UID: fmt.Sprintf("original-uid-%02d", i), ResourceVersion: fmt.Sprintf("rv-%02d", i)})
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	f.config = &rest.Config{Host: srv.URL + "/prefix", BearerToken: "PRIVATE_CURRENT_TOKEN", RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()}
	var err error
	f.b, err = New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	f.recovery = reconciliationFixtureRecovery(t, f.b, f.targets)
	return f
}

func (f *helmReconciliationFixture) consent(t *testing.T) *HelmUninstallReconciliationConsent {
	t.Helper()
	c, err := f.b.AcknowledgeHelmUninstallReconciliation(f.recovery, "new-session", "team", f.row, true)
	if err != nil || c == nil {
		t.Fatal("fresh read consent", err)
	}
	return c
}

func (f *helmReconciliationFixture) inspect(t *testing.T) (int, int, []string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bad) != 0 {
		t.Fatal("request invariant", f.bad)
	}
	if f.redirects != 0 {
		t.Fatal("redirect followed")
	}
	return f.lists, f.objects, append([]string(nil), f.requests...)
}

func (f *helmReconciliationFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer PRIVATE_CURRENT_TOKEN" || r.Header.Get("Accept-Encoding") != "identity" || r.Body != nil && r.ContentLength != 0 {
		f.bad = append(f.bad, "changed method, identity, encoding or body")
	}
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if f.onRead != nil {
		f.onRead(r.URL.Path)
	}
	switch {
	case r.URL.Path == "/prefix/api/v1/namespaces/team/secrets":
		f.lists++
		if r.Header.Get("Accept") != helmrelease.Accept || r.URL.Query().Get("labelSelector") != "owner=helm" || r.URL.Query().Get("limit") != strconv.Itoa(helmrelease.PageSize) || len(r.URL.Query()) > 3 {
			f.bad = append(f.bad, "changed metadata-only list or query")
		}
		f.list(w, r)
	case strings.HasPrefix(r.URL.Path, "/prefix/api/v1/namespaces/team/configmaps/"):
		f.objects++
		if r.Header.Get("Accept") != liveMetadataAccept || r.URL.RawQuery != "" {
			f.bad = append(f.bad, "changed metadata-only object or query")
		}
		name := strings.TrimPrefix(r.URL.Path, "/prefix/api/v1/namespaces/team/configmaps/")
		for _, target := range f.targets {
			if name == target.Name {
				f.target(w, r, target)
				return
			}
		}
		f.bad = append(f.bad, "unreviewed target")
		w.WriteHeader(http.StatusInternalServerError)
	case r.URL.Path == "/must-not-follow":
		f.redirects++
		w.WriteHeader(http.StatusInternalServerError)
	default:
		f.bad = append(f.bad, "unqualified route")
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func reconciliationRevisionMeta(row helmrelease.Revision) metav1.PartialObjectMetadata {
	return metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: row.StorageName, Namespace: "team", UID: types.UID(row.UID), ResourceVersion: row.ResourceVersion,
		Labels: map[string]string{"owner": "helm", "name": row.Release, "version": strconv.Itoa(int(row.Number)), "status": row.Status}}}
}

func reconciliationOtherMeta(i int) metav1.PartialObjectMetadata {
	row := helmrelease.Revision{Release: fmt.Sprintf("other-%03d", i), Number: 1, Status: "deployed", UID: fmt.Sprintf("other-uid-%03d", i), ResourceVersion: "other-rv"}
	row.StorageName = "sh.helm.release.v1." + row.Release + ".v1"
	return reconciliationRevisionMeta(row)
}

func (f *helmReconciliationFixture) list(w http.ResponseWriter, r *http.Request) {
	mode := strings.TrimPrefix(f.mode, "list-")
	row := reconciliationRevisionMeta(f.row)
	rows := []metav1.PartialObjectMetadata{row}
	token, rv := "", "collection-current"
	switch mode {
	case "missing":
		rows = []metav1.PartialObjectMetadata{}
	case "older":
		old := row.DeepCopy()
		old.Name = "sh.helm.release.v1.example.v1"
		old.UID = "old-storage"
		old.Labels["version"] = "1"
		old.Labels["status"] = "superseded"
		rows = append(rows, *old)
	case "newer":
		next := row.DeepCopy()
		next.Name = "sh.helm.release.v1.example.v3"
		next.UID = "next-storage"
		next.Labels["version"] = "3"
		next.Labels["status"] = "pending-upgrade"
		rows = append(rows, *next)
	case "replaced":
		rows[0].UID = "replacement-storage"
	case "rv-before":
		rows[0].ResourceVersion = "changed-before"
	case "status-before":
		rows[0].Labels["status"] = "uninstalled"
	case "rv-after":
		if f.lists == 2 {
			rows[0].ResourceVersion = "changed-after"
		}
	case "status-after":
		if f.lists == 2 {
			rows[0].Labels["status"] = "uninstalled"
		}
	case "missing-after":
		if f.lists == 2 {
			rows = []metav1.PartialObjectMetadata{}
		}
	case "duplicate":
		rows = append(rows, row)
	case "unrelated":
		rows = append(rows, reconciliationOtherMeta(f.lists))
	case "pagination", "page-rv", "page-repeat", "page-duplicate", "page-token":
		cursor := "cursor"
		if mode == "page-token" {
			cursor = "/PRIVATE_CURSOR?not=a&route=b"
		}
		if r.URL.Query().Get("continue") == "" {
			token = cursor
		} else {
			if r.URL.Query().Get("continue") != cursor {
				f.bad = append(f.bad, "cursor escaped into query or route")
			}
			rows = []metav1.PartialObjectMetadata{reconciliationOtherMeta(f.lists)}
			if mode == "page-rv" {
				rv = "different-page-rv"
			}
			if mode == "page-repeat" {
				token = cursor
			}
			if mode == "page-duplicate" {
				rows = []metav1.PartialObjectMetadata{row}
			}
		}
		if f.lists > 2 && mode == "pagination" {
			rv = "different-next-snapshot"
		}
	case "page-limit":
		if f.lists > 1 {
			rows = []metav1.PartialObjectMetadata{reconciliationOtherMeta(f.lists)}
		}
		token = strconv.Itoa(f.lists)
	case "count-limit":
		rows = nil
		for i := 0; i < helmrelease.PageSize; i++ {
			rows = append(rows, reconciliationOtherMeta((f.lists-1)*helmrelease.PageSize+i))
		}
		if f.lists == 1 {
			rows[0] = row
		}
		token = strconv.Itoa(f.lists)
	case "page-items":
		for i := 0; i < helmrelease.PageSize; i++ {
			rows = append(rows, reconciliationOtherMeta(i))
		}
	case "page-bytes":
		rows[0].Annotations = map[string]string{"private": strings.Repeat("p", helmrelease.MaxPageBytes)}
	case "total-bytes":
		if f.lists > 1 {
			rows = []metav1.PartialObjectMetadata{reconciliationOtherMeta(f.lists)}
		}
		rows[0].Annotations = map[string]string{"private": strings.Repeat("p", 240<<10)}
		token = strconv.Itoa(f.lists)
	}
	data := releasePage(token, rv, rows...)
	f.reply(w, r, mode, data, false)
}

func (f *helmReconciliationFixture) target(w http.ResponseWriter, r *http.Request, target helmrelease.UninstallTarget) {
	mode := strings.TrimPrefix(f.mode, "target-")
	m := liveObject()
	m.Name, m.Namespace, m.UID, m.ResourceVersion = target.Name, target.Namespace, types.UID(target.UID), target.ResourceVersion
	switch mode {
	case "modified":
		m.ResourceVersion = "opaque-rv:changed"
	case "replacement":
		m.UID = "replacement-uid"
	case "mixed":
		switch f.objects {
		case 2:
			m.ResourceVersion = "opaque-rv:changed"
		case 3:
			m.UID = "replacement-uid"
		case 4:
			mode = "absent"
		}
	case "flags":
		now := metav1.Now()
		m.DeletionTimestamp = &now
		m.Finalizers = []string{"PRIVATE_FINALIZER"}
		m.OwnerReferences = []metav1.OwnerReference{{Name: "PRIVATE_OWNER"}}
		m.Annotations["helm.sh/resource-policy"] = "PRIVATE_POLICY"
		m.Annotations["helm.sh/hook"] = "PRIVATE_HOOK"
	case "marker-missing":
		delete(m.Labels, "app.kubernetes.io/managed-by")
	case "marker-mismatch":
		m.Annotations["meta.helm.sh/release-name"] = "PRIVATE_OTHER_RELEASE"
	case "wrong-name":
		m.Name = "unreviewed-name"
	case "wrong-namespace":
		m.Namespace = "outside"
	case "empty-uid":
		m.UID = ""
	case "control-uid":
		m.UID = "uid\nPRIVATE"
	case "empty-rv":
		m.ResourceVersion = ""
	case "control-rv":
		m.ResourceVersion = "rv\nPRIVATE"
	case "full-object":
		m.Kind, m.APIVersion = "ConfigMap", "v1"
	}
	data, _ := json.Marshal(m)
	if strings.HasPrefix(mode, "absent") {
		s := metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: "Failure", Reason: metav1.StatusReasonNotFound, Code: 404,
			Details: &metav1.StatusDetails{Name: target.Name, Kind: "configmaps"}}
		switch mode {
		case "absent-wrong-name":
			s.Details.Name = "other"
		case "absent-wrong-kind":
			s.Details.Kind = "secrets"
		case "absent-wrong-group":
			s.Details.Group = "other"
		case "absent-uid":
			s.Details.UID = "old-uid"
		case "absent-cause":
			s.Details.Causes = []metav1.StatusCause{{Type: metav1.CauseTypeFieldValueInvalid, Message: "PRIVATE"}}
		case "absent-retry":
			s.Details.RetryAfterSeconds = 1
		case "absent-listmeta":
			s.ResourceVersion = "PRIVATE"
		case "absent-null-details":
			s.Details = nil
		}
		data, _ = json.Marshal(s)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(data)
		return
	}
	f.reply(w, r, mode, data, true)
}

func (f *helmReconciliationFixture) reply(w http.ResponseWriter, r *http.Request, mode string, data []byte, target bool) {
	switch mode {
	case "forbidden":
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("PRIVATE_SERVER_RESPONSE"))
		return
	case "unauthorized":
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("PRIVATE_IDENTITY_ERROR"))
		return
	case "retry":
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("PRIVATE_PROXY"))
		return
	case "redirect":
		w.Header().Set("Location", "/must-not-follow")
		w.WriteHeader(http.StatusTemporaryRedirect)
		return
	case "proxy404":
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("<p>PRIVATE_PROXY</p>"))
		return
	case "invalid-json":
		data = []byte("{PRIVATE_JSON")
	case "duplicate-field":
		data = append([]byte(`{"kind":"ignored",`), data[1:]...)
	case "unknown-field":
		data = append([]byte(`{"privateUnknown":"PRIVATE",`), data[1:]...)
	case "null":
		data = []byte("null")
	case "null-metadata":
		var object map[string]any
		_ = json.Unmarshal(data, &object)
		object["metadata"] = nil
		data, _ = json.Marshal(object)
	case "missing-content-type":
		w.Header()["Content-Type"] = nil
	case "duplicate-content-type":
		w.Header().Add("Content-Type", "application/json")
	case "wrong-content-type":
		w.Header().Set("Content-Type", "application/yaml")
	case "warning":
		w.Header().Add("Warning", `299 kube "PRIVATE_WARNING"`)
	case "encoding":
		w.Header().Set("Content-Encoding", "gzip")
	case "duplicate-encoding":
		w.Header().Add("Content-Encoding", "identity")
		w.Header().Add("Content-Encoding", "identity")
	case "truncated":
		w.Header().Set("Content-Length", strconv.Itoa(len(data)+128))
	case "oversized-length":
		limit := helmrelease.MaxPageBytes
		if target {
			limit = maxLiveObjectBytes
		}
		w.Header().Set("Content-Length", strconv.Itoa(limit+1))
	case "oversized-stream":
		limit := helmrelease.MaxPageBytes
		if target {
			limit = maxLiveObjectBytes
		}
		data = []byte(strings.Repeat("p", limit+1))
		w.(http.Flusher).Flush()
	case "full-object":
		if !target {
			data = []byte(`{"apiVersion":"v1","kind":"SecretList","metadata":{},"items":[]}`)
		}
	}
	_, _ = w.Write(data)
}
