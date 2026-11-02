"""Durable active-worker ownership at the site-worker execution boundary."""

from __future__ import annotations

import hashlib
import json
import re
import time
import uuid
from dataclasses import dataclass
from typing import Any, Callable, Mapping

from Data.Engine.services.activity_history import insert_activity_history_row, normalize_activity_status


_TOKEN = re.compile(r"[0-9a-f]{32}\Z")
_UUID = re.compile(r"[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\Z")
_AGENT_UUID = re.compile(r"[0-9A-F]{8}(?:-[0-9A-F]{4}){3}-[0-9A-F]{12}\Z")
_LABEL = re.compile(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\Z")
_KIND = re.compile(r"[a-z][a-z0-9_]{0,63}\Z")
_MAX_ID = (1 << 53) - 1
_CAPS = (4, 8, 12, 16)
_PLACEMENT_KEYS = {"version", "worker_guid", "site_id", "pod_uid", "pod_name", "node_name", "profile_rank", "route_generation"}
_REF_KEYS = {"job_id", "node_name", "pod_uid", "route_generation", "site_id", "kind", "agent_guid"}
_TERMINAL = {"Completed", "Failed", "Skipped"}
_RESERVED = {"worker_placement", "worker_admission"}
_MAX_METADATA_BYTES = 4 * 1024 * 1024


class AdmissionError(RuntimeError):
    """Missing capacity or ownership evidence; execution must not start."""


def _integer(value: Any) -> bool:
    return type(value) is int and 0 < value <= _MAX_ID


def _name(value: Any, *, node: bool = False) -> bool:
    return isinstance(value, str) and len(value) <= (253 if node else 63) and all(_LABEL.fullmatch(label) for label in (value.split(".") if node else [value]))


def _json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True, allow_nan=False)


def _object(raw: Any) -> dict:
    def strict_object(pairs):
        value = {}
        for key, item in pairs:
            if key in value:
                raise ValueError("duplicate JSON key")
            value[key] = item
        return value
    def invalid_constant(_value):
        raise ValueError("nonfinite JSON number")
    def bounded(value, depth=0):
        if depth > 32:
            raise ValueError("JSON nesting limit")
        if isinstance(value, dict):
            for item in value.values():
                bounded(item, depth + 1)
        elif isinstance(value, list):
            for item in value:
                bounded(item, depth + 1)
    try:
        if isinstance(raw, str) and len(raw.encode("utf-8")) > _MAX_METADATA_BYTES:
            raise ValueError("JSON size limit")
        value = json.loads(raw, object_pairs_hook=strict_object, parse_constant=invalid_constant) if isinstance(raw, str) else raw
        bounded(value)
    except (TypeError, ValueError, RecursionError, UnicodeError) as exc:
        raise AdmissionError("worker_admission_invalid") from exc
    if not isinstance(value, dict):
        raise AdmissionError("worker_admission_invalid")
    return value


def _binding(activity: dict) -> dict:
    value = activity.get("worker_operation")
    if not isinstance(value, dict) or set(value) - {"receipt_sha256", "grant_nonce"} != _REF_KEYS | {"version", "token", "worker_guid"} or not isinstance(value.get("worker_guid"), str) or not _UUID.fullmatch(value["worker_guid"]):
        raise AdmissionError("worker_receipt_unbound")
    wire_operation({key:value.get(key) for key in ("version", "token")})
    admission_ledger({"worker_admission":{"version":1,"operations":{value["token"]:{key:value[key] for key in _REF_KEYS}}}})
    if "receipt_sha256" in value and (not isinstance(value["receipt_sha256"], str) or re.fullmatch(r"[0-9a-f]{64}", value["receipt_sha256"]) is None):
        raise AdmissionError("worker_receipt_unbound")
    if "grant_nonce" in value and (not isinstance(value["grant_nonce"], str) or not _TOKEN.fullmatch(value["grant_nonce"])):
        raise AdmissionError("worker_receipt_unbound")
    return value


def wire_operation(value: Any) -> dict:
    if not isinstance(value, dict) or set(value) != {"version", "token"} or type(value["version"]) is not int or value["version"] != 1 or not isinstance(value["token"], str) or not _TOKEN.fullmatch(value["token"]):
        raise AdmissionError("worker_operation_invalid")
    return dict(value)


