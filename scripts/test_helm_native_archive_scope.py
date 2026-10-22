"""Native archive evidence adds names without replacing any old contract."""
import hashlib
import json
from pathlib import Path
import tempfile
import unittest

import helm_native_archive_evidence as archive
import helm_journal_evidence as journal
import helm_uninstall_contract_evidence as contract
import retire_completed_oidc as retirement
import run_migration_tests as migration
import test_evidence as evidence

ROOT = Path(__file__).resolve().parents[1]
CENSUS = {
    'IDENTITY_NAMES': (13, 'b4c9660a954b66cc702af4f9020683b2c6b020a018587289f138f39f4bd2b146'),
    'WORKER_NAMES': (16, '7062f9cdbf5b30daff7f806043142fb19a5334c21e863ac75341e03650dfec79'),
    'NATIVE_NAMES': (16, '62875baf0b04f821ad5854c2dc1ffe02ee6654605de08ec9d3925b0837fc78f9'),
    'REAL_NAMES': (3, '1c8a7689aa799961bd4a6ca416c0c96ff1fd3d6cd9133a044ea136abb28fa96a'),
}


class HelmNativeArchiveScopeTests(unittest.TestCase):
    def test_exact_independent_census(self):
        union = set()
        for key, (count, digest) in CENSUS.items():
            names = getattr(archive, key)
            self.assertEqual(len(names), count)
            self.assertEqual(len(set(names)), count)
            self.assertFalse(union & set(names))
            union.update(names)
            self.assertEqual(hashlib.sha256('\n'.join(sorted(names)).encode()).hexdigest(), digest)
        self.assertEqual(len(union), 48)

    def test_applicable_gates_require_all_additions(self):
        for suite, names in archive.AUTOMATIC.items():
            required = evidence.required_for_suite(suite, ['TestOldContract'])
            self.assertEqual(required[0], 'TestOldContract')
            self.assertTrue(set(names) <= set(required))
            self.assertEqual(len(required), len(set(required)))
        self.assertTrue(archive.additions('unit-native') <= set(contract.required('unit-native')))
        self.assertTrue(set(archive.NATIVE_NAMES) <= set(migration.REQUIRED))
        self.assertTrue(set(archive.IDENTITY_NAMES) <= set(journal.REQUIRED))
        self.assertFalse(set(archive.REAL_NAMES) & set(contract.required('unit-native')))
        self.assertEqual(evidence.required_for_suite('other-suite', ['TestOldContract']), ['TestOldContract'])

    def test_every_new_name_requires_a_completed_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'tests.jsonl'
            for group in CENSUS:
                names = ['TestOldContract', *getattr(archive, group)]
                for excluded in names:
                    for mode in ('pass', 'missing', 'fail', 'skip', 'unfinished', 'duplicate'):
                        with self.subTest(group=group, name=excluded, mode=mode):
                            events = []
                            for name in names:
                                if name == excluded and mode == 'missing':
                                    continue
                                events.append(dict(Action='run', Package='fixture', Test=name))
                                if name == excluded and mode == 'unfinished':
                                    continue
                                action = mode if name == excluded and mode in ('fail', 'skip') else 'pass'
                                events.append(dict(Action=action, Package='fixture', Test=name))
                                if name == excluded and mode == 'duplicate':
                                    events.append(dict(Action='pass', Package='fixture', Test=name))
                            events.append(dict(Action='pass', Package='fixture'))
                            path.write_text(''.join(json.dumps(e)+'\n' for e in events))
                            result = evidence.summarize(path, names, 'native-archive-fixture')
                            self.assertEqual(result['valid'], mode == 'pass')

    def test_exact_pr102_registration(self):
        self.assertEqual(retirement.APPROVED[102], 'feat/native-helm-journal-archive')
        self.assertNotIn(101, retirement.APPROVED)
        self.assertNotIn(102, retirement.DEPENDENCY_SCOPES)
        self.assertEqual(retirement.APPROVED[100], 'feat/helm-journal-archive')
        self.assertEqual(retirement.REQUIRED,
                         ('CI', 'Source evidence', 'Native migration verification', 'OIDC qualification'))

    def test_worker_has_no_network_or_replay_authority(self):
        text = (ROOT/'internal/kube/helm_journal_archive.go').read_text()
        for token in ('"net/http"', 'exec.Command', 'LoadKubeconfig', 'ExecuteHelmUninstall', 'store.Begin('):
            self.assertNotIn(token, text)
        for token in ('helmjournal.OpenExisting(path)', 'store.PrepareLoadedArchive(ctx, history.identity)',
                      'review.Confirm(id)', 'store.Archive(ctx, consent)', 'store.Close()',
                      'context.WithDeadline(ctx, review.ExpiresAt())', 'CompareAndSwap(false, true)',
                      's.state.history == r'):
            self.assertIn(token, text)

    def test_ui_requires_fresh_identity_and_explicit_confirmation(self):
        text = (ROOT/'internal/ui/helm_archive.go').read_text()
        for token in ('u.archive.MatchesHistory(u.recovery)', 'u.archiveComposing', 'u.archiveAck',
                      'u.archiveConfirmation != u.selectedID', 'x.archiveReload = true',
                      '<-session.Done()', 'w.operationContext()'):
            self.assertIn(token, text)
        self.assertNotIn('os.Remove(', text)
        self.assertNotIn('.Archive(', text)  # only the worker owns the Store


if __name__ == '__main__':
    unittest.main()
