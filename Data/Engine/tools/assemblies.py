# ======================================================
# Data\Engine\tools\assemblies.py
# Description: CLI helper for assembly maintenance tasks, including official domain synchronisation.
#
# API Endpoints (if applicable): None
# ======================================================

"""Assembly maintenance CLI."""

from __future__ import annotations

import argparse
import logging
import sys
from pathlib import Path
from typing import Optional

from Data.Engine.assembly_management.databases import AssemblyDatabaseManager
from Data.Engine.assembly_management.models import AssemblyDomain
from Data.Engine.assembly_management.payloads import PayloadManager
from Data.Engine.assembly_management.sync import sync_official_domain


logger = logging.getLogger("borealis.assembly.cli")


def _default_staging_root() -> Path:
    return Path(__file__).resolve().parents[3] / "Data" / "Engine" / "Assemblies"


def _default_runtime_root() -> Path:
    return Path(__file__).resolve().parents[3] / "Engine" / "Assemblies"


def cmd_sync_official(*, staging_root: Optional[Path], runtime_root: Optional[Path]) -> int:
    staging = staging_root or _default_staging_root()
    runtime = runtime_root or _default_runtime_root()
    staging.mkdir(parents=True, exist_ok=True)
    runtime.mkdir(parents=True, exist_ok=True)

    logger.info("Starting official assembly sync.")
    db_manager = AssemblyDatabaseManager(staging_root=staging, runtime_root=runtime, logger=logger)
    db_manager.initialise()
    payload_manager = PayloadManager(staging_root=staging / "Payloads", runtime_root=runtime / "Payloads", logger=logger)

    sync_official_domain(db_manager, payload_manager, staging, logger=logger)
    records = db_manager.load_all(AssemblyDomain.OFFICIAL)
    source_count = sum(1 for path in staging.rglob("*.json") if path.is_file())

    logger.info(
        "Official sync complete: %s records persisted (staging sources=%s).",
        len(records),
        source_count,
    )
    print(f"Official assemblies synced: records={len(records)} staged_json={source_count}")
    if len(records) != source_count:
        print("warning: record count does not match JSON source file count", file=sys.stderr)
        return 1
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Borealis assembly maintenance CLI.")
    subparsers = parser.add_subparsers(dest="command")

    sync_parser = subparsers.add_parser("sync-official", help="Rebuild the official assembly database from staged JSON sources.")
    sync_parser.add_argument("--staging-root", type=Path, default=None, help="Override the staging assemblies directory.")
    sync_parser.add_argument("--runtime-root", type=Path, default=None, help="Override the runtime assemblies directory.")

    return parser


def main(argv: Optional[list[str]] = None) -> int:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s-%(levelname)s: %(message)s")
    parser = build_parser()
    args = parser.parse_args(argv)

    if args.command == "sync-official":
        return cmd_sync_official(staging_root=args.staging_root, runtime_root=args.runtime_root)

    parser.print_help()
    return 1


if __name__ == "__main__":  # pragma: no cover - CLI entrypoint
    raise SystemExit(main())

