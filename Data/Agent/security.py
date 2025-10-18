#////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Agent/security.py

from __future__ import annotations

import base64
import contextlib
import errno
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

try:  # pragma: no cover - Windows only
    import msvcrt  # type: ignore
except Exception:  # pragma: no cover - non-Windows
    msvcrt = None  # type: ignore

try:  # pragma: no cover - POSIX only
    import fcntl  # type: ignore
except Exception:  # pragma: no cover - Windows
    fcntl = None  # type: ignore


def _ensure_dir(path: str) -> None:
    os.makedirs(path, exist_ok=True)


def _restrict_permissions(path: str) -> None:
    try:
        if not IS_WINDOWS:
            os.chmod(path, stat.S_IRUSR | stat.S_IWUSR)
    except Exception:
        pass


class _FileLock:
    def __init__(self, path: str) -> None:
        self.path = path
        self._handle = None

    def acquire(self, *, timeout: float = 60.0, poll_interval: float = 0.2) -> None:
        directory = os.path.dirname(self.path)
        if directory:
            os.makedirs(directory, exist_ok=True)
        deadline = time.time() + timeout if timeout else None
        while True:
            handle = open(self.path, "a+b")
            try:
                self._try_lock(handle)
            except OSError as exc:
                handle.close()
                if not self._is_lock_unavailable(exc):
                    raise
                if deadline and time.time() >= deadline:
                    raise TimeoutError("Timed out waiting for file lock")
                time.sleep(poll_interval)
                continue
            except Exception:
                handle.close()
                raise

            self._handle = handle
            try:
                handle.seek(0)
                handle.truncate(0)
                payload = f"pid={os.getpid()} ts={int(time.time())}\n".encode("utf-8")
                handle.write(payload)
                handle.flush()
            except Exception:
                pass
            return

    def release(self) -> None:
        handle = self._handle
        if not handle:
            return
        try:
            self._unlock(handle)
        finally:
            try:
                handle.close()
            except Exception:
                pass
            self._handle = None

    def _try_lock(self, handle):
        if IS_WINDOWS:
            if msvcrt is None:
                raise OSError(errno.EINVAL, "msvcrt unavailable for locking")
            try:
                msvcrt.locking(handle.fileno(), msvcrt.LK_NBLCK, 1)  # type: ignore[attr-defined]
            except OSError as exc:  # pragma: no cover - platform specific
                raise exc
        else:
            if fcntl is None:
                raise OSError(errno.EINVAL, "fcntl unavailable for locking")
            fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)  # type: ignore[attr-defined]

    def _unlock(self, handle):
        if IS_WINDOWS:
            if msvcrt is None:
                return
            try:
                msvcrt.locking(handle.fileno(), msvcrt.LK_UNLCK, 1)  # type: ignore[attr-defined]
            except OSError:  # pragma: no cover - platform specific
                pass
        else:
            if fcntl is None:
                return
            try:
                fcntl.flock(handle.fileno(), fcntl.LOCK_UN)  # type: ignore[attr-defined]
            except OSError:
                pass

    @staticmethod
    def _is_lock_unavailable(exc: OSError) -> bool:
        if hasattr(exc, "winerror"):
            return exc.winerror in (32, 33)  # type: ignore[attr-defined]
        err = exc.errno if hasattr(exc, "errno") else None
        return err in (errno.EACCES, errno.EAGAIN, errno.EWOULDBLOCK)


@contextlib.contextmanager
def _locked_file(path: str, *, timeout: float = 60.0):
    lock = _FileLock(path)
    lock.acquire(timeout=timeout)
    try:
        yield
    finally:
        lock.release()


