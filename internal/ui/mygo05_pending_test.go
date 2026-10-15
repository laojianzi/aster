package uiworkbench

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/credentialvault"
)

// A previous-frame editable control must not commit a queued choice after the
// model locks it. Exercise constructors rather than checking only helper code.
func TestNativeMyGo05GuardedChoiceTransitions(t *testing.T) {
	t.Run("checkbox", func(t *testing.T) {
		editable, value := true, false
		tt := ui.NewTester(func(c *ui.Context) {
			guardedCheckbox(c, "choice", &value, "Consent", editable)
		}, 400, 200)
		editable = false // no Frame: the visible control is still enabled
		if err := tt.Click("Consent"); err != nil {
			t.Fatal(err)
		}
		if value {
			t.Fatal("queued consent crossed a lock")
		}
		editable = true
		tt.Frame()
		tt.Frame()
		if value {
			t.Fatal("unlock replayed consent")
		}
		if err := tt.Click("Consent"); err != nil {
			t.Fatal(err)
		}
		if !value {
			t.Fatal("fresh consent was not committed")
		}
	})
	t.Run("select", func(t *testing.T) {
		editable, value := true, "first"
		tt := ui.NewTester(func(c *ui.Context) {
			guardedSelect(c, "choice", &value, []string{"first", "second"}, editable).Label("Lifetime")
		}, 400, 300)
		if err := tt.Click("Lifetime"); err != nil {
			t.Fatal(err)
		}
		tt.Key(0, ui.KeyHome)
		tt.Key(0, ui.KeyDown)
		editable = false // the open popup was enabled when its input was queued
		tt.Key(0, ui.KeyEnter)
		if value != "first" {
			t.Fatal("queued choice crossed a lock")
		}
		editable = true
		tt.Frame()
		tt.Frame()
		if value != "first" {
			t.Fatal("unlock replayed a choice")
		}
		// Close any surviving popup, then make an independent fresh choice.
		tt.Key(0, ui.KeyEscape)
		if err := tt.Click("Lifetime"); err != nil {
			t.Fatal(err)
		}
		tt.Key(0, ui.KeyHome)
		tt.Key(0, ui.KeyDown)
		tt.Key(0, ui.KeyEnter)
		if value != "second" {
			t.Fatalf("fresh choice lost: %q", value)
		}
	})
}

func TestNativeMyGo05PendingSensitiveFieldsDoNotInvalidateReview(t *testing.T) {
	for _, label := range []string{
		"Helm distribution archive", "Helm chart archive", "Helm values file",
		"Helm release name", "Helm Kubernetes version", "Token to store",
		"Confirm credential context", "Confirm OIDC context",
	} {
		t.Run(label, func(t *testing.T) {
			w := New()
			defer w.Close()
			var value *string
			var lock func()
			var unchanged func() bool
			switch label {
			case "Token to store", "Confirm credential context":
				w.vaultOpen = true
				w.vaultTarget = &credentialvault.Target{Context: "reviewed", Server: "https://example.invalid", User: "user"}
				value = &w.vaultConfirmation
				if label == "Token to store" {
					value = &w.vaultToken
				}
				lock = func() { w.vaultPending = true }
				unchanged = func() bool { return w.vaultPending && w.vaultTarget != nil }
			case "Confirm OIDC context":
				w.oidcOpen = true
				w.oidcTarget = &credentialvault.Target{Context: "reviewed", Server: "https://example.invalid", User: "user"}
				value = &w.oidcConfirmation
				lock = func() { w.oidcPending = true }
				unchanged = func() bool { return w.oidcPending && w.oidcTarget != nil }
			default:
				d := &w.delivery
				d.open, d.helm.open, d.helm.trusted = true, true, true
				d.editor = ui.NewTextBuffer("retained raw input\r\n")
				d.observed = d.editor.Snapshot()
				fields := map[string]*string{
					"Helm distribution archive": &d.helm.archive, "Helm chart archive": &d.helm.chart,
					"Helm values file": &d.helm.values, "Helm release name": &d.helm.release,
					"Helm Kubernetes version": &d.helm.kubeVersion,
				}
				value = fields[label]
				lock = func() { d.busy = "Rendering Helm" }
				unchanged = func() bool { return d.busy == "Rendering Helm" && d.helm.trusted }
			}
			*value = "original"
			tt := ui.NewTester(w.View, 1440, 1000)
			if err := tt.Click(label); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, ui.KeyA)
			lock()
			vaultEpoch, oidcEpoch, deliveryEpoch := w.vaultEpoch, w.oidcEpoch, w.delivery.epoch
			tt.Type("queued-replacement")
			if *value != "original" || !unchanged() || w.vaultEpoch != vaultEpoch || w.oidcEpoch != oidcEpoch || w.delivery.epoch != deliveryEpoch {
				t.Fatal("locked input altered value, review identity, trust or pending operation")
			}
			w.vaultPending, w.oidcPending, w.delivery.busy = false, false, ""
			tt.Frame()
			tt.Frame()
			if *value != "original" {
				t.Fatal("unlock replayed discarded input")
			}
		})
	}
}

func TestNativeMyGo05QueuedRenewalOptInDoesNotExpandScope(t *testing.T) {
	w := New()
	defer w.Close()
	w.oidcOpen = true
	tt := ui.NewTester(w.View, 1440, 1000)
	w.oidcPending = true
	epoch := w.oidcEpoch
	if err := tt.Click("Allow memory-only renewal (requests offline access and consent)"); err != nil {
		t.Fatal(err)
	}
	if w.oidcAllowRenewal || !w.oidcPending || w.oidcEpoch != epoch {
		t.Fatal("queued opt-in changed the pending identity scope")
	}
	w.oidcPending = false
	tt.Frame()
	tt.Frame()
	if w.oidcAllowRenewal {
		t.Fatal("unlock replayed renewal opt-in")
	}
	if err := tt.Click("Allow memory-only renewal (requests offline access and consent)"); err != nil {
		t.Fatal(err)
	}
	if !w.oidcAllowRenewal || w.oidcEpoch == epoch {
		t.Fatal("fresh opt-in must update scope and invalidate old review")
	}
}
