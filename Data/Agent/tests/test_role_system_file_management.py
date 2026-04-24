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


def test_file_management_text_editor_roundtrip_preserves_file_permissions(tmp_path: Path) -> None:
    fake_sio = _FakeSio()
    role = _make_role(fake_sio)
    role.register_events()
    handler = fake_sio.handlers["file_management_request"]

    file_path = tmp_path / "sample.ps1"
    file_path.write_text("Write-Host 'hello'\r\n", encoding="utf-8")
    if hasattr(os, "chmod"):
        os.chmod(file_path, 0o640)

    async def _run_test():
        read_response = await handler({"action": "read_text", "path": str(file_path)})
        assert read_response["ok"] is True
        assert read_response["encoding"].lower() in {"utf-8", "utf-8-sig"}
        assert read_response["line_ending"] == "crlf"
        assert read_response["content"] == "Write-Host 'hello'\r\n"

        before_mode = os.stat(file_path).st_mode
        write_response = await handler(
            {
                "action": "write_text",
                "path": str(file_path),
                "content": "Write-Host 'updated'\nWrite-Host 'again'\n",
                "encoding": read_response["encoding"],
                "line_ending": read_response["line_ending"],
            }
        )
        assert write_response["ok"] is True
        assert file_path.read_text(encoding=read_response["encoding"]).splitlines() == [
            "Write-Host 'updated'",
            "Write-Host 'again'",
        ]
        assert b"\r\n" in file_path.read_bytes()
        assert os.stat(file_path).st_mode == before_mode

    asyncio.run(_run_test())


def test_file_management_text_editor_rejects_binary_files(tmp_path: Path) -> None:
    fake_sio = _FakeSio()
    role = _make_role(fake_sio)
    role.register_events()
    handler = fake_sio.handlers["file_management_request"]

    file_path = tmp_path / "sample.bin"
    file_path.write_bytes(b"\x00\x01\x02\x03binary")

    async def _run_test():
        response = await handler({"action": "read_text", "path": str(file_path)})
        assert response["ok"] is False
        assert response["error"] == "binary_not_supported"

    asyncio.run(_run_test())


def test_file_management_upload_conflicts_reports_existing_destination_metadata(tmp_path: Path) -> None:
    fake_sio = _FakeSio()
    role = _make_role(fake_sio)
    role.register_events()
    handler = fake_sio.handlers["file_management_request"]

    existing_file = tmp_path / "report.log"
    existing_file.write_text("existing", encoding="utf-8")

    async def _run_test():
        response = await handler(
            {
                "action": "upload_conflicts",
                "target_path": str(tmp_path),
                "items": [
                    {"name": "report.log", "size_bytes": 321, "modified_at": 1700000000},
                    {"name": "fresh.log", "size_bytes": 123, "modified_at": 1700000001},
                ],
            }
        )
        assert response["ok"] is True
        assert len(response["conflicts"]) == 1
        conflict = response["conflicts"][0]
        assert conflict["name"] == "report.log"
        assert conflict["upload_size_bytes"] == 321
        assert conflict["destination"]["path"] == str(existing_file)
        assert conflict["destination"]["kind"] == "file"
        assert conflict["replace_supported"] is True

    asyncio.run(_run_test())


def test_file_management_upload_conflicts_supports_nested_relative_paths(tmp_path: Path) -> None:
    fake_sio = _FakeSio()
    role = _make_role(fake_sio)
    role.register_events()
    handler = fake_sio.handlers["file_management_request"]

    nested_dir = tmp_path / "Folder" / "Sub"
    nested_dir.mkdir(parents=True)
    existing_file = nested_dir / "report.log"
    existing_file.write_text("existing", encoding="utf-8")

    async def _run_test():
        response = await handler(
            {
                "action": "upload_conflicts",
                "target_path": str(tmp_path),
                "items": [
                    {
                        "client_key": "Folder/Sub/report.log",
                        "name": "report.log",
                        "relative_path": "Folder/Sub/report.log",
                        "size_bytes": 321,
                        "modified_at": 1700000000,
                    }
                ],
            }
        )
        assert response["ok"] is True
        assert len(response["conflicts"]) == 1
        conflict = response["conflicts"][0]
        assert conflict["client_key"] == "Folder/Sub/report.log"
        assert conflict["relative_path"] == "Folder/Sub/report.log"
        assert conflict["display_name"] == "Folder/Sub/report.log"
        assert conflict["destination"]["path"] == str(existing_file)

    asyncio.run(_run_test())


