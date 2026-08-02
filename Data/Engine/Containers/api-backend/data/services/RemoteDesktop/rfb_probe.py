# ======================================================
# Data\Engine\services\RemoteDesktop\rfb_probe.py
# Description: Lightweight RFB/VNC authentication probe helpers.
#
# API Endpoints (if applicable): None
# ======================================================

"""RFB/VNC authentication probes used before Guacamole session bootstrap."""
from __future__ import annotations

import os
import socket
import struct
import time
from typing import Any, NamedTuple, Optional


RFB_SECURITY_TYPE_NONE = 1
RFB_SECURITY_TYPE_VNC_AUTH = 2
RFB_VNC_AUTH_CHALLENGE_BYTES = 16
RFB_VNC_AUTH_RESPONSE_BYTES = 16


class VncAuthProbeResult(NamedTuple):
    checked: bool
    ok: bool
    reason: str
    stage: str = ""
    server_version: str = ""
    offered_security_types: tuple[int, ...] = ()
    selected_security_type: int = 0
    auth_result: Optional[int] = None
    framebuffer_width: int = 0
    framebuffer_height: int = 0
    desktop_name_length: int = 0
    elapsed_ms: int = 0
    socket_error: str = ""


def vnc_auth_probe_payload(result: VncAuthProbeResult) -> dict[str, Any]:
    return {
        "checked": bool(result.checked),
        "ok": bool(result.ok),
        "reason": result.reason,
        "stage": result.stage,
        "server_version": result.server_version,
        "offered_security_types": list(result.offered_security_types),
        "selected_security_type": result.selected_security_type,
        "auth_result": result.auth_result,
        "framebuffer_width": result.framebuffer_width,
        "framebuffer_height": result.framebuffer_height,
        "desktop_name_length": result.desktop_name_length,
        "elapsed_ms": result.elapsed_ms,
        "socket_error": result.socket_error,
    }


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _normalize_bool(value: Any, default: bool = False) -> bool:
    if isinstance(value, bool):
        return value
    if isinstance(value, (int, float)):
        return bool(value)
    if isinstance(value, str):
        cleaned = value.strip().lower()
        if cleaned in {"1", "true", "yes", "y", "on"}:
            return True
        if cleaned in {"0", "false", "no", "n", "off"}:
            return False
    return default


def _read_socket_exact(sock: socket.socket, byte_count: int) -> bytes:
    chunks: list[bytes] = []
    remaining = int(byte_count)
    while remaining > 0:
        chunk = sock.recv(remaining)
        if not chunk:
            raise RuntimeError("socket_closed")
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def _read_rfb_failure_reason(sock: socket.socket) -> str:
    try:
        raw_length = _read_socket_exact(sock, 4)
        length = struct.unpack(">I", raw_length)[0]
        if length <= 0 or length > 4096:
            return ""
        return _read_socket_exact(sock, length).decode("utf-8", errors="replace").strip()
    except Exception:
        return ""


def _normalize_rfb_rejection_reason(detail: str, fallback: str) -> str:
    text = _normalize_text(detail)
    lowered = text.lower()
    if "too many" in lowered or "to many" in lowered or ("attempt" in lowered and "many" in lowered):
        return f"too_many_auth_failures:{text}" if text else "too_many_auth_failures"
    if "reject" in lowered:
        return f"auth_rejected:{text}" if text else "auth_rejected"
    return text or fallback


def _reverse_byte_bits(value: int) -> int:
    result = 0
    for _index in range(8):
        result = (result << 1) | (value & 1)
        value >>= 1
    return result


def _vnc_auth_challenge_response(password: str, challenge: bytes) -> Optional[bytes]:
    """Return RFB security type 2 VNCAuth response bytes.

    RFB VNCAuth requires DES/ECB over one server-issued 16-byte challenge.
    Keep this helper scoped to VNC compatibility diagnostics only; Borealis
    must not reuse it for stored secrets, operator auth, or transport crypto.
    """
    if len(challenge) != RFB_VNC_AUTH_CHALLENGE_BYTES:
        return None
    try:
        from cryptography.hazmat.primitives.ciphers import Cipher, modes
    except Exception:
        return None
    try:
        des_algorithm = None
        triple_des_algorithm = None
        try:
            from cryptography.hazmat.decrepit.ciphers import algorithms as decrepit_algorithms

            des_algorithm = getattr(decrepit_algorithms, "DES", None)
            triple_des_algorithm = getattr(decrepit_algorithms, "TripleDES", None)
        except Exception:
            pass
        if des_algorithm is None or triple_des_algorithm is None:
            from cryptography.hazmat.primitives.ciphers import algorithms

            if des_algorithm is None:
                des_algorithm = getattr(algorithms, "DES", None)
            if triple_des_algorithm is None:
                triple_des_algorithm = getattr(algorithms, "TripleDES", None)
        raw_key = str(password or "").encode("latin-1", errors="ignore")[:8].ljust(8, b"\x00")
        key = bytes(_reverse_byte_bits(byte) for byte in raw_key)
        if des_algorithm is not None:
            # Protocol-required DES. See _vnc_auth_challenge_response docstring.
            algorithm = des_algorithm(key)
        elif triple_des_algorithm is not None:
            algorithm = triple_des_algorithm(key * 3)
        else:
            return None
        encryptor = Cipher(algorithm, modes.ECB()).encryptor()
        return encryptor.update(challenge) + encryptor.finalize()
    except Exception:
        return None


