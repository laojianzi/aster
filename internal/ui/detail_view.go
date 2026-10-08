package uiworkbench

import (
 "fmt"
 "github.com/egoist/mygo/ui"
)

func (w *Workbench) detailView(c *ui.Context) {
 t := c.Theme()
 ui.Column(c).Width(510).Padding(12).Gap(8).Children(func() {
  ui.Row(c).Gap(8).Children(func() {
   title := w.detail.GetName()
   if w.creating { title = "New " + w.detailKind.Kind }
   ui.Text(c, title).Bold().FontSize(18).Grow(1).SingleLine()
   if ui.Button(c, "Close detail").Clicked() { w.clearDetail() }
  })
  if w.detail == nil { return }
  ui.Text(c, w.activeContext+" / "+w.detail.GetNamespace()+" / "+w.detailKind.Kind).FontSize(11).TextColor(t.TextMuted)
  ui.Row(c).Gap(6).Children(func() {
   if ui.Button(c, "YAML").Clicked() { w.detailMode = "YAML"; w.stopLogs() }
   if ui.Button(c, "Edit").Disabled(w.detail.GetKind() == "Secret").Clicked() { w.detailMode = "Edit"; w.stopLogs() }
   if ui.Button(c, "Health").Disabled(w.creating).Clicked() {
    if w.healthActive { w.detailMode = "Health"; w.stopLogs() } else { w.loadHealth() }
   }
   if ui.Button(c, "Events").Disabled(w.creating).Clicked() { w.loadEvents() }
  })
  ui.Row(c).Gap(6).Children(func() {
   if ui.Button(c, "Logs").Disabled(w.creating || w.detailKind.GVR.Group != "" || w.detailKind.GVR.Resource != "pods").Clicked() { w.detailMode = "Logs" }
   if ui.Button(c, "Command").Disabled(w.creating || w.detailKind.GVR.Group != "" || w.detailKind.GVR.Resource != "pods").Clicked() {
    w.detailMode = "Command"; w.stopLogs()
    if w.commandArgv == "" { w.commandArgv = `["id"]` }
   }
   if ui.Button(c, "Port forward").Disabled(w.creating || w.detailKind.GVR.Group != "" || w.detailKind.GVR.Resource != "pods").Clicked() { w.detailMode = "Forward"; w.stopLogs() }
   if ui.Button(c, "Refresh detail").Disabled(w.creating).Clicked() { w.openResource(project(w.detail)) }
  })
  if w.detailMessage != "" { ui.Text(c, w.detailMessage).FontSize(12).TextColor(t.TextMuted) }
  switch w.detailMode {
  case "Edit":
   if ui.TextArea(c, &w.editor).Label("Manifest editor").Grow(1).Changed() { w.draftRevision++; w.plan = nil; w.diff = ""; w.confirmation = "" }
   if w.creating {
    if ui.PrimaryButton(c, "Preview create").Disabled(w.preparing).Clicked() { w.prepare("create") }
   } else {
    if ui.PrimaryButton(c, "Preview edit").Disabled(w.preparing).Clicked() { w.prepare("edit") }
   }
  case "Diff":
   ui.Text(c, "Server dry-run preview · live state is rechecked at execution").FontSize(12)
   ui.TextArea(c, &w.diff).Label("Change diff").ReadOnly(true).Grow(1)
   ui.TextInput(c, &w.confirmation).Label("Confirm resource name").Placeholder("Type the resource name to confirm")
   if ui.PrimaryButton(c, "Execute reviewed change").Disabled(w.plan == nil || w.confirmation != w.confirmationName() || w.pendingWrites > 0).Clicked() { w.execute() }
  case "Health": w.healthView(c)
  case "Command": w.commandView(c)
  case "Forward": w.forwardView(c)
  case "Events": ui.TextArea(c, &w.eventsText).Label("Resource events").ReadOnly(true).Grow(1)
  case "Logs":
   if ui.Select(c, &w.container, w.containers).Label("Container").Changed() { w.stopLogs(); w.logRows = nil; w.logDropped = 0; w.logStatus = "Select a log action for this container" }
   ui.Row(c).Gap(6).Children(func() {
    if ui.Button(c, "Follow logs").Clicked() { w.startLogs(false, true) }
    if ui.Button(c, "Previous logs").Clicked() { w.startLogs(true, false) }
    if ui.Button(c, "Stop logs").Clicked() { w.stopLogs(); w.logStatus = "Stopped" }
   })
   ui.List(c, &w.logList, len(w.logRows), func(i int) { ui.Text(c, w.logRows[i]).FontSize(11).SingleLine() }).Grow(1)
   ui.Text(c, fmt.Sprintf("%s · %d truncated line(s)", w.logStatus, w.logDropped)).FontSize(11)
  default: ui.TextArea(c, &w.detailText).Label("Resource document").ReadOnly(true).Grow(1)
  }
  if w.detail != nil && !w.creating && w.detailMode != "Diff" {
   ui.Row(c).Gap(6).Children(func() {
    ui.TextInput(c, &w.replicas).Label("Replica count").Width(70)
    if ui.Button(c, "Preview scale").Disabled(w.preparing).Clicked() { w.prepare("scale") }
    if ui.Button(c, "Preview restart").Disabled(w.preparing).Clicked() { w.prepare("restart") }
    if ui.Button(c, "Preview delete").Disabled(w.preparing).Clicked() { w.prepare("delete") }
   })
  }
 })
}
