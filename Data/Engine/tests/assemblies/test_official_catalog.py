# ======================================================
# Data\Engine\tests\assemblies\test_official_catalog.py
# Description: Verifies Aurora checkout crawling, update detection, and metadata persistence.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import base64
import json
import logging
import subprocess
from pathlib import Path

from Data.Engine.assembly_management.bootstrap import AssemblyCache
from Data.Engine.assembly_management.databases import AssemblyDatabaseManager
from Data.Engine.assembly_management.models import AssemblyDomain
from Data.Engine.services.assemblies.official_catalog import OfficialAssemblyCatalogService
from Data.Engine.services.assemblies.service import AssemblyRuntimeService


LOGGER = logging.getLogger("test.assemblies.official_catalog")


def _encoded_script(body: str) -> str:
    return base64.b64encode(body.encode("utf-8")).decode("ascii")


def _official_document(
    *,
    summary: str,
    script_body: str,
    assembly_guid: str = "aurora-official-guid",
    display_name: str = "Aurora Script",
    source_path: str = "scripts/windows/aurora-script.json",
) -> dict:
    return {
        "assembly_guid": assembly_guid,
        "display_name": display_name,
        "summary": summary,
        "assembly_type": "script",
        "assembly_subtype": "powershell",
        "source_repo": "https://github.com/bunny-lab-io/Aurora",
        "source_path": source_path,
        "source_version": "git:seeded",
        "payload": {
            "assembly_guid": assembly_guid,
            "name": display_name,
            "description": summary,
            "type": "powershell",
            "category": "script",
            "script": _encoded_script(script_body),
            "script_encoding": "base64",
            "timeout_seconds": 120,
            "variables": [],
            "files": [],
        },
    }


def _write_json(path: Path, payload: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, indent=2, sort_keys=True), encoding="utf-8")


def _run_git(repo: Path, *args: str) -> None:
    subprocess.run(["git", *args], cwd=str(repo), check=True, capture_output=True, text=True)


