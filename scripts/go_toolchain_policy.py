"""Exact Go 1.27.3 admission; unrelated bytes remain frozen by earlier scopes.

The reviewed patch is CVE-2026-111943 (golang/go 90617fb7). This is not a
normalizer for arbitrary future upgrades: each current file must first match
its full reviewed digest, then only its exact version token is reversed.

Reversibility is retired for go.mod by the mygo v0.7.3 migration. The edit that
moved the toolchain token also moved the mygo requirement, so reversing the
token alone can no longer reproduce the pre-1.27.3 file. `PINS` records None in
that previous-digest slot: admission still fails closed on any byte outside the
reviewed digest, but no reconstruction is offered and asking for one raises.
The retired previous digest was 2462ac2ec5f888077ed86aa4561b747272b710719c1623ab8028659e8c6fd2a8.
A two-token reversal (toolchain plus the single mygo requirement line) was
measured to reproduce that digest exactly on 2026-11-06, so the image is not
lost; this scope keeps the declaration instead of adding a migration-specific
substitution rule to the admission path. Rebuilding a provable image is the job
of the commit that lands the migration.

The same day the mygo requirement moved once more, v0.7.3 to v0.7.4, so the
reviewed digest was re-set again with no change to the None slot: a dependency
bump no longer promises any reconstructible pre-image here.

On 2026-11-07 ci.yml became the second file with a retired reversible slot.
It needed non-version edits: the Linux display/font dependency step ran after
the tests that use it, and the enlarged UI test package measured 296.1s
against a 300s package deadline. Reversing four version tokens cannot undo a
moved step or a changed deadline, so no pre-1.27.3 image is claimed. Its
reviewed digest remains the whole admission gate; the retired image was
95b5bbeb09c72d72a348d4dec47f955e42cbee29342432a38cda98b1ff804bc4.
"""
import hashlib
import json
import os
from pathlib import Path
import subprocess

VERSION = 'go1.27.3'
# path: (version-token count, previous SHA-256 or None, reviewed SHA-256)
# A None previous digest means "admitted but not reversible"; see the module
# docstring. go.mod carries it for the v0.7.x migrations, ci.yml for the
# 2026-11-07 deadline/dependency-order fix; both are still digest-gated.
PINS = {'go.mod': (1,
            None,
            '75f65e223ff31075f82184c4093f177ece4e1f2a6f599cf4cd28aeb36c3fafc8'),
 '.github/workflows/ci.yml': (4,
                              None,
                              'd98439cacba9b68988655689a6cc06380d0ab249ed1d38110a9a3fc9ca4fb4f4'),
 '.github/workflows/native-migration.yml': (2,
                                            '7f0d5609c7e7b674e37236b21a338bdcdb95253eaa92112e54d128d8a9c3af06',
                                            '899e1b524b26ab3396b65e4d2acc1788575ffd4b7576f6976a62bccc9289cb33'),
 '.github/workflows/offline-devkit.yml': (1,
                                          'affa7232b7bdca142678d3c67b86a8d354c181522f72dc866518e78bbfe32b3c',
                                          '12fdf79c9baef63ec1e0e8666dbf297f17ab7b421679f4479d65a62a95c2e405'),
 '.github/workflows/oidc-qualification.yml': (1,
                                              'dbd09f1223f11683dbaa1f59f057dab08e9ce04a5ddc0a16b921b5b3082b059a',
                                              'f808d03d07563ae4ac7004b7b644820c9e5ecf203154b896fbb3fbc424b4f7b0'),
 '.github/workflows/source-evidence.yml': (1,
                                           'b0d4cde5750f15a46a4c749d1edc656bd68f6c7d11cfcd83296fa5fd27f31453',
                                           '6e1c5b4508b9cf184d16a03347f3f29e1c970599ff5ba52a064a57dfb855d072'),
 '.github/workflows/terminal-devkit.yml': (1,
                                           '30d6e59f0763c566ae548095dfa644039000b69b2c1a4f0f52e1ac6923298a99',
                                           '5502c443df4dd085b547f4c0cc00c4814e36ee15b041156d35ea40e25ab6d0d0')}

# Git blob identities independently read from the official go1.27.3 tag.
X509_BLOBS = {
    'src/crypto/x509/verify.go': '6485ce684d7ba26a3fb40870e319d9244c367077',
    'src/crypto/x509/root_windows.go': '3be0c7839a1afe995137d59bc178ee641670ded5',
    'src/crypto/x509/root_darwin.go': '509e5c11ae4f9320febf089f2716c82647fc2cd1',
}


def _version_tokens(path):
    """The single 1.27.2 -> 1.27.3 substitution this policy can undo."""
    return (b'go 1.27.2\n', b'go 1.27.3\n') if path == 'go.mod' else (
        b"go-version: '1.27.2'", b"go-version: '1.27.3'")


def verify_admitted(path, data):
    """Fail closed unless `data` is the reviewed image of a pinned file."""
    if path not in PINS:
        return data
    count, _, reviewed = PINS[path]
    if hashlib.sha256(data).hexdigest() != reviewed:
        raise ValueError('unreviewed Go toolchain file: ' + path)
    old, new = _version_tokens(path)
    if data.count(new) != count or old in data:
        raise ValueError('unexpected Go toolchain pin count: ' + path)
    return data


def previous_toolchain_bytes(path, data):
    """Return the old scope's bytes only for an exact reviewed replacement."""
    data = verify_admitted(path, data)
    if path not in PINS:
        return data
    previous = PINS[path][1]
    if previous is None:
        raise ValueError('no reversible pre-1.27.3 image on record: ' + path)
    old, new = _version_tokens(path)
    restored = data.replace(new, old)
    if hashlib.sha256(restored).hexdigest() != previous:
        raise ValueError('non-version change in Go toolchain file: ' + path)
    return restored


def verify_toolchain(root, version):
    """Check the active compiler identity and all three upstream x509 paths."""
    if version != VERSION:
        raise ValueError('the active Go toolchain is not the reviewed version')
    root = Path(root)
    if (root / 'VERSION').read_text().splitlines()[0] != VERSION:
        raise ValueError('Go VERSION does not match the active compiler')
    checked = {}
    for path, expected in X509_BLOBS.items():
        data = (root / path).read_bytes()
        blob = hashlib.sha1(b'blob ' + str(len(data)).encode() + b'\0' + data).hexdigest()
        if blob != expected:
            raise ValueError('unreviewed Go x509 source: ' + path)
        checked[path] = {'git_blob': blob, 'sha256': hashlib.sha256(data).hexdigest()}
    return {'version': version, 'x509_sources': checked}


def installed_toolchain():
    # Never download an implicit alternate compiler while qualifying this one.
    result = subprocess.run(['go', 'env', '-json', 'GOROOT', 'GOVERSION'],
                            env=dict(os.environ, GOTOOLCHAIN='local'),
                            capture_output=True, text=True, check=True, timeout=20)
    env = json.loads(result.stdout)
    return verify_toolchain(env['GOROOT'], env['GOVERSION'])


if __name__ == '__main__':
    print(json.dumps(installed_toolchain(), sort_keys=True, indent=2))
