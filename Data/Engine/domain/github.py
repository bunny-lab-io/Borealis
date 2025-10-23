"""Domain types for GitHub integrations."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Dict, Optional


@dataclass(frozen=True, slots=True)
class GitHubRepoRef:
    """Identify a GitHub repository and branch."""

    owner: str
    name: str
    branch: str

    @property
    def full_name(self) -> str:
        return f"{self.owner}/{self.name}".strip("/")

    @classmethod
    def parse(cls, owner_repo: str, branch: str) -> "GitHubRepoRef":
        owner_repo = (owner_repo or "").strip()
        if "/" not in owner_repo:
            raise ValueError("repo must be in the form owner/name")
        owner, repo = owner_repo.split("/", 1)
        return cls(owner=owner.strip(), name=repo.strip(), branch=(branch or "main").strip())


@dataclass(frozen=True, slots=True)
class RepoHeadSnapshot:
    """Snapshot describing the current head of a repository branch."""

    repository: GitHubRepoRef
    sha: Optional[str]
    cached: bool
    age_seconds: Optional[float]
    source: str
    error: Optional[str]

    def to_dict(self) -> Dict[str, object]:
        return {
            "repo": self.repository.full_name,
            "branch": self.repository.branch,
            "sha": self.sha,
            "cached": self.cached,
            "age_seconds": self.age_seconds,
            "source": self.source,
            "error": self.error,
        }


@dataclass(frozen=True, slots=True)
class GitHubRateLimit:
    """Subset of rate limit details returned by the GitHub API."""

    limit: Optional[int]
    remaining: Optional[int]
    reset_epoch: Optional[int]
    used: Optional[int]

    def to_dict(self) -> Dict[str, Optional[int]]:
        return {
            "limit": self.limit,
            "remaining": self.remaining,
            "reset": self.reset_epoch,
            "used": self.used,
        }


@dataclass(frozen=True, slots=True)
class GitHubTokenStatus:
    """Describe the verification result for a GitHub access token."""

    has_token: bool
    valid: bool
    status: str
    message: str
    rate_limit: Optional[GitHubRateLimit]
    error: Optional[str] = None

    def to_dict(self) -> Dict[str, object]:
        payload: Dict[str, object] = {
            "has_token": self.has_token,
            "valid": self.valid,
            "status": self.status,
            "message": self.message,
            "error": self.error,
        }
        if self.rate_limit is not None:
            payload["rate_limit"] = self.rate_limit.to_dict()
        else:
            payload["rate_limit"] = None
        return payload


__all__ = [
    "GitHubRateLimit",
    "GitHubRepoRef",
    "GitHubTokenStatus",
    "RepoHeadSnapshot",
]

