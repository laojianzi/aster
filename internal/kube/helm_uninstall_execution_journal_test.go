package kube

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"k8s.io/client-go/rest"
)

func executionJournalSnapshot(f *helmExecutionFixture) (*HelmUninstallRecovery, error) {
	ids, err := f.journal.IDs()
	if err != nil {
		return nil, err
	}
	if len(ids) != 1 {
		return nil, errors.New("expected one operation")
	}
	return f.journal.Recover(ids[0])
}

// This server-side oracle runs only after the independent server has applied
// the request. It inspects actual file bytes while the executor awaits response.
func TestHelmUninstallExecutionDurableIntentBeforeHTTP(t *testing.T) {
	f := newHelmExecutionFixture(t, true)
	c := f.consent(t)
	f.onWrite = func(kind string) {
		r, err := executionJournalSnapshot(f)
		index := len(f.requests) - 1
		if err != nil || r == nil || r.Corrupt() || r.Complete() {
			f.bad = append(f.bad, "missing checked WAL at actual server write")
			return
		}
		effects := r.Effects()
		if len(effects) != 4 || effects[index].Kind != kind || effects[index].Outcome != HelmExecutionUnknown {
			f.bad = append(f.bad, "server write preceded durable exact intent")
			return
		}
		for i, effect := range effects {
			if i < index && effect.Outcome != HelmExecutionCompleted || i > index && effect.Outcome != HelmExecutionNotStarted {
				f.bad = append(f.bad, "WAL advanced before prior observation or started later work")
			}
		}
		if index == 3 && effects[index].ResourceVersion != effects[0].ObservedResourceVersion {
			f.bad = append(f.bad, "finish intent used hypothetical RV")
		}
		if !errors.Is(f.journal.Close(), helmjournal.ErrBusy) {
			f.bad = append(f.bad, "Close released executing lease")
		}
	}
	out, err := executeHelmFixture(f, c)
	if err != nil || out == nil || out.Outcome() != HelmExecutionCompleted || !out.JournalComplete() || out.JournalID() == "" {
		t.Fatal("durable complete execution", err)
	}
	f.inspect(t)
	if err := f.journal.Close(); err != nil {
		t.Fatal(err)
	}
	f.journal, err = OpenHelmUninstallJournal(f.journalDir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.journal.Recover(out.JournalID())
	if err != nil || !r.Complete() || r.Corrupt() || r.Outcome() != out.Outcome() ||
		!reflect.DeepEqual(r.Effects(), out.Effects()) || !reflect.DeepEqual(r.Targets(), out.Targets()) ||
		r.PreparationID() != out.PreparationID() || r.Revision() != out.Revision() ||
		!r.StartedAt().Equal(out.StartedAt()) || !r.FinishedAt().Equal(out.FinishedAt()) || !r.ExpiresAt().Equal(out.ExpiresAt()) {
		t.Fatal("fresh process-independent file projection differs", err)
	}
	r.Targets()[0].UID = "changed"
	r.Effects()[0].Outcome = HelmExecutionNotStarted
	if r.Targets()[0].UID == "changed" || r.Effects()[0].Outcome != HelmExecutionCompleted {
		t.Fatal("mutable recovery accessors")
	}
	wire, err := os.ReadFile(filepath.Join(f.journalDir, out.JournalID()+".wal"))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"PRIVATE", "Bearer", "current", "session", "manifest", "chart", "http://"} {
		if bytes.Contains(wire, []byte(private)) {
			t.Fatalf("journal retained forbidden %s", private)
		}
	}
}

