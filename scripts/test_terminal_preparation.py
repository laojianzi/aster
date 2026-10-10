import importlib.util
from pathlib import Path
import tempfile
import subprocess
import shutil
import unittest

MODULE = Path(__file__).with_name('prepare_terminal.py')
spec = importlib.util.spec_from_file_location('terminal_preparation', MODULE)
preparation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(preparation)

class TerminalSourceVerification(unittest.TestCase):
    def test_framework_and_terminal_versions_must_match(self):
        spec = {'module': 'github.com/egoist/mygo', 'version': 'v0.3.7'}
        preparation.validate_module_pin('require (\n github.com/egoist/mygo v0.3.7\n)\n', spec)
        preparation.validate_module_pin('require github.com/egoist/mygo v0.3.7 // native\n', spec)
        for invalid in [
            'require github.com/egoist/mygo v0.2.15\n',
            'module unrelated\n',
            'require github.com/egoist/mygo v0.3.7\nreplace github.com/egoist/mygo => ./fake\n',
            'require github.com/egoist/mygo v0.3.7\nreplace (\n github.com/egoist/mygo v0.3.7 => ./fake\n)\n',
        ]:
            with self.subTest(go_mod=invalid):
                with self.assertRaises(ValueError):
                    preparation.validate_module_pin(invalid, spec)

    def test_tamper_and_unsafe_paths_are_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            (root/'file.go').write_bytes(b'package terminal\n')
            expected = {'files': {'file.go': preparation.sha(b'package terminal\n')}}
            self.assertEqual(preparation.source_files(root, expected)['file.go'], b'package terminal\n')
            (root/'file.go').write_bytes(b'package changed\n')
            with self.assertRaises(ValueError):
                preparation.source_files(root, expected)
            with self.assertRaises(ValueError):
                preparation.source_files(root, {'files': {'../outside': '0'*64}})

    def test_windows_autocrlf_checkout_preserves_patch_bytes(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            subprocess.run(['git', 'init', '-q'], cwd=root, check=True, capture_output=True)
            subprocess.run(['git', 'config', 'core.autocrlf', 'true'], cwd=root, check=True)
            shutil.copyfile(preparation.ROOT/'.gitattributes', root/'.gitattributes')
            original = (preparation.META/'hardening.patch').read_bytes()
            self.assertNotIn(b'\r\n', original)
            patch = root/'hardening.patch'
            patch.write_bytes(original)
            subprocess.run(['git', 'add', '.gitattributes', 'hardening.patch'], cwd=root, check=True)
            patch.unlink()
            subprocess.run(['git', 'checkout-index', '-f', '--', 'hardening.patch'], cwd=root, check=True)
            self.assertEqual(patch.read_bytes(), original)

    def test_generated_format_is_repeatable_after_import_rewrite(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            source = root / 'view.go'
            source.write_text('package terminal\nimport ("z"; "a")\nfunc f(){println(1)}\n')
            preparation.format_generated_sources(root)
            first = source.read_bytes()
            self.assertNotIn(b';', first)
            self.assertLess(first.index(b'"a"'), first.index(b'"z"'))
            preparation.format_generated_sources(root)
            self.assertEqual(source.read_bytes(), first)

    def test_source_size_is_bounded(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            data = b'x' * ((1 << 20) + 1)
            (root/'file.go').write_bytes(data)
            with self.assertRaises(ValueError):
                preparation.source_files(root, {'files': {'file.go': preparation.sha(data)}})

if __name__ == '__main__':
    unittest.main()
