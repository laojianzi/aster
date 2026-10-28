package kube

import (
	"context"
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

// Build actual durable, expired v2 records, close the original writer/store,
// and reopen through the public product recovery API. No production test hook
// or in-memory fabricated recovery confers authority on these fixtures.
func installRecoveryHistory(t *testing.T, b *Backend, count int, mode string) (string, *HelmInstallRecovery, helmjournal.InstallPlan) {
	t.Helper()
	binding, err := b.helmUninstallJournalBinding()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Hour)
	p := helmjournal.InstallPlan{Binding: binding, CandidateID: strings.Repeat("a", 32), PreparationID: strings.Repeat("b", 32),
		Namespace: "team", NamespaceUID: "namespace-uid", NamespaceResourceVersion: "namespace-before", Release: "example", StorageName: "sh.helm.release.v1.example.v1",
		StorageDigest: strings.Repeat("c", 64), Started: started, Expires: started.Add(30 * time.Second)}
	for i := 0; i < count; i++ {
		p.Targets = append(p.Targets, helmjournal.InstallTarget{Name: fmt.Sprintf("target-%02d", i), Digest: fmt.Sprintf("%064x", i+1)})
	}
	dir := filepath.Join(t.TempDir(), "journal")
	s, err := helmjournal.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	w, err := s.BeginInstall(p)
	if err != nil {
		t.Fatal(err)
	}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal("durable install history", err)
		}
	}
	for i := 0; i < count+2; i++ {
		if mode == "not-started" || mode == "partial" && i == 1 {
			break
		}
		digest, uid, rv := p.StorageDigest, "storage-uid", "storage-before"
		if i > 0 && i <= count {
			digest, uid, rv = p.Targets[i-1].Digest, fmt.Sprintf("target-uid-%02d", i-1), fmt.Sprintf("target-rv-%02d", i-1)
		}
		if i == count+1 {
			digest, rv = strings.Repeat("d", 64), "storage-after"
		}
		check(w.Intent(i, digest))
		if (mode == "secret-unknown" || mode == "incomplete" || mode == "corrupt") && i == 0 || mode == "target-unknown" && i == 1 || mode == "deploy-unknown" && i == count+1 {
			break
		}
		if mode == "rejected" && i == 0 {
			check(w.Response(i, helmjournal.Rejected, "", ""))
			break
		}
		check(w.Response(i, helmjournal.AcceptedPending, uid, rv))
		if mode == "secret-pending" && i == 0 || mode == "target-pending" && i == 1 || mode == "deploy-pending" && i == count+1 {
			break
		}
		check(w.Observe(i, uid, rv))
	}
	if mode == "incomplete" {
		check(w.Abort())
	} else {
		check(w.Finish(started.Add(5 * time.Second)))
	}
	archived := mode == "archived"
	if archived {
		review, err := s.PrepareArchive(context.Background(), w.ID())
		check(err)
		grant, err := review.Confirm(w.ID())
		check(err)
		_, err = s.Archive(context.Background(), grant)
		check(err)
	}
	check(s.Close())
	if mode == "corrupt" {
		file, err := os.OpenFile(filepath.Join(dir, w.ID()+".wal"), os.O_WRONLY|os.O_APPEND, 0600)
		check(err)
		_, err = file.Write([]byte("PRIVATE_CORRUPT_TAIL"))
		check(err)
		check(file.Close())
	}
	j, err := OpenHelmUninstallJournal(dir)
	check(err)
	r, err := j.RecoverInstall(w.ID(), archived)
	if mode == "corrupt" {
		if err != helmjournal.ErrCorrupt || r == nil || !r.Corrupt() {
			t.Fatal("corrupt checked prefix lost", err)
		}
	} else {
		check(err)
	}
	check(j.Close())
	return dir, r, p
}

type installReconciliationFixture struct {
	mu        sync.Mutex
	b         *Backend
	config    *rest.Config
	dir       string
	recovery  *HelmInstallRecovery
	plan      helmjournal.InstallPlan
	namespace metav1.PartialObjectMetadata
	storage   *metav1.PartialObjectMetadata
	targets   map[string]*metav1.PartialObjectMetadata
	extra     []metav1.PartialObjectMetadata
	mode      string
	counts    map[string]int
	requests  []string
	bad       []string
	onRead    func(string, int)
}

