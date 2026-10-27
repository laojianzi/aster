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
DRY_RUN_FIXTURE = 'internal/kube/helm_install_dry_run_test.go'
HOOK = b'\t\tif f.onPost != nil {\n\t\t\tf.onPost(n)\n\t\t}\n'
OLD_SITE = b'\t\tn := int(f.posts.Add(1))\n'
NEW_SITE = b'\t\tclear(raw)\n'


def restore_fixture_hook(data):
    # The sole old-test exception is a precise move after full body validation.
    # Keep the original baseline digest; never accept broader fixture changes.
    if (data.count(HOOK) != 1 or data.count(NEW_SITE + HOOK) != 1
            or data.count(OLD_SITE) != 1 or OLD_SITE + HOOK in data):
        raise ValueError('unexpected dry-run fixture hook edit')
    return data.replace(NEW_SITE + HOOK, NEW_SITE, 1).replace(OLD_SITE, OLD_SITE + HOOK, 1)


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
        # Freeze path/mode/content together; works in the existing shallow CI
        # checkout without fetching history or trusting a live baseline list.
        added = {'internal/helmjournal/install_test.go',
                 'internal/helmjournal/install_integrity_test.go',
                 'internal/helmjournal/install_process_test.go',
                 'internal/kube/helm_install_journal_test.go',
                 'internal/kube/helm_install_dry_run_fixture_test.go'}
        raw=subprocess.check_output(['git','ls-files','--stage','-z'],cwd=ROOT)
        rows=[]
        tracked=set()
        for entry in raw.split(b'\0'):
            if not entry: continue
            header,path=entry.decode().split('\t',1)
            mode,_,stage=header.split()
            self.assertEqual(stage,'0')
            tracked.add(path)
            protected=(path.endswith('_test.go') or path.startswith('.github/workflows/')
                       or path in ('go.mod','go.sum') or path.startswith('third_party/native-terminal/'))
            if protected and path not in added:
                data=(ROOT/path).read_bytes()
                if path == DRY_RUN_FIXTURE:
                    data=restore_fixture_hook(data)
                digest=hashlib.sha256(data).hexdigest()
                rows.append(path+'\0'+mode+'\0'+digest)
        self.assertTrue(added <= tracked)
        self.assertEqual(len(rows),260)
        self.assertEqual(hashlib.sha256('\n'.join(sorted(rows)).encode()).hexdigest(),
                         '7e8a4d4c2695cb3054139fa133c794564a47233a3a402f49af15b58d59e83e1c')

    def test_precise_hook_exception_preserves_original_content(self):
        data=(ROOT/DRY_RUN_FIXTURE).read_bytes()
        expected='69695470da15cefb605e4e949d125630b62892623f1c61f8710f91511eb49fcf'
        self.assertEqual(hashlib.sha256(restore_fixture_hook(data)).hexdigest(),expected)
        # Assertion removal, route changes and suppressed read failures cannot
        # be hidden by moving the hook or changing a baseline checksum.
        for old,new in ((b'f.bad.Store(true)',b'f.bad.Store(false)'),
                        (b'q.Get("dryRun") != "All"',b'q.Get("dryRun") != "None"'),
                        (b'if e != nil ||',b'if false ||')):
            changed=data.replace(old,new,1)
            self.assertNotEqual(changed,data)
            self.assertNotEqual(hashlib.sha256(restore_fixture_hook(changed)).hexdigest(),expected)
        for changed in (data.replace(HOOK,b'',1), data+HOOK,
                        data.replace(NEW_SITE+HOOK,HOOK+NEW_SITE,1)):
            with self.assertRaises(ValueError):
                restore_fixture_hook(changed)
