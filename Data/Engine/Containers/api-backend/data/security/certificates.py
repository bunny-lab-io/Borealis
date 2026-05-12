# ======================================================
# Data\Engine\security\certificates.py
# Description: Generates and maintains Engine TLS certificate material under the api-backend service secret root without legacy fallback.
#
# API Endpoints (if applicable): None
# ======================================================

"""Engine-managed TLS certificate helpers.

The Engine runtime keeps writable artefacts under ``Engine/Services`` so they
can be regenerated when the staging tree under ``Data/`` is refreshed.  This
module provisions the server's TLS certificates within the api-backend service
secret root and ensures a sibling ``Code-Signing`` directory exists so
code-signing keys can live alongside the TLS bundle.  No legacy migration or
fallback is performed; the Engine manages its own material exclusively.
"""

from __future__ import annotations

import os
from datetime import datetime, timedelta, timezone
from functools import lru_cache
from pathlib import Path
from typing import Optional, Tuple

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import ExtendedKeyUsageOID, NameOID

__all__ = [
    "certificate_paths",
    "ensure_certificate",
    "engine_certificates_root",
    "engine_code_signing_root",
    "project_root_path",
]

_ROOT_COMMON_NAME = "Borealis Root CA"
_ORG_NAME = "Borealis"
_ROOT_VALIDITY = timedelta(days=365 * 100)
_SERVER_VALIDITY = timedelta(days=365 * 5)


def _env_path(name: str) -> Optional[Path]:
    value = os.environ.get(name)
    if not value:
        return None
    try:
        return Path(value).expanduser().resolve()
    except Exception:
        try:
            return Path(value).expanduser()
        except Exception:
            return Path(value)


@lru_cache(maxsize=None)
def _project_root() -> Path:
    env = _env_path("BOREALIS_PROJECT_ROOT")
    if env and env.is_dir():
        return env

    current = Path(__file__).resolve()
    for parent in (current, *current.parents):
        if (parent / "Agent.exe").is_file():
            return parent

    try:
        return current.parents[3]
    except IndexError:
        return current.parent


def project_root_path() -> Path:
    """Expose the detected project root."""

    return _project_root()


@lru_cache(maxsize=None)
def _engine_runtime_root() -> Path:
    env = _env_path("BOREALIS_ENGINE_ROOT") or _env_path("BOREALIS_ENGINE_RUNTIME")
    if env:
        env.mkdir(parents=True, exist_ok=True)
        return env

    root = _project_root() / "Engine"
    root.mkdir(parents=True, exist_ok=True)
    return root


@lru_cache(maxsize=None)
def engine_certificates_root() -> Path:
    """Base directory for Engine TLS and code-signing certificates."""

    env = _env_path("BOREALIS_ENGINE_CERT_ROOT")
    if env:
        env.mkdir(parents=True, exist_ok=True)
        return env

    root = _engine_runtime_root() / "Services" / "api-backend" / "secrets" / "Certificates"
    root.mkdir(parents=True, exist_ok=True)
    return root


@lru_cache(maxsize=None)
def engine_code_signing_root() -> Path:
    """Location under which Engine code-signing keys are stored."""

    root = engine_certificates_root() / "Code-Signing"
    root.mkdir(parents=True, exist_ok=True)
    return root


_CERT_DIR = engine_certificates_root()
_CERT_FILE = _CERT_DIR / "borealis-server-cert.pem"
_KEY_FILE = _CERT_DIR / "borealis-server-key.pem"
_BUNDLE_FILE = _CERT_DIR / "borealis-server-bundle.pem"
_CA_KEY_FILE = _CERT_DIR / "borealis-root-ca-key.pem"
_CA_CERT_FILE = _CERT_DIR / "borealis-root-ca.pem"


def _tighten_permissions(path: Path) -> None:
    try:
        if os.name != "nt":
            path.chmod(0o600)
    except Exception:
        pass


def _load_private_key(path: Path) -> Optional[ec.EllipticCurvePrivateKey]:
    try:
        return serialization.load_pem_private_key(path.read_bytes(), password=None)
    except Exception:
        return None


def _load_certificate(path: Path) -> Optional[x509.Certificate]:
    try:
        return x509.load_pem_x509_certificate(path.read_bytes())
    except Exception:
        return None


def _cert_not_after(cert: x509.Certificate) -> datetime:
    try:
        return cert.not_valid_after_utc  # type: ignore[attr-defined]
    except AttributeError:
        value = cert.not_valid_after
        if value.tzinfo is None:
            return value.replace(tzinfo=timezone.utc)
        return value


