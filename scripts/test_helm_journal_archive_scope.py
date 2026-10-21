"""Archival retains old requirements and cannot enable an unreviewed UI action."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import helm_journal_archive_evidence as archive
import helm_journal_evidence as journal
import helm_uninstall_contract_evidence as contract
import test_evidence as evidence
import retire_completed_oidc as retirement
from go_toolchain_policy import previous_toolchain_bytes

ROOT = Path(__file__).resolve().parents[1]


class HelmJournalArchiveScopeTests(unittest.TestCase):
    def test_exact_pr100_retirement_registration(self):
        self.assertEqual(retirement.APPROVED[100], 'feat/helm-journal-archive')
        self.assertNotIn(99, retirement.APPROVED)
        self.assertNotIn(100, retirement.DEPENDENCY_SCOPES)
        head, tip = 'a'*40, 'b'*40
        branch = 'feat/helm-journal-archive'
        pr = dict(number=100, state='closed', merged=True, merge_commit_sha=head,
                  head=dict(ref=branch, sha=tip, repo=dict(full_name=retirement.REPO)),
                  base=dict(ref='main', repo=dict(full_name=retirement.REPO)))
        refs = {branch: dict(protected=False, commit=dict(sha=tip))}
        self.assertEqual(retirement.select_scope([pr], head), 100)
        self.assertEqual(retirement.candidate(pr, head, refs, [], lambda _: True,
                                             number=100, branch=branch), tip)
        for number, selected in ((99, branch), (100, 'feat/other')):
            with self.assertRaises(ValueError):
                retirement.candidate(pr, head, refs, [], lambda _: True,
                                     number=number, branch=selected)
        refs[branch]['commit']['sha'] = 'c'*40
        with self.assertRaises(ValueError):
            retirement.candidate(pr, head, refs, [], lambda _: True,
                                 number=100, branch=branch)

    def test_exact_new_census_and_additive_gates(self):
        names = archive.ARCHIVE_REQUIRED
        self.assertEqual(len(names), 58)
        self.assertEqual(len(names), len(set(names)))
        self.assertEqual(hashlib.sha256('\n'.join(sorted(names)).encode()).hexdigest(),
                         '1963ca9fc4d831aff02f85d7077ba00dc10ca5d6a71d77dceeb3e14e332056e6')
        self.assertEqual(set(journal.REQUIRED), set(journal.RECOVERY_REQUIRED)
                         | set(journal.ADMISSION_REQUIRED) | set(names))
        self.assertEqual(contract.JOURNAL_ARCHIVE_REQUIRED, {'unit-native': names})
        self.assertTrue(set(names) <= set(contract.required('unit-native')))
        self.assertFalse(set(names) & set(contract.required('real-cluster-native')))

    def test_every_new_name_requires_exact_actual_package_execution(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'tests.jsonl'
            def write(missing=None, mode=None):
                events = []
                for name in journal.REQUIRED:
                    if name == missing and mode == 'missing':
                        continue
                    events.append(dict(Action='run', Package=journal.PACKAGE, Test=name))
                    if name == missing and mode == 'unfinished':
                        continue
                    action = mode if name == missing and mode in ('skip', 'fail') else 'pass'
                    events.append(dict(Action=action, Package=journal.PACKAGE, Test=name))
                    if name == missing and mode == 'duplicate':
                        events.append(dict(Action='pass', Package=journal.PACKAGE, Test=name))
                events.append(dict(Action='pass', Package=journal.PACKAGE))
                path.write_text(''.join(json.dumps(e)+'\n' for e in events))
            write()
            self.assertTrue(journal.validate(path)['valid'])
            for name in archive.ARCHIVE_REQUIRED:
                for mode in ('missing', 'skip', 'fail', 'unfinished', 'duplicate'):
                    with self.subTest(name=name, mode=mode):
                        write(name, mode)
                        self.assertFalse(journal.validate(path)['valid'])

    def test_platform_atomic_no_replace_no_fallback(self):
        expected = {'linux': 'unix.RENAME_NOREPLACE',
                    'darwin': 'unix.RENAME_EXCL',
                    'windows': 'windows.MOVEFILE_WRITE_THROUGH'}
        for platform, token in expected.items():
            text = (ROOT / ('internal/helmjournal/archive_'+platform+'.go')).read_text()
            self.assertIn(token, text)
            self.assertNotIn('os.Rename(', text)
            self.assertNotIn('os.Remove(', text)
            self.assertNotIn('io.Copy(', text)
        text = (ROOT / 'internal/helmjournal/archive.go').read_text()
        self.assertIn('!unresolved(s)', text)
        self.assertIn('s.Closed', text)
        self.assertIn('CompareAndSwap(false, true)', text)
        self.assertIn('os.SameFile(info, review.info)', text)
        self.assertIn('sum != review.sum', text)

    def test_no_unreviewed_native_or_network_consumer(self):
        for path in (ROOT / 'internal').rglob('*.go'):
            if path.name.endswith('_test.go') or 'helmjournal' in path.parts or 'upstream' in path.parts:
                continue
            self.assertNotIn('.PrepareArchive(', path.read_text(), str(path))
            self.assertNotIn('.LoadArchived(', path.read_text(), str(path))
        for path in (ROOT / 'internal/helmjournal').glob('archive*.go'):
            if path.name.endswith('_test.go'):
                continue
            for token in ('"net/http"', 'internal/kube', 'internal/operation', 'exec.Command('):
                self.assertNotIn(token, path.read_text(), str(path))

    def test_existing_toolchain_and_workflows_stay_exact(self):
        for path in ('go.mod', '.github/workflows/ci.yml', '.github/workflows/native-migration.yml',
                     '.github/workflows/source-evidence.yml', '.github/workflows/oidc-qualification.yml',
                     '.github/workflows/offline-devkit.yml', '.github/workflows/terminal-devkit.yml'):
            previous_toolchain_bytes(path, (ROOT/path).read_bytes())
        native = (ROOT / '.github/workflows/native-migration.yml').read_text()
        self.assertIn('go test -race -shuffle=on -count=1 -timeout=3m -json ./internal/helmjournal/...', native)
        self.assertIn('python scripts/helm_journal_evidence.py artifacts/journal/tests.jsonl', native)


if __name__ == '__main__':
    unittest.main()
