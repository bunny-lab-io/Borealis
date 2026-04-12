# ======================================================
# Data\Engine\db\dbapi.py
# Description: Minimal legacy DB-API compatibility layer backed by SQLAlchemy/psycopg connections.
#
# API Endpoints (if applicable): None
# ======================================================

"""Legacy DB-API compatibility backed by PostgreSQL connections."""

from __future__ import annotations

import re
import sqlite3 as _stdlib_sqlite3
from collections.abc import Iterator, Mapping
from typing import Any, List, Optional, Sequence, Tuple

from sqlalchemy.exc import DBAPIError, IntegrityError as SAIntegrityError, OperationalError as SAOperationalError

from .core import get_database_manager


class Error(_stdlib_sqlite3.Error):
    """Base database error."""


class OperationalError(_stdlib_sqlite3.OperationalError, Error):
    """Raised when a database operation fails."""


class IntegrityError(_stdlib_sqlite3.IntegrityError, Error):
    """Raised on uniqueness or foreign-key violations."""


class Row:
    """Row wrapper providing index and key access."""

    def __init__(self, values: Sequence[Any], keys: Sequence[str]) -> None:
        self._values = tuple(values)
        self._keys = tuple(keys)

    def keys(self) -> Tuple[str, ...]:
        return self._keys

    def __iter__(self):
        return iter(self._values)

    def __len__(self) -> int:
        return len(self._values)

    def __getitem__(self, item: Any) -> Any:
        if isinstance(item, str):
            try:
                index = self._keys.index(item)
            except ValueError as exc:
                raise KeyError(item) from exc
            return self._values[index]
        return self._values[item]

    def get(self, key: str, default: Any = None) -> Any:
        try:
            return self[key]
        except KeyError:
            return default

    def __repr__(self) -> str:
        return repr(self._values)


def _translate_placeholders(sql: str) -> str:
    result: List[str] = []
    in_single = False
    in_double = False
    index = 0
    while index < len(sql):
        char = sql[index]
        if char == "'" and not in_double:
            in_single = not in_single
            result.append(char)
        elif char == '"' and not in_single:
            in_double = not in_double
            result.append(char)
        elif char == "?" and not in_single and not in_double:
            result.append("%s")
        else:
            result.append(char)
        index += 1
    return "".join(result)


def _translate_postgres_sql(sql: str) -> str:
    translated = _translate_placeholders(sql)
    insert_ignore = re.match(r"^(?P<prefix>\s*)INSERT\s+OR\s+IGNORE\s+INTO\s+(?P<rest>.+)$", translated, re.IGNORECASE | re.DOTALL)
    if insert_ignore:
        prefix = insert_ignore.group("prefix")
        rest = insert_ignore.group("rest").rstrip().rstrip(";")
        translated = f"{prefix}INSERT INTO {rest} ON CONFLICT DO NOTHING"
    translated = re.sub(
        r"\bINTEGER\s+PRIMARY\s+KEY\s+AUTOINCREMENT\b",
        "BIGSERIAL PRIMARY KEY",
        translated,
        flags=re.IGNORECASE,
    )
    translated = re.sub(r"\bAUTOINCREMENT\b", "", translated, flags=re.IGNORECASE)
    translated = re.sub(r"\bBLOB\b", "BYTEA", translated, flags=re.IGNORECASE)
    return translated


_PRAGMA_TABLE_INFO_RE = re.compile(r"^\s*PRAGMA\s+table_info\((?P<table>[^)]+)\)\s*$", re.IGNORECASE)
_PRAGMA_NOOP_RE = re.compile(
    r"^\s*PRAGMA\s+(?:foreign_keys|journal_mode|busy_timeout|synchronous|cache_size|temp_store)\b",
    re.IGNORECASE,
)
_SQLITE_MASTER_RE = re.compile(
    r"SELECT\s+1\s+FROM\s+sqlite_master\s+WHERE\s+type='table'\s+AND\s+name=%s",
    re.IGNORECASE,
)


def _normalize_identifier(raw: str) -> str:
    text = str(raw or "").strip().strip("'").strip('"')
    if "." in text:
        return text
    return f"engine.{text}"


def _column_type_expression() -> str:
    return """
        CASE
            WHEN c.data_type = 'bigint' THEN 'INTEGER'
            WHEN c.data_type = 'integer' THEN 'INTEGER'
            WHEN c.data_type = 'smallint' THEN 'INTEGER'
            WHEN c.data_type = 'timestamp with time zone' THEN 'TEXT'
            WHEN c.data_type = 'timestamp without time zone' THEN 'TEXT'
            WHEN c.data_type = 'character varying' THEN 'TEXT'
            WHEN c.data_type = 'text' THEN 'TEXT'
            WHEN c.data_type = 'bytea' THEN 'BLOB'
            WHEN c.data_type = 'jsonb' THEN 'TEXT'
            ELSE UPPER(c.data_type)
        END
    """


