from __future__ import annotations

import json

from Data.Agent import security as security_module


class _FakeWin32Crypt:
    CRYPTPROTECT_LOCAL_MACHINE = 0x4

    def CryptProtectData(self, data, _description, _optional_entropy, _reserved, _prompt_struct, flags):
        prefix = b"lm:" if flags == self.CRYPTPROTECT_LOCAL_MACHINE else b"cu:"
        return ("label", prefix + bytes(data))

    def CryptUnprotectData(self, data, _description, _optional_entropy, _reserved, _prompt_struct, flags):
        expected = b"lm:" if flags == self.CRYPTPROTECT_LOCAL_MACHINE else b"cu:"
        payload = bytes(data)
        if not payload.startswith(expected):
            raise ValueError("scope mismatch")
        return ("label", payload[len(expected) :])


def test_refresh_token_storage_writes_multi_scope_dpapi_envelope(tmp_path, monkeypatch) -> None:
    monkeypatch.setattr(security_module, "IS_WINDOWS", True)
    monkeypatch.setattr(security_module, "win32crypt", _FakeWin32Crypt(), raising=False)

    store = security_module.AgentKeyStore(str(tmp_path), scope="CURRENTUSER")
    store.save_refresh_token("refresh-token")

    raw_payload = (tmp_path / "refresh.token").read_bytes()
    envelope = json.loads(raw_payload.decode("utf-8"))

    assert envelope["format"] == "dpapi-multi"
    assert set(envelope["entries"]) == {"current_user", "local_machine"}

    system_reader = security_module.AgentKeyStore(str(tmp_path), scope="SYSTEM")
    assert system_reader.load_refresh_token() == "refresh-token"


def test_refresh_token_storage_reads_legacy_single_scope_blob(tmp_path, monkeypatch) -> None:
    fake_win32 = _FakeWin32Crypt()
    monkeypatch.setattr(security_module, "IS_WINDOWS", True)
    monkeypatch.setattr(security_module, "win32crypt", fake_win32, raising=False)

    legacy_blob = fake_win32.CryptProtectData(
        b"legacy-refresh-token",
        None,
        None,
        None,
        None,
        fake_win32.CRYPTPROTECT_LOCAL_MACHINE,
    )[1]
    (tmp_path / "refresh.token").write_bytes(legacy_blob)

    store = security_module.AgentKeyStore(str(tmp_path), scope="CURRENTUSER")
    assert store.load_refresh_token() == "legacy-refresh-token"