func newInstallReconciliationFixture(t *testing.T, count int, history string) *installReconciliationFixture {
	t.Helper()
	f := &installReconciliationFixture{counts: map[string]int{}, targets: map[string]*metav1.PartialObjectMetadata{},
		namespace: metav1.PartialObjectMetadata{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "PartialObjectMetadata"}, ObjectMeta: metav1.ObjectMeta{Name: "team", UID: "namespace-uid", ResourceVersion: "namespace-now"}}}
	secret := liveObject()
	secret.Name, secret.UID, secret.ResourceVersion = "sh.helm.release.v1.example.v1", "storage-uid", "storage-after"
	secret.Labels = map[string]string{"owner": "helm", "name": "example", "version": "1", "status": "deployed", "private": "PRIVATE_LABEL"}
	f.storage = &secret
	for i := 0; i < count; i++ {
		m := liveObject()
		m.Name, m.UID, m.ResourceVersion = fmt.Sprintf("target-%02d", i), types.UID(fmt.Sprintf("target-uid-%02d", i)), fmt.Sprintf("target-rv-%02d", i)
		f.targets[m.Name] = &m
	}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	f.config = &rest.Config{Host: srv.URL + "/prefix", BearerToken: "PRIVATE_CURRENT_TOKEN", RateLimiter: flowcontrol.NewFakeAlwaysRateLimiter()}
	var err error
	f.b, err = New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	f.dir, f.recovery, f.plan = installRecoveryHistory(t, f.b, count, history)
	return f
}

func (f *installReconciliationFixture) consent(t *testing.T) *HelmInstallReconciliationConsent {
	t.Helper()
	c, err := f.b.AcknowledgeHelmInstallReconciliation(f.recovery, "new-session", "team", true)
	if err != nil || c == nil {
		t.Fatal("fresh read confirmation", err)
	}
	return c
}
func (f *installReconciliationFixture) inspect(t *testing.T) (map[string]int, []string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bad) != 0 {
		t.Fatal("request contract", f.bad)
	}
	counts := map[string]int{}
	for k, v := range f.counts {
		counts[k] = v
	}
	return counts, append([]string(nil), f.requests...)
}

func (f *installReconciliationFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer PRIVATE_CURRENT_TOKEN" || r.Header.Get("Accept-Encoding") != "identity" || r.ContentLength != 0 {
		f.bad = append(f.bad, "verb/identity/encoding/body")
	}
	path, route, name := strings.TrimPrefix(r.URL.Path, "/prefix"), "", ""
	switch {
	case path == "/api/v1/namespaces/team":
		route = "namespace"
	case path == "/api/v1/namespaces/team/secrets":
		route = "list"
	case path == "/api/v1/namespaces/team/secrets/sh.helm.release.v1.example.v1":
		route = "storage"
	case strings.HasPrefix(path, "/api/v1/namespaces/team/configmaps/"):
		route, name = "target", strings.TrimPrefix(path, "/api/v1/namespaces/team/configmaps/")
		if _, exists := f.targets[name]; !exists {
			f.bad = append(f.bad, "unplanned target")
		}
	default:
		f.bad = append(f.bad, "unplanned route or redirect followed")
		w.WriteHeader(500)
		return
	}
	f.counts[route]++
	f.requests = append(f.requests, route)
	if f.onRead != nil {
		f.onRead(route, f.counts[route])
	}
	wantAccept := liveMetadataAccept
	if route == "list" {
		wantAccept = helmrelease.Accept
		q := r.URL.Query()
		if q.Get("labelSelector") != "owner=helm" || q.Get("limit") != "64" || len(q) != 2+btoi(q.Get("continue") != "") {
			f.bad = append(f.bad, "list selector/pagination")
		}
	} else if r.URL.RawQuery != "" {
		f.bad = append(f.bad, "object query")
	}
	if r.Header.Get("Accept") != wantAccept {
		f.bad = append(f.bad, "metadata negotiation")
	}
	mode := ""
	if prefix, rest, ok := strings.Cut(f.mode, "-"); ok && prefix == route {
		mode = rest
	}
	if route == "list" {
		f.list(w, r, mode)
		return
	}
	m, resource, objectName := &f.namespace, "namespaces", "team"
	if route == "storage" {
		m, resource, objectName = f.storage, "secrets", "sh.helm.release.v1.example.v1"
	} else if route == "target" {
		m, resource, objectName = f.targets[name], "configmaps", name
	}
	if m == nil || strings.HasPrefix(mode, "absent") {
		f.absent(w, mode, resource, objectName)
		return
	}
	m = m.DeepCopy()
	switch mode {
	case "wrong-name":
		m.Name = "other"
	case "wrong-namespace":
		m.Namespace = "elsewhere"
	case "empty-uid":
		m.UID = ""
	case "control-uid":
		m.UID = "PRIVATE\nuid"
	case "empty-rv":
		m.ResourceVersion = ""
	case "control-rv":
		m.ResourceVersion = "PRIVATE\nrv"
	case "full-object":
		m.Kind, m.APIVersion = "Secret", "v1"
	}
	data, _ := json.Marshal(m)
	f.reply(w, r, mode, data, true)
}

