package uiworkbench

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/cluster"
	"github.com/laojianzi/aster/internal/credentialvault"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/nativeterm"
	"github.com/laojianzi/aster/internal/oidclogin"
	"github.com/laojianzi/aster/internal/operation"
	"github.com/laojianzi/aster/internal/relationship"
	"github.com/laojianzi/aster/internal/resourcemetrics"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Workbench state belongs to the UI goroutine. Workers capture immutable inputs
// and submit changes through emit; they never access live widget state.
type Workbench struct {
	workspaceNumber int
	newWorkspace    func() error
	ctx             context.Context
	cancel          context.CancelFunc
	life            *cluster.Session
	dispatch        func(func())

	contextEpoch, scopeEpoch, detailEpoch, draftRevision uint64
	scopeCancel, logCancel, connectionCancel             context.CancelFunc
	connectionCtx                                        context.Context
	connectionPending                                    bool
	credentialExpiry                                     time.Time
	activeContext, activeNamespace                       string
	backend                                              *kube.Backend
	ops                                                  *operation.Service
	sessionID                                            string
	path, currentContext, namespace, labelSelector       string
	contexts                                             []string
	trustedFingerprint, pendingTrustFingerprint          string
	trustRequired                                        bool

	kinds                   []kube.ResourceKind
	currentKind             kube.ResourceKind
	kindChoice              string
	status, errText, notice string
	filter, sortBy          string
	rows                    []resourceRow
	total                   int
	store                   *rowStore
	table, logList          ui.ListState
	selected                int

	detail                                                            *unstructured.Unstructured
	detailKind                                                        kube.ResourceKind
	detailMode, detailText, editor, diff, confirmation, detailMessage string
	containers                                                        []string
	container, replicas                                               string
	plan                                                              *operation.Prepared
	preparing, creating                                               bool
	pendingWrites                                                     int
	eventsText                                                        string
	eventsRevision                                                    uint64

	logEpoch   uint64
	logRows    []string
	logStatus  string
	logDropped uint64

	forwardCancel                              context.CancelFunc
	forwardEpoch                               uint64
	forwardPort, forwardStatus, forwardAddress string
	forwardLocal                               uint16
	forwardActive                              bool

	healthEpoch  uint64
	healthCancel context.CancelFunc
	healthActive bool
	healthText   string

	commandEpoch                                                                     uint64
	commandCancel                                                                    context.CancelFunc
	commandActive                                                                    bool
	commandArgv, commandContainer, commandConfirmation, commandOutput, commandStatus string

	terminal                                                              *nativeterm.Terminal
	terminalEpoch                                                         uint64
	terminalCancel                                                        context.CancelFunc
	terminalActive, terminalJoining                                       bool
	terminalArgv, terminalContainer, terminalConfirmation, terminalStatus string

	relatedEpoch  uint64
	relatedCancel context.CancelFunc
	relatedActive bool
	relatedResult relationship.Snapshot
	relatedStatus string
	relatedList   ui.ListState

	metricsEpoch                  uint64
	metricsCancel                 context.CancelFunc
	metricsActive, metricsLoading bool
	metricsResult                 resourcemetrics.Snapshot
	metricsHistory                resourcemetrics.History
	metricsList                   ui.ListState

	applyDraft, ownersText string
	applyAcknowledged      bool

	schemaEpoch                             uint64
	schemaCancel                            context.CancelFunc
	schemaActive                            bool
	schemaPointer, schemaText, schemaStatus string

	previewCancel context.CancelFunc
	previewEpoch  uint64

	vaultMutation, vaultUncertain                             bool
	vaultOpen, vaultPending                                   bool
	vaultEpoch                                                uint64
	vaultCancel                                               context.CancelFunc
	vaultToken, vaultDuration, vaultConfirmation, vaultStatus string
	vaultTrust, vaultTrustPending                             string
	vaultTarget                                               *credentialvault.Target
	vaultStore                                                credentialvault.Store

	oidcOpen, oidcPending                                                  bool
	oidcEpoch                                                              uint64
	oidcCancel                                                             context.CancelFunc
	oidcIssuer, oidcClient, oidcCA, oidcPort, oidcConfirmation, oidcStatus string
	oidcTrust, oidcTrustPending                                            string
	oidcTarget                                                             *credentialvault.Target
	oidcReview                                                             *oidclogin.Review
	oidcIdentity                                                           *oidclogin.Identity
	oidcBrowser                                                            func(string) error

	history []string
	frames  int
}