def test_official_catalog_detects_and_applies_aurora_updates(tmp_path: Path) -> None:
    bundled_root = tmp_path / "bundled"
    bundled_doc = _official_document(summary="Bundled seed version", script_body='Write-Host "bundled"')
    _write_json(bundled_root / "items" / "aurora-official-guid.json", bundled_doc)

    aurora_repo = tmp_path / "Aurora"
    aurora_repo.mkdir(parents=True, exist_ok=True)
    _run_git(aurora_repo, "init")
    _run_git(aurora_repo, "config", "user.email", "tests@example.com")
    _run_git(aurora_repo, "config", "user.name", "Borealis Tests")
    _run_git(aurora_repo, "checkout", "-b", "main")
    aurora_doc = _official_document(summary="Aurora repository version", script_body='Write-Host "aurora"')
    _write_json(aurora_repo / "scripts" / "windows" / "aurora-script.json", aurora_doc)
    _run_git(aurora_repo, "add", ".")
    _run_git(aurora_repo, "commit", "-m", "Seed Aurora official assembly")

    db_url = f"sqlite:///{(tmp_path / 'assemblies.sqlite3').as_posix()}"
    db_manager = AssemblyDatabaseManager(database_url=db_url, logger=LOGGER)
    db_manager.initialise()
    cache = AssemblyCache(
        database_manager=db_manager,
        flush_interval_seconds=5.0,
        logger=LOGGER,
    )
    runtime = AssemblyRuntimeService(cache, logger=LOGGER)
    service = OfficialAssemblyCatalogService(
        cache=cache,
        database_manager=db_manager,
        logger=LOGGER,
        bundled_root=bundled_root,
        checkout_root=tmp_path / "checkout",
        repo_url="https://github.com/bunny-lab-io/Aurora",
        repo_git_url=aurora_repo.as_posix(),
        repo_ref="main",
        refresh_seconds=30,
    )

    try:
        bundled_result = service.sync_bundled_catalog()
        assert bundled_result["updated"] == 1

        initial = runtime.get_assembly("aurora-official-guid")
        assert initial is not None
        assert initial["summary"] == "Bundled seed version"
        assert initial["source_path"] == "items/aurora-official-guid.json"
        assert initial["payload_json"]["assembly_guid"] == "aurora-official-guid"

        remote_manifest = service.manifest(force_remote=True)
        annotated = service.annotate_collection(
            runtime.list_assemblies(domain=AssemblyDomain.OFFICIAL.value),
            manifest=remote_manifest,
        )
        assert annotated[0]["official_update_available"] is True
        assert annotated[0]["official_source_path"] == "scripts/windows/aurora-script.json"
        status = service.catalog_status(manifest=remote_manifest)
        assert status["update_count"] == 1
        assert status["new_assembly_count"] == 0
        assert status["metadata_refresh_count"] == 0
        assert status["actionable_count"] == 1

        update_result = service.update_all_official_assemblies()
        assert update_result["updated"] == ["aurora-official-guid"]
        assert update_result["updated_items"] == [
            {
                "assembly_guid": "aurora-official-guid",
                "display_name": "Aurora Script",
                "source_path": "scripts/windows/aurora-script.json",
                "source_version": update_result["source_version"],
            }
        ]
        assert update_result["installed"] == []
        assert update_result["installed_items"] == []
        assert update_result["installed_count"] == 0
        assert update_result["updated_existing_count"] == 1
        assert update_result["failed"] == []

        updated = runtime.get_assembly("aurora-official-guid")
        assert updated is not None
        assert updated["summary"] == "Aurora repository version"
        assert updated["source_path"] == "scripts/windows/aurora-script.json"
        assert updated["source_repo"] == "https://github.com/bunny-lab-io/Aurora"
        assert str(updated["source_version"]).startswith("git:")
        assert updated["payload_json"]["assembly_guid"] == "aurora-official-guid"
        assert updated["content_hash"]

        persisted = db_manager.load_all(AssemblyDomain.OFFICIAL)
        assert persisted[0].source_path == "scripts/windows/aurora-script.json"
        assert persisted[0].source_repo == "https://github.com/bunny-lab-io/Aurora"
        assert str(persisted[0].source_version).startswith("git:")
        assert persisted[0].content_hash

        export_root = tmp_path / "aurora-export"
        written_root = service.write_aurora_snapshot(export_root)
        assert written_root == export_root.resolve()
        exported_file = export_root / "scripts" / "windows" / "aurora-script.json"
        assert exported_file.is_file()
    finally:
        cache.shutdown(flush=True)


def test_official_catalog_update_all_reports_sync_errors_without_bundled_fallback(tmp_path: Path) -> None:
    bundled_root = tmp_path / "bundled"
    bundled_doc = _official_document(summary="Bundled seed version", script_body='Write-Host "bundled"')
    _write_json(bundled_root / "items" / "aurora-official-guid.json", bundled_doc)

    db_url = f"sqlite:///{(tmp_path / 'assemblies.sqlite3').as_posix()}"
    db_manager = AssemblyDatabaseManager(database_url=db_url, logger=LOGGER)
    db_manager.initialise()
    cache = AssemblyCache(
        database_manager=db_manager,
        flush_interval_seconds=5.0,
        logger=LOGGER,
    )
    service = OfficialAssemblyCatalogService(
        cache=cache,
        database_manager=db_manager,
        logger=LOGGER,
        bundled_root=bundled_root,
        checkout_root=tmp_path / "checkout",
        repo_url="https://github.com/bunny-lab-io/Aurora",
        repo_git_url=(tmp_path / "missing-aurora.git").as_posix(),
        repo_ref="main",
        refresh_seconds=30,
    )

    try:
        bundled_result = service.sync_bundled_catalog()
        assert bundled_result["updated"] == 1

        result = service.update_all_official_assemblies()
        assert result["updated"] == []
        assert result["updated_items"] == []
        assert result["failed"] == []
        assert result["error"]
        assert "Failed to sync Aurora repository:" in result["error"]
    finally:
        cache.shutdown(flush=True)