def _ensure_root_ca() -> Tuple[ec.EllipticCurvePrivateKey, x509.Certificate, bool]:
    regenerated = False
    ca_key = None
    ca_cert = None

    if _CA_KEY_FILE.exists() and _CA_CERT_FILE.exists():
        ca_key = _load_private_key(_CA_KEY_FILE)
        ca_cert = _load_certificate(_CA_CERT_FILE)
        if ca_key is None or ca_cert is None:
            regenerated = True
        else:
            expiry = _cert_not_after(ca_cert)
            try:
                subject = ca_cert.subject.get_attributes_for_oid(NameOID.COMMON_NAME)[0].value  # type: ignore[index]
            except Exception:
                subject = ""
            try:
                basic = ca_cert.extensions.get_extension_for_class(x509.BasicConstraints).value  # type: ignore[attr-defined]
                is_ca = bool(basic.ca)
            except Exception:
                is_ca = False
            if expiry <= datetime.now(tz=timezone.utc) or subject != _ROOT_COMMON_NAME or not is_ca:
                regenerated = True

    else:
        regenerated = True

    if regenerated or ca_key is None or ca_cert is None:
        ca_key = ec.generate_private_key(ec.SECP384R1())
        public_key = ca_key.public_key()
        now = datetime.now(tz=timezone.utc)
        cert_builder = (
            x509.CertificateBuilder()
            .subject_name(
                x509.Name(
                    [
                        x509.NameAttribute(NameOID.COMMON_NAME, _ROOT_COMMON_NAME),
                        x509.NameAttribute(NameOID.ORGANIZATION_NAME, _ORG_NAME),
                    ]
                )
            )
            .issuer_name(
                x509.Name(
                    [
                        x509.NameAttribute(NameOID.COMMON_NAME, _ROOT_COMMON_NAME),
                        x509.NameAttribute(NameOID.ORGANIZATION_NAME, _ORG_NAME),
                    ]
                )
            )
            .public_key(public_key)
            .serial_number(x509.random_serial_number())
            .not_valid_before(now - timedelta(minutes=5))
            .not_valid_after(now + _ROOT_VALIDITY)
            .add_extension(x509.BasicConstraints(ca=True, path_length=None), critical=True)
            .add_extension(
                x509.KeyUsage(
                    digital_signature=True,
                    content_commitment=False,
                    key_encipherment=False,
                    data_encipherment=False,
                    key_agreement=False,
                    key_cert_sign=True,
                    crl_sign=True,
                    encipher_only=False,
                    decipher_only=False,
                ),
                critical=True,
            )
            .add_extension(
                x509.SubjectKeyIdentifier.from_public_key(public_key),
                critical=False,
            )
        )
        ca_cert = cert_builder.sign(private_key=ca_key, algorithm=hashes.SHA384())

        _CA_KEY_FILE.write_bytes(
            ca_key.private_bytes(
                encoding=serialization.Encoding.PEM,
                format=serialization.PrivateFormat.PKCS8,
                encryption_algorithm=serialization.NoEncryption(),
            )
        )
        _CA_CERT_FILE.write_bytes(ca_cert.public_bytes(serialization.Encoding.PEM))
        _tighten_permissions(_CA_KEY_FILE)
        _tighten_permissions(_CA_CERT_FILE)

    return ca_key, ca_cert, regenerated


def _server_certificate_needs_regeneration(
    certificate: Optional[x509.Certificate],
    issuer: x509.Certificate,
) -> bool:
    if certificate is None:
        return True

    try:
        subject_cn = certificate.subject.get_attributes_for_oid(NameOID.COMMON_NAME)[0].value  # type: ignore[index]
    except Exception:
        subject_cn = ""

    if subject_cn != "Borealis Server":
        return True

    try:
        eku = certificate.extensions.get_extension_for_class(x509.ExtendedKeyUsage).value  # type: ignore[attr-defined]
        if ExtendedKeyUsageOID.SERVER_AUTH not in eku:
            return True
    except Exception:
        return True

    try:
        san = certificate.extensions.get_extension_for_class(x509.SubjectAlternativeName).value  # type: ignore[attr-defined]
        raw_names = san.get_values_for_type(x509.DNSName)
        names = {
            entry if isinstance(entry, str) else getattr(entry, "value", str(entry))
            for entry in raw_names
        }
    except Exception:
        names = set()

    if {"localhost", "127.0.0.1", "::1"} - names:
        return True

    try:
        aki = certificate.extensions.get_extension_for_class(x509.AuthorityKeyIdentifier).value  # type: ignore[attr-defined]
        ski = issuer.extensions.get_extension_for_class(x509.SubjectKeyIdentifier).value  # type: ignore[attr-defined]
        if aki.key_identifier != ski.key_identifier:
            return True
    except Exception:
        return True

    expiry = _cert_not_after(certificate)
    if expiry <= datetime.now(tz=timezone.utc):
        return True

    return False


