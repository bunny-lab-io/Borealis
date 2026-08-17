# ======================================================
# Data\Engine\security\session_secret.py
# Description: Generates and loads the persistent Engine session-signing secret used for operator auth cookies.
#
# API Endpoints (if applicable): None
# ======================================================

"""Persistent Engine session secret helpers."""

from __future__ import annotations

import os
import secrets
import stat
from pathlib import Path
from typing import Optional

_MIN_SECRET_LENGTH = 32


def _tighten_permissions(path: Path) -> None:
    """Apply best-effort restrictive permissions for the secret file."""

    try:
        if os.name == "nt":
            path.chmod(stat.S_IREAD | stat.S_IWRITE)
        else:
            path.chmod(0o600)
    except Exception:
        # Permission hardening is best effort; startup should continue.
        pass


def _read_secret(secret_path: Path) -> Optional[str]:
    if not secret_path.is_file():
        return None

    try:
        secret_value = secret_path.read_text(encoding="utf-8").strip()
    except Exception as exc:
        raise RuntimeError(f"Unable to read Engine secret file: {secret_path}") from exc

    if not secret_value:
        raise RuntimeError(f"Engine secret file is empty: {secret_path}")
    if len(secret_value) < _MIN_SECRET_LENGTH:
        raise RuntimeError(
            f"Engine secret file must be at least {_MIN_SECRET_LENGTH} characters: {secret_path}"
        )
    return secret_value


def _write_secret_exclusive(secret_path: Path, secret_value: str) -> None:
    secret_path.parent.mkdir(parents=True, exist_ok=True)
    try:
        with secret_path.open("x", encoding="utf-8") as handle:
            handle.write(secret_value)
            handle.write("\n")
    except FileExistsError:
        return
    except Exception:
        try:
            secret_path.unlink(missing_ok=True)
        except Exception:
            pass
        raise

    _tighten_permissions(secret_path)


def load_or_create_engine_secret(secret_path: Path) -> str:
    """Return the existing Engine secret or create one if missing."""

    path = Path(secret_path).expanduser().resolve()

    existing = _read_secret(path)
    if existing:
        _tighten_permissions(path)
        return existing

    generated = secrets.token_urlsafe(64)
    _write_secret_exclusive(path, generated)

    loaded = _read_secret(path)
    if loaded:
        _tighten_permissions(path)
        return loaded

    raise RuntimeError(f"Failed to initialize Engine secret file: {path}")


__all__ = ["load_or_create_engine_secret"]