def admission_ledger(metadata: Mapping[str, Any]) -> dict:
    if "worker_admission" not in metadata:
        return {"version": 1, "operations": {}}
    ledger = metadata["worker_admission"]
    if not isinstance(ledger, dict) or set(ledger) != {"version", "operations"} or type(ledger["version"]) is not int or ledger["version"] != 1 or not isinstance(ledger["operations"], dict):
        raise AdmissionError("worker_admission_invalid")
    for token, ref in ledger["operations"].items():
        if not isinstance(token, str) or not _TOKEN.fullmatch(token) or not isinstance(ref, dict) or set(ref) != _REF_KEYS:
            raise AdmissionError("worker_admission_invalid")
        if not all(_integer(ref[key]) for key in ("job_id", "route_generation", "site_id")) or not _name(ref["node_name"], node=True) or not isinstance(ref["pod_uid"], str) or not _UUID.fullmatch(ref["pod_uid"]) or not isinstance(ref["kind"], str) or not _KIND.fullmatch(ref["kind"]) or not isinstance(ref["agent_guid"], str) or (ref["agent_guid"] and not _AGENT_UUID.fullmatch(ref["agent_guid"])):
            raise AdmissionError("worker_admission_invalid")
    return ledger


def preserve_admission_metadata(existing: Any, supplied: Mapping[str, Any]) -> dict:
    """Worker registration cannot publish operator placement or erase charges."""
    existing = _object(existing)
    result = {key: value for key, value in supplied.items() if key not in _RESERVED}
    result.update({key: existing[key] for key in _RESERVED if key in existing})
    return result


def admission_allows_prune(metadata: Any, claimed_count: Any) -> bool:
    if type(claimed_count) is not int or claimed_count != 0:
        return False
    try:
        return not admission_ledger(_object(metadata))["operations"]
    except AdmissionError:
        return False


@dataclass(frozen=True)
class AdmissionTicket:
    job_id: int
    token: str

    def wire(self) -> dict:
        return {"version": 1, "token": self.token}


