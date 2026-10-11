import unittest
from oidc_test_browser import Forms, logout_confirmation, fixture_cookies, save_fixture_cookies
from pathlib import Path
import tempfile
import os

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

class SignOutDriverTest(unittest.TestCase):
    def test_explicit_pinned_form_only(self):
        link = 'https://issuer.test/dex/logout?client_id=aster-test'
        form = '<div>Do you want to log out?</div><form method="post" action="/dex/logout"><button>Log Out</button></form>'
        self.assertEqual(logout_confirmation(form, link), 'https://issuer.test/dex/logout')
        for altered in [form.replace('/dex/logout', '//evil.test/dex/logout'), form.replace('post', 'get'), form.replace('/dex/logout', '/token'), form.replace('<button>', '<input name="id_token_hint" value="secret"><button>'), form + '<form method="post"></form>', 'No active session found.', form.replace('/dex/logout', '/dex/logout?next=evil')]:
            with self.assertRaises(ValueError):
                logout_confirmation(altered, link)

    def test_fixture_files_private_and_bounded(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'cookies'
            jar = fixture_cookies(path)
            save_fixture_cookies(jar, path)
            self.assertEqual(path.stat().st_mode & 0o077, 0)
            self.assertEqual(list(fixture_cookies(path)), [])
            path.write_text('x' * 16385)
            with self.assertRaises(ValueError):
                fixture_cookies(path)
            os.chmod(path, 0o644)
            with self.assertRaises(ValueError):
                fixture_cookies(path)
            path.unlink()
            path.symlink_to(Path(tmp) / 'target')
            with self.assertRaises(ValueError):
                fixture_cookies(path)
            with self.assertRaises(ValueError):
                save_fixture_cookies(jar, path)

if __name__ == '__main__':
    unittest.main()
