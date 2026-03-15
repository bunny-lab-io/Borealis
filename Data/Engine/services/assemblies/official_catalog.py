# ======================================================
# Data\Engine\services\assemblies\official_catalog.py
# Description: Official assembly catalog sync helpers for bundled snapshots and optional remote updates.
#
# API Endpoints (if applicable): None
# ======================================================

"""Official assembly catalog helpers."""

from __future__ import annotations

import argparse
import copy
import hashlib
import json
import logging
import time
from dataclasses import dataclass
from pathlib import Path
from typing import TYPE_CHECKING, Any, Dict, Iterable, List, Mapping, Optional
from urllib.parse import urljoin

from ...assembly_management.databases import AssemblyDatabaseManager, OfficialCatalogState
from ...assembly_management.models import AssemblyDomain, AssemblyRecord

if TYPE_CHECKING:  # pragma: no cover - typing only
    from ...assembly_management.bootstrap import AssemblyCache

try:  # pragma: no cover - graceful fallback mirrors other integration modules
    import requests  # type: ignore
except ImportError:  # pragma: no cover
    requests = None  # type: ignore


DEFAULT_OFFICIAL_REPO_URL = "https://example.com"
DEFAULT_REMOTE_REFRESH_SECONDS = 300
MANIFEST_FILENAME = "manifest.json"


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
    return dict(document)


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
        payload_value.setdefault("assembly_guid", assembly_guid)
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
    assembly_guid = _coerce_guid(document.get("assembly_guid") or (payload.get("assembly_guid") if isinstance(payload, Mapping) else None))
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
    summary = _coerce_optional_text(
        document.get("summary")
        or document.get("description")
        or (payload.get("summary") if isinstance(payload, Mapping) else None)
        or (payload.get("description") if isinstance(payload, Mapping) else None)
    )
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

    return _hash_content_fields(
        _stable_content_fields(
            assembly_guid=_coerce_guid(item.get("assembly_guid") or item.get("assembly_id")),
            display_name=_coerce_text(item.get("display_name"), item.get("assembly_guid") or "Assembly"),
            summary=_coerce_optional_text(item.get("summary")),
            assembly_type=_coerce_text(item.get("assembly_type"), "script").lower(),
            assembly_subtype=_coerce_optional_text(item.get("assembly_subtype")) or "powershell",
            payload=payload_value,
        )
    )


@dataclass(slots=True)
class OfficialCatalogEntry:
    """Manifest entry describing one managed official assembly."""

    assembly_guid: str
    display_name: str
    summary: Optional[str]
    assembly_type: str
    assembly_subtype: Optional[str]
    content_hash: str
    file_path: str
    source_url: Optional[str] = None
    source_version: Optional[str] = None
    download_url: Optional[str] = None


@dataclass(slots=True)
class OfficialCatalogManifest:
    """Loaded official catalog manifest from bundled or remote source."""

    source: str
    repo_url: str
    catalog_version: Optional[str]
    generated_at: Optional[str]
    entries: Dict[str, OfficialCatalogEntry]
    base_path: Optional[Path] = None
    base_url: Optional[str] = None
    error: Optional[str] = None

    @property
    def available(self) -> bool:
        return bool(self.entries)

    def get(self, assembly_guid: str) -> Optional[OfficialCatalogEntry]:
        return self.entries.get(_coerce_guid(assembly_guid))