def probe_vnc_security(host: str, port: int, timeout_seconds: float) -> VncAuthProbeResult:
    started_at = time.monotonic()

    def _result(
        checked: bool,
        ok: bool,
        reason: str,
        stage: str = "",
        *,
        server_version: str = "",
        offered_security_types: tuple[int, ...] = (),
        selected_security_type: int = 0,
        socket_error: str = "",
    ) -> VncAuthProbeResult:
        return VncAuthProbeResult(
            checked,
            ok,
            reason,
            stage,
            server_version,
            offered_security_types,
            selected_security_type,
            None,
            0,
            0,
            0,
            int((time.monotonic() - started_at) * 1000),
            socket_error,
        )

    host_value = _normalize_text(host)
    try:
        port_value = int(port)
    except Exception:
        return _result(True, False, "invalid_port", "input")
    if not host_value or port_value < 1 or port_value > 65535:
        return _result(True, False, "invalid_endpoint", "input")
    try:
        with socket.create_connection((host_value, port_value), timeout=max(0.25, timeout_seconds)) as sock:
            sock.settimeout(max(0.25, timeout_seconds))
            raw_server_version = _read_socket_exact(sock, 12)
            server_version = raw_server_version.decode("ascii", errors="replace").strip()
            if not raw_server_version.startswith(b"RFB "):
                return _result(True, False, "invalid_rfb_banner", "banner", server_version=server_version)
            try:
                major = int(raw_server_version[4:7])
                minor = int(raw_server_version[8:11])
            except Exception:
                major = 3
                minor = 8
            client_version = b"RFB 003.008\n" if major > 3 or minor >= 8 else raw_server_version
            sock.sendall(client_version)
            if major == 3 and minor <= 3:
                security_type = struct.unpack(">I", _read_socket_exact(sock, 4))[0]
                offered_security_types = (security_type,)
                if security_type == 0:
                    detail = _read_rfb_failure_reason(sock)
                    return _result(
                        True,
                        False,
                        _normalize_rfb_rejection_reason(detail, "security_type_rejected"),
                        "security",
                        server_version=server_version,
                        offered_security_types=offered_security_types,
                    )
                return _result(
                    True,
                    True,
                    f"security_type_available_{security_type}",
                    "security",
                    server_version=server_version,
                    offered_security_types=offered_security_types,
                    selected_security_type=security_type,
                )

            security_type_count = _read_socket_exact(sock, 1)[0]
            if security_type_count <= 0:
                detail = _read_rfb_failure_reason(sock)
                return _result(
                    True,
                    False,
                    _normalize_rfb_rejection_reason(detail, "security_type_rejected"),
                    "security",
                    server_version=server_version,
                )
            security_types = _read_socket_exact(sock, security_type_count)
            offered_security_types = tuple(int(item) for item in security_types)
            return _result(
                True,
                True,
                "security_types_available",
                "security",
                server_version=server_version,
                offered_security_types=offered_security_types,
            )
    except Exception as exc:
        return _result(False, True, "security_preflight_unavailable", "connect", socket_error=str(exc)[:160])


