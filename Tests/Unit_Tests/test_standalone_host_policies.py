import hashlib
import importlib.util
import os
import re
import subprocess
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import yaml


ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location(
    "host_policy", ROOT / "Data/Engine/K3s/apparmor/ensure_profiles.py")
policy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(policy)


class StandaloneHostTests(unittest.TestCase):
    def test_namespace_order_on_clean_server(self):
        available = {"borealis", "default", "kube-system", "longhorn-system"}
        manifest = ROOT / "Data/Engine/K3s/cluster/controller.yaml"
        for obj in yaml.safe_load_all(manifest.read_text()):
            meta = obj["metadata"]
            if obj["kind"] == "Namespace":
                available.add(meta["name"])
            elif "namespace" in meta:
                self.assertIn(meta["namespace"], available)

    def test_acme_store_fresh_existing_and_linked(self):
        with tempfile.TemporaryDirectory() as tmp:
            state = Path(tmp) / "Services/traefik-edge/state"
            state.mkdir(parents=True)
            store = state / "acme.json"
            def prepare():
                return subprocess.run(
                    ["bash", "-c", 'source "$1/Engine.sh"; RUNTIME_ROOT=$2; ensure_traefik_acme_storage "$3" "$4"',
                     "bash", str(ROOT), tmp, str(os.getuid()), str(os.getgid())],
                    env={**os.environ, "BOREALIS_ENGINE_LIBRARY_MODE": "1"}, capture_output=True)
            self.assertEqual(prepare().returncode, 0)
            self.assertEqual(store.stat().st_mode & 0o777, 0o600)
            store.write_bytes(b"existing fixture")
            store.chmod(0o640)
            self.assertEqual(prepare().returncode, 0)
            self.assertEqual(store.read_bytes(), b"existing fixture")
            self.assertEqual(store.stat().st_mode & 0o777, 0o600)
            other = state / "other"
            store.rename(other)
            other.chmod(0o640)
            store.symlink_to(other)
            self.assertNotEqual(prepare().returncode, 0)
            store.unlink()
            os.link(other, store)
            self.assertNotEqual(prepare().returncode, 0)
            self.assertEqual(other.stat().st_mode & 0o777, 0o640)

    def test_exact_policy_delta(self):
        self.assertEqual((policy.ASSETS / "wg-quick.local").read_text().splitlines(), [
            "  /usr/bin/bash r,",
            "  /opt/Borealis/Engine/Services/wireguard-tunnel/config/borealis-wg.conf r,",
        ])
        source = (policy.ASSETS / "cri-containerd.original").read_bytes()
        self.assertEqual(hashlib.sha256(source.replace(policy.SIGNAL, policy.SIGNAL + policy.PEER)).hexdigest(),
                         "370f83385db24056bb9d53e1b5cfd5732a0ef8303799cd92534786e41300a43d")

    def test_projected_key_allows_only_the_numeric_generation_and_private_key(self):
        rule = policy.projected_wireguard_key_rule().decode()
        prefix = "  /opt/Borealis/Engine/Services/wireguard-tunnel/secrets/.."
        self.assertTrue(rule.startswith(prefix))
        self.assertTrue(rule.endswith("/server_private.key r,\n"))
        stamp, suffix = rule[len(prefix):-len("/server_private.key r,\n")].split(".")
        self.assertEqual(stamp.split("_"), ["[0-9]" * width for width in (4, 2, 2, 2, 2, 2)])
        self.assertEqual(suffix, "{" + ",".join("[0-9]" * width for width in range(1, 11)) + "}")
        generation = re.compile(stamp + r"\.(?:" + "|".join(suffix[1:-1].split(",")) + ")")
        for ending in ("0", "3789378713"):
            self.assertIsNotNone(generation.fullmatch("2026_11_28_06_17_08." + ending))
        for ending in ("", "1x", "12345678901", "2/other"):
            self.assertIsNone(generation.fullmatch("2026_11_28_06_17_08." + ending))

    def test_existing_projected_policy_gains_only_the_standalone_key_read(self):
        prior = policy.projected_wireguard_key_rule()
        rule = b"  /opt/Borealis/Engine/Services/wireguard-tunnel/secrets/server_private.key r,\n"
        self.assertEqual(policy.WG_STANDALONE_KEY, rule)
        combined = prior + rule
        digest = hashlib.sha256(combined).hexdigest()
        for fail_reload in (False, True):
            with self.subTest(fail_reload=fail_reload), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                include = root / "local"
                include.write_bytes(prior)
                evidence = root / "wg"
                evidence.mkdir()
                (evidence / "compiled.original").write_bytes(b"vendor")
                reloads = [None] if not fail_reload else [RuntimeError("reload failed"), None]
                with patch.object(policy, "EVIDENCE", root), \
                        patch.object(policy, "WG_PROJECTED_HASH", hashlib.sha256(prior).hexdigest()), \
                        patch.object(policy, "compiled", side_effect=lambda _: include.read_bytes()), \
                        patch.object(policy, "verify_kernel") as verify, \
                        patch.object(policy.subprocess, "run", side_effect=reloads):
                    args = ("wg", include, root / "vendor", root / "vendor", combined,
                            hashlib.sha256(b"vendor").hexdigest(), digest, 0)
                    if fail_reload:
                        with self.assertRaisesRegex(RuntimeError, "reload failed"):
                            policy.reconcile(*args)
                    else:
                        policy.reconcile(*args)
                        policy.reconcile(*args)
                    expected = prior if fail_reload else combined
                    self.assertEqual(include.read_bytes(), expected)
                    verify.assert_any_call("wg", expected, 0)
                    self.assertEqual((evidence / "local.projected").read_bytes(), prior)
                    self.assertIn(b"+" + rule, (evidence / "standalone.diff").read_bytes())

    def test_conflict_and_failed_verification_preserve_policy(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            vendor, target = root / "vendor", root / "local"
            vendor.write_bytes(b"vendor")
            target.write_bytes(b"administrator")
            digest = hashlib.sha256(b"compiled").hexdigest()
            args = ("wg-quick", target, vendor, vendor, b"addition", digest, digest, 3)
            with self.assertRaisesRegex(RuntimeError, "administrator"):
                policy.reconcile(*args)
            self.assertEqual(target.read_bytes(), b"administrator")
            target.unlink()
            with patch.object(policy, "EVIDENCE", root), patch.object(policy, "compiled", return_value=b"compiled"), \
                    patch.object(policy, "verify_kernel", side_effect=[None, RuntimeError("different")]), \
                    patch.object(policy.subprocess, "run") as reload:
                with self.assertRaisesRegex(RuntimeError, "different"):
                    policy.reconcile(*args)
            self.assertFalse(target.exists())
            self.assertEqual(reload.call_count, 2)
            self.assertEqual((root / "wg-quick/vendor.original").read_bytes(), b"vendor")
