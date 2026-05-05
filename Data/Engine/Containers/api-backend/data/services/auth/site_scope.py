# ======================================================
# Data\Engine\services\auth\site_scope.py
# Description: Resolves per-user site visibility and access checks for Engine APIs and long-lived job targets.
#
# API Endpoints (if applicable): None
# ======================================================

"""Site-scoped RBAC helpers for Borealis Engine services."""

from __future__ import annotations

import json
import logging
from dataclasses import dataclass
from Data.Engine.db import dbapi as sqlite3
from typing import Any, Dict, Iterable, List, Mapping, Optional, Sequence, Set, Tuple


def _normalize_int(value: Any) -> Optional[int]:
    if value in (None, "", "null"):
        return None
    try:
        return int(value)
    except (TypeError, ValueError):
        return None


def _normalize_int_set(values: Any) -> Set[int]:
    if values is None:
        return set()
    if not isinstance(values, list):
        values = [values]
    normalized: Set[int] = set()
    for entry in values:
        if isinstance(entry, dict):
            entry = entry.get("site_id") or entry.get("id") or entry.get("value")
        parsed = _normalize_int(entry)
        if parsed is not None:
            normalized.add(parsed)
    return normalized


def _normalize_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _is_admin_role(role: Any) -> bool:
    return _normalize_text(role).lower() == "admin"


@dataclass(frozen=True)
class UserSiteScope:
    """Resolved site scope for a Borealis operator."""

    username: str
    role: str
    user_id: Optional[int]
    is_admin: bool
    site_ids: frozenset[int]


