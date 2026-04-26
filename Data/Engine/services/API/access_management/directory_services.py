# ======================================================
# Data\Engine\services\API\access_management\directory_services.py
# Description: Directory service provider management and LDAP/Active Directory authentication helpers.
#
# API Endpoints (if applicable):
# - GET /api/directory/providers (Token Authenticated (Admin)) - Lists configured directory providers.
# - POST /api/directory/providers (Token Authenticated (Admin)) - Creates a directory provider.
# - PATCH /api/directory/providers/<provider_id> (Token Authenticated (Admin)) - Updates a directory provider.
# - DELETE /api/directory/providers/<provider_id> (Token Authenticated (Admin)) - Deletes a directory provider.
# - POST /api/directory/providers/<provider_id>/test (Token Authenticated (Admin)) - Tests provider connectivity.
# - POST /api/directory/providers/<provider_id>/sync (Token Authenticated (Admin)) - Syncs cached directory users.
# - POST /api/directory/providers/certificate (Token Authenticated (Admin)) - Downloads LDAPS certificate metadata for operator trust.
# - POST /api/users/<username>/directory-cache (Token Authenticated (Admin)) - Enables or disables a cached directory user.
# ======================================================

"""LDAP, LDAPS, and Active Directory support for Borealis operator auth."""
from __future__ import annotations

import base64
import json
import os
import socket
import ssl
import tempfile
import time
from contextlib import contextmanager
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any, Dict, Iterable, List, Mapping, Optional, Sequence, Tuple
from urllib.parse import urlparse

from Data.Engine.db import dbapi as sqlite3
from cryptography import x509
from cryptography.hazmat.primitives import hashes
from cryptography.x509.oid import ExtensionOID, NameOID
from flask import Blueprint, Flask, jsonify, request, session
from itsdangerous import BadSignature, SignatureExpired, URLSafeTimedSerializer

try:  # pragma: no cover - exercised through monkeypatch/unit environments.
    from ldap3 import ALL, BASE, SIMPLE, SUBTREE, Connection, Server, Tls  # type: ignore
    from ldap3.utils.conv import escape_filter_chars  # type: ignore
except Exception:  # pragma: no cover - optional dependency.
    ALL = BASE = SIMPLE = SUBTREE = Connection = Server = Tls = None  # type: ignore

    def escape_filter_chars(value: str) -> str:  # type: ignore
        return value


try:  # pragma: no cover - optional dependency.
    import gssapi  # type: ignore
except Exception:  # pragma: no cover - optional dependency.
    gssapi = None  # type: ignore

if TYPE_CHECKING:  # pragma: no cover - typing helper
    from .. import EngineServiceAdapters

from ...auth.bootstrap_state import operator_auth_allowed
from ...auth.context import revalidate_operator_identity
from ...auth.secrets import require_app_secret

LOCAL_AUTH_SOURCE = "local"
DIRECTORY_AUTH_SOURCE = "directory"
DIRECTORY_PASSWORD_PLACEHOLDER = "__directory_auth__"
DEFAULT_SYNC_INTERVAL_SECONDS = 60


def _now_ts() -> int:
    return int(time.time())


def _as_bool(value: Any) -> bool:
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return bool(value)
    return str(value or "").strip().lower() in {"1", "true", "yes", "on", "enabled"}


def _as_int(value: Any, default: int = 0) -> int:
    try:
        return int(value)
    except (TypeError, ValueError):
        return default


def _clean_text(value: Any) -> str:
    return str(value or "").strip()


def _normalize_provider_type(value: Any) -> str:
    text = _clean_text(value).lower().replace("-", "_").replace(" ", "_")
    if text in {"ad", "active_directory", "activedirectory"}:
        return "active_directory"
    return "ldap"


def _canonical_role(value: Any) -> str:
    role = _clean_text(value).title()
    return role if role in {"Admin", "User"} else "User"


def _json_list(value: Any) -> List[str]:
    if isinstance(value, list):
        items = value
    else:
        try:
            parsed = json.loads(str(value or "[]"))
            items = parsed if isinstance(parsed, list) else []
        except Exception:
            items = []
    return [_clean_text(item) for item in items if _clean_text(item)]


def _json_dumps_list(values: Iterable[Any]) -> str:
    return json.dumps([_clean_text(value) for value in values if _clean_text(value)])


def _json_object(value: Any) -> Dict[str, str]:
    if isinstance(value, dict):
        raw = value
    else:
        try:
            parsed = json.loads(str(value or "{}"))
            raw = parsed if isinstance(parsed, dict) else {}
        except Exception:
            raw = {}
    return {_clean_text(key).lower(): _clean_text(item) for key, item in raw.items() if _clean_text(key) and _clean_text(item)}


def _json_dumps_object(values: Mapping[str, Any]) -> str:
    return json.dumps(
        {
            _clean_text(key).lower(): _clean_text(value)
            for key, value in values.items()
            if _clean_text(key) and _clean_text(value)
        },
        sort_keys=True,
    )


def _rows_to_dicts(cursor: Any, rows: Sequence[Sequence[Any]]) -> List[Dict[str, Any]]:
    keys = [str(item[0]) for item in (cursor.description or [])]
    return [dict(zip(keys, list(row))) for row in rows]


def _entry_attr(attrs: Mapping[str, Any], name: str) -> Any:
    wanted = _clean_text(name).lower()
    if not wanted:
        return None
    for key, value in attrs.items():
        if _clean_text(key).lower() == wanted:
            return value
    return None


def _first_attr(attrs: Mapping[str, Any], *names: str) -> str:
    for name in names:
        value = _entry_attr(attrs, name)
        if isinstance(value, list):
            value = value[0] if value else ""
        if isinstance(value, bytes):
            try:
                value = value.decode("utf-8")
            except Exception:
                value = base64.b64encode(value).decode("ascii")
        text = _clean_text(value)
        if text:
            return text
    return ""


def _list_attr(attrs: Mapping[str, Any], name: str) -> List[str]:
    value = _entry_attr(attrs, name)
    if value is None:
        return []
    if not isinstance(value, list):
        value = [value]
    result: List[str] = []
    for item in value:
        if isinstance(item, bytes):
            try:
                item = item.decode("utf-8")
            except Exception:
                item = base64.b64encode(item).decode("ascii")
        text = _clean_text(item)
        if text:
            result.append(text)
    return result


def _domain_hint(raw_username: str) -> Tuple[str, str]:
    text = _clean_text(raw_username)
    if "\\" in text:
        domain, account = text.split("\\", 1)
        return _clean_text(account), _clean_text(domain).lower()
    if "@" in text:
        account, domain = text.rsplit("@", 1)
        return _clean_text(account), _clean_text(domain).lower()
    return text, ""


def _provider_matches_domain(provider: Mapping[str, Any], domain: str) -> bool:
    if not domain:
        return True
    suffix = _clean_text(provider.get("domain_suffix")).lower().lstrip("@")
    realm = _clean_text(provider.get("kerberos_realm")).lower()
    if suffix and domain in {suffix, suffix.split(".", 1)[0]}:
        return True
    if realm and domain in {realm, realm.split(".", 1)[0]}:
        return True
    return False


def _server_urls(provider: Mapping[str, Any]) -> List[str]:
    scheme = "ldaps" if _as_bool(provider.get("use_ldaps")) else "ldap"
    urls = []
    for raw in _json_list(provider.get("server_urls_json")):
        text = _clean_text(raw)
        if not text:
            continue
        urls.append(text if "://" in text else f"{scheme}://{text}")
    return urls


def _host_overrides(provider: Mapping[str, Any]) -> Dict[str, str]:
    return _json_object(provider.get("host_overrides_json"))


