# ======================================================
# Data\Engine\services\API\assemblies\management.py
# Description: Assembly CRUD endpoints for workflows, scripts, and Ansible documents during the Engine migration.
#
# API Endpoints (if applicable):
# - POST /api/assembly/create (Token Authenticated) - Creates a folder or assembly file within the requested island.
# - POST /api/assembly/edit (Token Authenticated) - Replaces the contents of an existing assembly.
# - POST /api/assembly/rename (Token Authenticated) - Renames an assembly file or folder.
# - POST /api/assembly/move (Token Authenticated) - Moves an assembly file or folder to a new location.
# - POST /api/assembly/delete (Token Authenticated) - Deletes an assembly file or folder.
# - GET /api/assembly/list (Token Authenticated) - Lists assemblies and folders for a given island.
# - GET /api/assembly/load (Token Authenticated) - Loads an assembly file and returns normalized metadata.
# ======================================================

"""Assembly management endpoints for the Borealis Engine API."""
from __future__ import annotations

import base64
import json
import logging
import os
import re
import shutil
import time
from pathlib import Path
from typing import TYPE_CHECKING, Any, Dict, List, Mapping, MutableMapping, Optional, Tuple

from flask import Blueprint, jsonify, request

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import EngineServiceAdapters


_ISLAND_DIR_MAP: Mapping[str, str] = {
    "workflows": "Workflows",
    "workflow": "Workflows",
    "scripts": "Scripts",
    "script": "Scripts",
    "ansible": "Ansible_Playbooks",
    "ansible_playbooks": "Ansible_Playbooks",
    "ansible-playbooks": "Ansible_Playbooks",
    "playbooks": "Ansible_Playbooks",
}

_BASE64_CLEANER = re.compile(r"\s+")



