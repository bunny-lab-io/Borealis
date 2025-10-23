"""External system adapters for the Borealis Engine."""

from __future__ import annotations

from .github.artifact_provider import GitHubArtifactProvider

__all__ = ["GitHubArtifactProvider"]