class UserSiteAccessManager:
    """Database-backed helper for site-scoped RBAC checks."""

    def __init__(
        self,
        db_conn_factory,
        *,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._db_conn_factory = db_conn_factory
        self._logger = logger or logging.getLogger(__name__)

    def _conn(self) -> sqlite3.Connection:
        conn = self._db_conn_factory()
        try:
            conn.row_factory = sqlite3.Row
        except Exception:
            pass
        return conn

    def get_scope(self, user: Optional[Mapping[str, Any]]) -> UserSiteScope:
        username = _normalize_text((user or {}).get("username"))
        role = _normalize_text((user or {}).get("role")) or "User"
        if not username:
            return UserSiteScope(
                username="",
                role=role,
                user_id=None,
                is_admin=False,
                site_ids=frozenset(),
            )

        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT id, role
                  FROM users
                 WHERE LOWER(username) = LOWER(?)
                """,
                (username,),
            )
            row = cur.fetchone()
            user_id = _normalize_int(row[0] if row else None)
            resolved_role = _normalize_text(row[1] if row else role) or role
            is_admin = _is_admin_role(resolved_role)
            if is_admin or user_id is None:
                return UserSiteScope(
                    username=username,
                    role=resolved_role,
                    user_id=user_id,
                    is_admin=is_admin,
                    site_ids=frozenset(),
                )
            cur.execute(
                """
                SELECT site_id
                  FROM user_site_assignments
                 WHERE user_id = ?
                """,
                (user_id,),
            )
            site_ids = frozenset(
                parsed
                for parsed in (_normalize_int(entry[0]) for entry in cur.fetchall())
                if parsed is not None
            )
            return UserSiteScope(
                username=username,
                role=resolved_role,
                user_id=user_id,
                is_admin=False,
                site_ids=site_ids,
            )
        except Exception:
            self._logger.debug("Failed to resolve site scope for %s", username, exc_info=True)
            return UserSiteScope(
                username=username,
                role=role,
                user_id=None,
                is_admin=_is_admin_role(role),
                site_ids=frozenset(),
            )
        finally:
            conn.close()

    def site_ids_for_user(self, user: Optional[Mapping[str, Any]]) -> Optional[Set[int]]:
        scope = self.get_scope(user)
        if scope.is_admin:
            return None
        return set(scope.site_ids)

    def site_ids_within_scope(
        self,
        user: Optional[Mapping[str, Any]],
        site_ids: Iterable[Any],
    ) -> bool:
        scope = self.get_scope(user)
        if scope.is_admin:
            return True
        normalized = {_normalize_int(value) for value in site_ids}
        normalized.discard(None)
        return normalized.issubset(set(scope.site_ids))

    def user_can_access_site(
        self,
        user: Optional[Mapping[str, Any]],
        site_id: Any,
        *,
        allow_unassigned_admin_only: bool = True,
    ) -> bool:
        scope = self.get_scope(user)
        parsed_site_id = _normalize_int(site_id)
        if parsed_site_id is None:
            return bool(scope.is_admin and allow_unassigned_admin_only)
        return bool(scope.is_admin or parsed_site_id in scope.site_ids)

    def user_can_access_hostname(self, user: Optional[Mapping[str, Any]], hostname: Any) -> bool:
        scope = self.get_scope(user)
        hostname_value = _normalize_text(hostname)
        if not hostname_value:
            return False
        if scope.is_admin:
            return True
        site_id, _site_name, _resolved_hostname = self._lookup_device_site(hostname=hostname_value)
        return site_id is not None and site_id in scope.site_ids

    def user_can_access_guid(self, user: Optional[Mapping[str, Any]], guid: Any) -> bool:
        scope = self.get_scope(user)
        guid_value = _normalize_text(guid)
        if not guid_value:
            return False
        if scope.is_admin:
            return True
        site_id, _site_name, _resolved_hostname = self._lookup_device_site(guid=guid_value)
        return site_id is not None and site_id in scope.site_ids

    def user_can_access_agent_id(self, user: Optional[Mapping[str, Any]], agent_id: Any) -> bool:
        scope = self.get_scope(user)
        agent_id_value = _normalize_text(agent_id)
        if not agent_id_value:
            return False
        if scope.is_admin:
            return True
        site_id, _site_name, _resolved_hostname = self._lookup_device_site(agent_id=agent_id_value)
        return site_id is not None and site_id in scope.site_ids

    def user_can_access_activity_id(self, user: Optional[Mapping[str, Any]], activity_id: Any) -> bool:
        scope = self.get_scope(user)
        activity_id_value = _normalize_int(activity_id)
        if activity_id_value is None:
            return False
        if scope.is_admin:
            return True
        conn = self._conn()
        try:
            cur = conn.cursor()
            cur.execute(
                """
                SELECT hostname
                  FROM activity_history
                 WHERE id = ?
                """,
                (activity_id_value,),
            )
            row = cur.fetchone()
            hostname = _normalize_text(row[0] if row else "")
            if not hostname:
                return False
            return self.user_can_access_hostname(
                {"username": scope.username, "role": scope.role},
                hostname,
            )
        except Exception:
            self._logger.debug("Failed activity access check for %s", activity_id_value, exc_info=True)
            return False
        finally:
            conn.close()

    def scope_job_targets_for_persistence(
        self,
        user: Optional[Mapping[str, Any]],
        targets: Sequence[Any],
    ) -> Tuple[Optional[List[Any]], Optional[str]]:
        scope = self.get_scope(user)
        normalized_targets = list(targets or [])
        if scope.is_admin:
            return normalized_targets, None
        if not scope.site_ids:
            return None, "You do not have any sites assigned. Ask an administrator to assign at least one site."

        scoped_targets: List[Any] = []
        for target in normalized_targets:
            if isinstance(target, dict):
                kind = _normalize_text(target.get("kind") or target.get("type")).lower()
                if kind == "onboarding_scope":
                    site_id = _normalize_int(target.get("site_id") or target.get("siteId"))
                    if site_id is None or site_id not in scope.site_ids:
                        return None, "Onboarding scope is outside your assigned sites."
                    scoped_target = dict(target)
                    scoped_target["kind"] = "onboarding_scope"
                    scoped_target["site_id"] = site_id
                    scoped_target["allowed_site_ids"] = [site_id]
                    scoped_targets.append(scoped_target)
                    continue
                if kind == "filter" or target.get("filter_id") is not None:
                    scoped_target = dict(target)
                    scoped_target["allowed_site_ids"] = sorted(scope.site_ids)
                    scoped_targets.append(scoped_target)
                    continue

                hostname = _normalize_text(target.get("hostname"))
                device_guid = _normalize_text(target.get("device_guid") or target.get("guid"))
                site_id = _normalize_int(target.get("site_id"))
                site_name = _normalize_text(target.get("site_name") or target.get("site"))
                resolved_site_id = site_id
                resolved_site_name = site_name
                resolved_hostname = hostname
                if resolved_site_id is None:
                    looked_up_site_id, looked_up_site_name, looked_up_hostname = self._lookup_device_site(
                        hostname=hostname,
                        guid=device_guid,
                    )
                    resolved_site_id = looked_up_site_id
                    resolved_site_name = looked_up_site_name or resolved_site_name
                    resolved_hostname = looked_up_hostname or resolved_hostname
                if resolved_site_id is None or resolved_site_id not in scope.site_ids:
                    return None, (
                        f"Target device '{resolved_hostname or hostname or device_guid or 'unknown'}' is outside your assigned sites."
                    )
                scoped_targets.append(
                    {
                        "kind": "device",
                        "device_guid": device_guid,
                        "hostname": resolved_hostname,
                        "site_id": resolved_site_id,
                        "site_name": resolved_site_name,
                    }
                )
                continue

            hostname = _normalize_text(target)
            if not hostname:
                continue
            site_id, site_name, resolved_hostname = self._lookup_device_site(hostname=hostname)
            if site_id is None or site_id not in scope.site_ids:
                return None, f"Target device '{hostname}' is outside your assigned sites."
            scoped_targets.append(
                {
                    "kind": "device",
                    "hostname": resolved_hostname or hostname,
                    "site_id": site_id,
                    "site_name": site_name,
                    "device_guid": "",
                }
            )
        return scoped_targets, None

    def job_targets_fit_scope(
        self,
        user: Optional[Mapping[str, Any]],
        targets: Sequence[Any],
    ) -> bool:
        scope = self.get_scope(user)
        if scope.is_admin:
            return True
        if not scope.site_ids:
            return False
        conn = self._conn()
        try:
            normalized_scope = set(scope.site_ids)
            for target in targets or []:
                target_scope = self._target_site_scope(conn, target)
                if target_scope is None:
                    return False
                if not target_scope:
                    return False
                if not target_scope.issubset(normalized_scope):
                    return False
            return True
        except Exception:
            self._logger.debug("Failed to evaluate job target scope", exc_info=True)
            return False
        finally:
            conn.close()

    def filter_job_rows_for_user(
        self,
        user: Optional[Mapping[str, Any]],
        rows: Sequence[Sequence[Any]],
        *,
        targets_index: int = 3,
    ) -> List[Sequence[Any]]:
        scope = self.get_scope(user)
        if scope.is_admin:
            return list(rows or [])
        filtered: List[Sequence[Any]] = []
        for row in rows or []:
            raw_targets = row[targets_index] if len(row) > targets_index else "[]"
            try:
                targets = json.loads(raw_targets or "[]")
            except Exception:
                targets = []
            if self.job_targets_fit_scope(
                {"username": scope.username, "role": scope.role},
                targets,
            ):
                filtered.append(row)
        return filtered

    def _target_site_scope(self, conn: sqlite3.Connection, target: Any) -> Optional[Set[int]]:
        if isinstance(target, dict):
            kind = _normalize_text(target.get("kind") or target.get("type")).lower()
            if kind == "onboarding_scope":
                site_id = _normalize_int(target.get("site_id") or target.get("siteId"))
                return {site_id} if site_id is not None else set()
            if kind == "filter" or target.get("filter_id") is not None:
                allowed_site_ids = _normalize_int_set(
                    target.get("allowed_site_ids") or target.get("scope_site_ids")
                )
                if allowed_site_ids:
                    return allowed_site_ids
                filter_id = _normalize_int(target.get("filter_id") or target.get("id"))
                if filter_id is None:
                    return set()
                cur = conn.cursor()
                cur.execute(
                    """
                    SELECT site_mode
                      FROM device_filters
                     WHERE id = ?
                    """,
                    (filter_id,),
                )
                row = cur.fetchone()
                if not row:
                    return set()
                site_mode = _normalize_text(row[0]).lower() or "global"
                cur.execute(
                    """
                    SELECT site_id
                      FROM device_filter_sites
                     WHERE filter_id = ?
                    """,
                    (filter_id,),
                )
                filter_site_ids = {
                    parsed
                    for parsed in (_normalize_int(site_row[0]) for site_row in cur.fetchall())
                    if parsed is not None
                }
                if site_mode == "global" and not filter_site_ids:
                    return None
                return filter_site_ids

            site_id = _normalize_int(target.get("site_id"))
            hostname = _normalize_text(target.get("hostname"))
            device_guid = _normalize_text(target.get("device_guid") or target.get("guid"))
            if site_id is not None:
                return {site_id}
            looked_up_site_id, _site_name, _resolved_hostname = self._lookup_device_site(
                hostname=hostname,
                guid=device_guid,
                conn=conn,
            )
            return {looked_up_site_id} if looked_up_site_id is not None else set()

        hostname = _normalize_text(target)
        if not hostname:
            return set()
        looked_up_site_id, _site_name, _resolved_hostname = self._lookup_device_site(
            hostname=hostname,
            conn=conn,
        )
        return {looked_up_site_id} if looked_up_site_id is not None else set()

    def _lookup_device_site(
        self,
        *,
        hostname: Any = None,
        guid: Any = None,
        agent_id: Any = None,
        conn: Optional[sqlite3.Connection] = None,
    ) -> Tuple[Optional[int], str, str]:
        hostname_value = _normalize_text(hostname)
        guid_value = _normalize_text(guid)
        agent_id_value = _normalize_text(agent_id)
        if not hostname_value and not guid_value and not agent_id_value:
            return None, "", ""

        owns_connection = conn is None
        if conn is None:
            conn = self._conn()
        try:
            cur = conn.cursor()
            where_sql = ""
            params: Tuple[Any, ...] = ()
            if hostname_value:
                where_sql = "LOWER(d.hostname) = LOWER(?)"
                params = (hostname_value,)
            elif guid_value:
                where_sql = "LOWER(d.guid) = LOWER(?)"
                params = (guid_value,)
            else:
                where_sql = "LOWER(d.agent_id) = LOWER(?)"
                params = (agent_id_value,)
            cur.execute(
                f"""
                SELECT ds.site_id, s.name, d.hostname
                  FROM devices AS d
             LEFT JOIN device_sites AS ds ON ds.device_hostname = d.hostname
             LEFT JOIN sites AS s ON s.id = ds.site_id
                 WHERE {where_sql}
              ORDER BY COALESCE(d.last_seen, 0) DESC, d.hostname ASC
                 LIMIT 1
                """,
                params,
            )
            row = cur.fetchone()
            if not row:
                return None, "", hostname_value
            site_id = _normalize_int(row[0])
            site_name = _normalize_text(row[1])
            resolved_hostname = _normalize_text(row[2]) or hostname_value
            return site_id, site_name, resolved_hostname
        except Exception:
            self._logger.debug("Failed to look up device site", exc_info=True)
            return None, "", hostname_value
        finally:
            if owns_connection:
                conn.close()


__all__ = ["UserSiteAccessManager", "UserSiteScope"]
