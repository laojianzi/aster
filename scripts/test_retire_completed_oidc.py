import copy
import unittest
from retire_completed_oidc import candidate, workflows_pass, REQUIRED, REPO, BRANCH, select_scope, APPROVED

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

    def test_registered_renewal_scope_is_explicit_and_leased(self):
        branch=APPROVED[28]
        p=copy.deepcopy(self.pr);p['number']=28;p['head']['ref']=branch
        refs={branch:dict(protected=False,commit=dict(sha=self.tip))}
        self.assertEqual(select_scope([p],self.head),28)
        self.assertEqual(candidate(p,self.head,refs,[],lambda _:True,number=28,branch=branch),self.tip)
        with self.assertRaises(ValueError):candidate(p,self.head,refs,[],lambda _:True)
        with self.assertRaises(ValueError):candidate(p,self.head,refs,[],lambda _:True,number=999,branch=branch)
        with self.assertRaises(ValueError):candidate(p,self.head,refs,[],lambda _:True,number=25,branch=branch)
        with self.assertRaises(ValueError):candidate(p,self.head,refs,[p],lambda _:True,number=28,branch=branch)
        refs[branch]['commit']['sha']='d'*40
        with self.assertRaises(ValueError):candidate(p,self.head,refs,[],lambda _:True,number=28,branch=branch)

    def test_registered_diagnostic_scope_preserves_retirement_guards(self):
        branch=APPROVED[30]
        self.assertEqual(branch,'feat/native-diagnostic-navigation')
        p=copy.deepcopy(self.pr);p['number']=30;p['head']['ref']=branch
        refs={branch:dict(protected=False,commit=dict(sha=self.tip))}
        self.assertEqual(select_scope([p],self.head),30)
        self.assertEqual(candidate(p,self.head,refs,[],lambda _:True,number=30,branch=branch),self.tip)
        with self.assertRaises(ValueError):candidate(p,self.head,refs,[],lambda _:True,number=28,branch=branch)
        with self.assertRaises(ValueError):candidate(p,self.head,refs,[p],lambda _:True,number=30,branch=branch)
        with self.assertRaises(ValueError):candidate(p,self.head,refs,[],lambda _:False,number=30,branch=branch)
        refs[branch]['protected']=True
        with self.assertRaises(ValueError):candidate(p,self.head,refs,[],lambda _:True,number=30,branch=branch)
        refs[branch]['protected']=False;refs[branch]['commit']['sha']='e'*40
        with self.assertRaises(ValueError):candidate(p,self.head,refs,[],lambda _:True,number=30,branch=branch)

    def test_registered_find_scope_preserves_retirement_guards(self):
        branch=APPROVED[34]
        self.assertEqual(branch,'feat/native-manifest-find')
        p=copy.deepcopy(self.pr);p['number']=34;p['head']['ref']=branch
        refs={branch:dict(protected=False,commit=dict(sha=self.tip))}
        self.assertEqual(select_scope([p],self.head),34)
        self.assertEqual(candidate(p,self.head,refs,[],lambda _:True,number=34,branch=branch),self.tip)
        self.assertIsNone(candidate(p,self.head,{},[],lambda _:True,number=34,branch=branch))
        with self.assertRaises(ValueError):candidate(p,self.head,refs,[],lambda _:True,number=30,branch=branch)
        with self.assertRaises(ValueError):candidate(p,self.head,refs,[p],lambda _:True,number=34,branch=branch)
        with self.assertRaises(ValueError):candidate(p,self.head,refs,[],lambda _:False,number=34,branch=branch)
        refs[branch]['protected']=True
        with self.assertRaises(ValueError):candidate(p,self.head,refs,[],lambda _:True,number=34,branch=branch)
        refs[branch]['protected']=False;refs[branch]['commit']['sha']='e'*40
        with self.assertRaises(ValueError):candidate(p,self.head,refs,[],lambda _:True,number=34,branch=branch)

    def test_registered_buffer_scope_preserves_retirement_guards(self):
        number, branch = 36, APPROVED[36]
        self.assertEqual(branch, 'feat/native-manifest-buffer')
        p = copy.deepcopy(self.pr); p['number'] = number; p['head']['ref'] = branch
        refs = {branch: dict(protected=False, commit=dict(sha=self.tip))}
        self.assertEqual(select_scope([p], self.head), number)
        self.assertEqual(candidate(p, self.head, refs, [], lambda _: True, number=number, branch=branch), self.tip)
        self.assertIsNone(candidate(p, self.head, {}, [], lambda _: True, number=number, branch=branch))
        with self.assertRaises(ValueError): candidate(p, self.head, refs, [], lambda _: True, number=34, branch=branch)
        with self.assertRaises(ValueError): candidate(p, self.head, refs, [p], lambda _: True, number=number, branch=branch)
        with self.assertRaises(ValueError): candidate(p, self.head, refs, [], lambda _: False, number=number, branch=branch)
        refs[branch]['protected'] = True
        with self.assertRaises(ValueError): candidate(p, self.head, refs, [], lambda _: True, number=number, branch=branch)
        refs[branch]['protected'] = False; refs[branch]['commit']['sha'] = 'e'*40
        with self.assertRaises(ValueError): candidate(p, self.head, refs, [], lambda _: True, number=number, branch=branch)

    def test_signout_retirement_requires_its_own_completed_reachable_inactive_merge(self):
        number, branch = 38, APPROVED[38]
        self.assertEqual(branch, 'feat/native-oidc-signout')
        p = copy.deepcopy(self.pr); p['number'] = number; p['head']['ref'] = branch
        refs = {branch: dict(protected=False, commit=dict(sha=self.tip))}
        self.assertEqual(select_scope([p], self.head), number)
        self.assertEqual(candidate(p, self.head, refs, [], lambda _: True, number=number, branch=branch), self.tip)
        self.assertIsNone(candidate(p, self.head, {}, [], lambda _: True, number=number, branch=branch))
        for mode in ('open', 'unmerged', 'other-merge', 'wrong-branch', 'moved', 'protected', 'active', 'unreachable', 'other-scope'):
            with self.subTest(mode=mode):
                pr, branches = copy.deepcopy(p), copy.deepcopy(refs)
                if mode == 'open': pr['state'] = 'open'
                if mode == 'unmerged': pr['merged'] = False
                if mode == 'other-merge': pr['merge_commit_sha'] = 'e'*40
                if mode == 'wrong-branch': pr['head']['ref'] = APPROVED[28]
                if mode == 'moved': branches[branch]['commit']['sha'] = 'e'*40
                if mode == 'protected': branches[branch]['protected'] = True
                with self.assertRaises(ValueError):
                    candidate(pr, self.head, branches, [p] if mode == 'active' else [],
                              lambda _: mode != 'unreachable',
                              number=28 if mode == 'other-scope' else number, branch=branch)

    def test_mygo_patch_retirement_never_deletes_an_active_or_moved_group(self):
        for number in (39, 41):
            with self.subTest(pr=number):
                branch = APPROVED[number]
                p = copy.deepcopy(self.pr); p['number'] = number; p['head']['ref'] = branch
                refs = {branch: dict(protected=False, commit=dict(sha=self.tip))}
                self.assertEqual(select_scope([p], self.head), number)
                self.assertEqual(candidate(p, self.head, refs, [], lambda _: True, number=number, branch=branch), self.tip)
                # Older PRs used the same group name; they must never authorize
                # deletion of this newer PR's branch or select its merge.
                for older in (33, 39) if number == 41 else (33,):
                    with self.assertRaises(ValueError):
                        candidate(p, self.head, refs, [], lambda _: True, number=older, branch=branch)
                    old = copy.deepcopy(p); old['number'] = older; old['merge_commit_sha'] = 'c'*40
                    self.assertEqual(select_scope([old, p], self.head), number)
                for side in ('head', 'base'):
                    active = copy.deepcopy(p)
                    active['head']['ref'] = 'unrelated-head'
                    active['base']['ref'] = 'unrelated-base'
                    active[side]['ref'] = branch
                    with self.assertRaises(ValueError):
                        candidate(p, self.head, refs, [active], lambda _: True, number=number, branch=branch)
                with self.assertRaises(ValueError): candidate(p, self.head, refs, [], lambda _: False, number=number, branch=branch)
                refs[branch]['commit']['sha'] = 'f'*40
                with self.assertRaises(ValueError): candidate(p, self.head, refs, [], lambda _: True, number=number, branch=branch)

    def test_schema_constraints_retirement_requires_its_own_completed_inactive_merge(self):
        self.assertEqual(APPROVED[43], 'feat/native-schema-constraints')
        self.assert_completion_scope_guards(43, 42, 38)

    def test_delivery_retirement_requires_its_own_completed_inactive_merge(self):
        self.assertEqual(APPROVED[45], 'feat/reviewed-manifest-delivery')
        self.assert_completion_scope_guards(45, 44, 43)

    def test_schema_multipleof_retirement_requires_its_own_completed_inactive_merge(self):
        self.assertEqual(APPROVED[47], 'feat/native-schema-multipleof')
        self.assert_completion_scope_guards(47, 46, 45)

    def assert_completion_scope_guards(self, number, issue, previous):
        from retire_completed_oidc import DEPENDENCY_SCOPES
        branch = APPROVED[number]
        self.assertNotIn(issue, APPROVED, 'the child Issue is not the implementation PR')
        self.assertNotIn(number, DEPENDENCY_SCOPES)
        p = copy.deepcopy(self.pr); p['number'] = number; p['head']['ref'] = branch
        refs = {branch: dict(protected=False, commit=dict(sha=self.tip))}
        self.assertEqual(select_scope([p], self.head), number)
        self.assertEqual(candidate(p, self.head, refs, [], lambda _: True, number=number, branch=branch), self.tip)
        self.assertIsNone(candidate(p, self.head, {}, [], lambda _: True, number=number, branch=branch))
        for other in (previous, issue):
            with self.subTest(other_scope=other), self.assertRaises(ValueError):
                candidate(p, self.head, refs, [], lambda _: True, number=other, branch=branch)
        for mode in ('open', 'unmerged', 'other-merge', 'wrong-branch', 'wrong-base',
                     'foreign-head', 'foreign-base', 'invalid-tip', 'moved', 'protected', 'unreachable'):
            with self.subTest(mode=mode):
                pr, branches = copy.deepcopy(p), copy.deepcopy(refs)
                if mode == 'open': pr['state'] = 'open'
                if mode == 'unmerged': pr['merged'] = False
                if mode == 'other-merge': pr['merge_commit_sha'] = 'e'*40
                if mode == 'wrong-branch': pr['head']['ref'] = APPROVED[previous]
                if mode == 'wrong-base': pr['base']['ref'] = 'other-main'
                if mode == 'foreign-head': pr['head']['repo']['full_name'] = 'foreign/repo'
                if mode == 'foreign-base': pr['base']['repo']['full_name'] = 'foreign/repo'
                if mode == 'invalid-tip': pr['head']['sha'] = 'invalid'
                if mode == 'moved': branches[branch]['commit']['sha'] = 'e'*40
                if mode == 'protected': branches[branch]['protected'] = True
                with self.assertRaises(ValueError):
                    candidate(pr, self.head, branches, [], lambda _: mode != 'unreachable',
                              number=number, branch=branch)
        for side in ('head', 'base'):
            with self.subTest(active_side=side):
                active = copy.deepcopy(p)
                active.update(number=999, state='open', merged=False)
                active['head']['ref'] = 'unrelated-head'
                active['base']['ref'] = 'unrelated-base'
                active[side]['ref'] = branch
                with self.assertRaises(ValueError):
                    candidate(p, self.head, refs, [active], lambda _: True, number=number, branch=branch)
        runs = [dict(name=n, id=i, event='push', head_branch='main', head_sha=self.head,
                     status='completed', conclusion='success') for i, n in enumerate(REQUIRED)]
        self.assertTrue(workflows_pass(runs, self.head))
        for missing in REQUIRED:
            with self.subTest(missing_gate=missing):
                self.assertFalse(workflows_pass([r for r in runs if r['name'] != missing], self.head))

    def test_unregistered_open_old_or_ambiguous_merges_do_not_select(self):
        self.assertIsNone(select_scope([],self.head))
        self.assertEqual(select_scope([self.pr],self.head),25)
        for field,value in [('number',99),('state','open'),('merged',False),('merge_commit_sha','e'*40)]:
            p=copy.deepcopy(self.pr);p[field]=value
            self.assertIsNone(select_scope([p],self.head))
        other=copy.deepcopy(self.pr);other['number']=28
        with self.assertRaises(ValueError):select_scope([self.pr,other],self.head)

if __name__=='__main__':unittest.main()
