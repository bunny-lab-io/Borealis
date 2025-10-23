import base64
import sqlite3
from datetime import datetime, timezone

import pytest

from Data.Engine.repositories.sqlite import connection as sqlite_connection
from Data.Engine.repositories.sqlite import migrations as sqlite_migrations
from Data.Engine.repositories.sqlite.enrollment_repository import SQLiteEnrollmentRepository
from Data.Engine.repositories.sqlite.user_repository import SQLiteUserRepository
from Data.Engine.services.enrollment.admin_service import EnrollmentAdminService


def _build_service(tmp_path):
    db_path = tmp_path / "admin.db"
    conn = sqlite3.connect(db_path)
    sqlite_migrations.apply_all(conn)
    conn.close()

    factory = sqlite_connection.connection_factory(db_path)
    enrollment_repo = SQLiteEnrollmentRepository(factory)
    user_repo = SQLiteUserRepository(factory)

    fixed_now = datetime(2024, 1, 1, tzinfo=timezone.utc)
    service = EnrollmentAdminService(
        repository=enrollment_repo,
        user_repository=user_repo,
        clock=lambda: fixed_now,
    )
    return service, factory, fixed_now


def test_create_and_list_install_codes(tmp_path):
    service, factory, fixed_now = _build_service(tmp_path)

    record = service.create_install_code(ttl_hours=3, max_uses=5, created_by="admin")
    assert record.code
    assert record.max_uses == 5
    assert record.status(now=fixed_now) == "active"

    records = service.list_install_codes()
    assert any(r.record_id == record.record_id for r in records)

    # Invalid TTL should raise
    with pytest.raises(ValueError):
        service.create_install_code(ttl_hours=2, max_uses=1, created_by=None)

    # Deleting should succeed and remove the record
    assert service.delete_install_code(record.record_id) is True
    remaining = service.list_install_codes()
    assert all(r.record_id != record.record_id for r in remaining)


def test_list_device_approvals_includes_conflict(tmp_path):
    service, factory, fixed_now = _build_service(tmp_path)

    conn = factory()
    cur = conn.cursor()

    cur.execute(
        "INSERT INTO sites (name, description, created_at) VALUES (?, ?, ?)",
        ("HQ", "Primary site", int(fixed_now.timestamp())),
    )
    site_id = cur.lastrowid

    cur.execute(
        """
        INSERT INTO devices (guid, hostname, created_at, last_seen, ssl_key_fingerprint, status)
        VALUES (?, ?, ?, ?, ?, 'active')
        """,
        ("11111111-1111-1111-1111-111111111111", "agent-one", int(fixed_now.timestamp()), int(fixed_now.timestamp()), "abc123",),
    )
    cur.execute(
        "INSERT INTO device_sites (device_hostname, site_id, assigned_at) VALUES (?, ?, ?)",
        ("agent-one", site_id, int(fixed_now.timestamp())),
    )

    now_iso = fixed_now.isoformat()
    cur.execute(
        """
        INSERT INTO device_approvals (
            id,
            approval_reference,
            guid,
            hostname_claimed,
            ssl_key_fingerprint_claimed,
            enrollment_code_id,
            status,
            client_nonce,
            server_nonce,
            created_at,
            updated_at,
            approved_by_user_id,
            agent_pubkey_der
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
        """,
        (
            "approval-1",
            "REF123",
            None,
            "agent-one",
            "abc123",
            "code-1",
            "pending",
            base64.b64encode(b"client").decode(),
            base64.b64encode(b"server").decode(),
            now_iso,
            now_iso,
            None,
            b"pubkey",
        ),
    )
    conn.commit()
    conn.close()

    approvals = service.list_device_approvals()
    assert len(approvals) == 1
    record = approvals[0]
    assert record.hostname_conflict is not None
    assert record.hostname_conflict.fingerprint_match is True
    assert record.conflict_requires_prompt is False

