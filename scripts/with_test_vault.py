#!/usr/bin/env python3
"""Run explicit OS credential tests in a disposable provider, never a user's vault.

Linux: private D-Bus plus an ephemeral gnome-keyring data directory.
macOS: temporary keychain, restored search list/default on all exits.
Windows: hosted-runner logon store, random per-test keys, mandatory test cleanup.
Only test passwords (not Kubernetes tokens) are used by setup commands.
"""
from __future__ import annotations
import os
from pathlib import Path
import secrets
import re
import shlex
import signal
import subprocess
import sys
import tempfile
import time


def provider_owned_by(env: dict[str, str], pid: int) -> bool:
    """Probe without auto-starting the Secret Service or accepting another daemon."""
    result = subprocess.run(["gdbus", "call", "--session", "--dest", "org.freedesktop.DBus",
                             "--object-path", "/org/freedesktop/DBus", "--method",
                             "org.freedesktop.DBus.GetConnectionUnixProcessID", "org.freedesktop.secrets"],
                            env=env, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=3)
    match = re.fullmatch(rb"\(uint32 ([0-9]+),\)\s*", result.stdout)
    return result.returncode == 0 and match is not None and int(match.group(1)) == pid


def main() -> int:
    command = sys.argv[1:]
    if not command:
        raise SystemExit("a test command is required")
    if os.environ.get("CI") != "true" and os.environ.get("ASTER_ALLOW_OS_VAULT_TEST") != "1":
        raise SystemExit("explicit disposable-environment opt-in required")
    env = dict(os.environ, ASTER_OS_VAULT_TEST="1")
    if sys.platform.startswith("linux"):
        if env.get("ASTER_PRIVATE_VAULT_BUS") != "1":
            # Set isolation before creating the bus. A D-Bus-activated provider
            # otherwise inherits the real user's XDG paths from the bus process.
            with tempfile.TemporaryDirectory(prefix="aster-vault-") as tmp:
                env.update(XDG_DATA_HOME=tmp + "/data", XDG_CONFIG_HOME=tmp + "/config", XDG_RUNTIME_DIR=tmp + "/run")
                for key in ("XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_RUNTIME_DIR"):
                    Path(env[key]).mkdir(mode=0o700)
                env.pop("GNOME_KEYRING_CONTROL", None)
                env["ASTER_PRIVATE_VAULT_BUS"] = "1"
                return subprocess.call(["dbus-run-session", "--", sys.executable, __file__, *command], env=env)
        control = Path(env["XDG_RUNTIME_DIR"]) / "keyring"
        control.mkdir(mode=0o700, exist_ok=True)
        # --unlock creates/unlocks the *disposable* login collection from stdin.
        # The product never requests an unlock; it still fails closed when locked.
        with tempfile.TemporaryFile() as diagnostics:
            proc = subprocess.Popen(["gnome-keyring-daemon", "--foreground", "--unlock", "--components=secrets",
                                     "--control-directory", str(control)],
                                    stdin=subprocess.PIPE, stdout=subprocess.DEVNULL, stderr=diagnostics,
                                    env=env, start_new_session=True)
            try:
                assert proc.stdin is not None
                proc.stdin.write(b"aster-disposable-test-password")
                proc.stdin.close()
                deadline = time.monotonic() + 15
                while time.monotonic() < deadline:
                    if proc.poll() is not None:
                        break
                    # Query the bus, not the service: ReadAlias here used to
                    # auto-activate a competing, uninitialised daemon.
                    if provider_owned_by(env, proc.pid):
                        ready = subprocess.run(["gdbus", "call", "--session", "--dest", "org.freedesktop.secrets",
                                                "--object-path", "/org/freedesktop/secrets", "--method",
                                                "org.freedesktop.Secret.Service.ReadAlias", "default"],
                                               env=env, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=3)
                        if ready.returncode == 0 and b"/org/freedesktop/secrets/collection/" in ready.stdout:
                            return subprocess.call(command, env=env)
                    time.sleep(0.1)
                # Only setup diagnostics, before any tests/credential payloads.
                diagnostics.seek(0, 2)
                length = diagnostics.tell()
                diagnostics.seek(max(0, length - 2048))
                sys.stderr.write(diagnostics.read().decode("utf-8", "replace"))
                raise RuntimeError("disposable Secret Service not ready or owned by an unexpected process")
            finally:
                try:
                    os.killpg(proc.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
                try:
                    proc.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    os.killpg(proc.pid, signal.SIGKILL)
                    proc.wait(timeout=3)
    if sys.platform == "darwin":
        def security(*args: str) -> str:
            return subprocess.check_output(["security", *args], text=True).strip()
        original_default = shlex.split(security("default-keychain", "-d", "user"))
        original_search = shlex.split(security("list-keychains", "-d", "user"))
        with tempfile.TemporaryDirectory(prefix="aster-vault-") as tmp:
            path = tmp + "/test.keychain-db"
            password = secrets.token_urlsafe(24)
            try:
                security("create-keychain", "-p", password, path)
                security("set-keychain-settings", "-lut", "3600", path)
                security("unlock-keychain", "-p", password, path)
                security("list-keychains", "-d", "user", "-s", path)
                security("default-keychain", "-d", "user", "-s", path)
                return subprocess.call(command, env=env)
            finally:
                if original_default:
                    security("default-keychain", "-d", "user", "-s", original_default[0])
                security("list-keychains", "-d", "user", "-s", *original_search)
                if Path(path).exists():
                    security("delete-keychain", path)
    if sys.platform == "win32":
        return subprocess.call(command, env=env)
    raise SystemExit("unsupported native credential test platform")


if __name__ == "__main__":
    raise SystemExit(main())
