package uiworkbench

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/helmjournal"
	"github.com/laojianzi/aster/internal/helmrelease"
	"github.com/laojianzi/aster/internal/helmrender"
)

const (
	helmInstallTrustLabel          = "I trust the local chart and values"
	helmInstallCapabilityLabel     = "I accept the fixed Kubernetes capability assumption"
	helmInstallPrepareConsentLabel = "Allow current-identity preparation GETs"
	helmInstallDryRunConsentLabel  = "Allow sensitive server dry-run requests"
	helmInstallExecuteConsentLabel = "Allow persistent creation and private recovery records"
)

func helmInstallControlKey(part string, epoch uint64) string {
	return fmt.Sprintf("helm.install.%s.%d", part, epoch)
}

// The review remains in one scroll viewport. Stop, Close and the current status
// stay outside it; neither a large target inventory nor scaled text can move
// those lifecycle controls below the scrollable review.
func (w *Workbench) helmInstallView(c *ui.Context) {
	s := &w.helmInstall
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Padding(12).Gap(8).Children(func() {
		ui.Text(c, "Restricted Helm install").Label("Helm install title").Bold().FontSize(c.Theme().Rem(1.25))
		ui.Scroll(c.Key(helmInstallControlKey("flow", s.paneEpoch))).Label("Helm install inventory").Grow(1).Basis(0).MinHeight(0).Gap(8).Children(func() {
			w.helmInstallReviewView(c)
		})
		ui.Column(c).Gap(5).Shrink(0).Children(func() {
			ui.Text(c, s.status).Label("Helm install status")
			ui.Row(c).Gap(8).Wrap().Children(func() {
				ui.Button(c.Key(helmInstallControlKey("stop", s.epoch)), "Stop Helm install").Disabled(s.stopRequested || s.busy == "" && (s.flowCtx == nil || s.attempted)).OnClick(w.stopHelmInstall)
				ui.Button(c.Key(helmInstallControlKey("close", s.epoch)), "Close install review").OnClick(w.clearHelmInstall)
			})
		})
	})
}

