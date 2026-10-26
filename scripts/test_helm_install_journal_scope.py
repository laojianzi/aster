"""Install WAL additions do not replace old acceptance or grant cluster access."""
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest

import helm_install_journal_evidence as install
import helm_journal_evidence as journal
import helm_uninstall_contract_evidence as contract
import test_evidence as evidence

ROOT = Path(__file__).resolve().parents[1]
BASE = '64ed3e4430b9a6fdcfda8bfa3f17dd946bbb9a08'

class InstallJournalScopeTests(unittest.TestCase):
    def test_new_names_are_additive_on_existing_matrices(self):
        names = install.INSTALL_JOURNAL_NAMES
        self.assertEqual(len(names), len(set(names)))
        self.assertTrue(set(names) <= set(journal.REQUIRED))
        self.assertTrue(set(install.INSTALL_JOURNAL_REQUIRED['unit-native']) <= set(contract.required('unit-native')))
        self.assertFalse(set(names) & set(contract.required('real-cluster-native')))
        self.assertEqual(set(journal.REQUIRED), set(journal.RECOVERY_REQUIRED) | set(journal.ADMISSION_REQUIRED)
                         | set(journal.ARCHIVE_REQUIRED) | set(journal.IDENTITY_NAMES) | set(names))

    def test_every_new_name_must_run_and_finish_once(self):
        names = install.INSTALL_JOURNAL_REQUIRED['unit-native']
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'test.jsonl'
            for chosen in names:
                for mode in ('pass', 'missing', 'fail', 'skip', 'unfinished', 'duplicate'):
                    events = []
                    for name in names:
                        if name == chosen and mode == 'missing': continue
                        events.append(dict(Action='run', Package='fixture', Test=name))
                        if name == chosen and mode == 'unfinished': continue
                        action = mode if name == chosen and mode in ('fail','skip') else 'pass'
                        events.append(dict(Action=action, Package='fixture', Test=name))
                        if name == chosen and mode == 'duplicate': events.append(events[-1])
                    events.append(dict(Action='pass',Package='fixture'))
                    path.write_text(''.join(json.dumps(e)+'\n' for e in events))
                    self.assertEqual(evidence.summarize(path,list(names),'install-wal')['valid'],mode=='pass')

    def test_no_transport_authority_or_duplicate_storage(self):
        for p in (ROOT/'internal/helmjournal').glob('install_*.go'):
            if p.name.endswith('_test.go'): continue
            text=p.read_text()
            for forbidden in ('net/http','os/exec','client-go','func (s *Store) Resume','http.MethodPost'):
                self.assertNotIn(forbidden,text)
        store=(ROOT/'internal/helmjournal/install_store.go').read_text()
        for required in ('s.beginFrame(', 's.admissionLocked(', 'x.append(', 'x.check()'):
            self.assertIn(required,store)
        for p in (ROOT/'internal/ui').glob('*.go'):
            if not p.name.endswith('_test.go'): self.assertNotIn('BeginInstall(',p.read_text())

    def test_old_go_tests_workflows_and_pins_are_byte_preserved(self):
        paths=subprocess.check_output(['git','ls-tree','-r','--name-only',BASE],cwd=ROOT,text=True).splitlines()
        selected=[p for p in paths if p.endswith('_test.go') or p.startswith('.github/workflows/')
                  or p in ('go.mod','go.sum') or p.startswith('third_party/native-terminal/')]
        self.assertGreater(len(selected),250)
        for p in selected:
            original=subprocess.check_output(['git','show',BASE+':'+p],cwd=ROOT)
            self.assertEqual((ROOT/p).read_bytes(),original,p)