class WorkerAdmission:
    def __init__(self, db_factory: Callable, *, worker_guid: str, site_id: int, pod_uid: str, node_name: str) -> None:
        self.db_factory = db_factory
        self.worker_guid = worker_guid
        self.site_id = site_id
        self.pod_uid = pod_uid
        self.node_name = node_name

    @staticmethod
    def _locking(conn) -> str:
        return "" if getattr(conn, "_dialect", "") == "sqlite" else " FOR UPDATE"

    @staticmethod
    def _begin(conn, node: str) -> None:
        if getattr(conn, "_dialect", "") == "sqlite":
            conn.execute("BEGIN IMMEDIATE").close()
        else:
            conn.execute("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", ("borealis.worker-admission.v1/node/" + node,)).close()

    def _route(self, conn, worker: str, *, lock: bool = False):
        cur = conn.execute("SELECT site_id,generation,status,metadata_json FROM job_scheduler_worker_routes WHERE worker_guid=?" + (self._locking(conn) if lock else ""), (worker,))
        try:
            row = cur.fetchone()
        finally:
            cur.close()
        if row is None:
            raise AdmissionError("worker_placement_unavailable")
        return row[0], row[1], row[2], _object(row[3])

    def _placement(self, row) -> dict:
        site, generation, status, metadata = row
        placement = metadata.get("worker_placement")
        if not isinstance(placement, dict) or set(placement) != _PLACEMENT_KEYS or type(placement["version"]) is not int or placement["version"] != 1:
            raise AdmissionError("worker_placement_unavailable")
        if status != "active" or not _integer(site) or site != self.site_id or not _integer(placement["site_id"]) or placement["site_id"] != site or not _integer(generation) or not _integer(placement["route_generation"]) or placement["route_generation"] != generation:
            raise AdmissionError("worker_placement_changed")
        if placement["worker_guid"] != self.worker_guid or not _UUID.fullmatch(self.worker_guid) or not _UUID.fullmatch(self.pod_uid) or not _name(self.node_name, node=True) or placement["pod_uid"] != self.pod_uid or placement["node_name"] != self.node_name or not _name(placement["pod_name"]):
            raise AdmissionError("worker_placement_changed")
        rank = placement["profile_rank"]
        if type(rank) is not int or not 0 <= rank < len(_CAPS):
            raise AdmissionError("worker_profile_unavailable")
        return placement

    @staticmethod
    def _activity(conn, job_id: int, *, lock: bool = False):
        cur = conn.execute("SELECT hostname,metadata_json FROM activity_history WHERE id=?" + (WorkerAdmission._locking(conn) if lock else ""), (job_id,))
        try:
            row = cur.fetchone()
        finally:
            cur.close()
        if row is None:
            raise AdmissionError("worker_activity_missing")
        return str(row[0] or ""), _object(row[1] or "{}")

    def admit(self, *, kind: str, hostname: str, agent_guid: str = "", job_id: Any = None) -> AdmissionTicket:
        if not isinstance(kind, str) or not _KIND.fullmatch(kind) or not isinstance(hostname, str) or not hostname or len(hostname) > 255 or (agent_guid and not _AGENT_UUID.fullmatch(agent_guid)) or (job_id is not None and not _integer(job_id)):
            raise AdmissionError("worker_operation_invalid")
        conn = self.db_factory()
        try:
            before = self._placement(self._route(conn, self.worker_guid))
        finally:
            conn.close()
        token = uuid.uuid4().hex
        conn = self.db_factory()
        try:
            self._begin(conn, before["node_name"])
            row = self._route(conn, self.worker_guid, lock=True)
            placement = self._placement(row)
            if placement != before:
                raise AdmissionError("worker_placement_changed")
            metadata = row[3]
            ledger = admission_ledger(metadata)
            cur = conn.execute("""SELECT r.worker_guid,w.claimed_count,r.metadata_json FROM job_scheduler_worker_routes r
                LEFT JOIN job_scheduler_workers w ON w.worker_guid=r.worker_guid
                UNION ALL SELECT w.worker_guid,w.claimed_count,NULL FROM job_scheduler_workers w
                WHERE NOT EXISTS(SELECT 1 FROM job_scheduler_worker_routes r WHERE r.worker_guid=w.worker_guid)""")
            try:
                rows = cur.fetchall()
            finally:
                cur.close()
            active = set()
            for worker, count, raw in rows:
                operations = admission_ledger(_object(raw) if raw is not None else {})["operations"]
                if (count is None and operations) or (count is not None and (type(count) is not int or count != len(operations))):
                    raise AdmissionError("worker_admission_unaccounted")
                for ref in operations.values():
                    if ref["node_name"] == before["node_name"]:
                        active.add((worker, ref["pod_uid"]))
            identity = (self.worker_guid, self.pod_uid)
            if identity not in active and len(active) >= _CAPS[before["profile_rank"]]:
                raise AdmissionError("worker_capacity_exhausted")
            now = int(time.time())
            if job_id is None:
                job_id = insert_activity_history_row(conn, hostname=hostname, script_path="", script_name=kind, script_type="", ran_at=now, status="Running", activity_kind="worker_operation", metadata={})
            if not _integer(job_id):
                raise AdmissionError("worker_activity_missing")
            activity_host, activity = self._activity(conn, job_id, lock=True)
            if activity_host.casefold() != hostname.casefold() or "worker_operation" in activity:
                raise AdmissionError("worker_operation_already_admitted")
            ref = {"job_id": job_id, "node_name": self.node_name, "pod_uid": self.pod_uid, "route_generation": before["route_generation"], "site_id": self.site_id, "kind": kind, "agent_guid": agent_guid}
            ledger["operations"][token] = ref
            metadata["worker_admission"] = ledger
            activity["worker_operation"] = {"version": 1, "token": token, "worker_guid": self.worker_guid, **ref}
            if len(_json(metadata).encode("utf-8")) > _MAX_METADATA_BYTES or len(_json(activity).encode("utf-8")) > _MAX_METADATA_BYTES:
                raise AdmissionError("worker_admission_storage_bound")
            conn.execute("UPDATE job_scheduler_worker_routes SET metadata_json=? WHERE worker_guid=?", (_json(metadata), self.worker_guid)).close()
            cur = conn.execute("UPDATE job_scheduler_workers SET claimed_count=? WHERE worker_guid=? AND site_id=?", (len(ledger["operations"]), self.worker_guid, self.site_id))
            try:
                if cur.rowcount != 1:
                    raise AdmissionError("worker_identity_missing")
            finally:
                cur.close()
            conn.execute("UPDATE activity_history SET metadata_json=?,status='Running',started_at=COALESCE(started_at,?),updated_at=? WHERE id=?", (_json(activity), now, now, job_id)).close()
            conn.commit()
            return AdmissionTicket(job_id, token)
        finally:
            conn.close()

    def preflight(self, request: Mapping[str, Any], *, agent_guid: str) -> dict:
        if not isinstance(agent_guid, str) or not _AGENT_UUID.fullmatch(agent_guid):
            raise AdmissionError("worker_preflight_invalid")
        if not isinstance(request, dict) or set(request) != {"job_id", "status", "context", "worker_operation_preflight"} or not _integer(request.get("job_id")) or request.get("status") != "Running" or not isinstance(request.get("context"), dict) or set(request["context"]) != {"worker_operation"}:
            raise AdmissionError("worker_preflight_invalid")
        operation = wire_operation(request["context"]["worker_operation"])
        proof = request["worker_operation_preflight"]
        if not isinstance(proof, dict) or set(proof) != {"version", "nonce"} or type(proof["version"]) is not int or proof["version"] != 1 or not isinstance(proof["nonce"], str) or not _TOKEN.fullmatch(proof["nonce"]):
            raise AdmissionError("worker_preflight_invalid")
        ack = {"job_id":request["job_id"], "worker_operation":operation, "worker_operation_preflight":dict(proof), "status":"deny"}
        try:
            conn = self.db_factory()
            try:
                _, activity = self._activity(conn, request["job_id"])
                expected = _binding(activity)
            finally:
                conn.close()
            if expected["job_id"] != request["job_id"] or expected["token"] != operation["token"] or expected["agent_guid"] != agent_guid or "receipt_sha256" in expected:
                return ack
            conn = self.db_factory()
            try:
                self._begin(conn, expected["node_name"])
                row = self._route(conn, expected["worker_guid"], lock=True)
                _, activity = self._activity(conn, request["job_id"], lock=True)
                current = _binding(activity)
                if {key:value for key,value in current.items() if key != "grant_nonce"} != {key:value for key,value in expected.items() if key != "grant_nonce"}:
                    return ack
                expected = current
                ref = admission_ledger(row[3])["operations"].get(operation["token"])
                if ref != {key:expected[key] for key in _REF_KEYS}:
                    return ack
                issued = expected.get("grant_nonce")
                if issued is not None and issued != proof["nonce"]:
                    return ack
                if issued is None:
                    expected["grant_nonce"] = proof["nonce"]
                    activity["worker_operation"] = expected
                    conn.execute("UPDATE activity_history SET metadata_json=? WHERE id=?", (_json(activity), request["job_id"])).close()
                conn.commit()
                ack["status"] = "execute"
            finally:
                conn.close()
        except AdmissionError:
            pass
        return ack

    def complete(self, receipt: Mapping[str, Any], *, agent_guid: str, _local: bool = False, _terminal_guard: Callable | None = None) -> dict:
        if not isinstance(receipt, dict) or not _integer(receipt.get("job_id")) or receipt.get("status") not in _TERMINAL or not isinstance(receipt.get("context"), dict):
            raise AdmissionError("worker_receipt_invalid")
        operation = wire_operation(receipt["context"].get("worker_operation"))
        if not all(isinstance(receipt.get(key), str) and len(receipt[key].encode("utf-8")) <= 256 * 1024 for key in ("stdout", "stderr")):
            raise AdmissionError("worker_receipt_invalid")
        canonical = {key: receipt[key] for key in ("job_id", "status", "stdout", "stderr")}
        canonical["context"] = {"worker_operation": operation}
        # Agent bounds UTF-8 JSON. ASCII surrogate escaping would reject valid
        # bounded non-ASCII output and strand its durable terminal receipt.
        raw = json.dumps(canonical, sort_keys=True, separators=(",", ":"), ensure_ascii=False, allow_nan=False)
        if len(raw.encode("utf-8")) > 1024 * 1024:
            raise AdmissionError("worker_receipt_invalid")
        digest = hashlib.sha256(raw.encode("utf-8")).hexdigest()
        conn = self.db_factory()
        try:
            _, activity = self._activity(conn, receipt["job_id"])
        finally:
            conn.close()
        expected = _binding(activity)
        if expected["job_id"] != receipt["job_id"] or expected["token"] != operation["token"] or expected["agent_guid"] != agent_guid:
            raise AdmissionError("worker_receipt_unbound")
        if _local:
            if agent_guid or expected["kind"] not in {"ansible", "file_content", "file_stage"} or "grant_nonce" in expected:
                raise AdmissionError("worker_receipt_unbound")
        elif not agent_guid or "grant_nonce" not in expected:
            raise AdmissionError("worker_receipt_ungranted")
        worker = expected.get("worker_guid")
        if not isinstance(worker, str) or not _UUID.fullmatch(worker):
            raise AdmissionError("worker_receipt_unbound")
        ack = {"worker_operation": operation, "job_id": receipt["job_id"], "status": "committed"}
        # A committed activity tombstone remains proof after empty route
        # history is pruned. Reconnect must not depend on that old route.
        if "receipt_sha256" in expected:
            if expected["receipt_sha256"] != digest:
                raise AdmissionError("worker_receipt_conflict")
            return ack
        if _terminal_guard is not None:
            _terminal_guard(expected)
        elif expected["kind"] in {"file_transfer_upload", "file_transfer_download"}:
            raise AdmissionError("worker_file_transfer_custody_missing")
        conn = self.db_factory()
        try:
            self._begin(conn, expected["node_name"])
            row = self._route(conn, worker, lock=True)
            _, activity = self._activity(conn, receipt["job_id"], lock=True)
            current = _binding(activity)
            original = {key:value for key,value in expected.items() if key != "receipt_sha256"}
            if not isinstance(current, dict) or {key:value for key,value in current.items() if key != "receipt_sha256"} != original:
                raise AdmissionError("worker_receipt_changed")
            if "receipt_sha256" in current:
                if current["receipt_sha256"] != digest:
                    raise AdmissionError("worker_receipt_conflict")
                conn.commit()
                return ack
            metadata = row[3]
            ledger = admission_ledger(metadata)
            ref = ledger["operations"].get(operation["token"])
            if ref is None or any(current.get(key) != value for key, value in ref.items()):
                raise AdmissionError("worker_receipt_unbound")
            del ledger["operations"][operation["token"]]
            metadata["worker_admission"] = ledger
            current["receipt_sha256"] = digest
            activity["worker_operation"] = current
            now = int(time.time())
            if _local:
                conn.execute("UPDATE activity_history SET metadata_json=?,status=?,finished_at=?,updated_at=? WHERE id=?", (_json(activity), normalize_activity_status(receipt["status"]), now, now, receipt["job_id"])).close()
            else:
                conn.execute("UPDATE activity_history SET metadata_json=?,status=?,stdout=?,stderr=?,finished_at=?,updated_at=? WHERE id=?", (_json(activity), normalize_activity_status(receipt["status"]), receipt["stdout"], receipt["stderr"], now, now, receipt["job_id"])).close()
            conn.execute("UPDATE job_scheduler_worker_routes SET metadata_json=? WHERE worker_guid=?", (_json(metadata), worker)).close()
            cur = conn.execute("UPDATE job_scheduler_workers SET claimed_count=? WHERE worker_guid=? AND claimed_count=?", (len(ledger["operations"]), worker, len(ledger["operations"])+1))
            try:
                if cur.rowcount != 1:
                    raise AdmissionError("worker_admission_unaccounted")
            finally:
                cur.close()
            conn.commit()
            return ack
        finally:
            conn.close()

    def complete_local(self, ticket: AdmissionTicket, *, status: str = "Completed") -> dict:
        return self.complete({"job_id": ticket.job_id, "status": status, "stdout": "", "stderr": "", "context": {"worker_operation": ticket.wire()}}, agent_guid="", _local=True)