def _table_info_sql(table_name: str) -> Tuple[str, Tuple[Any, ...]]:
    schema_name, simple_name = ("engine", table_name)
    if "." in table_name:
        schema_name, simple_name = table_name.split(".", 1)
    sql = f"""
        SELECT
            c.ordinal_position - 1 AS cid,
            c.column_name,
            {_column_type_expression()} AS type,
            CASE WHEN c.is_nullable = 'NO' THEN 1 ELSE 0 END AS notnull,
            c.column_default,
            CASE
                WHEN pk.column_name IS NOT NULL THEN 1
                ELSE 0
            END AS pk
        FROM information_schema.columns c
        LEFT JOIN (
            SELECT kcu.column_name
            FROM information_schema.table_constraints tc
            JOIN information_schema.key_column_usage kcu
              ON tc.constraint_name = kcu.constraint_name
             AND tc.table_schema = kcu.table_schema
             AND tc.table_name = kcu.table_name
            WHERE tc.constraint_type = 'PRIMARY KEY'
              AND tc.table_schema = %s
              AND tc.table_name = %s
        ) pk
          ON pk.column_name = c.column_name
        WHERE c.table_schema = %s
          AND c.table_name = %s
        ORDER BY c.ordinal_position
    """
    params = (schema_name, simple_name, schema_name, simple_name)
    return sql, params


def _table_exists_sql() -> str:
    return """
        SELECT 1
        FROM information_schema.tables
        WHERE table_schema = ANY (current_schemas(false))
          AND table_name = %s
        LIMIT 1
    """


def _translate_sql(sql: str, params: Optional[Sequence[Any]]) -> Tuple[Optional[str], Tuple[Any, ...], bool]:
    raw = str(sql or "").strip()
    if not raw:
        return "", tuple(), False
    if _PRAGMA_NOOP_RE.match(raw):
        return None, tuple(), False
    pragma_match = _PRAGMA_TABLE_INFO_RE.match(raw)
    if pragma_match:
        translated_sql, translated_params = _table_info_sql(_normalize_identifier(pragma_match.group("table")))
        return translated_sql, translated_params, True
    translated = _translate_postgres_sql(raw)
    if translated.upper() == "BEGIN IMMEDIATE":
        return "BEGIN", tuple(), False
    if _SQLITE_MASTER_RE.match(translated):
        return _table_exists_sql(), tuple(params or ()), False
    return translated, tuple(params or ()), False


def _map_error(exc: Exception) -> Error:
    if isinstance(exc, SAIntegrityError):
        return IntegrityError(str(exc))
    if isinstance(exc, SAOperationalError):
        return OperationalError(str(exc))
    if isinstance(exc, DBAPIError):
        original = getattr(exc, "orig", None)
        if original is not None:
            return Error(str(original))
    class_name = exc.__class__.__name__.lower()
    if "integrity" in class_name or "unique" in class_name or "foreignkey" in class_name:
        return IntegrityError(str(exc))
    if "operational" in class_name or "interface" in class_name or "connection" in class_name:
        return OperationalError(str(exc))
    return Error(str(exc))


def _split_script(script: str) -> Iterator[str]:
    current: List[str] = []
    in_single = False
    in_double = False
    for char in script:
        if char == "'" and not in_double:
            in_single = not in_single
        elif char == '"' and not in_single:
            in_double = not in_double
        if char == ";" and not in_single and not in_double:
            statement = "".join(current).strip()
            current.clear()
            if statement:
                yield statement
            continue
        current.append(char)
    tail = "".join(current).strip()
    if tail:
        yield tail