func New() *Workbench {
	w := &Workbench{ctx: context.Background(), status: "Not connected", sortBy: "Name", selected: -1, replicas: "1", detailMode: "YAML"}
	w.vaultDuration = "1 hour"
	w.oidcPort = "0"
	w.vaultStore = credentialvault.Client{}
	w.kinds = catalog()
	w.currentKind = w.kinds[0]
	w.kindChoice = w.currentKind.Label()
	w.table.Selected = &w.selected
	w.table.Key = func(i int) any {
		if i < 0 || i >= len(w.rows) {
			return ""
		}
		return w.rows[i].key()
	}
	return w
}
func formatReplicas(ready, desired int64) string { return fmt.Sprintf("%d / %d ready", ready, desired) }

// WindowOptions are window-local callbacks, always invoked on the UI thread.
// Every window starts disconnected and does not inherit another window's trust,
// kubeconfig selection, credentials, prepared operations or streaming sessions.
type WindowOptions struct {
	Number       int
	NewWorkspace func() error
	Closed       func()
	SmokeFrame   func(frame int, closeWindow func())
}

func Open(parent context.Context, options WindowOptions) *Workbench {
	w := New()
	w.workspaceNumber, w.newWorkspace = options.Number, options.NewWorkspace
	win := mygo.NewWindow(mygo.WindowOptions{Title: fmt.Sprintf("Aster · Workspace %d", options.Number), Width: 1440, Height: 900, MinWidth: 1100, MinHeight: 700, StateKey: fmt.Sprintf("workspace-%d", options.Number), Content: ui.View(w.View)})
	w.attach(parent, func(fn func()) { win.Update(fn) })
	win.OnClosed(func() {
		w.cancel()
		if options.Closed != nil {
			options.Closed()
		}
	})
	if os.Getenv("ASTER_NATIVE_SMOKE") == "1" {
		w.notice = "Smoke fixture · no kubeconfig is read"
		w.rows = []resourceRow{{UID: "fixture", Name: "aster-native-smoke", Namespace: "fixture", Status: "Rendered"}}
		w.total = 1
		w.status = "Smoke fixture"
		if err := w.prepareTerminalSmoke(); err != nil {
			fmt.Fprintln(os.Stderr, "ASTER_NATIVE_TERMINAL_ERROR:", err)
			mygo.App.Quit()
			return w
		}
		terminalReported := false
		w.run(func(ctx context.Context) {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					w.emit(func() {
						if w.frames > 0 {
							if !terminalReported && w.terminal != nil {
								cols, rows := w.terminal.Size()
								if (cols != 80 || rows != 24) && strings.Contains(w.terminal.Text(), "ASTER NATIVE TERMINAL") {
									fmt.Printf("ASTER_NATIVE_TERMINAL_RENDERED:%d\n", options.Number)
									terminalReported = true
								}
							}
							if terminalReported && options.SmokeFrame != nil {
								options.SmokeFrame(w.frames, func() { win.Close() })
							}
						}
					})
				}
			}
		})
	} else {
		w.loadContexts()
	}
	return w
}
func (w *Workbench) attach(parent context.Context, dispatch func(func())) {
	w.ctx, w.cancel = context.WithCancel(parent)
	w.life = cluster.NewSession(w.ctx, "desktop")
	w.dispatch = dispatch
}
func (w *Workbench) run(fn func(context.Context)) {
	if w.life != nil {
		_ = w.life.Go(fn)
	}
}
func (w *Workbench) emit(fn func()) {
	if w.dispatch == nil || w.ctx.Err() != nil {
		return
	}
	w.dispatch(func() {
		if w.ctx.Err() == nil {
			fn()
		}
	})
}

// Close is called after the event loop, or by the owning UI test goroutine.
func (w *Workbench) Close() {
	w.clearOIDC()
	w.clearVault()
	if w.cancel != nil {
		w.cancel()
	}
	if w.life != nil {
		w.life.Close()
	}
}
func parseReplicaCount(text string) (int64, error) { return strconv.ParseInt(text, 10, 64) }