def _split_server_urls(value: Any, *, use_ldaps: bool = True) -> List[str]:
    if isinstance(value, str):
        items = [part.strip() for part in value.replace(",", "\n").splitlines()]
    elif isinstance(value, list):
        items = [_clean_text(item) for item in value]
    else:
        items = []
    provider = {
        "server_urls_json": _json_dumps_list(items),
        "use_ldaps": 1 if use_ldaps else 0,
    }
    return _server_urls(provider)


def _split_host_overrides(value: Any) -> Dict[str, str]:
    if isinstance(value, dict):
        return _json_object(value)
    overrides: Dict[str, str] = {}
    for raw_line in str(value or "").replace(",", "\n").splitlines():
        line = raw_line.strip()
        if not line:
            continue
        if "=" in line:
            host, connect_host = line.split("=", 1)
        elif "|" in line:
            host, connect_host = line.split("|", 1)
        else:
            parts = line.split()
            if len(parts) != 2:
                continue
            host, connect_host = parts
        host = _clean_text(host).lower()
        connect_host = _clean_text(connect_host)
        if host and connect_host:
            overrides[host] = connect_host
    return overrides


def _parse_ldap_url(server_url: str, *, default_scheme: str = "ldap") -> Tuple[str, str, int, str]:
    text = _clean_text(server_url)
    if not text:
        raise DirectoryAuthError("missing_server", "LDAP server URL is required.", 400)
    scheme = _clean_text(default_scheme).lower() or "ldap"
    parsed = urlparse(text if "://" in text else f"{scheme}://{text}")
    scheme = parsed.scheme.lower()
    if scheme not in {"ldap", "ldaps"}:
        raise DirectoryAuthError("invalid_server_url", "LDAP server URL must use ldap:// or ldaps://.", 400)
    host = _clean_text(parsed.hostname)
    if not host:
        raise DirectoryAuthError("invalid_server_url", "LDAP server URL is missing a host.", 400)
    try:
        port = int(parsed.port or (636 if scheme == "ldaps" else 389))
    except ValueError as exc:
        raise DirectoryAuthError("invalid_server_url", "LDAP server URL has an invalid port.", 400) from exc
    if port <= 0 or port > 65535:
        raise DirectoryAuthError("invalid_server_url", "LDAP server URL has an invalid port.", 400)
    normalized = f"{scheme}://{host}:{port}"
    return scheme, host, port, normalized


def _parse_ldaps_url(server_url: str) -> Tuple[str, int, str]:
    scheme, host, port, normalized = _parse_ldap_url(server_url, default_scheme="ldaps")
    if scheme != "ldaps":
        raise DirectoryAuthError("ldaps_required", "Certificate download requires an LDAPS server URL.", 400)
    return host, port, normalized


def _connection_target(provider: Mapping[str, Any], server_url: str) -> Dict[str, Any]:
    scheme, host, port, normalized = _parse_ldap_url(
        server_url,
        default_scheme="ldaps" if _as_bool(provider.get("use_ldaps")) else "ldap",
    )
    overrides = _host_overrides(provider)
    return {
        "scheme": scheme,
        "host": host,
        "connect_host": overrides.get(host.lower(), host),
        "port": port,
        "server_url": normalized,
    }


def _fingerprint_sha256(cert: x509.Certificate) -> str:
    digest = cert.fingerprint(hashes.SHA256()).hex().upper()
    return ":".join(digest[index:index + 2] for index in range(0, len(digest), 2))


def _certificate_time(cert: x509.Certificate, attr: str) -> str:
    value = getattr(cert, f"{attr}_utc", None)
    if value is None:
        value = getattr(cert, attr)
    return value.isoformat().replace("+00:00", "Z")


def _certificate_common_name(name: x509.Name) -> str:
    values = name.get_attributes_for_oid(NameOID.COMMON_NAME)
    return values[0].value if values else ""


def _certificate_san_entries(cert: x509.Certificate) -> Tuple[List[str], List[str]]:
    try:
        san = cert.extensions.get_extension_for_oid(ExtensionOID.SUBJECT_ALTERNATIVE_NAME).value
    except Exception:
        return [], []
    dns_names = [str(item) for item in san.get_values_for_type(x509.DNSName)]
    ip_addresses = [str(item) for item in san.get_values_for_type(x509.IPAddress)]
    return dns_names, ip_addresses


def _certificate_metadata(der_bytes: bytes, *, server_url: str, host: str, port: int) -> Dict[str, Any]:
    cert = x509.load_der_x509_certificate(der_bytes)
    dns_names, ip_addresses = _certificate_san_entries(cert)
    pem = ssl.DER_cert_to_PEM_cert(der_bytes)
    return {
        "server_url": server_url,
        "host": host,
        "port": port,
        "subject": cert.subject.rfc4514_string(),
        "issuer": cert.issuer.rfc4514_string(),
        "common_name": _certificate_common_name(cert.subject),
        "serial_number": format(cert.serial_number, "X"),
        "sha256_fingerprint": _fingerprint_sha256(cert),
        "not_before": _certificate_time(cert, "not_valid_before"),
        "not_after": _certificate_time(cert, "not_valid_after"),
        "dns_names": dns_names,
        "ip_addresses": ip_addresses,
        "pem": pem,
    }


def _fetch_ldaps_certificate(server_url: str, *, host_overrides: Optional[Mapping[str, str]] = None, timeout: int = 10) -> Dict[str, Any]:
    host, port, normalized_url = _parse_ldaps_url(server_url)
    connect_host = _json_object(host_overrides or {}).get(host.lower(), host)
    context = ssl._create_unverified_context()
    context.check_hostname = False
    context.verify_mode = ssl.CERT_NONE
    try:
        with socket.create_connection((connect_host, port), timeout=timeout) as raw_socket:
            with context.wrap_socket(raw_socket, server_hostname=host) as tls_socket:
                der_bytes = tls_socket.getpeercert(binary_form=True)
    except DirectoryAuthError:
        raise
    except Exception as exc:
        raise DirectoryAuthError("certificate_download_failed", str(exc), 502) from exc
    if not der_bytes:
        raise DirectoryAuthError("certificate_download_failed", "LDAPS server did not present a certificate.", 502)
    metadata = _certificate_metadata(der_bytes, server_url=normalized_url, host=host, port=port)
    metadata["connect_host"] = connect_host
    return metadata


def _pem_certificates(pem_text: str) -> List[x509.Certificate]:
    text = _clean_text(pem_text)
    if not text:
        return []
    certificates: List[x509.Certificate] = []
    block: List[str] = []
    inside = False
    for line in text.splitlines():
        stripped = line.strip()
        if stripped == "-----BEGIN CERTIFICATE-----":
            block = [stripped]
            inside = True
            continue
        if not inside:
            continue
        block.append(stripped)
        if stripped == "-----END CERTIFICATE-----":
            cert_pem = ("\n".join(block) + "\n").encode("ascii")
            certificates.append(x509.load_pem_x509_certificate(cert_pem))
            block = []
            inside = False
    return certificates


def _certificate_is_ca(cert: x509.Certificate) -> bool:
    try:
        constraints = cert.extensions.get_extension_for_class(x509.BasicConstraints).value
        return bool(constraints.ca)
    except Exception:
        return False


def _pem_contains_pinned_leaf(pem_text: str) -> bool:
    try:
        certs = _pem_certificates(pem_text)
    except Exception:
        return False
    return any(not _certificate_is_ca(cert) for cert in certs)


def _pinned_certificate_fingerprints(pem_text: str) -> List[str]:
    return [_fingerprint_sha256(cert) for cert in _pem_certificates(pem_text) if not _certificate_is_ca(cert)]