class OfficialAssemblyCatalogService:
    """Synchronise bundled and remote official assembly catalogs into PostgreSQL."""

    def __init__(
        self,
        *,
        cache: AssemblyCache,
        database_manager: AssemblyDatabaseManager,
        logger: Optional[logging.Logger] = None,
        github_integration: Optional[Any] = None,
        bundled_root: Optional[Path] = None,
        repo_url: str = DEFAULT_OFFICIAL_REPO_URL,
        manifest_url: str = "",
        refresh_seconds: int = DEFAULT_REMOTE_REFRESH_SECONDS,
    ) -> None:
        self._cache = cache
        self._db_manager = database_manager
        self._logger = logger or logging.getLogger(__name__)
        self._github = github_integration
        self._bundled_root = bundled_root.resolve() if bundled_root else None
        self._repo_url = _coerce_text(repo_url, DEFAULT_OFFICIAL_REPO_URL)
        self._manifest_url = _coerce_text(manifest_url)
        self._refresh_seconds = max(30, int(refresh_seconds or DEFAULT_REMOTE_REFRESH_SECONDS))
        self._remote_manifest_cache: Optional[OfficialCatalogManifest] = None
        self._remote_manifest_loaded_at: float = 0.0

    # ------------------------------------------------------------------
    # Public operations
    # ------------------------------------------------------------------
    def sync_bundled_catalog(self) -> Dict[str, Any]:
        """Apply bundled official assemblies when the bundled snapshot changes."""

        manifest = self._load_bundled_manifest()
        if not manifest.available:
            if manifest.error:
                self._logger.debug("Bundled official catalog unavailable: %s", manifest.error)
            return {"source": "bundled", "updated": 0, "skipped": 0, "available": False, "error": manifest.error}

        state_map = self._db_manager.load_official_catalog_state()
        runtime = self._runtime_service()
        changed = 0
        skipped = 0

        for entry in manifest.entries.values():
            state = state_map.get(entry.assembly_guid)
            if state and state.bundled_hash == entry.content_hash:
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
                applied_hash=entry.content_hash,
                last_applied_source="bundled",
                repo_url=manifest.repo_url,
                source_url=entry.source_url or manifest.repo_url,
                source_version=entry.source_version or manifest.catalog_version,
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
        }

    def annotate_collection(self, items: Iterable[Mapping[str, Any]]) -> List[Dict[str, Any]]:
        """Attach official-catalog update metadata to assembly list payloads."""

        manifest = self._active_manifest()
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
            repo_url = manifest.repo_url or self._repo_url
            source_url = entry.source_url if entry else (state.source_url if state else repo_url)
            update_available = bool(entry and entry.content_hash and current_hash != entry.content_hash)

            item["official_managed"] = bool(entry or state)
            item["official_repo_url"] = repo_url
            item["official_source_url"] = source_url or repo_url
            item["official_catalog_source"] = manifest.source if manifest.available else "bundled"
            item["official_source_version"] = (
                entry.source_version if entry and entry.source_version else (state.source_version if state else None)
            )
            item["official_update_available"] = update_available
            item["official_last_applied_source"] = state.last_applied_source if state else None
            item["official_last_synced_at"] = state.updated_at.isoformat() if state and state.updated_at else None
            annotated.append(item)

        return annotated

    def catalog_status(self, items: Optional[Iterable[Mapping[str, Any]]] = None) -> Dict[str, Any]:
        """Return metadata describing the current official catalog source and update state."""

        manifest = self._active_manifest()
        update_count = 0
        if items is not None:
            for item in items:
                if bool(item.get("official_update_available")):
                    update_count += 1

        return {
            "repo_url": manifest.repo_url or self._repo_url,
            "source": manifest.source,
            "available": manifest.available,
            "manifest_url": self._manifest_url or None,
            "error": manifest.error,
            "update_count": update_count,
        }

    def update_official_assembly(self, assembly_guid: str) -> Dict[str, Any]:
        """Update a single official assembly from the active catalog source."""

        guid = _coerce_guid(assembly_guid)
        if not guid:
            raise ValueError("assembly_guid is required")
        manifest = self._active_manifest(force_remote=True)
        entry = manifest.get(guid)
        if entry is None:
            raise ValueError(f"Assembly '{guid}' not found in the official catalog.")

        document = self._load_entry_document(manifest, entry)
        runtime = self._runtime_service()
        record = runtime.import_assembly(
            domain=AssemblyDomain.OFFICIAL,
            document=document,
            assembly_guid=entry.assembly_guid,
        )
        runtime.flush_writes()
        self._db_manager.upsert_official_catalog_state(
            entry.assembly_guid,
            bundled_hash=entry.content_hash if manifest.source == "bundled" else None,
            remote_hash=entry.content_hash if manifest.source == "remote" else None,
            applied_hash=entry.content_hash,
            last_applied_source=manifest.source,
            repo_url=manifest.repo_url,
            source_url=entry.source_url or manifest.repo_url,
            source_version=entry.source_version or manifest.catalog_version,
        )
        return record

    def update_all_official_assemblies(self) -> Dict[str, Any]:
        """Update every official assembly that differs from the active catalog source."""

        manifest = self._active_manifest(force_remote=True)
        if not manifest.available:
            return {"updated": [], "skipped": 0, "source": manifest.source, "repo_url": manifest.repo_url, "error": manifest.error}

        runtime = self._runtime_service()
        current_items = runtime.list_assemblies(domain=AssemblyDomain.OFFICIAL.value)
        current_hashes = {
            _coerce_guid(item.get("assembly_guid") or item.get("assembly_id")): _coerce_text(item.get("content_hash"))
            for item in current_items
        }

        updated: List[str] = []
        skipped = 0
        for entry in manifest.entries.values():
            current_hash = current_hashes.get(entry.assembly_guid)
            if current_hash and current_hash == entry.content_hash:
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
                bundled_hash=entry.content_hash if manifest.source == "bundled" else None,
                remote_hash=entry.content_hash if manifest.source == "remote" else None,
                applied_hash=entry.content_hash,
                last_applied_source=manifest.source,
                repo_url=manifest.repo_url,
                source_url=entry.source_url or manifest.repo_url,
                source_version=entry.source_version or manifest.catalog_version,
            )
            updated.append(entry.assembly_guid)

        if updated:
            runtime.flush_writes()

        return {
            "updated": updated,
            "skipped": skipped,
            "source": manifest.source,
            "repo_url": manifest.repo_url,
            "error": manifest.error,
        }

    # ------------------------------------------------------------------
    # CLI/export helpers
    # ------------------------------------------------------------------
    def write_bundled_snapshot(self, output_root: Path, *, repo_url: Optional[str] = None, source_version: Optional[str] = None) -> Path:
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
            payload = document.get("payload")
            if isinstance(payload, Mapping):
                payload = dict(payload)
                payload.setdefault("assembly_guid", record.assembly_guid)
                document["payload"] = payload

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
                    "source_url": resolved_repo_url,
                    "source_version": source_version or record.updated_at.isoformat(),
                }
            )

        manifest = {
            "catalog_version": source_version or str(int(time.time())),
            "generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "repo_url": resolved_repo_url,
            "assemblies": manifest_entries,
        }
        manifest_path = output_root / MANIFEST_FILENAME
        manifest_path.write_text(json.dumps(manifest, indent=2, sort_keys=True), encoding="utf-8")
        return manifest_path

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------
    def _runtime_service(self):
        from .service import AssemblyRuntimeService

        return AssemblyRuntimeService(self._cache, logger=self._logger)

    def _active_manifest(self, *, force_remote: bool = False) -> OfficialCatalogManifest:
        remote = self._load_remote_manifest(force=force_remote)
        if remote.available:
            return remote
        bundled = self._load_bundled_manifest()
        if bundled.available:
            return bundled
        if remote.error:
            return remote
        return bundled

    def _load_bundled_manifest(self) -> OfficialCatalogManifest:
        if self._bundled_root is None:
            return OfficialCatalogManifest(
                source="bundled",
                repo_url=self._repo_url,
                catalog_version=None,
                generated_at=None,
                entries={},
                error="Bundled official catalog root not configured.",
            )
        manifest_path = self._bundled_root / MANIFEST_FILENAME
        if not manifest_path.is_file():
            return OfficialCatalogManifest(
                source="bundled",
                repo_url=self._repo_url,
                catalog_version=None,
                generated_at=None,
                entries={},
                base_path=self._bundled_root,
                error=f"Bundled official catalog manifest not found at {manifest_path}",
            )
        try:
            payload = json.loads(manifest_path.read_text(encoding="utf-8"))
        except Exception as exc:
            return OfficialCatalogManifest(
                source="bundled",
                repo_url=self._repo_url,
                catalog_version=None,
                generated_at=None,
                entries={},
                base_path=self._bundled_root,
                error=f"Failed to read bundled manifest: {exc}",
            )
        return self._manifest_from_payload(payload, source="bundled", base_path=self._bundled_root)

    def _load_remote_manifest(self, *, force: bool = False) -> OfficialCatalogManifest:
        if not self._manifest_url:
            return OfficialCatalogManifest(
                source="remote",
                repo_url=self._repo_url,
                catalog_version=None,
                generated_at=None,
                entries={},
                error="Remote official catalog manifest URL is not configured.",
            )

        now = time.time()
        if (
            not force
            and self._remote_manifest_cache is not None
            and now - self._remote_manifest_loaded_at < self._refresh_seconds
        ):
            return self._remote_manifest_cache

        headers = {
            "Accept": "application/json",
            "User-Agent": "Borealis-Engine",
        }
        token = None
        if self._github is not None:
            try:
                token = self._github.load_token(force_refresh=force)
            except Exception:
                token = None
        if token:
            headers["Authorization"] = f"Bearer {token}"

        if requests is None:
            manifest = OfficialCatalogManifest(
                source="remote",
                repo_url=self._repo_url,
                catalog_version=None,
                generated_at=None,
                entries={},
                base_url=self._manifest_url,
                error="The 'requests' package is required for remote official catalog updates.",
            )
            self._remote_manifest_cache = manifest
            self._remote_manifest_loaded_at = now
            return manifest

        try:
            response = requests.get(self._manifest_url, headers=headers, timeout=20)
            response.raise_for_status()
            payload = response.json()
            manifest = self._manifest_from_payload(payload, source="remote", base_url=self._manifest_url)
        except Exception as exc:
            manifest = OfficialCatalogManifest(
                source="remote",
                repo_url=self._repo_url,
                catalog_version=None,
                generated_at=None,
                entries={},
                base_url=self._manifest_url,
                error=f"Failed to fetch remote official catalog manifest: {exc}",
            )

        self._remote_manifest_cache = manifest
        self._remote_manifest_loaded_at = now
        return manifest

    def _manifest_from_payload(
        self,
        payload: Mapping[str, Any],
        *,
        source: str,
        base_path: Optional[Path] = None,
        base_url: Optional[str] = None,
    ) -> OfficialCatalogManifest:
        repo_url = _coerce_text(payload.get("repo_url"), self._repo_url)
        entries_payload = payload.get("assemblies")
        entries: Dict[str, OfficialCatalogEntry] = {}
        if isinstance(entries_payload, list):
            for entry_payload in entries_payload:
                if not isinstance(entry_payload, Mapping):
                    continue
                guid = _coerce_guid(entry_payload.get("assembly_guid"))
                file_path = _coerce_text(entry_payload.get("file"))
                content_hash = _coerce_text(entry_payload.get("content_hash"))
                if not guid or not file_path or not content_hash:
                    continue
                entries[guid] = OfficialCatalogEntry(
                    assembly_guid=guid,
                    display_name=_coerce_text(entry_payload.get("display_name"), guid),
                    summary=_coerce_optional_text(entry_payload.get("summary")),
                    assembly_type=_coerce_text(entry_payload.get("assembly_type"), "script").lower(),
                    assembly_subtype=_coerce_optional_text(entry_payload.get("assembly_subtype")) or "powershell",
                    content_hash=content_hash,
                    file_path=file_path,
                    source_url=_coerce_optional_text(entry_payload.get("source_url")) or repo_url,
                    source_version=_coerce_optional_text(entry_payload.get("source_version")),
                    download_url=_coerce_optional_text(entry_payload.get("download_url")),
                )

        return OfficialCatalogManifest(
            source=source,
            repo_url=repo_url,
            catalog_version=_coerce_optional_text(payload.get("catalog_version")),
            generated_at=_coerce_optional_text(payload.get("generated_at")),
            entries=entries,
            base_path=base_path,
            base_url=base_url,
        )

    def _load_entry_document(self, manifest: OfficialCatalogManifest, entry: OfficialCatalogEntry) -> Mapping[str, Any]:
        if manifest.source == "bundled":
            if manifest.base_path is None:
                raise RuntimeError("Bundled catalog root is unavailable.")
            source_path = (manifest.base_path / entry.file_path).resolve()
            return json.loads(source_path.read_text(encoding="utf-8"))

        headers = {
            "Accept": "application/json",
            "User-Agent": "Borealis-Engine",
        }
        token = None
        if self._github is not None:
            try:
                token = self._github.load_token(force_refresh=False)
            except Exception:
                token = None
        if token:
            headers["Authorization"] = f"Bearer {token}"

        if requests is None:
            raise RuntimeError("The 'requests' package is required for remote official catalog updates.")

        download_url = entry.download_url
        if not download_url:
            if manifest.base_url is None:
                raise RuntimeError("Remote manifest base URL is unavailable.")
            download_url = urljoin(manifest.base_url, entry.file_path)
        response = requests.get(download_url, headers=headers, timeout=20)
        response.raise_for_status()
        return response.json()


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
    )
    return service.write_bundled_snapshot(output_root, repo_url=repo_url, source_version=source_version)


def _build_arg_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Official Borealis assembly catalog utilities.")
    subparsers = parser.add_subparsers(dest="command", required=True)

    export_parser = subparsers.add_parser("export-bundled", help="Write the official assemblies table into a bundled catalog snapshot.")
    export_parser.add_argument("--database-url", required=True, help="Engine PostgreSQL URL")
    export_parser.add_argument("--output-root", required=True, help="Bundled catalog output root")
    export_parser.add_argument("--repo-url", default=DEFAULT_OFFICIAL_REPO_URL, help="Official catalog repository URL")
    export_parser.add_argument("--source-version", default="", help="Optional version string recorded in the manifest")
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

    parser.error(f"Unsupported command: {args.command}")
    return 2


if __name__ == "__main__":  # pragma: no cover - CLI entry
    raise SystemExit(main())
