package uiworkbench

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/schemaassist"
)

type schemaLocation struct {
	label    string
	position manifest.SourceLocation
}

type schemaJump struct {
	position                             manifest.SourceLocation
	schema, detail, revision, connection uint64
	pointer                              string
}

// Locate only in the original draft, not its re-encoded representation. A
// missing field is anchored at its explicit parent and labeled as such.
func schemaLocations(ctx context.Context, draft string, diagnostics []schemaassist.Diagnostic) (*manifest.SourceIndex, []schemaLocation, string) {
	source, err := manifest.IndexSource(ctx, []byte(draft))
	if err != nil {
		return nil, nil, "\nSource navigation unavailable; no source position was guessed."
	}
	locations := make([]schemaLocation, 0, min(len(diagnostics), schemaassist.MaxDiagnostics))
	for i, d := range diagnostics {
		if i >= schemaassist.MaxDiagnostics {
			break
		}
		position, ok := source.Locate(d.Pointer)
		if !ok {
			continue
		}
		kind := d.Code
		if !position.Exact {
			kind += " (enclosing value)"
		}
		label := fmt.Sprintf("%d · L%d:C%d · %s", i+1, position.Line, position.Column, kind)
		locations = append(locations, schemaLocation{label: label, position: position})
	}
	note := "\nLocations use one-based Unicode line/column coordinates; missing fields use an enclosing explicit value."
	if source.Partial() {
		note += " Some YAML branches have no exact source mapping (aliases/merges or coerced keys)."
	}
	return source, locations, note
}

func (w *Workbench) schemaLocationsView(c *ui.Context) {
	if len(w.schemaLocations) == 0 {
		return
	}
	ui.Row(c).Gap(6).Children(func() {
		epoch, choice := w.schemaEpoch, w.schemaLocationChoice
		// A platform menu can scroll a bounded long result set without creating
		// an unbounded-height inline popup in the minimum-size editor window.
		ui.MenuButton(c.Key("field.schemaLocation"), choice, func(menu *ui.Menu) {
			for _, label := range w.schemaLocationChoices {
				if menu.Item(label).Checked(label == choice).Chosen() && epoch == w.schemaEpoch && w.schemaSource.Matches(w.editor) {
					w.schemaLocationChoice = label
				}
			}
		}).Label("Schema location").Grow(1)
		ui.Button(c, "Go to location").Disabled(w.schemaActive).OnClick(func() { w.queueSchemaJump(epoch, choice) })
	})
}

func (w *Workbench) queueSchemaJump(epoch uint64, choice string) {
	if epoch != w.schemaEpoch || w.schemaActive || w.detail == nil || w.detailMode != "Edit" || w.detail.GetKind() == "Secret" || w.operationContext().Err() != nil || !w.schemaSource.Matches(w.editor) {
		return
	}
	for _, target := range w.schemaLocations {
		if target.label == choice {
			w.find.jump = nil
			w.schemaJump = &schemaJump{position: target.position, schema: epoch, detail: w.detailEpoch, revision: w.draftRevision, connection: w.contextEpoch, pointer: w.schemaPointer}
			return
		}
	}
}

// The Element is used only within this build. Persist a plain navigation request,
// never a MyGo build-scoped Element or Context. Typing is processed first, so a
// queued navigation cannot act on a newly edited document in the same frame.
func (w *Workbench) manifestEditorView(c *ui.Context) {
	editor := ui.TextArea(c.Key("field.editor"), &w.editor).Label("Manifest editor").Grow(1)
	if editor.Changed() {
		w.clearFind(false)
		w.clearSchema()
		w.stopPreview()
		w.draftRevision++
		w.plan = nil
		w.diff, w.confirmation = "", ""
	}
	w.syncFind()
	w.applyFindJump(editor)
	jump := w.schemaJump
	if jump == nil {
		return
	}
	w.schemaJump = nil
	if jump.schema != w.schemaEpoch || jump.detail != w.detailEpoch || jump.revision != w.draftRevision || jump.connection != w.contextEpoch || jump.pointer != w.schemaPointer || w.operationContext().Err() != nil || !w.schemaSource.Matches(w.editor) {
		return
	}
	if editor.Composing() {
		w.schemaStatus = "Finish text composition before navigating; draft unchanged."
		return
	}
	editor.SetTextSelection(jump.position.RuneOffset, jump.position.RuneOffset).Focus()
	kind := "field start"
	if !jump.position.Exact {
		kind = "enclosing explicit value, not the absent field"
	}
	w.schemaStatus = fmt.Sprintf("L%d:C%d · %s · hints only", jump.position.Line, jump.position.Column, kind)
}

func (w *Workbench) setSchemaLocations(source *manifest.SourceIndex, locations []schemaLocation) {
	w.schemaSource, w.schemaLocations = source, locations
	w.schemaLocationChoices = make([]string, 0, len(locations))
	for _, location := range locations {
		w.schemaLocationChoices = append(w.schemaLocationChoices, location.label)
	}
	w.schemaLocationChoice = ""
	if len(locations) > 0 {
		w.schemaLocationChoice = locations[0].label
	}
}

// Keep the stable diagnostic pointer/code prefix used by existing consumers.
// Values and draft excerpts are intentionally not copied into this display.
func sourceReportText(report schemaassist.Report, locations []schemaLocation) string {
	text := report.Text()
	if len(locations) == 0 {
		return text
	}
	var lines strings.Builder
	lines.WriteString(text)
	lines.WriteString("\nSource locations:\n")
	for _, location := range locations {
		lines.WriteString(location.label)
		lines.WriteByte('\n')
	}
	return lines.String()
}

// Reserve a truncation marker and never cut a UTF-8 sequence. Output remains
// bounded even when both the original report and location help reach budgets.
func boundedSchemaOutput(text string) string {
	const limit = 64 << 10
	const suffix = "\n[Display truncated; server preview is still required.]"
	if len(text) <= limit {
		return text
	}
	end := limit - len(suffix)
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + suffix
}