def _generate_server_certificate(
    common_name: str,
    ca_key: ec.EllipticCurvePrivateKey,
    ca_cert: x509.Certificate,
) -> x509.Certificate:
    private_key = ec.generate_private_key(ec.SECP384R1())
    public_key = private_key.public_key()
    now = datetime.now(tz=timezone.utc)
    not_after = now + _SERVER_VALIDITY

    builder = (
        x509.CertificateBuilder()
        .subject_name(
            x509.Name(
                [
                    x509.NameAttribute(NameOID.COMMON_NAME, common_name),
                    x509.NameAttribute(NameOID.ORGANIZATION_NAME, _ORG_NAME),
                ]
            )
        )
        .issuer_name(ca_cert.subject)
        .public_key(public_key)
        .serial_number(x509.random_serial_number())
        .not_valid_before(now - timedelta(minutes=5))
        .not_valid_after(not_after)
        .add_extension(
            x509.SubjectAlternativeName(
                [
                    x509.DNSName("localhost"),
                    x509.DNSName("127.0.0.1"),
                    x509.DNSName("::1"),
                ]
            ),
            critical=False,
        )
        .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
        .add_extension(
            x509.KeyUsage(
                digital_signature=True,
                content_commitment=False,
                key_encipherment=False,
                data_encipherment=False,
                key_agreement=False,
                key_cert_sign=False,
                crl_sign=False,
                encipher_only=False,
                decipher_only=False,
            ),
            critical=True,
        )
        .add_extension(
            x509.ExtendedKeyUsage([ExtendedKeyUsageOID.SERVER_AUTH]),
            critical=False,
        )
        .add_extension(
            x509.SubjectKeyIdentifier.from_public_key(public_key),
            critical=False,
        )
        .add_extension(
            x509.AuthorityKeyIdentifier.from_issuer_public_key(ca_key.public_key()),
            critical=False,
        )
    )

    certificate = builder.sign(private_key=ca_key, algorithm=hashes.SHA384())

    _KEY_FILE.write_bytes(
        private_key.private_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PrivateFormat.TraditionalOpenSSL,
            encryption_algorithm=serialization.NoEncryption(),
        )
    )
    _CERT_FILE.write_bytes(certificate.public_bytes(serialization.Encoding.PEM))
    _tighten_permissions(_KEY_FILE)
    _tighten_permissions(_CERT_FILE)

    return certificate


def _write_bundle(server_cert: x509.Certificate, ca_cert: x509.Certificate) -> None:
    try:
        server_pem = server_cert.public_bytes(serialization.Encoding.PEM).decode("utf-8").strip()
        ca_pem = ca_cert.public_bytes(serialization.Encoding.PEM).decode("utf-8").strip()
    except Exception:
        return

    bundle = f"{server_pem}\n{ca_pem}\n"
    _BUNDLE_FILE.write_text(bundle, encoding="utf-8")
    _tighten_permissions(_BUNDLE_FILE)


def ensure_certificate(common_name: str = "Borealis Server") -> Tuple[Path, Path, Path]:
    """
    Ensure the Engine TLS certificate bundle exists.

    Returns (cert_path, key_path, bundle_path).
    """

    engine_certificates_root()
    engine_code_signing_root()

    ca_key, ca_cert, ca_regenerated = _ensure_root_ca()
    existing_cert = _load_certificate(_CERT_FILE)
    if _server_certificate_needs_regeneration(existing_cert, ca_cert) or ca_regenerated:
        existing_cert = _generate_server_certificate(common_name, ca_key, ca_cert)

    if existing_cert is None:
        existing_cert = _generate_server_certificate(common_name, ca_key, ca_cert)

    _write_bundle(existing_cert, ca_cert)

    return _CERT_FILE, _KEY_FILE, _BUNDLE_FILE


def certificate_paths() -> Tuple[str, str, str]:
    cert_path, key_path, bundle_path = ensure_certificate()
    return str(cert_path), str(key_path), str(bundle_path)
