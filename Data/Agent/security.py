#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Agent/security.py

from __future__ import annotations

import base64
import hashlib
import json
import os
import platform
import stat
import time
from dataclasses import dataclass
from typing import Optional

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519

IS_WINDOWS = platform.system().lower().startswith("win")

try:
    if IS_WINDOWS:
        import win32crypt  # type: ignore
except Exception:  # pragma: no cover - win32crypt missing
    win32crypt = None  # type: ignore


def _ensure_dir(path: str) -> None:
    os.makedirs(path, exist_ok=True)


def _restrict_permissions(path: str) -> None:
    try:
        if not IS_WINDOWS:
            os.chmod(path, stat.S_IRUSR | stat.S_IWUSR)
    except Exception:
        pass


def _protect(data: bytes, *, scope_system: bool) -> bytes:
    if not IS_WINDOWS or not win32crypt:
        return data
    flags = win32crypt.CRYPTPROTECT_LOCAL_MACHINE if scope_system else 0
    protected = win32crypt.CryptProtectData(data, None, None, None, None, flags)  # type: ignore[attr-defined]
    return protected[1]


def _unprotect(data: bytes, *, scope_system: bool) -> bytes:
    if not IS_WINDOWS or not win32crypt:
        return data
    flags = win32crypt.CRYPTPROTECT_LOCAL_MACHINE if scope_system else 0
    unwrapped = win32crypt.CryptUnprotectData(data, None, None, None, None, flags)  # type: ignore[attr-defined]
    return unwrapped[1]


def _fingerprint_der(public_der: bytes) -> str:
    digest = hashlib.sha256(public_der).hexdigest()
    return digest.lower()


@dataclass
class AgentIdentity:
    private_key: ed25519.Ed25519PrivateKey
    public_key_der: bytes
    public_key_b64: str
    fingerprint: str

    def sign(self, payload: bytes) -> bytes:
        return self.private_key.sign(payload)


