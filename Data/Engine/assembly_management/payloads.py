# ======================================================
# Data\Engine\assembly_management\payloads.py
# Description: Handles payload GUID generation, filesystem storage, and staging/runtime mirroring.
#
# API Endpoints (if applicable): None
# ======================================================

"""Payload storage helpers for assembly persistence."""

from __future__ import annotations

import datetime as _dt
import hashlib
import logging
import shutil
import uuid
from pathlib import Path
from typing import Optional, Union

from .models import PayloadDescriptor, PayloadType


class PayloadManager:
    """Stores payload content on disk and mirrors it to the runtime directory."""

    def __init__(self, staging_root: Path, runtime_root: Path, *, logger: Optional[logging.Logger] = None) -> None:
        self._staging_root = staging_root
        self._runtime_root = runtime_root
        self._logger = logger or logging.getLogger(__name__)
        self._staging_root.mkdir(parents=True, exist_ok=True)
        self._runtime_root.mkdir(parents=True, exist_ok=True)

    # ------------------------------------------------------------------
    # Public API
    # ------------------------------------------------------------------
    def store_payload(
        self,
        payload_type: PayloadType,
        content: Union[str, bytes],
        *,
        assembly_guid: Optional[str] = None,
        extension: Optional[str] = None,
    ) -> PayloadDescriptor:
        """Persist payload content inside the runtime directory only."""

        resolved_guid = self._normalise_guid(assembly_guid or uuid.uuid4().hex)
        resolved_extension = self._normalise_extension(extension or self._default_extension(payload_type))
        now = _dt.datetime.utcnow()
        data = content.encode("utf-8") if isinstance(content, str) else bytes(content)
        checksum = hashlib.sha256(data).hexdigest()

        runtime_dir = self._payload_dir(self._runtime_root, resolved_guid)
        runtime_dir.mkdir(parents=True, exist_ok=True)

        file_name = f"payload{resolved_extension}"
        runtime_path = runtime_dir / file_name

        with runtime_path.open("wb") as handle:
            handle.write(data)

        descriptor = PayloadDescriptor(
            assembly_guid=resolved_guid,
            payload_type=payload_type,
            file_name=file_name,
            file_extension=resolved_extension,
            size_bytes=len(data),
            checksum=checksum,
            created_at=now,
            updated_at=now,
        )
        return descriptor

    def update_payload(self, descriptor: PayloadDescriptor, content: Union[str, bytes]) -> PayloadDescriptor:
        """Replace payload content while retaining GUID and metadata."""

        data = content.encode("utf-8") if isinstance(content, str) else bytes(content)
        checksum = hashlib.sha256(data).hexdigest()
        now = _dt.datetime.utcnow()

        runtime_path = self._payload_dir(self._runtime_root, descriptor.assembly_guid) / descriptor.file_name
        runtime_path.parent.mkdir(parents=True, exist_ok=True)

        with runtime_path.open("wb") as handle:
            handle.write(data)

        descriptor.size_bytes = len(data)
        descriptor.checksum = checksum
        descriptor.updated_at = now
        return descriptor

    def read_payload_bytes(self, descriptor: PayloadDescriptor) -> bytes:
        """Retrieve payload content from the runtime copy, falling back to staging if required."""

        runtime_path = self._payload_dir(self._runtime_root, descriptor.assembly_guid) / descriptor.file_name
        if runtime_path.exists():
            return runtime_path.read_bytes()
        staging_path = self._payload_dir(self._staging_root, descriptor.assembly_guid) / descriptor.file_name
        if staging_path.exists():
            data = staging_path.read_bytes()
            try:
                shutil.copy2(staging_path, runtime_path)
            except Exception as exc:  # pragma: no cover - best effort seed
                self._logger.debug("Failed to seed runtime payload %s from staging: %s", descriptor.assembly_guid, exc)
            return data
        raise FileNotFoundError(f"Payload content missing for {descriptor.assembly_guid}")

    def ensure_runtime_copy(self, descriptor: PayloadDescriptor) -> None:
        """Ensure the runtime payload copy exists."""

        runtime_path = self._payload_dir(self._runtime_root, descriptor.assembly_guid) / descriptor.file_name
        if runtime_path.exists():
            return
        staging_path = self._payload_dir(self._staging_root, descriptor.assembly_guid) / descriptor.file_name
        if not staging_path.exists():
            self._logger.warning("Payload missing on disk; guid=%s path=%s", descriptor.assembly_guid, staging_path)
            return
        runtime_path.parent.mkdir(parents=True, exist_ok=True)
        try:
            shutil.copy2(staging_path, runtime_path)
        except Exception as exc:  # pragma: no cover - best effort mirror
            self._logger.debug("Failed to mirror payload %s via ensure_runtime_copy: %s", descriptor.assembly_guid, exc)

    def delete_payload(self, descriptor: PayloadDescriptor) -> None:
        """Remove runtime payload files without mutating staging copies."""

        dir_path = self._payload_dir(self._runtime_root, descriptor.assembly_guid)
        file_path = dir_path / descriptor.file_name
        try:
            if file_path.exists():
                file_path.unlink()
            if dir_path.exists() and not any(dir_path.iterdir()):
                dir_path.rmdir()
        except Exception as exc:  # pragma: no cover - best effort cleanup
            self._logger.debug(
                "Failed to remove runtime payload directory %s: %s", descriptor.assembly_guid, exc
            )

    # ------------------------------------------------------------------
    # Helper methods
    # ------------------------------------------------------------------
    def _payload_dir(self, root: Path, guid: str) -> Path:
        return root / guid.lower().strip()

    @staticmethod
    def _default_extension(payload_type: PayloadType) -> str:
        if payload_type == PayloadType.SCRIPT:
            return ".txt"
        if payload_type == PayloadType.WORKFLOW:
            return ".json"
        return ".bin"

    @staticmethod
    def _normalise_extension(extension: str) -> str:
        value = (extension or "").strip()
        if not value:
            return ".bin"
        if not value.startswith("."):
            return f".{value}"
        return value

    @staticmethod
    def _normalise_guid(guid: str) -> str:
        return guid.strip().lower()
