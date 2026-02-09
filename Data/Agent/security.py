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
from pathlib import Path
from typing import List, Optional, Tuple

import ssl

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519

try:
    from cryptography import x509  # type: ignore
except Exception:  # pragma: no cover - optional dependency guard
    x509 = None  # type: ignore

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


def _resolve_agent_certificate_dir(settings_dir: str, scope: str) -> str:
    scope_name = (scope or "CURRENTUSER").strip().upper() or "CURRENTUSER"

    def _as_path(value: Optional[str]) -> Optional[Path]:
        if not value:
            return None
        try:
            return Path(value).expanduser().resolve()
        except Exception:
            try:
                return Path(value).expanduser()
            except Exception:
                return Path(value)

    env_agent_root = _as_path(os.environ.get("BOREALIS_AGENT_CERT_ROOT"))
    env_cert_root = _as_path(os.environ.get("BOREALIS_CERTIFICATES_ROOT")) or _as_path(
        os.environ.get("BOREALIS_CERT_ROOT")
    )

    if env_agent_root is not None:
        base = env_agent_root
    elif env_cert_root is not None:
        base = env_cert_root / "Agent"
    else:
        settings_path = Path(settings_dir).resolve()
        try:
            project_root = settings_path.parents[2]
        except Exception:
            project_root = settings_path.parent
        base = project_root / "Agent" / "Borealis" / "Certificates"

    target = base / "Trusted_Server_Cert"
    if scope_name not in {"SYSTEM", "CURRENTUSER"}:
        target = target / scope_name

    try:
        target.mkdir(parents=True, exist_ok=True)
    except Exception:
        pass

    return str(target)


def _resolve_agent_identity_dir(settings_dir: str, scope: str) -> str:
    scope_name = (scope or "CURRENTUSER").strip().upper() or "CURRENTUSER"

    def _as_path(value: Optional[str]) -> Optional[Path]:
        if not value:
            return None
        try:
            return Path(value).expanduser().resolve()
        except Exception:
            try:
                return Path(value).expanduser()
            except Exception:
                return Path(value)

    env_agent_root = _as_path(os.environ.get("BOREALIS_AGENT_CERT_ROOT"))
    env_cert_root = _as_path(os.environ.get("BOREALIS_CERTIFICATES_ROOT")) or _as_path(
        os.environ.get("BOREALIS_CERT_ROOT")
    )

    if env_agent_root is not None:
        base = env_agent_root
    elif env_cert_root is not None:
        base = env_cert_root / "Agent"
    else:
        settings_path = Path(settings_dir).resolve()
        try:
            project_root = settings_path.parents[2]
        except Exception:
            project_root = settings_path.parent
        base = project_root / "Agent" / "Borealis" / "Certificates"

    meta_fp: Optional[str] = None
    try:
        meta_path = Path(settings_dir).resolve() / "access.meta.json"
        if meta_path.is_file():
            meta_data = json.loads(meta_path.read_text(encoding="utf-8"))
            value = (meta_data.get("ssl_key_fingerprint") or "").strip().lower()
            if value:
                meta_fp = value
    except Exception:
        meta_fp = None

    shared_target = base / "Identity"
    try:
        shared_target.mkdir(parents=True, exist_ok=True)
    except Exception:
        pass

    if scope_name in {"SYSTEM", "CURRENTUSER"}:
        private_name = "agent_identity_private.ed25519"
        public_name = "agent_identity_public.ed25519"
        shared_private = shared_target / private_name
        shared_public = shared_target / public_name

        legacy_dirs: List[Path] = []
        for candidate_scope in ("SYSTEM", "CURRENTUSER"):
            legacy_path = shared_target / candidate_scope
            if legacy_path.is_dir():
                legacy_dirs.append(legacy_path)

        shared_fp = _fingerprint_from_public_file(shared_public)
        if meta_fp and shared_fp and shared_fp == meta_fp:
            return str(shared_target)

        def _adopt_identity(source_dir: Path) -> bool:
            if not source_dir.is_dir():
                return False
            src_private = source_dir / private_name
            src_public = source_dir / public_name
            if not src_private.exists() or not src_public.exists():
                return False
            moved = False
            try:
                if shared_private.exists():
                    shared_private.unlink()
            except Exception:
                pass
            try:
                if shared_public.exists():
                    shared_public.unlink()
            except Exception:
                pass
            try:
                src_private.replace(shared_private)
                src_public.replace(shared_public)
                moved = True
            except Exception:
                try:
                    shared_private.write_bytes(src_private.read_bytes())
                    shared_public.write_bytes(src_public.read_bytes())
                    moved = True
                except Exception:
                    moved = False
            if moved:
                try:
                    if src_private.exists():
                        src_private.unlink()
                except Exception:
                    pass
                try:
                    if src_public.exists():
                        src_public.unlink()
                except Exception:
                    pass
            return moved

        if meta_fp:
            for candidate in legacy_dirs:
                candidate_fp = _fingerprint_from_public_file(candidate / public_name)
                if candidate_fp and candidate_fp == meta_fp:
                    if _adopt_identity(candidate):
                        return str(shared_target)

        try:
            if (
                (not shared_private.exists() or not shared_public.exists())
                and legacy_dirs
            ):
                for candidate in legacy_dirs:
                    if _adopt_identity(candidate):
                        break
        except Exception:
            pass

        for candidate in legacy_dirs:
            try:
                if candidate.is_dir() and not any(candidate.iterdir()):
                    candidate.rmdir()
            except Exception:
                pass

        return str(shared_target)

    target = shared_target / scope_name if scope_name else shared_target
    try:
        target.mkdir(parents=True, exist_ok=True)
    except Exception:
        pass

    return str(target)


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


