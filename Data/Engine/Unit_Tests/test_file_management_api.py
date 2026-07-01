from __future__ import annotations

import logging
from pathlib import Path

import pytest

from Data.Engine.services.remote_files.transfers import FileTransferStore


def test_upload_session_accepts_manifest_only_empty_file(tmp_path: Path) -> None:
    store = FileTransferStore(tmp_path / "transfers", ttl_seconds=300, logger=logging.getLogger("test"))

    snapshot = store.create_upload_session(
        hostname="LAB-ONE",
        device_guid="11111111-2222-3333-4444-555555555555",
        agent_id="LAB-ONE_SYSTEM",
        operator_id="operator",
        target_path="C:\\Temp",
        files=[],
        manifest_items=[
            {
                "client_key": "Test.ini",
                "name": "Test.ini",
                "relative_path": "Test.ini",
                "size_bytes": 0,
                "modified_at": 0,
            }
        ],
    )

    session = store.get_session(snapshot["transfer_id"])
    assert session is not None
    assert session["item_count"] == 1
    assert session["bytes_total"] == 0
    item = session["upload_items"][0]
    assert item["name"] == "Test.ini"
    assert item["size_bytes"] == 0
    assert Path(item["stored_path"]).read_bytes() == b""


def test_upload_session_rejects_manifest_only_non_empty_file(tmp_path: Path) -> None:
    store = FileTransferStore(tmp_path / "transfers", ttl_seconds=300, logger=logging.getLogger("test"))

    with pytest.raises(ValueError, match="upload_manifest_mismatch"):
        store.create_upload_session(
            hostname="LAB-ONE",
            device_guid="11111111-2222-3333-4444-555555555555",
            agent_id="LAB-ONE_SYSTEM",
            operator_id="operator",
            target_path="C:\\Temp",
            files=[],
            manifest_items=[
                {
                    "client_key": "payload.txt",
                    "name": "payload.txt",
                    "relative_path": "payload.txt",
                    "size_bytes": 12,
                    "modified_at": 0,
                }
            ],
        )
