# ======================================================
# Data\Engine\services\API\enrollment\routes.py
# Description: Engine-native device enrollment endpoints handling install codes, approvals, and token issuance.
#
# API Endpoints (if applicable):
# - POST /api/agent/enroll/request (No Authentication) - Submits device enrollment requests.
# - POST /api/agent/enroll/poll (No Authentication) - Finalises approved enrollment requests.
# ======================================================

"""Device enrollment routes for the Borealis Engine runtime."""
from __future__ import annotations

import base64
import secrets
import sqlite3
import uuid
from datetime import datetime, timezone, timedelta
import time
from typing import Any, Callable, Dict, Optional, Tuple

AGENT_CONTEXT_HEADER = "X-Borealis-Agent-Context"


def _canonical_context(value: Optional[str]) -> Optional[str]:
    if not value:
        return None
    cleaned = "".join(ch for ch in str(value) if ch.isalnum() or ch in ("_", "-"))
    if not cleaned:
        return None
    return cleaned.upper()

from flask import Blueprint, jsonify, request

from ....auth.rate_limit import SlidingWindowRateLimiter
from ....crypto import keys as crypto_keys
from ....enrollment.nonce_store import NonceCache
from ....auth.guid_utils import normalize_guid
from cryptography.hazmat.primitives import serialization


