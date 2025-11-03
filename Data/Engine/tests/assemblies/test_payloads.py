# ======================================================
# Data\Engine\tests\assemblies\test_payloads.py
# Description: Exercises PayloadManager storage, mirroring, and deletion behaviours.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import base64
from pathlib import Path

from Data.Engine.assembly_management.models import PayloadType
from Data.Engine.assembly_management.payloads import PayloadManager


def test_payload_manager_store_update_delete(tmp_path: Path) -> None:
    staging_root = tmp_path / "staging"
    runtime_root = tmp_path / "runtime"
    manager = PayloadManager(staging_root=staging_root, runtime_root=runtime_root)

    content = base64.b64encode(b"payload-bytes").decode("ascii")
    descriptor = manager.store_payload(PayloadType.SCRIPT, content, assembly_guid="abc123", extension=".json")

    staging_path = staging_root / "abc123" / descriptor.file_name
    runtime_path = runtime_root / "abc123" / descriptor.file_name
    assert staging_path.is_file()
    assert runtime_path.is_file()
    assert descriptor.size_bytes == len(content)

    updated = manager.update_payload(descriptor, content + "-v2")
    assert updated.size_bytes == len(content + "-v2")
    assert staging_path.read_text(encoding="utf-8").endswith("-v2")

    manager.delete_payload(descriptor)
    assert not staging_path.exists()
    assert not runtime_path.exists()
