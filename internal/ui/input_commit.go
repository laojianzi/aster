package uiworkbench

import "github.com/egoist/mygo/ui"

// MyGo 0.5 applies queued input according to the last displayed frame. Small
// transaction/consent fields therefore bind a candidate and commit it only
// while the CURRENT model is editable. This does not retain Context/Element,
// synthesize keys, or introduce a second Undo stack. Buffer-backed documents
// deliberately keep their existing revision/stale-plan contract instead.
// Callers must also guard Changed side effects with the same condition.
func guardedTextInput(c *ui.Context, key any, target *string, editable bool) ui.Element {
	candidate := *target
	e := ui.TextInput(c.Key(key), &candidate).Disabled(!editable)
	if editable {
		*target = candidate
	}
	return e
}

func guardedTextArea(c *ui.Context, key any, target *string, editable bool) ui.Element {
	candidate := *target
	e := ui.TextArea(c.Key(key), &candidate).ReadOnly(!editable)
	if editable {
		*target = candidate
	}
	return e
}

func guardedSelect(c *ui.Context, key any, target *string, options []string, editable bool) ui.Element {
	candidate := *target
	e := ui.Select(c.Key(key), &candidate, options).Disabled(!editable)
	if editable {
		*target = candidate
	}
	return e
}

func guardedCheckbox(c *ui.Context, key any, target *bool, label string, editable bool) ui.Element {
	candidate := *target
	e := ui.Checkbox(c.Key(key), &candidate, label).Disabled(!editable)
	if editable {
		*target = candidate
	}
	return e
}
