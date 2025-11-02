# ======================================================
# Data\Engine\assembly_management\__init__.py
# Description: Exposes assembly persistence helpers for the Borealis Engine.
#
# API Endpoints (if applicable): None
# ======================================================

"""Assembly persistence package for the Borealis Engine."""

from __future__ import annotations

from .bootstrap import AssemblyCache, initialise_assembly_runtime
from .databases import AssemblyDatabaseManager
from .models import AssemblyDomain, AssemblyRecord, PayloadDescriptor, PayloadType
from .payloads import PayloadManager

__all__ = [
    "AssemblyCache",
    "AssemblyDatabaseManager",
    "AssemblyDomain",
    "AssemblyRecord",
    "PayloadDescriptor",
    "PayloadType",
    "PayloadManager",
    "initialise_assembly_runtime",
]

