import unittest


class DependencyRetirementTests(unittest.TestCase):
    def test_dependency_retirement_needs_fifth_exact_main_gate(self):
        from retire_completed_oidc import APPROVED, REQUIRED, DEPENDENCY_SCOPES, workflows_pass
        self.assertEqual(APPROVED[31], 'fix/dependency-dashboard-mygo')
        self.assertEqual(APPROVED[33], 'renovate/mygo-native-framework')
        self.assertEqual(APPROVED[39], 'renovate/mygo-native-framework')
        self.assertEqual(APPROVED[41], 'renovate/mygo-native-framework')
        self.assertEqual(APPROVED[58], 'renovate/mygo-native-framework')
        self.assertEqual(APPROVED[73], 'renovate/github.com-go-jose-go-jose-v4-4.x')
        self.assertEqual(APPROVED[75], 'renovate/mygo-native-framework')
        self.assertEqual(APPROVED[85], 'feat/helm-release-status-transition')
        self.assertEqual(set(DEPENDENCY_SCOPES), {31, 33, 39, 40, 41, 58, 73, 74, 75, 85})
        head = 'a' * 40
        runs = [dict(name=n,id=i,event='push',head_branch='main',head_sha=head,
                     status='completed',conclusion='success') for i,n in enumerate(REQUIRED)]
        extra = ('Renovate configuration',)
        self.assertFalse(workflows_pass(runs,head,extra=extra))
        result = dict(runs[0],name=extra[0],id=100)
        self.assertTrue(workflows_pass(runs+[result],head,extra=extra))
        self.assertFalse(workflows_pass(runs+[dict(result,head_sha='b'*40)],head,extra=extra))
        self.assertFalse(workflows_pass(runs+[result,dict(result,id=101,conclusion='failure')],head,extra=extra))


if __name__ == "__main__": unittest.main()