func (f *installReconciliationFixture) list(w http.ResponseWriter, r *http.Request, mode string) {
	var rows []metav1.PartialObjectMetadata
	if f.storage != nil && f.storage.Labels["owner"] == "helm" {
		rows = append(rows, *f.storage.DeepCopy())
	}
	rows = append(rows, f.extra...)
	token, rv := "", "list-snapshot"
	n := f.counts["list"]
	switch mode {
	case "empty-null":
		data, _ := json.Marshal(metav1.PartialObjectMetadataList{TypeMeta: metav1.TypeMeta{APIVersion: "meta.k8s.io/v1", Kind: "PartialObjectMetadataList"}, ListMeta: metav1.ListMeta{ResourceVersion: rv}})
		f.reply(w, r, "", data, false)
		return
	case "duplicate":
		rows = append(rows, rows[0])
	case "pagination", "page-rv", "page-repeat", "page-duplicate", "page-token":
		cursor := "/PRIVATE_CURSOR?not=a&route=b"
		if r.URL.Query().Get("continue") == "" {
			token = cursor
		} else {
			if r.URL.Query().Get("continue") != cursor {
				f.bad = append(f.bad, "cursor escaped query")
			}
			rows = []metav1.PartialObjectMetadata{reconciliationOtherMeta(n)}
			if mode == "page-rv" {
				rv = "different-page-rv"
			}
			if mode == "page-repeat" {
				token = cursor
			}
			if mode == "page-duplicate" {
				rows = []metav1.PartialObjectMetadata{*f.storage.DeepCopy()}
			}
		}
	case "page-limit":
		rows, token = []metav1.PartialObjectMetadata{reconciliationOtherMeta(n)}, strconv.Itoa(n)
	case "count-limit":
		rows = nil
		for i := 0; i < helmrelease.PageSize; i++ {
			rows = append(rows, reconciliationOtherMeta((n-1)*helmrelease.PageSize+i))
		}
		token = strconv.Itoa(n)
	case "page-items":
		for i := 0; i < helmrelease.PageSize; i++ {
			rows = append(rows, reconciliationOtherMeta(i))
		}
	case "page-bytes":
		rows[0].Annotations = map[string]string{"private": strings.Repeat("p", helmrelease.MaxPageBytes)}
	case "total-bytes":
		rows = []metav1.PartialObjectMetadata{reconciliationOtherMeta(n)}
		rows[0].Annotations = map[string]string{"private": strings.Repeat("p", 240<<10)}
		token = strconv.Itoa(n)
	case "control-uid":
		rows[0].UID = "PRIVATE\nuid"
	case "control-rv":
		rv = "PRIVATE\nrv"
	case "storage-mismatch":
		rows[0].ResourceVersion = "contradictory-storage"
	case "storage-omitted":
		rows = nil
	}
	f.reply(w, r, mode, releasePage(token, rv, rows...), false)
}

func (f *installReconciliationFixture) absent(w http.ResponseWriter, mode, resource, name string) {
	s := metav1.Status{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"}, Status: metav1.StatusFailure, Code: 404, Reason: metav1.StatusReasonNotFound,
		Details: &metav1.StatusDetails{Name: name, Kind: resource}}
	switch mode {
	case "absent-wrong-name":
		s.Details.Name = "other"
	case "absent-wrong-kind":
		s.Details.Kind = "pods"
	case "absent-wrong-group":
		s.Details.Group = "other"
	case "absent-uid":
		s.Details.UID = "PRIVATE_UID"
	case "absent-cause":
		s.Details.Causes = []metav1.StatusCause{{Message: "PRIVATE_CAUSE"}}
	case "absent-retry":
		s.Details.RetryAfterSeconds = 1
	case "absent-listmeta":
		s.ResourceVersion = "PRIVATE_RV"
	case "absent-null-details":
		s.Details = nil
	}
	w.WriteHeader(404)
	_ = json.NewEncoder(w).Encode(s)
}

func (f *installReconciliationFixture) reply(w http.ResponseWriter, r *http.Request, mode string, data []byte, object bool) {
	if mode == "not-acceptable" {
		w.WriteHeader(http.StatusNotAcceptable)
		_, _ = w.Write([]byte("PRIVATE_NEGOTIATION"))
		return
	}
	if mode == "trailer-warning" {
		w.Header().Set("Trailer", "Warning")
		_, _ = w.Write(data)
		w.Header().Set("Warning", "PRIVATE_TRAILER")
		return
	}
	if mode == "secret-data" {
		data = append([]byte(`{"data":{"release":"PRIVATE_RELEASE"},`), data[1:]...)
	}
	(&helmReconciliationFixture{}).reply(w, r, mode, data, object)
}
