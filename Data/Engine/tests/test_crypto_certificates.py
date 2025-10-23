from __future__ import annotations

import importlib
import os
import shutil
import ssl
import sys
import tempfile
import unittest
from pathlib import Path

from Data.Engine import runtime


class CertificateGenerationTests(unittest.TestCase):
    def setUp(self) -> None:
        self._tmpdir = Path(tempfile.mkdtemp(prefix="engine-cert-tests-"))
        self.addCleanup(lambda: shutil.rmtree(self._tmpdir, ignore_errors=True))

        self._previous_env: dict[str, str | None] = {}
        for name in ("BOREALIS_CERTIFICATES_ROOT", "BOREALIS_SERVER_CERT_ROOT"):
            self._previous_env[name] = os.environ.get(name)
            os.environ[name] = str(self._tmpdir / name.lower())

        runtime.certificates_root.cache_clear()
        runtime.server_certificates_root.cache_clear()

        module_name = "Data.Engine.services.crypto.certificates"
        if module_name in sys.modules:
            del sys.modules[module_name]

        try:
            self.certificates = importlib.import_module(module_name)
        except ModuleNotFoundError as exc:  # pragma: no cover - optional deps absent
            self.skipTest(f"cryptography dependency unavailable: {exc}")

    def tearDown(self) -> None:  # pragma: no cover - environment cleanup
        for name, value in self._previous_env.items():
            if value is None:
                os.environ.pop(name, None)
            else:
                os.environ[name] = value
        runtime.certificates_root.cache_clear()
        runtime.server_certificates_root.cache_clear()

    def test_ensure_certificate_creates_material(self) -> None:
        cert_path, key_path, bundle_path = self.certificates.ensure_certificate()

        self.assertTrue(cert_path.exists(), "certificate was not generated")
        self.assertTrue(key_path.exists(), "private key was not generated")
        self.assertTrue(bundle_path.exists(), "bundle was not generated")

        context = self.certificates.build_ssl_context()
        self.assertIsInstance(context, ssl.SSLContext)
        self.assertEqual(context.minimum_version, ssl.TLSVersion.TLSv1_3)

    def test_certificate_paths_returns_strings(self) -> None:
        cert_path, key_path, bundle_path = self.certificates.certificate_paths()
        self.assertIsInstance(cert_path, str)
        self.assertIsInstance(key_path, str)
        self.assertIsInstance(bundle_path, str)


if __name__ == "__main__":  # pragma: no cover - convenience
    unittest.main()
