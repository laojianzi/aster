"""The PNG speed change cannot exempt assertions, paths, modes or old hashes."""
import hashlib
import importlib
from pathlib import Path
import subprocess
from types import SimpleNamespace
import unittest
from unittest import mock

from native_screenshot_scope import (ACCEPTED_FAST_SCREENSHOT_SHA256, FAST_ENCODE,
                                     FROZEN_SCREENSHOT_SHA256, OLD_ENCODE, SCREENSHOT_TEST,
                                     UNCOMPRESSED_ENCODE, UNCOMPRESSED_SCREENSHOT_SHA256,
                                     restore_screenshot_best_speed,
                                     restore_screenshot_encoder)

ROOT = Path(__file__).resolve().parents[1]
GUARDS = (
    ('test_helm_install_journal_scope', 'InstallJournalScopeTests',
     'test_old_go_tests_workflows_and_pins_are_byte_preserved'),
    ('test_helm_install_execution_scope', 'InstallExecutionScopeTests',
     'test_exact_250_existing_go_tests_workflows_pins_preserved'),
    ('test_helm_install_recovery_scope', 'InstallRecoveryScopeTests',
     'test_all_256_accepted_go_tests_workflows_and_pins_are_byte_preserved'),
    ('test_native_helm_install_scope', 'NativeHelmInstallScopeTests',
     'test_all_263_existing_go_tests_workflows_and_pins_are_byte_preserved'),
)


class ContentRoot:
    """Immutable byte inputs for calling the actual frozen inventory guards."""
    def __init__(self, contents):
        self.contents = dict(contents)

    def __truediv__(self, path):
        return SimpleNamespace(read_bytes=lambda: self.contents[path])


def index_bytes(entries):
    return b''.join((header + '\t' + path).encode() + b'\0'
                    for header, path in entries)


def run_guard(spec, root, index):
    name, class_name, method = spec
    module = importlib.import_module(name)
    case = getattr(module, class_name)(method)
    with mock.patch.object(module, 'ROOT', root), \
            mock.patch.object(module.subprocess, 'check_output', return_value=index):
        getattr(case, method)()


