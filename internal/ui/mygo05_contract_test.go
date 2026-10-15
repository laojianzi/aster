package uiworkbench

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

// v0.5 applies input using the previous frame's editability. The model may
// already be busy when that queued edit reaches the constructor.
func TestNativeMyGo05BusyTransitionRejectsQueuedFields(t *testing.T) {
	for _, mode := range []string{"Command", "Terminal", "Forward"} {
		t.Run(mode, func(t *testing.T) {
			w := New()
			defer w.Close()
			w.detail, w.detailKind, w.detailMode = modelObject("uid", "pod"), catalog()[0], mode
			w.containers = []string{"app", "other"}
			var field *string
			var busy *bool
			var label, original string
			switch mode {
			case "Command":
				field, busy, label, original = &w.commandArgv, &w.commandActive, "Command argv JSON", `["id"]`
			case "Terminal":
				field, busy, label, original = &w.terminalArgv, &w.terminalActive, "Terminal argv JSON", `["/bin/sh"]`
			default:
				field, busy, label, original = &w.forwardPort, &w.forwardActive, "Remote port", "8080"
			}
			*field = original
			tt := ui.NewTester(w.View, 1440, 1000)
			if err := tt.Click(label); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, ui.KeyA)
			*busy = true // Deliberately no Frame: the visible control was editable.
			tt.Type("queued-must-not-commit")
			if *field != original {
				t.Fatalf("busy transition committed queued input: %q", *field)
			}
			*busy = false
			tt.Frame()
			tt.Frame()
			if *field != original {
				t.Fatal("unlock replayed rejected input")
			}
			if err := tt.Click(label); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Type("fresh-edit")
			if *field != "fresh-edit" {
				t.Fatal("fresh editable input was lost")
			}
		})
	}
}

func TestNativeMyGo05BusyTransitionCannotRestoreConsumedConsent(t *testing.T) {
	for _, mode := range []string{"Command", "Terminal"} {
		t.Run(mode, func(t *testing.T) {
			w := New()
			defer w.Close()
			w.detail, w.detailKind, w.detailMode = modelObject("uid", "pod"), catalog()[0], mode
			w.containers = []string{"app"}
			value, busy, label := &w.commandConfirmation, &w.commandActive, "Confirm Pod for command"
			if mode == "Terminal" {
				value, busy, label = &w.terminalConfirmation, &w.terminalActive, "Confirm Pod for terminal"
			}
			tt := ui.NewTester(w.View, 1440, 1000)
			if err := tt.Click(label); err != nil {
				t.Fatal(err)
			}
			*busy = true
			*value = ""
			tt.Type("pod")
			if *value != "" {
				t.Fatal("busy input restored consumed consent")
			}
			*busy = false
			tt.Frame()
			tt.Frame()
			if *value != "" {
				t.Fatal("consent reappeared after unlock")
			}
		})
	}
}

func TestNativeMyGo05ScaledWorkbenchKeepsActionsVisible(t *testing.T) {
	for _, rtl := range []bool{false, true} {
		name := "ltr"
		if rtl {
			name = "rtl-host"
		}
		t.Run(name, func(t *testing.T) {
			w := New()
			defer w.Close()
			w.detail, w.detailKind, w.detailMode = modelObject("uid", "pod"), catalog()[0], "Command"
			w.containers = []string{"app"}
			tt := ui.NewTester(w.View, 1100, 700)
			if rtl {
				tt.SetLocale("ar")
				tt.SetDirection(ui.RightToLeft)
			}
			tt.SetPreferences(ui.Preferences{TextScale: 1.25, HighContrast: true, ReduceMotion: true})
			tt.Frame()
			for _, label := range []string{"Refresh detail", "Close detail", "Command container", "Command argv JSON", "Cluster context", "Namespace", "Connect", "Disconnect", "Kubeconfig path", "Load contexts", "Credentials", "Browser sign-in", "New workspace", "Run command", "Stop command", "Confirm Pod for command", "Metrics", "Owners", "Preview scale", "Preview restart", "Preview delete"} {
				r, ok := tt.Find(label)
				if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > 1100 || r.Y+r.H > 700 || r.W <= 0 || r.H <= 0 {
					t.Fatalf("scaled action %q missing/outside window: %+v", label, r)
				}
			}
			left, _ := tt.Find("Connect")
			right, _ := tt.Find("Disconnect")
			if left.X >= right.X {
				t.Fatal("untranslated workbench unexpectedly inherited mirrored action ordering")
			}
			saveNativeScreenshot(t, tt)
		})
	}
}
