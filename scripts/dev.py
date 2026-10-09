#!/usr/bin/env python3
"""Portable development build/test entrypoint (Go and Python must be installed)."""
import argparse
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['prepare', 'build', 'test'])
    parser.add_argument('--source', type=Path)
    parser.add_argument('--library-source', type=Path)
    args = parser.parse_args()
    prepare = [sys.executable, str(ROOT/'scripts/prepare_terminal.py')]
    for key in ('source', 'library_source'):
        if getattr(args, key):
            prepare += ['--'+key.replace('_', '-'), str(getattr(args, key).resolve())]
    subprocess.run(prepare, cwd=ROOT, check=True)
    provenance = json.loads((ROOT/'.aster-native/provenance.json').read_text())
    library = ROOT/'.aster-native'/provenance['library']['name']
    if args.command == 'test':
        subprocess.run(['go', 'test', '-race', '-count=1', './...'], cwd=ROOT,
                       env=dict(os.environ, ASTER_TEST_LIBRARY=str(library)), check=True)
    if args.command == 'build':
        destination = ROOT/'dist'
        destination.mkdir(exist_ok=True)
        binary = 'aster.exe' if platform.system() == 'Windows' else 'aster'
        subprocess.run(['go', 'build', '-trimpath', '-o', str(destination/binary), './cmd/aster'], cwd=ROOT, check=True)
        shutil.copyfile(library, destination/library.name)
        shutil.copyfile(ROOT/'.aster-native/provenance.json', destination/'terminal-provenance.json')
        (destination/'licenses').mkdir(exist_ok=True)
        for license in (ROOT/'third_party/native-terminal').glob('LICENSE.*'):
            shutil.copyfile(license, destination/'licenses'/license.name)
