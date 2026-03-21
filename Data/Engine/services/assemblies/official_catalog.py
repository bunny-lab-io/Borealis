# ======================================================
# Data\Engine\services\assemblies\official_catalog.py
# Description: Official assembly catalog sync helpers for bundled snapshots and Aurora repository updates.
#
# API Endpoints (if applicable): None
# ======================================================

"""Official assembly catalog helpers."""

from __future__ import annotations

import argparse
import base64
import copy
import hashlib
import json
import logging
import os
import re
import shutil
import subprocess
import tempfile
import threading
import time
from dataclasses import dataclass
from pathlib import Path
from typing import TYPE_CHECKING, Any, Dict, Iterable, List, Mapping, Optional
from urllib.parse import quote, urlparse

from ...assembly_management.databases import AssemblyDatabaseManager
from ...assembly_management.models import AssemblyDomain, AssemblyRecord

if TYPE_CHECKING:  # pragma: no cover - typing only
    from ...assembly_management.bootstrap import AssemblyCache


DEFAULT_OFFICIAL_REPO_URL = "https://github.com/bunny-lab-io/Aurora"
DEFAULT_OFFICIAL_REPO_GIT_URL = "https://github.com/bunny-lab-io/Aurora.git"
DEFAULT_OFFICIAL_REPO_REF = "main"
DEFAULT_REMOTE_REFRESH_SECONDS = 300
MANIFEST_FILENAME = "manifest.json"
MAX_RECOMMENDED_PAYLOAD_BYTES = 500 * 1024 * 1024


def _coerce_text(value: Any, default: str = "") -> str:
    if value is None:
        return default
    text = str(value).strip()
    return text if text else default


def _coerce_optional_text(value: Any) -> Optional[str]:
    text = _coerce_text(value)
    return text or None


def _coerce_guid(value: Any) -> str:
    return _coerce_text(value).lower()


def _payload_from_document(document: Mapping[str, Any]) -> Any:
    payload = document.get("payload")
    if isinstance(payload, Mapping):
        return dict(payload)
    if isinstance(payload, list):
        return list(payload)
    workflow_payload = _workflow_payload_from_document(document)
    if workflow_payload is not None:
        return workflow_payload
    return dict(document)


def _workflow_payload_from_document(document: Mapping[str, Any]) -> Optional[Dict[str, Any]]:
    workflow = document.get("workflow")
    if workflow is None:
        return None

    extracted: Optional[Dict[str, Any]] = None
    if isinstance(workflow, Mapping):
        extracted = dict(workflow)
    elif isinstance(workflow, str):
        workflow_text = workflow.strip()
        decoded = _decode_base64_text(workflow_text)
        if decoded is not None:
            workflow_text = decoded
        try:
            parsed = json.loads(workflow_text)
        except json.JSONDecodeError as exc:
            raise ValueError("workflow field did not decode to JSON") from exc
        if not isinstance(parsed, Mapping):
            raise ValueError("workflow field must decode to a JSON object")
        extracted = dict(parsed)
    else:
        raise ValueError("workflow field must be a JSON object or encoded string")

    resolved_guid = _coerce_guid(document.get("assembly_guid") or extracted.get("assembly_guid"))
    if resolved_guid:
        extracted["assembly_guid"] = resolved_guid
    tab_name = _coerce_optional_text(
        extracted.get("tab_name")
        or document.get("tab_name")
        or document.get("name")
        or document.get("display_name")
    )
    if tab_name:
        extracted["tab_name"] = tab_name
    return extracted


def _decode_base64_text(value: Any) -> Optional[str]:
    if value is None:
        return None
    text = str(value).strip()
    if not text:
        return ""
    cleaned = "".join(text.split())
    try:
        decoded = base64.b64decode(cleaned, validate=True)
    except Exception:
        return None
    try:
        return decoded.decode("utf-8")
    except UnicodeDecodeError:
        return None


def _summary_from_document(document: Mapping[str, Any], payload: Any) -> Optional[str]:
    payload_description = payload.get("description") if isinstance(payload, Mapping) else None
    payload_summary = payload.get("summary") if isinstance(payload, Mapping) else None
    return _coerce_optional_text(
        payload_description
        or payload_summary
        or document.get("description")
        or document.get("summary")
    )


def _infer_assembly_type(document: Mapping[str, Any], payload: Any) -> str:
    type_hint = _coerce_text(
        document.get("assembly_type")
        or document.get("kind")
        or (payload.get("assembly_type") if isinstance(payload, Mapping) else None)
        or (payload.get("kind") if isinstance(payload, Mapping) else None)
    ).lower()
    if type_hint in {"script", "workflow", "ansible"}:
        return type_hint

    subtype_hint = _coerce_text(
        document.get("assembly_subtype")
        or document.get("type")
        or (payload.get("assembly_subtype") if isinstance(payload, Mapping) else None)
        or (payload.get("type") if isinstance(payload, Mapping) else None)
        or (payload.get("script_type") if isinstance(payload, Mapping) else None)
    ).lower()
    if subtype_hint in {"workflow", "ansible"}:
        return subtype_hint
    if isinstance(payload, Mapping) and "nodes" in payload and "edges" in payload:
        return "workflow"
    if isinstance(payload, Mapping) and ("playbook" in payload or "tasks" in payload or "roles" in payload):
        return "ansible"
    return "script"


def _infer_assembly_subtype(document: Mapping[str, Any], payload: Any, assembly_type: str) -> str:
    subtype = _coerce_text(
        document.get("assembly_subtype")
        or document.get("type")
        or (payload.get("assembly_subtype") if isinstance(payload, Mapping) else None)
        or (payload.get("type") if isinstance(payload, Mapping) else None)
        or (payload.get("script_type") if isinstance(payload, Mapping) else None)
    ).lower()
    if subtype:
        return subtype
    if assembly_type == "workflow":
        return "workflow"
    if assembly_type == "ansible":
        return "ansible"
    return "powershell"


def _stable_content_fields(
    *,
    assembly_guid: str,
    display_name: str,
    summary: Optional[str],
    assembly_type: str,
    assembly_subtype: Optional[str],
    payload: Any,
) -> Dict[str, Any]:
    payload_value = payload
    if isinstance(payload, Mapping):
        payload_value = copy.deepcopy(dict(payload))
        payload_value["assembly_guid"] = assembly_guid
    elif isinstance(payload, list):
        payload_value = copy.deepcopy(list(payload))

    return {
        "assembly_guid": assembly_guid,
        "display_name": display_name,
        "summary": summary or "",
        "assembly_type": assembly_type or "script",
        "assembly_subtype": assembly_subtype or "powershell",
        "payload": payload_value,
    }


