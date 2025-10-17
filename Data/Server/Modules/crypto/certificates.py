"""
Self-signed certificate management for Borealis.

The production Flask server and the Vite dev server both consume the same
certificate chain so agents and browsers can pin a single trust anchor during
enrollment.
"""

from __future__ import annotations

import os
import ssl
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Tuple

from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import NameOID

_CERT_DIR = Path(__file__).resolve().parent.parent / "certs"
_CERT_FILE = _CERT_DIR / "borealis-server-cert.pem"
_KEY_FILE = _CERT_DIR / "borealis-server-key.pem"
_BUNDLE_FILE = _CERT_DIR / "borealis-server-bundle.pem"

# 100-year lifetime (effectively "never" for self-signed deployments).
_CERT_VALIDITY = timedelta(days=365 * 100)


def ensure_certificate(common_name: str = "Borealis Server") -> Tuple[Path, Path, Path]:
    """
    Ensure the self-signed certificate and key exist on disk.

    Returns (cert_path, key_path, bundle_path).
    """

    _CERT_DIR.mkdir(parents=True, exist_ok=True)

    regenerate = not (_CERT_FILE.exists() and _KEY_FILE.exists())
    if not regenerate:
        try:
            with _CERT_FILE.open("rb") as fh:
                cert = x509.load_pem_x509_certificate(fh.read())
            if cert.not_valid_after.replace(tzinfo=timezone.utc) <= datetime.now(tz=timezone.utc):
                regenerate = True
        except Exception:
            regenerate = True

    if regenerate:
        _generate_certificate(common_name)

    if not _BUNDLE_FILE.exists():
        _BUNDLE_FILE.write_bytes(_CERT_FILE.read_bytes())

    return _CERT_FILE, _KEY_FILE, _BUNDLE_FILE


def _generate_certificate(common_name: str) -> None:
    private_key = ec.generate_private_key(ec.SECP384R1())
    public_key = private_key.public_key()

    now = datetime.now(tz=timezone.utc)
    builder = (
        x509.CertificateBuilder()
        .subject_name(
            x509.Name(
                [
                    x509.NameAttribute(NameOID.COMMON_NAME, common_name),
                    x509.NameAttribute(NameOID.ORGANIZATION_NAME, "Borealis"),
                ]
            )
        )
        .issuer_name(
            x509.Name(
                [
                    x509.NameAttribute(NameOID.COMMON_NAME, common_name),
                ]
            )
        )
        .public_key(public_key)
        .serial_number(x509.random_serial_number())
        .not_valid_before(now - timedelta(minutes=5))
        .not_valid_after(now + _CERT_VALIDITY)
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
        .add_extension(x509.BasicConstraints(ca=True, path_length=None), critical=True)
    )

    certificate = builder.sign(private_key=private_key, algorithm=hashes.SHA384())

    _KEY_FILE.write_bytes(
        private_key.private_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PrivateFormat.TraditionalOpenSSL,
            encryption_algorithm=serialization.NoEncryption(),
        )
    )
    _CERT_FILE.write_bytes(certificate.public_bytes(serialization.Encoding.PEM))

    # Propagate filesystem permissions to restrict accidental disclosure.
    _tighten_permissions(_KEY_FILE)
    _tighten_permissions(_CERT_FILE)


def _tighten_permissions(path: Path) -> None:
    try:
        if os.name == "posix":
            path.chmod(0o600)
    except Exception:
        pass


def build_ssl_context() -> ssl.SSLContext:
    cert_path, key_path, _bundle_path = ensure_certificate()
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.minimum_version = ssl.TLSVersion.TLSv1_3
    context.load_cert_chain(certfile=str(cert_path), keyfile=str(key_path))
    return context


def certificate_paths() -> Tuple[str, str, str]:
    cert_path, key_path, bundle_path = ensure_certificate()
    return str(cert_path), str(key_path), str(bundle_path)