def register(
    app,
    *,
    db_conn_factory: Callable[[], sqlite3.Connection],
    log: Callable[[str, str, Optional[str]], None],
    jwt_service,
    tls_bundle_path: str,
    ip_rate_limiter: SlidingWindowRateLimiter,
    fp_rate_limiter: SlidingWindowRateLimiter,
    nonce_cache: NonceCache,
    script_signer,
) -> None:
    blueprint = Blueprint("enrollment", __name__)

    def _now() -> datetime:
        return datetime.now(tz=timezone.utc)

    def _iso(dt: datetime) -> str:
        return dt.isoformat()

    def _remote_addr() -> str:
        forwarded = request.headers.get("X-Forwarded-For")
        if forwarded:
            return forwarded.split(",")[0].strip()
        addr = request.remote_addr or "unknown"
        return addr.strip()

    def _signing_key_b64() -> str:
        if not script_signer:
            return ""
        try:
            return script_signer.public_base64_spki()
        except Exception:
            return ""

    def _rate_limited(
        key: str,
        limiter: SlidingWindowRateLimiter,
        limit: int,
        window_s: float,
        context_hint: Optional[str],
    ):
        decision = limiter.check(key, limit, window_s)
        if not decision.allowed:
            log(
                "server",
                f"enrollment rate limited key={key} limit={limit}/{window_s}s retry_after={decision.retry_after:.2f}",
                context_hint,
            )
            response = jsonify({"error": "rate_limited", "retry_after": decision.retry_after})
            response.status_code = 429
            response.headers["Retry-After"] = f"{int(decision.retry_after) or 1}"
            return response
        return None

    def _load_install_code(cur: sqlite3.Cursor, code_value: str) -> Optional[Dict[str, Any]]:
        cur.execute(
            """
            SELECT id,
                   code,
                   expires_at,
                   used_at,
                   used_by_guid,
                   max_uses,
                   use_count,
                   last_used_at
              FROM enrollment_install_codes
             WHERE code = ?
            """,
            (code_value,),
        )
        row = cur.fetchone()
        if not row:
            return None
        keys = [
            "id",
            "code",
            "expires_at",
            "used_at",
            "used_by_guid",
            "max_uses",
            "use_count",
            "last_used_at",
        ]
        record = dict(zip(keys, row))
        return record

    def _install_code_valid(
        record: Dict[str, Any], fingerprint: str, cur: sqlite3.Cursor
    ) -> Tuple[bool, Optional[str]]:
        if not record:
            return False, None
        expires_at = record.get("expires_at")
        if not isinstance(expires_at, str):
            return False, None
        try:
            expiry = datetime.fromisoformat(expires_at)
        except Exception:
            return False, None
        if expiry <= _now():
            return False, None
        try:
            max_uses = int(record.get("max_uses") or 1)
        except Exception:
            max_uses = 1
        if max_uses < 1:
            max_uses = 1
        try:
            use_count = int(record.get("use_count") or 0)
        except Exception:
            use_count = 0
        if use_count < max_uses:
            return True, None

        guid = normalize_guid(record.get("used_by_guid"))
        if not guid:
            return False, None
        cur.execute(
            "SELECT ssl_key_fingerprint FROM devices WHERE UPPER(guid) = ?",
            (guid,),
        )
        row = cur.fetchone()
        if not row:
            return False, None
        stored_fp = (row[0] or "").strip().lower()
        if not stored_fp:
            return False, None
        if stored_fp == (fingerprint or "").strip().lower():
            return True, guid
        return False, None

    def _normalize_host(hostname: str, guid: str, cur: sqlite3.Cursor) -> str:
        guid_norm = normalize_guid(guid)
        base = (hostname or "").strip() or guid_norm
        base = base[:253]
        candidate = base
        suffix = 1
        while True:
            cur.execute(
                "SELECT guid FROM devices WHERE hostname = ?",
                (candidate,),
            )
            row = cur.fetchone()
            if not row:
                return candidate
            existing_guid = normalize_guid(row[0])
            if existing_guid == guid_norm:
                return candidate
            candidate = f"{base}-{suffix}"
            suffix += 1
            if suffix > 50:
                return guid_norm

    def _store_device_key(cur: sqlite3.Cursor, guid: str, fingerprint: str) -> None:
        guid_norm = normalize_guid(guid)
        added_at = _iso(_now())
        cur.execute(
            """
            INSERT OR IGNORE INTO device_keys (id, guid, ssl_key_fingerprint, added_at)
            VALUES (?, ?, ?, ?)
            """,
            (str(uuid.uuid4()), guid_norm, fingerprint, added_at),
        )
        cur.execute(
            """
            UPDATE device_keys
               SET retired_at = ?
             WHERE guid = ?
               AND ssl_key_fingerprint != ?
               AND retired_at IS NULL
            """,
            (_iso(_now()), guid_norm, fingerprint),
        )

    def _ensure_device_record(cur: sqlite3.Cursor, guid: str, hostname: str, fingerprint: str) -> Dict[str, Any]:
        guid_norm = normalize_guid(guid)
        cur.execute(
            """
            SELECT guid, hostname, token_version, status, ssl_key_fingerprint, key_added_at
              FROM devices
             WHERE UPPER(guid) = ?
            """,
            (guid_norm,),
        )
        row = cur.fetchone()
        if row:
            keys = [
                "guid",
                "hostname",
                "token_version",
                "status",
                "ssl_key_fingerprint",
                "key_added_at",
            ]
            record = dict(zip(keys, row))
            record["guid"] = normalize_guid(record.get("guid"))
            stored_fp = (record.get("ssl_key_fingerprint") or "").strip().lower()
            new_fp = (fingerprint or "").strip().lower()
            if not stored_fp and new_fp:
                cur.execute(
                    "UPDATE devices SET ssl_key_fingerprint = ?, key_added_at = ? WHERE guid = ?",
                    (fingerprint, _iso(_now()), record["guid"]),
                )
                record["ssl_key_fingerprint"] = fingerprint
            elif new_fp and stored_fp != new_fp:
                now_iso = _iso(_now())
                try:
                    current_version = int(record.get("token_version") or 1)
                except Exception:
                    current_version = 1
                new_version = max(current_version + 1, 1)
                cur.execute(
                    """
                    UPDATE devices
                       SET ssl_key_fingerprint = ?,
                           key_added_at = ?,
                           token_version = ?,
                           status = 'active'
                     WHERE guid = ?
                    """,
                    (fingerprint, now_iso, new_version, record["guid"]),
                )
                cur.execute(
                    """
                    UPDATE refresh_tokens
                       SET revoked_at = ?
                     WHERE guid = ?
                       AND revoked_at IS NULL
                    """,
                    (now_iso, record["guid"]),
                )
                record["ssl_key_fingerprint"] = fingerprint
                record["token_version"] = new_version
                record["status"] = "active"
                record["key_added_at"] = now_iso
            return record

        resolved_hostname = _normalize_host(hostname, guid_norm, cur)
        created_at = int(time.time())
        key_added_at = _iso(_now())
        cur.execute(
            """
            INSERT INTO devices (
                guid, hostname, created_at, last_seen, ssl_key_fingerprint,
                token_version, status, key_added_at
            )
            VALUES (?, ?, ?, ?, ?, 1, 'active', ?)
            """,
            (
                guid_norm,
                resolved_hostname,
                created_at,
                created_at,
                fingerprint,
                key_added_at,
            ),
        )
        return {
            "guid": guid_norm,
            "hostname": resolved_hostname,
            "token_version": 1,
            "status": "active",
            "ssl_key_fingerprint": fingerprint,
            "key_added_at": key_added_at,
        }

    def _hash_refresh_token(token: str) -> str:
        import hashlib

        return hashlib.sha256(token.encode("utf-8")).hexdigest()

    def _issue_refresh_token(cur: sqlite3.Cursor, guid: str) -> Dict[str, Any]:
        token = secrets.token_urlsafe(48)
        now = _now()
        expires_at = now.replace(microsecond=0) + timedelta(days=30)
        cur.execute(
            """
            INSERT INTO refresh_tokens (id, guid, token_hash, created_at, expires_at)
            VALUES (?, ?, ?, ?, ?)
            """,
            (
                str(uuid.uuid4()),
                guid,
                _hash_refresh_token(token),
                _iso(now),
                _iso(expires_at),
            ),
        )
        return {"token": token, "expires_at": expires_at}

    @blueprint.route("/api/agent/enroll/request", methods=["POST"])
    def enrollment_request():
        remote = _remote_addr()
        context_hint = _canonical_context(request.headers.get(AGENT_CONTEXT_HEADER))

        rate_error = _rate_limited(f"ip:{remote}", ip_rate_limiter, 40, 60.0, context_hint)
        if rate_error:
            return rate_error

        payload = request.get_json(force=True, silent=True) or {}
        hostname = str(payload.get("hostname") or "").strip()
        enrollment_code = str(payload.get("enrollment_code") or "").strip()
        agent_pubkey_b64 = payload.get("agent_pubkey")
        client_nonce_b64 = payload.get("client_nonce")

        log(
            "server",
            "enrollment request received "
            f"ip={remote} hostname={hostname or '<missing>'} code_mask={_mask_code(enrollment_code)} "
            f"pubkey_len={len(agent_pubkey_b64 or '')} nonce_len={len(client_nonce_b64 or '')}",
            context_hint,
        )

        if not hostname:
            log("server", f"enrollment rejected missing_hostname ip={remote}", context_hint)
            return jsonify({"error": "hostname_required"}), 400
        if not enrollment_code:
            log("server", f"enrollment rejected missing_code ip={remote} host={hostname}", context_hint)
            return jsonify({"error": "enrollment_code_required"}), 400
        if not isinstance(agent_pubkey_b64, str):
            log("server", f"enrollment rejected missing_pubkey ip={remote} host={hostname}", context_hint)
            return jsonify({"error": "agent_pubkey_required"}), 400
        if not isinstance(client_nonce_b64, str):
            log("server", f"enrollment rejected missing_nonce ip={remote} host={hostname}", context_hint)
            return jsonify({"error": "client_nonce_required"}), 400

        try:
            agent_pubkey_der = crypto_keys.spki_der_from_base64(agent_pubkey_b64)
        except Exception:
            log("server", f"enrollment rejected invalid_pubkey ip={remote} host={hostname}", context_hint)
            return jsonify({"error": "invalid_agent_pubkey"}), 400

        if len(agent_pubkey_der) < 10:
            log("server", f"enrollment rejected short_pubkey ip={remote} host={hostname}", context_hint)
            return jsonify({"error": "invalid_agent_pubkey"}), 400

        try:
            client_nonce_bytes = base64.b64decode(client_nonce_b64, validate=True)
        except Exception:
            log("server", f"enrollment rejected invalid_nonce ip={remote} host={hostname}", context_hint)
            return jsonify({"error": "invalid_client_nonce"}), 400
        if len(client_nonce_bytes) < 16:
            log("server", f"enrollment rejected short_nonce ip={remote} host={hostname}", context_hint)
            return jsonify({"error": "invalid_client_nonce"}), 400

        fingerprint = crypto_keys.fingerprint_from_spki_der(agent_pubkey_der)
        rate_error = _rate_limited(f"fp:{fingerprint}", fp_rate_limiter, 12, 60.0, context_hint)
        if rate_error:
            return rate_error

        conn = db_conn_factory()
        try:
            cur = conn.cursor()
            install_code = _load_install_code(cur, enrollment_code)
            valid_code, reuse_guid = _install_code_valid(install_code, fingerprint, cur)
            if not valid_code:
                log(
                    "server",
                    "enrollment request invalid_code "
                    f"host={hostname} fingerprint={fingerprint[:12]} code_mask={_mask_code(enrollment_code)}",
                    context_hint,
                )
                return jsonify({"error": "invalid_enrollment_code"}), 400

            approval_reference: str
            record_id: str
            server_nonce_bytes = secrets.token_bytes(32)
            server_nonce_b64 = base64.b64encode(server_nonce_bytes).decode("ascii")
            now = _iso(_now())

            cur.execute(
                """
                SELECT id, approval_reference
                  FROM device_approvals
                 WHERE ssl_key_fingerprint_claimed = ?
                   AND status = 'pending'
                """,
                (fingerprint,),
            )
            existing = cur.fetchone()
            if existing:
                record_id = existing[0]
                approval_reference = existing[1]
                cur.execute(
                    """
                    UPDATE device_approvals
                       SET hostname_claimed = ?,
                           guid = ?,
                           enrollment_code_id = ?,
                           client_nonce = ?,
                           server_nonce = ?,
                           agent_pubkey_der = ?,
                           updated_at = ?
                     WHERE id = ?
                    """,
                    (
                        hostname,
                        reuse_guid,
                        install_code["id"],
                        client_nonce_b64,
                        server_nonce_b64,
                        agent_pubkey_der,
                        now,
                        record_id,
                    ),
                )
            else:
                record_id = str(uuid.uuid4())
                approval_reference = str(uuid.uuid4())
                cur.execute(
                    """
                    INSERT INTO device_approvals (
                        id, approval_reference, guid, hostname_claimed,
                        ssl_key_fingerprint_claimed, enrollment_code_id,
                        status, client_nonce, server_nonce, agent_pubkey_der,
                        created_at, updated_at
                    )
                    VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?, ?)
                    """,
                    (
                        record_id,
                        approval_reference,
                        reuse_guid,
                        hostname,
                        fingerprint,
                        install_code["id"],
                        client_nonce_b64,
                        server_nonce_b64,
                        agent_pubkey_der,
                        now,
                        now,
                    ),
                )

            conn.commit()
        finally:
            conn.close()

        response = {
            "status": "pending",
            "approval_reference": approval_reference,
            "server_nonce": server_nonce_b64,
            "poll_after_ms": 3000,
            "server_certificate": _load_tls_bundle(tls_bundle_path),
            "signing_key": _signing_key_b64(),
        }
        log(
            "server",
            f"enrollment request queued fingerprint={fingerprint[:12]} host={hostname} ip={remote}",
            context_hint,
        )
        return jsonify(response)

    @blueprint.route("/api/agent/enroll/poll", methods=["POST"])
    def enrollment_poll():
        payload = request.get_json(force=True, silent=True) or {}
        approval_reference = payload.get("approval_reference")
        client_nonce_b64 = payload.get("client_nonce")
        proof_sig_b64 = payload.get("proof_sig")
        context_hint = _canonical_context(request.headers.get(AGENT_CONTEXT_HEADER))

        log(
            "server",
            "enrollment poll received "
            f"ref={approval_reference} client_nonce_len={len(client_nonce_b64 or '')}"
            f" proof_sig_len={len(proof_sig_b64 or '')}",
            context_hint,
        )

        if not isinstance(approval_reference, str) or not approval_reference:
            log("server", "enrollment poll rejected missing_reference", context_hint)
            return jsonify({"error": "approval_reference_required"}), 400
        if not isinstance(client_nonce_b64, str):
            log("server", f"enrollment poll rejected missing_nonce ref={approval_reference}", context_hint)
            return jsonify({"error": "client_nonce_required"}), 400
        if not isinstance(proof_sig_b64, str):
            log("server", f"enrollment poll rejected missing_sig ref={approval_reference}", context_hint)
            return jsonify({"error": "proof_sig_required"}), 400

        try:
            client_nonce_bytes = base64.b64decode(client_nonce_b64, validate=True)
        except Exception:
            log("server", f"enrollment poll invalid_client_nonce ref={approval_reference}", context_hint)
            return jsonify({"error": "invalid_client_nonce"}), 400

        try:
            proof_sig = base64.b64decode(proof_sig_b64, validate=True)
        except Exception:
            log("server", f"enrollment poll invalid_sig ref={approval_reference}", context_hint)
            return jsonify({"error": "invalid_proof_sig"}), 400

        conn = db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id, guid, hostname_claimed, ssl_key_fingerprint_claimed,
                       enrollment_code_id, status, client_nonce, server_nonce,
                       agent_pubkey_der, created_at, updated_at, approved_by_user_id
                  FROM device_approvals
                 WHERE approval_reference = ?
                """,
                (approval_reference,),
            )
            row = cur.fetchone()
            if not row:
                log("server", f"enrollment poll unknown_reference ref={approval_reference}", context_hint)
                return jsonify({"status": "unknown"}), 404

            (
                record_id,
                guid,
                hostname_claimed,
                fingerprint,
                enrollment_code_id,
                status,
                client_nonce_stored,
                server_nonce_b64,
                agent_pubkey_der,
                created_at,
                updated_at,
                approved_by,
            ) = row

            if client_nonce_stored != client_nonce_b64:
                log("server", f"enrollment poll nonce_mismatch ref={approval_reference}", context_hint)
                return jsonify({"error": "nonce_mismatch"}), 400

            try:
                server_nonce_bytes = base64.b64decode(server_nonce_b64, validate=True)
            except Exception:
                log("server", f"enrollment poll invalid_server_nonce ref={approval_reference}", context_hint)
                return jsonify({"error": "server_nonce_invalid"}), 400

            message = server_nonce_bytes + approval_reference.encode("utf-8") + client_nonce_bytes

            try:
                public_key = serialization.load_der_public_key(agent_pubkey_der)
            except Exception:
                log("server", f"enrollment poll pubkey_load_failed ref={approval_reference}", context_hint)
                public_key = None

            if public_key is None:
                log("server", f"enrollment poll invalid_pubkey ref={approval_reference}", context_hint)
                return jsonify({"error": "agent_pubkey_invalid"}), 400

            try:
                public_key.verify(proof_sig, message)
            except Exception:
                log("server", f"enrollment poll invalid_proof ref={approval_reference}", context_hint)
                return jsonify({"error": "invalid_proof"}), 400

            if status == "pending":
                log(
                    "server",
                    f"enrollment poll pending ref={approval_reference} host={hostname_claimed}"
                    f" fingerprint={fingerprint[:12]}",
                    context_hint,
                )
                return jsonify({"status": "pending", "poll_after_ms": 5000})
            if status == "denied":
                log(
                    "server",
                    f"enrollment poll denied ref={approval_reference} host={hostname_claimed}",
                    context_hint,
                )
                return jsonify({"status": "denied", "reason": "operator_denied"})
            if status == "expired":
                log(
                    "server",
                    f"enrollment poll expired ref={approval_reference} host={hostname_claimed}",
                    context_hint,
                )
                return jsonify({"status": "expired"})
            if status == "completed":
                log(
                    "server",
                    f"enrollment poll already_completed ref={approval_reference} host={hostname_claimed}",
                    context_hint,
                )
                return jsonify({"status": "approved", "detail": "finalized"})

            if status != "approved":
                log(
                    "server",
                    f"enrollment poll unexpected_status={status} ref={approval_reference}",
                    context_hint,
                )
                return jsonify({"status": status or "unknown"}), 400

            nonce_key = f"{approval_reference}:{base64.b64encode(proof_sig).decode('ascii')}"
            if not nonce_cache.consume(nonce_key):
                log(
                    "server",
                    f"enrollment poll replay_detected ref={approval_reference} fingerprint={fingerprint[:12]}",
                    context_hint,
                )
                return jsonify({"error": "proof_replayed"}), 409

            # Finalize enrollment
            effective_guid = normalize_guid(guid) if guid else normalize_guid(str(uuid.uuid4()))
            now_iso = _iso(_now())

            device_record = _ensure_device_record(cur, effective_guid, hostname_claimed, fingerprint)
            _store_device_key(cur, effective_guid, fingerprint)

            # Mark install code used
            if enrollment_code_id:
                cur.execute(
                    "SELECT use_count, max_uses FROM enrollment_install_codes WHERE id = ?",
                    (enrollment_code_id,),
                )
                usage_row = cur.fetchone()
                try:
                    prior_count = int(usage_row[0]) if usage_row else 0
                except Exception:
                    prior_count = 0
                try:
                    allowed_uses = int(usage_row[1]) if usage_row else 1
                except Exception:
                    allowed_uses = 1
                if allowed_uses < 1:
                    allowed_uses = 1
                new_count = prior_count + 1
                consumed = new_count >= allowed_uses
                cur.execute(
                    """
                    UPDATE enrollment_install_codes
                       SET use_count = ?,
                           used_by_guid = ?,
                           last_used_at = ?,
                           used_at = CASE WHEN ? THEN ? ELSE used_at END
                     WHERE id = ?
                    """,
                    (
                        new_count,
                        effective_guid,
                        now_iso,
                        1 if consumed else 0,
                        now_iso,
                        enrollment_code_id,
                    ),
                )
                cur.execute(
                    """
                    UPDATE enrollment_install_codes_persistent
                       SET last_known_use_count = ?,
                           used_by_guid = ?,
                           last_used_at = ?,
                           used_at = CASE WHEN ? THEN ? ELSE used_at END,
                           is_active = CASE WHEN ? THEN 0 ELSE is_active END,
                           consumed_at = CASE WHEN ? THEN COALESCE(consumed_at, ?) ELSE consumed_at END,
                           archived_at = CASE WHEN ? THEN COALESCE(archived_at, ?) ELSE archived_at END
                     WHERE id = ?
                    """,
                    (
                        new_count,
                        effective_guid,
                        now_iso,
                        1 if consumed else 0,
                        now_iso,
                        1 if consumed else 0,
                        1 if consumed else 0,
                        now_iso,
                        1 if consumed else 0,
                        now_iso,
                        enrollment_code_id,
                    ),
                )

            # Update approval record with final state
            cur.execute(
                """
                UPDATE device_approvals
                   SET guid = ?,
                       status = 'completed',
                       updated_at = ?
                 WHERE id = ?
                """,
                (effective_guid, now_iso, record_id),
            )

            refresh_info = _issue_refresh_token(cur, effective_guid)
            access_token = jwt_service.issue_access_token(
                effective_guid,
                fingerprint,
                device_record.get("token_version") or 1,
            )

            conn.commit()
        finally:
            conn.close()

        log(
            "server",
            f"enrollment finalized guid={effective_guid} fingerprint={fingerprint[:12]} host={hostname_claimed}",
            context_hint,
        )
        return jsonify(
            {
                "status": "approved",
                "guid": effective_guid,
                "access_token": access_token,
                "expires_in": 900,
                "refresh_token": refresh_info["token"],
                "token_type": "Bearer",
                "server_certificate": _load_tls_bundle(tls_bundle_path),
                "signing_key": _signing_key_b64(),
            }
        )

    app.register_blueprint(blueprint)


def _load_tls_bundle(path: str) -> str:
    try:
        with open(path, "r", encoding="utf-8") as fh:
            return fh.read()
    except Exception:
        return ""


def _mask_code(code: str) -> str:
    if not code:
        return "<missing>"
    trimmed = str(code).strip()
    if len(trimmed) <= 6:
        return "***"
    return f"{trimmed[:3]}***{trimmed[-3:]}"

