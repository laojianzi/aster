import copy
import unittest
from retire_completed_oidc import candidate, workflows_pass, REQUIRED, REPO, BRANCH

class RetirementTests(unittest.TestCase):
    def setUp(self):
        self.head, self.tip = 'a'*40, 'b'*40
        self.pr = dict(number=25, state='closed', merged=True, merge_commit_sha=self.head,
            head=dict(ref=BRANCH, sha=self.tip, repo=dict(full_name=REPO)),
            base=dict(ref='main', repo=dict(full_name=REPO)))
        self.branches={BRANCH:dict(protected=False, commit=dict(sha=self.tip))}

    def test_only_expected_reachable_merge(self):
        self.assertEqual(candidate(self.pr,self.head,self.branches,[],lambda _:True),self.tip)
        self.assertIsNone(candidate(self.pr,self.head,{},[],lambda _:True))
        for field,value in [('number',24),('state','open'),('merged',False),('merge_commit_sha','c'*40)]:
            with self.subTest(field=field):
                p=copy.deepcopy(self.pr); p[field]=value
                with self.assertRaises(ValueError):candidate(p,self.head,self.branches,[],lambda _:True)

    def test_moved_protected_active_or_unreachable_refused(self):
        for b in [{BRANCH:dict(protected=True,commit=dict(sha=self.tip))}, {BRANCH:dict(protected=False,commit=dict(sha='d'*40))}]:
            with self.assertRaises(ValueError):candidate(self.pr,self.head,b,[],lambda _:True)
        with self.assertRaises(ValueError):candidate(self.pr,self.head,self.branches,[self.pr],lambda _:True)
        with self.assertRaises(ValueError):candidate(self.pr,self.head,self.branches,[],lambda _:False)
        for side in ['head','base']:
            p=copy.deepcopy(self.pr);p[side]['repo']['full_name']='foreign/repo'
            with self.assertRaises(ValueError):candidate(p,self.head,self.branches,[],lambda _:True)

    def test_all_four_latest_exact_main_runs_required(self):
        runs=[dict(name=n,id=i,status='completed',conclusion='success',event='push',head_branch='main',head_sha=self.head) for i,n in enumerate(REQUIRED)]
        self.assertTrue(workflows_pass(runs,self.head))
        self.assertFalse(workflows_pass(runs[:-1],self.head))
        for key,value in [('conclusion','failure'),('status','in_progress'),('event','pull_request'),('head_sha','c'*40),('head_branch','feature')]:
            r=copy.deepcopy(runs);r[-1][key]=value
            self.assertFalse(workflows_pass(r,self.head))
        latest=dict(runs[0],id=100,conclusion='failure')
        self.assertFalse(workflows_pass(runs+[latest],self.head))

if __name__=='__main__':unittest.main()
