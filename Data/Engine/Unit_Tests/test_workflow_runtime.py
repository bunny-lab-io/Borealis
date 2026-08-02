# ======================================================
# Data\Engine\Unit_Tests\test_workflow_runtime.py
# Description: Validates workflow runtime hardening entrypoints.
#
# API Endpoints (if applicable): /api/workflows/runs
# ======================================================

from __future__ import annotations

from pathlib import Path


PROJECT_ROOT = Path(__file__).resolve().parents[3]
WORKFLOW_RUNTIME = (
    PROJECT_ROOT / "Data/Engine/Containers/api-backend/cmd/api-backend/workflows_runtime.go"
)


def test_workflow_runtime_uses_update_column_allowlists() -> None:
    source = WORKFLOW_RUNTIME.read_text(encoding="utf-8")

    assert "workflowRunUpdateColumns" in source
    assert "workflowChildJobUpdateColumns" in source
    assert "workflowUpdateSQL(" in source
    assert '"UPDATE " + table + " SET " + strings.Join(sets, ", ")' in source
    assert "if !ok" in source