func TestHelmUninstallExecutionJournalRequiredAndFrozen(t *testing.T) {
	for _, mode := range []string{"nil", "zero", "closed", "wrapper-reassigned", "store-wrapper-reassigned", "copied-close", "unbound"} {
		t.Run(mode, func(t *testing.T) {
			f := newHelmExecutionFixture(t, false)
			journal := f.journal
			if mode == "nil" {
				journal = nil
			}
			if mode == "zero" {
				journal = &HelmUninstallJournal{}
			}
			if mode == "unbound" {
				f.b.helmJournalBinding = ""
			}
			c, err := f.b.AcknowledgeHelmUninstallExecution(f.preflight, "session", "team", selectedHelmRevision(), true, journal)
			if mode == "nil" || mode == "zero" || mode == "unbound" {
				if err == nil || c != nil {
					t.Fatal("missing persistent destination or binding admitted")
				}
				requests, events, _ := f.inspect(t)
				if len(requests)+len(events) != 0 {
					t.Fatal("rejected mint performed HTTP")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "closed" || mode == "copied-close" {
				copy := *journal
				if err := copy.Close(); err != nil {
					t.Fatal(err)
				}
			} else if mode == "wrapper-reassigned" {
				saved := *journal
				*journal = HelmUninstallJournal{}
				f.journal = &saved
			} else if mode == "store-wrapper-reassigned" {
				saved := *journal.store
				*journal.store = helmjournal.Store{}
				f.journal = &HelmUninstallJournal{store: &saved}
			}
			out, err := executeHelmFixture(f, c)
			requests, _, _ := f.inspect(t)
			if mode == "closed" || mode == "copied-close" {
				if err == nil || out == nil || out.Outcome() != HelmExecutionNotStarted || len(requests) != 0 {
					t.Fatal("closed store did not stop before HTTP")
				}
			} else if err != nil || out == nil || !out.JournalComplete() || len(requests) != 3 {
				t.Fatal("caller wrapper retargeted consent", err)
			}
			if _, err := executeHelmFixture(f, c); err == nil {
				t.Fatal("journal failure or success refunded consent")
			}
		})
	}
}

// Change only the on-disk tail at selected real HTTP barriers. The production
// store must detect this and refuse subsequent effects; no fake success sink is
// accepted by the public executor. File-level tests cover short writes/Sync.
func TestHelmUninstallExecutionJournalFailureStopsEffects(t *testing.T) {
	for _, mode := range []string{"before-begin", "begin-result", "delete-result", "finish-result", "final-observed-result"} {
		t.Run(mode, func(t *testing.T) {
			f := newHelmExecutionFixture(t, false)
			c := f.consent(t)
			changed, finishReads := false, 0
			corrupt := func() {
				if changed {
					return
				}
				ids, err := f.journal.IDs()
				if err != nil || len(ids) != 1 {
					f.bad = append(f.bad, "fault did not locate operation")
					return
				}
				file, err := os.OpenFile(filepath.Join(f.journalDir, ids[0]+".wal"), os.O_WRONLY|os.O_APPEND, 0600)
				if err != nil {
					f.bad = append(f.bad, "fault did not open operation")
					return
				}
				_, writeErr := file.Write([]byte("torn"))
				closeErr := file.Close()
				if writeErr != nil || closeErr != nil {
					f.bad = append(f.bad, "fault did not change operation")
					return
				}
				changed = true
			}
			f.onWrite = func(kind string) {
				if mode == kind+"-result" {
					corrupt()
				}
			}
			f.onRead = func(path string) {
				if mode == "before-begin" {
					corrupt()
				}
				if mode == "final-observed-result" && f.patches == 2 {
					finishReads++
					// Two complete source guards (three GETs each) bracket the
					// last exact target absence; corrupt only at their last GET.
					if finishReads == 7 {
						corrupt()
					}
				}
			}
			out, err := executeHelmFixture(f, c)
			requests, _, _ := f.inspect(t)
			want := map[string]int{"before-begin": 0, "begin-result": 1, "delete-result": 2, "finish-result": 3, "final-observed-result": 3}[mode]
			if !changed || err == nil || !errors.Is(err, ErrHelmExecutionJournal) || out == nil || out.JournalComplete() || len(requests) != want {
				t.Fatal("persistence failure did not stop at the actual effect", changed, err, len(requests), want)
			}
			if mode == "final-observed-result" && out.Outcome() != HelmExecutionCompleted {
				t.Fatal("journal failure erased independently completed effects", out.Effects())
			}
			if mode == "finish-result" && out.Effects()[2].Outcome != HelmExecutionAcceptedPending {
				t.Fatal("failed result append erased valid server acknowledgement")
			}
			if _, err := executeHelmFixture(f, c); err == nil {
				t.Fatal("persistence failure replayed authority")
			}
			if err := f.journal.Close(); err != nil {
				t.Fatal(err)
			}
			f.journal, err = OpenHelmUninstallJournal(f.journalDir)
			if err != nil {
				t.Fatal(err)
			}
			r, err := f.journal.Recover(out.JournalID())
			if !errors.Is(err, helmjournal.ErrCorrupt) || r == nil || !r.Corrupt() || r.Complete() || r.Outcome() != HelmExecutionUnknown {
				t.Fatal("torn tail became a clean recovery", err)
			}
		})
	}
}

func TestHelmUninstallRecoveryConnectionBinding(t *testing.T) {
	base := &rest.Config{Host: "https://CLUSTER.example:6443/Prefix", TLSClientConfig: rest.TLSClientConfig{ServerName: "cluster.example", CAData: []byte("public-ca")}}
	want := helmUninstallConnectionBinding(base)
	if !helmReconciliationDigest(want) || strings.Contains(want, "cluster") {
		t.Fatal("invalid private projection")
	}
	for _, mode := range []string{"endpoint", "prefix-case", "server-name", "ca", "insecure", "impersonation", "protocols", "credentials", "wrapper", "query", "userinfo", "fragment", "opaque", "encoded-prefix", "ca-file", "custom-transport", "proxy", "dial"} {
		t.Run(mode, func(t *testing.T) {
			c := copyConnectionConfig(base)
			switch mode {
			case "endpoint":
				c.Host = "https://other.example:6443/Prefix"
			case "prefix-case":
				c.Host = "https://cluster.example:6443/prefix"
			case "server-name":
				c.ServerName = "other.example"
			case "ca":
				c.CAData = []byte("other-public-ca")
			case "insecure":
				c.Insecure = true
			case "impersonation":
				c.Impersonate.UserName = "other"
			case "protocols":
				c.NextProtos = []string{"http/1.1"}
			case "credentials":
				c.BearerToken = "PRIVATE_TOKEN"
				c.Password = "PRIVATE_PASSWORD"
				c.KeyData = []byte("PRIVATE_KEY")
			case "wrapper":
				c.Wrap(func(r http.RoundTripper) http.RoundTripper { return r })
			case "query":
				c.Host += "?q=1"
			case "userinfo":
				c.Host = "https://PRIVATE@cluster.example"
			case "fragment":
				c.Host += "#fragment"
			case "opaque":
				c.Host = "https:opaque"
			case "encoded-prefix":
				c.Host = "https://cluster.example/a%2fb"
			case "ca-file":
				c.CAData = nil
				c.CAFile = "PRIVATE_NEVER_READ"
			case "custom-transport":
				c.Transport = http.DefaultTransport
			case "proxy":
				c.Proxy = http.ProxyFromEnvironment
			case "dial":
				c.Dial = (&net.Dialer{}).DialContext
			}
			got := helmUninstallConnectionBinding(c)
			switch mode {
			case "credentials", "wrapper":
				if got != want {
					t.Fatal("credential or application wrapper became portable authority")
				}
			case "query", "userinfo", "fragment", "opaque", "encoded-prefix", "ca-file", "custom-transport", "proxy", "dial":
				if got != "" {
					t.Fatal("unqualified connection acquired recovery binding")
				}
			default:
				if got == "" || got == want {
					t.Fatal("different connection did not reject matching")
				}
			}
		})
	}
	if got := helmUninstallConnectionBinding(nil); got != "" {
		t.Fatal("nil binding")
	}
}

func TestHelmUninstallRecoveryEmptyHandles(t *testing.T) {
	var j *HelmUninstallJournal
	if _, err := j.IDs(); err == nil {
		t.Fatal("nil list")
	}
	if _, err := j.Recover("bad"); err == nil {
		t.Fatal("nil recovery")
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	var r *HelmUninstallRecovery
	if r.ID() != "" || r.PreparationID() != "" || r.Namespace() != "" || r.Revision() != (helmrelease.Revision{}) || r.Targets() != nil || r.Effects() != nil ||
		r.Outcome() != HelmExecutionNotStarted || !r.StartedAt().IsZero() || !r.FinishedAt().IsZero() || !r.ExpiresAt().IsZero() || r.Complete() || !r.Corrupt() {
		t.Fatal("nil recovery exposed authority or history")
	}
	var out *HelmUninstallExecution
	if out.JournalID() != "" || out.JournalComplete() {
		t.Fatal("nil ledger")
	}
}
