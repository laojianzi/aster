package uiworkbench

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/credentialvault"
	"github.com/laojianzi/aster/internal/kubeconfig"
	"github.com/laojianzi/aster/internal/testvault"
	"k8s.io/client-go/rest"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == credentialvault.HelperFlag {
		os.Exit(credentialvault.Serve(os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}

type vaultMemory struct {
	mu                  sync.Mutex
	data                map[string][]byte
	puts, gets, deletes int
}

func (s *vaultMemory) Put(_ context.Context, k string, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts++
	if s.data == nil {
		s.data = map[string][]byte{}
	}
	s.data[k] = append([]byte(nil), v...)
	return nil
}
func (s *vaultMemory) Get(_ context.Context, k string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++
	v, ok := s.data[k]
	if !ok {
		return nil, credentialvault.ErrNotFound
	}
	return append([]byte(nil), v...), nil
}
func (s *vaultMemory) Delete(_ context.Context, k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	delete(s.data, k)
	return nil
}
func setupVault(t *testing.T, h *relationshipHarness, cfg *rest.Config) *vaultMemory {
	t.Helper()
	s := &vaultMemory{}
	h.w.vaultStore = s
	h.w.path = testvault.ConfigFile(t, cfg, "team")
	h.w.currentContext = "vault-test"
	h.w.contexts = []string{"vault-test"}
	h.tt.Frame()
	h.click("Connections")
	h.click("Credentials")
	h.click("Review credential target")
	h.pump(func() bool { return !h.w.vaultPending })
	if h.w.vaultTarget == nil {
		t.Fatal(h.w.vaultStatus)
	}
	return s
}
func TestNativeVaultControlsAtMinimumWindow(t *testing.T) {
	h := newRelationshipHarness(t, 5*time.Second)
	setupVault(t, h, &rest.Config{Host: "https://example.invalid"})
	h.tt.SetSize(1100, 700)
	for _, label := range []string{"Review credential target", "Token to store", "Local token lifetime", "Confirm credential context", "Store token", "Connect with stored token", "Forget stored token", "Credential storage status"} {
		box, ok := h.tt.Find(label)
		if !ok || box.H < 8 || box.X < 0 || box.X+box.W > 1100 || box.Y+box.H > 700 {
			t.Fatal("credential control clipped", label, box)
		}
	}
	for _, label := range []string{"Token", "Storage lifetime", "Confirm context"} {
		if _, ok := h.tt.Find(label); !ok {
			t.Errorf("credential field has no persistent label: %s", label)
		}
	}
	if box, ok := h.tt.Find("Token to store"); !ok || box.W > 760 {
		t.Errorf("credential input exceeds readable form width: %+v", box)
	}
	saveNativeScreenshot(t, h.tt)
}
func TestNativeVaultRequiresExplicitConfirmationAndErasesInput(t *testing.T) {
	h := newRelationshipHarness(t, 5*time.Second)
	store := setupVault(t, h, &rest.Config{Host: "https://example.invalid"})
	h.w.vaultToken = "fixture-token"
	h.w.writeVault(false)
	if store.puts != 0 {
		t.Fatal("unconfirmed store")
	}
	h.w.vaultConfirmation = "vault-test"
	h.tt.Frame()
	h.click("Store token")
	if h.w.vaultToken != "" || h.w.vaultConfirmation != "" {
		t.Fatal("submitted input retained")
	}
	h.pump(func() bool { return !h.w.vaultPending })
	if store.puts != 1 || store.gets != 0 || h.w.backend != nil {
		t.Fatal("store connected or loaded implicitly")
	}
	h.w.vaultConfirmation = "vault-test"
	h.tt.Frame()
	h.click("Forget stored token")
	h.pump(func() bool { return !h.w.vaultPending })
	if store.deletes != 1 {
		t.Fatal("delete not performed")
	}
	h.w.vaultToken = "must-clear"
	h.click("Back to resources")
	if h.w.vaultToken != "" || h.w.vaultTarget != nil {
		t.Fatal("closed panel retained credential input")
	}
}
func TestNativeVaultConnectUsesExpiryAndNoImplicitLookup(t *testing.T) {
	cfg, calls, _ := authServer(t)
	h := newRelationshipHarness(t, 12*time.Second)
	store := setupVault(t, h, cfg)
	if calls.Load() != 0 || store.gets != 0 {
		t.Fatal("review made a network or store read")
	}
	if e := h.w.vaultTarget.Put(h.ctx, store, "fixture-token", time.Now().Add(4*time.Second)); e != nil {
		t.Fatal(e)
	}
	h.w.vaultConfirmation = "vault-test"
	h.tt.Frame()
	h.click("Connect with stored token")
	h.pump(func() bool { return h.w.status == "Live" })
	old := h.w.connectionCtx
	h.pump(func() bool { return h.w.status == "Credentials expired" })
	if old.Err() == nil || h.w.backend != nil || h.w.detail != nil || len(h.w.rows) > 0 || store.gets != 1 {
		t.Fatal("expiry/identity cleanup failed")
	}
}
func TestNativeVaultRevalidatesChangedProfileBeforeOSWrite(t *testing.T) {
	h := newRelationshipHarness(t, 5*time.Second)
	store := setupVault(t, h, &rest.Config{Host: "https://example.invalid"})
	raw, _ := os.ReadFile(h.w.path)
	if e := os.WriteFile(h.w.path, []byte(strings.ReplaceAll(string(raw), "example.invalid", "other.invalid")), 0600); e != nil {
		t.Fatal(e)
	}
	h.w.vaultToken = "never-store"
	h.w.vaultConfirmation = "vault-test"
	h.w.writeVault(false)
	h.pump(func() bool { return !h.w.vaultPending })
	if store.puts != 0 || !strings.Contains(h.w.vaultStatus, "invalid") {
		t.Fatal("changed profile retained review")
	}
}

type blockingVault struct{ start chan struct{} }

func (s blockingVault) Put(ctx context.Context, _ string, _ []byte) error {
	close(s.start)
	<-ctx.Done()
	return credentialvault.ErrUncertain
}
func (s blockingVault) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("unexpected lookup")
}
func (s blockingVault) Delete(context.Context, string) error { return errors.New("unexpected delete") }
func TestNativeVaultCancellationKeepsAdmissionUntilJoined(t *testing.T) {
	h := newRelationshipHarness(t, 5*time.Second)
	setupVault(t, h, &rest.Config{Host: "https://example.invalid"})
	s := blockingVault{make(chan struct{})}
	h.w.vaultStore = s
	h.w.vaultToken = "sensitive"
	h.w.vaultConfirmation = "vault-test"
	h.w.writeVault(false)
	select {
	case <-s.start:
	case <-time.After(time.Second):
		t.Fatal("store not started")
	}
	h.w.disconnect()
	epoch := h.w.contextEpoch
	h.w.connect()
	if h.w.contextEpoch != epoch {
		t.Fatal("new connection admitted before helper joined")
	}
	h.w.vaultStatus = "new target"
	h.pump(func() bool { return !h.w.vaultPending })
	if h.w.vaultStatus != "new target" || h.w.vaultToken != "" || h.w.vaultTarget != nil {
		t.Fatal("late store result crossed epoch")
	}
	// Changed identity cannot reconstruct the old reviewed slot from UI fields.
	if _, _, e := kubeconfig.LoadVault(kubeconfig.Options{Path: h.w.path, Context: "missing"}); e == nil {
		t.Fatal("missing context loaded")
	}
}

// Password input has editor-local undo history; clearing the bound string alone
// must not allow an earlier token to reappear after a completed submission.
func TestNativeVaultSubmissionCannotUndoTokenHistory(t *testing.T) {
	h := newRelationshipHarness(t, 6*time.Second)
	setupVault(t, h, &rest.Config{Host: "https://example.invalid"})
	h.click("Token to store")
	h.tt.Type("earlier-secret")
	h.tt.Key(ui.Cmd, ui.KeyA)
	h.tt.Type("replacement-secret")
	h.click("Confirm credential context")
	h.tt.Type("vault-test")
	h.click("Store token")
	h.pump(func() bool { return !h.w.vaultPending })
	h.click("Token to store")
	h.tt.Key(ui.Cmd, ui.KeyZ)
	h.tt.Frame()
	if h.w.vaultToken != "" {
		t.Fatal("submitted token recovered from input undo history")
	}
	h.tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	h.tt.Frame()
	if h.w.vaultToken != "" {
		t.Fatal("submitted token recovered from input redo history")
	}
}

func TestNativeVaultScaledReviewRemainsReachable(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{{"minimum", 900, 480}, {"tiled", 936, 499}, {"desktop", 1100, 700}} {
		t.Run(size.name, func(t *testing.T) {
			h := newRelationshipHarness(t, 15*time.Second)
			store := setupVault(t, h, &rest.Config{Host: "https://example.invalid"})
			h.w.vaultUncertain = true
			h.tt.SetSize(size.width, size.height)
			title, _ := h.tt.Find("OS credential storage")
			h.tt.SetPreferences(ui.Preferences{TextScale: 2, ReduceMotion: true})
			large, _ := h.tt.Find("OS credential storage")
			if large.H < title.H*1.8 {
				t.Fatal("vault heading ignores font preference")
			}
			viewport, ok := h.tt.Find("Credential task")
			if !ok {
				t.Fatal("credential task has no scroll viewport")
			}
			for _, label := range []string{"Token to store", "Confirm credential context", "Store token", "Connect with stored token", "Forget stored token", "Credential storage status"} {
				h.tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, -10000)
				reached := false
				for range 30 {
					r, ok := h.tt.Find(label)
					if ok && r.W > 0 && r.H > 0 && r.X >= viewport.X && r.X+r.W <= viewport.X+viewport.W && r.Y >= viewport.Y && r.Y+r.H <= viewport.Y+viewport.H {
						reached = true
						if label == "Confirm credential context" {
							h.click(label)
							h.tt.Type("vault-test")
						}
						break
					}
					h.tt.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 40)
				}
				if !reached {
					saveNativeScreenshot(t, h.tt)
					t.Fatalf("scaled credential control unreachable: %s", label)
				}
			}
			if h.w.vaultConfirmation != "vault-test" || store.puts+store.gets+store.deletes != 0 || h.w.backend != nil {
				t.Fatal("scrolling lost confirmation or accessed credential storage")
			}
			saveNativeScreenshot(t, h.tt)

		})
	}
}