def _hash_content_fields(fields: Mapping[str, Any]) -> str:
    encoded = json.dumps(fields, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode("utf-8")
    return hashlib.sha256(encoded).hexdigest()


def compute_catalog_content_hash(document: Mapping[str, Any]) -> str:
    """Return a stable content hash for an assembly document or export envelope."""

    payload = _payload_from_document(document)
    assembly_guid = _coerce_guid(
        document.get("assembly_guid") or (payload.get("assembly_guid") if isinstance(payload, Mapping) else None)
    )
    assembly_type = _infer_assembly_type(document, payload)
    assembly_subtype = _infer_assembly_subtype(document, payload, assembly_type)
    display_name = _coerce_text(
        document.get("display_name")
        or document.get("displayName")
        or document.get("name")
        or document.get("tab_name")
        or (payload.get("display_name") if isinstance(payload, Mapping) else None)
        or (payload.get("name") if isinstance(payload, Mapping) else None)
        or (payload.get("tab_name") if isinstance(payload, Mapping) else None)
        or assembly_guid
        or "Assembly"
    )
    summary = _summary_from_document(document, payload)
    return _hash_content_fields(
        _stable_content_fields(
            assembly_guid=assembly_guid,
            display_name=display_name,
            summary=summary,
            assembly_type=assembly_type,
            assembly_subtype=assembly_subtype,
            payload=payload,
        )
    )


def compute_record_content_hash(record: AssemblyRecord) -> str:
    """Return a stable content hash for a persisted assembly record."""

    payload: Any
    try:
        payload = json.loads(record.payload_json or "{}")
    except Exception:
        payload = record.payload_json or ""
    return _hash_content_fields(
        _stable_content_fields(
            assembly_guid=_coerce_guid(record.assembly_guid),
            display_name=_coerce_text(record.display_name, record.assembly_guid),
            summary=_coerce_optional_text(record.summary),
            assembly_type=_coerce_text(record.assembly_type, "script").lower(),
            assembly_subtype=_coerce_optional_text(record.assembly_subtype) or "powershell",
            payload=payload,
        )
    )


def compute_api_item_content_hash(item: Mapping[str, Any]) -> str:
    """Return a stable content hash for an API payload returned from the assemblies service."""

    payload_value: Any = item.get("payload_json")
    if isinstance(payload_value, str):
        try:
            payload_value = json.loads(payload_value)
        except Exception:
            payload_value = payload_value
    if payload_value in (None, ""):
        payload_value = item.get("payload") or {}

    summary = _summary_from_document(
        {"summary": item.get("summary"), "description": item.get("description")},
        payload_value,
    )

    return _hash_content_fields(
        _stable_content_fields(
            assembly_guid=_coerce_guid(item.get("assembly_guid") or item.get("assembly_id")),
            display_name=_coerce_text(item.get("display_name"), item.get("assembly_guid") or "Assembly"),
            summary=summary,
            assembly_type=_coerce_text(item.get("assembly_type"), "script").lower(),
            assembly_subtype=_coerce_optional_text(item.get("assembly_subtype")) or "powershell",
            payload=payload_value,
        )
    )


def _normalize_relative_path(value: Any) -> str:
    text = str(value or "").replace("\\", "/").strip()
    if not text:
        return ""
    parts: List[str] = []
    for part in text.split("/"):
        candidate = part.strip()
        if not candidate or candidate == ".":
            continue
        if candidate == "..":
            return ""
        parts.append(candidate)
    return "/".join(parts)


def _manifest_allows_deleted_cleanup(manifest: "OfficialCatalogManifest") -> bool:
    return (
        manifest.available
        and manifest.source == "aurora"
        and not manifest.error
        and int(manifest.failed_files or 0) == 0
    )


def _should_ignore_catalog_path(path: Path) -> bool:
    if path.name == MANIFEST_FILENAME:
        return True
    for part in path.parts:
        if part.startswith("."):
            return True
    return False


def _json_size_bytes(value: Any) -> int:
    try:
        return len(json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8"))
    except Exception:
        return 0


_FILENAME_SANITIZE_PATTERN = re.compile(r"[^A-Za-z0-9._-]+")


def _slugify_filename(value: Any, fallback: str = "assembly") -> str:
    text = str(value or "").strip().lower()
    if not text:
        return fallback
    sanitized = _FILENAME_SANITIZE_PATTERN.sub("-", text)
    sanitized = sanitized.strip("-._")
    return sanitized or fallback


def _type_root_folder(assembly_type: Optional[str]) -> str:
    lowered = _coerce_text(assembly_type).lower()
    if lowered == "workflow":
        return "workflows"
    if lowered == "ansible":
        return "ansible"
    return "scripts"


def _type_subfolder(record: AssemblyRecord) -> str:
    if _coerce_text(record.assembly_type).lower() == "script":
        subtype = _coerce_text(record.assembly_subtype).lower()
        if subtype in {"powershell", "bash", "python", "shell"}:
            return subtype
    return "general"


def _looks_like_legacy_seed_path(source_path: Optional[str]) -> bool:
    normalized = _normalize_relative_path(source_path)
    if not normalized:
        return True
    if normalized == MANIFEST_FILENAME:
        return True
    return normalized.startswith("items/")


def _default_export_relative_path(record: AssemblyRecord) -> str:
    existing = _normalize_relative_path(record.source_path)
    if existing and not _looks_like_legacy_seed_path(existing):
        return existing
    root = _type_root_folder(record.assembly_type)
    subfolder = _type_subfolder(record)
    slug = _slugify_filename(record.display_name or record.assembly_guid)
    guid_suffix = _coerce_guid(record.assembly_guid)[:8] or "assembly"
    return f"{root}/{subfolder}/{slug}--{guid_suffix}.json"


def _repo_default_git_url(repo_url: str) -> str:
    if repo_url.endswith(".git"):
        return repo_url
    return f"{repo_url}.git"


def _repo_checkout_name(repo_url: str, repo_git_url: str) -> str:
    parsed = urlparse(repo_git_url or repo_url)
    leaf = Path(parsed.path or "").name
    leaf = leaf[:-4] if leaf.endswith(".git") else leaf
    leaf = leaf or "Aurora"
    safe = "".join(char if char.isalnum() or char in {"-", "_"} else "-" for char in leaf)
    return safe or "Aurora"


def _github_blob_url(repo_url: str, source_version: Optional[str], source_path: str) -> Optional[str]:
    if not repo_url or "github.com" not in repo_url.lower():
        return None
    rel_path = _normalize_relative_path(source_path)
    if not rel_path:
        return repo_url
    commit = _coerce_text(source_version)
    if commit.startswith("git:"):
        commit = commit[4:]
    if not commit:
        return repo_url
    return f"{repo_url.rstrip('/')}/blob/{quote(commit)}/{quote(rel_path, safe='/')}"


def _utcnow_iso() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def _entry_requires_metadata_refresh(current_item: Optional[Mapping[str, Any]], entry: "OfficialCatalogEntry") -> bool:
    if not current_item:
        return True
    current_source_repo = _coerce_text(current_item.get("source_repo"))
    current_source_path = _normalize_relative_path(current_item.get("source_path"))
    current_source_version = _coerce_text(current_item.get("source_version"))
    current_content_hash = _coerce_text(current_item.get("content_hash"))

    if not current_source_repo:
        return True
    if current_source_repo != entry.source_repo:
        return True
    if current_source_path != entry.source_path:
        return True
    if not current_source_version:
        return True
    if not current_content_hash:
        return True
    return False


@dataclass(slots=True)
class OfficialCatalogEntry:
    """Catalog entry describing one managed official assembly."""

    assembly_guid: str
    display_name: str
    summary: Optional[str]
    assembly_type: str
    assembly_subtype: Optional[str]
    content_hash: str
    file_path: str
    source_repo: str
    source_path: str
    source_version: Optional[str] = None
    source_url: Optional[str] = None
    payload_size_bytes: int = 0


@dataclass(slots=True)
class OfficialCatalogManifest:
    """Loaded official catalog snapshot from Aurora checkout or bundled seed."""

    source: str
    repo_url: str
    repo_git_url: str
    repo_ref: str
    catalog_version: Optional[str]
    generated_at: Optional[str]
    entries: Dict[str, OfficialCatalogEntry]
    base_path: Optional[Path] = None
    error: Optional[str] = None
    scanned_files: int = 0
    skipped_files: int = 0
    failed_files: int = 0

    @property
    def available(self) -> bool:
        return bool(self.entries)

    def get(self, assembly_guid: str) -> Optional[OfficialCatalogEntry]:
        return self.entries.get(_coerce_guid(assembly_guid))


class OfficialAssemblyCatalogService:
    """Synchronise bundled and Aurora-managed official assembly catalogs into PostgreSQL."""

    def __init__(
        self,
        *,
        cache: "AssemblyCache",
        database_manager: AssemblyDatabaseManager,
        logger: Optional[logging.Logger] = None,
        github_integration: Optional[Any] = None,
        bundled_root: Optional[Path] = None,
        checkout_root: Optional[Path] = None,
        repo_url: str = DEFAULT_OFFICIAL_REPO_URL,
        repo_git_url: str = DEFAULT_OFFICIAL_REPO_GIT_URL,
        repo_ref: str = DEFAULT_OFFICIAL_REPO_REF,
        manifest_url: str = "",
        refresh_seconds: int = DEFAULT_REMOTE_REFRESH_SECONDS,
    ) -> None:
        self._cache = cache
        self._db_manager = database_manager
        self._logger = logger or logging.getLogger(__name__)
        self._github = github_integration
        self._bundled_root = bundled_root.resolve() if bundled_root else None
        self._checkout_root = checkout_root.resolve() if checkout_root else None
        self._repo_url = _coerce_text(repo_url, DEFAULT_OFFICIAL_REPO_URL)
        self._repo_git_url = _coerce_text(repo_git_url, _repo_default_git_url(self._repo_url))
        self._repo_ref = _coerce_text(repo_ref, DEFAULT_OFFICIAL_REPO_REF)
        self._manifest_url = _coerce_text(manifest_url)
        self._refresh_seconds = max(30, int(refresh_seconds or DEFAULT_REMOTE_REFRESH_SECONDS))
        self._catalog_cache: Optional[OfficialCatalogManifest] = None
        self._catalog_loaded_at: float = 0.0
        self._catalog_lock = threading.RLock()

    # ------------------------------------------------------------------
    # Public operations
    # ------------------------------------------------------------------
    def sync_bundled_catalog(self) -> Dict[str, Any]:
        """Apply bundled official assemblies when the bundled snapshot changes."""

        manifest = self._load_bundled_manifest()
        if not manifest.available:
            if manifest.error:
                self._logger.debug("Bundled official catalog unavailable: %s", manifest.error)
            return {
                "source": "bundled",
                "updated": 0,
                "skipped": 0,
                "available": False,
                "error": manifest.error,
            }

        state_map = self._db_manager.load_official_catalog_state()
        runtime = self._runtime_service()
        current_items = runtime.list_assemblies(domain=AssemblyDomain.OFFICIAL.value)
        current_items_by_guid = {
            _coerce_guid(item.get("assembly_guid") or item.get("assembly_id")): item
            for item in current_items
        }
        changed = 0
        skipped = 0

        for entry in manifest.entries.values():
            state = state_map.get(entry.assembly_guid)
            current_item = current_items_by_guid.get(entry.assembly_guid)
            metadata_refresh_needed = _entry_requires_metadata_refresh(current_item, entry)
            if state and state.bundled_hash == entry.content_hash and not metadata_refresh_needed:
                skipped += 1
                continue

            document = self._load_entry_document(manifest, entry)
            runtime.import_assembly(
                domain=AssemblyDomain.OFFICIAL,
                document=document,
                assembly_guid=entry.assembly_guid,
            )
            self._db_manager.upsert_official_catalog_state(
                entry.assembly_guid,
                bundled_hash=entry.content_hash,
                catalog_hash=entry.content_hash,
                applied_hash=entry.content_hash,
                last_applied_source="bundled",
                repo_url=manifest.repo_url,
                source_url=entry.source_url or manifest.repo_url,
                source_repo=entry.source_repo,
                source_path=entry.source_path,
                source_version=entry.source_version or manifest.catalog_version,
                last_catalog_sync_at=_utcnow_iso(),
            )
            changed += 1

        if changed:
            runtime.flush_writes()

        return {
            "source": "bundled",
            "updated": changed,
            "skipped": skipped,
            "available": True,
            "repo_url": manifest.repo_url,
            "source_version": manifest.catalog_version,
        }

    def manifest(
        self,
        *,
        force_remote: bool = False,
        allow_remote_sync: Optional[bool] = None,
    ) -> OfficialCatalogManifest:
        """Return the active official catalog manifest."""

        if allow_remote_sync is None:
            allow_remote_sync = force_remote
        return self._active_manifest(
            force_remote=force_remote,
            allow_remote_sync=bool(allow_remote_sync),
        )

    def annotate_collection(
        self,
        items: Iterable[Mapping[str, Any]],
        *,
        manifest: Optional[OfficialCatalogManifest] = None,
        force_remote: bool = False,
        allow_remote_sync: Optional[bool] = None,
    ) -> List[Dict[str, Any]]:
        """Attach official-catalog update metadata to assembly list payloads."""

        if allow_remote_sync is None:
            allow_remote_sync = force_remote
        manifest = manifest or self._active_manifest(
            force_remote=force_remote,
            allow_remote_sync=bool(allow_remote_sync),
        )
        state_map = self._db_manager.load_official_catalog_state()
        annotated: List[Dict[str, Any]] = []

        for raw_item in items:
            item = dict(raw_item)
            if _coerce_text(item.get("source")).lower() != AssemblyDomain.OFFICIAL.value:
                annotated.append(item)
                continue

            guid = _coerce_guid(item.get("assembly_guid") or item.get("assembly_id"))
            current_hash = _coerce_text(item.get("content_hash")) or compute_api_item_content_hash(item)
            entry = manifest.get(guid) if manifest.available else None
            state = state_map.get(guid)
            repo_url = manifest.repo_url or (state.repo_url if state else self._repo_url)
            source_url = entry.source_url if entry else (state.source_url if state else repo_url)
            source_repo = entry.source_repo if entry else (state.source_repo if state else repo_url)
            source_path = entry.source_path if entry else (state.source_path if state else item.get("source_path"))
            source_version = entry.source_version if entry and entry.source_version else (
                state.source_version if state else item.get("source_version")
            )
            update_available = bool(entry and entry.content_hash and current_hash != entry.content_hash)

            item["source_repo"] = item.get("source_repo") or source_repo
            item["source_path"] = item.get("source_path") or source_path
            item["source_version"] = item.get("source_version") or source_version
            item["official_managed"] = bool(entry or state or source_path)
            item["official_repo_url"] = repo_url
            item["official_source_url"] = source_url or repo_url
            item["official_catalog_source"] = manifest.source if manifest.available else "bundled"
            item["official_source_version"] = source_version
            item["official_source_path"] = source_path
            item["official_update_available"] = update_available
            item["official_last_applied_source"] = state.last_applied_source if state else None
            item["official_last_synced_at"] = (
                state.last_catalog_sync_at.isoformat()
                if state and state.last_catalog_sync_at
                else (state.updated_at.isoformat() if state and state.updated_at else None)
            )
            annotated.append(item)

        return annotated

    def catalog_status(
        self,
        items: Optional[Iterable[Mapping[str, Any]]] = None,
        *,
        manifest: Optional[OfficialCatalogManifest] = None,
        force_remote: bool = False,
        allow_remote_sync: Optional[bool] = None,
    ) -> Dict[str, Any]:
        """Return metadata describing the current official catalog source and update state."""

        if allow_remote_sync is None:
            allow_remote_sync = force_remote
        manifest = manifest or self._active_manifest(
            force_remote=force_remote,
            allow_remote_sync=bool(allow_remote_sync),
        )
        runtime = self._runtime_service()
        current_items = runtime.list_assemblies(domain=AssemblyDomain.OFFICIAL.value)
        current_items_by_guid = {
            _coerce_guid(item.get("assembly_guid") or item.get("assembly_id")): item
            for item in current_items
            if _coerce_guid(item.get("assembly_guid") or item.get("assembly_id"))
        }
        update_count = 0
        new_assembly_count = 0
        metadata_refresh_count = 0
        if manifest.available:
            for entry in manifest.entries.values():
                current_item = current_items_by_guid.get(entry.assembly_guid)
                if current_item is None:
                    new_assembly_count += 1
                    continue
                current_hash = _coerce_text(current_item.get("content_hash")) or compute_api_item_content_hash(current_item)
                metadata_refresh_needed = _entry_requires_metadata_refresh(current_item, entry)
                if current_hash and entry.content_hash and current_hash != entry.content_hash:
                    update_count += 1
                    continue
                if metadata_refresh_needed:
                    metadata_refresh_count += 1
        manifest_error = manifest.error or ""
        actionable_count = update_count + new_assembly_count + metadata_refresh_count

        return {
            "repo_url": manifest.repo_url or self._repo_url,
            "repo_git_url": manifest.repo_git_url or self._repo_git_url,
            "repo_ref": manifest.repo_ref or self._repo_ref,
            "source": manifest.source,
            "available": manifest.available,
            "manifest_url": self._manifest_url or None,
            "source_version": manifest.catalog_version,
            "generated_at": manifest.generated_at,
            "error": manifest_error if not manifest.available else "",
            "warning": manifest_error if manifest.available else "",
            "update_count": update_count,
            "new_assembly_count": new_assembly_count,
            "metadata_refresh_count": metadata_refresh_count,
            "actionable_count": actionable_count,
            "scanned_files": manifest.scanned_files,
            "failed_files": manifest.failed_files,
        }

    def cleanup_deleted_official_assemblies(
        self,
        *,
        manifest: Optional[OfficialCatalogManifest] = None,
        force_remote: bool = False,
        allow_remote_sync: Optional[bool] = None,
    ) -> Dict[str, Any]:
        """Delete local official assemblies that no longer exist in the authoritative Aurora manifest."""

        if allow_remote_sync is None:
            allow_remote_sync = force_remote
        manifest = manifest or self._active_manifest(
            force_remote=force_remote,
            allow_remote_sync=bool(allow_remote_sync),
        )

        if not _manifest_allows_deleted_cleanup(manifest):
            return {
                "cleanup_performed": False,
                "deleted": [],
                "deleted_items": [],
                "deleted_count": 0,
                "state_pruned_count": 0,
                "failed": [],
            }

        runtime = self._runtime_service()
        current_items = runtime.list_assemblies(domain=AssemblyDomain.OFFICIAL.value)
        current_items_by_guid = {
            _coerce_guid(item.get("assembly_guid") or item.get("assembly_id")): item
            for item in current_items
            if _coerce_guid(item.get("assembly_guid") or item.get("assembly_id"))
        }
        state_map = self._db_manager.load_official_catalog_state()
        manifest_guids = set(manifest.entries.keys())

        deleted: List[str] = []
        deleted_items: List[Dict[str, Any]] = []
        failed: List[Dict[str, str]] = []

        for guid, item in current_items_by_guid.items():
            if guid in manifest_guids:
                continue
            try:
                runtime.delete_assembly(guid)
                deleted.append(guid)
                deleted_items.append(
                    {
                        "assembly_guid": guid,
                        "display_name": _coerce_text(item.get("display_name") or item.get("name"), guid),
                        "source_path": _normalize_relative_path(item.get("source_path") or item.get("path") or ""),
                    }
                )
            except Exception as exc:
                failed.append({"assembly_guid": guid, "error": str(exc), "action": "delete"})
                self._logger.exception("Failed to delete revoked official assembly %s", guid)

        if deleted:
            runtime.flush_writes()
            for guid in deleted:
                self._db_manager.delete_official_catalog_state(guid)

        state_pruned_count = 0
        for guid in set(state_map.keys()) - manifest_guids - set(current_items_by_guid.keys()):
            self._db_manager.delete_official_catalog_state(guid)
            state_pruned_count += 1

        return {
            "cleanup_performed": True,
            "deleted": deleted,
            "deleted_items": deleted_items,
            "deleted_count": len(deleted),
            "state_pruned_count": state_pruned_count,
            "failed": failed,
        }

    def update_official_assembly(self, assembly_guid: str) -> Dict[str, Any]:
        """Update a single official assembly from the active Aurora or bundled source."""

        guid = _coerce_guid(assembly_guid)
        if not guid:
            raise ValueError("assembly_guid is required")
        manifest = self._active_manifest(
            force_remote=True,
            allow_existing_checkout_fallback=False,
            allow_bundled_fallback=False,
        )
        if not manifest.available:
            raise RuntimeError(manifest.error or "Official Aurora catalog is unavailable.")
        entry = manifest.get(guid)
        if entry is None:
            raise ValueError(f"Assembly '{guid}' not found in the official catalog.")

        document = self._load_entry_document(manifest, entry)
        runtime = self._runtime_service()
        runtime.import_assembly(
            domain=AssemblyDomain.OFFICIAL,
            document=document,
            assembly_guid=entry.assembly_guid,
        )
        runtime.flush_writes()
        self._db_manager.upsert_official_catalog_state(
            entry.assembly_guid,
            bundled_hash=entry.content_hash if manifest.source == "bundled" else None,
            remote_hash=entry.content_hash if manifest.source != "bundled" else None,
            catalog_hash=entry.content_hash,
            applied_hash=entry.content_hash,
            last_applied_source=manifest.source,
            repo_url=manifest.repo_url,
            source_url=entry.source_url or manifest.repo_url,
            source_repo=entry.source_repo,
            source_path=entry.source_path,
            source_version=entry.source_version or manifest.catalog_version,
            last_catalog_sync_at=_utcnow_iso(),
        )
        return runtime.get_assembly(entry.assembly_guid) or {}

    def update_all_official_assemblies(self) -> Dict[str, Any]:
        """Update every official assembly that differs from the active Aurora or bundled source."""

        manifest = self._active_manifest(
            force_remote=True,
            allow_existing_checkout_fallback=False,
            allow_bundled_fallback=False,
        )
        if not manifest.available:
            return {
                "updated": [],
                "updated_items": [],
                "skipped": 0,
                "failed": [],
                "source": manifest.source,
                "repo_url": manifest.repo_url,
                "warning": "",
                "error": manifest.error,
            }

        runtime = self._runtime_service()
        current_items = runtime.list_assemblies(domain=AssemblyDomain.OFFICIAL.value)
        current_hashes = {
            _coerce_guid(item.get("assembly_guid") or item.get("assembly_id")): (
                _coerce_text(item.get("content_hash")) or compute_api_item_content_hash(item)
            )
            for item in current_items
        }
        current_items_by_guid = {
            _coerce_guid(item.get("assembly_guid") or item.get("assembly_id")): item
            for item in current_items
        }

        updated: List[str] = []
        updated_items: List[Dict[str, Any]] = []
        installed: List[str] = []
        installed_items: List[Dict[str, Any]] = []
        failed: List[Dict[str, str]] = []
        skipped = 0
        for entry in manifest.entries.values():
            current_hash = current_hashes.get(entry.assembly_guid)
            current_item = current_items_by_guid.get(entry.assembly_guid)
            is_new_install = current_item is None
            metadata_refresh_needed = _entry_requires_metadata_refresh(current_item, entry)
            if current_hash and current_hash == entry.content_hash and not metadata_refresh_needed:
                skipped += 1
                continue
            try:
                document = self._load_entry_document(manifest, entry)
                runtime.import_assembly(
                    domain=AssemblyDomain.OFFICIAL,
                    document=document,
                    assembly_guid=entry.assembly_guid,
                )
                self._db_manager.upsert_official_catalog_state(
                    entry.assembly_guid,
                    bundled_hash=entry.content_hash if manifest.source == "bundled" else None,
                    remote_hash=entry.content_hash if manifest.source != "bundled" else None,
                    catalog_hash=entry.content_hash,
                    applied_hash=entry.content_hash,
                    last_applied_source=manifest.source,
                    repo_url=manifest.repo_url,
                    source_url=entry.source_url or manifest.repo_url,
                    source_repo=entry.source_repo,
                    source_path=entry.source_path,
                    source_version=entry.source_version or manifest.catalog_version,
                    last_catalog_sync_at=_utcnow_iso(),
                )
                updated.append(entry.assembly_guid)
                refreshed = runtime.get_assembly(entry.assembly_guid) or {}
                refreshed_item = {
                    "assembly_guid": entry.assembly_guid,
                    "display_name": _coerce_text(
                        refreshed.get("display_name"),
                        entry.display_name,
                    ),
                    "source_path": _normalize_relative_path(
                        refreshed.get("source_path") or entry.source_path
                    ),
                    "source_version": _coerce_optional_text(entry.source_version or manifest.catalog_version),
                }
                updated_items.append(refreshed_item)
                if is_new_install:
                    installed.append(entry.assembly_guid)
                    installed_items.append(refreshed_item)
            except Exception as exc:
                failed.append({"assembly_guid": entry.assembly_guid, "error": str(exc)})
                self._logger.exception("Failed to update official assembly %s from %s", entry.assembly_guid, manifest.source)

        if updated:
            runtime.flush_writes()

        cleanup_result = self.cleanup_deleted_official_assemblies(manifest=manifest)
        cleanup_failed = list(cleanup_result.get("failed") or [])
        if cleanup_failed:
            failed.extend(cleanup_failed)

        return {
            "updated": updated,
            "updated_items": updated_items,
            "installed": installed,
            "installed_items": installed_items,
            "installed_count": len(installed),
            "updated_existing_count": max(len(updated) - len(installed), 0),
            "deleted": cleanup_result.get("deleted") or [],
            "deleted_items": cleanup_result.get("deleted_items") or [],
            "deleted_count": int(cleanup_result.get("deleted_count") or 0),
            "skipped": skipped,
            "failed": failed,
            "source": manifest.source,
            "repo_url": manifest.repo_url,
            "source_version": manifest.catalog_version,
            "warning": manifest.error if manifest.available else "",
            "error": manifest.error if not manifest.available else "",
        }

    # ------------------------------------------------------------------
    # CLI/export helpers
    # ------------------------------------------------------------------
    def write_bundled_snapshot(
        self,
        output_root: Path,
        *,
        repo_url: Optional[str] = None,
        source_version: Optional[str] = None,
    ) -> Path:
        """Write the current official table into a bundled manifest + JSON item files."""

        from .serialization import record_to_legacy_payload

        output_root = output_root.resolve()
        items_root = output_root / "items"
        output_root.mkdir(parents=True, exist_ok=True)
        items_root.mkdir(parents=True, exist_ok=True)

        records = self._db_manager.load_all(AssemblyDomain.OFFICIAL)
        manifest_entries: List[Dict[str, Any]] = []
        resolved_repo_url = _coerce_text(repo_url, self._repo_url)

        for record in records:
            document = record_to_legacy_payload(
                record,
                domain=AssemblyDomain.OFFICIAL,
                payload_text=record.payload_json,
            )

            relative_path = Path("items") / f"{record.assembly_guid}.json"
            target_path = output_root / relative_path
            target_path.parent.mkdir(parents=True, exist_ok=True)
            target_path.write_text(json.dumps(document, indent=2, sort_keys=True), encoding="utf-8")

            manifest_entries.append(
                {
                    "assembly_guid": record.assembly_guid,
                    "display_name": record.display_name,
                    "summary": record.summary,
                    "assembly_type": record.assembly_type,
                    "assembly_subtype": record.assembly_subtype,
                    "content_hash": compute_catalog_content_hash(document),
                    "file": relative_path.as_posix(),
                    "source_url": _github_blob_url(
                        resolved_repo_url,
                        source_version or record.source_version or record.updated_at.isoformat(),
                        record.source_path or relative_path.as_posix(),
                    )
                    or resolved_repo_url,
                    "source_version": source_version or record.source_version or record.updated_at.isoformat(),
                }
            )

        manifest = {
            "catalog_version": source_version or str(int(time.time())),
            "generated_at": _utcnow_iso(),
            "repo_url": resolved_repo_url,
            "assemblies": manifest_entries,
        }
        manifest_path = output_root / MANIFEST_FILENAME
        manifest_path.write_text(json.dumps(manifest, indent=2, sort_keys=True), encoding="utf-8")
        return manifest_path

    def write_aurora_snapshot(
        self,
        output_root: Path,
        *,
        repo_url: Optional[str] = None,
        source_version: Optional[str] = None,
    ) -> Path:
        """Write the current official table into a human-readable Aurora-style folder tree."""

        from .serialization import record_to_legacy_payload

        output_root = output_root.resolve()
        output_root.mkdir(parents=True, exist_ok=True)
        records = self._db_manager.load_all(AssemblyDomain.OFFICIAL)
        resolved_repo_url = _coerce_text(repo_url, self._repo_url)

        for record in records:
            relative_path = _default_export_relative_path(record)
            target_path = output_root / Path(relative_path)
            target_path.parent.mkdir(parents=True, exist_ok=True)

            document = record_to_legacy_payload(
                record,
                domain=AssemblyDomain.OFFICIAL,
                payload_text=record.payload_json,
            )

            target_path.write_text(json.dumps(document, indent=2, sort_keys=True), encoding="utf-8")

        return output_root

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------
    def _runtime_service(self):
        from .service import AssemblyRuntimeService

        return AssemblyRuntimeService(self._cache, logger=self._logger)

    def _active_manifest(
        self,
        *,
        force_remote: bool = False,
        allow_remote_sync: bool = True,
        allow_existing_checkout_fallback: bool = True,
        allow_bundled_fallback: bool = True,
    ) -> OfficialCatalogManifest:
        repo_manifest = self._load_repo_manifest(
            force=force_remote,
            allow_remote_sync=allow_remote_sync,
            allow_existing_checkout_fallback=allow_existing_checkout_fallback,
        )
        if repo_manifest.available:
            return repo_manifest

        if not allow_bundled_fallback:
            if repo_manifest.error:
                return repo_manifest
            return OfficialCatalogManifest(
                source="aurora",
                repo_url=self._repo_url,
                repo_git_url=self._repo_git_url,
                repo_ref=self._repo_ref,
                catalog_version=None,
                generated_at=None,
                entries={},
                error="Aurora official catalog is unavailable.",
            )

        bundled = self._load_bundled_manifest()
        if bundled.available:
            if repo_manifest.error and not bundled.error:
                bundled.error = repo_manifest.error
            return bundled
        if repo_manifest.error:
            return repo_manifest
        return bundled

    def _load_bundled_manifest(self) -> OfficialCatalogManifest:
        if self._bundled_root is None:
            return OfficialCatalogManifest(
                source="bundled",
                repo_url=self._repo_url,
                repo_git_url=self._repo_git_url,
                repo_ref=self._repo_ref,
                catalog_version=None,
                generated_at=None,
                entries={},
                error="Bundled official catalog root not configured.",
            )
        return self._crawl_catalog_root(
            self._bundled_root,
            source="bundled",
            repo_url=self._repo_url,
            repo_git_url=self._repo_git_url,
            repo_ref=self._repo_ref,
        )

    def _load_repo_manifest(
        self,
        *,
        force: bool = False,
        allow_remote_sync: bool = True,
        allow_existing_checkout_fallback: bool = True,
    ) -> OfficialCatalogManifest:
        if self._checkout_root is None:
            return OfficialCatalogManifest(
                source="aurora",
                repo_url=self._repo_url,
                repo_git_url=self._repo_git_url,
                repo_ref=self._repo_ref,
                catalog_version=None,
                generated_at=None,
                entries={},
                error="Official assembly checkout root is not configured.",
            )

        now = time.time()
        with self._catalog_lock:
            if not force and self._catalog_cache is not None:
                if not allow_remote_sync or now - self._catalog_loaded_at < self._refresh_seconds:
                    return self._catalog_cache

            if not force and not allow_remote_sync:
                fallback = self._load_existing_checkout_manifest(error="")
                if fallback is not None:
                    fallback.error = None
                    self._catalog_cache = fallback
                    self._catalog_loaded_at = now
                    return fallback
                return OfficialCatalogManifest(
                    source="aurora",
                    repo_url=self._repo_url,
                    repo_git_url=self._repo_git_url,
                    repo_ref=self._repo_ref,
                    catalog_version=None,
                    generated_at=None,
                    entries={},
                )

            if (
                not force
                and self._catalog_cache is not None
                and now - self._catalog_loaded_at < self._refresh_seconds
            ):
                return self._catalog_cache

            try:
                checkout_path, commit_sha = self._refresh_repo_checkout()
                manifest = self._crawl_catalog_root(
                    checkout_path,
                    source="aurora",
                    repo_url=self._repo_url,
                    repo_git_url=self._repo_git_url,
                    repo_ref=self._repo_ref,
                    source_version=f"git:{commit_sha}",
                )
            except Exception as exc:
                self._logger.warning(
                    "Official assembly repo sync failed repo=%s ref=%s error=%s",
                    self._repo_git_url,
                    self._repo_ref,
                    exc,
                )
                fallback = None
                if allow_existing_checkout_fallback:
                    fallback = self._load_existing_checkout_manifest(error=str(exc))
                manifest = fallback or OfficialCatalogManifest(
                    source="aurora",
                    repo_url=self._repo_url,
                    repo_git_url=self._repo_git_url,
                    repo_ref=self._repo_ref,
                    catalog_version=None,
                    generated_at=None,
                    entries={},
                    error=f"Failed to sync Aurora repository: {exc}",
                )

            if manifest.available or not force or allow_existing_checkout_fallback:
                self._catalog_cache = manifest
                self._catalog_loaded_at = now
            return manifest

    def _load_existing_checkout_manifest(self, *, error: str) -> Optional[OfficialCatalogManifest]:
        active_checkout = self._active_checkout_dir()
        if not active_checkout.is_dir():
            return None
        commit_sha: Optional[str] = None
        try:
            completed = self._run_git(["rev-parse", "HEAD"], cwd=active_checkout, force_refresh=False)
            commit_sha = completed.stdout.strip() or None
        except Exception:
            commit_sha = None
        manifest = self._crawl_catalog_root(
            active_checkout,
            source="aurora",
            repo_url=self._repo_url,
            repo_git_url=self._repo_git_url,
            repo_ref=self._repo_ref,
            source_version=f"git:{commit_sha}" if commit_sha else None,
        )
        manifest.error = f"Failed to sync Aurora repository: {error}"
        return manifest

    def _crawl_catalog_root(
        self,
        root: Path,
        *,
        source: str,
        repo_url: str,
        repo_git_url: str,
        repo_ref: str,
        source_version: Optional[str] = None,
    ) -> OfficialCatalogManifest:
        root = root.resolve()
        if not root.is_dir():
            return OfficialCatalogManifest(
                source=source,
                repo_url=repo_url,
                repo_git_url=repo_git_url,
                repo_ref=repo_ref,
                catalog_version=source_version,
                generated_at=None,
                entries={},
                base_path=root,
                error=f"Official catalog root not found at {root}",
            )

        entries: Dict[str, OfficialCatalogEntry] = {}
        scanned_files = 0
        skipped_files = 0
        failed_files = 0

        for path in sorted(root.rglob("*.json")):
            rel_path = path.relative_to(root)
            if _should_ignore_catalog_path(rel_path):
                skipped_files += 1
                continue
            scanned_files += 1
            try:
                payload = json.loads(path.read_text(encoding="utf-8"))
                if not isinstance(payload, Mapping):
                    raise ValueError("assembly document must decode to a JSON object")
                normalized = self._normalize_catalog_document(
                    payload,
                    source_repo=repo_url,
                    source_path=rel_path.as_posix(),
                    source_version=source_version,
                )
                guid = _coerce_guid(normalized.get("assembly_guid"))
                if guid in entries:
                    raise ValueError(f"duplicate assembly_guid '{guid}' detected in official catalog")
                payload_size = _json_size_bytes(normalized.get("payload"))
                if payload_size > MAX_RECOMMENDED_PAYLOAD_BYTES:
                    self._logger.warning(
                        "Official assembly payload exceeds recommended size limit; guid=%s path=%s bytes=%s",
                        guid,
                        rel_path.as_posix(),
                        payload_size,
                    )
                entry = OfficialCatalogEntry(
                    assembly_guid=guid,
                    display_name=_coerce_text(normalized.get("display_name"), guid),
                    summary=_coerce_optional_text(normalized.get("summary")),
                    assembly_type=_coerce_text(normalized.get("assembly_type"), "script").lower(),
                    assembly_subtype=_coerce_optional_text(normalized.get("assembly_subtype")) or "powershell",
                    content_hash=_coerce_text(normalized.get("content_hash")),
                    file_path=rel_path.as_posix(),
                    source_repo=_coerce_text(normalized.get("source_repo"), repo_url),
                    source_path=_normalize_relative_path(normalized.get("source_path")) or rel_path.as_posix(),
                    source_version=_coerce_optional_text(normalized.get("source_version")) or source_version,
                    source_url=_coerce_optional_text(normalized.get("source_url"))
                    or _github_blob_url(
                        repo_url,
                        _coerce_optional_text(normalized.get("source_version")) or source_version,
                        _normalize_relative_path(normalized.get("source_path")) or rel_path.as_posix(),
                    )
                    or repo_url,
                    payload_size_bytes=payload_size,
                )
                entries[guid] = entry
            except Exception as exc:
                failed_files += 1
                self._logger.warning("Skipping official assembly file %s: %s", rel_path.as_posix(), exc)

        error: Optional[str] = None
        if not entries:
            error = f"No official assembly JSON documents were found in {root}"

        return OfficialCatalogManifest(
            source=source,
            repo_url=repo_url,
            repo_git_url=repo_git_url,
            repo_ref=repo_ref,
            catalog_version=source_version,
            generated_at=_utcnow_iso(),
            entries=entries,
            base_path=root,
            error=error,
            scanned_files=scanned_files,
            skipped_files=skipped_files,
            failed_files=failed_files,
        )

    def _normalize_catalog_document(
        self,
        document: Mapping[str, Any],
        *,
        source_repo: str,
        source_path: str,
        source_version: Optional[str],
    ) -> Dict[str, Any]:
        payload = _payload_from_document(document)
        if not isinstance(payload, (Mapping, list)):
            raise ValueError("official assembly payload must be a JSON object or array")

        assembly_guid = _coerce_guid(
            document.get("assembly_guid") or (payload.get("assembly_guid") if isinstance(payload, Mapping) else None)
        )
        if not assembly_guid:
            raise ValueError("official assembly document is missing assembly_guid")

        display_name = _coerce_text(
            document.get("display_name")
            or document.get("name")
            or document.get("tab_name")
            or (payload.get("display_name") if isinstance(payload, Mapping) else None)
            or (payload.get("name") if isinstance(payload, Mapping) else None)
            or (payload.get("tab_name") if isinstance(payload, Mapping) else None)
            or assembly_guid
        )
        if not display_name:
            raise ValueError("official assembly document is missing display_name/name")

        summary = _summary_from_document(document, payload)
        assembly_type = _infer_assembly_type(document, payload)
        assembly_subtype = _infer_assembly_subtype(document, payload, assembly_type)

        normalized_payload = payload
        if isinstance(payload, Mapping):
            normalized_payload = dict(payload)
            normalized_payload["assembly_guid"] = assembly_guid

        normalized_path = _normalize_relative_path(source_path) or source_path
        resolved_source_version = source_version or _coerce_optional_text(
            document.get("source_version") or (payload.get("source_version") if isinstance(payload, Mapping) else None)
        )

        normalized_document: Dict[str, Any] = {
            "assembly_guid": assembly_guid,
            "display_name": display_name,
            "summary": summary,
            "assembly_type": assembly_type,
            "assembly_subtype": assembly_subtype,
            "source_repo": source_repo,
            "source_path": normalized_path,
            "source_version": resolved_source_version,
            "payload": normalized_payload,
        }
        normalized_document["source_url"] = (
            _coerce_optional_text(document.get("source_url"))
            or _github_blob_url(source_repo, resolved_source_version, normalized_path)
            or source_repo
        )
        normalized_document["content_hash"] = compute_catalog_content_hash(normalized_document)
        return normalized_document

    def _load_entry_document(self, manifest: OfficialCatalogManifest, entry: OfficialCatalogEntry) -> Mapping[str, Any]:
        if manifest.base_path is None:
            raise RuntimeError("Official catalog root is unavailable.")
        source_path = (manifest.base_path / entry.file_path).resolve()
        payload = json.loads(source_path.read_text(encoding="utf-8"))
        if not isinstance(payload, Mapping):
            raise RuntimeError(f"Official catalog entry {entry.file_path} did not decode to an object.")
        return self._normalize_catalog_document(
            payload,
            source_repo=entry.source_repo,
            source_path=entry.file_path,
            source_version=entry.source_version or manifest.catalog_version,
        )

    def _active_checkout_dir(self) -> Path:
        checkout_root = self._checkout_root or Path.cwd()
        return checkout_root / _repo_checkout_name(self._repo_url, self._repo_git_url)

    def _refresh_repo_checkout(self) -> tuple[Path, str]:
        if self._checkout_root is None:
            raise RuntimeError("Official assembly checkout root is not configured.")

        checkout_root = self._checkout_root
        checkout_root.mkdir(parents=True, exist_ok=True)
        active_checkout = self._active_checkout_dir()
        self._logger.info(
            "Official assembly repo sync start repo=%s ref=%s checkout=%s",
            self._repo_git_url,
            self._repo_ref,
            active_checkout,
        )

        temp_checkout = Path(tempfile.mkdtemp(prefix="aurora-sync-", dir=str(checkout_root)))
        backup_checkout = active_checkout.with_name(f"{active_checkout.name}.previous")
        try:
            self._run_git(["init"], cwd=temp_checkout, force_refresh=True)
            self._run_git(["remote", "add", "origin", self._repo_git_url], cwd=temp_checkout, force_refresh=True)
            self._run_git(["fetch", "--depth", "1", "origin", self._repo_ref], cwd=temp_checkout, force_refresh=True)
            self._run_git(["checkout", "--detach", "FETCH_HEAD"], cwd=temp_checkout, force_refresh=True)
            commit_sha = self._run_git(["rev-parse", "HEAD"], cwd=temp_checkout, force_refresh=False).stdout.strip()
            if not commit_sha:
                raise RuntimeError("Git checkout completed without a resolved commit SHA.")

            if backup_checkout.exists():
                shutil.rmtree(backup_checkout, ignore_errors=True)
            if active_checkout.exists():
                active_checkout.rename(backup_checkout)
            temp_checkout.rename(active_checkout)
            if backup_checkout.exists():
                shutil.rmtree(backup_checkout, ignore_errors=True)

            self._logger.info(
                "Official assembly repo sync complete repo=%s ref=%s commit=%s",
                self._repo_git_url,
                self._repo_ref,
                commit_sha,
            )
            return active_checkout, commit_sha
        except Exception:
            shutil.rmtree(temp_checkout, ignore_errors=True)
            raise

    def _run_git(
        self,
        args: List[str],
        *,
        cwd: Path,
        force_refresh: bool,
    ) -> subprocess.CompletedProcess[str]:
        def _base_env() -> Dict[str, str]:
            env = os.environ.copy()
            env["GIT_TERMINAL_PROMPT"] = "0"
            env["GIT_ASKPASS"] = "true"
            return env

        def _run_once(env: Dict[str, str]) -> subprocess.CompletedProcess[str]:
            return subprocess.run(
                ["git", *args],
                cwd=str(cwd),
                env=env,
                capture_output=True,
                text=True,
                check=False,
            )

        env = _base_env()
        token = None
        if self._github is not None:
            try:
                token = self._github.load_token(force_refresh=force_refresh)
            except Exception:
                token = None
        if token:
            git_config_count = int(env.get("GIT_CONFIG_COUNT", "0") or "0")
            env["GIT_CONFIG_COUNT"] = str(git_config_count + 1)
            env[f"GIT_CONFIG_KEY_{git_config_count}"] = "http.extraHeader"
            env[f"GIT_CONFIG_VALUE_{git_config_count}"] = f"AUTHORIZATION: bearer {token}"

        completed = _run_once(env)
        if completed.returncode != 0:
            error_text = (completed.stderr or completed.stdout or "").strip() or f"git {' '.join(args)} failed"
            lowered = error_text.lower()
            if "could not read username for 'https://github.com'" in lowered or "authentication failed" in lowered:
                if token:
                    anonymous_retry = _run_once(_base_env())
                    if anonymous_retry.returncode == 0:
                        self._logger.info(
                            "Official assembly repo sync succeeded without GitHub token after token auth failed; repo=%s ref=%s",
                            self._repo_git_url,
                            self._repo_ref,
                        )
                        return anonymous_retry
                    raise RuntimeError(
                        "Failed to fetch Aurora from GitHub using the configured token. "
                        "Verify the token, repo URL/ref, and outbound network path, or remove the stored token so "
                        "public Aurora access can proceed anonymously."
                    )
                raise RuntimeError(
                    "Failed to fetch Aurora from GitHub over HTTPS. "
                    "Verify the repo URL/ref, outbound network or proxy settings, and any cached Git credentials. "
                    "If Aurora is private, configure /api/github/token."
                )
            raise RuntimeError(error_text)
        return completed


def export_bundled_snapshot(
    *,
    database_url: str,
    output_root: Path,
    repo_url: str = DEFAULT_OFFICIAL_REPO_URL,
    source_version: Optional[str] = None,
    logger: Optional[logging.Logger] = None,
) -> Path:
    """Export the current official table into a repo-bundled manifest and item files."""

    from ...assembly_management.bootstrap import AssemblyCache

    db_manager = AssemblyDatabaseManager(database_url=database_url, logger=logger)
    db_manager.initialise()
    cache = AssemblyCache.initialise(database_manager=db_manager, flush_interval_seconds=60.0, logger=logger)
    service = OfficialAssemblyCatalogService(
        cache=cache,
        database_manager=db_manager,
        logger=logger,
        repo_url=repo_url,
        repo_git_url=_repo_default_git_url(repo_url),
    )
    return service.write_bundled_snapshot(output_root, repo_url=repo_url, source_version=source_version)


def export_aurora_snapshot(
    *,
    database_url: str,
    output_root: Path,
    repo_url: str = DEFAULT_OFFICIAL_REPO_URL,
    source_version: Optional[str] = None,
    logger: Optional[logging.Logger] = None,
) -> Path:
    """Export the current official table into an Aurora-style folder tree."""

    from ...assembly_management.bootstrap import AssemblyCache

    db_manager = AssemblyDatabaseManager(database_url=database_url, logger=logger)
    db_manager.initialise()
    cache = AssemblyCache.initialise(database_manager=db_manager, flush_interval_seconds=60.0, logger=logger)
    service = OfficialAssemblyCatalogService(
        cache=cache,
        database_manager=db_manager,
        logger=logger,
        repo_url=repo_url,
        repo_git_url=_repo_default_git_url(repo_url),
    )
    return service.write_aurora_snapshot(output_root, repo_url=repo_url, source_version=source_version)


def _build_arg_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Official Borealis assembly catalog utilities.")
    subparsers = parser.add_subparsers(dest="command", required=True)

    export_parser = subparsers.add_parser(
        "export-bundled",
        help="Write the official assemblies table into a bundled catalog snapshot.",
    )
    export_parser.add_argument("--database-url", required=True, help="Engine PostgreSQL URL")
    export_parser.add_argument("--output-root", required=True, help="Bundled catalog output root")
    export_parser.add_argument("--repo-url", default=DEFAULT_OFFICIAL_REPO_URL, help="Official catalog repository URL")
    export_parser.add_argument("--source-version", default="", help="Optional version string recorded in the manifest")

    aurora_parser = subparsers.add_parser(
        "export-aurora",
        help="Write the official assemblies table into a human-readable Aurora folder tree.",
    )
    aurora_parser.add_argument("--database-url", required=True, help="Engine PostgreSQL URL")
    aurora_parser.add_argument("--output-root", required=True, help="Aurora-style output root")
    aurora_parser.add_argument("--repo-url", default=DEFAULT_OFFICIAL_REPO_URL, help="Official catalog repository URL")
    aurora_parser.add_argument("--source-version", default="", help="Optional source version string")
    return parser


def main(argv: Optional[List[str]] = None) -> int:
    parser = _build_arg_parser()
    args = parser.parse_args(argv)

    logger = logging.getLogger("borealis.engine.official_catalog")
    if args.command == "export-bundled":
        manifest_path = export_bundled_snapshot(
            database_url=str(args.database_url),
            output_root=Path(str(args.output_root)),
            repo_url=str(args.repo_url),
            source_version=str(args.source_version or "") or None,
            logger=logger,
        )
        print(manifest_path)
        return 0
    if args.command == "export-aurora":
        output_root = export_aurora_snapshot(
            database_url=str(args.database_url),
            output_root=Path(str(args.output_root)),
            repo_url=str(args.repo_url),
            source_version=str(args.source_version or "") or None,
            logger=logger,
        )
        print(output_root)
        return 0

    parser.error(f"Unsupported command: {args.command}")
    return 2


if __name__ == "__main__":  # pragma: no cover - CLI entry
    raise SystemExit(main())
