"""A green platform summary cannot hide absent, skipped or repeated journal work."""
import json
from pathlib import Path
import tempfile
import unittest

import helm_journal_evidence as journal


class HelmJournalEvidenceTests(unittest.TestCase):
    def events(self):
        events = []
        for name in journal.REQUIRED:
            events.extend((dict(Action='run', Package=journal.PACKAGE, Test=name),
                           dict(Action='pass', Package=journal.PACKAGE, Test=name)))
        return events + [dict(Action='pass', Package=journal.PACKAGE)]

    def validate(self, events):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'tests.jsonl'
            path.write_text(''.join(json.dumps(item) + '\n' for item in events), encoding='utf-8')
            return journal.validate(path)

    def test_complete_actual_package_stream_and_subcases(self):
        events = self.events()
        for action in ('run', 'pass'):
            events.insert(-1, dict(Action=action, Package=journal.PACKAGE,
                                   Test=journal.REQUIRED[0] + '/reopened'))
        self.assertTrue(self.validate(events)['valid'])

    def test_every_required_full_name_must_actually_run_and_pass(self):
        self.assertTrue(self.validate(self.events())['valid'])
        for name in journal.REQUIRED:
            for outcome in ('missing', 'fail', 'skip', 'unfinished', 'pass-without-run'):
                with self.subTest(name=name, outcome=outcome):
                    events = []
                    for event in self.events():
                        if event.get('Test') == name:
                            if outcome == 'missing':
                                continue
                            if event['Action'] == 'run' and outcome == 'pass-without-run':
                                continue
                            if event['Action'] == 'pass':
                                if outcome == 'unfinished':
                                    continue
                                if outcome in ('fail', 'skip'):
                                    event['Action'] = outcome
                        events.append(event)
                    self.assertFalse(self.validate(events)['valid'])

    def test_replayed_tests_and_forged_package_cannot_qualify(self):
        for mode in ('repeat-run', 'repeat-pass', 'repeat-both', 'foreign',
                     'missing-package-pass', 'duplicate-package-pass'):
            with self.subTest(mode=mode):
                events = self.events()
                if mode.startswith('repeat'):
                    for event in self.events()[:2]:
                        if mode == 'repeat-both' or event['Action'] == mode.removeprefix('repeat-'):
                            events.insert(-1, event)
                elif mode == 'foreign':
                    for event in events:
                        event['Package'] = journal.PACKAGE + '-substitute'
                elif mode == 'missing-package-pass':
                    events.pop()
                else:
                    events.append(dict(Action='pass', Package=journal.PACKAGE))
                self.assertFalse(self.validate(events)['valid'])

    def test_invalid_json_event_is_rejected(self):
        for event in ([], 'summary says pass', {'Action': 'run', 'Test': 7}):
            with self.subTest(event=event), self.assertRaises(ValueError):
                self.validate([event])


if __name__ == '__main__':
    unittest.main()
