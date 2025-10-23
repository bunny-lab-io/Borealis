"""Filesystem-backed assembly management service."""

from __future__ import annotations

import base64
import json
import logging
import os
import re
import shutil
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

__all__ = [
    "AssemblyService",
    "AssemblyListing",
    "AssemblyLoadResult",
    "AssemblyMutationResult",
]


@dataclass(frozen=True, slots=True)
class AssemblyListing:
    """Listing payload for an assembly island."""

    root: Path
    items: List[Dict[str, Any]]
    folders: List[str]

    def to_dict(self) -> dict[str, Any]:
        return {
            "root": str(self.root),
            "items": self.items,
            "folders": self.folders,
        }


@dataclass(frozen=True, slots=True)
class AssemblyLoadResult:
    """Container describing a loaded assembly artifact."""

    payload: Dict[str, Any]

    def to_dict(self) -> Dict[str, Any]:
        return dict(self.payload)


@dataclass(frozen=True, slots=True)
class AssemblyMutationResult:
    """Mutation acknowledgement for create/edit/rename operations."""

    status: str = "ok"
    rel_path: Optional[str] = None

    def to_dict(self) -> Dict[str, Any]:
        payload: Dict[str, Any] = {"status": self.status}
        if self.rel_path:
            payload["rel_path"] = self.rel_path
        return payload


