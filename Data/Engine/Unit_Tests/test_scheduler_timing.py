# ======================================================
# Data\Engine\Unit_Tests\test_scheduler_timing.py
# Description: Validates the Engine job scheduler's interval calculations to
#              ensure jobs are queued on the expected cadence.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

import datetime as dt
from pathlib import Path
from typing import Callable, List, Tuple

from flask import Flask

from Data.Engine.services.API.scheduled_jobs import job_scheduler


class _DummySocketIO:
    def __init__(self) -> None:
        self.started_tasks: List[Tuple[Callable, tuple, dict]] = []

    def start_background_task(self, target: Callable, *args, **kwargs):
        # The scheduler calls into Socket.IO to spawn the background loop.
        # Tests only verify the scheduling math, so we capture the request
        # without launching a greenlet/thread.
        self.started_tasks.append((target, args, kwargs))
        return None

    def emit(self, *_args, **_kwargs):
        return None


def _make_scheduler(tmp_path: Path) -> job_scheduler.JobScheduler:
    app = Flask(__name__)
    db_path = tmp_path / "scheduler.sqlite3"
    return job_scheduler.JobScheduler(app, _DummySocketIO(), str(db_path))


def _ts(year: int, month: int, day: int, hour: int = 0, minute: int = 0) -> int:
    return int(dt.datetime(year, month, day, hour, minute, tzinfo=dt.timezone.utc).timestamp())


def test_immediate_schedule_runs_once(tmp_path):
    scheduler = _make_scheduler(tmp_path)
    now = _ts(2024, 3, 1, 12, 0)
    assert scheduler._compute_next_run("immediately", None, None, now) == now
    assert scheduler._compute_next_run("immediately", None, now, now) is None


def test_hourly_schedule_advances_increments(tmp_path):
    scheduler = _make_scheduler(tmp_path)
    start = _ts(2024, 3, 1, 9, 0)
    now = _ts(2024, 3, 1, 9, 30)
    assert scheduler._compute_next_run("every_hour", start, None, now) == start
    next_candidate = scheduler._compute_next_run("every_hour", start, start, now)
    assert next_candidate == _ts(2024, 3, 1, 10, 0)


def test_daily_schedule_rolls_forward(tmp_path):
    scheduler = _make_scheduler(tmp_path)
    start = _ts(2024, 3, 1, 6, 15)
    after_two_days = _ts(2024, 3, 3, 5, 0)
    next_run = scheduler._compute_next_run("daily", start, start, after_two_days)
    assert next_run == _ts(2024, 3, 2, 6, 15)


def test_monthly_schedule_handles_late_month(tmp_path):
    scheduler = _make_scheduler(tmp_path)
    start = _ts(2024, 1, 31, 8, 0)
    after_two_months = _ts(2024, 3, 5, 8, 0)
    next_run = scheduler._compute_next_run("monthly", start, start, after_two_months)
    # February 2024 has 29 days, so the scheduler should clamp to Feb 29th.
    assert next_run == _ts(2024, 2, 29, 8, 0)


def test_yearly_schedule_rolls_forward(tmp_path):
    scheduler = _make_scheduler(tmp_path)
    start = _ts(2023, 2, 28, 0, 0)
    after_two_years = _ts(2025, 3, 1, 0, 0)
    next_run = scheduler._compute_next_run("yearly", start, start, after_two_years)
    assert next_run == _ts(2024, 2, 28, 0, 0)