def _verify_pinned_ldaps_certificate(server_url: str, pem_text: str, *, host_overrides: Optional[Mapping[str, str]] = None) -> None:
    expected = set(_pinned_certificate_fingerprints(pem_text))
    if not expected:
        return
    certificate = _fetch_ldaps_certificate(server_url, host_overrides=host_overrides)
    observed = _clean_text(certificate.get("sha256_fingerprint"))
    if observed not in expected:
        raise DirectoryAuthError(
            "pinned_certificate_mismatch",
            "LDAPS certificate does not match the trusted certificate pinned for this provider.",
            502,
        )


@contextmanager
def _temporary_text_file(contents: str):
    path = ""
    try:
        with tempfile.NamedTemporaryFile("w", encoding="utf-8", delete=False) as handle:
            handle.write(contents)
            path = handle.name
        yield path
    finally:
        if path:
            try:
                os.unlink(path)
            except OSError:
                pass


@contextmanager
def _temporary_binary_file(contents_b64: str):
    path = ""
    try:
        raw = base64.b64decode(_clean_text(contents_b64), validate=True)
        with tempfile.NamedTemporaryFile("wb", delete=False) as handle:
            handle.write(raw)
            path = handle.name
        yield path
    finally:
        if path:
            try:
                os.unlink(path)
            except OSError:
                pass


@contextmanager
def _kerberos_environment(provider: Mapping[str, Any], keytab_base64: str = ""):
    realm = _clean_text(provider.get("kerberos_realm")).upper()
    kdc = _clean_text(provider.get("kerberos_kdc"))
    domain = _clean_text(provider.get("domain_suffix")).lower().lstrip("@")
    config = ["[libdefaults]", f" default_realm = {realm}", " rdns = false"]
    if kdc:
        config.extend([" dns_lookup_realm = false", " dns_lookup_kdc = false"])
    if realm and kdc:
        config.extend(["[realms]", f" {realm} = {{", f"  kdc = {kdc}", " }"])
    if realm and domain:
        config.extend(["[domain_realm]", f" .{domain} = {realm}", f" {domain} = {realm}"])
    old_config = os.environ.get("KRB5_CONFIG")
    old_keytab = os.environ.get("KRB5_KTNAME")
    with _temporary_text_file("\n".join(config) + "\n") as krb5_config:
        if keytab_base64:
            with _temporary_binary_file(keytab_base64) as keytab_path:
                os.environ["KRB5_CONFIG"] = krb5_config
                os.environ["KRB5_KTNAME"] = keytab_path
                try:
                    yield
                finally:
                    _restore_env("KRB5_CONFIG", old_config)
                    _restore_env("KRB5_KTNAME", old_keytab)
        else:
            os.environ["KRB5_CONFIG"] = krb5_config
            try:
                yield
            finally:
                _restore_env("KRB5_CONFIG", old_config)
                _restore_env("KRB5_KTNAME", old_keytab)


def _restore_env(name: str, value: Optional[str]) -> None:
    if value is None:
        os.environ.pop(name, None)
    else:
        os.environ[name] = value


class DirectoryAuthError(RuntimeError):
    def __init__(self, code: str, message: str, status_code: int = 401) -> None:
        super().__init__(message)
        self.code = code
        self.status_code = status_code


@dataclass
class DirectoryLoginResult:
    username: str
    display_name: str
    role: str
    provider_id: int
    provider_name: str
    domain: str
    dn: str
    subject: str
    groups: List[str]