class NativeScreenshotScopeTests(unittest.TestCase):
    def test_exact_inverse_recovers_every_accepted_helper_byte(self):
        data = (ROOT / SCREENSHOT_TEST).read_bytes()
        # The former NoCompression negative case is now admitted only as this
        # complete reviewed postimage, never as an arbitrary encoder choice.
        self.assertEqual(hashlib.sha256(data).hexdigest(), UNCOMPRESSED_SCREENSHOT_SHA256)
        accepted = restore_screenshot_best_speed(data)
        self.assertEqual(hashlib.sha256(accepted).hexdigest(), ACCEPTED_FAST_SCREENSHOT_SHA256)
        self.assertEqual(accepted.replace(FAST_ENCODE, UNCOMPRESSED_ENCODE, 1), data)
        restored = restore_screenshot_encoder(data)
        self.assertEqual(hashlib.sha256(restored).hexdigest(), FROZEN_SCREENSHOT_SHA256)
        self.assertEqual(restored.count(OLD_ENCODE), 1)
        self.assertNotIn(FAST_ENCODE, restored)
        self.assertNotIn(UNCOMPRESSED_ENCODE, restored)
        self.assertEqual(restored.replace(OLD_ENCODE, FAST_ENCODE, 1), accepted)

    def test_missing_partial_duplicate_or_other_encoder_is_rejected(self):
        data = (ROOT / SCREENSHOT_TEST).read_bytes()
        variants = {
            'original-default': data.replace(UNCOMPRESSED_ENCODE, OLD_ENCODE, 1),
            'missing': data.replace(UNCOMPRESSED_ENCODE, b'', 1),
            'duplicate': data + UNCOMPRESSED_ENCODE,
            'default-compression': data.replace(b'png.NoCompression', b'png.DefaultCompression', 1),
            'best-compression': data.replace(b'png.NoCompression', b'png.BestCompression', 1),
            'previous-best-speed': data.replace(UNCOMPRESSED_ENCODE, FAST_ENCODE, 1),
            'numeric-no-compression': data.replace(b'png.NoCompression', b'-1', 1),
            'old-call-with-declaration': data.replace(b'encoder.Encode(f, tt.Image())',
                                                      b'png.Encode(f, tt.Image())', 1),
            'missing-declaration': data.replace(UNCOMPRESSED_ENCODE.splitlines(keepends=True)[0], b'', 1),
            'moved-outside-helper': data.replace(UNCOMPRESSED_ENCODE, b'', 1) + UNCOMPRESSED_ENCODE,
            'crlf': data.replace(b'\n', b'\r\n'),
        }
        for name, changed in variants.items():
            with self.subTest(change=name):
                self.assertNotEqual(changed, data)
                for restore in (restore_screenshot_best_speed, restore_screenshot_encoder):
                    with self.subTest(inverse=restore.__name__):
                        with self.assertRaises(ValueError):
                            restore(changed)

    def test_other_helper_bytes_and_existing_assertions_are_not_exempt(self):
        data = (ROOT / SCREENSHOT_TEST).read_bytes()
        changes = (
            ('artifact-opt-in', b'if dir == "" {', b'if false {'),
            ('directory-mode', b'os.MkdirAll(dir, 0755)', b'os.MkdirAll(dir, 0777)'),
            ('output-name', b'strings.ReplaceAll(t.Name(), "/", "_")+".png"',
             b'strings.ReplaceAll(t.Name(), "/", "_")+".jpg"'),
            ('image-source', b'encoder.Encode(f, tt.Image())', b'encoder.Encode(f, nil)'),
            ('encode-failure', b'encoder.Encode(f, tt.Image()); err != nil',
             b'encoder.Encode(f, tt.Image()); false'),
            ('close-failure', b'if err = f.Close(); err != nil {\n\t\tt.Fatal(err)',
             b'if err = f.Close(); err != nil {\n\t\tt.Log(err)'),
            ('error-close', b'\t\tf.Close()\n\t\tt.Fatal(err)', b'\t\tt.Fatal(err)'),
            ('native-assertion', b'if !tt.HasText("Aster") {', b'if false {'),
            ('screenshot-call', b'\tsaveNativeScreenshot(t, tt)\n', b''),
        )
        for name, old, new in changes:
            with self.subTest(change=name):
                self.assertIn(old, data)
                changed = data.replace(old, new, 1)
                for restore in (restore_screenshot_best_speed, restore_screenshot_encoder):
                    with self.subTest(inverse=restore.__name__):
                        with self.assertRaises(ValueError):
                            restore(changed)
        for restore in (restore_screenshot_best_speed, restore_screenshot_encoder):
            with self.assertRaises(ValueError):
                restore(data + b'\n')

    def inventory(self):
        raw = subprocess.check_output(['git', 'ls-files', '--stage', '-z'], cwd=ROOT)
        entries = [tuple(row.decode().split('\t', 1)) for row in raw.split(b'\0') if row]
        contents = {}
        for _, path in entries:
            if (path.endswith('_test.go') or path.startswith('.github/workflows/')
                    or path in ('go.mod', 'go.sum') or path.startswith('third_party/native-terminal/')):
                contents[path] = (ROOT / path).read_bytes()
        return entries, contents

    def test_all_four_original_frozen_inventories_accept_exact_change(self):
        entries, contents = self.inventory()
        for spec in GUARDS:
            with self.subTest(guard=spec[0]):
                run_guard(spec, ContentRoot(contents), index_bytes(entries))

    def test_all_four_frozen_inventories_reject_other_content_paths_modes_and_counts(self):
        entries, contents = self.inventory()
        original = next(item for item in entries if item[1] == SCREENSHOT_TEST)
        for change in ('helper-byte', 'native-assertion', 'previous-best-speed', 'original-default',
                       'default-compression', 'best-compression', 'numeric-no-compression',
                       'other-protected-byte', 'removed-path',
                       'renamed-path', 'executable-mode', 'extra-old-test', 'unmerged-stage'):
            changed_entries, changed_contents = list(entries), dict(contents)
            if change == 'helper-byte':
                changed_contents[SCREENSHOT_TEST] += b'\n'
            if change == 'native-assertion':
                changed_contents[SCREENSHOT_TEST] = changed_contents[SCREENSHOT_TEST].replace(
                    b'if !tt.HasText("Aster") {', b'if false {', 1)
            if change == 'previous-best-speed':
                changed_contents[SCREENSHOT_TEST] = changed_contents[SCREENSHOT_TEST].replace(
                    UNCOMPRESSED_ENCODE, FAST_ENCODE, 1)
            if change == 'original-default':
                changed_contents[SCREENSHOT_TEST] = changed_contents[SCREENSHOT_TEST].replace(
                    UNCOMPRESSED_ENCODE, OLD_ENCODE, 1)
            if change in ('default-compression', 'best-compression', 'numeric-no-compression'):
                alternative = {'default-compression': b'png.DefaultCompression',
                               'best-compression': b'png.BestCompression',
                               'numeric-no-compression': b'-1'}[change]
                changed_contents[SCREENSHOT_TEST] = changed_contents[SCREENSHOT_TEST].replace(
                    b'png.NoCompression', alternative, 1)
            if change == 'other-protected-byte':
                changed_contents['go.mod'] += b'\n'
            if change == 'removed-path':
                changed_entries.remove(original)
            if change == 'renamed-path':
                renamed = 'internal/ui/workbench_renamed_test.go'
                changed_entries[changed_entries.index(original)] = (original[0], renamed)
                changed_contents[renamed] = changed_contents[SCREENSHOT_TEST]
            if change in ('executable-mode', 'unmerged-stage'):
                mode, oid, stage = original[0].split()
                if change == 'executable-mode': mode = '100755'
                if change == 'unmerged-stage': stage = '1'
                changed_entries[changed_entries.index(original)] = (' '.join((mode, oid, stage)), original[1])
            if change == 'extra-old-test':
                extra = 'internal/ui/unreviewed_test.go'
                changed_entries.append(('100644 ' + '0' * 40 + ' 0', extra))
                changed_contents[extra] = b'package ui\n'
            for spec in GUARDS:
                with self.subTest(change=change, guard=spec[0]):
                    with self.assertRaises((AssertionError, ValueError)):
                        run_guard(spec, ContentRoot(changed_contents), index_bytes(changed_entries))


if __name__ == '__main__':
    unittest.main()
