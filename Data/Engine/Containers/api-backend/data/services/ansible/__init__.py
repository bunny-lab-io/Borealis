# ======================================================
# Data\Engine\services\ansible\__init__.py
# Description: Exposes Engine-local Ansible execution helpers.
#
# API Endpoints (if applicable): None
# ======================================================

"""Ansible service helpers for the Borealis Engine runtime."""

__all__ = ["EngineAnsibleRunner"]


def __getattr__(name):
    if name == "EngineAnsibleRunner":
        from .runner import EngineAnsibleRunner

        return EngineAnsibleRunner
    raise AttributeError(name)
