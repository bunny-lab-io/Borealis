"""Task scheduler container healthcheck."""

from __future__ import annotations

import os
import sys

from Data.Engine.config import load_runtime_config
from Data.Engine.db import dbapi as sqlite3
from Data.Engine.services.task_scheduler.queue import ensure_task_scheduler_tables


def main() -> int:
    try:
        settings = load_runtime_config()
        conn = sqlite3.connect(settings.database_url, timeout=5)
        try:
            ensure_task_scheduler_tables(conn)
            conn.commit()
        finally:
            conn.close()
    except Exception as exc:
        sys.stderr.write(f"job-scheduler unhealthy: {exc}\n")
        return 1
    if os.getppid() <= 1:
        return 0
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
