# ======================================================
# Data\Engine\Unit_Tests\conftest.py
# Description: Pytest fixtures for retained Python worker/helper tests.
#
# API Endpoints (if applicable): None
# ======================================================

from __future__ import annotations

from dataclasses import dataclass
import os
from pathlib import Path
import tempfile
from typing import Iterator

import pytest

os.environ.setdefault(
    "BOREALIS_ENGINE_CERT_ROOT",
    tempfile.mkdtemp(prefix="borealis-engine-test-certs-"),
)
os.environ.setdefault(
    "BOREALIS_ENGINE_SECRET_PATH",
    str(Path(tempfile.mkdtemp(prefix="borealis-engine-test-secret-")) / "engine_secret.txt"),
)


@dataclass
class EngineTestHarness:
    db_path: Path


@pytest.fixture()
def engine_harness(tmp_path: Path) -> Iterator[EngineTestHarness]:
    db_path = tmp_path / "database" / "engine.sqlite3"
    db_path.parent.mkdir(parents=True, exist_ok=True)
    yield EngineTestHarness(db_path=db_path)
