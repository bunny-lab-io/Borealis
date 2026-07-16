from __future__ import annotations

import sqlite3

import pytest

from Data.Engine.services.metadata_fields import (
    RESERVED_METADATA_FIELD_TOOLTIP,
    list_metadata_definitions,
    upsert_metadata_definition,
)


def metadata_conn() -> sqlite3.Connection:
    conn = sqlite3.connect(":memory:")
    conn.execute(
        """
        CREATE TABLE metadata_field_definitions (
            field_number INTEGER PRIMARY KEY,
            description TEXT NOT NULL DEFAULT '',
            updated_at INTEGER NOT NULL DEFAULT 0,
            updated_by TEXT NOT NULL DEFAULT ''
        )
        """
    )
    return conn


def test_reserved_metadata_definitions_override_database_labels() -> None:
    conn = metadata_conn()
    conn.execute(
        """
        INSERT INTO metadata_field_definitions(field_number, description, updated_at, updated_by)
        VALUES (1, 'Custom Server Label', 1700000000, 'operator')
        """
    )

    fields = list_metadata_definitions(conn)

    assert fields[0]["label"] == "Server Roles"
    assert fields[0]["description"] == "Server Roles"
    assert fields[0]["reserved"] is True
    assert fields[0]["updated_by"] == "Borealis"
    assert fields[0]["linked_assembly"]["path"] == "/assemblies/scripts/628f6686-c7c4-477d-bf9a-13c73d8246ba"
    assert fields[1]["label"] == "Bitlocker Drive Encryption"
    assert fields[1]["reserved"] is True
    assert fields[9]["label"] == "Reserved"
    assert fields[9]["reserved"] is True
    assert "linked_assembly" not in fields[9]


def test_reserved_metadata_definition_cannot_be_upserted() -> None:
    conn = metadata_conn()

    with pytest.raises(ValueError, match="Reserved Borealis Metadata Field"):
        upsert_metadata_definition(conn, 1, "Asset Tag", actor="operator")

    assert RESERVED_METADATA_FIELD_TOOLTIP.startswith("Reserved Borealis Metadata Field")
