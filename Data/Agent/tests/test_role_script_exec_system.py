from __future__ import annotations

import asyncio
import time
import types

import Data.Agent.Roles.role_ScriptExec_SYSTEM as role_module


class _FakeSio:
    def __init__(self) -> None:
        self.handlers = {}
        self.emitted = []

    def on(self, event_name):
        def _decorator(handler):
            self.handlers[event_name] = handler
            return handler

        return _decorator

    async def emit(self, event_name, payload):
        self.emitted.append((event_name, dict(payload or {})))


def _make_role(fake_sio: _FakeSio):
    role = role_module.Role.__new__(role_module.Role)
    role.ctx = type(
        "Ctx",
        (),
        {
            "sio": fake_sio,
            "hooks": {
                "http_client": lambda: object(),
                "log_agent": lambda *args, **kwargs: None,
            },
        },
    )()
    role.role_health_label = "Script Execution - SYSTEM"
    role._listener_registered = False
    role._system_queue_locks = {
        "software_uninstall": asyncio.Lock(),
    }
    role._system_job_tasks = set()
    return role


def test_register_events_returns_without_blocking_on_system_job(monkeypatch) -> None:
    fake_sio = _FakeSio()
    role = _make_role(fake_sio)

    monkeypatch.setattr(role_module, "decode_script_bytes", lambda raw, encoding: b"Write-Output 'hello'")
    monkeypatch.setattr(role_module, "verify_and_store_script_signature", lambda *args, **kwargs: True)

    def _slow_powershell(*args, **kwargs):
        time.sleep(0.08)
        return 0, "finished", ""

    monkeypatch.setattr(role_module, "_run_powershell_via_system_task", _slow_powershell)
    monkeypatch.setattr(role_module, "_run_powershell_script_content", _slow_powershell)

    role.register_events()
    handler = fake_sio.handlers["quick_job_run"]

    payload = {
        "job_id": 41,
        "target_hostname": "",
        "script_type": "powershell",
        "run_mode": "system",
        "script_content": "ignored",
        "script_encoding": "utf-8",
        "signature": "valid-signature",
    }

    async def _run_test():
        started = time.perf_counter()
        await asyncio.wait_for(handler(payload), timeout=0.03)
        elapsed = time.perf_counter() - started
        assert elapsed < 0.03
        assert fake_sio.emitted == []
        await asyncio.sleep(0.12)
        assert fake_sio.emitted == [
            (
                "quick_job_result",
                {
                    "job_id": 41,
                    "status": "Success",
                    "stdout": "finished",
                    "stderr": "",
                },
            )
        ]

    asyncio.run(_run_test())


def test_register_events_serializes_back_to_back_software_uninstalls(monkeypatch) -> None:
    fake_sio = _FakeSio()
    role = _make_role(fake_sio)

    monkeypatch.setattr(
        role_module,
        "decode_script_bytes",
        lambda raw, encoding: str(raw or "").encode("utf-8"),
    )
    monkeypatch.setattr(role_module, "verify_and_store_script_signature", lambda *args, **kwargs: True)

    run_log = []

    def _slow_powershell(content, *args, **kwargs):
        run_log.append(("start", content, time.perf_counter()))
        if content == "job-one":
            time.sleep(0.07)
        else:
            time.sleep(0.01)
        run_log.append(("end", content, time.perf_counter()))
        return 0, f"done:{content}", ""

    monkeypatch.setattr(role_module, "_run_powershell_via_system_task", _slow_powershell)
    monkeypatch.setattr(role_module, "_run_powershell_script_content", _slow_powershell)

    role.register_events()
    handler = fake_sio.handlers["quick_job_run"]

    payload_one = {
        "job_id": 51,
        "target_hostname": "",
        "script_type": "powershell",
        "run_mode": "system",
        "script_content": "job-one",
        "script_encoding": "utf-8",
        "signature": "valid-signature",
        "context": {"assembly_source": "device_software_uninstall"},
    }
    payload_two = {
        "job_id": 52,
        "target_hostname": "",
        "script_type": "powershell",
        "run_mode": "system",
        "script_content": "job-two",
        "script_encoding": "utf-8",
        "signature": "valid-signature",
        "context": {"assembly_source": "device_software_uninstall"},
    }

    async def _run_test():
        await handler(payload_one)
        await handler(payload_two)
        await asyncio.sleep(0.16)

    asyncio.run(_run_test())

    emitted_job_ids = [payload["job_id"] for event_name, payload in fake_sio.emitted if event_name == "quick_job_result"]
    assert emitted_job_ids == [51, 52]

    start_one = next(ts for stage, content, ts in run_log if stage == "start" and content == "job-one")
    end_one = next(ts for stage, content, ts in run_log if stage == "end" and content == "job-one")
    start_two = next(ts for stage, content, ts in run_log if stage == "start" and content == "job-two")

    assert start_one < end_one
    assert start_two >= end_one


def test_register_events_does_not_block_generic_system_jobs_behind_uninstall(monkeypatch) -> None:
    fake_sio = _FakeSio()
    role = _make_role(fake_sio)

    monkeypatch.setattr(
        role_module,
        "decode_script_bytes",
        lambda raw, encoding: str(raw or "").encode("utf-8"),
    )
    monkeypatch.setattr(role_module, "verify_and_store_script_signature", lambda *args, **kwargs: True)

    run_log = []

    def _slow_powershell(content, *args, **kwargs):
        run_log.append(("start", content, time.perf_counter()))
        if content == "uninstall-job":
            time.sleep(0.08)
        else:
            time.sleep(0.01)
        run_log.append(("end", content, time.perf_counter()))
        return 0, f"done:{content}", ""

    monkeypatch.setattr(role_module, "_run_powershell_via_system_task", _slow_powershell)
    monkeypatch.setattr(role_module, "_run_powershell_script_content", _slow_powershell)

    role.register_events()
    handler = fake_sio.handlers["quick_job_run"]

    uninstall_payload = {
        "job_id": 61,
        "target_hostname": "",
        "script_type": "powershell",
        "run_mode": "system",
        "script_content": "uninstall-job",
        "script_encoding": "utf-8",
        "signature": "valid-signature",
        "context": {"assembly_source": "device_software_uninstall"},
    }
    generic_payload = {
        "job_id": 62,
        "target_hostname": "",
        "script_type": "powershell",
        "run_mode": "system",
        "script_content": "generic-job",
        "script_encoding": "utf-8",
        "signature": "valid-signature",
    }

    async def _run_test():
        await handler(uninstall_payload)
        await handler(generic_payload)
        await asyncio.sleep(0.16)

    asyncio.run(_run_test())

    emitted_job_ids = [payload["job_id"] for event_name, payload in fake_sio.emitted if event_name == "quick_job_result"]
    assert sorted(emitted_job_ids) == [61, 62]

    uninstall_start = next(ts for stage, content, ts in run_log if stage == "start" and content == "uninstall-job")
    uninstall_end = next(ts for stage, content, ts in run_log if stage == "end" and content == "uninstall-job")
    generic_start = next(ts for stage, content, ts in run_log if stage == "start" and content == "generic-job")

    assert uninstall_start < uninstall_end
    assert generic_start < uninstall_end