class AgentKeyStore:
    def __init__(self, settings_dir: str, scope: str = "CURRENTUSER") -> None:
        self.settings_dir = settings_dir
        self.scope_system = scope.upper() == "SYSTEM"
        _ensure_dir(self.settings_dir)
        self._private_path = os.path.join(self.settings_dir, "agent_key.ed25519")
        self._public_path = os.path.join(self.settings_dir, "agent_key.pub")
        self._guid_path = os.path.join(self.settings_dir, "guid.txt")
        self._access_token_path = os.path.join(self.settings_dir, "access.jwt")
        self._refresh_token_path = os.path.join(self.settings_dir, "refresh.token")
        self._token_meta_path = os.path.join(self.settings_dir, "access.meta.json")

    # ------------------------------------------------------------------
    # Identity management
    # ------------------------------------------------------------------
    def load_or_create_identity(self) -> AgentIdentity:
        if os.path.isfile(self._private_path) and os.path.isfile(self._public_path):
            try:
                return self._load_identity()
            except Exception:
                pass
        return self._create_identity()

    def _load_identity(self) -> AgentIdentity:
        with open(self._private_path, "rb") as fh:
            protected = fh.read()
        private_bytes = _unprotect(protected, scope_system=self.scope_system)
        private_key = serialization.load_pem_private_key(private_bytes, password=None)
        public_der = private_key.public_key().public_bytes(
            encoding=serialization.Encoding.DER,
            format=serialization.PublicFormat.SubjectPublicKeyInfo,
        )
        with open(self._public_path, "r", encoding="utf-8") as fh:
            public_b64 = fh.read().strip()
        if not public_b64:
            public_b64 = base64.b64encode(public_der).decode("ascii")
        fingerprint = _fingerprint_der(public_der)
        return AgentIdentity(private_key=private_key, public_key_der=public_der, public_key_b64=public_b64, fingerprint=fingerprint)

    def _create_identity(self) -> AgentIdentity:
        private_key = ed25519.Ed25519PrivateKey.generate()
        private_bytes = private_key.private_bytes(
            serialization.Encoding.PEM,
            serialization.PrivateFormat.PKCS8,
            serialization.NoEncryption(),
        )
        protected = _protect(private_bytes, scope_system=self.scope_system)
        with open(self._private_path, "wb") as fh:
            fh.write(protected)
        _restrict_permissions(self._private_path)

        public_der = private_key.public_key().public_bytes(
            encoding=serialization.Encoding.DER,
            format=serialization.PublicFormat.SubjectPublicKeyInfo,
        )
        public_b64 = base64.b64encode(public_der).decode("ascii")
        with open(self._public_path, "w", encoding="utf-8") as fh:
            fh.write(public_b64)
        _restrict_permissions(self._public_path)
        fingerprint = _fingerprint_der(public_der)
        return AgentIdentity(private_key=private_key, public_key_der=public_der, public_key_b64=public_b64, fingerprint=fingerprint)

    # ------------------------------------------------------------------
    # GUID helpers
    # ------------------------------------------------------------------
    def save_guid(self, guid: str) -> None:
        if not guid:
            return
        with open(self._guid_path, "w", encoding="utf-8") as fh:
            fh.write(str(guid).strip())
        _restrict_permissions(self._guid_path)

    def load_guid(self) -> Optional[str]:
        if not os.path.isfile(self._guid_path):
            return None
        try:
            with open(self._guid_path, "r", encoding="utf-8") as fh:
                return fh.read().strip() or None
        except Exception:
            return None

    # ------------------------------------------------------------------
    # Token helpers
    # ------------------------------------------------------------------
    def save_access_token(self, token: str, *, expires_at: Optional[int] = None) -> None:
        if token:
            with open(self._access_token_path, "w", encoding="utf-8") as fh:
                fh.write(token.strip())
            _restrict_permissions(self._access_token_path)
        if expires_at:
            meta = self._load_token_meta()
            meta["access_expires_at"] = int(expires_at)
            self._store_token_meta(meta)

    def load_access_token(self) -> Optional[str]:
        if not os.path.isfile(self._access_token_path):
            return None
        try:
            with open(self._access_token_path, "r", encoding="utf-8") as fh:
                token = fh.read().strip()
            return token or None
        except Exception:
            return None

    def save_refresh_token(self, token: str) -> None:
        if not token:
            return
        protected = _protect(token.encode("utf-8"), scope_system=self.scope_system)
        with open(self._refresh_token_path, "wb") as fh:
            fh.write(protected)
        _restrict_permissions(self._refresh_token_path)

    def load_refresh_token(self) -> Optional[str]:
        if not os.path.isfile(self._refresh_token_path):
            return None
        try:
            with open(self._refresh_token_path, "rb") as fh:
                protected = fh.read()
            raw = _unprotect(protected, scope_system=self.scope_system)
            return raw.decode("utf-8")
        except Exception:
            return None

    def clear_tokens(self) -> None:
        for path in (self._access_token_path, self._refresh_token_path, self._token_meta_path):
            try:
                if os.path.isfile(path):
                    os.remove(path)
            except Exception:
                pass

    # ------------------------------------------------------------------
    # Token metadata (e.g., expiry, fingerprint binding)
    # ------------------------------------------------------------------
    def _load_token_meta(self) -> dict:
        if not os.path.isfile(self._token_meta_path):
            return {}
        try:
            with open(self._token_meta_path, "r", encoding="utf-8") as fh:
                data = json.load(fh)
            if isinstance(data, dict):
                return data
        except Exception:
            pass
        return {}

    def _store_token_meta(self, meta: dict) -> None:
        try:
            with open(self._token_meta_path, "w", encoding="utf-8") as fh:
                json.dump(meta, fh, indent=2)
            _restrict_permissions(self._token_meta_path)
        except Exception:
            pass

    def get_access_expiry(self) -> Optional[int]:
        meta = self._load_token_meta()
        expiry = meta.get("access_expires_at")
        if isinstance(expiry, (int, float)):
            return int(expiry)
        return None

    def set_access_binding(self, fingerprint: str) -> None:
        meta = self._load_token_meta()
        meta["ssl_key_fingerprint"] = fingerprint
        meta["access_bound_at"] = int(time.time())
        self._store_token_meta(meta)

    def get_access_binding(self) -> Optional[str]:
        meta = self._load_token_meta()
        value = meta.get("ssl_key_fingerprint")
        if isinstance(value, str) and value.strip():
            return value.strip()
        return None
