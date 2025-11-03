# ======================================================
# Data\Engine\assembly_management\sync.py
# Description: Synchronises assembly databases from staged filesystem sources (official domain importer).
#
# API Endpoints (if applicable): None
# ======================================================

"""Synchronisation helpers for assembly persistence domains."""

from __future__ import annotations

import datetime as _dt
import hashlib
import json
import logging
from pathlib import Path
from typing import Iterable, Optional, Tuple

from .databases import AssemblyDatabaseManager
from .models import AssemblyDomain, AssemblyRecord, CachedAssembly, PayloadType
from .payloads import PayloadManager


_SCRIPT_DIRS = {"scripts", "script"}
_WORKFLOW_DIRS = {"workflows", "workflow"}
_ANSIBLE_DIRS = {"ansible_playbooks", "ansible-playbooks", "ansible"}


def sync_official_domain(
    db_manager: AssemblyDatabaseManager,
    payload_manager: PayloadManager,
    staging_root: Path,
    *,
    logger: Optional[logging.Logger] = None,
) -> None:
    """Repopulate the official domain database from staged JSON assemblies."""

    log = logger or logging.getLogger(__name__)
    root = staging_root.resolve()
    if not root.is_dir():
        log.warning("Assembly staging root missing during official sync: %s", root)
        return

    files = tuple(_iter_assembly_sources(root))
    if not files:
        log.info("No staged assemblies discovered for official sync; clearing domain.")
        db_manager.reset_domain(AssemblyDomain.OFFICIAL)
        return

    db_manager.reset_domain(AssemblyDomain.OFFICIAL)

    imported = 0
    skipped = 0

    for rel_path, file_path in files:
        record = _record_from_file(rel_path, file_path, payload_manager, log)
        if record is None:
            skipped += 1
            continue
        entry = CachedAssembly(
            domain=AssemblyDomain.OFFICIAL,
            record=record,
            is_dirty=False,
            last_persisted=record.updated_at,
        )
        try:
            db_manager.upsert_record(AssemblyDomain.OFFICIAL, entry)
            imported += 1
        except Exception:  # pragma: no cover - defensive logging
            skipped += 1
            log.exception("Failed to import assembly %s during official sync.", rel_path)

    log.info(
        "Official assembly sync complete: imported=%s skipped=%s source_root=%s",
        imported,
        skipped,
        root,
    )


def _iter_assembly_sources(root: Path) -> Iterable[Tuple[str, Path]]:
    for path in root.rglob("*.json"):
        if not path.is_file():
            continue
        rel_path = path.relative_to(root).as_posix()
        yield rel_path, path


def _record_from_file(
    rel_path: str,
    file_path: Path,
    payload_manager: PayloadManager,
    logger: logging.Logger,
) -> Optional[AssemblyRecord]:
    try:
        text = file_path.read_text(encoding="utf-8")
    except Exception as exc:
        logger.warning("Unable to read assembly source %s: %s", file_path, exc)
        return None

    try:
        document = json.loads(text)
    except Exception as exc:
        logger.warning("Invalid JSON for assembly %s: %s", file_path, exc)
        return None

    kind = _infer_kind(rel_path)
    if kind == "unknown":
        logger.debug("Skipping non-assembly file %s", rel_path)
        return None

    payload_type = _payload_type_for_kind(kind)
    guid = hashlib.sha1(rel_path.encode("utf-8")).hexdigest()
    descriptor = payload_manager.store_payload(payload_type, text, assembly_guid=guid, extension=".json")

    file_stat = file_path.stat()
    timestamp = _dt.datetime.utcfromtimestamp(file_stat.st_mtime).replace(microsecond=0)
    descriptor.created_at = timestamp
    descriptor.updated_at = timestamp

    assembly_id = _assembly_id_from_path(rel_path)
    document_metadata = _metadata_from_document(kind, document, rel_path)
    tags = _coerce_dict(document.get("tags"))

    record = AssemblyRecord(
        assembly_id=assembly_id,
        display_name=document_metadata.get("display_name") or assembly_id.rsplit("/", 1)[-1],
        summary=document_metadata.get("summary"),
        category=document_metadata.get("category"),
        assembly_kind=kind,
        assembly_type=document_metadata.get("assembly_type"),
        version=_coerce_int(document.get("version"), default=1),
        payload=descriptor,
        metadata=document_metadata,
        tags=tags,
        checksum=hashlib.sha256(text.encode("utf-8")).hexdigest(),
        created_at=timestamp,
        updated_at=timestamp,
    )
    return record


def _infer_kind(rel_path: str) -> str:
    first = rel_path.split("/", 1)[0].strip().lower()
    if first in _SCRIPT_DIRS:
        return "script"
    if first in _WORKFLOW_DIRS:
        return "workflow"
    if first in _ANSIBLE_DIRS:
        return "ansible"
    return "unknown"


def _payload_type_for_kind(kind: str) -> PayloadType:
    if kind == "workflow":
        return PayloadType.WORKFLOW
    if kind == "script":
        return PayloadType.SCRIPT
    if kind == "ansible":
        return PayloadType.BINARY
    return PayloadType.UNKNOWN


def _assembly_id_from_path(rel_path: str) -> str:
    normalised = rel_path.replace("\\", "/")
    if normalised.lower().endswith(".json"):
        normalised = normalised[:-5]
    return normalised


def _metadata_from_document(kind: str, document: dict, rel_path: str) -> dict:
    metadata = {
        "source_path": rel_path,
        "display_name": None,
        "summary": None,
        "category": None,
        "assembly_type": None,
    }

    if kind == "workflow":
        metadata["display_name"] = document.get("tab_name") or document.get("name")
        metadata["summary"] = document.get("description")
        metadata["category"] = "workflow"
        metadata["assembly_type"] = "workflow"
    elif kind == "script":
        metadata["display_name"] = document.get("name") or document.get("display_name")
        metadata["summary"] = document.get("description")
        metadata["category"] = (document.get("category") or "script").lower()
        metadata["assembly_type"] = (document.get("type") or "powershell").lower()
    elif kind == "ansible":
        metadata["display_name"] = document.get("name") or document.get("display_name") or rel_path.rsplit("/", 1)[-1]
        metadata["summary"] = document.get("description")
        metadata["category"] = "ansible"
        metadata["assembly_type"] = "ansible"

    metadata.update(
        {
            "sites": document.get("sites"),
            "variables": document.get("variables"),
            "files": document.get("files"),
        }
    )
    metadata["display_name"] = metadata.get("display_name") or rel_path.rsplit("/", 1)[-1]
    return metadata


def _coerce_int(value, *, default: int = 0) -> int:
    try:
        return int(value)
    except Exception:
        return default


def _coerce_dict(value) -> dict:
    return value if isinstance(value, dict) else {}
