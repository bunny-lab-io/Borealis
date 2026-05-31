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
from Data.Engine.db import dbapi as sqlite3
import uuid
from datetime import datetime, timezone, timedelta
import time
from typing import Any, Callable, Dict, Optional

AGENT_CONTEXT_HEADER = "X-Borealis-Agent-Context"


def _canonical_context(value: Optional[str]) -> Optional[str]:
    if not value:
        return None
    cleaned = "".join(ch for ch in str(value) if ch.isalnum() or ch in ("_", "-"))
    if not cleaned:
        return None
    return cleaned.upper()

from flask import Blueprint, jsonify, request

from ....auth import device_purge_state
from ....auth.rate_limit import SlidingWindowRateLimiter
from ....crypto import keys as crypto_keys
from ....enrollment.nonce_store import NonceCache
from ....auth.guid_utils import normalize_guid
from ...remote_ops.agent_routes import build_agent_remote_ops_route_payload, fetch_active_site_worker_route
from cryptography.hazmat.primitives import serialization


def register(
    app,
    *,
    db_conn_factory: Callable[[], sqlite3.Connection],
    log: Callable[[str, str, Optional[str]], None],
    jwt_service,
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

    def _enrollment_log(message: str, context_hint: Optional[str] = None) -> None:
        log("device_enrollment", message, context_hint)

    def _rate_limited(
        key: str,
        limiter: SlidingWindowRateLimiter,
        limit: int,
        window_s: float,
        context_hint: Optional[str],
    ):
        decision = limiter.check(key, limit, window_s)
        if not decision.allowed:
            _enrollment_log(
                f"enrollment rate limited key={key} limit={limit}/{window_s}s retry_after={decision.retry_after:.2f}",
                context_hint,
            )
            response = jsonify({"error": "rate_limited", "retry_after": decision.retry_after})
            response.status_code = 429
            response.headers["Retry-After"] = f"{int(decision.retry_after) or 1}"
            return response
        return None

    def _poll_log(message: str, context_hint: Optional[str] = None) -> None:
        _enrollment_log(message, context_hint)

    def _load_site_for_enrollment(cur: sqlite3.Cursor, code_value: str) -> Optional[Dict[str, Any]]:
        cur.execute(
            """
            SELECT id,
                   name,
                   enrollment_code,
                   auto_approve_until
              FROM sites
             WHERE UPPER(enrollment_code) = UPPER(?)
            """,
            (code_value,),
        )
        row = cur.fetchone()
        if not row:
            return None
        keys = ["id", "name", "enrollment_code", "auto_approve_until"]
        record = dict(zip(keys, row))
        return record

    def _auto_approval_decision(
        cur: sqlite3.Cursor,
        *,
        site_record: Dict[str, Any],
        hostname: str,
        fingerprint: str,
    ) -> Dict[str, Any]:
        try:
            until_ts = int(site_record.get("auto_approve_until") or 0)
        except Exception:
            until_ts = 0
        if until_ts <= int(time.time()):
            return {"status": "pending", "guid": None, "reason": "inactive"}
        cur.execute(
            """
            SELECT guid,
                   ssl_key_fingerprint
              FROM devices
             WHERE LOWER(hostname) = LOWER(?)
             LIMIT 1
            """,
            (hostname,),
        )
        existing = cur.fetchone()
        if not existing:
            return {"status": "approved", "guid": None, "reason": "site_auto_approval"}
        existing_guid = normalize_guid(existing[0]) if existing[0] else None
        existing_fingerprint = str(existing[1] or "").strip().lower()
        if existing_guid and existing_fingerprint and existing_fingerprint == fingerprint:
            return {
                "status": "approved",
                "guid": existing_guid,
                "reason": "site_auto_approval_fingerprint_match",
            }
        return {"status": "pending", "guid": None, "reason": "hostname_conflict"}

    def _onboarding_context_from_payload(payload: Dict[str, Any]) -> Dict[str, Any]:
        raw = payload.get("onboarding_context")
        if not isinstance(raw, dict):
            return {}
        context: Dict[str, Any] = {}
        for key in ("job_id", "run_id"):
            try:
                value = int(str(raw.get(key) or "").strip())
                if value > 0:
                    context[key] = value
            except Exception:
                pass
        target = str(raw.get("target") or "").strip()
        if target:
            context["target"] = target[:253]
        return context

    def _record_wrong_code_attempt(
        cur: sqlite3.Cursor,
        *,
        hostname: str,
        fingerprint: str,
        enrollment_code: str,
        remote_addr: str,
        now: datetime,
    ) -> None:
        cutoff = _iso(now - timedelta(days=1))
        cur.execute(
            "DELETE FROM enrollment_code_failures WHERE last_seen_at < ?",
            (cutoff,),
        )
        code_mask = _mask_code(enrollment_code)
        now_iso = _iso(now)
        cur.execute(
            """
            SELECT id, attempt_count
              FROM enrollment_code_failures
             WHERE ssl_key_fingerprint_claimed = ?
            """,
            (fingerprint,),
        )
        existing = cur.fetchone()
        if existing:
            try:
                attempt_count = int(existing[1] or 0) + 1
            except Exception:
                attempt_count = 1
            cur.execute(
                """
                UPDATE enrollment_code_failures
                   SET hostname_claimed = ?,
                       enrollment_code_mask = ?,
                       remote_addr = ?,
                       last_seen_at = ?,
                       attempt_count = ?,
                       last_error = ?
                 WHERE id = ?
                """,
                (
                    hostname,
                    code_mask,
                    remote_addr,
                    now_iso,
                    attempt_count,
                    "invalid_enrollment_code",
                    existing[0],
                ),
            )
            return

        cur.execute(
            """
            INSERT INTO enrollment_code_failures (
                id,
                hostname_claimed,
                ssl_key_fingerprint_claimed,
                enrollment_code_mask,
                remote_addr,
                first_seen_at,
                last_seen_at,
                attempt_count,
                last_error
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
            """,
            (
                str(uuid.uuid4()),
                hostname,
                fingerprint,
                code_mask,
                remote_addr,
                now_iso,
                now_iso,
                1,
                "invalid_enrollment_code",
            ),
        )

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
        now_iso = _iso(_now())
        now_ts = int(time.time())
        required_token_version = max(
            1,
            int(device_purge_state.get_required_token_version(cur, guid_norm) or 1),
        )
        cur.execute(
            """
            SELECT guid, hostname, token_version, status, ssl_key_fingerprint, key_added_at, last_enrollment_at
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
                "last_enrollment_at",
            ]
            record = dict(zip(keys, row))
            record["guid"] = normalize_guid(record.get("guid"))
            stored_fp = (record.get("ssl_key_fingerprint") or "").strip().lower()
            new_fp = (fingerprint or "").strip().lower()
            try:
                current_version = max(1, int(record.get("token_version") or 1))
            except Exception:
                current_version = 1
            effective_current_version = max(current_version, required_token_version)
            if not stored_fp and new_fp:
                cur.execute(
                    """
                    UPDATE devices
                       SET ssl_key_fingerprint = ?,
                           key_added_at = ?,
                           last_enrollment_at = ?,
                           token_version = ?,
                           status = 'active'
                     WHERE guid = ?
                    """,
                    (fingerprint, now_iso, now_ts, effective_current_version, record["guid"]),
                )
                record["ssl_key_fingerprint"] = fingerprint
                record["key_added_at"] = now_iso
                record["token_version"] = effective_current_version
            elif new_fp and stored_fp != new_fp:
                new_version = max(effective_current_version + 1, required_token_version, 1)
                cur.execute(
                    """
                        UPDATE devices
                           SET ssl_key_fingerprint = ?,
                               key_added_at = ?,
                               last_enrollment_at = ?,
                               token_version = ?,
                               status = 'active'
                         WHERE guid = ?
                    """,
                    (fingerprint, now_iso, now_ts, new_version, record["guid"]),
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
            else:
                if current_version != effective_current_version:
                    cur.execute(
                        """
                        UPDATE devices
                           SET last_enrollment_at = ?,
                               token_version = ?,
                               status = 'active'
                         WHERE guid = ?
                        """,
                        (now_ts, effective_current_version, record["guid"]),
                    )
                    record["token_version"] = effective_current_version
                else:
                    cur.execute(
                        """
                        UPDATE devices
                           SET last_enrollment_at = ?,
                               status = 'active'
                         WHERE guid = ?
                        """,
                        (now_ts, record["guid"]),
                    )
                record["status"] = "active"
            record["last_enrollment_at"] = now_ts
            return record

        resolved_hostname = _normalize_host(hostname, guid_norm, cur)
        created_at = int(time.time())
        key_added_at = _iso(_now())
        insert_token_version = max(1, required_token_version)
        cur.execute(
            """
            INSERT INTO devices (
                guid, hostname, created_at, last_enrollment_at, last_seen, ssl_key_fingerprint,
                token_version, status, key_added_at
            )
            VALUES (?, ?, ?, ?, ?, ?, ?, 'active', ?)
            """,
            (
                guid_norm,
                resolved_hostname,
                created_at,
                created_at,
                created_at,
                fingerprint,
                insert_token_version,
                key_added_at,
            ),
        )
        return {
            "guid": guid_norm,
            "hostname": resolved_hostname,
            "token_version": insert_token_version,
            "status": "active",
            "ssl_key_fingerprint": fingerprint,
            "key_added_at": key_added_at,
            "last_enrollment_at": created_at,
        }

    def _hash_refresh_token(token: str) -> str:
        import hashlib

        return hashlib.sha256(token.encode("utf-8")).hexdigest()

    def _issue_refresh_token(cur: sqlite3.Cursor, guid: str) -> Dict[str, Any]:
        # Sliding window expiration; refreshed on each successful token refresh call.
        REFRESH_TOKEN_TTL_DAYS = 90
        token = secrets.token_urlsafe(48)
        now = _now()
        expires_at = now.replace(microsecond=0) + timedelta(days=REFRESH_TOKEN_TTL_DAYS)
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
        onboarding_context = _onboarding_context_from_payload(payload)

        _enrollment_log(
            "enrollment request received "
            f"ip={remote} hostname={hostname or '<missing>'} code_mask={_mask_code(enrollment_code)} "
            f"pubkey_len={len(agent_pubkey_b64 or '')} nonce_len={len(client_nonce_b64 or '')}",
            context_hint,
        )

        if not hostname:
            _enrollment_log(f"enrollment rejected missing_hostname ip={remote}", context_hint)
            return jsonify({"error": "hostname_required"}), 400
        if not enrollment_code:
            _enrollment_log(f"enrollment rejected missing_code ip={remote} host={hostname}", context_hint)
            return jsonify({"error": "enrollment_code_required"}), 400
        if not isinstance(agent_pubkey_b64, str):
            _enrollment_log(f"enrollment rejected missing_pubkey ip={remote} host={hostname}", context_hint)
            return jsonify({"error": "agent_pubkey_required"}), 400
        if not isinstance(client_nonce_b64, str):
            _enrollment_log(f"enrollment rejected missing_nonce ip={remote} host={hostname}", context_hint)
            return jsonify({"error": "client_nonce_required"}), 400

        try:
            agent_pubkey_der = crypto_keys.spki_der_from_base64(agent_pubkey_b64)
        except Exception:
            _enrollment_log(f"enrollment rejected invalid_pubkey ip={remote} host={hostname}", context_hint)
            return jsonify({"error": "invalid_agent_pubkey"}), 400

        if len(agent_pubkey_der) < 10:
            _enrollment_log(f"enrollment rejected short_pubkey ip={remote} host={hostname}", context_hint)
            return jsonify({"error": "invalid_agent_pubkey"}), 400

        try:
            client_nonce_bytes = base64.b64decode(client_nonce_b64, validate=True)
        except Exception:
            _enrollment_log(f"enrollment rejected invalid_nonce ip={remote} host={hostname}", context_hint)
            return jsonify({"error": "invalid_client_nonce"}), 400
        if len(client_nonce_bytes) < 16:
            _enrollment_log(f"enrollment rejected short_nonce ip={remote} host={hostname}", context_hint)
            return jsonify({"error": "invalid_client_nonce"}), 400

        fingerprint = crypto_keys.fingerprint_from_spki_der(agent_pubkey_der)
        rate_error = _rate_limited(f"fp:{fingerprint}", fp_rate_limiter, 12, 60.0, context_hint)
        if rate_error:
            return rate_error

        conn = db_conn_factory()
        try:
            cur = conn.cursor()
            site_record = _load_site_for_enrollment(cur, enrollment_code)
            if not site_record:
                _enrollment_log(
                    "enrollment request rejected invalid_site_enrollment_code "
                    f"host={hostname} fingerprint={fingerprint[:12]} code_mask={_mask_code(enrollment_code)}",
                    context_hint,
                )
                try:
                    _record_wrong_code_attempt(
                        cur,
                        hostname=hostname,
                        fingerprint=fingerprint,
                        enrollment_code=enrollment_code,
                        remote_addr=remote,
                        now=_now(),
                    )
                    conn.commit()
                except Exception as exc:
                    try:
                        conn.rollback()
                    except Exception:
                        pass
                    _enrollment_log(
                        "enrollment wrong_code_tracking_failed "
                        f"host={hostname} fingerprint={fingerprint[:12]} error={type(exc).__name__}",
                        context_hint,
                    )
                return jsonify({"error": "invalid_enrollment_code"}), 400
            site_id = int(site_record["id"])
            cur.execute(
                "DELETE FROM enrollment_code_failures WHERE ssl_key_fingerprint_claimed = ?",
                (fingerprint,),
            )
            reuse_guid = None
            auto_approval = _auto_approval_decision(
                cur,
                site_record=site_record,
                hostname=hostname,
                fingerprint=fingerprint,
            )
            approval_status = str(auto_approval.get("status") or "pending").strip().lower()
            if approval_status == "approved":
                reuse_guid = auto_approval.get("guid") or None

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
                           enrollment_code = ?,
                           site_id = ?,
                           client_nonce = ?,
                           server_nonce = ?,
                           agent_pubkey_der = ?,
                           onboarding_job_id = ?,
                           onboarding_run_id = ?,
                           onboarding_target = ?,
                           status = ?,
                           approved_by_user_id = ?,
                           updated_at = ?
                     WHERE id = ?
                    """,
                    (
                        hostname,
                        reuse_guid,
                        site_record.get("enrollment_code"),
                        site_id,
                        client_nonce_b64,
                        server_nonce_b64,
                        agent_pubkey_der,
                        onboarding_context.get("job_id"),
                        onboarding_context.get("run_id"),
                        onboarding_context.get("target"),
                        approval_status,
                        "site_auto_approval" if approval_status == "approved" else None,
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
                        ssl_key_fingerprint_claimed, enrollment_code, site_id,
                        status, client_nonce, server_nonce, agent_pubkey_der,
                        created_at, updated_at, approved_by_user_id, onboarding_job_id, onboarding_run_id,
                        onboarding_target
                    )
                    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                    """,
                    (
                        record_id,
                        approval_reference,
                        reuse_guid,
                        hostname,
                        fingerprint,
                        site_record.get("enrollment_code"),
                        site_id,
                        approval_status,
                        client_nonce_b64,
                        server_nonce_b64,
                        agent_pubkey_der,
                        now,
                        now,
                        "site_auto_approval" if approval_status == "approved" else None,
                        onboarding_context.get("job_id"),
                        onboarding_context.get("run_id"),
                        onboarding_context.get("target"),
                    ),
                )

            conn.commit()
        finally:
            conn.close()

        response = {
            "status": approval_status,
            "approval_reference": approval_reference,
            "server_nonce": server_nonce_b64,
            "poll_after_ms": 250 if approval_status == "approved" else 3000,
            "signing_key": _signing_key_b64(),
        }
        if approval_status == "approved":
            response["auto_approved"] = True
        _enrollment_log(
            f"enrollment request {approval_status} fingerprint={fingerprint[:12]} host={hostname} ip={remote} "
            f"auto_reason={auto_approval.get('reason')}",
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

        _poll_log(
            "enrollment poll received "
            f"ref={approval_reference} client_nonce_len={len(client_nonce_b64 or '')}"
            f" proof_sig_len={len(proof_sig_b64 or '')}",
            context_hint,
        )

        if not isinstance(approval_reference, str) or not approval_reference:
            _poll_log("enrollment poll rejected missing_reference", context_hint)
            return jsonify({"error": "approval_reference_required"}), 400
        if not isinstance(client_nonce_b64, str):
            _poll_log(f"enrollment poll rejected missing_nonce ref={approval_reference}", context_hint)
            return jsonify({"error": "client_nonce_required"}), 400
        if not isinstance(proof_sig_b64, str):
            _poll_log(f"enrollment poll rejected missing_sig ref={approval_reference}", context_hint)
            return jsonify({"error": "proof_sig_required"}), 400

        try:
            client_nonce_bytes = base64.b64decode(client_nonce_b64, validate=True)
        except Exception:
            _poll_log(f"enrollment poll invalid_client_nonce ref={approval_reference}", context_hint)
            return jsonify({"error": "invalid_client_nonce"}), 400

        try:
            proof_sig = base64.b64decode(proof_sig_b64, validate=True)
        except Exception:
            _poll_log(f"enrollment poll invalid_sig ref={approval_reference}", context_hint)
            return jsonify({"error": "invalid_proof_sig"}), 400

        remote_ops_site_id: Optional[int] = None
        remote_ops_route: Optional[Dict[str, Any]] = None
        remote_ops_reason = "site_worker_unavailable"
        conn = db_conn_factory()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id, guid, hostname_claimed, ssl_key_fingerprint_claimed,
                       enrollment_code, site_id, status, client_nonce, server_nonce,
                       agent_pubkey_der, created_at, updated_at, approved_by_user_id
                  FROM device_approvals
                 WHERE approval_reference = ?
                """,
                (approval_reference,),
            )
            row = cur.fetchone()
            if not row:
                _poll_log(f"enrollment poll unknown_reference ref={approval_reference}", context_hint)
                return jsonify({"status": "unknown"}), 404

            (
                record_id,
                guid,
                hostname_claimed,
                fingerprint,
                _enrollment_code,
                site_id,
                status,
                client_nonce_stored,
                server_nonce_b64,
                agent_pubkey_der,
                created_at,
                updated_at,
                approved_by,
            ) = row

            if client_nonce_stored != client_nonce_b64:
                _poll_log(f"enrollment poll nonce_mismatch ref={approval_reference}", context_hint)
                return jsonify({"error": "nonce_mismatch"}), 400

            try:
                server_nonce_bytes = base64.b64decode(server_nonce_b64, validate=True)
            except Exception:
                _poll_log(f"enrollment poll invalid_server_nonce ref={approval_reference}", context_hint)
                return jsonify({"error": "server_nonce_invalid"}), 400

            message = server_nonce_bytes + approval_reference.encode("utf-8") + client_nonce_bytes

            try:
                public_key = serialization.load_der_public_key(agent_pubkey_der)
            except Exception:
                _poll_log(f"enrollment poll pubkey_load_failed ref={approval_reference}", context_hint)
                public_key = None

            if public_key is None:
                _poll_log(f"enrollment poll invalid_pubkey ref={approval_reference}", context_hint)
                return jsonify({"error": "agent_pubkey_invalid"}), 400

            try:
                public_key.verify(proof_sig, message)
            except Exception:
                _poll_log(f"enrollment poll invalid_proof ref={approval_reference}", context_hint)
                return jsonify({"error": "invalid_proof"}), 400

            if status == "pending":
                _poll_log(
                    f"enrollment poll pending ref={approval_reference} host={hostname_claimed}"
                    f" fingerprint={fingerprint[:12]}",
                    context_hint,
                )
                return jsonify({"status": "pending", "poll_after_ms": 5000})
            if status == "denied":
                _poll_log(
                    f"enrollment poll denied ref={approval_reference} host={hostname_claimed}",
                    context_hint,
                )
                return jsonify({"status": "denied", "reason": "operator_denied"})
            if status == "expired":
                _poll_log(
                    f"enrollment poll expired ref={approval_reference} host={hostname_claimed}",
                    context_hint,
                )
                return jsonify({"status": "expired"})
            if status == "completed":
                _poll_log(
                    f"enrollment poll already_completed ref={approval_reference} host={hostname_claimed}",
                    context_hint,
                )
                return jsonify({"status": "approved", "detail": "finalized"})

            if status != "approved":
                _poll_log(
                    f"enrollment poll unexpected_status={status} ref={approval_reference}",
                    context_hint,
                )
                return jsonify({"status": status or "unknown"}), 400

            nonce_key = f"{approval_reference}:{base64.b64encode(proof_sig).decode('ascii')}"
            if not nonce_cache.consume(nonce_key):
                _poll_log(
                    f"enrollment poll replay_detected ref={approval_reference} fingerprint={fingerprint[:12]}",
                    context_hint,
                )
                return jsonify({"error": "proof_replayed"}), 409

            # Finalize enrollment
            effective_guid = normalize_guid(guid) if guid else normalize_guid(str(uuid.uuid4()))
            now_iso = _iso(_now())

            device_record = _ensure_device_record(cur, effective_guid, hostname_claimed, fingerprint)
            _store_device_key(cur, effective_guid, fingerprint)
            if site_id:
                remote_ops_site_id = int(site_id)
                assigned_at = int(time.time())
                cur.execute(
                    """
                    INSERT INTO device_sites(device_hostname, site_id, assigned_at)
                    VALUES (?, ?, ?)
                    ON CONFLICT(device_hostname)
                    DO UPDATE SET site_id=excluded.site_id, assigned_at=excluded.assigned_at
                    """,
                    (device_record.get("hostname"), site_id, assigned_at),
                )
                remote_ops_route = fetch_active_site_worker_route(conn, site_id=int(site_id))
            else:
                remote_ops_reason = "device_site_unassigned"

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
            device_purge_state.clear_barrier(cur, effective_guid)

            conn.commit()
        finally:
            conn.close()

        remote_ops_payload = build_agent_remote_ops_route_payload(
            app,
            request,
            site_id=remote_ops_site_id,
            route=remote_ops_route,
            reason=remote_ops_reason,
        )
        _poll_log(
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
                "signing_key": _signing_key_b64(),
                "remote_ops_route": remote_ops_payload,
            }
        )

    app.register_blueprint(blueprint)

def _mask_code(code: str) -> str:
    if not code:
        return "<missing>"
    trimmed = str(code).strip()
    if len(trimmed) <= 6:
        return "***"
    return f"{trimmed[:3]}***{trimmed[-3:]}"
