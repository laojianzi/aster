package uiworkbench

import (
	"fmt"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (w *Workbench) containerEvidenceView(c *ui.Context) {
	if w.detail.GetAPIVersion() != "v1" || w.detail.GetKind() != "Pod" {
		return
	}
	// Pointers keep absent status counters distinct from reported zeroes.
	type termination struct {
		ExitCode   *int32       `json:"exitCode"`
		Reason     string       `json:"reason"`
		FinishedAt *metav1.Time `json:"finishedAt"`
	}
	type state struct {
		Waiting    *corev1.ContainerStateWaiting `json:"waiting"`
		Running    *corev1.ContainerStateRunning `json:"running"`
		Terminated *termination                  `json:"terminated"`
	}
	type status struct {
		Name         string `json:"name"`
		Ready        *bool  `json:"ready"`
		RestartCount *int32 `json:"restartCount"`
		State        state  `json:"state"`
		LastState    state  `json:"lastState"`
	}
	type container struct {
		Name string `json:"name"`
	}
	var pod struct {
		Spec struct {
			Containers          []container `json:"containers"`
			InitContainers      []container `json:"initContainers"`
			EphemeralContainers []container `json:"ephemeralContainers"`
		} `json:"spec"`
		Status struct {
			Containers          []status `json:"containerStatuses"`
			InitContainers      []status `json:"initContainerStatuses"`
			EphemeralContainers []status `json:"ephemeralContainerStatuses"`
		} `json:"status"`
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(w.detail.Object, &pod); err != nil {
		ui.Text(c, "Container status unavailable").TextColor(c.Theme().Danger)
		return
	}
	type row struct {
		name, kind, current, restarts string
		status                        status
	}
	var rows []row
	for _, group := range []struct {
		kind       string
		containers []container
		statuses   []status
	}{{"init container", pod.Spec.InitContainers, pod.Status.InitContainers}, {"container", pod.Spec.Containers, pod.Status.Containers}, {"ephemeral container", pod.Spec.EphemeralContainers, pod.Status.EphemeralContainers}} {
		for _, container := range group.containers {
			r := row{name: container.Name, kind: group.kind, current: "Unknown", restarts: "Unknown"}
			if index := slices.IndexFunc(group.statuses, func(s status) bool { return s.Name == container.Name }); index >= 0 {
				r.status = group.statuses[index]
			}
			if r.status.RestartCount != nil {
				r.restarts = fmt.Sprint(*r.status.RestartCount)
			}
			switch s := r.status.State; {
			case s.Waiting != nil:
				r.current = "Waiting"
				if s.Waiting.Reason != "" {
					r.current = s.Waiting.Reason
				}
			case s.Running != nil:
				r.current = "Running"
			case s.Terminated != nil:
				r.current = "Terminated"
				if s.Terminated.Reason != "" {
					r.current = s.Terminated.Reason
				}
			}
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		ui.Text(c, "No containers reported").TextColor(c.Theme().TextMuted)
		return
	}
	panel := ui.Column(c.Key("container.evidence")).Label("Container evidence").Gap(8).MinWidth(0)
	selected := ui.Local(panel, "selected", func() string {
		for _, row := range rows {
			if row.status.State.Waiting != nil || (row.status.State.Terminated != nil && row.status.State.Terminated.ExitCode != nil && *row.status.State.Terminated.ExitCode != 0) {
				return row.name
			}
		}
		for _, row := range rows {
			if row.kind == "container" {
				return row.name
			}
		}
		return rows[0].name
	})
	table := ui.Local(panel, "table", func() ui.ListState { return ui.ListState{} })
	panel.Children(func() {
		index := slices.IndexFunc(rows, func(r row) bool { return r.name == *selected })
		current := rows[index]
		ui.Row(c).Gap(8).Wrap().Children(func() {
			ui.Text(c, current.name).Bold().Grow(1).MinWidth(0).Tooltip(current.name)
			ready := "Unknown"
			if current.status.Ready != nil {
				ready = "No"
				if *current.status.Ready {
					ready = "Yes"
				}
			}
			ui.Text(c, "Ready: "+ready).Label("Readiness for " + current.name + ": " + ready).SingleLine()
		})
		ui.Row(c).Gap(8).Wrap().Children(func() {
			for _, action := range []struct {
				label    string
				previous bool
			}{{"Follow logs", false}, {"Previous logs", true}} {
				ui.Button(c, action.label).Label(action.label + " for " + current.name).Disabled(w.backend == nil || (action.previous && current.status.LastState.Terminated == nil)).OnClick(func() {
					w.selectDiagnostic("Logs")
					w.container = current.name
					w.startLogs(action.previous, !action.previous)
				})
			}
		})
		scale := c.Preferences().TextScale
		columns := []ui.TableColumn{{ID: "name", Title: "Container"}, {ID: "state", Title: "Current state", Width: 160 * scale}, {ID: "restarts", Title: "Restarts", Width: 90 * scale}}
		ui.Table(c.Key("container.table"), table, columns, len(rows), func(i, column int) {
			r := rows[i]
			switch column {
			case 0:
				label := r.name
				if r.kind != "container" {
					label += " (" + r.kind + ")"
				}
				ui.Button(c, label).Label("Inspect " + r.kind + " " + r.name).Tooltip(label).Selected(*selected == r.name).Shrink(1).MinWidth(0).OnClick(func() { *selected = r.name })
			case 1:
				ui.Text(c, r.current).SingleLine().Tooltip(r.current)
			case 2:
				ui.Text(c, r.restarts).Label("Restart count for " + r.name + ": " + r.restarts).SingleLine()
			}
		}).Height(c.Theme().Space(8) + 1 + float32(min(len(rows), 4))*(c.Theme().FontSize*1.5+c.Theme().Space(6)+2)).MinWidth(0)
		if current.status.State.Waiting != nil && current.status.State.Waiting.Message != "" {
			ui.Text(c, current.status.State.Waiting.Message).Selectable().TextColor(c.Theme().TextMuted)
		}
		for _, evidence := range []struct {
			label, prefix string
			terminated    *termination
		}{{"Current termination", "Current", current.status.State.Terminated}, {"Last termination", "Previous", current.status.LastState.Terminated}} {
			if evidence.terminated == nil {
				if evidence.prefix == "Previous" {
					ui.Text(c, "No previous termination").TextColor(c.Theme().TextMuted)
				}
				continue
			}
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, evidence.label).Bold()
				code, finished := "Unknown", "Unknown"
				if evidence.terminated.ExitCode != nil {
					code = fmt.Sprint(*evidence.terminated.ExitCode)
				}
				if evidence.terminated.FinishedAt != nil && !evidence.terminated.FinishedAt.IsZero() {
					finished = evidence.terminated.FinishedAt.UTC().Format(time.RFC3339)
				}
				ui.Row(c).Gap(12).Wrap().Children(func() {
					ui.Text(c, "Exit code "+code).Label(evidence.prefix + " exit code: " + code).Selectable()
					if evidence.terminated.Reason != "" {
						ui.Text(c, evidence.terminated.Reason).Selectable()
					}
				})
				ui.Text(c, finished).Label(evidence.prefix + " termination time: " + finished).Selectable().TextColor(c.Theme().TextMuted)
			})
		}
	})
}
