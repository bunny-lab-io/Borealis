from __future__ import annotations

import json
import argparse
import os
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import threading
import time
import uuid

import pytest
from werkzeug.wsgi import ClosingIterator

from Data.Engine.db import dbapi
from Data.Engine.services.job_scheduler.queue import WORKER_ROUTE_SELECT_COLUMNS, ensure_job_scheduler_tables, heartbeat_worker, prune_worker_history, register_worker, stop_worker, upsert_worker_route
from Data.Engine.services.job_scheduler.worker_admission import AdmissionError, WorkerAdmission
from Data.Engine.services.activity_history import insert_activity_history_row, update_activity_history_row
from Data.Engine.services.job_scheduler import queue as queue_module


AGENT_GUID = "11111111-2222-3333-4444-555555555555"


@pytest.fixture(autouse=True)
def _no_runtime_route_files(monkeypatch):
    monkeypatch.setenv("BOREALIS_SITE_WORKER_ROUTE_FILE_WRITES", "0")


def _factory(path):
    return lambda: dbapi.connect(str(path))


def _initialize(factory):
    with factory() as conn:
        ensure_job_scheduler_tables(conn)
        conn.execute("""CREATE TABLE IF NOT EXISTS activity_history (
            id INTEGER PRIMARY KEY AUTOINCREMENT,hostname TEXT,script_path TEXT,script_name TEXT,script_type TEXT,
            ran_at INTEGER,status TEXT,stdout TEXT,stderr TEXT,queue_lane TEXT,activity_kind TEXT,metadata_json TEXT,
            started_at INTEGER,updated_at INTEGER,finished_at INTEGER)""").close()


def seed_worker(factory, *, rank=0, node="engine-1", site=1, worker=None, pod_uid=None):
    worker = worker or str(uuid.uuid4())
    pod_uid = pod_uid or str(uuid.uuid4())
    with factory() as conn:
        register_worker(conn, worker_guid=worker, container_name="site-" + worker, site_id=site, upstream_port=0)
        row = conn.execute("SELECT generation,metadata_json FROM job_scheduler_worker_routes WHERE worker_guid=?", (worker,)).fetchone()
        metadata = json.loads(row[1])
        metadata["worker_placement"] = {"version": 1, "worker_guid": worker, "site_id": site, "pod_uid": pod_uid, "pod_name": "site-" + worker, "node_name": node, "profile_rank": rank, "route_generation": row[0]}
        conn.execute("UPDATE job_scheduler_worker_routes SET status='active',metadata_json=? WHERE worker_guid=?", (json.dumps(metadata), worker)).close()
    return WorkerAdmission(factory, worker_guid=worker, site_id=site, pod_uid=pod_uid, node_name=node)


def _admit(owner):
    ticket = owner.admit(kind="quick_job_run", hostname="LAB-ONE", agent_guid=AGENT_GUID)
    assert owner.preflight(_preflight(ticket), agent_guid=AGENT_GUID)["status"] == "execute"
    return ticket


def _preflight(ticket, nonce=None):
    return {"job_id":ticket.job_id, "status":"Running", "context":{"worker_operation":ticket.wire()}, "worker_operation_preflight":{"version":1,"nonce":nonce or uuid.uuid4().hex}}


def _receipt(ticket, **changes):
    receipt = {"job_id": ticket.job_id, "status": "Completed", "stdout": "ok", "stderr": "", "context": {"worker_operation": ticket.wire()}}
    receipt.update(changes)
    return receipt


def _admission_snapshot(factory):
    with factory() as conn:
        return tuple(tuple(tuple(row) for row in conn.execute(query).fetchall()) for query in (
            "SELECT * FROM job_scheduler_worker_routes ORDER BY worker_guid",
            "SELECT * FROM job_scheduler_workers ORDER BY worker_guid",
            "SELECT * FROM activity_history ORDER BY id",
        ))


def _set_placement(factory, owner, **changes):
    with factory() as conn:
        metadata = json.loads(conn.execute("SELECT metadata_json FROM job_scheduler_worker_routes WHERE worker_guid=?", (owner.worker_guid,)).fetchone()[0])
        metadata["worker_placement"].update(changes)
        conn.execute("UPDATE job_scheduler_worker_routes SET metadata_json=? WHERE worker_guid=?", (json.dumps(metadata), owner.worker_guid)).close()


@pytest.mark.parametrize("rank,peer_rank", [(rank, peer) for rank in range(4) for peer in range(4) if rank != peer])
@pytest.mark.parametrize("charged", (False, True))
def test_worker_admission_mixed_current_ranks_reject_without_mutation(tmp_path, rank, peer_rank, charged):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    owner = seed_worker(factory, rank=rank)
    peer = seed_worker(factory, rank=rank, site=2)
    tickets = [_admit(owner), _admit(peer)] if charged else []
    _set_placement(factory, peer, profile_rank=peer_rank)
    before = _admission_snapshot(factory)
    with pytest.raises(AdmissionError, match="worker_profile_changed"):
        _admit(owner)
    assert _admission_snapshot(factory) == before
    if charged:
        for worker, ticket in zip((owner, peer), tickets):
            ack = worker.complete(_receipt(ticket), agent_guid=AGENT_GUID)
            assert ack["status"] == "committed"
            settled = _admission_snapshot(factory)
            assert worker.complete(_receipt(ticket), agent_guid=AGENT_GUID) == ack
            assert _admission_snapshot(factory) == settled


def test_worker_admission_mixed_rank_cannot_admit_fifth_worker(tmp_path):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    workers = [seed_worker(factory, site=i+1) for i in range(5)]
    tickets = [_admit(worker) for worker in workers[:-1]]
    _set_placement(factory, workers[-1], profile_rank=1)
    before = _admission_snapshot(factory)
    with pytest.raises(AdmissionError, match="worker_profile_changed"):
        _admit(workers[-1])
    assert _admission_snapshot(factory) == before
    assert len(tickets) == 4


