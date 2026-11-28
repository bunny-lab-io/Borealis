import hashlib
import importlib.util
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

    def test_exact_policy_delta(self):
        self.assertEqual((policy.ASSETS / "wg-quick.local").read_text().splitlines(), [
            "  /usr/bin/bash r,",
            "  /opt/Borealis/Engine/Services/wireguard-tunnel/config/borealis-wg.conf r,",
        ])
        source = (policy.ASSETS / "cri-containerd.original").read_bytes()
        self.assertEqual(hashlib.sha256(source.replace(policy.SIGNAL, policy.SIGNAL + policy.PEER)).hexdigest(),
                         "370f83385db24056bb9d53e1b5cfd5732a0ef8303799cd92534786e41300a43d")

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