def _protect(data: bytes, *, scope_system: bool) -> bytes:
    if not IS_WINDOWS or not win32crypt:
        return data
    scopes = [scope_system]
    # Always include the alternate scope so we can fall back if the preferred
    # protection attempt fails (e.g., running under a limited account that
    # lacks access to the desired DPAPI scope).
    if scope_system:
        scopes.append(False)
    else:
        scopes.append(True)
    for scope in scopes:
        flags = 0
        if scope:
            flags = getattr(win32crypt, "CRYPTPROTECT_LOCAL_MACHINE", 0x4)
        try:
            protected = win32crypt.CryptProtectData(data, None, None, None, None, flags)  # type: ignore[attr-defined]
        except Exception:
            continue
        blob = protected[1]
        if isinstance(blob, memoryview):
            return blob.tobytes()
        if isinstance(blob, bytearray):
            return bytes(blob)
        if isinstance(blob, bytes):
            return blob
    return data


def _unprotect(data: bytes, *, scope_system: bool) -> bytes:
    if not IS_WINDOWS or not win32crypt:
        return data
    scopes = [scope_system]
    if scope_system:
        scopes.append(False)
    else:
        scopes.append(True)
    for scope in scopes:
        flags = 0
        if scope:
            flags = getattr(win32crypt, "CRYPTPROTECT_LOCAL_MACHINE", 0x4)
        try:
            unwrapped = win32crypt.CryptUnprotectData(data, None, None, None, None, flags)  # type: ignore[attr-defined]
        except Exception:
            continue
        blob = unwrapped[1]
        if isinstance(blob, memoryview):
            return blob.tobytes()
        if isinstance(blob, bytearray):
            return bytes(blob)
        if isinstance(blob, bytes):
            return blob
    return data


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
        self._server_certificate_path = os.path.join(self.settings_dir, "server_certificate.pem")
        self._server_signing_key_path = os.path.join(self.settings_dir, "server_signing_key.pub")
        self._identity_lock_path = os.path.join(self.settings_dir, "identity.lock")

    # ------------------------------------------------------------------
    # Identity management
    # ------------------------------------------------------------------
    def load_or_create_identity(self) -> AgentIdentity:
        with _locked_file(self._identity_lock_path, timeout=120.0):
            if os.path.isfile(self._private_path) and os.path.isfile(self._public_path):
                try:
                    return self._load_identity()
                except Exception:
                    # If loading fails, fall back to regenerating the identity while
                    # holding the lock so concurrent agents do not thrash the key files.
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
        except Exception:
            return None

        # Try both scopes (preferred first) and decode once a UTF-8 payload is recovered.
        for scope_first in (self.scope_system, not self.scope_system):
            try:
                candidate = _unprotect(protected, scope_system=scope_first)
            except Exception:
                continue
            if not candidate:
                continue
            try:
                return candidate.decode("utf-8")
            except Exception:
                continue
        return None

    def clear_tokens(self) -> None:
        for path in (self._access_token_path, self._refresh_token_path, self._token_meta_path):
            try:
                if os.path.isfile(path):
                    os.remove(path)
            except Exception:
                pass
    # ------------------------------------------------------------------
    # Server certificate & signing key helpers
    # ------------------------------------------------------------------
    def server_certificate_path(self) -> str:
        return self._server_certificate_path

    def save_server_certificate(self, pem_text: str) -> None:
        if not pem_text:
            return
        normalized = pem_text.strip()
        if not normalized:
            return
        if not normalized.endswith("\n"):
            normalized += "\n"
        with open(self._server_certificate_path, "w", encoding="utf-8") as fh:
            fh.write(normalized)
        _restrict_permissions(self._server_certificate_path)

    def load_server_certificate(self) -> Optional[str]:
        try:
            if os.path.isfile(self._server_certificate_path):
                with open(self._server_certificate_path, "r", encoding="utf-8") as fh:
                    return fh.read()
        except Exception:
            return None
        return None

    def save_server_signing_key(self, value: str) -> None:
        if not value:
            return
        normalized = value.strip()
        if not normalized:
            return
        with open(self._server_signing_key_path, "w", encoding="utf-8") as fh:
            fh.write(normalized)
            fh.write("\n")
        _restrict_permissions(self._server_signing_key_path)

    def load_server_signing_key(self) -> Optional[str]:
        try:
            if os.path.isfile(self._server_signing_key_path):
                with open(self._server_signing_key_path, "r", encoding="utf-8") as fh:
                    value = fh.read().strip()
                    return value or None
        except Exception:
            return None
        return None


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
