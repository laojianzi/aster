import importlib.util
from pathlib import Path
import tempfile
import unittest

MODULE = Path(__file__).with_name('prepare_terminal.py')
spec = importlib.util.spec_from_file_location('terminal_preparation', MODULE)
preparation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(preparation)

class TerminalSourceVerification(unittest.TestCase):
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

    def test_source_size_is_bounded(self):
        with tempfile.TemporaryDirectory() as d:
            root = Path(d)
            data = b'x' * ((1 << 20) + 1)
            (root/'file.go').write_bytes(data)
            with self.assertRaises(ValueError):
                preparation.source_files(root, {'files': {'file.go': preparation.sha(data)}})

if __name__ == '__main__':
    unittest.main()