def _fingerprint_from_public_file(path: Path) -> Optional[str]:
    try:
        if not path or not path.is_file():
            return None
        data = path.read_text(encoding="utf-8").strip()
        if not data:
            return None
        der = base64.b64decode(data)
        return _fingerprint_der(der)
    except Exception:
        return None


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
        self.scope_name = (scope or "CURRENTUSER").strip().upper() or "CURRENTUSER"
        self.scope_system = self.scope_name == "SYSTEM"
        _ensure_dir(self.settings_dir)
        self._certificate_dir = _resolve_agent_certificate_dir(self.settings_dir, self.scope_name)
        self._identity_dir = _resolve_agent_identity_dir(self.settings_dir, self.scope_name)
        _ensure_dir(self._identity_dir)
        self._private_path = os.path.join(self._identity_dir, "agent_identity_private.ed25519")
        self._public_path = os.path.join(self._identity_dir, "agent_identity_public.ed25519")
        self._guid_path = os.path.join(self.settings_dir, "Agent_GUID.txt")
        self._access_token_path = os.path.join(self.settings_dir, "access.jwt")
        self._refresh_token_path = os.path.join(self.settings_dir, "refresh.token")
        self._token_meta_path = os.path.join(self.settings_dir, "access.meta.json")
        self._server_certificate_path = os.path.join(self._certificate_dir, "server_certificate.pem")
        self._server_signing_key_path = os.path.join(self.settings_dir, "server_signing_key.pub")
        self._identity_lock_path = os.path.join(self.settings_dir, "identity.lock")
        self._installer_cache_path = os.path.join(self.settings_dir, "installer_code.shared.json")

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
        _ensure_dir(os.path.dirname(self._private_path))
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

    def describe_server_certificate(self) -> Tuple[int, Optional[str]]:
        """Return (certificate_count, sha256_fingerprint_prefix)."""

        count, fingerprint, _ = self.summarize_server_certificate()
        return count, fingerprint

    def summarize_server_certificate(self) -> Tuple[int, Optional[str], bool]:
        """Return (certificate_count, fingerprint_prefix, layered_default_trust)."""

        pem_bytes, certs = self._load_server_certificates()
        if not pem_bytes:
            return 0, None, False

        fingerprint = None
        if certs:
            try:
                first_cert = certs[0]
                fingerprint = hashlib.sha256(
                    first_cert.public_bytes(serialization.Encoding.DER)
                ).hexdigest()
            except Exception:
                fingerprint = None
        else:
            try:
                pem_text = pem_bytes.decode("utf-8")
                der_bytes = ssl.PEM_cert_to_DER_cert(pem_text)
                fingerprint = hashlib.sha256(der_bytes).hexdigest()
            except Exception:
                fingerprint = None

        count = len(certs) if certs else 1
        prefix = fingerprint[:12] if fingerprint else None
        include_default = self._should_layer_default_trust(certs)
        return count, prefix, include_default

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

    def build_ssl_context(self) -> Optional[ssl.SSLContext]:
        pem_bytes, certs = self._load_server_certificates()
        if not pem_bytes:
            return None

        try:
            context = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
        except Exception:
            try:
                context = ssl.create_default_context(purpose=ssl.Purpose.SERVER_AUTH)
            except Exception:
                return None

        try:
            context.check_hostname = True
        except Exception:
            pass

        try:
            context.verify_mode = ssl.CERT_REQUIRED
        except Exception:
            pass

        if hasattr(context, "minimum_version"):
            try:
                context.minimum_version = ssl.TLSVersion.TLSv1_2
            except Exception:
                pass

        pem_text = None
        try:
            pem_text = pem_bytes.decode("utf-8")
        except Exception:
            pass

        loaded = False
        if pem_text:
            try:
                context.load_verify_locations(cadata=pem_text)
                loaded = True
            except Exception:
                loaded = False

        if not loaded:
            try:
                context.load_verify_locations(cafile=self._server_certificate_path)
                loaded = True
            except Exception:
                loaded = False

        if not loaded:
            return None

        include_default = self._should_layer_default_trust(certs)
        try:
            setattr(context, "_borealis_layered_default", include_default)
        except Exception:
            pass

        if include_default:
            try:
                context.load_default_certs()
            except Exception:
                pass

        verify_flag = getattr(ssl, "VERIFY_X509_TRUSTED_FIRST", None)
        if verify_flag is not None:
            try:
                context.verify_flags |= verify_flag  # type: ignore[attr-defined]
            except Exception:
                pass

        return context

    # ------------------------------------------------------------------
    # Server certificate helpers (internal)
    # ------------------------------------------------------------------
    def _load_server_certificates(self) -> Tuple[Optional[bytes], List["x509.Certificate"]]:
        try:
            if not os.path.isfile(self._server_certificate_path):
                return None, []
            with open(self._server_certificate_path, "rb") as fh:
                pem_bytes = fh.read()
        except Exception:
            return None, []

        if not pem_bytes.strip():
            return None, []

        if x509 is None:
            return pem_bytes, []

        terminator = b"-----END CERTIFICATE-----"
        certs: List["x509.Certificate"] = []
        for chunk in pem_bytes.split(terminator):
            if b"-----BEGIN CERTIFICATE-----" not in chunk:
                continue
            block = chunk + terminator + b"\n"
            try:
                cert = x509.load_pem_x509_certificate(block)
            except Exception:
                continue
            certs.append(cert)

        return pem_bytes, certs

    def _should_layer_default_trust(self, certs: List["x509.Certificate"]) -> bool:
        if not certs:
            return True

        try:
            first_cert = certs[0]
            is_self_issued = first_cert.issuer == first_cert.subject
        except Exception:
            return True

        if not is_self_issued:
            return True

        try:
            basic = first_cert.extensions.get_extension_for_class(x509.BasicConstraints)  # type: ignore[attr-defined]
            is_ca = bool(basic.value.ca)
        except Exception:
            is_ca = False

        return is_ca

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

    # ------------------------------------------------------------------
    # Installer code sharing helpers
    # ------------------------------------------------------------------
    def _load_installer_cache(self) -> dict:
        if not os.path.isfile(self._installer_cache_path):
            return {}
        try:
            with open(self._installer_cache_path, "r", encoding="utf-8") as fh:
                data = json.load(fh)
            if isinstance(data, dict):
                return data
        except Exception:
            pass
        return {}

    def _store_installer_cache(self, payload: dict) -> None:
        try:
            with open(self._installer_cache_path, "w", encoding="utf-8") as fh:
                json.dump(payload, fh, indent=2)
            _restrict_permissions(self._installer_cache_path)
        except Exception:
            pass

    def cache_installer_code(self, code: str, consumer: Optional[str] = None) -> None:
        normalized = (code or "").strip()
        if not normalized:
            return
        payload = self._load_installer_cache()
        payload["code"] = normalized
        consumers = set()
        existing = payload.get("consumed")
        if isinstance(existing, list):
            consumers = {str(item).upper() for item in existing if isinstance(item, str)}
        if consumer:
            consumers.add(str(consumer).upper())
        payload["consumed"] = sorted(consumers)
        payload["updated_at"] = int(time.time())
        self._store_installer_cache(payload)

    def load_cached_installer_code(self) -> Optional[str]:
        payload = self._load_installer_cache()
        code = payload.get("code")
        if isinstance(code, str):
            stripped = code.strip()
            if stripped:
                return stripped
        return None

    def mark_installer_code_consumed(self, consumer: Optional[str] = None) -> None:
        payload = self._load_installer_cache()
        if not payload:
            return
        consumers = set()
        existing = payload.get("consumed")
        if isinstance(existing, list):
            consumers = {str(item).upper() for item in existing if isinstance(item, str)}
        if consumer:
            consumers.add(str(consumer).upper())
        payload["consumed"] = sorted(consumers)
        payload["updated_at"] = int(time.time())

        code_present = isinstance(payload.get("code"), str) and payload["code"].strip()
        should_clear = False
        if not code_present:
            should_clear = True
        else:
            required_consumers = {"SYSTEM", "CURRENTUSER"}
            if required_consumers.issubset(consumers):
                should_clear = True
            else:
                remaining = required_consumers - consumers
                if not remaining:
                    should_clear = True
                else:
                    exists_other = False
                    for other in remaining:
                        if other == "SYSTEM":
                            cfg_name = "agent_settings_SYSTEM.json"
                        elif other == "CURRENTUSER":
                            cfg_name = "agent_settings_CURRENTUSER.json"
                        else:
                            cfg_name = None
                        if not cfg_name:
                            continue
                        path = os.path.join(self.settings_dir, cfg_name)
                        if os.path.isfile(path):
                            exists_other = True
                            break
                    if not exists_other:
                        should_clear = True

        if should_clear:
            payload.pop("code", None)
            payload["consumed"] = []

        if payload.get("code") or payload.get("consumed"):
            self._store_installer_cache(payload)
        else:
            try:
                if os.path.isfile(self._installer_cache_path):
                    os.remove(self._installer_cache_path)
            except Exception:
                pass