def _complete_rfb_client_init(
    sock: socket.socket,
    *,
    started_at: float,
    server_version: str,
    offered_security_types: tuple[int, ...],
    selected_security_type: int,
    auth_result: Optional[int],
) -> VncAuthProbeResult:
    def _result(
        ok: bool,
        reason: str,
        stage: str,
        *,
        framebuffer_width: int = 0,
        framebuffer_height: int = 0,
        desktop_name_length: int = 0,
        socket_error: str = "",
    ) -> VncAuthProbeResult:
        return VncAuthProbeResult(
            True,
            ok,
            reason,
            stage,
            server_version,
            offered_security_types,
            selected_security_type,
            auth_result,
            framebuffer_width,
            framebuffer_height,
            desktop_name_length,
            int((time.monotonic() - started_at) * 1000),
            socket_error,
        )

    try:
        sock.sendall(b"\x01")
        server_init = _read_socket_exact(sock, 24)
        width, height = struct.unpack(">HH", server_init[:4])
        if width <= 0 or height <= 0:
            return _result(False, "server_init_invalid_display", "server_init", framebuffer_width=width, framebuffer_height=height)
        name_length = struct.unpack(">I", server_init[20:24])[0]
        if name_length < 0 or name_length > 4096:
            return _result(False, "server_init_invalid_name", "server_init", framebuffer_width=width, framebuffer_height=height)
        if name_length:
            _read_socket_exact(sock, name_length)
        return _result(
            True,
            "server_init_ok",
            "server_init",
            framebuffer_width=width,
            framebuffer_height=height,
            desktop_name_length=name_length,
        )
    except Exception as exc:
        return _result(False, f"server_init_failed:{str(exc)[:120]}", "server_init", socket_error=str(exc)[:160])


def probe_vnc_auth(host: str, port: int, password: str, timeout_seconds: float) -> VncAuthProbeResult:
    started_at = time.monotonic()

    def _result(
        checked: bool,
        ok: bool,
        reason: str,
        stage: str = "",
        *,
        server_version: str = "",
        offered_security_types: tuple[int, ...] = (),
        selected_security_type: int = 0,
        auth_result: Optional[int] = None,
        framebuffer_width: int = 0,
        framebuffer_height: int = 0,
        desktop_name_length: int = 0,
        socket_error: str = "",
    ) -> VncAuthProbeResult:
        return VncAuthProbeResult(
            checked,
            ok,
            reason,
            stage,
            server_version,
            offered_security_types,
            selected_security_type,
            auth_result,
            framebuffer_width,
            framebuffer_height,
            desktop_name_length,
            int((time.monotonic() - started_at) * 1000),
            socket_error,
        )

    host_value = _normalize_text(host)
    try:
        port_value = int(port)
    except Exception:
        return _result(True, False, "invalid_port", "input")
    if not host_value or port_value < 1 or port_value > 65535:
        return _result(True, False, "invalid_endpoint", "input")
    if not _normalize_text(password):
        return _result(True, False, "missing_password", "input")
    try:
        with socket.create_connection((host_value, port_value), timeout=max(0.25, timeout_seconds)) as sock:
            sock.settimeout(max(0.25, timeout_seconds))
            raw_server_version = _read_socket_exact(sock, 12)
            server_version = raw_server_version.decode("ascii", errors="replace").strip()
            if not raw_server_version.startswith(b"RFB "):
                return _result(True, False, "invalid_rfb_banner", "banner", server_version=server_version)
            try:
                major = int(raw_server_version[4:7])
                minor = int(raw_server_version[8:11])
            except Exception:
                major = 3
                minor = 8
            client_version = b"RFB 003.008\n" if major > 3 or minor >= 8 else raw_server_version
            sock.sendall(client_version)

            security_type = 0
            offered_security_types: tuple[int, ...] = ()
            if major == 3 and minor <= 3:
                security_type = struct.unpack(">I", _read_socket_exact(sock, 4))[0]
                offered_security_types = (security_type,)
                if security_type == 0:
                    detail = _read_rfb_failure_reason(sock)
                    return _result(
                        True,
                        False,
                        detail or "security_type_rejected",
                        "security",
                        server_version=server_version,
                        offered_security_types=offered_security_types,
                    )
                if security_type == RFB_SECURITY_TYPE_NONE:
                    return _result(
                        True,
                        True,
                        "none_auth",
                        "security",
                        server_version=server_version,
                        offered_security_types=offered_security_types,
                        selected_security_type=1,
                    )
                if security_type != RFB_SECURITY_TYPE_VNC_AUTH:
                    return _result(
                        True,
                        False,
                        f"unsupported_security_type_{security_type}",
                        "security",
                        server_version=server_version,
                        offered_security_types=offered_security_types,
                    )
            else:
                security_type_count = _read_socket_exact(sock, 1)[0]
                if security_type_count <= 0:
                    detail = _read_rfb_failure_reason(sock)
                    return _result(
                        True,
                        False,
                        _normalize_rfb_rejection_reason(detail, "security_type_rejected"),
                        "security",
                        server_version=server_version,
                    )
                security_types = _read_socket_exact(sock, security_type_count)
                offered_security_types = tuple(int(item) for item in security_types)
                if RFB_SECURITY_TYPE_VNC_AUTH in security_types:
                    security_type = RFB_SECURITY_TYPE_VNC_AUTH
                    sock.sendall(bytes([RFB_SECURITY_TYPE_VNC_AUTH]))
                elif RFB_SECURITY_TYPE_NONE in security_types:
                    sock.sendall(bytes([RFB_SECURITY_TYPE_NONE]))
                    return _result(
                        True,
                        True,
                        "none_auth",
                        "security",
                        server_version=server_version,
                        offered_security_types=offered_security_types,
                        selected_security_type=1,
                    )
                else:
                    offered = ".".join(str(item) for item in security_types)
                    return _result(
                        True,
                        False,
                        f"unsupported_security_types_{offered}",
                        "security",
                        server_version=server_version,
                        offered_security_types=offered_security_types,
                    )

            if security_type != RFB_SECURITY_TYPE_VNC_AUTH:
                return _result(
                    True,
                    False,
                    "vnc_auth_unavailable",
                    "security",
                    server_version=server_version,
                    offered_security_types=offered_security_types,
                    selected_security_type=security_type,
                )
            challenge = _read_socket_exact(sock, RFB_VNC_AUTH_CHALLENGE_BYTES)
            response = _vnc_auth_challenge_response(password, challenge)
            if response is None or len(response) != RFB_VNC_AUTH_RESPONSE_BYTES:
                return _result(
                    False,
                    True,
                    "auth_probe_crypto_unavailable",
                    "auth_challenge",
                    server_version=server_version,
                    offered_security_types=offered_security_types,
                    selected_security_type=security_type,
                )
            sock.sendall(response)
            auth_result = struct.unpack(">I", _read_socket_exact(sock, 4))[0]
            if auth_result == 0:
                return _complete_rfb_client_init(
                    sock,
                    started_at=started_at,
                    server_version=server_version,
                    offered_security_types=offered_security_types,
                    selected_security_type=security_type,
                    auth_result=auth_result,
                )
            detail = _read_rfb_failure_reason(sock)
            if auth_result == 1:
                return _result(
                    True,
                    False,
                    f"auth_failed:{detail}" if detail else "auth_failed",
                    "security_result",
                    server_version=server_version,
                    offered_security_types=offered_security_types,
                    selected_security_type=security_type,
                    auth_result=auth_result,
                )
            if auth_result == 2:
                return _result(
                    True,
                    False,
                    _normalize_rfb_rejection_reason(detail, "too_many_auth_failures"),
                    "security_result",
                    server_version=server_version,
                    offered_security_types=offered_security_types,
                    selected_security_type=security_type,
                    auth_result=auth_result,
                )
            return _result(
                True,
                False,
                f"auth_result_{auth_result}",
                "security_result",
                server_version=server_version,
                offered_security_types=offered_security_types,
                selected_security_type=security_type,
                auth_result=auth_result,
            )
    except Exception as exc:
        return _result(True, False, (str(exc) or "connect_failed")[:160], "connect", socket_error=str(exc)[:160])