func (w *Workbench) helmInstallReviewView(c *ui.Context) {
	s := &w.helmInstall
	editable := w.helmInstallEligible() && !s.attempted
	ui.Text(c, fmt.Sprintf("Context: %s · Namespace: %s", s.contextName, s.namespace)).Label("Helm install scope").Bold()
	ui.Text(c, "First revision only: one new release and 1–32 plain ConfigMaps in this namespace. Existing releases and objects are never adopted.").Label("Helm install profile")
	ui.Text(c, "The supported input range does not guarantee that every candidate fits the current execution budgets. Insufficient capacity stops before persistent requests.").Label("Helm install budget boundary")
	ui.Text(c, "Private recovery directory: "+s.journalPath).Label("Helm install journal directory")
	if s.journalPath == "" {
		ui.Text(c, "The private recovery directory is unavailable; installation cannot execute.").Label("Helm install journal unavailable")
	}
	ui.Text(c, "Operation identities and uncertain effects are retained across restarts. Credentials, chart content, values and request bodies are not written to this history.").Label("Helm install journal boundary")

	ui.Text(c, "1. Capture trusted local inputs").Bold()
	ui.Text(c, "Approved tool: "+helmrender.ApprovedArchive()+". Templates are not sandboxed. Select only a chart and values you trust.").Label("Helm install source boundary")
	changed, composing := false, false
	field := func(part string, value *string, label, placeholder string) {
		ui.Column(c).Grow(1).MinWidth(230).Gap(3).Children(func() {
			ui.Text(c, label).Label(label + " label")
			e := guardedTextInput(c, helmInstallControlKey(part, s.paneEpoch), value, editable).Label(label).Placeholder(placeholder)
			changed = changed || editable && e.Changed()
			composing = composing || e.Composing()
		})
	}
	ui.Row(c).Gap(12).Wrap().Children(func() {
		field("archive", &s.archive, "Helm install distribution archive", "Absolute path to the approved Helm archive")
		field("chart", &s.chart, "Helm install chart archive", "Absolute path to a local chart .tgz")
	})
	ui.Row(c).Gap(12).Wrap().Children(func() {
		field("values", &s.values, "Helm install values file", "Optional absolute values-file path")
		field("release", &s.release, "Helm install release name", "New release name")
	})
	s.composing = composing
	if changed {
		w.helmInstallInputsChanged()
		c.Invalidate()
	}
	trustChanged := guardedCheckbox(c, helmInstallControlKey("trust", s.epoch), &s.trusted, helmInstallTrustLabel, editable).Label("Helm install source trust").Changed()
	ui.Text(c, "The pinned client's client-only capture assumes Kubernetes "+helmrelease.InstallCapabilityAssumption+". This is not the connected server version or a compatibility guarantee; server dry-run remains separate.").Label("Helm install capability assumption")
	capabilityChanged := guardedCheckbox(c, helmInstallControlKey("capability", s.epoch), &s.capabilityAck, helmInstallCapabilityLabel, editable).Label("Helm install capability consent").Changed()
	if editable && (trustChanged || capabilityChanged) {
		w.helmInstallTrustChanged()
		c.Invalidate()
	}
	ui.Button(c.Key(helmInstallControlKey("capture", s.epoch)), "Capture install candidate").Disabled(!editable || !s.trusted || !s.capabilityAck || composing).OnClick(w.captureHelmInstall)
	ui.Text(c, "Capture reads the selected files once. Later preparation and execution use that frozen content. Changing an input selection or capturing again discards the previous review.").Label("Helm install frozen input boundary")

	if candidate := s.candidate; candidate != nil {
		ui.Text(c, "Capture ID: "+candidate.ID()).Label("Helm install candidate")
		ui.Text(c, fmt.Sprintf("Captured release: %s/%s · revision 1 · storage %s", candidate.Namespace(), candidate.Release(), candidate.StorageName())).Label("Helm install captured release")
		summary := candidate.Summary()
		ui.Text(c, fmt.Sprintf("Chart: %s %s · %d ConfigMap(s) · %d supplied value key(s)", summary.ChartName, summary.ChartVersion, len(candidate.Targets()), summary.SuppliedValues)).Label("Helm install capture summary")
		ui.Text(c, "Original capture expires: "+candidate.ExpiresAt().UTC().Format("15:04:05 UTC")).Label("Helm install capture expiry")
		if remaining := time.Until(candidate.ExpiresAt()); remaining > 0 {
			c.After(remaining)
		}
		for i, target := range candidate.Targets() {
			ui.Text(c, fmt.Sprintf("%d. ConfigMap %s/%s", i+1, target.Namespace, target.Name)).Label(fmt.Sprintf("Helm install captured target %d", i+1)).Shrink(0)
		}
		if s.preparation == nil && !s.attempted {
			ui.Text(c, "2. Prepare with current-identity reads").Bold()
			ui.Text(c, "Read current API routes, namespace identity, complete Helm metadata history and exact target names. Preparation grants no server dry-run or persistent write permission.").Label("Helm install preparation boundary")
			guardedCheckbox(c, helmInstallControlKey("prepare.ack", s.epoch), &s.prepareAck, helmInstallPrepareConsentLabel, editable && !composing).Label("Helm install preparation consent")
			ui.Button(c.Key(helmInstallControlKey("prepare", s.epoch)), "Prepare Helm install").Disabled(!editable || composing || !s.prepareAck || !candidate.Valid(time.Now())).OnClick(w.prepareHelmInstall)
		}
	}
	if preparation := s.preparation; preparation != nil {
		ui.Text(c, "Preparation ID: "+preparation.ID()).Label("Helm install preparation identity")
		uid, rv := preparation.NamespaceIdentity()
		ui.Text(c, "Reviewed namespace UID: "+uid+" · RV: "+rv).Label("Helm install namespace identity")
		ui.Text(c, "Original review expires: "+preparation.ExpiresAt().UTC().Format("15:04:05 UTC")).Label("Helm install review expiry")
		if remaining := time.Until(preparation.ExpiresAt()); remaining > 0 {
			c.After(remaining)
		}
		if (s.dryRun == nil || !s.dryRun.Complete()) && !s.attempted {
			ui.Text(c, "3. Independently authorize server dry-run").Bold()
			ui.Text(c, "Send the frozen Secret and ConfigMap bodies through strict server dry-run POSTs, bracketed by fresh checks. These bodies can contain sensitive values. No resource is persisted by this stage.").Label("Helm install dry-run boundary")
			guardedCheckbox(c, helmInstallControlKey("dryrun.ack", s.epoch), &s.dryRunAck, helmInstallDryRunConsentLabel, editable && !composing).Label("Helm install dry-run consent")
			ui.Button(c.Key(helmInstallControlKey("dryrun", s.epoch)), "Run install dry-run").Disabled(!editable || composing || !s.dryRunAck || !preparation.Matches(s.backend, s.candidate, s.session, time.Now())).OnClick(w.preflightHelmInstall)
		}
	}
	if report := s.dryRun; report != nil && !s.attempted {
		ui.Text(c, fmt.Sprintf("Server dry-run complete: %t", report.Complete())).Label("Helm install dry-run result")
		for i, step := range report.Steps() {
			ui.Text(c, fmt.Sprintf("%d. %s %s · response verified: %t", i+1, step.Resource, step.Name, step.Verified)).Label(fmt.Sprintf("Helm install dry-run step %d", i+1)).Shrink(0)
		}
		if report.Complete() {
			ui.Text(c, "4. Confirm one persistent execution").Bold()
			ui.Text(c, "Create the reviewed pending release and ConfigMaps, verify each accepted effect, then conditionally mark the release deployed. The shared private journal must admit the operation before any persistent request.").Label("Helm install execution boundary")
			ui.Text(c, "Execution can stop with Partial, Unknown or AcceptedPending effects. Cancellation does not undo an accepted request. There is no automatic retry, rollback, replay or resumed execution.").Label("Helm install consequences")
			allowed := editable && !composing && report.Matches(s.backend, s.candidate, s.session, time.Now())
			guardedCheckbox(c, helmInstallControlKey("execute.ack", s.epoch), &s.executeAck, helmInstallExecuteConsentLabel, allowed).Label("Helm install execution consent")
			ui.Text(c, "Type the exact captured release name to confirm this execution.").Label("Helm install release confirmation label")
			s.confirmationComposing = guardedTextInput(c, helmInstallControlKey("execute.confirm", s.epoch), &s.confirmation, allowed).Label("Helm install release confirmation").Placeholder("Exact release name: " + s.candidate.Release()).Composing()
			ui.Button(c.Key(helmInstallControlKey("execute", s.epoch)), "Execute Helm install").Disabled(!allowed || s.journalPath == "" || !s.executeAck || s.confirmationComposing || s.confirmation != s.candidate.Release()).OnClick(w.executeHelmInstall)
		}
	}
	if result := s.execution; result != nil {
		ui.Text(c, "Server outcome: "+result.Outcome()).Label("Helm install outcome").Bold()
		ui.Text(c, fmt.Sprintf("Journal terminal saved: %t · record %s", result.JournalComplete(), result.JournalID())).Label("Helm install journal outcome")
		ui.Text(c, "Server effects and local journal completion are separate. A saved terminal record can still contain uncertain effects; current observations cannot clear them or resume this attempt.").Label("Helm install result boundary")
		helmInstallEffectsView(c, result.Effects())
	} else if s.attempted && s.busy == "" {
		ui.Text(c, "Server outcome: "+helmjournal.NotStarted).Label("Helm install outcome").Bold()
		ui.Text(c, "Journal terminal saved: false · no operation record ID returned").Label("Helm install journal outcome")
		ui.Text(c, "The execution worker has finished. It returned no server-effect report or operation record ID; review the status below for the admission or cancellation reason.").Label("Helm install result boundary")
	}
	if s.attempted {
		ui.Text(c, "This execution attempt is consumed. A new review requires closing this pane and capturing again; unresolved journal records still block another execution.").Label("Helm install attempt consumed")
		ui.Button(c.Key(helmInstallControlKey("recovery", s.epoch)), "Review install recovery").Disabled(!w.helmInstallEligible()).OnClick(w.openHelmInstallHistory)
		if s.execution != nil && s.execution.JournalID() != "" {
			ui.Text(c, "Review install recovery reads this saved local record. Any current cluster observation requires another explicit metadata-read confirmation.").Label("Helm install recovery boundary")
		} else {
			ui.Text(c, "No record ID was returned for this attempt. Review install recovery reads local history to inspect blockers; it cannot clear unresolved records or authorize writes.").Label("Helm install recovery boundary")
		}
	}
}

func helmInstallEffectsView(c *ui.Context, effects []helmjournal.Effect) {
	for i, effect := range effects {
		ui.Column(c.Key(fmt.Sprintf("helm.install.effect.%d", i))).Gap(3).Padding(4).Shrink(0).Children(func() {
			ui.Text(c, fmt.Sprintf("%d. %s %s/%s · %s", i+1, effect.Kind, effect.Namespace, effect.Name, effect.Outcome)).Label(fmt.Sprintf("Helm install effect %d", i+1))
			if effect.UID == "" {
				ui.Text(c, "No accepted UID was recorded for this effect; a same-name object cannot be attributed to this attempt.").Label(fmt.Sprintf("Helm install unassigned effect %d", i+1))
			} else {
				ui.Text(c, "Accepted UID: "+effect.UID+" · request RV: "+effect.ResourceVersion+" · observed RV: "+effect.ObservedResourceVersion).Label(fmt.Sprintf("Helm install effect identity %d", i+1))
			}
		})
	}
}
