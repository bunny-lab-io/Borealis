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


class VncAuthProbeResult(NamedTuple):
    checked: bool
    ok: bool
    reason: str


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


def _reverse_byte_bits(value: int) -> int:
    result = 0
    for _index in range(8):
        result = (result << 1) | (value & 1)
        value >>= 1
    return result


def _vnc_auth_challenge_response(password: str, challenge: bytes) -> Optional[bytes]:
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
            algorithm = des_algorithm(key)
        elif triple_des_algorithm is not None:
            algorithm = triple_des_algorithm(key * 3)
        else:
            return None
        encryptor = Cipher(algorithm, modes.ECB()).encryptor()
        return encryptor.update(challenge) + encryptor.finalize()
    except Exception:
        return None


def _complete_rfb_client_init(sock: socket.socket) -> VncAuthProbeResult:
    try:
        sock.sendall(b"\x01")
        server_init = _read_socket_exact(sock, 24)
        width, height = struct.unpack(">HH", server_init[:4])
        if width <= 0 or height <= 0:
            return VncAuthProbeResult(True, False, "server_init_invalid_display")
        name_length = struct.unpack(">I", server_init[20:24])[0]
        if name_length < 0 or name_length > 4096:
            return VncAuthProbeResult(True, False, "server_init_invalid_name")
        if name_length:
            _read_socket_exact(sock, name_length)
        return VncAuthProbeResult(True, True, "server_init_ok")
    except Exception as exc:
        return VncAuthProbeResult(True, False, f"server_init_failed:{str(exc)[:120]}")


def probe_vnc_auth(host: str, port: int, password: str, timeout_seconds: float) -> VncAuthProbeResult:
    host_value = _normalize_text(host)
    try:
        port_value = int(port)
    except Exception:
        return VncAuthProbeResult(True, False, "invalid_port")
    if not host_value or port_value < 1 or port_value > 65535:
        return VncAuthProbeResult(True, False, "invalid_endpoint")
    if not _normalize_text(password):
        return VncAuthProbeResult(True, False, "missing_password")
    try:
        with socket.create_connection((host_value, port_value), timeout=max(0.25, timeout_seconds)) as sock:
            sock.settimeout(max(0.25, timeout_seconds))
            server_version = _read_socket_exact(sock, 12)
            if not server_version.startswith(b"RFB "):
                return VncAuthProbeResult(True, False, "invalid_rfb_banner")
            try:
                major = int(server_version[4:7])
                minor = int(server_version[8:11])
            except Exception:
                major = 3
                minor = 8
            client_version = b"RFB 003.008\n" if major > 3 or minor >= 8 else server_version
            sock.sendall(client_version)

            security_type = 0
            if major == 3 and minor <= 3:
                security_type = struct.unpack(">I", _read_socket_exact(sock, 4))[0]
                if security_type == 0:
                    detail = _read_rfb_failure_reason(sock)
                    return VncAuthProbeResult(True, False, detail or "security_type_rejected")
                if security_type == 1:
                    return VncAuthProbeResult(True, True, "none_auth")
                if security_type != 2:
                    return VncAuthProbeResult(True, False, f"unsupported_security_type_{security_type}")
            else:
                security_type_count = _read_socket_exact(sock, 1)[0]
                if security_type_count <= 0:
                    detail = _read_rfb_failure_reason(sock)
                    return VncAuthProbeResult(True, False, detail or "security_type_rejected")
                security_types = _read_socket_exact(sock, security_type_count)
                if 2 in security_types:
                    security_type = 2
                    sock.sendall(b"\x02")
                elif 1 in security_types:
                    sock.sendall(b"\x01")
                    return VncAuthProbeResult(True, True, "none_auth")
                else:
                    offered = ".".join(str(item) for item in security_types)
                    return VncAuthProbeResult(True, False, f"unsupported_security_types_{offered}")

            if security_type != 2:
                return VncAuthProbeResult(True, False, "vnc_auth_unavailable")
            challenge = _read_socket_exact(sock, 16)
            response = _vnc_auth_challenge_response(password, challenge)
            if response is None or len(response) != 16:
                return VncAuthProbeResult(False, True, "auth_probe_crypto_unavailable")
            sock.sendall(response)
            result = struct.unpack(">I", _read_socket_exact(sock, 4))[0]
            if result == 0:
                return _complete_rfb_client_init(sock)
            detail = _read_rfb_failure_reason(sock)
            if result == 1:
                return VncAuthProbeResult(True, False, detail or "auth_failed")
            if result == 2:
                return VncAuthProbeResult(True, False, detail or "too_many_auth_failures")
            return VncAuthProbeResult(True, False, f"auth_result_{result}")
    except Exception as exc:
        return VncAuthProbeResult(True, False, (str(exc) or "connect_failed")[:160])


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
) -> VncAuthProbeResult:
    if not _normalize_bool(os.environ.get("BOREALIS_VNC_AUTH_PROBE"), True):
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


__all__ = ["VncAuthProbeResult", "probe_vnc_auth", "wait_for_vnc_auth_ready"]
