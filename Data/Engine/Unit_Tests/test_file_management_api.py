from __future__ import annotations

from pathlib import Path

from Data.Engine.services.API.devices import file_management as file_management_module


class _Logger:
    def debug(self, *args, **kwargs) -> None:
        return None


class _FakeStorage:
    def __init__(self, filename: str, payload: bytes) -> None:
        self.filename = filename
        self._payload = payload

    def save(self, destination) -> None:
        Path(destination).write_bytes(self._payload)


def test_guess_download_name_prefers_7z_for_archives() -> None:
    archive_name = file_management_module._guess_download_name(
        "LAB-OPERATOR-01",
        [{"path": r"C:\Temp", "name": "Temp", "kind": "directory"}],
        archive_required=True,
    )

    assert archive_name.startswith("LAB-OPERATOR-01-files-")
    assert archive_name.endswith(".7z")


def test_delete_session_removes_download_artifact_directory(tmp_path: Path) -> None:
    store = file_management_module.FileTransferStore(tmp_path / "engine_file_management", ttl_seconds=3600, logger=_Logger())
    session = store.create_download_session(
        hostname="LAB-OPERATOR-01",
        device_guid="00000000-0000-0000-0000-000000000001",
        agent_id="agent-001",
        operator_id="operator-001",
        selections=[{"path": r"C:\Temp", "name": "Temp", "kind": "directory"}],
        archive_name="LAB-OPERATOR-01-files-test.7z",
    )

    transfer_id = session["transfer_id"]
    session_dir = tmp_path / "engine_file_management" / transfer_id
    download_dir = session_dir / "download"
    download_dir.mkdir(parents=True, exist_ok=True)
    artifact_path = download_dir / "LAB-OPERATOR-01-files-test.7z"
    artifact_path.write_bytes(b"archive")

    with store._lock:
        store._sessions[transfer_id]["result_path"] = str(artifact_path)
        store._sessions[transfer_id]["result_name"] = artifact_path.name
        store._sessions[transfer_id]["session_dir"] = str(session_dir)

    store.delete_session(transfer_id)

    assert store.get_session(transfer_id) is None
    assert not session_dir.exists()


def test_rpc_error_response_maps_text_editor_failures() -> None:
    payload, status = file_management_module._rpc_error_response(
        {"ok": False, "error": "binary_not_supported", "message": "Binary files cannot be edited."}
    )

    assert status == 415
    assert payload == {"error": "binary_not_supported", "message": "Binary files cannot be edited."}


def test_rpc_error_response_maps_unsupported_agent_actions_to_update_required() -> None:
    payload, status = file_management_module._rpc_error_response(
        {"ok": False, "error": "invalid_request", "message": "Unsupported file-management action 'read_text'."}
    )

    assert status == 409
    assert payload == {
        "error": "agent_update_required",
        "message": "The device agent needs to be updated before this File Management capability is available.",
    }


def test_create_upload_session_marks_overwrite_items(tmp_path: Path) -> None:
    store = file_management_module.FileTransferStore(tmp_path / "engine_file_management", ttl_seconds=3600, logger=_Logger())

    session = store.create_upload_session(
        hostname="LAB-OPERATOR-01",
        device_guid="00000000-0000-0000-0000-000000000001",
        agent_id="agent-001",
        operator_id="operator-001",
        target_path=r"C:\Temp",
        files=[
            _FakeStorage("replace-me.txt", b"replace"),
            _FakeStorage("leave-me.txt", b"leave"),
        ],
        overwrite_names=["replace-me.txt"],
    )

    stored_session = store.get_session(session["transfer_id"])
    assert stored_session is not None
    items = {row["name"]: row for row in stored_session["upload_items"]}
    assert items["replace-me.txt"]["overwrite_existing"] is True
    assert items["leave-me.txt"]["overwrite_existing"] is False