class Cursor:
    """Cursor wrapper exposing the legacy DB-API surface."""

    def __init__(self, connection: "Connection", raw_cursor) -> None:
        self.connection = connection
        self._raw_cursor = raw_cursor
        self.lastrowid: Optional[int] = None
        self.rowcount: int = -1
        self.description = None

    def execute(self, sql: str, params: Optional[Sequence[Any]] = None):
        if self.connection._dialect == "sqlite":
            translated_sql = sql
            translated_params = tuple(params or ())
            force_row = False
        else:
            translated_sql, translated_params, force_row = _translate_sql(sql, params)
        if translated_sql is None:
            self.rowcount = -1
            self.description = None
            self.lastrowid = None
            return self
        try:
            self._raw_cursor.execute(translated_sql, translated_params)
            self.rowcount = getattr(self._raw_cursor, "rowcount", -1)
            self.description = getattr(self._raw_cursor, "description", None)
            self.lastrowid = getattr(self._raw_cursor, "lastrowid", None)
            if (
                self.connection._dialect != "sqlite"
                and translated_sql.lstrip().upper().startswith("INSERT")
                and int(self.rowcount or 0) > 0
            ):
                savepoint = "borealis_lastrowid_probe"
                try:
                    self._raw_cursor.execute(f"SAVEPOINT {savepoint}")
                    self._raw_cursor.execute("SELECT LASTVAL()")
                    row = self._raw_cursor.fetchone()
                    self.lastrowid = int(row[0]) if row and row[0] is not None else None
                    self._raw_cursor.execute(f"RELEASE SAVEPOINT {savepoint}")
                except Exception:
                    try:
                        self._raw_cursor.execute(f"ROLLBACK TO SAVEPOINT {savepoint}")
                        self._raw_cursor.execute(f"RELEASE SAVEPOINT {savepoint}")
                    except Exception:
                        pass
                    self.lastrowid = None
            self.connection._force_row_factory = force_row
            return self
        except Exception as exc:
            raise _map_error(exc) from exc

    def executemany(self, sql: str, seq_of_params: Sequence[Sequence[Any]]):
        if self.connection._dialect == "sqlite":
            translated_sql = sql
        else:
            translated_sql, _, _ = _translate_sql(sql, None)
        if translated_sql is None:
            return self
        try:
            params = [tuple(item or ()) for item in seq_of_params]
            self._raw_cursor.executemany(translated_sql, params)
            self.rowcount = getattr(self._raw_cursor, "rowcount", -1)
            self.description = getattr(self._raw_cursor, "description", None)
            self.lastrowid = None
            return self
        except Exception as exc:
            raise _map_error(exc) from exc

    def fetchone(self):
        row = self._raw_cursor.fetchone()
        return self.connection._adapt_row(row, description=self.description)

    def fetchall(self):
        rows = self._raw_cursor.fetchall()
        return [self.connection._adapt_row(row, description=self.description) for row in rows]

    def close(self) -> None:
        try:
            self._raw_cursor.close()
        except Exception:
            pass

    def __iter__(self):
        while True:
            row = self.fetchone()
            if row is None:
                break
            yield row


class Connection:
    """Connection wrapper exposing the legacy DB-API surface."""

    def __init__(self, raw_connection, *, dialect: str = "postgresql") -> None:
        self._raw_connection = raw_connection
        self._dialect = dialect
        self.row_factory = None
        self._force_row_factory = False

    def cursor(self) -> Cursor:
        if self._dialect == "sqlite":
            if self.row_factory is Row:
                self._raw_connection.row_factory = _stdlib_sqlite3.Row
            else:
                self._raw_connection.row_factory = self.row_factory
        return Cursor(self, self._raw_connection.cursor())

    def commit(self) -> None:
        self._raw_connection.commit()

    def rollback(self) -> None:
        self._raw_connection.rollback()

    def close(self) -> None:
        try:
            self._raw_connection.rollback()
        except Exception:
            pass
        self._raw_connection.close()

    def execute(self, sql: str, params: Optional[Sequence[Any]] = None) -> Cursor:
        cur = self.cursor()
        return cur.execute(sql, params)

    def executemany(self, sql: str, seq_of_params: Sequence[Sequence[Any]]) -> Cursor:
        cur = self.cursor()
        return cur.executemany(sql, seq_of_params)

    def executescript(self, script: str) -> None:
        cur = self.cursor()
        try:
            for statement in _split_script(script):
                cur.execute(statement)
            self.commit()
        finally:
            cur.close()

    def __enter__(self) -> "Connection":
        return self

    def __exit__(self, exc_type, exc, _tb) -> None:
        try:
            if exc_type is None:
                self.commit()
            else:
                self.rollback()
        finally:
            self.close()

    def _adapt_row(self, row: Any, *, description: Any = None) -> Any:
        if row is None:
            return None
        if self._dialect == "sqlite":
            if self.row_factory is Row:
                keys = tuple(col[0] for col in description or ())
                return Row(row, keys)
            return row
        keys = tuple(col[0] for col in description or ())
        use_row = self.row_factory is Row or self._force_row_factory
        self._force_row_factory = False
        if use_row:
            return Row(row, keys)
        return row


def connect(database: str, timeout: int = 15, **_kwargs: Any) -> Connection:
    """Create a DB-API compatibility connection backed by the shared PostgreSQL engine."""

    database_text = str(database or "").strip()
    if database_text.startswith("sqlite://") or "://" not in database_text:
        if database_text.startswith("sqlite://"):
            database_text = database_text[len("sqlite:///") :]
        raw = _stdlib_sqlite3.connect(database_text, timeout=timeout)
        return Connection(raw, dialect="sqlite")
    manager = get_database_manager(database, connect_timeout=timeout)
    return Connection(manager.raw_connection(), dialect="postgresql")