class AssemblyManagementService:
    """Implements assembly CRUD helpers for Engine routes."""

    def __init__(self, adapters: "EngineServiceAdapters") -> None:
        self.adapters = adapters
        self.logger = adapters.context.logger or logging.getLogger(__name__)
        self.service_log = adapters.service_log
        self._base_root = self._discover_assemblies_root()
        self._log_action("init", f"assemblies root set to {self._base_root}")

    def _discover_assemblies_root(self) -> Path:
        module_path = Path(__file__).resolve()
        for candidate in (module_path, *module_path.parents):
            engine_dir = candidate / "Engine"
            assemblies_dir = engine_dir / "Assemblies"
            if assemblies_dir.is_dir():
                return assemblies_dir.resolve()

        raise RuntimeError("Engine assemblies directory not found; expected <ProjectRoot>/Engine/Assemblies.")

    # ------------------------------------------------------------------
    # Path helpers
    # ------------------------------------------------------------------
    def _normalize_relpath(self, value: str) -> str:
        return (value or "").replace("\\", "/").strip("/")

    def _resolve_island_root(self, island: str) -> Optional[str]:
        subdir = _ISLAND_DIR_MAP.get((island or "").strip().lower())
        if not subdir:
            return None
        root = (self._base_root / subdir).resolve()
        return str(root)

    def _resolve_assembly_path(self, island: str, rel_path: str) -> Tuple[str, str, str]:
        root = self._resolve_island_root(island)
        if not root:
            raise ValueError("invalid island")
        rel_norm = self._normalize_relpath(rel_path)
        abs_path = os.path.abspath(os.path.join(root, rel_norm))
        if not abs_path.startswith(root):
            raise ValueError("invalid path")
        return root, abs_path, rel_norm

    # ------------------------------------------------------------------
    # Document helpers
    # ------------------------------------------------------------------
    def _default_ext_for_island(self, island: str, item_type: str = "") -> str:
        isl = (island or "").lower().strip()
        if isl in ("workflows", "workflow"):
            return ".json"
        if isl in ("ansible", "ansible_playbooks", "ansible-playbooks", "playbooks"):
            return ".json"
        if isl in ("scripts", "script"):
            return ".json"
        typ = (item_type or "").lower().strip()
        if typ in ("bash", "batch", "powershell"):
            return ".json"
        return ".json"

    def _default_type_for_island(self, island: str, item_type: str = "") -> str:
        isl = (island or "").lower().strip()
        if isl in ("ansible", "ansible_playbooks", "ansible-playbooks", "playbooks"):
            return "ansible"
        typ = (item_type or "").lower().strip()
        if typ in ("powershell", "batch", "bash", "ansible"):
            return typ
        return "powershell"

    def _empty_document(self, default_type: str = "powershell") -> Dict[str, Any]:
        return {
            "version": 1,
            "name": "",
            "description": "",
            "category": "application" if (default_type or "").lower() == "ansible" else "script",
            "type": default_type or "powershell",
            "script": "",
            "timeout_seconds": 3600,
            "sites": {"mode": "all", "values": []},
            "variables": [],
            "files": [],
        }

    def _decode_base64_text(self, value: Any) -> Optional[str]:
        if not isinstance(value, str):
            return None
        stripped = value.strip()
        if not stripped:
            return ""
        try:
            cleaned = _BASE64_CLEANER.sub("", stripped)
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
            if encoding in ("base64", "b64", "base-64"):
                decoded = self._decode_base64_text(value)
                if decoded is not None:
                    return decoded.replace("\r\n", "\n")
            decoded = self._decode_base64_text(value)
            if decoded is not None:
                return decoded.replace("\r\n", "\n")
            return value.replace("\r\n", "\n")
        return ""

    def _encode_script_content(self, script_text: Any) -> str:
        if not isinstance(script_text, str):
            script_text = "" if script_text is None else str(script_text)
        normalized = script_text.replace("\r\n", "\n")
        if not normalized:
            return ""
        encoded = base64.b64encode(normalized.encode("utf-8"))
        return encoded.decode("ascii")

    def _prepare_storage(self, doc: Dict[str, Any]) -> Dict[str, Any]:
        stored: Dict[str, Any] = {}
        for key, value in (doc or {}).items():
            if key == "script":
                stored[key] = self._encode_script_content(value)
            else:
                stored[key] = value
        stored["script_encoding"] = "base64"
        return stored

    def _normalize_document(self, obj: Any, default_type: str, base_name: str) -> Dict[str, Any]:
        doc = self._empty_document(default_type)
        if not isinstance(obj, dict):
            obj = {}
        base = (base_name or "assembly").strip()
        doc["name"] = str(obj.get("name") or obj.get("display_name") or base)
        doc["description"] = str(obj.get("description") or "")
        category = str(obj.get("category") or doc["category"]).strip().lower()
        if category in ("script", "application"):
            doc["category"] = category
        typ = str(obj.get("type") or obj.get("script_type") or default_type or "powershell").strip().lower()
        if typ in ("powershell", "batch", "bash", "ansible"):
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
        else:
            doc["script"] = ""

        encoding_hint = str(obj.get("script_encoding") or obj.get("scriptEncoding") or "").strip().lower()
        doc["script"] = self._decode_script_content(doc.get("script"), encoding_hint)
        if encoding_hint in ("base64", "b64", "base-64"):
            doc["script_encoding"] = "base64"
        else:
            doc["script_encoding"] = "plain"

        timeout = obj.get("timeout_seconds")
        if isinstance(timeout, (int, float)) and timeout > 0:
            doc["timeout_seconds"] = int(timeout)

        sites = obj.get("sites")
        if isinstance(sites, dict):
            mode = str(sites.get("mode") or doc["sites"]["mode"]).strip().lower()
            if mode in ("all", "include", "exclude"):
                doc["sites"]["mode"] = mode
            values = sites.get("values")
            if isinstance(values, list):
                doc["sites"]["values"] = [str(v) for v in values if str(v).strip()]

        variables = obj.get("variables") or obj.get("variable_definitions")
        if isinstance(variables, list):
            normalized_vars: List[Dict[str, Any]] = []
            for entry in variables:
                if not isinstance(entry, dict):
                    continue
                normalized_vars.append(
                    {
                        "name": str(entry.get("name") or entry.get("variable") or "").strip(),
                        "label": str(entry.get("label") or "").strip(),
                        "description": str(entry.get("description") or "").strip(),
                        "type": str(entry.get("type") or "string").strip().lower() or "string",
                        "default": entry.get("default"),
                        "required": bool(entry.get("required")),
                    }
                )
            doc["variables"] = normalized_vars

        files = obj.get("files")
        if isinstance(files, list):
            normalized_files: List[Dict[str, Any]] = []
            for entry in files:
                if not isinstance(entry, dict):
                    continue
                normalized_files.append(
                    {
                        "file_name": str(entry.get("file_name") or entry.get("name") or "").strip(),
                        "content": entry.get("content") or "",
                    }
                )
            doc["files"] = normalized_files

        return doc

    def _safe_read_json(self, path: str) -> Dict[str, Any]:
        try:
            with open(path, "r", encoding="utf-8") as handle:
                return json.load(handle)
        except Exception:
            return {}

    def _extract_tab_name(self, obj: Mapping[str, Any]) -> str:
        if not isinstance(obj, Mapping):
            return ""
        for key in ("tabName", "tab_name", "name", "title"):
            val = obj.get(key)
            if isinstance(val, str) and val.strip():
                return val.strip()
        return ""

    def _detect_script_type(self, filename: str) -> str:
        lower = (filename or "").lower()
        if lower.endswith(".json") and os.path.isfile(filename):
            try:
                obj = self._safe_read_json(filename)
                if isinstance(obj, dict):
                    typ = str(obj.get("type") or obj.get("script_type") or "").strip().lower()
                    if typ in ("powershell", "batch", "bash", "ansible"):
                        return typ
            except Exception:
                pass
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

    def _load_assembly_document(self, abs_path: str, island: str, type_hint: str = "") -> Dict[str, Any]:
        base_name = os.path.splitext(os.path.basename(abs_path))[0]
        default_type = self._default_type_for_island(island, type_hint)
        if abs_path.lower().endswith(".json"):
            data = self._safe_read_json(abs_path)
            return self._normalize_document(data, default_type, base_name)
        try:
            with open(abs_path, "r", encoding="utf-8", errors="replace") as handle:
                content = handle.read()
        except Exception:
            content = ""
        doc = self._empty_document(default_type)
        doc["name"] = base_name
        doc["script"] = (content or "").replace("\r\n", "\n")
        if default_type == "ansible":
            doc["category"] = "application"
        return doc

    def _log_action(self, action: str, message: str) -> None:
        try:
            self.service_log("assemblies", f"{action}: {message}")
        except Exception:
            self.logger.debug("Failed to record assemblies log entry for %s", action, exc_info=True)


    # ------------------------------------------------------------------
    # CRUD operations
    # ------------------------------------------------------------------
    def create(self, payload: Mapping[str, Any]) -> Tuple[MutableMapping[str, Any], int]:
        island = (payload.get("island") or "").strip()
        kind = (payload.get("kind") or "").strip().lower()
        path_value = (payload.get("path") or "").strip()
        content_value = payload.get("content")
        item_type = (payload.get("type") or "").strip().lower()
        try:
            root, abs_path, rel_norm = self._resolve_assembly_path(island, path_value)
            if not rel_norm:
                return {"error": "path required"}, 400
            if kind == "folder":
                os.makedirs(abs_path, exist_ok=True)
                self._log_action("create-folder", f"island={island} rel_path={rel_norm}")
                return {"status": "ok"}, 200

            if kind != "file":
                return {"error": "invalid kind"}, 400

            base, ext = os.path.splitext(abs_path)
            if not ext:
                abs_path = base + self._default_ext_for_island(island, item_type)
            os.makedirs(os.path.dirname(abs_path), exist_ok=True)
            isl = (island or "").lower()
            if isl in ("workflows", "workflow"):
                obj = self._coerce_workflow_dict(content_value)
                base_name = os.path.splitext(os.path.basename(abs_path))[0]
                obj.setdefault("tab_name", base_name)
                with open(abs_path, "w", encoding="utf-8") as handle:
                    json.dump(obj, handle, indent=2)
            else:
                obj = self._coerce_generic_dict(content_value)
                base_name = os.path.splitext(os.path.basename(abs_path))[0]
                normalized = self._normalize_document(obj, self._default_type_for_island(island, item_type), base_name)
                with open(abs_path, "w", encoding="utf-8") as handle:
                    json.dump(self._prepare_storage(normalized), handle, indent=2)
            rel_new = os.path.relpath(abs_path, root).replace(os.sep, "/")
            self._log_action("create-file", f"island={island} rel_path={rel_new}")
            return {"status": "ok", "rel_path": rel_new}, 200
        except ValueError as err:
            return {"error": str(err)}, 400
        except Exception as exc:  # pragma: no cover - defensive logging
            self.logger.exception("Failed to create assembly", exc_info=exc)
            return {"error": str(exc)}, 500

    def edit(self, payload: Mapping[str, Any]) -> Tuple[MutableMapping[str, Any], int]:
        island = (payload.get("island") or "").strip()
        path_value = (payload.get("path") or "").strip()
        content_value = payload.get("content")
        data_type = (payload.get("type") or "").strip()
        try:
            root, abs_path, _ = self._resolve_assembly_path(island, path_value)
            if not os.path.isfile(abs_path):
                return {"error": "file not found"}, 404
            target_abs = abs_path
            if not abs_path.lower().endswith(".json"):
                base, _ = os.path.splitext(abs_path)
                target_abs = base + self._default_ext_for_island(island, data_type)

            isl = (island or "").lower()
            if isl in ("workflows", "workflow"):
                obj = self._coerce_workflow_dict(content_value, strict=True)
                with open(target_abs, "w", encoding="utf-8") as handle:
                    json.dump(obj, handle, indent=2)
            else:
                obj = self._coerce_generic_dict(content_value)
                base_name = os.path.splitext(os.path.basename(target_abs))[0]
                normalized = self._normalize_document(
                    obj,
                    self._default_type_for_island(island, obj.get("type") if isinstance(obj, dict) else ""),
                    base_name,
                )
                with open(target_abs, "w", encoding="utf-8") as handle:
                    json.dump(self._prepare_storage(normalized), handle, indent=2)

            if target_abs != abs_path:
                try:
                    os.remove(abs_path)
                except Exception:
                    pass

            rel_new = os.path.relpath(target_abs, root).replace(os.sep, "/")
            self._log_action("edit", f"island={island} rel_path={rel_new}")
            return {"status": "ok", "rel_path": rel_new}, 200
        except ValueError as err:
            return {"error": str(err)}, 400
        except Exception as exc:  # pragma: no cover
            self.logger.exception("Failed to edit assembly", exc_info=exc)
            return {"error": str(exc)}, 500

    def rename(self, payload: Mapping[str, Any]) -> Tuple[MutableMapping[str, Any], int]:
        island = (payload.get("island") or "").strip()
        kind = (payload.get("kind") or "").strip().lower()
        path_value = (payload.get("path") or "").strip()
        new_name = (payload.get("new_name") or "").strip()
        item_type = (payload.get("type") or "").strip().lower()
        if not new_name:
            return {"error": "new_name required"}, 400
        try:
            root, old_abs, _ = self._resolve_assembly_path(island, path_value)
            if kind == "folder":
                if not os.path.isdir(old_abs):
                    return {"error": "folder not found"}, 404
                new_abs = os.path.join(os.path.dirname(old_abs), new_name)
            elif kind == "file":
                if not os.path.isfile(old_abs):
                    return {"error": "file not found"}, 404
                base, ext = os.path.splitext(new_name)
                if not ext:
                    new_name = base + self._default_ext_for_island(island, item_type)
                new_abs = os.path.join(os.path.dirname(old_abs), os.path.basename(new_name))
            else:
                return {"error": "invalid kind"}, 400

            new_abs_norm = os.path.abspath(new_abs)
            if not new_abs_norm.startswith(root):
                return {"error": "invalid destination"}, 400

            os.rename(old_abs, new_abs_norm)

            isl = (island or "").lower()
            if kind == "file" and isl in ("workflows", "workflow"):
                try:
                    obj = self._safe_read_json(new_abs_norm)
                    base_name = os.path.splitext(os.path.basename(new_abs_norm))[0]
                    for key in ("tabName", "tab_name", "name", "title"):
                        if key in obj:
                            obj[key] = base_name
                    obj.setdefault("tab_name", base_name)
                    with open(new_abs_norm, "w", encoding="utf-8") as handle:
                        json.dump(obj, handle, indent=2)
                except Exception:
                    self.logger.debug("Failed to normalize workflow metadata after rename", exc_info=True)

            rel_new = os.path.relpath(new_abs_norm, root).replace(os.sep, "/")
            self._log_action("rename", f"island={island} from={path_value} to={rel_new}")
            return {"status": "ok", "rel_path": rel_new}, 200
        except ValueError as err:
            return {"error": str(err)}, 400
        except Exception as exc:  # pragma: no cover
            self.logger.exception("Failed to rename assembly", exc_info=exc)
            return {"error": str(exc)}, 500

    def move(self, payload: Mapping[str, Any]) -> Tuple[MutableMapping[str, Any], int]:
        island = (payload.get("island") or "").strip()
        path_value = (payload.get("path") or "").strip()
        new_path = (payload.get("new_path") or "").strip()
        kind = (payload.get("kind") or "").strip().lower()
        try:
            _, old_abs, _ = self._resolve_assembly_path(island, path_value)
            root, new_abs, _ = self._resolve_assembly_path(island, new_path)
            if kind == "folder":
                if not os.path.isdir(old_abs):
                    return {"error": "folder not found"}, 404
            else:
                if not os.path.isfile(old_abs):
                    return {"error": "file not found"}, 404
            os.makedirs(os.path.dirname(new_abs), exist_ok=True)
            shutil.move(old_abs, new_abs)
            rel_new = os.path.relpath(new_abs, root).replace(os.sep, "/")
            self._log_action("move", f"island={island} from={path_value} to={rel_new}")
            return {"status": "ok", "rel_path": rel_new}, 200
        except ValueError as err:
            return {"error": str(err)}, 400
        except Exception as exc:  # pragma: no cover
            self.logger.exception("Failed to move assembly", exc_info=exc)
            return {"error": str(exc)}, 500

    def delete(self, payload: Mapping[str, Any]) -> Tuple[MutableMapping[str, Any], int]:
        island = (payload.get("island") or "").strip()
        kind = (payload.get("kind") or "").strip().lower()
        path_value = (payload.get("path") or "").strip()
        try:
            root, abs_path, rel_norm = self._resolve_assembly_path(island, path_value)
            if not rel_norm:
                return {"error": "cannot delete root"}, 400
            if kind == "folder":
                if not os.path.isdir(abs_path):
                    return {"error": "folder not found"}, 404
                shutil.rmtree(abs_path)
            elif kind == "file":
                if not os.path.isfile(abs_path):
                    return {"error": "file not found"}, 404
                os.remove(abs_path)
            else:
                return {"error": "invalid kind"}, 400
            self._log_action("delete", f"island={island} rel_path={rel_norm} kind={kind}")
            return {"status": "ok"}, 200
        except ValueError as err:
            return {"error": str(err)}, 400
        except Exception as exc:  # pragma: no cover
            self.logger.exception("Failed to delete assembly", exc_info=exc)
            return {"error": str(exc)}, 500

    def list_items(self, island: str) -> Tuple[MutableMapping[str, Any], int]:
        island = (island or "").strip()
        try:
            root = self._resolve_island_root(island)
            if not root:
                return {"error": "invalid island"}, 400
            os.makedirs(root, exist_ok=True)

            items: List[Dict[str, Any]] = []
            folders: List[str] = []
            isl = island.lower()

            if isl in ("workflows", "workflow"):
                exts = (".json",)
                for dirpath, dirnames, filenames in os.walk(root):
                    rel_root = os.path.relpath(dirpath, root)
                    if rel_root != ".":
                        folders.append(rel_root.replace(os.sep, "/"))
                    for fname in filenames:
                        if not fname.lower().endswith(exts):
                            continue
                        fp = os.path.join(dirpath, fname)
                        rel_path = os.path.relpath(fp, root).replace(os.sep, "/")
                        try:
                            mtime = os.path.getmtime(fp)
                        except Exception:
                            mtime = 0.0
                        obj = self._safe_read_json(fp)
                        tab = self._extract_tab_name(obj)
                        items.append(
                            {
                                "file_name": fname,
                                "rel_path": rel_path,
                                "type": "workflow",
                                "tab_name": tab,
                                "last_edited": time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(mtime)),
                                "last_edited_epoch": mtime,
                            }
                        )
            elif isl in ("scripts", "script"):
                exts = (".json", ".ps1", ".bat", ".sh")
                for dirpath, dirnames, filenames in os.walk(root):
                    rel_root = os.path.relpath(dirpath, root)
                    if rel_root != ".":
                        folders.append(rel_root.replace(os.sep, "/"))
                    for fname in filenames:
                        if not fname.lower().endswith(exts):
                            continue
                        fp = os.path.join(dirpath, fname)
                        rel_path = os.path.relpath(fp, root).replace(os.sep, "/")
                        try:
                            mtime = os.path.getmtime(fp)
                        except Exception:
                            mtime = 0.0
                        stype = self._detect_script_type(fp)
                        doc = self._load_assembly_document(fp, "scripts", stype)
                        items.append(
                            {
                                "file_name": fname,
                                "rel_path": rel_path,
                                "type": doc.get("type", stype),
                                "name": doc.get("name"),
                                "category": doc.get("category"),
                                "description": doc.get("description"),
                                "last_edited": time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(mtime)),
                                "last_edited_epoch": mtime,
                            }
                        )
            else:
                exts = (".json", ".yml")
                for dirpath, dirnames, filenames in os.walk(root):
                    rel_root = os.path.relpath(dirpath, root)
                    if rel_root != ".":
                        folders.append(rel_root.replace(os.sep, "/"))
                    for fname in filenames:
                        if not fname.lower().endswith(exts):
                            continue
                        fp = os.path.join(dirpath, fname)
                        rel_path = os.path.relpath(fp, root).replace(os.sep, "/")
                        try:
                            mtime = os.path.getmtime(fp)
                        except Exception:
                            mtime = 0.0
                        stype = self._detect_script_type(fp)
                        doc = self._load_assembly_document(fp, "ansible", stype)
                        items.append(
                            {
                                "file_name": fname,
                                "rel_path": rel_path,
                                "type": doc.get("type", "ansible"),
                                "name": doc.get("name"),
                                "category": doc.get("category"),
                                "description": doc.get("description"),
                                "last_edited": time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime(mtime)),
                                "last_edited_epoch": mtime,
                            }
                        )

            items.sort(key=lambda row: row.get("last_edited_epoch", 0.0), reverse=True)
            return {"root": root, "items": items, "folders": folders}, 200
        except ValueError as err:
            return {"error": str(err)}, 400
        except Exception as exc:  # pragma: no cover
            self.logger.exception("Failed to list assemblies", exc_info=exc)
            return {"error": str(exc)}, 500

    def load(self, island: str, rel_path: str) -> Tuple[MutableMapping[str, Any], int]:
        island = (island or "").strip()
        rel_path = (rel_path or "").strip()
        try:
            root, abs_path, _ = self._resolve_assembly_path(island, rel_path)
            if not os.path.isfile(abs_path):
                return {"error": "file not found"}, 404
            isl = island.lower()
            if isl in ("workflows", "workflow"):
                obj = self._safe_read_json(abs_path)
                return obj, 200
            doc = self._load_assembly_document(abs_path, island)
            rel = os.path.relpath(abs_path, root).replace(os.sep, "/")
            result: Dict[str, Any] = {
                "file_name": os.path.basename(abs_path),
                "rel_path": rel,
                "type": doc.get("type"),
                "assembly": doc,
                "content": doc.get("script"),
            }
            return result, 200
        except ValueError as err:
            return {"error": str(err)}, 400
        except Exception as exc:  # pragma: no cover
            self.logger.exception("Failed to load assembly", exc_info=exc)
            return {"error": str(exc)}, 500


    # ------------------------------------------------------------------
    # Content coercion
    # ------------------------------------------------------------------
    def _coerce_generic_dict(self, value: Any) -> Dict[str, Any]:
        obj = value
        if isinstance(obj, str):
            try:
                obj = json.loads(obj)
            except Exception:
                obj = {}
        if not isinstance(obj, dict):
            obj = {}
        return obj

    def _coerce_workflow_dict(self, value: Any, strict: bool = False) -> Dict[str, Any]:
        obj = value
        if isinstance(obj, str):
            obj = json.loads(obj)
        if not isinstance(obj, dict):
            if strict:
                raise ValueError("invalid content for workflow")
            obj = {}
        return obj


