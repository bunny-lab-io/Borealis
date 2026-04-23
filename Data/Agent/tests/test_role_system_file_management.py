from __future__ import annotations

import asyncio
import os
from pathlib import Path

import pytest

import Data.Agent.Roles.role_system_file_management as role_module


class _FakeSio:
    def __init__(self) -> None:
        self.handlers = {}

    def on(self, event_name):
        def _decorator(handler):
            self.handlers[event_name] = handler
            return handler

        return _decorator


def _make_role(fake_sio: _FakeSio):
    ctx = type(
        "Ctx",
        (),
        {
            "sio": fake_sio,
            "agent_id": "TEST-HOST_ABCDEF01-0000-0000-0000-000000000001_SYSTEM",
            "hooks": {
                "log_agent": lambda *args, **kwargs: None,
            },
        },
    )()
    return role_module.Role(ctx)


def test_file_management_children_marks_directory_symlink_non_expandable(tmp_path: Path) -> None:
    fake_sio = _FakeSio()
    role = _make_role(fake_sio)
    role.register_events()
    handler = fake_sio.handlers["file_management_request"]

    folder = tmp_path / "folder"
    folder.mkdir()
    nested = folder / "nested.txt"
    nested.write_text("hello", encoding="utf-8")
    plain_file = tmp_path / "plain.txt"
    plain_file.write_text("world", encoding="utf-8")

    if hasattr(os, "symlink"):
        link_path = tmp_path / "folder-link"
        try:
            os.symlink(str(folder), str(link_path), target_is_directory=True)
        except OSError:
            link_path = None
    else:
        link_path = None

    async def _run_test():
        response = await handler({"action": "children", "path": str(tmp_path)})
        assert response["ok"] is True
        entries = {entry["name"]: entry for entry in response["entries"]}
        assert entries["folder"]["kind"] == "directory"
        assert entries["folder"]["has_children"] is True
        assert entries["plain.txt"]["kind"] == "file"
        if link_path is not None:
            assert entries["folder-link"]["kind"] == "symlink"
            assert entries["folder-link"]["has_children"] is False

    asyncio.run(_run_test())


def test_file_management_children_does_not_probe_nested_directories(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    fake_sio = _FakeSio()
    role = _make_role(fake_sio)
    role.register_events()
    handler = fake_sio.handlers["file_management_request"]

    folder = tmp_path / "folder"
    folder.mkdir()
    (folder / "nested.txt").write_text("hello", encoding="utf-8")
    (tmp_path / "plain.txt").write_text("world", encoding="utf-8")

    original_scandir = role_module.os.scandir

    def _guarded_scandir(path):
        if os.fspath(path) == os.fspath(folder):
            raise AssertionError("children listing should not recurse into nested directories")
        return original_scandir(path)

    monkeypatch.setattr(role_module.os, "scandir", _guarded_scandir)

    async def _run_test():
        response = await handler({"action": "children", "path": str(tmp_path)})
        assert response["ok"] is True
        entries = {entry["name"]: entry for entry in response["entries"]}
        assert entries["folder"]["kind"] == "directory"
        assert entries["folder"]["has_children"] is True
        assert entries["plain.txt"]["kind"] == "file"

    asyncio.run(_run_test())


def test_file_management_handler_rejects_root_rename() -> None:
    fake_sio = _FakeSio()
    role = _make_role(fake_sio)
    role.register_events()
    handler = fake_sio.handlers["file_management_request"]

    if role_module.IS_WINDOWS:
        roots = role_module._roots_payload()["entries"]
        if not roots:
            pytest.skip("No Windows drive roots are available in the test environment.")
        root_path = roots[0]["path"]
    else:
        root_path = "/"

    async def _run_test():
        response = await handler({"action": "rename", "path": root_path, "new_name": "renamed-root"})
        assert response["ok"] is False
        assert response["error"] == "invalid_path"

    asyncio.run(_run_test())


def test_file_management_handler_accepts_background_transfer(monkeypatch: pytest.MonkeyPatch) -> None:
    fake_sio = _FakeSio()
    role = _make_role(fake_sio)
    role.register_events()
    handler = fake_sio.handlers["file_management_request"]
    recorded = []

    async def _fake_transfer(payload):
        recorded.append(dict(payload))

    monkeypatch.setattr(role, "_run_transfer_background", _fake_transfer)

    async def _run_test():
        response = await handler({"action": "download_start", "transfer_id": "transfer-001"})
        assert response == {"ok": True, "status": "accepted", "transfer_id": "transfer-001"}
        await asyncio.sleep(0)
        assert recorded == [{"action": "download_start", "transfer_id": "transfer-001"}]

    asyncio.run(_run_test())
