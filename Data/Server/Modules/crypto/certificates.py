"""
Server TLS certificate management.

Borealis now issues a dedicated root CA and a leaf server certificate so that
agents can pin the CA without requiring a re-enrollment every time the server
certificate is refreshed. The CA is persisted alongside the server key so that
existing deployments can be upgraded in-place.
"""

from __future__ import annotations

import os
import ssl
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Optional, Tuple

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import ExtendedKeyUsageOID, NameOID

from Modules.runtime import ensure_server_certificates_dir, runtime_path, server_certificates_path

_CERT_DIR = server_certificates_path()
_CERT_FILE = _CERT_DIR / "borealis-server-cert.pem"
_KEY_FILE = _CERT_DIR / "borealis-server-key.pem"
_BUNDLE_FILE = _CERT_DIR / "borealis-server-bundle.pem"
_CA_KEY_FILE = _CERT_DIR / "borealis-root-ca-key.pem"
_CA_CERT_FILE = _CERT_DIR / "borealis-root-ca.pem"

_LEGACY_CERT_DIR = runtime_path("certs")
_LEGACY_CERT_FILE = _LEGACY_CERT_DIR / "borealis-server-cert.pem"
_LEGACY_KEY_FILE = _LEGACY_CERT_DIR / "borealis-server-key.pem"
_LEGACY_BUNDLE_FILE = _LEGACY_CERT_DIR / "borealis-server-bundle.pem"

_ROOT_COMMON_NAME = "Borealis Root CA"
_ORG_NAME = "Borealis"
_ROOT_VALIDITY = timedelta(days=365 * 100)
_SERVER_VALIDITY = timedelta(days=365 * 5)


def ensure_certificate(common_name: str = "Borealis Server") -> Tuple[Path, Path, Path]:
    """
    Ensure the root CA, server certificate, and bundle exist on disk.

    Returns (cert_path, key_path, bundle_path).
    """

    ensure_server_certificates_dir()
    _migrate_legacy_material_if_present()

    ca_key, ca_cert, ca_regenerated = _ensure_root_ca()

    server_cert = _load_certificate(_CERT_FILE)
    needs_regen = ca_regenerated or _server_certificate_needs_regeneration(server_cert, ca_cert)
    if needs_regen:
        server_cert = _generate_server_certificate(common_name, ca_key, ca_cert)

    if server_cert is None:
        server_cert = _generate_server_certificate(common_name, ca_key, ca_cert)

    _write_bundle(server_cert, ca_cert)

    return _CERT_FILE, _KEY_FILE, _BUNDLE_FILE


def _migrate_legacy_material_if_present() -> None:
    # Promote legacy runtime certificates (Server/Borealis/certs) into the new location.
    if not _CERT_FILE.exists() or not _KEY_FILE.exists():
        legacy_cert = _LEGACY_CERT_FILE
        legacy_key = _LEGACY_KEY_FILE
        if legacy_cert.exists() and legacy_key.exists():
            try:
                ensure_server_certificates_dir()
                if not _CERT_FILE.exists():
                    _safe_copy(legacy_cert, _CERT_FILE)
                if not _KEY_FILE.exists():
                    _safe_copy(legacy_key, _KEY_FILE)
            except Exception:
                pass


def _ensure_root_ca() -> Tuple[ec.EllipticCurvePrivateKey, x509.Certificate, bool]:
    regenerated = False

    ca_key: Optional[ec.EllipticCurvePrivateKey] = None
    ca_cert: Optional[x509.Certificate] = None

    if _CA_KEY_FILE.exists() and _CA_CERT_FILE.exists():
        try:
            ca_key = _load_private_key(_CA_KEY_FILE)
            ca_cert = _load_certificate(_CA_CERT_FILE)
            if ca_cert is not None and ca_key is not None:
                expiry = _cert_not_after(ca_cert)
                subject = ca_cert.subject
                subject_cn = ""
                try:
                    subject_cn = subject.get_attributes_for_oid(NameOID.COMMON_NAME)[0].value  # type: ignore[index]
                except Exception:
                    subject_cn = ""
                try:
                    basic = ca_cert.extensions.get_extension_for_class(x509.BasicConstraints).value  # type: ignore[attr-defined]
                    is_ca = bool(basic.ca)
                except Exception:
                    is_ca = False
                if (
                    expiry <= datetime.now(tz=timezone.utc)
                    or not is_ca
                    or subject_cn != _ROOT_COMMON_NAME
                ):
                    regenerated = True
            else:
                regenerated = True
        except Exception:
            regenerated = True
    else:
        regenerated = True

    if regenerated or ca_key is None or ca_cert is None:
        ca_key = ec.generate_private_key(ec.SECP384R1())
        public_key = ca_key.public_key()

        now = datetime.now(tz=timezone.utc)
        builder = (
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

        builder = builder.add_extension(
            x509.AuthorityKeyIdentifier.from_issuer_public_key(public_key),
            critical=False,
        )

        ca_cert = builder.sign(private_key=ca_key, algorithm=hashes.SHA384())

        _CA_KEY_FILE.write_bytes(
            ca_key.private_bytes(
                encoding=serialization.Encoding.PEM,
                format=serialization.PrivateFormat.TraditionalOpenSSL,
                encryption_algorithm=serialization.NoEncryption(),
            )
        )
        _CA_CERT_FILE.write_bytes(ca_cert.public_bytes(serialization.Encoding.PEM))

        _tighten_permissions(_CA_KEY_FILE)
        _tighten_permissions(_CA_CERT_FILE)
    else:
        regenerated = False

    return ca_key, ca_cert, regenerated


def _server_certificate_needs_regeneration(
    server_cert: Optional[x509.Certificate],
    ca_cert: x509.Certificate,
) -> bool:
    if server_cert is None:
        return True

    try:
        if server_cert.issuer != ca_cert.subject:
            return True
    except Exception:
        return True

    try:
        expiry = _cert_not_after(server_cert)
        if expiry <= datetime.now(tz=timezone.utc):
            return True
    except Exception:
        return True

    try:
        basic = server_cert.extensions.get_extension_for_class(x509.BasicConstraints).value  # type: ignore[attr-defined]
        if basic.ca:
            return True
    except Exception:
        return True

    try:
        eku = server_cert.extensions.get_extension_for_class(x509.ExtendedKeyUsage).value  # type: ignore[attr-defined]
        if ExtendedKeyUsageOID.SERVER_AUTH not in eku:
            return True
    except Exception:
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
    ca_expiry = _cert_not_after(ca_cert)
    candidate_expiry = now + _SERVER_VALIDITY
    not_after = min(ca_expiry - timedelta(days=1), candidate_expiry)

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


def _safe_copy(src: Path, dst: Path) -> None:
    try:
        dst.write_bytes(src.read_bytes())
    except Exception:
        pass


def _tighten_permissions(path: Path) -> None:
    try:
        if os.name == "posix":
            path.chmod(0o600)
    except Exception:
        pass


def _load_private_key(path: Path) -> ec.EllipticCurvePrivateKey:
    with path.open("rb") as fh:
        return serialization.load_pem_private_key(fh.read(), password=None)


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


def build_ssl_context() -> ssl.SSLContext:
    cert_path, key_path, bundle_path = ensure_certificate()
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.minimum_version = ssl.TLSVersion.TLSv1_3
    context.load_cert_chain(certfile=str(bundle_path), keyfile=str(key_path))
    return context


def certificate_paths() -> Tuple[str, str, str]:
    cert_path, key_path, bundle_path = ensure_certificate()
    return str(cert_path), str(key_path), str(bundle_path)
