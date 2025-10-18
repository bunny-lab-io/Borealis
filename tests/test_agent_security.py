
from __future__ import annotations

import shutil
import tempfile
import unittest

import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

try:
    from Data.Agent.security import AgentKeyStore  # type: ignore
    _IMPORT_ERROR: Exception | None = None
except Exception as exc:  # pragma: no cover - handled via skip
    AgentKeyStore = None  # type: ignore
    _IMPORT_ERROR = exc


@unittest.skipIf(AgentKeyStore is None, f"security module unavailable: {_IMPORT_ERROR}")
class AgentKeyStoreTests(unittest.TestCase):
    def test_roundtrip(self):
        tmp_dir = tempfile.mkdtemp(prefix="akstest-")
        try:
            store = AgentKeyStore(tmp_dir, scope="CURRENTUSER")
            identity = store.load_or_create_identity()

            self.assertTrue(identity.public_key_b64)
            self.assertEqual(len(identity.fingerprint), 64)

            store.save_guid("ABC-123")
            self.assertEqual(store.load_guid(), "ABC-123")

            store.save_access_token("access-token", expires_at=12345)
            self.assertEqual(store.load_access_token(), "access-token")
            self.assertEqual(store.get_access_expiry(), 12345)

            store.save_refresh_token("refresh-token")
            self.assertEqual(store.load_refresh_token(), "refresh-token")

            store.set_access_binding(identity.fingerprint)
            self.assertEqual(store.get_access_binding(), identity.fingerprint)

            store.save_server_certificate("-----BEGIN CERT-----\nABC\n-----END CERT-----")
            self.assertIn("BEGIN CERT", store.load_server_certificate() or "")

            store.save_server_signing_key("PUBKEYDATA")
            self.assertEqual(store.load_server_signing_key(), "PUBKEYDATA")
        finally:
            shutil.rmtree(tmp_dir, ignore_errors=True)


if __name__ == "__main__":
    unittest.main()
