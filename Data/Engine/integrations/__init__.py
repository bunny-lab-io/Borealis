# ======================================================
# Data\Engine\integrations\__init__.py
# Description: Integration namespace exposing helper utilities for external service adapters.
#
# API Endpoints (if applicable): None
# ======================================================

"""Integration namespace for the Borealis Engine runtime."""

from .github import GitHubIntegration

__all__ = ["GitHubIntegration"]