@pytest.mark.parametrize("state", ("retired", "stale_generation", "moved", "replaced"))
def test_worker_admission_historical_placement_keeps_charge_without_policy(tmp_path, state):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    original = seed_worker(factory)
    ticket = _admit(original)
    if state == "moved":
        _set_placement(factory, original, node_name="engine-2", profile_rank=1)
    elif state == "replaced":
        _set_placement(factory, original, pod_uid=str(uuid.uuid4()), profile_rank=1)
    else:
        with factory() as conn:
            if state == "retired":
                conn.execute("UPDATE job_scheduler_worker_routes SET status='retired' WHERE worker_guid=?", (original.worker_guid,)).close()
            else:
                conn.execute("UPDATE job_scheduler_worker_routes SET generation=generation+1 WHERE worker_guid=?", (original.worker_guid,)).close()
    rank = 0 if state == "moved" else 1
    cap = (4, 8)[rank]
    peers = [seed_worker(factory, rank=rank, site=i+2) for i in range(cap)]
    for peer in peers[:-1]:
        _admit(peer)
    before = _admission_snapshot(factory)
    with pytest.raises(AdmissionError, match="capacity_exhausted"):
        _admit(peers[-1])
    assert _admission_snapshot(factory) == before
    ack = original.complete(_receipt(ticket), agent_guid=AGENT_GUID)
    assert original.complete(_receipt(ticket), agent_guid=AGENT_GUID) == ack
    _admit(peers[-1])


@pytest.mark.parametrize("field,value", (("profile_rank", True), ("profile_rank", None), ("profile_rank", 4), ("worker_guid", str(uuid.uuid4())), ("site_id", True), ("pod_uid", None), ("node_name", "invalid..node"), ("route_generation", True), ("route_generation", 999)))
def test_worker_admission_current_peer_requires_native_placement(tmp_path, field, value):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    owner = seed_worker(factory)
    peer = seed_worker(factory, site=2)
    _set_placement(factory, peer, **{field: value})
    before = _admission_snapshot(factory)
    with pytest.raises(AdmissionError):
        _admit(owner)
    assert _admission_snapshot(factory) == before


@pytest.mark.parametrize("placement", (None, {}))
def test_worker_admission_unplaced_active_peer_is_uncertain(tmp_path, placement):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    owner = seed_worker(factory)
    peer = seed_worker(factory, site=2, node="engine-2")
    with factory() as conn:
        conn.execute("UPDATE job_scheduler_worker_routes SET metadata_json=? WHERE worker_guid=?", (json.dumps({"worker_placement": placement}), peer.worker_guid)).close()
    before = _admission_snapshot(factory)
    with pytest.raises(AdmissionError, match="worker_placement_unavailable"):
        _admit(owner)
    assert _admission_snapshot(factory) == before


@pytest.mark.parametrize("rank,cap", tuple(enumerate((4, 8, 12, 16))))
def test_worker_admission_counts_incarnations_not_operations(tmp_path: Path, rank, cap):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    workers = [seed_worker(factory, rank=rank, site=i+1) for i in range(cap+1)]
    tickets = [_admit(owner) for owner in workers[:-1]]
    extra = _admit(workers[0])
    with pytest.raises(AdmissionError, match="capacity_exhausted"):
        _admit(workers[-1])
    workers[0].complete(_receipt(tickets[0]), agent_guid=AGENT_GUID)
    with pytest.raises(AdmissionError, match="capacity_exhausted"):
        _admit(workers[-1])
    workers[0].complete(_receipt(extra), agent_guid=AGENT_GUID)
    _admit(workers[-1])


def test_worker_admission_unknown_work_survives_heartbeat_upsert_and_prune(tmp_path: Path):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    owner = seed_worker(factory)
    ticket = _admit(owner)
    with factory() as conn:
        heartbeat_worker(conn, worker_guid=owner.worker_guid, status="idle", claimed_count=0)
        upsert_worker_route(conn, worker_guid=owner.worker_guid, container_name="site-"+owner.worker_guid, site_id=1, metadata={"worker_admission": {"version":1,"operations":{}}, "worker_placement": {"version":1}})
        conn.execute("UPDATE job_scheduler_workers SET status='lost',stopped_at=1,last_seen_at=1").close()
        conn.execute("UPDATE job_scheduler_worker_routes SET status='lost',retired_at=1").close()
        assert prune_worker_history(conn, retention_seconds=1) == 0
        assert conn.execute("SELECT claimed_count FROM job_scheduler_workers").fetchone()[0] == 1
    assert owner.complete(_receipt(ticket), agent_guid=AGENT_GUID)["status"] == "committed"
    with factory() as conn:
        assert prune_worker_history(conn, retention_seconds=1) == 1
    # Receipt custody can complete after old empty route history disappears.
    assert owner.complete(_receipt(ticket), agent_guid=AGENT_GUID)["status"] == "committed"


@pytest.mark.parametrize("mutation", ("agent", "token", "status", "output", "job"))
def test_worker_admission_receipt_binding(tmp_path: Path, mutation):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    owner = seed_worker(factory)
    ticket = _admit(owner)
    receipt = _receipt(ticket)
    agent = AGENT_GUID
    if mutation == "agent":
        agent = str(uuid.uuid4())
    elif mutation == "token":
        receipt["context"]["worker_operation"]["token"] = uuid.uuid4().hex
    elif mutation == "status":
        receipt["status"] = "accepted"
    elif mutation == "output":
        receipt["stdout"] = "x" * (256*1024+1)
    else:
        receipt["job_id"] = True
    with pytest.raises(AdmissionError):
        owner.complete(receipt, agent_guid=agent)
    with factory() as conn:
        assert conn.execute("SELECT claimed_count FROM job_scheduler_workers").fetchone()[0] == 1
    ack = owner.complete(_receipt(ticket), agent_guid=AGENT_GUID)
    assert ack == {"worker_operation":ticket.wire(), "job_id":ticket.job_id, "status":"committed"}
    assert owner.complete(_receipt(ticket), agent_guid=AGENT_GUID) == ack
    with pytest.raises(AdmissionError, match="conflict"):
        owner.complete(_receipt(ticket, status="Failed"), agent_guid=AGENT_GUID)


@pytest.mark.parametrize("field,value", (("profile_rank",None),("profile_rank",True),("pod_uid",str(uuid.uuid4())),("node_name","other-node"),("route_generation",999),("route_generation",True),("route_generation",1.0),("site_id",True),("site_id",1.0)))
def test_worker_admission_requires_native_placement(tmp_path: Path, field, value):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    owner = seed_worker(factory)
    with factory() as conn:
        raw = conn.execute("SELECT metadata_json FROM job_scheduler_worker_routes").fetchone()[0]
        metadata = json.loads(raw)
        metadata["worker_placement"][field] = value
        conn.execute("UPDATE job_scheduler_worker_routes SET metadata_json=?", (json.dumps(metadata),)).close()
    with pytest.raises(AdmissionError):
        _admit(owner)
    with factory() as conn:
        assert conn.execute("SELECT COUNT(*) FROM activity_history").fetchone()[0] == 0


