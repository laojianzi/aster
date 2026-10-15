"""Keep the JOSE security update coupled to its actual tests and safe defaults."""
from pathlib import Path
import re
import copy
import unittest

ROOT = Path(__file__).resolve().parents[1]
NAMES = (
    'TestOIDCJOSE416RSAAndES256Compatibility',
    'TestOIDCJOSE416RejectsPrivateAndEmbeddedKeys',
)


class JOSESecurityScopeTests(unittest.TestCase):
    def test_runtime_tests_mandatory_in_unit_and_oidc_gates(self):
        for path, suite in [('.github/workflows/ci.yml', 'unit-native'),
                            ('.github/workflows/oidc-qualification.yml', 'oidc-dex-real-kubernetes')]:
            commands = [line for line in (ROOT/path).read_text().splitlines()
                        if 'scripts/test_evidence.py' in line and '--suite '+suite+' ' in line]
            self.assertEqual(len(commands), 1)
            for name in NAMES:
                self.assertEqual(commands[0].count('--require '+name), 1)
        source = (ROOT/'internal/oidclogin/jose_security_test.go').read_text()
        for name in NAMES:
            self.assertIn('func '+name+'(t *testing.T)', source)

    def test_security_dependency_is_pinned_and_checksummed(self):
        self.assertRegex((ROOT/'go.mod').read_text(), r'(?m)^\s*github\.com/go-jose/go-jose/v4 v4\.1\.6$')
        sums = (ROOT/'go.sum').read_text().splitlines()
        self.assertIn('github.com/go-jose/go-jose/v4 v4.1.6 h1:sNRkftxFJxwsILiQukqp5z83QBSmntVwmgRY7AqBDO4=', sums)
        self.assertIn('github.com/go-jose/go-jose/v4 v4.1.6/go.mod h1:x4oUasVrzR7071A4TnHLGSPpNOm2a21K9Kf04k1rs08=', sums)

    def test_no_process_wide_limit_override_or_broader_documented_range(self):
        for path in (ROOT/'internal').rglob('*.go'):
            if 'nativeterm/upstream' in path.as_posix():
                continue
            self.assertIsNone(re.search(r'\bjose\.MaxRSAModulusBits\s*=(?!=)', path.read_text()), str(path))
        contract = (ROOT/'docs/oidc-login.md').read_text()
        self.assertIn('2048–4096-bit RSA', contract)
        self.assertIn('login and renewal', contract)
        self.assertNotIn('2048–8192-bit RSA', contract)


    def test_only_actual_jose_pr_is_eligible_for_guarded_retirement(self):
        from retire_completed_oidc import APPROVED, DEPENDENCY_SCOPES, REPO, candidate, select_scope
        branch, head, tip = 'renovate/github.com-go-jose-go-jose-v4-4.x', 'a'*40, 'b'*40
        self.assertEqual(APPROVED[73], branch)
        self.assertIn(73, DEPENDENCY_SCOPES)
        pr = dict(number=73, state='closed', merged=True, merge_commit_sha=head,
                  head=dict(ref=branch, sha=tip, repo=dict(full_name=REPO)),
                  base=dict(ref='main', repo=dict(full_name=REPO)))
        refs = {branch: dict(protected=False, commit=dict(sha=tip))}
        self.assertEqual(select_scope([pr], head), 73)
        self.assertEqual(candidate(pr, head, refs, [], lambda _: True, number=73, branch=branch), tip)
        self.assertIsNone(candidate(pr, head, {}, [], lambda _: True, number=73, branch=branch))
        for number in (11, 71, 72, 74):
            with self.assertRaises(ValueError):
                candidate(pr, head, refs, [], lambda _: True, number=number, branch=branch)
        for field, value in [('number', 11), ('state', 'open'), ('merged', False), ('merge_commit_sha', 'c'*40)]:
            changed = dict(pr, **{field: value})
            with self.assertRaises(ValueError):
                candidate(changed, head, refs, [], lambda _: True, number=73, branch=branch)
        for changed_refs, active, ancestor in [
            ({branch: dict(protected=True, commit=dict(sha=tip))}, [], True),
            ({branch: dict(protected=False, commit=dict(sha='c'*40))}, [], True),
            (refs, [pr], True), (refs, [], False),
        ]:
            with self.assertRaises(ValueError):
                candidate(pr, head, changed_refs, active, lambda _: ancestor, number=73, branch=branch)
        for side in ('head', 'base'):
            changed = copy.deepcopy(pr)
            changed[side]['repo']['full_name'] = 'foreign/repo'
            with self.assertRaises(ValueError):
                candidate(changed, head, refs, [], lambda _: True, number=73, branch=branch)

if __name__ == '__main__':
    unittest.main()