def test_file_management_paste_copy_creates_copy_name_for_same_directory(tmp_path: Path) -> None:
    fake_sio = _FakeSio()
    role = _make_role(fake_sio)
    role.register_events()
    handler = fake_sio.handlers["file_management_request"]

    source_file = tmp_path / "report.txt"
    source_file.write_text("hello", encoding="utf-8")

    async def _run_test():
        response = await handler(
            {
                "action": "paste",
                "operation": "copy",
                "destination_path": str(tmp_path),
                "paths": [{"path": str(source_file), "name": source_file.name, "kind": "file"}],
            }
        )
        assert response["ok"] is True
        copied = tmp_path / "report - Copy.txt"
        assert copied.is_file()
        assert copied.read_text(encoding="utf-8") == "hello"

    asyncio.run(_run_test())


def test_file_management_paste_cut_moves_file(tmp_path: Path) -> None:
    fake_sio = _FakeSio()
    role = _make_role(fake_sio)
    role.register_events()
    handler = fake_sio.handlers["file_management_request"]

    source_dir = tmp_path / "source"
    source_dir.mkdir()
    destination_dir = tmp_path / "destination"
    destination_dir.mkdir()
    source_file = source_dir / "move-me.txt"
    source_file.write_text("payload", encoding="utf-8")

    async def _run_test():
        response = await handler(
            {
                "action": "paste",
                "operation": "cut",
                "destination_path": str(destination_dir),
                "paths": [{"path": str(source_file), "name": source_file.name, "kind": "file"}],
            }
        )
        assert response["ok"] is True
        assert not source_file.exists()
        moved_file = destination_dir / "move-me.txt"
        assert moved_file.is_file()
        assert moved_file.read_text(encoding="utf-8") == "payload"

    asyncio.run(_run_test())


def test_file_management_download_prefers_7zip_archive_when_available(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    role = _make_role(_FakeSio())
    role._temp_root = tmp_path / "transfers"
    role._temp_root.mkdir(parents=True, exist_ok=True)

    source_dir = tmp_path / "source"
    source_dir.mkdir()
    (source_dir / "payload.txt").write_text("hello world", encoding="utf-8")

    reported = []
    uploaded = {}

    monkeypatch.setattr(role, "_find_7zip_executable", lambda: "/usr/bin/7zz")

    def _fake_report_progress(*args, **kwargs):
        reported.append({"args": args, "kwargs": kwargs})

    def _fake_build_7zip_selection(selections, archive_path, seven_zip_exe, transfer_id=""):
        assert seven_zip_exe == "/usr/bin/7zz"
        assert transfer_id == "transfer-7z"
        Path(archive_path).write_bytes(b"7zdata")
        return 6

    def _fake_post_download_artifact(*, transfer_id, artifact_path, artifact_name, mime_type):
        uploaded["transfer_id"] = transfer_id
        uploaded["artifact_path"] = artifact_path
        uploaded["artifact_name"] = artifact_name
        uploaded["mime_type"] = mime_type
        assert Path(artifact_path).is_file()

    monkeypatch.setattr(role, "_report_progress", _fake_report_progress)
    monkeypatch.setattr(role, "_build_7zip_selection", _fake_build_7zip_selection)
    monkeypatch.setattr(role, "_post_download_artifact", _fake_post_download_artifact)

    role._download_transfer_worker(
        {
            "transfer_id": "transfer-7z",
            "archive_required": True,
            "archive_name": "host-files.zip",
            "items": [{"path": str(source_dir), "name": source_dir.name, "kind": "directory"}],
        }
    )

    assert uploaded["transfer_id"] == "transfer-7z"
    assert uploaded["artifact_name"].endswith(".7z")
    assert uploaded["mime_type"] == "application/x-7z-compressed"
    assert reported[0]["kwargs"]["archive_name"].endswith(".7z")
    assert not Path(uploaded["artifact_path"]).exists()


def test_file_management_build_7zip_selection_uses_popen_pipes(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    role = _make_role(_FakeSio())
    source_dir = tmp_path / "source"
    source_dir.mkdir()
    payload = source_dir / "payload.txt"
    payload.write_text("hello world", encoding="utf-8")
    archive_path = tmp_path / "download.7z"

    popen_calls = []

    class _FakePopen:
        def __init__(self, command, **kwargs) -> None:
            popen_calls.append({"command": command, "kwargs": kwargs})
            assert "capture_output" not in kwargs
            assert kwargs["stdout"] is role_module.subprocess.PIPE
            assert kwargs["stderr"] is role_module.subprocess.PIPE
            assert kwargs["text"] is True
            assert kwargs["cwd"] == str(source_dir.parent)
            self.returncode = 0
            archive_path.write_bytes(b"7zdata")

        def poll(self):
            return 0

        def communicate(self):
            return ("", "")

    monkeypatch.setattr(role_module.subprocess, "Popen", _FakePopen)

    total_bytes = role._build_7zip_selection(
        [{"path": str(source_dir), "name": source_dir.name, "kind": "directory"}],
        str(archive_path),
        "/usr/bin/7zz",
        transfer_id="transfer-7z",
    )

    assert total_bytes == 6
    assert len(popen_calls) == 1
