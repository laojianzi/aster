package uiworkbench

import (
	"context"
	"errors"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/manifest"
	"github.com/laojianzi/aster/internal/schemaassist"
)

func (w *Workbench) clearSchema() {
	w.schemaEpoch++
	if w.schemaCancel != nil {
		w.schemaCancel()
		w.schemaCancel = nil
	}
	w.schemaSource, w.schemaJump = nil, nil
	w.schemaLocations, w.schemaLocationChoices = nil, nil
	w.schemaLocationChoice = ""
	w.schemaActive = false
	w.schemaText, w.schemaStatus = "", ""
}
func (w *Workbench) startSchema(check bool) {
	if w.backend == nil || w.detail == nil || w.detailMode != "Edit" || w.detailKind.Kind == "Secret" {
		return
	}
	w.clearSchema()
	parent := w.operationContext()
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	w.schemaCancel, w.schemaActive = cancel, true
	w.schemaStatus = "Reading schema with the current identity…"
	epoch, detail, revision, connection := w.schemaEpoch, w.detailEpoch, w.draftRevision, w.contextEpoch
	backend, gvk, path, draft := w.backend, w.detailKind.GVR.GroupVersion().WithKind(w.detailKind.Kind), w.schemaPointer, w.editor
	w.run(func(context.Context) {
		defer cancel()
		output := ""
		var err error
		var source *manifest.SourceIndex
		var locations []schemaLocation
		var objectData map[string]any
		if check {
			obj, e := manifest.Decode([]byte(draft))
			if e != nil || obj == nil || obj.GroupVersionKind() != gvk {
				err = errors.New("draft is invalid or does not match the selected GVK")
			} else {
				objectData = obj.Object
			}
		}
		if err == nil {
			document, e := backend.SchemaDocument(ctx, gvk)
			err = e
			if err == nil {
				if check {
					report, e := document.Check(ctx, objectData)
					err = e
					if err == nil {
						var note string
						source, locations, note = schemaLocations(ctx, draft, report.Diagnostics)
						output = sourceReportText(report, locations) + note
					}
				} else {
					help, e := document.Help(ctx, path)
					err = e
					if err == nil {
						var note string
						source, locations, note = schemaLocations(ctx, draft, []schemaassist.Diagnostic{{Pointer: path, Code: "field"}})
						output = help.Text() + note
					}
				}
			}
		}
		requestErr := ctx.Err()
		if err == nil && requestErr != nil {
			err = requestErr
		}
		w.emit(func() {
			if epoch != w.schemaEpoch || detail != w.detailEpoch || revision != w.draftRevision || connection != w.contextEpoch || w.detailMode != "Edit" || parent.Err() != nil || w.editor != draft || w.schemaPointer != path {
				return
			}
			w.schemaCancel, w.schemaActive = nil, false
			if err != nil {
				w.schemaText = ""
				if requestErr != nil {
					w.schemaStatus = "Schema request canceled or timed out."
				} else {
					w.schemaStatus = err.Error() + ". Draft unchanged; server preview is still required."
				}
				return
			}
			w.setSchemaLocations(source, locations)
			w.schemaText = boundedSchemaOutput(output)
			w.schemaStatus = "Read-only hints · not server validation"
		})
	})
}
func (w *Workbench) schemaView(c *ui.Context) {
	if ui.TextInput(c.Key("field.schemaPointer"), &w.schemaPointer).Label("Schema field pointer").Placeholder("/spec/containers/0/image (empty for root)").Changed() {
		w.clearSchema()
	}
	ui.Row(c).Gap(6).Children(func() {
		ui.Button(c, "Field help").Disabled(w.schemaActive).OnClick(func() {
			w.startSchema(false)
		})
		ui.Button(c, "Check draft").Disabled(w.schemaActive).OnClick(func() {
			w.startSchema(true)
		})
		ui.Button(c, "Clear schema").Disabled(!w.schemaActive && w.schemaText == "" && w.schemaStatus == "").OnClick(func() {
			w.clearSchema()
		})
	})
	w.schemaLocationsView(c)
	if w.schemaStatus != "" {
		ui.Text(c, w.schemaStatus).FontSize(11).SingleLine()
	}
	if w.schemaText != "" {
		ui.TextArea(c.Key("field.schemaText"), &w.schemaText).Label("Schema assistance").ReadOnly(true).Height(145)
	}
}
