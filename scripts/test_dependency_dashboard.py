import copy
import json
from pathlib import Path
import re
import unittest
from check_dependency_extraction import check, MODULE, SPEC
from prepare_terminal import validate_module_pin

ROOT = Path(__file__).resolve().parents[1]


class DashboardTests(unittest.TestCase):
    def test_tracking_group_and_approval(self):
        cfg = json.loads((ROOT/'renovate.json').read_text())
        self.assertIs(cfg['dependencyDashboard'], True)
        self.assertIs(cfg['dependencyDashboardAutoclose'], True)
        self.assertIs(cfg['dependencyDashboardApproval'], True)
        self.assertIs(cfg['automerge'], False)
        rules = [r for r in cfg['packageRules'] if MODULE in r.get('matchPackageNames', [])]
        self.assertEqual(len(rules), 1)
        self.assertNotEqual(rules[0].get('enabled'), False)
        self.assertEqual(set(rules[0]['matchManagers']), {'gomod','custom.regex'})
        self.assertEqual(rules[0]['groupSlug'], 'mygo-native-framework')

    def test_autoclose_does_not_relax_approval_or_hide_dependencies(self):
        cfg = json.loads((ROOT/'renovate.json').read_text())
        self.assertEqual(cfg['extends'], ['config:recommended', 'helpers:pinGitHubActionDigests'])
        self.assertNotIn('enabledManagers', cfg)
        self.assertNotIn('ignoreDeps', cfg)
        self.assertNotIn('mode', cfg)
        for rule in cfg['packageRules']:
            self.assertIsNot(rule.get('enabled'), False)
            self.assertIsNot(rule.get('dependencyDashboardApproval'), False)
            self.assertIsNot(rule.get('automerge'), True)
        mygo, = [r for r in cfg['packageRules'] if MODULE in r.get('matchPackageNames', [])]
        self.assertIs(mygo['dependencyDashboardApproval'], True)
        self.assertIs(mygo['automerge'], False)
        k8s, = [r for r in cfg['packageRules'] if 'k8s.io/client-go' in r.get('matchPackageNames', [])]
        self.assertEqual(set(k8s['matchPackageNames']),
                         {'k8s.io/api', 'k8s.io/apimachinery', 'k8s.io/client-go', 'k8s.io/streaming'})
        self.assertEqual(k8s['groupName'], 'kubernetes client and streaming')

    def test_version_only_replacement_cannot_rewrite_trust(self):
        cfg = json.loads((ROOT/'renovate.json').read_text())
        manager, = cfg['customManagers']
        self.assertEqual(manager['managerFilePatterns'], [r'/^third_party\/native-terminal\/UPSTREAM\.json$/'])
        self.assertEqual(manager['datasourceTemplate'], 'go')
        self.assertEqual(manager['depNameTemplate'], MODULE)
        pattern = manager['matchStrings'][0].replace('(?<currentValue>', '(?P<currentValue>')
        text = (ROOT/SPEC).read_text()
        match, = list(re.finditer(pattern, text))
        a,b = match.span('currentValue')
        updated = text[:a]+'v0.999.0'+text[b:]
        before, after = json.loads(text), json.loads(updated)
        before['version'] = after['version']
        self.assertEqual(before, after)
        with self.assertRaises(ValueError):
            validate_module_pin((ROOT/'go.mod').read_text(), after)
        self.assertFalse(re.search(pattern, text.replace(MODULE, 'untrusted.example/mygo')))

    def snapshot(self):
        return {'packageFiles': {'gomod': [{'packageFile':'go.mod','deps':[{'depName':MODULE,'currentValue':'v0.3.7'}]}],
                                'regex': [{'packageFile':SPEC,'deps':[{'depName':MODULE,'currentValue':'v0.3.7'}]}]}}

    def test_actual_snapshot_required(self):
        good = self.snapshot()
        self.assertEqual(len(check([good], 'v0.3.7')['extractions']), 2)
        for bad in [[], [{'msg':MODULE}], [{'packageFiles':{}}]]:
            with self.assertRaises(ValueError): check(bad, 'v0.3.7')

    def test_missing_duplicate_disabled_drift_and_error_rejected(self):
        for mode in ['missing','duplicate','disabled','skipped','drift','error']:
            with self.subTest(mode=mode):
                item = self.snapshot(); dep = item['packageFiles']['regex'][0]['deps'][0]
                if mode == 'missing': del item['packageFiles']['regex']
                if mode == 'duplicate': item['packageFiles']['regex'].append(copy.deepcopy(item['packageFiles']['regex'][0]))
                if mode == 'disabled': dep['enabled'] = False
                if mode == 'skipped': dep['skipReason'] = 'disabled'
                if mode == 'drift': dep['currentValue'] = 'v9.0.0'
                if mode == 'error': item['level'] = 50
                with self.assertRaises(ValueError): check([item], 'v0.3.7')


if __name__ == '__main__': unittest.main()
