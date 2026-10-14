package uiworkbench

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// Clipboard limits deliberately overestimate insertion: do not subtract the
// selection or reconstruct native composition. Checking total bytes first
// avoids scanning a giant clipboard payload; all subsequent scans are bounded.
// Empty clipboard text remains a native no-op, even for an oversized draft.
func clipboardPasteExceedsBudget(snapshot ui.TextSnapshot, text string, maxBytes, maxNewlines, maxLine int) bool {
	if text == "" {
		return false
	}
	if snapshot.ByteLen() > maxBytes || len(text) > maxBytes-snapshot.ByteLen() {
		return true
	}
	if snapshot.LineCount()-1+strings.Count(text, "\n") > maxNewlines {
		return true
	}
	// Flatten only this explicit, size-bounded clipboard consumer, not idle
	// renders. The sum also bounds a line formed by joining the two inputs.
	return longestClipboardLine(snapshot.String())+longestClipboardLine(text) > maxLine
}

func longestClipboardLine(text string) int {
	longest, start := 0, 0
	for i := range len(text) {
		if text[i] == '\n' {
			longest = max(longest, i-start)
			start = i + 1
		}
	}
	return max(longest, len(text)-start)
}
