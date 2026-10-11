#!/usr/bin/env python3
"""CI-only HTML form driver for real Dex. Not a rendered/system-browser test.

Only disposable static Dex credentials below are used. Never upload pages,
request URLs, cookies or callback query strings as artifacts.
"""
import argparse
import copy
import os
from pathlib import Path
import stat
import tempfile
import http.cookiejar
from html.parser import HTMLParser
import ssl
import sys
import urllib.error
import urllib.parse
import urllib.request


class Forms(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.forms, self.current = [], None

    def handle_starttag(self, tag, attrs):
        a = dict(attrs)
        if tag == 'form':
            self.current = {'action': a.get('action', ''), 'fields': {}, 'method': a.get('method', 'get').lower()}
            self.forms.append(self.current)
        elif tag == 'input' and self.current is not None and a.get('name'):
            self.current['fields'][a['name']] = a.get('value', '')

    def handle_endtag(self, tag):
        if tag == 'form':
            self.current = None


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def fixture_cookies(path):
    jar = http.cookiejar.LWPCookieJar()
    if path:
        path = Path(path)
        if path.exists() or path.is_symlink():
            info = path.lstat()
            if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1 or info.st_mode & 0o077 or info.st_size > 16384:
                raise ValueError('unsafe fixture cookie file')
            jar.load(str(path), ignore_discard=True, ignore_expires=False)
    return jar


def save_fixture_cookies(jar, path):
    if not path:
        return
    path = Path(path)
    # Test harness owns a disposable private directory, never a developer's
    # browser profile or product credential store. Files are excluded from CI artifacts.
    if path.is_symlink():
        raise ValueError('unsafe fixture cookie path')
    fd, tmp = tempfile.mkstemp(prefix='dex-cookie-', dir=path.parent)
    os.close(fd)
    try:
        jar.save(tmp, ignore_discard=True, ignore_expires=False)
        if os.path.getsize(tmp) > 16384:
            raise ValueError('fixture cookie budget')
        os.replace(tmp, path)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)


def fixture_opener(ca, jar):
    return urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect(),
        urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=ca)),
        urllib.request.HTTPCookieProcessor(jar))


def logout_confirmation(page, link):
    # Pin the dedicated test driver's consent to the upstream Dex form. The
    # actual desktop never reads pages, posts this form or bypasses confirmation.
    parser = Forms()
    parser.feed(page)
    if 'Do you want to log out?' not in page or len(parser.forms) != 1:
        raise ValueError('expected active-session confirmation')
    form = parser.forms[0]
    target = urllib.parse.urljoin(link, form['action'])
    original, dest = urllib.parse.urlsplit(link), urllib.parse.urlsplit(target)
    if form['method'] != 'post' or form['fields'] or (dest.scheme, dest.netloc, dest.path) != (original.scheme, original.netloc, original.path) or dest.fragment or dest.query not in ('', original.query):
        raise ValueError('unexpected logout form; no generic consent')
    return target


def sign_out(link, ca, cookie_file):
    u = urllib.parse.urlsplit(link)
    if not cookie_file or u.scheme != 'https' or u.username or u.fragment or not u.path.endswith('/dex/logout') or urllib.parse.parse_qs(u.query, strict_parsing=True) != {'client_id': ['aster-test']}:
        raise ValueError('unexpected public sign-out target')
    jar = fixture_cookies(cookie_file)
    original_cookies = [copy.copy(cookie) for cookie in jar]
    if not original_cookies:
        raise ValueError('fixture login cookies missing')
    opener = fixture_opener(ca, jar)

    def page(target, body=None):
        req = urllib.request.Request(target, data=body)
        if body is not None:
            req.add_header('Content-Type', 'application/x-www-form-urlencoded')
        with opener.open(req, timeout=8) as response:
            data = response.read((128 << 10) + 1)
            if response.code != 200 or len(data) > 128 << 10:
                raise ValueError('unexpected logout response')
        return data.decode('utf-8')

    target = logout_confirmation(page(link), link)
    if 'You have been successfully logged out.' not in page(target, b''):
        raise ValueError('issuer did not end session')
    # Replay the prior disposable cookie as well as using the cleared jar: the
    # server must reject the old session, not just instruct this client to drop it.
    if 'No active session found.' not in page(link):
        raise ValueError('cookie session retained')
    for cookie in original_cookies:
        jar.set_cookie(cookie)
    if 'No active session found.' not in page(link):
        raise ValueError('issuer retained old session')
    save_fixture_cookies(jar, cookie_file)
    print('DEX_SIGNOUT_CONFIRMED_AND_OLD_SESSION_REJECTED')


def run(link, ca, cookie_file=None):
    initial = urllib.parse.urlsplit(link)
    values = urllib.parse.parse_qs(initial.query, strict_parsing=True)
    callback = urllib.parse.urlsplit(values['redirect_uri'][0])
    origin = (initial.scheme, initial.netloc)
    if initial.scheme != 'https' or callback.hostname != '127.0.0.1' or callback.scheme != 'http' or callback.path != '/oidc/callback':
        raise ValueError('untrusted fixture origins')
    jar = fixture_cookies(cookie_file)
    opener = fixture_opener(ca, jar)
    body = None
    for _ in range(12):
        u = urllib.parse.urlsplit(link)
        is_callback = (u.scheme, u.netloc, u.path) == (callback.scheme, callback.netloc, callback.path)
        if not is_callback and (u.scheme, u.netloc) != origin:
            raise ValueError('unreviewed browser fixture redirect')
        if is_callback and body is not None:
            raise ValueError('unexpected callback POST')
        req = urllib.request.Request(link, data=body)
        if body is not None:
            req.add_header('Content-Type', 'application/x-www-form-urlencoded')
        try:
            response = opener.open(req, timeout=8)
        except urllib.error.HTTPError as exc:
            response = exc
        with response:
            status, headers = response.code, response.headers
            data = response.read((128 << 10) + 1)
        if len(data) > 128 << 10:
            raise ValueError('fixture page exceeds budget')
        if is_callback:
            if status != 200:
                raise ValueError('callback denied')
            save_fixture_cookies(jar, cookie_file)
            return
        if status in (302, 303):
            link, body = urllib.parse.urljoin(link, headers['Location']), None
            continue
        if status != 200:
            raise ValueError('fixture provider response failed')
        parser = Forms()
        parser.feed(data.decode('utf-8'))
        selected = next((f for f in parser.forms if 'password' in f['fields'] or f['fields'].get('approval') == 'approve'), None)
        if selected is None or selected['method'] != 'post':
            raise ValueError('unknown provider form; no generic auto-approval')
        fields = selected['fields']
        if 'password' in fields:
            fields['login'], fields['password'] = 'engineer@example.test', 'password'
        link = urllib.parse.urljoin(link, selected['action'])
        body = urllib.parse.urlencode(fields).encode()
    raise ValueError('fixture redirect budget exhausted')


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--url', required=True)
    parser.add_argument('--ca', required=True)
    parser.add_argument('--cookie-file')
    parser.add_argument('--sign-out', action='store_true')
    args = parser.parse_args()
    try:
        if args.sign_out:
            sign_out(args.url, args.ca, args.cookie_file)
        else:
            run(args.url, args.ca, args.cookie_file)
    except Exception:
        # urllib exceptions embed the URL (code/state); never print them.
        sys.exit('Disposable Dex form driver failed; sensitive diagnostics suppressed')