class DirectoryAuthenticationManager:
    """Performs provider lookup, password verification, and JIT user cache updates."""

    def __init__(
        self,
        *,
        db_conn_factory,
        aegis_cipher_service,
        logger,
        service_log=None,
    ) -> None:
        self.db_conn_factory = db_conn_factory
        self.aegis_cipher_service = aegis_cipher_service
        self.logger = logger
        self.service_log = service_log

    def _db_conn(self) -> sqlite3.Connection:
        return self.db_conn_factory()

    def _encrypt_secret(self, value: str) -> str:
        if not _clean_text(value):
            return ""
        return self.aegis_cipher_service.encrypt_secret_for_text(value) or ""

    def _decrypt_secret(self, value: Any) -> str:
        text = _clean_text(value)
        if not text:
            return ""
        return self.aegis_cipher_service.decrypt_secret_text(text)

    def load_providers(self, *, enabled_only: bool = False) -> List[Dict[str, Any]]:
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            where = "WHERE COALESCE(enabled, 0) = 1" if enabled_only else ""
            cur.execute(
                f"""
                SELECT *
                  FROM directory_providers
                  {where}
                 ORDER BY priority ASC, LOWER(name) ASC
                """
            )
            providers = _rows_to_dicts(cur, cur.fetchall())
            provider_ids = [int(item.get("id") or 0) for item in providers]
            mappings: Dict[int, List[Dict[str, Any]]] = {provider_id: [] for provider_id in provider_ids}
            if provider_ids:
                placeholders = ",".join("?" for _ in provider_ids)
                cur.execute(
                    f"""
                    SELECT provider_id, group_dn, role
                      FROM directory_provider_group_mappings
                     WHERE provider_id IN ({placeholders})
                     ORDER BY LOWER(group_dn) ASC, role ASC
                    """,
                    tuple(provider_ids),
                )
                for row in _rows_to_dicts(cur, cur.fetchall()):
                    mappings.setdefault(int(row.get("provider_id") or 0), []).append(
                        {"group_dn": _clean_text(row.get("group_dn")), "role": _canonical_role(row.get("role"))}
                    )
            for provider in providers:
                provider["group_mappings"] = mappings.get(int(provider.get("id") or 0), [])
            return providers
        finally:
            conn.close()

    def load_provider(self, provider_id: int) -> Optional[Dict[str, Any]]:
        for provider in self.load_providers(enabled_only=False):
            if int(provider.get("id") or 0) == int(provider_id):
                return provider
        return None

    def public_provider(self, provider: Mapping[str, Any]) -> Dict[str, Any]:
        return {
            "id": int(provider.get("id") or 0),
            "name": _clean_text(provider.get("name")),
            "provider_type": _normalize_provider_type(provider.get("provider_type")),
            "enabled": _as_bool(provider.get("enabled")),
            "priority": _as_int(provider.get("priority"), 100),
            "domain_suffix": _clean_text(provider.get("domain_suffix")),
            "server_urls": _json_list(provider.get("server_urls_json")),
            "host_overrides": _host_overrides(provider),
            "use_ldaps": _as_bool(provider.get("use_ldaps")),
            "tls_required": _as_bool(provider.get("tls_required")),
            "tls_ca_pem_present": bool(_clean_text(provider.get("tls_ca_pem"))),
            "base_dn": _clean_text(provider.get("base_dn")),
            "bind_dn": _clean_text(provider.get("bind_dn")),
            "bind_password_present": bool(_clean_text(provider.get("bind_password_encrypted"))),
            "user_search_filter": _clean_text(provider.get("user_search_filter")),
            "username_attribute": _clean_text(provider.get("username_attribute")) or self.default_username_attribute(provider),
            "display_name_attribute": _clean_text(provider.get("display_name_attribute")) or "displayName",
            "email_attribute": _clean_text(provider.get("email_attribute")) or "mail",
            "member_of_attribute": _clean_text(provider.get("member_of_attribute")) or "memberOf",
            "group_search_base_dn": _clean_text(provider.get("group_search_base_dn")),
            "nested_groups": _as_bool(provider.get("nested_groups")),
            "kerberos_realm": _clean_text(provider.get("kerberos_realm")),
            "kerberos_kdc": _clean_text(provider.get("kerberos_kdc")),
            "kerberos_keytab_present": bool(_clean_text(provider.get("kerberos_keytab_encrypted"))),
            "sync_interval_seconds": _as_int(provider.get("sync_interval_seconds"), DEFAULT_SYNC_INTERVAL_SECONDS),
            "last_sync_at": _as_int(provider.get("last_sync_at"), 0),
            "last_sync_status": _clean_text(provider.get("last_sync_status")),
            "last_sync_message": _clean_text(provider.get("last_sync_message")),
            "last_test_at": _as_int(provider.get("last_test_at"), 0),
            "last_test_status": _clean_text(provider.get("last_test_status")),
            "last_test_message": _clean_text(provider.get("last_test_message")),
            "created_at": _as_int(provider.get("created_at"), 0),
            "updated_at": _as_int(provider.get("updated_at"), 0),
            "group_mappings": [
                {"group_dn": _clean_text(item.get("group_dn")), "role": _canonical_role(item.get("role"))}
                for item in provider.get("group_mappings", [])
                if _clean_text(item.get("group_dn"))
            ],
        }

    def default_username_attribute(self, provider: Mapping[str, Any]) -> str:
        return "sAMAccountName" if _normalize_provider_type(provider.get("provider_type")) == "active_directory" else "uid"

    def _tls_for_target(self, provider: Mapping[str, Any], target: Mapping[str, Any], *, ca_path: str = ""):
        if Tls is None or target.get("scheme") != "ldaps":
            return None
        ca_pem = _clean_text(provider.get("tls_ca_pem"))
        host = _clean_text(target.get("host"))
        if ca_pem and _pem_contains_pinned_leaf(ca_pem):
            _verify_pinned_ldaps_certificate(_clean_text(target.get("server_url")), ca_pem, host_overrides=_host_overrides(provider))
            return Tls(validate=ssl.CERT_NONE, sni=host)
        if ca_path:
            return Tls(validate=ssl.CERT_REQUIRED, ca_certs_file=ca_path, valid_names=[host], sni=host)
        if _as_bool(provider.get("tls_required")):
            return Tls(validate=ssl.CERT_REQUIRED, valid_names=[host], sni=host)
        return Tls(validate=ssl.CERT_NONE, sni=host)

    def _server_for_target(self, target: Mapping[str, Any], tls: Any):
        return Server(
            _clean_text(target.get("connect_host")),
            port=_as_int(target.get("port"), 636 if target.get("scheme") == "ldaps" else 389),
            use_ssl=target.get("scheme") == "ldaps",
            get_info=ALL,
            connect_timeout=10,
            tls=tls,
        )

    def _service_connection(self, provider: Mapping[str, Any]):
        if Connection is None or Server is None:
            raise DirectoryAuthError("ldap_unavailable", "ldap3 is not installed.", 503)
        urls = _server_urls(provider)
        if not urls:
            raise DirectoryAuthError("missing_server", "Provider has no LDAP server URL.", 400)
        bind_dn = _clean_text(provider.get("bind_dn"))
        bind_password = self._decrypt_secret(provider.get("bind_password_encrypted"))
        ca_pem = _clean_text(provider.get("tls_ca_pem"))
        last_error: Optional[Exception] = None
        for url in urls:
            target = _connection_target(provider, url)
            if _as_bool(provider.get("tls_required")) and target.get("scheme") != "ldaps":
                last_error = RuntimeError("Strict TLS requires ldaps:// server URLs.")
                continue
            try:
                if ca_pem and Tls is not None and not _pem_contains_pinned_leaf(ca_pem):
                    with _temporary_text_file(ca_pem) as ca_path:
                        tls = self._tls_for_target(provider, target, ca_path=ca_path)
                        server = self._server_for_target(target, tls)
                        return Connection(server, user=bind_dn or None, password=bind_password or None, authentication=SIMPLE, auto_bind=True)
                server = self._server_for_target(target, self._tls_for_target(provider, target))
                return Connection(server, user=bind_dn or None, password=bind_password or None, authentication=SIMPLE, auto_bind=True)
            except Exception as exc:
                last_error = exc
        raise DirectoryAuthError("ldap_connect_failed", str(last_error or "LDAP connection failed."), 502)

    def test_provider(self, provider: Mapping[str, Any]) -> Tuple[bool, str]:
        provider_type = _normalize_provider_type(provider.get("provider_type"))
        if provider_type == "active_directory" and gssapi is None:
            return False, "python-gssapi is not installed."
        try:
            conn = self._service_connection(provider)
            try:
                base_dn = _clean_text(provider.get("base_dn"))
                if base_dn:
                    conn.search(base_dn, "(objectClass=*)", search_scope=BASE, attributes=["distinguishedName"])
                return True, "Provider connectivity verified."
            finally:
                try:
                    conn.unbind()
                except Exception:
                    pass
        except DirectoryAuthError as exc:
            return False, str(exc)
        except Exception as exc:
            return False, str(exc)

    def search_user(self, provider: Mapping[str, Any], login_name: str) -> Optional[Dict[str, Any]]:
        base_dn = _clean_text(provider.get("base_dn"))
        if not base_dn:
            return None
        conn = self._service_connection(provider)
        try:
            username_attr = _clean_text(provider.get("username_attribute")) or self.default_username_attribute(provider)
            display_attr = _clean_text(provider.get("display_name_attribute")) or "displayName"
            email_attr = _clean_text(provider.get("email_attribute")) or "mail"
            member_attr = _clean_text(provider.get("member_of_attribute")) or "memberOf"
            account_name, _domain = _domain_hint(login_name)
            escaped_login = escape_filter_chars(_clean_text(login_name))
            escaped_account = escape_filter_chars(_clean_text(account_name))
            filter_template = _clean_text(provider.get("user_search_filter"))
            if filter_template:
                search_filter = filter_template.format(
                    username=escaped_account,
                    login=escaped_login,
                    user=escaped_account,
                )
            elif _normalize_provider_type(provider.get("provider_type")) == "active_directory":
                search_filter = (
                    f"(|(sAMAccountName={escaped_account})(userPrincipalName={escaped_login})"
                    f"({email_attr}={escaped_login}))"
                )
            else:
                search_filter = f"({username_attr}={escaped_account or escaped_login})"
            attributes = list(
                dict.fromkeys(
                    [
                        username_attr,
                        "sAMAccountName",
                        "userPrincipalName",
                        display_attr,
                        email_attr,
                        member_attr,
                        "distinguishedName",
                        "entryUUID",
                        "objectGUID",
                    ]
                )
            )
            if not conn.search(base_dn, search_filter, search_scope=SUBTREE, attributes=attributes):
                return None
            entries = list(getattr(conn, "entries", []) or [])
            if len(entries) != 1:
                return None
            entry = entries[0]
            attrs = getattr(entry, "entry_attributes_as_dict", {}) or {}
            dn = _clean_text(getattr(entry, "entry_dn", "")) or _first_attr(attrs, "distinguishedName")
            groups = _list_attr(attrs, member_attr)
            if _as_bool(provider.get("nested_groups")) and _clean_text(provider.get("group_search_base_dn")) and dn:
                groups = sorted(set(groups + self._search_nested_groups(conn, provider, dn)))
            account = _first_attr(attrs, "userPrincipalName", username_attr, "sAMAccountName", email_attr) or _clean_text(login_name)
            display_name = _first_attr(attrs, display_attr, "cn", username_attr, "sAMAccountName") or account
            subject = _first_attr(attrs, "entryUUID", "objectGUID", "objectSid") or dn or account
            return {
                "dn": dn,
                "attrs": attrs,
                "account": account,
                "display_name": display_name,
                "subject": subject,
                "groups": groups,
            }
        finally:
            try:
                conn.unbind()
            except Exception:
                pass

    def _search_nested_groups(self, conn: Any, provider: Mapping[str, Any], user_dn: str) -> List[str]:
        base_dn = _clean_text(provider.get("group_search_base_dn"))
        if not base_dn:
            return []
        escaped_dn = escape_filter_chars(user_dn)
        search_filter = f"(member:1.2.840.113556.1.4.1941:={escaped_dn})"
        try:
            conn.search(base_dn, search_filter, search_scope=SUBTREE, attributes=["distinguishedName", "cn"])
            groups = []
            for entry in list(getattr(conn, "entries", []) or []):
                attrs = getattr(entry, "entry_attributes_as_dict", {}) or {}
                dn = _clean_text(getattr(entry, "entry_dn", "")) or _first_attr(attrs, "distinguishedName")
                if dn:
                    groups.append(dn)
            return groups
        except Exception:
            self.logger.debug("Nested directory group lookup failed.", exc_info=True)
            return []

    def _verify_ldap_password(self, provider: Mapping[str, Any], user_dn: str, password: str) -> None:
        if Connection is None or Server is None:
            raise DirectoryAuthError("ldap_unavailable", "ldap3 is not installed.", 503)
        if not user_dn:
            raise DirectoryAuthError("directory_user_not_found", "Directory user DN was not found.", 401)
        urls = _server_urls(provider)
        ca_pem = _clean_text(provider.get("tls_ca_pem"))
        last_error: Optional[Exception] = None
        for url in urls:
            target = _connection_target(provider, url)
            if _as_bool(provider.get("tls_required")) and target.get("scheme") != "ldaps":
                last_error = RuntimeError("Strict TLS requires ldaps:// server URLs.")
                continue
            try:
                if ca_pem and Tls is not None and not _pem_contains_pinned_leaf(ca_pem):
                    with _temporary_text_file(ca_pem) as ca_path:
                        server = self._server_for_target(target, self._tls_for_target(provider, target, ca_path=ca_path))
                        conn = Connection(server, user=user_dn, password=password, authentication=SIMPLE, auto_bind=True)
                else:
                    server = self._server_for_target(target, self._tls_for_target(provider, target))
                    conn = Connection(server, user=user_dn, password=password, authentication=SIMPLE, auto_bind=True)
                try:
                    conn.unbind()
                except Exception:
                    pass
                return
            except Exception as exc:
                last_error = exc
        raise DirectoryAuthError("invalid_username_or_password", str(last_error or "LDAP bind failed."), 401)

    def _verify_kerberos_password(self, provider: Mapping[str, Any], login_name: str, password: str) -> None:
        if gssapi is None:
            raise DirectoryAuthError("kerberos_unavailable", "python-gssapi is not installed.", 503)
        account_name, domain = _domain_hint(login_name)
        realm = _clean_text(provider.get("kerberos_realm")).upper()
        if not realm:
            realm = (_clean_text(provider.get("domain_suffix")).upper().lstrip("@") or domain.upper())
        principal = _clean_text(login_name)
        if "@" not in principal:
            principal = f"{account_name}@{realm}"
        keytab = self._decrypt_secret(provider.get("kerberos_keytab_encrypted"))
        with _kerberos_environment(provider, keytab):
            name = gssapi.Name(principal, name_type=gssapi.NameType.user)
            gssapi.raw.acquire_cred_with_password(name, password.encode("utf-8"), usage="initiate")

    def _mapped_role(self, provider: Mapping[str, Any], groups: Sequence[str]) -> Optional[str]:
        mappings = [
            {"group_dn": _clean_text(item.get("group_dn")).lower(), "role": _canonical_role(item.get("role"))}
            for item in provider.get("group_mappings", [])
            if _clean_text(item.get("group_dn"))
        ]
        if not mappings:
            return "User"
        group_set = {_clean_text(group).lower() for group in groups if _clean_text(group)}
        matched_role: Optional[str] = None
        for mapping in mappings:
            if mapping["group_dn"] in group_set:
                if mapping["role"] == "Admin":
                    return "Admin"
                matched_role = "User"
        return matched_role

    def authenticate_login(self, login_name: str, password: str) -> DirectoryLoginResult:
        login_name = _clean_text(login_name)
        if not login_name or not password:
            raise DirectoryAuthError("missing_credentials", "Directory username and password are required.", 400)
        account_name, domain = _domain_hint(login_name)
        providers = [
            provider
            for provider in self.load_providers(enabled_only=True)
            if _provider_matches_domain(provider, domain)
        ]
        if not providers:
            raise DirectoryAuthError("directory_provider_not_found", "No enabled directory provider matches this login.", 401)

        candidates: List[Tuple[Dict[str, Any], Dict[str, Any]]] = []
        for provider in providers:
            try:
                found = self.search_user(provider, login_name)
            except DirectoryAuthError:
                found = None
            except Exception:
                self.logger.debug("Directory user lookup failed for provider %s.", provider.get("id"), exc_info=True)
                found = None
            if found:
                candidates.append((provider, found))
            elif _normalize_provider_type(provider.get("provider_type")) == "active_directory" and domain:
                candidates.append(
                    (
                        provider,
                        {
                            "dn": "",
                            "account": login_name if "@" in login_name else f"{account_name}@{_clean_text(provider.get('domain_suffix')).lstrip('@')}",
                            "display_name": account_name,
                            "subject": login_name,
                            "groups": [],
                        },
                    )
                )

        if not candidates:
            raise DirectoryAuthError("invalid_username_or_password", "Invalid username or password.", 401)
        if not domain and len(candidates) > 1:
            raise DirectoryAuthError(
                "ambiguous_directory_username",
                "Multiple directory providers contain this username. Use a domain-qualified username.",
                409,
            )

        provider, user_info = candidates[0]
        provider_type = _normalize_provider_type(provider.get("provider_type"))
        if provider_type == "active_directory":
            self._verify_kerberos_password(provider, user_info.get("account") or login_name, password)
        else:
            self._verify_ldap_password(provider, _clean_text(user_info.get("dn")), password)

        groups = list(user_info.get("groups") or [])
        role = self._mapped_role(provider, groups)
        if not role:
            raise DirectoryAuthError("directory_group_not_allowed", "Directory user is not in an allowed group.", 403)

        result = self._upsert_directory_user(provider, user_info, role)
        self._audit(f"directory_login provider_id={result.provider_id} username={result.username} status=ok")
        return result

    def _canonical_directory_username(self, provider: Mapping[str, Any], user_info: Mapping[str, Any]) -> str:
        account = _clean_text(user_info.get("account"))
        if account:
            return account
        subject = _clean_text(user_info.get("subject"))
        if subject:
            return subject
        domain = _clean_text(provider.get("domain_suffix")).lstrip("@")
        return f"user@{domain}" if domain else "directory-user"

    def _upsert_directory_user(
        self,
        provider: Mapping[str, Any],
        user_info: Mapping[str, Any],
        role: str,
    ) -> DirectoryLoginResult:
        provider_id = int(provider.get("id") or 0)
        username = self._canonical_directory_username(provider, user_info)
        display_name = _clean_text(user_info.get("display_name")) or username
        domain = _clean_text(provider.get("domain_suffix")).lstrip("@")
        dn = _clean_text(user_info.get("dn"))
        subject = _clean_text(user_info.get("subject")) or dn or username
        groups = [_clean_text(group) for group in user_info.get("groups", []) if _clean_text(group)]
        now_ts = _now_ts()

        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id, COALESCE(auth_source, 'local'), COALESCE(directory_provider_id, 0), COALESCE(directory_disabled, 0)
                  FROM users
                 WHERE LOWER(username)=LOWER(?)
                 LIMIT 1
                """,
                (username,),
            )
            row = cur.fetchone()
            if row and _clean_text(row[1]).lower() != DIRECTORY_AUTH_SOURCE:
                raise DirectoryAuthError(
                    "directory_username_conflict",
                    "A local Borealis user already owns this username.",
                    409,
                )
            if row and int(row[2] or 0) not in {0, provider_id}:
                raise DirectoryAuthError(
                    "directory_username_conflict",
                    "Another directory provider already owns this username.",
                    409,
                )
            if row:
                cur.execute(
                    """
                    UPDATE users
                       SET display_name=?,
                           role=?,
                           auth_source=?,
                           directory_provider_id=?,
                           directory_subject=?,
                           directory_domain=?,
                           directory_dn=?,
                           directory_groups_json=?,
                           directory_last_sync_at=?,
                           directory_disabled=0,
                           directory_disabled_at=NULL,
                           mfa_disabled=0,
                           updated_at=?
                     WHERE id=?
                    """,
                    (
                        display_name,
                        role,
                        DIRECTORY_AUTH_SOURCE,
                        provider_id,
                        subject,
                        domain,
                        dn,
                        _json_dumps_list(groups),
                        now_ts,
                        now_ts,
                        int(row[0] or 0),
                    ),
                )
            else:
                cur.execute(
                    """
                    INSERT INTO users(
                        username,
                        display_name,
                        password_sha512,
                        role,
                        last_login,
                        created_at,
                        updated_at,
                        mfa_enabled,
                        mfa_disabled,
                        auth_reset_required,
                        auth_source,
                        directory_provider_id,
                        directory_subject,
                        directory_domain,
                        directory_dn,
                        directory_groups_json,
                        directory_last_sync_at,
                        directory_disabled
                    ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
                    """,
                    (
                        username,
                        display_name,
                        DIRECTORY_PASSWORD_PLACEHOLDER,
                        role,
                        0,
                        now_ts,
                        now_ts,
                        0,
                        0,
                        0,
                        DIRECTORY_AUTH_SOURCE,
                        provider_id,
                        subject,
                        domain,
                        dn,
                        _json_dumps_list(groups),
                        now_ts,
                        0,
                    ),
                )
            conn.commit()
        except DirectoryAuthError:
            conn.rollback()
            raise
        finally:
            conn.close()

        return DirectoryLoginResult(
            username=username,
            display_name=display_name,
            role=role,
            provider_id=provider_id,
            provider_name=_clean_text(provider.get("name")),
            domain=domain,
            dn=dn,
            subject=subject,
            groups=groups,
        )

    def sync_provider(self, provider: Mapping[str, Any]) -> Tuple[int, str]:
        provider_id = int(provider.get("id") or 0)
        ok, test_message = self.test_provider(provider)
        if not ok:
            raise DirectoryAuthError("provider_unreachable", test_message, 502)
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT username, directory_dn
                  FROM users
                 WHERE COALESCE(auth_source, 'local')=?
                   AND COALESCE(directory_provider_id, 0)=?
                   AND COALESCE(directory_disabled, 0)=0
                """,
                (DIRECTORY_AUTH_SOURCE, provider_id),
            )
            cached = _rows_to_dicts(cur, cur.fetchall())
        finally:
            conn.close()

        disabled = 0
        for user in cached:
            username = _clean_text(user.get("username"))
            try:
                found = self.search_user(provider, username)
            except Exception:
                found = None
            if found:
                continue
            self.set_directory_cache_disabled(username, disabled=True, allow_self=True)
            disabled += 1

        message = f"Directory sync completed. Disabled {disabled} cached user{'s' if disabled != 1 else ''}."
        self._update_provider_sync(provider_id, "ok", message)
        self._audit(f"directory_sync provider_id={provider_id} disabled={disabled} status=ok")
        return disabled, message

    def _update_provider_sync(self, provider_id: int, status: str, message: str) -> None:
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE directory_providers
                   SET last_sync_at=?,
                       last_sync_status=?,
                       last_sync_message=?,
                       updated_at=?
                 WHERE id=?
                """,
                (_now_ts(), status, message[:500], _now_ts(), provider_id),
            )
            conn.commit()
        finally:
            conn.close()

    def set_directory_cache_disabled(self, username: str, *, disabled: bool, allow_self: bool = False) -> None:
        username_norm = _clean_text(username)
        if not username_norm:
            raise DirectoryAuthError("invalid_username", "Invalid username.", 400)
        now_ts = _now_ts()
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE users
                   SET directory_disabled=?,
                       directory_disabled_at=?,
                       updated_at=?
                 WHERE LOWER(username)=LOWER(?)
                   AND COALESCE(auth_source, 'local')=?
                """,
                (1 if disabled else 0, now_ts if disabled else None, now_ts, username_norm, DIRECTORY_AUTH_SOURCE),
            )
            if int(cur.rowcount or 0) <= 0:
                conn.rollback()
                raise DirectoryAuthError("directory_user_not_found", "Directory user not found.", 404)
            conn.commit()
        finally:
            conn.close()

    def _audit(self, message: str) -> None:
        if not callable(self.service_log):
            return
        try:
            self.service_log("directory_services", message, None)
        except Exception:
            pass


