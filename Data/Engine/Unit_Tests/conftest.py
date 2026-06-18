# ======================================================
# Data\Engine\Unit_Tests\conftest.py
# Description: Pytest fixtures for retained Python worker/helper tests.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import Iterator

import pytest


@dataclass
class EngineTestHarness:
    db_path: Path


@pytest.fixture()
def engine_harness(tmp_path: Path) -> Iterator[EngineTestHarness]:
    db_path = tmp_path / "database" / "engine.sqlite3"
    db_path.parent.mkdir(parents=True, exist_ok=True)
    yield EngineTestHarness(db_path=db_path)