@pytest.mark.parametrize("raw", (
    '{"worker_admission":{"version":1,"operations":{"a":{}},"operations":{}}}',
    '{"worker_admission":{"version":1,"operations":{}},"worker_admission":{"version":1,"operations":{}}}',
    '{"worker_admission":{"version":1,"operations":{}},"unknown":NaN}',
))
def test_worker_admission_malformed_census_blocks_and_cannot_prune(tmp_path, raw):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    owner = seed_worker(factory)
    corrupt = seed_worker(factory, site=2)
    with factory() as conn:
        conn.execute("UPDATE job_scheduler_worker_routes SET metadata_json=?,status='lost',retired_at=1 WHERE worker_guid=?", (raw,corrupt.worker_guid)).close()
        conn.execute("UPDATE job_scheduler_workers SET status='lost',stopped_at=1,last_seen_at=1 WHERE worker_guid=?", (corrupt.worker_guid,)).close()
    with pytest.raises(AdmissionError, match="admission_invalid"):
        _admit(owner)
    with factory() as conn:
        assert prune_worker_history(conn, retention_seconds=1) == 0


@pytest.mark.parametrize("field,value", (("node_name","engine..one"),("node_name","-engine"),("node_name","engine-"),("pod_uid","00000000-0000-0000-0000-000000000000"),("site_id",True),("route_generation",1.0)))
def test_worker_admission_malformed_original_ref_blocks_whole_census(tmp_path, field, value):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    owner = seed_worker(factory)
    corrupt = seed_worker(factory, site=2)
    ticket = _admit(corrupt)
    with factory() as conn:
        metadata = json.loads(conn.execute("SELECT metadata_json FROM job_scheduler_worker_routes WHERE worker_guid=?", (corrupt.worker_guid,)).fetchone()[0])
        metadata["worker_admission"]["operations"][ticket.token][field] = value
        conn.execute("UPDATE job_scheduler_worker_routes SET metadata_json=? WHERE worker_guid=?", (json.dumps(metadata),corrupt.worker_guid)).close()
    with pytest.raises(AdmissionError, match="admission_invalid"):
        _admit(owner)


@pytest.mark.parametrize("missing_route", (False, True))
def test_worker_admission_unknown_claimed_count_blocks(tmp_path, missing_route):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    owner = seed_worker(factory)
    unknown = seed_worker(factory, site=2)
    with factory() as conn:
        conn.execute("UPDATE job_scheduler_workers SET claimed_count=1 WHERE worker_guid=?", (unknown.worker_guid,)).close()
        if missing_route:
            conn.execute("DELETE FROM job_scheduler_worker_routes WHERE worker_guid=?", (unknown.worker_guid,)).close()
    with pytest.raises(AdmissionError, match="admission_unaccounted"):
        _admit(owner)


def test_worker_admission_preflight_grant_survives_journal_loss_and_metadata_writers(tmp_path):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    owner = seed_worker(factory)
    ticket = owner.admit(kind="quick_job_run", hostname="LAB-ONE", agent_guid=AGENT_GUID)
    with pytest.raises(AdmissionError, match="ungranted"):
        owner.complete(_receipt(ticket), agent_guid=AGENT_GUID)
    request = _preflight(ticket)
    assert owner.preflight(request, agent_guid=AGENT_GUID)["status"] == "execute"
    assert owner.preflight(request, agent_guid=AGENT_GUID)["status"] == "execute"
    assert owner.preflight(_preflight(ticket), agent_guid=AGENT_GUID)["status"] == "deny"
    with factory() as conn:
        retained = json.loads(conn.execute("SELECT metadata_json FROM activity_history WHERE id=?", (ticket.job_id,)).fetchone()[0])["worker_operation"]
        update_activity_history_row(conn, ticket.job_id, metadata={"ordinary":"new","worker_operation":{"receipt_sha256":"f"*64}})
        stored = json.loads(conn.execute("SELECT metadata_json FROM activity_history WHERE id=?", (ticket.job_id,)).fetchone()[0])
        assert stored == {"ordinary":"new", "worker_operation":retained}
    assert owner.preflight(_preflight(ticket), agent_guid=AGENT_GUID)["status"] == "deny"
    owner.complete(_receipt(ticket), agent_guid=AGENT_GUID)
    assert owner.preflight(request, agent_guid=AGENT_GUID)["status"] == "deny"
    with factory() as conn:
        conn.execute("DELETE FROM activity_history WHERE id=?", (ticket.job_id,)).close()
    assert owner.preflight(_preflight(ticket), agent_guid=AGENT_GUID)["status"] == "deny"
    with pytest.raises(AdmissionError, match="activity_missing"):
        owner.complete(_receipt(ticket), agent_guid=AGENT_GUID)


def test_worker_admission_local_completion_preserves_runner_output(tmp_path):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    owner = seed_worker(factory)
    ticket = owner.admit(kind="ansible", hostname="LAB-ONE")
    with factory() as conn:
        conn.execute("UPDATE activity_history SET stdout='runner output',stderr='runner detail' WHERE id=?", (ticket.job_id,)).close()
    owner.complete_local(ticket)
    with factory() as conn:
        row = conn.execute("SELECT stdout,stderr,metadata_json FROM activity_history WHERE id=?", (ticket.job_id,)).fetchone()
        assert row[:2] == ("runner output","runner detail")
        assert "grant_nonce" not in json.loads(row[2])["worker_operation"]
        assert conn.execute("SELECT claimed_count FROM job_scheduler_workers").fetchone()[0] == 0


