#!/usr/bin/env python3
"""CI-only HTML form driver for real Dex. Not a rendered/system-browser test.

Only disposable static Dex credentials below are used. Never upload pages,
request URLs, cookies or callback query strings as artifacts.
"""
import argparse
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


def run(link, ca):
    initial = urllib.parse.urlsplit(link)
    values = urllib.parse.parse_qs(initial.query, strict_parsing=True)
    callback = urllib.parse.urlsplit(values['redirect_uri'][0])
    origin = (initial.scheme, initial.netloc)
    if initial.scheme != 'https' or callback.hostname != '127.0.0.1' or callback.scheme != 'http' or callback.path != '/oidc/callback':
        raise ValueError('untrusted fixture origins')
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect(),
        urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=ca)),
        urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
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
    args = parser.parse_args()
    try:
        run(args.url, args.ca)
    except Exception:
        # urllib exceptions embed the URL (code/state); never print them.
        sys.exit('Disposable Dex form driver failed; sensitive diagnostics suppressed')
