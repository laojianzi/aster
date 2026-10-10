import unittest
from oidc_test_browser import Forms

class FormDriverTest(unittest.TestCase):
    def test_login_hidden_and_password_fields(self):
        p = Forms()
        p.feed('<form method="post" action="/dex/auth/local"><input name="req" value="a&amp;b"><input name="login"><input name="password" type="password"></form>')
        self.assertEqual(p.forms, [{'action': '/dex/auth/local', 'method':'post', 'fields':{'req':'a&b','login':'','password':''}}])

    def test_approval_and_denial_stay_separate(self):
        p = Forms()
        p.feed('<form method="post"><input name="approval" value="approve"></form><form method="post"><input name="approval" value="rejected"></form>')
        self.assertEqual(len(p.forms), 2)
        self.assertEqual(p.forms[0]['fields']['approval'], 'approve')
        self.assertEqual(p.forms[1]['fields']['approval'], 'rejected')

    def test_outside_inputs_not_submitted(self):
        p = Forms()
        p.feed('<input name="unsafe"><form></form><input name="unsafe">')
        self.assertEqual(p.forms[0]['fields'], {})

if __name__ == '__main__':
    unittest.main()