class AssemblyService:
    """Provide CRUD helpers for workflow/script/ansible assemblies."""

    _ISLAND_DIR_MAP = {
        "workflows": "Workflows",
        "workflow": "Workflows",
        "scripts": "Scripts",
        "script": "Scripts",
        "ansible": "Ansible_Playbooks",
        "ansible_playbooks": "Ansible_Playbooks",
        "ansible-playbooks": "Ansible_Playbooks",
        "playbooks": "Ansible_Playbooks",
    }

    _SCRIPT_EXTENSIONS = (".json", ".ps1", ".bat", ".sh")
    _ANSIBLE_EXTENSIONS = (".json", ".yml")

    def __init__(self, *, root: Path, logger: Optional[logging.Logger] = None) -> None:
        self._root = root.resolve()
        self._log = logger or logging.getLogger("borealis.engine.services.assemblies")
        try:
            self._root.mkdir(parents=True, exist_ok=True)
        except Exception as exc:  # pragma: no cover - defensive logging
            self._log.warning("failed to ensure assemblies root %s: %s", self._root, exc)

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------
    def list_items(self, island: str) -> AssemblyListing:
        root = self._resolve_island_root(island)
        root.mkdir(parents=True, exist_ok=True)

        items: List[Dict[str, Any]] = []
        folders: List[str] = []

        isl = (island or "").strip().lower()
        if isl in {"workflows", "workflow"}:
            for dirpath, dirnames, filenames in os.walk(root):
                rel_root = os.path.relpath(dirpath, root)
                if rel_root != ".":
                    folders.append(rel_root.replace(os.sep, "/"))
                for fname in filenames:
                    if not fname.lower().endswith(".json"):
                        continue
                    abs_path = Path(dirpath) / fname
                    rel_path = abs_path.relative_to(root).as_posix()
                    try:
                        mtime = abs_path.stat().st_mtime
                    except OSError:
                        mtime = 0.0
                    obj = self._safe_read_json(abs_path)
                    tab = self._extract_tab_name(obj)
                    items.append(
                        {
                            "file_name": fname,
                            "rel_path": rel_path,
                            "type": "workflow",
                            "tab_name": tab,
                            "last_edited": time.strftime(
                                "%Y-%m-%dT%H:%M:%S", time.localtime(mtime)
                            ),
                            "last_edited_epoch": mtime,
                        }
                    )
        elif isl in {"scripts", "script"}:
            for dirpath, dirnames, filenames in os.walk(root):
                rel_root = os.path.relpath(dirpath, root)
                if rel_root != ".":
                    folders.append(rel_root.replace(os.sep, "/"))
                for fname in filenames:
                    if not fname.lower().endswith(self._SCRIPT_EXTENSIONS):
                        continue
                    abs_path = Path(dirpath) / fname
                    rel_path = abs_path.relative_to(root).as_posix()
                    try:
                        mtime = abs_path.stat().st_mtime
                    except OSError:
                        mtime = 0.0
                    script_type = self._detect_script_type(abs_path)
                    doc = self._load_assembly_document(abs_path, "scripts", script_type)
                    items.append(
                        {
                            "file_name": fname,
                            "rel_path": rel_path,
                            "type": doc.get("type", script_type),
                            "name": doc.get("name"),
                            "category": doc.get("category"),
                            "description": doc.get("description"),
                            "last_edited": time.strftime(
                                "%Y-%m-%dT%H:%M:%S", time.localtime(mtime)
                            ),
                            "last_edited_epoch": mtime,
                        }
                    )
        elif isl in {
            "ansible",
            "ansible_playbooks",
            "ansible-playbooks",
            "playbooks",
        }:
            for dirpath, dirnames, filenames in os.walk(root):
                rel_root = os.path.relpath(dirpath, root)
                if rel_root != ".":
                    folders.append(rel_root.replace(os.sep, "/"))
                for fname in filenames:
                    if not fname.lower().endswith(self._ANSIBLE_EXTENSIONS):
                        continue
                    abs_path = Path(dirpath) / fname
                    rel_path = abs_path.relative_to(root).as_posix()
                    try:
                        mtime = abs_path.stat().st_mtime
                    except OSError:
                        mtime = 0.0
                    script_type = self._detect_script_type(abs_path)
                    doc = self._load_assembly_document(abs_path, "ansible", script_type)
                    items.append(
                        {
                            "file_name": fname,
                            "rel_path": rel_path,
                            "type": doc.get("type", "ansible"),
                            "name": doc.get("name"),
                            "category": doc.get("category"),
                            "description": doc.get("description"),
                            "last_edited": time.strftime(
                                "%Y-%m-%dT%H:%M:%S", time.localtime(mtime)
                            ),
                            "last_edited_epoch": mtime,
                        }
                    )
        else:
            raise ValueError("invalid_island")

        items.sort(key=lambda entry: entry.get("last_edited_epoch", 0.0), reverse=True)
        return AssemblyListing(root=root, items=items, folders=folders)

    def load_item(self, island: str, rel_path: str) -> AssemblyLoadResult:
        root, abs_path, _ = self._resolve_assembly_path(island, rel_path)
        if not abs_path.is_file():
            raise FileNotFoundError("file_not_found")

        isl = (island or "").strip().lower()
        if isl in {"workflows", "workflow"}:
            payload = self._safe_read_json(abs_path)
            return AssemblyLoadResult(payload=payload)

        doc = self._load_assembly_document(abs_path, island)
        rel = abs_path.relative_to(root).as_posix()
        payload = {
            "file_name": abs_path.name,
            "rel_path": rel,
            "type": doc.get("type"),
            "assembly": doc,
            "content": doc.get("script"),
        }
        return AssemblyLoadResult(payload=payload)

    def create_item(
        self,
        island: str,
        *,
        kind: str,
        rel_path: str,
        content: Any,
        item_type: Optional[str] = None,
    ) -> AssemblyMutationResult:
        root, abs_path, rel_norm = self._resolve_assembly_path(island, rel_path)
        if not rel_norm:
            raise ValueError("path_required")

        normalized_kind = (kind or "").strip().lower()
        if normalized_kind == "folder":
            abs_path.mkdir(parents=True, exist_ok=True)
            return AssemblyMutationResult()
        if normalized_kind != "file":
            raise ValueError("invalid_kind")

        target_path = abs_path
        if not target_path.suffix:
            target_path = target_path.with_suffix(
                self._default_ext_for_island(island, item_type or "")
            )
        target_path.parent.mkdir(parents=True, exist_ok=True)

        isl = (island or "").strip().lower()
        if isl in {"workflows", "workflow"}:
            payload = self._ensure_workflow_document(content)
            base_name = target_path.stem
            payload.setdefault("tab_name", base_name)
            self._write_json(target_path, payload)
        else:
            document = self._normalize_assembly_document(
                content,
                self._default_type_for_island(island, item_type or ""),
                target_path.stem,
            )
            self._write_json(target_path, self._prepare_assembly_storage(document))

        rel_new = target_path.relative_to(root).as_posix()
        return AssemblyMutationResult(rel_path=rel_new)

    def edit_item(
        self,
        island: str,
        *,
        rel_path: str,
        content: Any,
        item_type: Optional[str] = None,
    ) -> AssemblyMutationResult:
        root, abs_path, _ = self._resolve_assembly_path(island, rel_path)
        if not abs_path.exists():
            raise FileNotFoundError("file_not_found")

        target_path = abs_path
        if not target_path.suffix:
            target_path = target_path.with_suffix(
                self._default_ext_for_island(island, item_type or "")
            )

        isl = (island or "").strip().lower()
        if isl in {"workflows", "workflow"}:
            payload = self._ensure_workflow_document(content)
            self._write_json(target_path, payload)
        else:
            document = self._normalize_assembly_document(
                content,
                self._default_type_for_island(island, item_type or ""),
                target_path.stem,
            )
            self._write_json(target_path, self._prepare_assembly_storage(document))

        if target_path != abs_path and abs_path.exists():
            try:
                abs_path.unlink()
            except OSError:  # pragma: no cover - best effort cleanup
                pass

        rel_new = target_path.relative_to(root).as_posix()
        return AssemblyMutationResult(rel_path=rel_new)

    def rename_item(
        self,
        island: str,
        *,
        kind: str,
        rel_path: str,
        new_name: str,
        item_type: Optional[str] = None,
    ) -> AssemblyMutationResult:
        root, old_path, _ = self._resolve_assembly_path(island, rel_path)

        normalized_kind = (kind or "").strip().lower()
        if normalized_kind not in {"file", "folder"}:
            raise ValueError("invalid_kind")

        if normalized_kind == "folder":
            if not old_path.is_dir():
                raise FileNotFoundError("folder_not_found")
            destination = old_path.parent / new_name
        else:
            if not old_path.is_file():
                raise FileNotFoundError("file_not_found")
            candidate = Path(new_name)
            if not candidate.suffix:
                candidate = candidate.with_suffix(
                    self._default_ext_for_island(island, item_type or "")
                )
            destination = old_path.parent / candidate.name

        destination = destination.resolve()
        if not str(destination).startswith(str(root)):
            raise ValueError("invalid_destination")

        old_path.rename(destination)

        isl = (island or "").strip().lower()
        if normalized_kind == "file" and isl in {"workflows", "workflow"}:
            try:
                obj = self._safe_read_json(destination)
                base_name = destination.stem
                for key in ["tabName", "tab_name", "name", "title"]:
                    if key in obj:
                        obj[key] = base_name
                obj.setdefault("tab_name", base_name)
                self._write_json(destination, obj)
            except Exception:  # pragma: no cover - best effort update
                self._log.debug("failed to normalize workflow metadata for %s", destination)

        rel_new = destination.relative_to(root).as_posix()
        return AssemblyMutationResult(rel_path=rel_new)

    def move_item(
        self,
        island: str,
        *,
        rel_path: str,
        new_path: str,
        kind: Optional[str] = None,
    ) -> AssemblyMutationResult:
        root, old_path, _ = self._resolve_assembly_path(island, rel_path)
        _, dest_path, _ = self._resolve_assembly_path(island, new_path)

        normalized_kind = (kind or "").strip().lower()
        if normalized_kind == "folder":
            if not old_path.is_dir():
                raise FileNotFoundError("folder_not_found")
        else:
            if not old_path.exists():
                raise FileNotFoundError("file_not_found")

        dest_path.parent.mkdir(parents=True, exist_ok=True)
        shutil.move(str(old_path), str(dest_path))
        return AssemblyMutationResult()

    def delete_item(
        self,
        island: str,
        *,
        rel_path: str,
        kind: str,
    ) -> AssemblyMutationResult:
        _, abs_path, rel_norm = self._resolve_assembly_path(island, rel_path)
        if not rel_norm:
            raise ValueError("cannot_delete_root")

        normalized_kind = (kind or "").strip().lower()
        if normalized_kind == "folder":
            if not abs_path.is_dir():
                raise FileNotFoundError("folder_not_found")
            shutil.rmtree(abs_path)
        elif normalized_kind == "file":
            if not abs_path.is_file():
                raise FileNotFoundError("file_not_found")
            abs_path.unlink()
        else:
            raise ValueError("invalid_kind")

        return AssemblyMutationResult()

    # ------------------------------------------------------------------
    # Helpers
    # ------------------------------------------------------------------
    def _resolve_island_root(self, island: str) -> Path:
        key = (island or "").strip().lower()
        subdir = self._ISLAND_DIR_MAP.get(key)
        if not subdir:
            raise ValueError("invalid_island")
        root = (self._root / subdir).resolve()
        root.mkdir(parents=True, exist_ok=True)
        return root

    def _resolve_assembly_path(self, island: str, rel_path: str) -> Tuple[Path, Path, str]:
        root = self._resolve_island_root(island)
        rel_norm = self._normalize_relpath(rel_path)
        abs_path = (root / rel_norm).resolve()
        if not str(abs_path).startswith(str(root)):
            raise ValueError("invalid_path")
        return root, abs_path, rel_norm

    @staticmethod
    def _normalize_relpath(value: str) -> str:
        return (value or "").replace("\\", "/").strip("/")

    @staticmethod
    def _default_ext_for_island(island: str, item_type: str) -> str:
        isl = (island or "").strip().lower()
        if isl in {"workflows", "workflow"}:
            return ".json"
        if isl in {"ansible", "ansible_playbooks", "ansible-playbooks", "playbooks"}:
            return ".json"
        if isl in {"scripts", "script"}:
            return ".json"
        typ = (item_type or "").strip().lower()
        if typ in {"bash", "batch", "powershell"}:
            return ".json"
        return ".json"

    @staticmethod
    def _default_type_for_island(island: str, item_type: str) -> str:
        isl = (island or "").strip().lower()
        if isl in {"ansible", "ansible_playbooks", "ansible-playbooks", "playbooks"}:
            return "ansible"
        typ = (item_type or "").strip().lower()
        if typ in {"powershell", "batch", "bash", "ansible"}:
            return typ
        return "powershell"

    @staticmethod
    def _empty_assembly_document(default_type: str) -> Dict[str, Any]:
        return {
            "version": 1,
            "name": "",
            "description": "",
            "category": "application" if default_type.lower() == "ansible" else "script",
            "type": default_type or "powershell",
            "script": "",
            "timeout_seconds": 3600,
            "sites": {"mode": "all", "values": []},
            "variables": [],
            "files": [],
        }

    @staticmethod
    def _decode_base64_text(value: Any) -> Optional[str]:
        if not isinstance(value, str):
            return None
        stripped = value.strip()
        if not stripped:
            return ""
        try:
            cleaned = re.sub(r"\s+", "", stripped)
        except Exception:
            cleaned = stripped
        try:
            decoded = base64.b64decode(cleaned, validate=True)
        except Exception:
            return None
        try:
            return decoded.decode("utf-8")
        except Exception:
            return decoded.decode("utf-8", errors="replace")

    def _decode_script_content(self, value: Any, encoding_hint: str = "") -> str:
        encoding = (encoding_hint or "").strip().lower()
        if isinstance(value, str):
            if encoding in {"base64", "b64", "base-64"}:
                decoded = self._decode_base64_text(value)
                if decoded is not None:
                    return decoded.replace("\r\n", "\n")
            decoded = self._decode_base64_text(value)
            if decoded is not None:
                return decoded.replace("\r\n", "\n")
            return value.replace("\r\n", "\n")
        return ""

    @staticmethod
    def _encode_script_content(script_text: Any) -> str:
        if not isinstance(script_text, str):
            if script_text is None:
                script_text = ""
            else:
                script_text = str(script_text)
        normalized = script_text.replace("\r\n", "\n")
        if not normalized:
            return ""
        encoded = base64.b64encode(normalized.encode("utf-8"))
        return encoded.decode("ascii")

    def _prepare_assembly_storage(self, document: Dict[str, Any]) -> Dict[str, Any]:
        stored: Dict[str, Any] = {}
        for key, value in (document or {}).items():
            if key == "script":
                stored[key] = self._encode_script_content(value)
            else:
                stored[key] = value
        stored["script_encoding"] = "base64"
        return stored

    def _normalize_assembly_document(
        self,
        obj: Any,
        default_type: str,
        base_name: str,
    ) -> Dict[str, Any]:
        doc = self._empty_assembly_document(default_type)
        if not isinstance(obj, dict):
            obj = {}
        base = (base_name or "assembly").strip()
        doc["name"] = str(obj.get("name") or obj.get("display_name") or base)
        doc["description"] = str(obj.get("description") or "")
        category = str(obj.get("category") or doc["category"]).strip().lower()
        if category in {"script", "application"}:
            doc["category"] = category
        typ = str(obj.get("type") or obj.get("script_type") or default_type or "powershell").strip().lower()
        if typ in {"powershell", "batch", "bash", "ansible"}:
            doc["type"] = typ
        script_val = obj.get("script")
        content_val = obj.get("content")
        script_lines = obj.get("script_lines")
        if isinstance(script_lines, list):
            try:
                doc["script"] = "\n".join(str(line) for line in script_lines)
            except Exception:
                doc["script"] = ""
        elif isinstance(script_val, str):
            doc["script"] = script_val
        elif isinstance(content_val, str):
            doc["script"] = content_val
        encoding_hint = str(
            obj.get("script_encoding") or obj.get("scriptEncoding") or ""
        ).strip().lower()
        doc["script"] = self._decode_script_content(doc.get("script"), encoding_hint)
        if encoding_hint in {"base64", "b64", "base-64"}:
            doc["script_encoding"] = "base64"
        else:
            probe_source = ""
            if isinstance(script_val, str) and script_val:
                probe_source = script_val
            elif isinstance(content_val, str) and content_val:
                probe_source = content_val
            decoded_probe = self._decode_base64_text(probe_source) if probe_source else None
            if decoded_probe is not None:
                doc["script_encoding"] = "base64"
                doc["script"] = decoded_probe.replace("\r\n", "\n")
            else:
                doc["script_encoding"] = "plain"
        timeout_val = obj.get("timeout_seconds", obj.get("timeout"))
        if timeout_val is not None:
            try:
                doc["timeout_seconds"] = max(0, int(timeout_val))
            except Exception:
                pass
        sites = obj.get("sites") if isinstance(obj.get("sites"), dict) else {}
        values = sites.get("values") if isinstance(sites.get("values"), list) else []
        mode = str(sites.get("mode") or ("specific" if values else "all")).strip().lower()
        if mode not in {"all", "specific"}:
            mode = "all"
        doc["sites"] = {
            "mode": mode,
            "values": [
                str(v).strip()
                for v in values
                if isinstance(v, (str, int, float)) and str(v).strip()
            ],
        }
        vars_in = obj.get("variables") if isinstance(obj.get("variables"), list) else []
        doc_vars: List[Dict[str, Any]] = []
        for entry in vars_in:
            if not isinstance(entry, dict):
                continue
            name = str(entry.get("name") or entry.get("key") or "").strip()
            if not name:
                continue
            vtype = str(entry.get("type") or "string").strip().lower()
            if vtype not in {"string", "number", "boolean", "credential"}:
                vtype = "string"
            default_val = entry.get("default", entry.get("default_value"))
            doc_vars.append(
                {
                    "name": name,
                    "label": str(entry.get("label") or ""),
                    "type": vtype,
                    "default": default_val,
                    "required": bool(entry.get("required")),
                    "description": str(entry.get("description") or ""),
                }
            )
        doc["variables"] = doc_vars
        files_in = obj.get("files") if isinstance(obj.get("files"), list) else []
        doc_files: List[Dict[str, Any]] = []
        for record in files_in:
            if not isinstance(record, dict):
                continue
            fname = record.get("file_name") or record.get("name")
            data = record.get("data")
            if not fname or not isinstance(data, str):
                continue
            size_val = record.get("size")
            try:
                size_int = int(size_val)
            except Exception:
                size_int = 0
            doc_files.append(
                {
                    "file_name": str(fname),
                    "size": size_int,
                    "mime_type": str(record.get("mime_type") or record.get("mimeType") or ""),
                    "data": data,
                }
            )
        doc["files"] = doc_files
        try:
            doc["version"] = int(obj.get("version") or doc["version"])
        except Exception:
            pass
        return doc

    def _load_assembly_document(
        self,
        abs_path: Path,
        island: str,
        type_hint: str = "",
    ) -> Dict[str, Any]:
        base_name = abs_path.stem
        default_type = self._default_type_for_island(island, type_hint)
        if abs_path.suffix.lower() == ".json":
            data = self._safe_read_json(abs_path)
            return self._normalize_assembly_document(data, default_type, base_name)
        try:
            content = abs_path.read_text(encoding="utf-8", errors="replace")
        except Exception:
            content = ""
        document = self._empty_assembly_document(default_type)
        document["name"] = base_name
        document["script"] = (content or "").replace("\r\n", "\n")
        if default_type == "ansible":
            document["category"] = "application"
        return document

    @staticmethod
    def _safe_read_json(path: Path) -> Dict[str, Any]:
        try:
            return json.loads(path.read_text(encoding="utf-8"))
        except Exception:
            return {}

    @staticmethod
    def _extract_tab_name(obj: Dict[str, Any]) -> str:
        if not isinstance(obj, dict):
            return ""
        for key in ["tabName", "tab_name", "name", "title"]:
            value = obj.get(key)
            if isinstance(value, str) and value.strip():
                return value.strip()
        return ""

    def _detect_script_type(self, path: Path) -> str:
        lower = path.name.lower()
        if lower.endswith(".json") and path.is_file():
            obj = self._safe_read_json(path)
            if isinstance(obj, dict):
                typ = str(
                    obj.get("type") or obj.get("script_type") or ""
                ).strip().lower()
                if typ in {"powershell", "batch", "bash", "ansible"}:
                    return typ
            return "powershell"
        if lower.endswith(".yml"):
            return "ansible"
        if lower.endswith(".ps1"):
            return "powershell"
        if lower.endswith(".bat"):
            return "batch"
        if lower.endswith(".sh"):
            return "bash"
        return "unknown"

    @staticmethod
    def _ensure_workflow_document(content: Any) -> Dict[str, Any]:
        payload = content
        if isinstance(payload, str):
            try:
                payload = json.loads(payload)
            except Exception:
                payload = {}
        if not isinstance(payload, dict):
            payload = {}
        return payload

    @staticmethod
    def _write_json(path: Path, payload: Dict[str, Any]) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(payload, indent=2), encoding="utf-8")
