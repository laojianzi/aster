"""Mandatory Go patch identity and exact-change negative regressions for PR #98."""
import hashlib
import json
import os
from pathlib import Path
import tempfile
import unittest

import go_toolchain_policy as policy
from retire_completed_oidc import APPROVED, DEPENDENCY_SCOPES, REQUIRED, REPO, candidate, workflows_pass

ROOT = Path(__file__).resolve().parents[1]


class GoToolchainPolicyTests(unittest.TestCase):
    def test_all_workflow_and_module_pins_are_exact_and_reversible(self):
        found = set()
        for p in (ROOT / '.github/workflows').glob('*.yml'):
            if b'go-version:' in p.read_bytes():
                found.add(p.relative_to(ROOT).as_posix())
        self.assertEqual(found, set(policy.PINS) - {'go.mod'})
        self.assertEqual(sum(v[0] for k, v in policy.PINS.items() if k != 'go.mod'), 10)
        for path, (count, old_sha, new_sha) in policy.PINS.items():
            with self.subTest(path=path):
                data = (ROOT / path).read_bytes()
                self.assertEqual(hashlib.sha256(data).hexdigest(), new_sha)
                old = policy.previous_toolchain_bytes(path, data)
                self.assertEqual(hashlib.sha256(old).hexdigest(), old_sha)
                self.assertNotEqual(old, data)

    def test_older_newer_floating_duplicate_and_missing_pins_are_refused(self):
        for path in policy.PINS:
            data = (ROOT / path).read_bytes()
            for wrong in (b'1.27.2', b'1.27.4', b'1.27', b'latest', b''):
                with self.subTest(path=path, wrong=wrong):
                    with self.assertRaises(ValueError):
                        policy.previous_toolchain_bytes(path, data.replace(b'1.27.3', wrong))
            with self.assertRaises(ValueError):
                policy.previous_toolchain_bytes(path, data + b"\n# 1.27.3\n")

    def test_actions_permissions_timeouts_and_required_tests_are_not_normalized(self):
        path = '.github/workflows/ci.yml'
        data = (ROOT / path).read_bytes()
        mutations = (
            (b'timeout-minutes: 20', b'timeout-minutes: 21'),
            (b'contents: read', b'contents: write'),
            (b'-timeout=5m', b'-timeout=10m'),
            (b'-race', b''),
            (b'actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1', b'actions/checkout@main'),
            (b'--require TestSchemaListSetStringEquality', b'--require NotTheReviewedTest'),
        )
        for old, new in mutations:
            with self.subTest(marker=old):
                self.assertIn(old, data)
                with self.assertRaises(ValueError):
                    policy.previous_toolchain_bytes(path, data.replace(old, new, 1))

    def test_other_dependency_changes_cannot_hide_in_module_version_update(self):
        data = (ROOT / 'go.mod').read_bytes()
        self.assertIn(b'github.com/ebitengine/purego v0.11.1', data)
        with self.assertRaises(ValueError):
            policy.previous_toolchain_bytes('go.mod', data.replace(b'purego v0.11.1', b'purego v0.11.2'))
        other = b'go 1.27.3\n'
        self.assertEqual(policy.previous_toolchain_bytes('unregistered.yml', other), other)

    def test_old_journal_fixture_remains_immutable(self):
        data = (ROOT / 'scripts/fixtures/helm_journal_admission_baseline.json').read_bytes()
        self.assertEqual(hashlib.sha256(data).hexdigest(),
                         'a67c2846450f78c9fe8cfea87d0a90dd8d6b6470368eb8b895b9dff3e7d77109')
        fixture = json.loads(data)
        for path, old_hash in fixture['unchanged_source_sha256'].items():
            restored = policy.previous_toolchain_bytes(path, (ROOT / path).read_bytes())
            self.assertEqual(hashlib.sha256(restored).hexdigest(), old_hash, path)

    def test_installed_go_matches_official_security_sources(self):
        receipt = policy.installed_toolchain()
        self.assertEqual(receipt['version'], 'go1.27.3')
        self.assertEqual(set(receipt['x509_sources']), set(policy.X509_BLOBS))
        # CI already owns this output directory; retain a machine-readable receipt.
        destination = os.environ.get('ASTER_TEST_ARTIFACTS')
        if destination:
            root = Path(destination)
            root.mkdir(parents=True, exist_ok=True)
            (root / 'go-toolchain.json').write_text(json.dumps(receipt, indent=2, sort_keys=True) + '\n')

    def test_wrong_runtime_and_modified_security_source_fail_closed(self):
        import subprocess
        env = json.loads(subprocess.check_output(
            ['go', 'env', '-json', 'GOROOT'], env=dict(os.environ, GOTOOLCHAIN='local'), text=True, timeout=20))
        root = Path(env['GOROOT'])
        with self.assertRaises(ValueError):
            policy.verify_toolchain(root, 'go1.27.2')
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory)
            (target / 'VERSION').write_text('go1.27.3\n')
            for path in policy.X509_BLOBS:
                p = target / path
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_bytes((root / path).read_bytes())
            self.assertEqual(policy.verify_toolchain(target, 'go1.27.3')['version'], 'go1.27.3')
            for path in policy.X509_BLOBS:
                p = target / path
                original = p.read_bytes()
                p.write_bytes(original + b'\n')
                with self.assertRaises(ValueError):
                    policy.verify_toolchain(target, 'go1.27.3')
                p.write_bytes(original)
            (target / 'VERSION').write_text('go1.27.2\n')
            with self.assertRaises(ValueError):
                policy.verify_toolchain(target, 'go1.27.3')

    def test_exact_go_pr_retirement_still_needs_all_five_main_gates(self):
        branch = 'renovate/go-1.x'
        self.assertEqual(APPROVED[98], branch)
        self.assertIn(98, DEPENDENCY_SCOPES)
        head, tip = 'a' * 40, 'b' * 40
        pr = dict(number=98, state='closed', merged=True, merge_commit_sha=head,
                  head=dict(ref=branch, sha=tip, repo=dict(full_name=REPO)),
                  base=dict(ref='main', repo=dict(full_name=REPO)))
        refs = {branch: dict(protected=False, commit=dict(sha=tip))}
        self.assertEqual(candidate(pr, head, refs, [], lambda _: True, number=98, branch=branch), tip)
        for field, value in [('state', 'open'), ('merged', False), ('merge_commit_sha', 'c' * 40)]:
            p = dict(pr, **{field: value})
            with self.assertRaises(ValueError):
                candidate(p, head, refs, [], lambda _: True, number=98, branch=branch)
        for bad_refs, active, ancestor in [
            ({branch: dict(protected=True, commit=dict(sha=tip))}, [], True),
            ({branch: dict(protected=False, commit=dict(sha='c' * 40))}, [], True),
            (refs, [pr], True), (refs, [], False),
        ]:
            with self.assertRaises(ValueError):
                candidate(pr, head, bad_refs, active, lambda _: ancestor, number=98, branch=branch)
        runs = [dict(id=i, name=n, event='push', head_branch='main', head_sha=head,
                     status='completed', conclusion='success') for i, n in enumerate(REQUIRED)]
        extra = ('Renovate configuration',)
        self.assertFalse(workflows_pass(runs, head, extra=extra))
        fifth = dict(runs[0], id=100, name=extra[0])
        self.assertTrue(workflows_pass(runs + [fifth], head, extra=extra))
        for k, v in [('head_sha', 'c' * 40), ('event', 'pull_request'), ('conclusion', 'failure')]:
            self.assertFalse(workflows_pass(runs + [dict(fifth, **{k: v})], head, extra=extra))


if __name__ == '__main__':
    unittest.main()
