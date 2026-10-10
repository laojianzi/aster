#!/usr/bin/env python3
"""Run the named migration contract in both builds; absent/skipped cases fail."""
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
REQUIRED = [
    'TestNativeEditorUndoCannotCrossResourceIdentity',
    'TestNativeCommandUndoCannotCrossResourceIdentity',
    'TestNativeMigrationTextFieldsDoNotShareHistory',
    'TestNativeMigrationReadOnlyRejectsTypingAndUndo',
    'TestNativeMigrationDisabledCommandControlsIgnoreInput',
    'TestNativeMigrationResourceSwitchDropsComposition',
    'TestNativeMigrationRowReorderDoesNotRetargetClick',
    'TestNativeMigrationActionsRunAfterConstructionOnce',
    'TestNativeMigrationTransientMessagePreservesEditorHistory',
    'TestNativeMigrationConstructorsAreKeyed',
]

if __name__ == '__main__':
    output = Path(os.environ.get('ASTER_TEST_ARTIFACTS', ROOT / 'artifacts'))
    output.mkdir(parents=True, exist_ok=True)
    head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=ROOT, text=True)
    (output / 'COMMIT').write_text(head)
    for mode in ('checked', 'production'):
        path = output / ('migration-' + mode + '.jsonl')
        args = ['go', 'test', '-count=1', '-timeout=3m', '-json', './internal/ui',
                '-run', '^(' + '|'.join(REQUIRED) + ')$']
        if mode == 'production':
            args.insert(2, '-tags=mygo_noinspector')
        with path.open('w', encoding='utf-8') as log:
            result = subprocess.run(args, cwd=ROOT, stdout=log)
        # Validate even on nonzero Go exit, to retain missing-case evidence.
        verify = [sys.executable, 'scripts/test_evidence.py', str(path), '--suite', 'migration-' + mode]
        for name in REQUIRED:
            verify.extend(['--require', name])
        validation = subprocess.run(verify, cwd=ROOT)
        if result.returncode or validation.returncode:
            raise SystemExit('migration verification failed in ' + mode)
