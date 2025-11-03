# ======================================================
# Data\Engine\tests\assemblies\test_cache.py
# Description: Validates AssemblyCache lifecycle behaviour, including dirty flags and background flushing.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import base64
import logging
import threading
import time
from typing import Iterator, Tuple

import pytest

from Data.Engine.assembly_management.databases import AssemblyDatabaseManager
from Data.Engine.assembly_management.models import AssemblyDomain
from Data.Engine.assembly_management.payloads import PayloadManager
from Data.Engine.assembly_management.bootstrap import AssemblyCache
from Data.Engine.services.assemblies.service import AssemblyRuntimeService


@pytest.fixture()
def assembly_runtime(tmp_path_factory: pytest.TempPathFactory) -> Iterator[Tuple[AssemblyRuntimeService, AssemblyCache, AssemblyDatabaseManager]]:
    staging_root = tmp_path_factory.mktemp("assemblies_staging")
    runtime_root = tmp_path_factory.mktemp("assemblies_runtime")

    db_manager = AssemblyDatabaseManager(staging_root=staging_root, runtime_root=runtime_root, logger=logging.getLogger("test.assemblies.db"))
    db_manager.initialise()
    payload_manager = PayloadManager(
        staging_root=staging_root / "Payloads",
        runtime_root=runtime_root / "Payloads",
        logger=logging.getLogger("test.assemblies.payload"),
    )
    cache = AssemblyCache(
        database_manager=db_manager,
        payload_manager=payload_manager,
        flush_interval_seconds=5.0,
        logger=logging.getLogger("test.assemblies.cache"),
    )
    service = AssemblyRuntimeService(cache, logger=logging.getLogger("test.assemblies.runtime"))
    try:
        yield service, cache, db_manager
    finally:
        cache.shutdown(flush=True)


def _script_payload(display_name: str = "Cache Test Script") -> dict:
    body = 'Write-Host "Hello from cache tests"'
    encoded = base64.b64encode(body.encode("utf-8")).decode("ascii")
    return {
        "domain": "user",
        "assembly_guid": None,
        "assembly_kind": "script",
        "display_name": display_name,
        "summary": "Cache test fixture payload.",
        "category": "script",
        "assembly_type": "powershell",
        "version": 1,
        "metadata": {
            "sites": {"mode": "all", "values": []},
            "variables": [],
            "files": [],
            "timeout_seconds": 120,
            "script_encoding": "base64",
        },
        "tags": {},
        "payload": {
            "version": 1,
            "name": display_name,
            "description": "Cache test fixture payload.",
            "category": "script",
            "type": "powershell",
            "script": encoded,
            "timeout_seconds": 120,
            "sites": {"mode": "all", "values": []},
            "variables": [],
            "files": [],
            "script_encoding": "base64",
        },
    }


def test_cache_flush_marks_entries_clean(assembly_runtime) -> None:
    service, cache, db_manager = assembly_runtime
    record = service.create_assembly(_script_payload())
    guid = record["assembly_guid"]

    snapshot = {entry["assembly_guid"]: entry for entry in cache.describe()}
    assert snapshot[guid]["is_dirty"] == "true"

    cache.flush_now()
    cache.reload()
    snapshot = {entry["assembly_guid"]: entry for entry in cache.describe()}
    assert snapshot[guid]["is_dirty"] == "false"
    persisted = db_manager.load_all(AssemblyDomain.USER)
    assert any(item.assembly_guid == guid for item in persisted)


def test_cache_worker_flushes_on_event(assembly_runtime) -> None:
    service, cache, _db_manager = assembly_runtime
    record = service.create_assembly(_script_payload(display_name="Worker Flush Script"))
    guid = record["assembly_guid"]

    cache._flush_event.set()  # Trigger worker loop without waiting for full interval.
    time.sleep(0.2)
    cache.reload()
    snapshot = {entry["assembly_guid"]: entry for entry in cache.describe()}
    assert snapshot[guid]["is_dirty"] == "false"


def test_cache_flush_waits_for_locked_database(assembly_runtime) -> None:
    service, cache, db_manager = assembly_runtime
    if cache._worker.is_alive():  # type: ignore[attr-defined]
        cache._stop_event.set()  # type: ignore[attr-defined]
        cache._flush_event.set()  # type: ignore[attr-defined]
        cache._worker.join(timeout=1.0)  # type: ignore[attr-defined]
        cache._stop_event.clear()  # type: ignore[attr-defined]
    record = service.create_assembly(_script_payload(display_name="Concurrency Script"))
    guid = record["assembly_guid"]
    cache.flush_now()

    update_payload = _script_payload(display_name="Concurrency Script")
    update_payload["assembly_guid"] = guid
    update_payload["summary"] = "Updated summary after lock."
    service.update_assembly(guid, update_payload)

    conn = db_manager._open_connection(AssemblyDomain.USER)  # type: ignore[attr-defined]
    cur = conn.cursor()
    cur.execute("BEGIN IMMEDIATE")

    flush_thread = threading.Thread(target=cache.flush_now)
    flush_thread.start()

    time.sleep(0.2)
    snapshot = {entry["assembly_guid"]: entry for entry in cache.describe()}
    assert snapshot[guid]["is_dirty"] == "true"
    conn.rollback()
    conn.close()

    flush_thread.join(timeout=5.0)
    assert not flush_thread.is_alive(), "Initial flush attempt did not return after releasing database lock."
    cache.flush_now()
    cache.reload()
    snapshot = {entry["assembly_guid"]: entry for entry in cache.describe()}
    assert snapshot[guid]["is_dirty"] == "false"

    records = db_manager.load_all(AssemblyDomain.USER)
    summaries = {entry.assembly_guid: entry.summary for entry in records}
    assert summaries[guid] == "Updated summary after lock."
