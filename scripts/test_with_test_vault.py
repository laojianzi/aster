import os
from pathlib import Path
import subprocess
import unittest
from unittest.mock import patch
import with_test_vault as vault


class VaultWrapperTests(unittest.TestCase):
    def test_bus_inherits_disposable_paths_before_activation(self):
        recorded = {}
        def launch(command, env):
            self.assertEqual(command[0], "dbus-run-session")
            self.assertEqual(env["ASTER_PRIVATE_VAULT_BUS"], "1")
            self.assertNotIn("GNOME_KEYRING_CONTROL", env)
            for key in ("XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_RUNTIME_DIR"):
                path = Path(env[key])
                self.assertTrue(path.is_dir())
                self.assertEqual(path.stat().st_mode & 0o777, 0o700)
                recorded[key] = path
            self.assertEqual(len({path.parent for path in recorded.values()}), 1)
            return 23
        with patch.dict(os.environ, {"CI": "true", "GNOME_KEYRING_CONTROL": "/ambient"}, clear=True), patch.object(vault.sys, "platform", "linux"), patch.object(vault.sys, "argv", ["wrapper", "test-command"]), patch.object(vault.subprocess, "call", side_effect=launch):
            self.assertEqual(vault.main(), 23)
        self.assertTrue(all(not p.exists() for p in recorded.values()))

    def test_owner_probe_never_addresses_activation_target(self):
        with patch.object(vault.subprocess, "run", return_value=subprocess.CompletedProcess([], 0, b"(uint32 123,)\n")) as run:
            self.assertTrue(vault.provider_owned_by({}, 123))
            command = run.call_args.args[0]
            self.assertEqual(command[command.index("--dest") + 1], "org.freedesktop.DBus")
            self.assertIn("org.freedesktop.DBus.GetConnectionUnixProcessID", command)
            self.assertFalse(vault.provider_owned_by({}, 456))
        for code, output in [(1, b"(uint32 123,)"), (0, b""), (0, b"(uint32 123,) garbage"), (0, b"(123,)")]:
            with patch.object(vault.subprocess, "run", return_value=subprocess.CompletedProcess([], code, output)):
                self.assertFalse(vault.provider_owned_by({}, 123))

    def test_disposable_opt_in_is_required(self):
        with patch.dict(os.environ, {}, clear=True), patch.object(vault.sys, "argv", ["wrapper", "test-command"]):
            with self.assertRaises(SystemExit):
                vault.main()

    def test_macos_restores_default_and_search_after_test_failure(self):
        calls = []
        test_path = []
        def security(command, text=True):
            calls.append(command)
            if command[1:] == ["default-keychain", "-d", "user"]:
                return '"/original/login.keychain-db"'
            if command[1:] == ["list-keychains", "-d", "user"]:
                return '"/original/login.keychain-db"\n"/original/other.keychain-db"'
            if command[1] == "create-keychain":
                test_path.append(Path(command[-1]))
                test_path[0].touch()
            return ""
        def run_test(command, env):
            self.assertEqual(env["ASTER_TEST_KEYCHAIN_PATH"], str(test_path[0]))
            self.assertTrue(env["ASTER_TEST_KEYCHAIN_PASSWORD"])
            self.assertEqual(env["ASTER_OS_VAULT_TEST"], "1")
            raise RuntimeError("simulated test-process failure")
        with patch.dict(os.environ, {"CI": "true"}, clear=True), patch.object(vault.sys, "platform", "darwin"), patch.object(vault.sys, "argv", ["wrapper", "test-command"]), patch.object(vault.subprocess, "check_output", side_effect=security), patch.object(vault.subprocess, "call", side_effect=run_test):
            with self.assertRaises(RuntimeError):
                vault.main()
        self.assertEqual(calls[-3][1:], ["default-keychain", "-d", "user", "-s", "/original/login.keychain-db"])
        self.assertEqual(calls[-2][1:], ["list-keychains", "-d", "user", "-s", "/original/login.keychain-db", "/original/other.keychain-db"])
        self.assertEqual(calls[-1][1:], ["delete-keychain", str(test_path[0])])
        self.assertFalse(test_path[0].exists())