class DirectoryManagementService:
    """Admin API surface for directory providers."""

    def __init__(self, app: Flask, adapters: "EngineServiceAdapters") -> None:
        self.app = app
        self.adapters = adapters
        self.db_conn_factory = adapters.db_conn_factory
        self.logger = adapters.context.logger
        self.aegis_cipher_service = adapters.aegis_cipher_service
        self.manager = DirectoryAuthenticationManager(
            db_conn_factory=self.db_conn_factory,
            aegis_cipher_service=self.aegis_cipher_service,
            logger=self.logger,
            service_log=adapters.service_log,
        )

    def _db_conn(self) -> sqlite3.Connection:
        return self.db_conn_factory()

    def _token_serializer(self) -> URLSafeTimedSerializer:
        secret = require_app_secret(self.app)
        return URLSafeTimedSerializer(secret, salt="borealis-auth")

    def _current_user(self) -> Optional[Dict[str, Any]]:
        if not operator_auth_allowed(
            db_conn_factory=self.db_conn_factory,
            aegis_cipher_service=self.aegis_cipher_service,
        ):
            return None
        username = session.get("username")
        role = session.get("role") or "User"
        if username:
            return revalidate_operator_identity(
                self.db_conn_factory,
                username=str(username),
                role=str(role),
                logger=self.logger,
            )
        token = None
        auth_header = request.headers.get("Authorization") or ""
        if auth_header.lower().startswith("bearer "):
            token = auth_header.split(" ", 1)[1].strip()
        if not token:
            token = request.cookies.get("borealis_auth")
        if not token:
            return None
        try:
            data = self._token_serializer().loads(
                token,
                max_age=int(os.environ.get("BOREALIS_TOKEN_TTL_SECONDS", 60 * 60 * 24 * 30)),
            )
        except (BadSignature, SignatureExpired, Exception):
            return None
        return revalidate_operator_identity(
            self.db_conn_factory,
            username=str(data.get("u") or ""),
            role=str(data.get("r") or "User"),
            logger=self.logger,
        )

    def _require_admin(self) -> Optional[Tuple[Dict[str, Any], int]]:
        user = self._current_user()
        if not user:
            return {"error": "unauthorized"}, 401
        if (user.get("role") or "").lower() != "admin":
            return {"error": "forbidden"}, 403
        return None

    def list_providers(self):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        return jsonify({"providers": [self.manager.public_provider(item) for item in self.manager.load_providers()]})

    def save_provider(self, provider_id: Optional[int] = None):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status

        payload = request.get_json(silent=True) or {}
        existing = self.manager.load_provider(provider_id) if provider_id else None
        if provider_id and not existing:
            return jsonify({"error": "provider_not_found"}), 404
        try:
            saved = self._persist_provider(payload, existing=existing)
            return jsonify({"status": "ok", "provider": self.manager.public_provider(saved)})
        except DirectoryAuthError as exc:
            return jsonify({"error": exc.code, "message": str(exc)}), exc.status_code
        except sqlite3.IntegrityError:
            return jsonify({"error": "provider_name_exists"}), 409
        except Exception as exc:
            self.logger.debug("Failed to save directory provider", exc_info=True)
            return jsonify({"error": str(exc)}), 500

    def delete_provider(self, provider_id: int):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                "SELECT COUNT(*) FROM users WHERE COALESCE(auth_source, 'local')=? AND COALESCE(directory_provider_id, 0)=?",
                (DIRECTORY_AUTH_SOURCE, int(provider_id)),
            )
            if int((cur.fetchone() or [0])[0] or 0) > 0:
                return jsonify({"error": "provider_has_cached_users"}), 409
            cur.execute("DELETE FROM directory_provider_group_mappings WHERE provider_id=?", (int(provider_id),))
            cur.execute("DELETE FROM directory_providers WHERE id=?", (int(provider_id),))
            deleted = int(cur.rowcount or 0)
            conn.commit()
            if deleted <= 0:
                return jsonify({"error": "provider_not_found"}), 404
            return jsonify({"status": "ok"})
        finally:
            conn.close()

    def test_provider(self, provider_id: int):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        provider = self.manager.load_provider(provider_id)
        if not provider:
            return jsonify({"error": "provider_not_found"}), 404
        ok, message = self.manager.test_provider(provider)
        status = "ok" if ok else "failed"
        now_ts = _now_ts()
        conn = self._db_conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                UPDATE directory_providers
                   SET last_test_at=?,
                       last_test_status=?,
                       last_test_message=?,
                       updated_at=?
                 WHERE id=?
                """,
                (now_ts, status, message[:500], now_ts, int(provider_id)),
            )
            conn.commit()
        finally:
            conn.close()
        if callable(self.adapters.service_log):
            self.adapters.service_log("directory_services", f"provider_test provider_id={provider_id} status={status}", None)
        return jsonify({"status": status, "ok": ok, "message": message}), 200 if ok else 502

    def sync_provider(self, provider_id: int):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        provider = self.manager.load_provider(provider_id)
        if not provider:
            return jsonify({"error": "provider_not_found"}), 404
        try:
            disabled, message = self.manager.sync_provider(provider)
            return jsonify({"status": "ok", "disabled_users": disabled, "message": message})
        except Exception as exc:
            message = str(exc) or "Directory sync failed."
            self.manager._update_provider_sync(provider_id, "failed", message)
            return jsonify({"error": "sync_failed", "message": message}), 502

    def fetch_certificate(self):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        payload = request.get_json(silent=True) or {}
        raw_urls = payload.get("server_urls") if "server_urls" in payload else payload.get("server_url")
        urls = _split_server_urls(raw_urls, use_ldaps=_as_bool(payload.get("use_ldaps", True)))
        host_overrides = _split_host_overrides(payload.get("host_overrides", {}))
        if not urls:
            return jsonify({"error": "missing_server", "message": "LDAP server URL is required."}), 400
        last_error: Optional[DirectoryAuthError] = None
        for url in urls:
            try:
                certificate = _fetch_ldaps_certificate(url, host_overrides=host_overrides)
                if callable(self.adapters.service_log):
                    self.adapters.service_log("directory_services", f"certificate_download server={certificate.get('server_url')}", None)
                return jsonify({"status": "ok", "certificate": certificate})
            except DirectoryAuthError as exc:
                last_error = exc
                if exc.status_code == 400:
                    break
        error = last_error or DirectoryAuthError("certificate_download_failed", "Certificate download failed.", 502)
        return jsonify({"error": error.code, "message": str(error)}), error.status_code

    def set_user_cache(self, username: str):
        requirement = self._require_admin()
        if requirement:
            payload, status = requirement
            return jsonify(payload), status
        current_user = self._current_user() or {}
        if _clean_text(current_user.get("username")).lower() == _clean_text(username).lower():
            return jsonify({"error": "cannot_disable_self"}), 400
        payload = request.get_json(silent=True) or {}
        disabled = _as_bool(payload.get("disabled", True))
        try:
            self.manager.set_directory_cache_disabled(username, disabled=disabled)
            return jsonify({"status": "ok", "username": username, "directory_disabled": disabled})
        except DirectoryAuthError as exc:
            return jsonify({"error": exc.code, "message": str(exc)}), exc.status_code

    def _persist_provider(self, payload: Mapping[str, Any], *, existing: Optional[Mapping[str, Any]]) -> Dict[str, Any]:
        now_ts = _now_ts()
        name = _clean_text(payload.get("name") if "name" in payload else (existing or {}).get("name"))
        if not name:
            raise DirectoryAuthError("name_required", "Provider name is required.", 400)
        provider_type = _normalize_provider_type(payload.get("provider_type") if "provider_type" in payload else (existing or {}).get("provider_type"))
        current_enabled = _as_bool((existing or {}).get("enabled"))
        enabled = _as_bool(payload.get("enabled")) if "enabled" in payload else current_enabled
        if enabled and _clean_text((existing or {}).get("last_test_status")).lower() != "ok":
            raise DirectoryAuthError("test_required", "Provider must pass connectivity test before it can be enabled.", 409)

        def field(name_key: str, default: Any = "") -> Any:
            return payload.get(name_key) if name_key in payload else (existing or {}).get(name_key, default)

        server_urls_raw = payload.get("server_urls") if "server_urls" in payload else _json_list((existing or {}).get("server_urls_json"))
        if isinstance(server_urls_raw, str):
            server_urls = [part.strip() for part in server_urls_raw.replace(",", "\n").splitlines() if part.strip()]
        else:
            server_urls = [_clean_text(item) for item in (server_urls_raw or []) if _clean_text(item)]

        host_overrides_raw = payload.get("host_overrides") if "host_overrides" in payload else _host_overrides(existing or {})
        host_overrides = _split_host_overrides(host_overrides_raw)

        bind_password_encrypted = _clean_text((existing or {}).get("bind_password_encrypted"))
        if "bind_password" in payload:
            bind_password = _clean_text(payload.get("bind_password"))
            bind_password_encrypted = self.manager._encrypt_secret(bind_password) if bind_password else ""

        keytab_encrypted = _clean_text((existing or {}).get("kerberos_keytab_encrypted"))
        if "kerberos_keytab_base64" in payload:
            keytab = _clean_text(payload.get("kerberos_keytab_base64"))
            if keytab:
                base64.b64decode(keytab, validate=True)
            keytab_encrypted = self.manager._encrypt_secret(keytab) if keytab else ""

        row_values = {
            "name": name,
            "provider_type": provider_type,
            "enabled": 1 if enabled else 0,
            "priority": _as_int(field("priority", 100), 100),
            "domain_suffix": _clean_text(field("domain_suffix", "")),
            "server_urls_json": _json_dumps_list(server_urls),
            "host_overrides_json": _json_dumps_object(host_overrides),
            "use_ldaps": 1 if _as_bool(field("use_ldaps", False)) else 0,
            "tls_required": 1 if _as_bool(field("tls_required", True)) else 0,
            "tls_ca_pem": _clean_text(field("tls_ca_pem", "")),
            "base_dn": _clean_text(field("base_dn", "")),
            "bind_dn": _clean_text(field("bind_dn", "")),
            "bind_password_encrypted": bind_password_encrypted,
            "user_search_filter": _clean_text(field("user_search_filter", "")),
            "username_attribute": _clean_text(field("username_attribute", "")),
            "display_name_attribute": _clean_text(field("display_name_attribute", "displayName")),
            "email_attribute": _clean_text(field("email_attribute", "mail")),
            "member_of_attribute": _clean_text(field("member_of_attribute", "memberOf")),
            "group_search_base_dn": _clean_text(field("group_search_base_dn", "")),
            "nested_groups": 1 if _as_bool(field("nested_groups", provider_type == "active_directory")) else 0,
            "kerberos_realm": _clean_text(field("kerberos_realm", "")).upper(),
            "kerberos_kdc": _clean_text(field("kerberos_kdc", "")),
            "kerberos_keytab_encrypted": keytab_encrypted,
            "sync_interval_seconds": max(60, _as_int(field("sync_interval_seconds", DEFAULT_SYNC_INTERVAL_SECONDS), DEFAULT_SYNC_INTERVAL_SECONDS)),
            "updated_at": now_ts,
        }

        if provider_type == "active_directory" and not row_values["kerberos_realm"]:
            raise DirectoryAuthError("kerberos_realm_required", "Active Directory providers require a Kerberos realm.", 400)

        conn = self._db_conn()
        try:
            cur = conn.cursor()
            if existing:
                provider_id = int(existing.get("id") or 0)
                cur.execute(
                    """
                    UPDATE directory_providers
                       SET name=?,
                           provider_type=?,
                           enabled=?,
                           priority=?,
                           domain_suffix=?,
                           server_urls_json=?,
                           host_overrides_json=?,
                           use_ldaps=?,
                           tls_required=?,
                           tls_ca_pem=?,
                           base_dn=?,
                           bind_dn=?,
                           bind_password_encrypted=?,
                           user_search_filter=?,
                           username_attribute=?,
                           display_name_attribute=?,
                           email_attribute=?,
                           member_of_attribute=?,
                           group_search_base_dn=?,
                           nested_groups=?,
                           kerberos_realm=?,
                           kerberos_kdc=?,
                           kerberos_keytab_encrypted=?,
                           sync_interval_seconds=?,
                           updated_at=?
                     WHERE id=?
                    """,
                    (
                        row_values["name"],
                        row_values["provider_type"],
                        row_values["enabled"],
                        row_values["priority"],
                        row_values["domain_suffix"],
                        row_values["server_urls_json"],
                        row_values["host_overrides_json"],
                        row_values["use_ldaps"],
                        row_values["tls_required"],
                        row_values["tls_ca_pem"],
                        row_values["base_dn"],
                        row_values["bind_dn"],
                        row_values["bind_password_encrypted"],
                        row_values["user_search_filter"],
                        row_values["username_attribute"],
                        row_values["display_name_attribute"],
                        row_values["email_attribute"],
                        row_values["member_of_attribute"],
                        row_values["group_search_base_dn"],
                        row_values["nested_groups"],
                        row_values["kerberos_realm"],
                        row_values["kerberos_kdc"],
                        row_values["kerberos_keytab_encrypted"],
                        row_values["sync_interval_seconds"],
                        row_values["updated_at"],
                        provider_id,
                    ),
                )
            else:
                provider_id = 0
                cur.execute(
                    """
                    INSERT INTO directory_providers(
                        name,
                        provider_type,
                        enabled,
                        priority,
                        domain_suffix,
                        server_urls_json,
                        host_overrides_json,
                        use_ldaps,
                        tls_required,
                        tls_ca_pem,
                        base_dn,
                        bind_dn,
                        bind_password_encrypted,
                        user_search_filter,
                        username_attribute,
                        display_name_attribute,
                        email_attribute,
                        member_of_attribute,
                        group_search_base_dn,
                        nested_groups,
                        kerberos_realm,
                        kerberos_kdc,
                        kerberos_keytab_encrypted,
                        sync_interval_seconds,
                        created_at,
                        updated_at
                    ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
                    """,
                    (
                        row_values["name"],
                        row_values["provider_type"],
                        row_values["enabled"],
                        row_values["priority"],
                        row_values["domain_suffix"],
                        row_values["server_urls_json"],
                        row_values["host_overrides_json"],
                        row_values["use_ldaps"],
                        row_values["tls_required"],
                        row_values["tls_ca_pem"],
                        row_values["base_dn"],
                        row_values["bind_dn"],
                        row_values["bind_password_encrypted"],
                        row_values["user_search_filter"],
                        row_values["username_attribute"],
                        row_values["display_name_attribute"],
                        row_values["email_attribute"],
                        row_values["member_of_attribute"],
                        row_values["group_search_base_dn"],
                        row_values["nested_groups"],
                        row_values["kerberos_realm"],
                        row_values["kerberos_kdc"],
                        row_values["kerberos_keytab_encrypted"],
                        row_values["sync_interval_seconds"],
                        now_ts,
                        row_values["updated_at"],
                    ),
                )
                provider_id = int(cur.lastrowid or 0)
            if not provider_id:
                cur.execute("SELECT id FROM directory_providers WHERE LOWER(name)=LOWER(?)", (name,))
                row = cur.fetchone()
                provider_id = int(row[0] or 0)
            if "group_mappings" in payload:
                cur.execute("DELETE FROM directory_provider_group_mappings WHERE provider_id=?", (provider_id,))
                for item in payload.get("group_mappings") or []:
                    group_dn = _clean_text((item or {}).get("group_dn"))
                    if not group_dn:
                        continue
                    cur.execute(
                        """
                        INSERT INTO directory_provider_group_mappings(provider_id, group_dn, role, created_at, updated_at)
                        VALUES(?,?,?,?,?)
                        """,
                        (provider_id, group_dn, _canonical_role((item or {}).get("role")), now_ts, now_ts),
                    )
            conn.commit()
        except Exception:
            conn.rollback()
            raise
        finally:
            conn.close()
        saved = self.manager.load_provider(provider_id)
        if not saved:
            raise DirectoryAuthError("provider_not_found", "Provider was not found after save.", 404)
        return saved


def register_directory_services(app: Flask, adapters: "EngineServiceAdapters") -> None:
    """Register directory service endpoints."""

    service = DirectoryManagementService(app, adapters)
    blueprint = Blueprint("access_mgmt_directory_services", __name__)

    @blueprint.route("/api/directory/providers", methods=["GET"])
    def _list_providers():
        return service.list_providers()

    @blueprint.route("/api/directory/providers", methods=["POST"])
    def _create_provider():
        return service.save_provider()

    @blueprint.route("/api/directory/providers/certificate", methods=["POST"])
    def _fetch_certificate():
        return service.fetch_certificate()

    @blueprint.route("/api/directory/providers/<int:provider_id>", methods=["PATCH"])
    def _update_provider(provider_id: int):
        return service.save_provider(provider_id)

    @blueprint.route("/api/directory/providers/<int:provider_id>", methods=["DELETE"])
    def _delete_provider(provider_id: int):
        return service.delete_provider(provider_id)

    @blueprint.route("/api/directory/providers/<int:provider_id>/test", methods=["POST"])
    def _test_provider(provider_id: int):
        return service.test_provider(provider_id)

    @blueprint.route("/api/directory/providers/<int:provider_id>/sync", methods=["POST"])
    def _sync_provider(provider_id: int):
        return service.sync_provider(provider_id)

    @blueprint.route("/api/users/<username>/directory-cache", methods=["POST"])
    def _set_user_cache(username: str):
        return service.set_user_cache(username)

    app.register_blueprint(blueprint)


__all__ = [
    "DIRECTORY_AUTH_SOURCE",
    "DIRECTORY_PASSWORD_PLACEHOLDER",
    "DirectoryAuthenticationManager",
    "DirectoryAuthError",
    "DirectoryLoginResult",
    "register_directory_services",
]