def _should_retry_vnc_auth_probe(result: VncAuthProbeResult) -> bool:
    if result.ok or not result.checked:
        return False
    reason = _normalize_text(result.reason).replace("_", " ").lower()
    if not reason:
        return False
    transient_markers = {
        "connect failed",
        "connection refused",
        "connection reset",
        "network is unreachable",
        "no route to host",
        "socket closed",
        "timed out",
        "timeout",
    }
    if any(marker in reason for marker in transient_markers):
        return True
    return reason.startswith("server init failed")


def wait_for_vnc_auth_ready(
    host: str,
    port: int,
    password: str,
    *,
    timeout_seconds: float,
    poll_interval_seconds: float,
    enabled: Optional[bool] = None,
) -> VncAuthProbeResult:
    probe_enabled = _normalize_bool(os.environ.get("BOREALIS_VNC_AUTH_PROBE"), False) if enabled is None else bool(enabled)
    if not probe_enabled:
        return VncAuthProbeResult(False, True, "auth_probe_disabled")
    deadline = time.monotonic() + max(0.25, timeout_seconds)
    last_result = VncAuthProbeResult(True, False, "not_checked")
    while time.monotonic() < deadline:
        last_result = probe_vnc_auth(host, port, password, min(1.5, max(0.25, timeout_seconds)))
        if last_result.ok:
            return last_result
        if not _should_retry_vnc_auth_probe(last_result):
            return last_result
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            break
        time.sleep(min(max(0.1, poll_interval_seconds), remaining))
    return last_result


__all__ = [
    "VncAuthProbeResult",
    "probe_vnc_auth",
    "probe_vnc_security",
    "vnc_auth_probe_payload",
    "wait_for_vnc_auth_ready",
]