def register_assemblies(app, adapters: "EngineServiceAdapters") -> None:
    """Register assembly CRUD endpoints on the Flask app."""

    service = AssemblyManagementService(adapters)
    blueprint = Blueprint("assemblies", __name__)

    @blueprint.route("/api/assembly/create", methods=["POST"])
    def _create():
        payload = request.get_json(silent=True) or {}
        response, status = service.create(payload)
        return jsonify(response), status

    @blueprint.route("/api/assembly/edit", methods=["POST"])
    def _edit():
        payload = request.get_json(silent=True) or {}
        response, status = service.edit(payload)
        return jsonify(response), status

    @blueprint.route("/api/assembly/rename", methods=["POST"])
    def _rename():
        payload = request.get_json(silent=True) or {}
        response, status = service.rename(payload)
        return jsonify(response), status

    @blueprint.route("/api/assembly/move", methods=["POST"])
    def _move():
        payload = request.get_json(silent=True) or {}
        response, status = service.move(payload)
        return jsonify(response), status

    @blueprint.route("/api/assembly/delete", methods=["POST"])
    def _delete():
        payload = request.get_json(silent=True) or {}
        response, status = service.delete(payload)
        return jsonify(response), status

    @blueprint.route("/api/assembly/list", methods=["GET"])
    def _list():
        response, status = service.list_items(request.args.get("island", ""))
        return jsonify(response), status

    @blueprint.route("/api/assembly/load", methods=["GET"])
    def _load():
        response, status = service.load(request.args.get("island", ""), request.args.get("path", ""))
        return jsonify(response), status

    app.register_blueprint(blueprint)

