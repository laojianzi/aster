# MyGo 0.4.8 native editor: source review

Reviewed 2026-10-13 against Aster main
`9f7010fdfe23513985c22fc1a8feb8e9ed24f6f7`, which still pins MyGo v0.4.5.
The [official v0.4.8 release](https://github.com/egoist/mygo/releases/tag/v0.4.8)
was published on 2026-10-12 at 21:54:51 UTC. Its tag and the observed upstream
main resolve to `c0e1068027faf8b14777c9cb925c937ef34ac2bb`.
This is an independent assistant's static source review, not a dependency
upgrade, executed test result or migration acceptance.

## A supported transaction path now exists

The new official `ui/editor` package provides a persistent `Buffer` with
`Edit`, `Transact`, `Undo` and `Redo`. `State` exposes selection and insertion
operations. Built-in completion acceptance uses the same editor transaction
and stores the originating editor's selections before and after the edit;
that editor's Undo/Redo restores them. This is material progress beyond
v0.4.5, not merely a layout-cache change.

Primary sources: [buffer transactions](https://github.com/egoist/mygo/blob/c0e1068027faf8b14777c9cb925c937ef34ac2bb/ui/editor/buffer.go#L254-L329),
[selection/history handling](https://github.com/egoist/mygo/blob/c0e1068027faf8b14777c9cb925c937ef34ac2bb/ui/editor/edit.go#L15-L76),
[native completion acceptance](https://github.com/egoist/mygo/blob/c0e1068027faf8b14777c9cb925c937ef34ac2bb/ui/editor/lsp.go#L267-L301).
`Transact` groups history but does not hold an exclusive lock across its callback;
Aster would still serialize document writes on the UI owner.

Simply upgrading the dependency leaves the existing `ui.TextAreaBuffer` problem
intact: `ui/textbuffer_input.go` has the same blob in v0.4.5 and v0.4.8,
`35bad0a598bf12edcd948537f3a99191b4b966d3`.
An external snapshot change still calls `loadBuffer`, which clears Undo, Redo,
queued input and composition and resets selection state.
See [the unchanged synchronization path](https://github.com/egoist/mygo/blob/c0e1068027faf8b14777c9cb925c937ef34ac2bb/ui/textbuffer_input.go#L35-L60).

## Direct migration changes existing source bytes

Aster deliberately distinguishes loaded text from native input. Native insertion
already normalizes CRLF to LF; this is documented in [Manifest Find](manifest-find.md)
and required by `TestNativeFindTextInputNormalizesCRLFBeforeSearch`.
Programmatic loading and captured snapshots preserve their original bytes.
`TestManifestBufferImmutableConcurrentSnapshotsAndExportBounds` loads
`"# 中文 🚢\r\n" + schemaDraft`, where `schemaDraft` contains LF, and requires exact
export. `TestNativeSchemaSourceCoordinatesAcrossJSONAndCRLF` also explicitly
requires a programmatically loaded CRLF document to retain its source coordinates.
See [the mixed-ending snapshot test](../internal/ui/manifest_buffer_test.go),
[the native source-coordinate test](../internal/ui/schema_locations_test.go)
and [the current buffer owner/export](../internal/ui/manifest_buffer.go).

The following is a **source-derived counterexample, not an executed test**:

| Operation | Result for input `"a\r\nb\n"` |
| --- | --- |
| `editor.NewBuffer(input).String()` | `"a\nb\n"` |
| `WriteTo` with its initial line-ending setting | `"a\r\nb\r\n"` |
| `SetLineEnding("\n")`, then `WriteTo` | `"a\nb\n"` |

`NewBuffer` removes each CRLF pair's CR and remembers only whether any CRLF was
present. Export then expands every LF when that single flag is set.
Inputs `"a\r\nb\n"` and `"a\nb\r\n"` therefore become the same stored text and
line-ending flag: an export-only adapter cannot recover which separator was CRLF.
`Reset` applies the same normalization; `Set` and `Edit` normalize their inserted
text as well. See [construction and export](https://github.com/egoist/mygo/blob/c0e1068027faf8b14777c9cb925c937ef34ac2bb/ui/editor/buffer.go#L121-L164)
and [edit normalization](https://github.com/egoist/mygo/blob/c0e1068027faf8b14777c9cb925c937ef34ac2bb/ui/editor/buffer.go#L374-L389).

Inserting CR characters separately with public `Edit` can reconstruct an initial
string, but records an additional undoable edit. The public history-clearing
operation, `Reset`, normalizes it again. A separate newline map would need its own
correct lifecycle through every edit, Undo/Redo and IME operation; it is not a
proven small adapter preserving one native history. No such adapter was established
in this review. Direct replacement of Aster's current buffer is therefore not
qualified under its existing contracts.

## Completion still needs application and IME guards

New editor offsets count UTF-8 bytes, whereas the current text buffer uses rune
offsets. Source navigation, snapshots, byte/line limits and explicit oversized-draft
restoration would all require compatibility checks.
`CompletionRequest` includes a version, but `ShowCompletions(start, items)` does
not check a request version or document identity. Aster must reject stale
buffer/session/revision/selection/request results itself, including after a buffer
switch. See [the request/display path](https://github.com/egoist/mygo/blob/c0e1068027faf8b14777c9cb925c937ef34ac2bb/ui/editor/lsp.go#L140-L188).

IME preedit writes into the new buffer and retains private marked ranges.
Completion request/display/acceptance do not guard `markedActive`, and `State`
does not expose that state. Ordinary composition tests do not prove the combined
sequence of an existing completion list, preedit, accepting a candidate and the
next IME commit. That sequence needs an actual public-input regression before
adoption; this review does not claim to have reproduced a failure.
See [composition handling](https://github.com/egoist/mygo/blob/c0e1068027faf8b14777c9cb925c937ef34ac2bb/ui/editor/input.go#L592-L670)
and [the upstream ordinary composition test](https://github.com/egoist/mygo/blob/c0e1068027faf8b14777c9cb925c937ef34ac2bb/ui/editor/editor_test.go#L325-L335).

Safe completion remains unfinished. The new official API merits a separately
qualified integration, without weakening old byte/history assertions, retaining
private handles, synthesizing production keys or adding a replacement history.
This review changes no dependency and runs no tests or physical IME qualification;
the independent bounded string-set assistance scope can proceed meanwhile.