def test_official_catalog_prefers_nested_payload_description_for_summary(tmp_path: Path) -> None:
    aurora_repo = tmp_path / "Aurora"
    aurora_repo.mkdir(parents=True, exist_ok=True)
    _run_git(aurora_repo, "init")
    _run_git(aurora_repo, "config", "user.email", "tests@example.com")
    _run_git(aurora_repo, "config", "user.name", "Borealis Tests")
    _run_git(aurora_repo, "checkout", "-b", "main")

    aurora_doc = _official_document(summary="Fresh nested description", script_body='Write-Host "aurora"')
    aurora_doc["summary"] = "Stale top-level summary"
    aurora_doc["description"] = "Older envelope description"
    _write_json(aurora_repo / "scripts" / "windows" / "aurora-script.json", aurora_doc)
    _run_git(aurora_repo, "add", ".")
    _run_git(aurora_repo, "commit", "-m", "Seed Aurora summary precedence")

    db_url = f"sqlite:///{(tmp_path / 'assemblies.sqlite3').as_posix()}"
    db_manager = AssemblyDatabaseManager(database_url=db_url, logger=LOGGER)
    db_manager.initialise()
    cache = AssemblyCache(
        database_manager=db_manager,
        flush_interval_seconds=5.0,
        logger=LOGGER,
    )
    runtime = AssemblyRuntimeService(cache, logger=LOGGER)
    service = OfficialAssemblyCatalogService(
        cache=cache,
        database_manager=db_manager,
        logger=LOGGER,
        bundled_root=tmp_path / "missing-bundled",
        checkout_root=tmp_path / "checkout",
        repo_url="https://github.com/bunny-lab-io/Aurora",
        repo_git_url=aurora_repo.as_posix(),
        repo_ref="main",
        refresh_seconds=30,
    )

    try:
        update_result = service.update_all_official_assemblies()
        assert update_result["updated"] == ["aurora-official-guid"]
        assert update_result["installed"] == ["aurora-official-guid"]
        assert update_result["installed_items"] == [
            {
                "assembly_guid": "aurora-official-guid",
                "display_name": "Aurora Script",
                "source_path": "scripts/windows/aurora-script.json",
                "source_version": update_result["source_version"],
            }
        ]
        assert update_result["installed_count"] == 1
        assert update_result["updated_existing_count"] == 0

        updated = runtime.get_assembly("aurora-official-guid")
        assert updated is not None
        assert updated["summary"] == "Fresh nested description"
        assert updated["payload_json"]["description"] == "Fresh nested description"
    finally:
        cache.shutdown(flush=True)


def test_official_catalog_status_reports_new_assemblies_missing_locally(tmp_path: Path) -> None:
    aurora_repo = tmp_path / "Aurora"
    aurora_repo.mkdir(parents=True, exist_ok=True)
    _run_git(aurora_repo, "init")
    _run_git(aurora_repo, "config", "user.email", "tests@example.com")
    _run_git(aurora_repo, "config", "user.name", "Borealis Tests")
    _run_git(aurora_repo, "checkout", "-b", "main")

    aurora_doc = _official_document(
        summary="Brand new Aurora assembly",
        script_body='Write-Host "fresh"',
        assembly_guid="aurora-new-guid",
        display_name="Aurora New Script",
        source_path="scripts/windows/aurora-new-script.json",
    )
    _write_json(aurora_repo / "scripts" / "windows" / "aurora-new-script.json", aurora_doc)
    _run_git(aurora_repo, "add", ".")
    _run_git(aurora_repo, "commit", "-m", "Add new Aurora assembly")

    db_url = f"sqlite:///{(tmp_path / 'assemblies.sqlite3').as_posix()}"
    db_manager = AssemblyDatabaseManager(database_url=db_url, logger=LOGGER)
    db_manager.initialise()
    cache = AssemblyCache(
        database_manager=db_manager,
        flush_interval_seconds=5.0,
        logger=LOGGER,
    )
    service = OfficialAssemblyCatalogService(
        cache=cache,
        database_manager=db_manager,
        logger=LOGGER,
        bundled_root=tmp_path / "missing-bundled",
        checkout_root=tmp_path / "checkout",
        repo_url="https://github.com/bunny-lab-io/Aurora",
        repo_git_url=aurora_repo.as_posix(),
        repo_ref="main",
        refresh_seconds=30,
    )

    try:
        status = service.catalog_status(manifest=service.manifest(force_remote=True))
        assert status["update_count"] == 0
        assert status["new_assembly_count"] == 1
        assert status["metadata_refresh_count"] == 0
        assert status["actionable_count"] == 1
    finally:
        cache.shutdown(flush=True)
