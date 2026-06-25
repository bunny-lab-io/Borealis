# ======================================================
# Data\Engine\services\auth\passkeys.py
# Description: Passkey storage helpers retained for Aegis protected-secret migration.
#
# API Endpoints (if applicable): None
# ======================================================

"""Passkey storage helpers shared by retained Python secret-migration code."""

from __future__ import annotations

import ast
import base64
import hashlib
import hmac
import json
from typing import Any, Optional


def _bytes_to_base64url(value: bytes) -> str:
    encoded = base64.urlsafe_b64encode(value or b"").decode("ascii")
    return encoded.rstrip("=")


def _bytes_literal_to_bytes(value: Any) -> Optional[bytes]:
    if value is None:
        return None
    text = str(value or "").strip()
    if not text or not text.startswith("b"):
        return None
    try:
        parsed = ast.literal_eval(text)
    except Exception:
        return None
    if isinstance(parsed, bytearray):
        return bytes(parsed)
    if isinstance(parsed, bytes):
        return parsed
    return None


def normalize_webauthn_storage_value(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, memoryview):
        value = value.tobytes()
    if isinstance(value, bytearray):
        value = bytes(value)
    if isinstance(value, bytes):
        return _bytes_to_base64url(value)

    text = str(value or "").strip()
    if not text:
        return ""

    literal_bytes = _bytes_literal_to_bytes(text)
    if literal_bytes is not None:
        return _bytes_to_base64url(literal_bytes)
    return text


def build_passkey_lookup_hmac(app_secret: str, credential_id: Any) -> str:
    normalized = normalize_webauthn_storage_value(credential_id)
    if not normalized:
        return ""
    return hmac.new(
        str(app_secret or "").encode("utf-8"),
        normalized.encode("utf-8"),
        hashlib.sha256,
    ).hexdigest()


def serialize_passkey_secret_bundle(
    *,
    credential_id: Any,
    public_key: Any,
    sign_count: Any,
    aaguid: Any,
) -> str:
    payload = {
        "credential_id": normalize_webauthn_storage_value(credential_id),
        "public_key": normalize_webauthn_storage_value(public_key),
        "sign_count": int(sign_count or 0),
        "aaguid": str(aaguid or "").strip(),
    }
    return json.dumps(payload, sort_keys=True)


def deserialize_passkey_secret_bundle(value: Any) -> dict[str, Any]:
    if isinstance(value, dict):
        payload = dict(value)
    else:
        try:
            payload = json.loads(str(value or "{}"))
        except Exception:
            payload = {}
    return {
        "credential_id": normalize_webauthn_storage_value(payload.get("credential_id")),
        "public_key": normalize_webauthn_storage_value(payload.get("public_key")),
        "sign_count": int(payload.get("sign_count") or 0),
        "aaguid": str(payload.get("aaguid") or "").strip(),
    }


__all__ = [
    "build_passkey_lookup_hmac",
    "deserialize_passkey_secret_bundle",
    "normalize_webauthn_storage_value",
    "serialize_passkey_secret_bundle",
]