def test_worker_admission_rejects_copied_activity_binding(tmp_path):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    owner = seed_worker(factory)
    ticket = _admit(owner)
    with factory() as conn:
        binding = conn.execute("SELECT metadata_json FROM activity_history WHERE id=?",(ticket.job_id,)).fetchone()[0]
        conn.execute("INSERT INTO activity_history(hostname,metadata_json) VALUES('LAB-ONE',?)",(binding,)).close()
        copied = conn.execute("SELECT MAX(id) FROM activity_history").fetchone()[0]
    forged = type(ticket)(copied,ticket.token)
    assert owner.preflight(_preflight(forged),agent_guid=AGENT_GUID)["status"] == "deny"
    with pytest.raises(AdmissionError,match="unbound"):
        owner.complete(_receipt(forged),agent_guid=AGENT_GUID)
    with factory() as conn:
        assert conn.execute("SELECT claimed_count FROM job_scheduler_workers").fetchone()[0] == 1


@pytest.mark.parametrize("character", ("\u4e2d", "\U0001f680"))
def test_worker_admission_accepts_agent_bounded_utf8_terminal_output(tmp_path, character):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    owner = seed_worker(factory)
    ticket = _admit(owner)
    output = character * ((256*1024)//len(character.encode("utf-8")))
    receipt = _receipt(ticket,stdout=output,stderr=output)
    ack = owner.complete(receipt,agent_guid=AGENT_GUID)
    assert ack["status"] == "committed"
    assert owner.complete(receipt,agent_guid=AGENT_GUID) == ack
    with factory() as conn:
        assert conn.execute("SELECT claimed_count FROM job_scheduler_workers").fetchone()[0] == 0


@pytest.mark.parametrize("action", ("heartbeat", "stop"))
def test_worker_stale_container_rolls_back_route_and_file_effects(tmp_path, monkeypatch, action):
    factory = _factory(tmp_path / "admission.sqlite")
    _initialize(factory)
    owner = seed_worker(factory)
    with factory() as conn:
        stop_worker(conn,worker_guid=owner.worker_guid)
        conn.execute("UPDATE job_scheduler_workers SET container_name='replacement' WHERE worker_guid=?",(owner.worker_guid,)).close()
        original = conn.execute("SELECT status,generation,metadata_json FROM job_scheduler_worker_routes WHERE worker_guid=?",(owner.worker_guid,)).fetchone()
    effects = []
    monkeypatch.setattr(queue_module,"_write_worker_route_file",lambda route: effects.append(("write",route)))
    monkeypatch.setattr(queue_module,"_remove_worker_route_files",lambda routes: effects.append(("remove",routes)))
    with factory() as conn:
        if action == "heartbeat":
            result = heartbeat_worker(conn,worker_guid=owner.worker_guid,status="idle",container_name="site-"+owner.worker_guid)
        else:
            result = stop_worker(conn,worker_guid=owner.worker_guid,container_name="site-"+owner.worker_guid)
        assert result is False
        assert conn.execute("SELECT status,generation,metadata_json FROM job_scheduler_worker_routes WHERE worker_guid=?",(owner.worker_guid,)).fetchone() == original
    assert effects == []


def run_postgres_admission_checks():
    """Mandatory integration entrypoint; missing PostgreSQL is an error."""
    url = os.environ["BOREALIS_TEST_DATABASE_URL"]
    if not url.startswith(("postgresql://", "postgresql+psycopg://")):
        raise AssertionError("actual disposable PostgreSQL required")
    factory = _factory(url)
    os.environ["BOREALIS_SITE_WORKER_ROUTE_FILE_WRITES"] = "0"
    suffix = uuid.uuid4().hex
    owned_workers = [str(uuid.uuid5(uuid.UUID(suffix),"cap-"+str(i))) for i in range(5)] + [str(uuid.uuid5(uuid.UUID(suffix),"fixture-"+str(i))) for i in range(1,9)]
    placeholders = ",".join("?" for _ in owned_workers)
    with factory() as conn:
        assert conn.execute(f"SELECT COUNT(*) FROM job_scheduler_workers WHERE worker_guid IN ({placeholders})",tuple(owned_workers)).fetchone()[0] == 0
        assert conn.execute(f"SELECT COUNT(*) FROM job_scheduler_worker_routes WHERE worker_guid IN ({placeholders})",tuple(owned_workers)).fetchone()[0] == 0
        original_activity_ids = {row[0] for row in conn.execute("SELECT id FROM activity_history").fetchall()}
    try:
        node = "admission-" + suffix
        site_base = 100000 + int(suffix[:6],16)*16
        with factory() as conn:
            assert conn.execute("SELECT COUNT(*) FROM job_scheduler_workers WHERE site_id BETWEEN ? AND ?",(site_base,site_base+13)).fetchone()[0] == 0
            assert conn.execute("SELECT COUNT(*) FROM job_scheduler_worker_routes WHERE site_id BETWEEN ? AND ?",(site_base,site_base+13)).fetchone()[0] == 0
        passed = []
        def done(name):
            passed.append(name)
            print("PASS PostgreSQL " + name, flush=True)
        def fixture(index):
            return seed_worker(factory, node=node, site=site_base+5+index, worker=str(uuid.uuid5(uuid.UUID(suffix),"fixture-"+str(index))))
        # Existing canonical database initializer owns schema; fixture adds rows only.
        workers = [seed_worker(factory, node=node, site=site_base+i, worker=str(uuid.uuid5(uuid.UUID(suffix),"cap-"+str(i)))) for i in range(5)]
        barrier = threading.Barrier(5)
        def attempt(owner):
            barrier.wait()
            try:
                return _admit(owner)
            except AdmissionError as exc:
                assert str(exc) == "worker_capacity_exhausted"
                return None
        with ThreadPoolExecutor(max_workers=5) as pool:
            tickets = list(pool.map(attempt, workers))
        assert sum(ticket is not None for ticket in tickets) == 4
        done("concurrent_distinct_worker_cap")
        winner = next(i for i,ticket in enumerate(tickets) if ticket is not None)
        loser = next(i for i,ticket in enumerate(tickets) if ticket is None)
        extra = _admit(workers[winner])
        workers[winner].complete(_receipt(tickets[winner]), agent_guid=AGENT_GUID)
        with pytest.raises(AdmissionError, match="capacity_exhausted"):
            _admit(workers[loser])
        workers[winner].complete(_receipt(extra), agent_guid=AGENT_GUID)
        tickets[loser] = _admit(workers[loser])
        for i,ticket in enumerate(tickets):
            if i != winner and ticket is not None:
                workers[i].complete(_receipt(ticket), agent_guid=AGENT_GUID)
        done("same_worker_partial_terminal_retains_slot")

        tickets = [_admit(worker) for worker in workers[:4]]
        _set_placement(factory, workers[4], profile_rank=1)
        before = _admission_snapshot(factory)
        with pytest.raises(AdmissionError, match="worker_profile_changed"):
            _admit(workers[4])
        assert _admission_snapshot(factory) == before
        done("mixed_current_rank_cannot_admit_fifth_worker")
        with pytest.raises(AdmissionError, match="worker_profile_changed"):
            _admit(workers[0])
        assert _admission_snapshot(factory) == before
        for worker, ticket in zip(workers[:4], tickets):
            ack = worker.complete(_receipt(ticket), agent_guid=AGENT_GUID)
            settled = _admission_snapshot(factory)
            assert worker.complete(_receipt(ticket), agent_guid=AGENT_GUID) == ack
            assert _admission_snapshot(factory) == settled
        _set_placement(factory, workers[4], profile_rank=0)
        done("mixed_current_rank_preserves_original_terminal_and_replay")

        # Placement writers do not acquire the node advisory lock. A committed
        # peer update while admission waits for its own route must be present
        # in the later census snapshot. No peer lock or global ordering added.
        owner = workers[4]
        attempted = threading.Event()
        waiting_pid = []
        class WaitingAdmissionConnection:
            def __init__(self): self.conn = factory()
            def __getattr__(self, name): return getattr(self.conn, name)
            def execute(self, sql, params=()):
                if sql.startswith("SELECT site_id,generation,status,metadata_json") and sql.endswith("FOR UPDATE"):
                    waiting_pid.append(self.conn.execute("SELECT pg_backend_pid()").fetchone()[0])
                    attempted.set()
                return self.conn.execute(sql, params)
        owner.db_factory = WaitingAdmissionConnection
        try:
            with ThreadPoolExecutor(max_workers=1) as pool:
                blocker = factory()
                try:
                    blocker.execute("SET LOCAL lock_timeout='5s'").close()
                    blocker.execute("SELECT worker_guid FROM job_scheduler_worker_routes WHERE worker_guid=? FOR UPDATE", (owner.worker_guid,)).close()
                    blocker_pid = blocker.execute("SELECT pg_backend_pid()").fetchone()[0]
                    future = pool.submit(owner.admit, kind="quick_job_run", hostname="LAB-ONE", agent_guid=AGENT_GUID)
                    assert attempted.wait(5), "admission never attempted its route lock"
                    deadline = time.monotonic() + 5
                    with factory() as observer:
                        while True:
                            blockers = observer.execute("SELECT pg_blocking_pids(?)", (waiting_pid[0],)).fetchone()[0]
                            if blocker_pid in blockers:
                                break
                            assert time.monotonic() < deadline, "admission did not wait on held route"
                            assert not future.done(), "admission escaped held route"
                            threading.Event().wait(0.01)
                    _set_placement(factory, workers[0], profile_rank=1)
                    before = _admission_snapshot(factory)
                finally:
                    blocker.close()
                with pytest.raises(AdmissionError, match="worker_profile_changed"):
                    future.result(timeout=5)
                assert _admission_snapshot(factory) == before
        finally:
            owner.db_factory = factory
            _set_placement(factory, workers[0], profile_rank=0)
        done("peer_rank_commit_during_candidate_route_wait_rejects_atomically")

        def custody(owner, ticket):
            with factory() as conn:
                count = conn.execute("SELECT claimed_count FROM job_scheduler_workers WHERE worker_guid=?",(owner.worker_guid,)).fetchone()[0]
                operations = json.loads(conn.execute("SELECT metadata_json FROM job_scheduler_worker_routes WHERE worker_guid=?",(owner.worker_guid,)).fetchone()[0])["worker_admission"]["operations"]
                binding = json.loads(conn.execute("SELECT metadata_json FROM activity_history WHERE id=?",(ticket.job_id,)).fetchone()[0])["worker_operation"]
            return count, operations, binding

        owner = workers[0]
        stage = owner.admit(kind="file_stage",hostname="LAB-ONE")
        count, operations, binding = custody(owner,stage)
        assert count == 1 and set(operations) == {stage.token}
        assert binding["kind"] == "file_stage" and binding["agent_guid"] == "" and "grant_nonce" not in binding
        stage_stream = ClosingIterator(iter([b"multipart fixture"]),lambda: owner.complete_local(stage))
        transfer = owner.admit(kind="file_transfer_upload",hostname="LAB-ONE",agent_guid=AGENT_GUID)
        assert owner.preflight(_preflight(transfer),agent_guid=AGENT_GUID)["status"] == "execute"
        peers = [_admit(peer) for peer in workers[1:4]]
        assert custody(owner,transfer)[0] == 2
        with pytest.raises(AdmissionError,match="capacity_exhausted"):
            _admit(workers[4])
        assert list(stage_stream) == [b"multipart fixture"]
        assert custody(owner,transfer)[0] == 2
        stage_stream.close()
        assert "receipt_sha256" in custody(owner,stage)[2]
        count, operations, original = custody(owner,transfer)
        assert count == 1 and set(operations) == {transfer.token}
        assert original["kind"] == "file_transfer_upload" and original["agent_guid"] == AGENT_GUID and "grant_nonce" in original
        assert "receipt_sha256" not in original
        with pytest.raises(AdmissionError,match="capacity_exhausted"):
            _admit(workers[4])

        # The real completion path must close its read connection before calling
        # the stream owner, then open a separate transaction only after approval.
        connections, guard_calls = [], []
        class TrackedReadConnection:
            def __init__(self):
                self.conn, self.closed = factory(), False
                connections.append(self)
            def __getattr__(self,name): return getattr(self.conn,name)
            def close(self):
                self.conn.close()
                self.closed = True
        closed = threading.Event()
        transfer_stream = ClosingIterator(iter([b"upload fixture"]),closed.set)
        def terminal_guard(expected):
            assert connections and all(conn.closed for conn in connections)
            assert expected == original
            guard_calls.append(dict(expected))
            if not closed.is_set():
                raise AdmissionError("worker_file_transfer_streams_active")
        owner.db_factory = TrackedReadConnection
        try:
            with pytest.raises(AdmissionError,match="custody_missing"):
                owner.complete(_receipt(transfer),agent_guid=AGENT_GUID)
            with pytest.raises(AdmissionError,match="streams_active"):
                owner.complete(_receipt(transfer),agent_guid=AGENT_GUID,_terminal_guard=terminal_guard)
            assert custody(owner,transfer) == (count,operations,original)
            with pytest.raises(AdmissionError,match="capacity_exhausted"):
                _admit(workers[4])
            assert list(transfer_stream) == [b"upload fixture"]
            assert not closed.is_set()
            transfer_stream.close()
            assert custody(owner,transfer) == (count,operations,original)
            ack = owner.complete(_receipt(transfer),agent_guid=AGENT_GUID,_terminal_guard=terminal_guard)
            assert ack == {"job_id":transfer.job_id,"worker_operation":transfer.wire(),"status":"committed"}
            committed_count, committed_operations, committed = custody(owner,transfer)
            assert committed_count == 0 and committed_operations == {}
            assert committed == {**original,"receipt_sha256":committed["receipt_sha256"]}
            assert len(guard_calls) == 2 and all(conn.closed for conn in connections)
            # Durable exact replay needs no vanished process-local stream guard.
            assert owner.complete(_receipt(transfer),agent_guid=AGENT_GUID) == ack
            assert custody(owner,transfer)[0:2] == (0,{})
        finally:
            owner.db_factory = factory
        contender = _admit(workers[4])
        for peer,ticket in zip(workers[1:4],peers):
            peer.complete(_receipt(ticket),agent_guid=AGENT_GUID)
        workers[4].complete(_receipt(contender),agent_guid=AGENT_GUID)
        done("file_stage_upload_guarded_terminal_preserves_worker_cap")

        content = owner.admit(kind="file_content",hostname="LAB-ONE")
        count, operations, original = custody(owner,content)
        assert count == 1 and set(operations) == {content.token}
        assert original["kind"] == "file_content" and original["agent_guid"] == "" and "grant_nonce" not in original
        assert owner.preflight(_preflight(content),agent_guid=AGENT_GUID)["status"] == "deny"
        with pytest.raises(AdmissionError,match="unbound"):
            owner.complete(_receipt(content),agent_guid=AGENT_GUID)
        content_acks = []
        content_stream = ClosingIterator(iter([b"operator artifact"]),lambda: content_acks.append(owner.complete_local(content)))
        assert list(content_stream) == [b"operator artifact"]
        assert custody(owner,content) == (count,operations,original)
        content_stream.close()
        assert content_acks == [{"job_id":content.job_id,"worker_operation":content.wire(),"status":"committed"}]
        count, operations, committed = custody(owner,content)
        assert count == 0 and operations == {}
        assert committed["agent_guid"] == "" and "grant_nonce" not in committed and "receipt_sha256" in committed
        assert owner.complete_local(content) == content_acks[0]
        with pytest.raises(AdmissionError,match="conflict"):
            owner.complete_local(content,status="Failed")
        assert custody(owner,content) == (count,operations,committed)
        done("file_content_local_close_receipt_replay_preserves_count")

        owner = fixture(1)
        ticket = owner.admit(kind="quick_job_run",hostname="LAB-ONE",agent_guid=AGENT_GUID)
        requests = [_preflight(ticket),_preflight(ticket)]
        grant_barrier = threading.Barrier(2)
        def grant(request):
            grant_barrier.wait()
            return owner.preflight(request,agent_guid=AGENT_GUID)
        with ThreadPoolExecutor(max_workers=2) as pool:
            grants = list(pool.map(grant,requests))
        assert sorted(ack["status"] for ack in grants) == ["deny","execute"]
        accepted = requests[next(i for i,ack in enumerate(grants) if ack["status"] == "execute")]
        assert owner.preflight(accepted,agent_guid=AGENT_GUID)["status"] == "execute"
        # A recreated Agent journal uses a fresh nonce; server ownership still fences it.
        assert owner.preflight(_preflight(ticket),agent_guid=AGENT_GUID)["status"] == "deny"
        done("concurrent_single_grant_and_journal_loss_fence")

        with factory() as conn:
            retained = json.loads(conn.execute("SELECT metadata_json FROM activity_history WHERE id=?",(ticket.job_id,)).fetchone()[0])["worker_operation"]
            update_activity_history_row(conn,ticket.job_id,metadata={"ordinary":True,"worker_operation":{"receipt_sha256":"f"*64}})
            assert json.loads(conn.execute("SELECT metadata_json FROM activity_history WHERE id=?",(ticket.job_id,)).fetchone()[0])["worker_operation"] == retained
        assert owner.preflight(_preflight(ticket),agent_guid=AGENT_GUID)["status"] == "deny"
        done("ordinary_activity_writer_preserves_fresh_custody")

        race = threading.Barrier(2)
        def registration():
            race.wait()
            with factory() as conn:
                register_worker(conn,worker_guid=owner.worker_guid,container_name="site-"+owner.worker_guid,site_id=owner.site_id,upstream_port=0)
                heartbeat_worker(conn,worker_guid=owner.worker_guid,status="idle",claimed_count=0)
        def completion():
            race.wait()
            return owner.complete(_receipt(ticket),agent_guid=AGENT_GUID)
        with ThreadPoolExecutor(max_workers=2) as pool:
            futures = [pool.submit(registration),pool.submit(completion)]
            for future in futures:
                future.result(timeout=15)
        with factory() as conn:
            assert conn.execute("SELECT claimed_count FROM job_scheduler_workers WHERE worker_guid=?",(owner.worker_guid,)).fetchone()[0] == 0
        assert owner.complete(_receipt(ticket),agent_guid=AGENT_GUID)["status"] == "committed"
        done("registration_heartbeat_terminal_lock_order_and_replay")

        owner = fixture(2)
        uncertain = _admit(owner)
        with factory() as conn:
            upsert_worker_route(conn,worker_guid=owner.worker_guid,container_name="site-"+owner.worker_guid,site_id=owner.site_id,metadata={"worker_admission":{"version":1,"operations":{}}})
            heartbeat_worker(conn,worker_guid=owner.worker_guid,status="idle",claimed_count=0)
            conn.execute("UPDATE job_scheduler_workers SET status='lost',last_seen_at=1,stopped_at=1 WHERE worker_guid=?",(owner.worker_guid,)).close()
            conn.execute("UPDATE job_scheduler_worker_routes SET status='lost',retired_at=1 WHERE worker_guid=?",(owner.worker_guid,)).close()
            prune_worker_history(conn,retention_seconds=1)
            assert conn.execute("SELECT claimed_count FROM job_scheduler_workers WHERE worker_guid=?",(owner.worker_guid,)).fetchone()[0] == 1
        owner.complete(_receipt(uncertain),agent_guid=AGENT_GUID)
        with factory() as conn:
            prune_worker_history(conn,retention_seconds=1)
            assert conn.execute("SELECT 1 FROM job_scheduler_worker_routes WHERE worker_guid=?",(owner.worker_guid,)).fetchone() is None
        assert owner.complete(_receipt(uncertain),agent_guid=AGENT_GUID)["status"] == "committed"
        done("uncertain_custody_survives_heartbeat_upsert_prune")

        eligible, corrupt = fixture(3), fixture(4)
        with factory() as conn:
            saved = conn.execute("SELECT metadata_json FROM job_scheduler_worker_routes WHERE worker_guid=?",(corrupt.worker_guid,)).fetchone()[0]
            conn.execute("UPDATE job_scheduler_worker_routes SET metadata_json=? WHERE worker_guid=?",('{"worker_admission":{"version":1,"operations":{"unknown":{}},"operations":{}}}',corrupt.worker_guid)).close()
        try:
            with pytest.raises(AdmissionError,match="admission_invalid"):
                _admit(eligible)
        finally:
            with factory() as conn:
                conn.execute("UPDATE job_scheduler_worker_routes SET metadata_json=? WHERE worker_guid=?",(saved,corrupt.worker_guid)).close()
        done("duplicate_json_cannot_erase_census_charge")

        unknown = fixture(5)
        with factory() as conn:
            conn.execute("DELETE FROM job_scheduler_worker_routes WHERE worker_guid=?",(unknown.worker_guid,)).close()
            conn.execute("UPDATE job_scheduler_workers SET claimed_count=1 WHERE worker_guid=?",(unknown.worker_guid,)).close()
        try:
            with pytest.raises(AdmissionError,match="admission_unaccounted"):
                _admit(eligible)
        finally:
            with factory() as conn:
                conn.execute("UPDATE job_scheduler_workers SET claimed_count=0 WHERE worker_guid=?",(unknown.worker_guid,)).close()
        done("orphan_positive_claim_blocks_admission")

        original = _admit(eligible)
        replacement_uid = str(uuid.uuid4())
        with factory() as conn:
            metadata = json.loads(conn.execute("SELECT metadata_json FROM job_scheduler_worker_routes WHERE worker_guid=?",(eligible.worker_guid,)).fetchone()[0])
            metadata["worker_placement"].update(pod_uid=replacement_uid,node_name=node+"-new",route_generation=metadata["worker_placement"]["route_generation"]+1)
            conn.execute("UPDATE job_scheduler_worker_routes SET generation=generation+1,metadata_json=? WHERE worker_guid=?",(json.dumps(metadata),eligible.worker_guid)).close()
        replacement = WorkerAdmission(factory,worker_guid=eligible.worker_guid,site_id=eligible.site_id,pod_uid=replacement_uid,node_name=node+"-new")
        second = _admit(replacement)
        with factory() as conn:
            refs = json.loads(conn.execute("SELECT metadata_json FROM job_scheduler_worker_routes WHERE worker_guid=?",(eligible.worker_guid,)).fetchone()[0])["worker_admission"]["operations"]
            assert refs[original.token]["node_name"] == node
            assert refs[second.token]["node_name"] == node+"-new"
            assert conn.execute("SELECT claimed_count FROM job_scheduler_workers WHERE worker_guid=?",(eligible.worker_guid,)).fetchone()[0] == 2
        eligible.complete(_receipt(original),agent_guid=AGENT_GUID)
        replacement.complete(_receipt(second),agent_guid=AGENT_GUID)
        done("replacement_preserves_original_node_charge")

        with factory() as conn:
            conn.execute("DELETE FROM activity_history WHERE id=?",(second.job_id,)).close()
        assert replacement.preflight(_preflight(second),agent_guid=AGENT_GUID)["status"] == "deny"
        with pytest.raises(AdmissionError,match="activity_missing"):
            replacement.complete(_receipt(second),agent_guid=AGENT_GUID)
        done("deleted_terminal_history_never_grants_or_fabricates_ack")

        published = fixture(6)
        owned = _admit(published)
        with factory() as conn:
            route = conn.execute(f"SELECT {WORKER_ROUTE_SELECT_COLUMNS} FROM job_scheduler_worker_routes WHERE worker_guid=?",(published.worker_guid,)).fetchone()
            conn.execute("DELETE FROM job_scheduler_worker_routes WHERE worker_guid=?",(published.worker_guid,)).close()
        interleaved = []
        class CohortCursor:
            def __init__(self, cursor): self.cursor = cursor
            def fetchall(self):
                result = self.cursor.fetchall()
                assert not any(row[0] == published.worker_guid for row in result)
                with factory() as native:
                    native.execute(f"INSERT INTO job_scheduler_worker_routes ({WORKER_ROUTE_SELECT_COLUMNS}) VALUES ({','.join('?' for _ in route)})",tuple(route)).close()
                interleaved.append(True)
                return result
            def close(self): self.cursor.close()
        class InterleavedConnection:
            def __init__(self, conn): self.conn = conn
            def __getattr__(self,name): return getattr(self.conn,name)
            def execute(self,sql,params=()):
                cursor = self.conn.execute(sql,params)
                if sql.startswith("SELECT worker_guid,metadata_json FROM job_scheduler_worker_routes"):
                    return CohortCursor(cursor)
                return cursor
        with factory() as conn:
            upsert_worker_route(InterleavedConnection(conn),worker_guid=published.worker_guid,container_name="site-"+published.worker_guid,site_id=published.site_id)
            ledger = json.loads(conn.execute("SELECT metadata_json FROM job_scheduler_worker_routes WHERE worker_guid=?",(published.worker_guid,)).fetchone()[0])["worker_admission"]
            assert owned.token in ledger["operations"]
        assert interleaved == [True]
        published.complete(_receipt(owned),agent_guid=AGENT_GUID)
        done("native_route_publication_during_registration_preserves_ledger")

        owner = fixture(7)
        original = _admit(owner)
        with factory() as conn:
            metadata = conn.execute("SELECT metadata_json FROM activity_history WHERE id=?",(original.job_id,)).fetchone()[0]
            copied = insert_activity_history_row(conn,hostname="LAB-ONE",script_path="",script_name="copied",script_type="",ran_at=1,status="Running",metadata=json.loads(metadata))
        forged = type(original)(copied,original.token)
        assert owner.preflight(_preflight(forged),agent_guid=AGENT_GUID)["status"] == "deny"
        with pytest.raises(AdmissionError,match="unbound"):
            owner.complete(_receipt(forged),agent_guid=AGENT_GUID)
        with factory() as conn:
            assert conn.execute("SELECT claimed_count FROM job_scheduler_workers WHERE worker_guid=?",(owner.worker_guid,)).fetchone()[0] == 1
        owner.complete(_receipt(original),agent_guid=AGENT_GUID)
        with pytest.raises(AdmissionError,match="unbound"):
            owner.complete(_receipt(forged),agent_guid=AGENT_GUID)
        done("copied_activity_binding_cannot_grant_complete_or_ack")

        owner = fixture(8)
        with factory() as conn:
            route = conn.execute(f"SELECT {WORKER_ROUTE_SELECT_COLUMNS} FROM job_scheduler_worker_routes WHERE worker_guid=?",(owner.worker_guid,)).fetchone()
            conn.execute("DELETE FROM job_scheduler_worker_routes WHERE worker_guid=?",(owner.worker_guid,)).close()
        route_held, retirement_attempted = threading.Event(), threading.Event()
        class AdmissionConnection:
            def __init__(self): self.conn = factory()
            def __getattr__(self,name): return getattr(self.conn,name)
            def execute(self,sql,params=()):
                if sql.startswith("UPDATE job_scheduler_workers SET claimed_count="):
                    route_held.set()
                    assert retirement_attempted.wait(5)
                return self.conn.execute(sql,params)
        owner.db_factory = AdmissionConnection
        with ThreadPoolExecutor(max_workers=1) as pool:
            admissions = []
            class PublishCursor:
                def __init__(self,cursor): self.cursor = cursor
                def fetchall(self):
                    result = self.cursor.fetchall()
                    assert owner.worker_guid not in [row[0] for row in result]
                    with factory() as native:
                        native.execute(f"INSERT INTO job_scheduler_worker_routes ({WORKER_ROUTE_SELECT_COLUMNS}) VALUES ({','.join('?' for _ in route)})",tuple(route)).close()
                    admissions.append(pool.submit(owner.admit,kind="quick_job_run",hostname="LAB-ONE",agent_guid=AGENT_GUID))
                    assert route_held.wait(5)
                    return result
                def close(self): self.cursor.close()
            class StopCursor:
                def __init__(self,cursor): self.cursor = cursor
                def __getattr__(self,name): return getattr(self.cursor,name)
                def execute(self,sql,params=()):
                    if sql.lstrip().startswith("UPDATE job_scheduler_worker_routes"):
                        retirement_attempted.set()
                    return self.cursor.execute(sql,params)
            class StopConnection:
                def __init__(self,conn): self.conn = conn
                def __getattr__(self,name): return getattr(self.conn,name)
                def cursor(self): return StopCursor(self.conn.cursor())
                def execute(self,sql,params=()):
                    cursor = self.conn.execute(sql,params)
                    if sql.startswith("SELECT worker_guid FROM job_scheduler_worker_routes WHERE site_id IN"):
                        return PublishCursor(cursor)
                    return cursor
            with factory() as conn:
                conn.execute("SET LOCAL lock_timeout='5s'").close()
                assert stop_worker(StopConnection(conn),worker_guid=owner.worker_guid)
            assert len(admissions) == 1
            ticket = admissions[0].result(timeout=5)
        owner.db_factory = factory
        with factory() as conn:
            assert conn.execute("SELECT claimed_count FROM job_scheduler_workers WHERE worker_guid=?",(owner.worker_guid,)).fetchone()[0] == 1
        assert owner.preflight(_preflight(ticket),agent_guid=AGENT_GUID)["status"] == "execute"
        owner.complete(_receipt(ticket),agent_guid=AGENT_GUID)
        done("missing_route_publication_admit_stop_has_no_lock_inversion")
    finally:
        # Exact namespace-owned synthetic rows only. This database subsequently
        # runs mandatory Go tests, whose global census must see no fixture debt.
        with factory() as conn:
            rows = conn.execute("SELECT id,metadata_json FROM activity_history").fetchall()
            activity_ids = []
            for activity_id, raw in rows:
                if activity_id in original_activity_ids:
                    continue
                try:
                    metadata = json.loads(raw or "{}")
                except (TypeError,ValueError):
                    continue
                binding = metadata.get("worker_operation") if isinstance(metadata,dict) else None
                if isinstance(binding,dict) and binding.get("worker_guid") in owned_workers:
                    activity_ids.append(activity_id)
            for activity_id in activity_ids:
                conn.execute("DELETE FROM activity_history WHERE id=?",(activity_id,)).close()
                assert conn.execute("SELECT 1 FROM activity_history WHERE id=?",(activity_id,)).fetchone() is None
            conn.execute(f"DELETE FROM job_scheduler_worker_routes WHERE worker_guid IN ({placeholders})",tuple(owned_workers)).close()
            conn.execute(f"DELETE FROM job_scheduler_workers WHERE worker_guid IN ({placeholders})",tuple(owned_workers)).close()
            assert conn.execute(f"SELECT COUNT(*) FROM job_scheduler_worker_routes WHERE worker_guid IN ({placeholders})",tuple(owned_workers)).fetchone()[0] == 0
            assert conn.execute(f"SELECT COUNT(*) FROM job_scheduler_workers WHERE worker_guid IN ({placeholders})",tuple(owned_workers)).fetchone()[0] == 0
    assert len(passed) == 18 and len(set(passed)) == 18
    print(json.dumps({"suite":"worker_admission_postgres","passed":len(passed),"failed":0,"skipped":0,"cases":passed}),flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--postgres", action="store_true", required=True)
    parser.parse_args()
    run_postgres_admission_checks()
