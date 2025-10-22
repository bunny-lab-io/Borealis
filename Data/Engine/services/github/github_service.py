"""GitHub service layer bridging repositories and integrations."""

from __future__ import annotations

import logging
import time
from dataclasses import dataclass
from typing import Callable, Optional

from Data.Engine.domain.github import GitHubRepoRef, GitHubTokenStatus, RepoHeadSnapshot
from Data.Engine.integrations.github import GitHubArtifactProvider
from Data.Engine.repositories.sqlite.github_repository import SQLiteGitHubRepository

__all__ = ["GitHubService", "GitHubTokenPayload"]


@dataclass(frozen=True, slots=True)
class GitHubTokenPayload:
    token: Optional[str]
    status: GitHubTokenStatus
    checked_at: int

    def to_dict(self) -> dict:
        payload = self.status.to_dict()
        payload.update(
            {
                "token": self.token or "",
                "checked_at": self.checked_at,
            }
        )
        return payload


class GitHubService:
    """Coordinate GitHub caching, verification, and persistence."""

    def __init__(
        self,
        *,
        repository: SQLiteGitHubRepository,
        provider: GitHubArtifactProvider,
        logger: Optional[logging.Logger] = None,
        clock: Optional[Callable[[], float]] = None,
    ) -> None:
        self._repository = repository
        self._provider = provider
        self._log = logger or logging.getLogger("borealis.engine.services.github")
        self._clock = clock or time.time
        self._token_cache: Optional[str] = None
        self._token_loaded_at: float = 0.0

        initial_token = self._repository.load_token()
        self._apply_token(initial_token)

    def get_repo_head(
        self,
        owner_repo: Optional[str],
        branch: Optional[str],
        *,
        ttl_seconds: int,
        force_refresh: bool = False,
    ) -> RepoHeadSnapshot:
        repo_str = (owner_repo or self._provider.default_repo).strip()
        branch_name = (branch or self._provider.default_branch).strip()
        repo = GitHubRepoRef.parse(repo_str, branch_name)
        ttl = max(30, min(ttl_seconds, 3600))
        return self._provider.fetch_repo_head(repo, ttl_seconds=ttl, force_refresh=force_refresh)

    def refresh_default_repo(self, *, force: bool = False) -> RepoHeadSnapshot:
        return self._provider.refresh_default_repo_head(force=force)

    def get_token_status(self, *, force_refresh: bool = False) -> GitHubTokenPayload:
        token = self._load_token(force_refresh=force_refresh)
        status = self._provider.verify_token(token)
        return GitHubTokenPayload(token=token, status=status, checked_at=int(self._clock()))

    def update_token(self, token: Optional[str]) -> GitHubTokenPayload:
        normalized = (token or "").strip()
        self._repository.store_token(normalized)
        self._apply_token(normalized)
        status = self._provider.verify_token(normalized)
        self._provider.start_background_refresh()
        self._log.info("github-token updated valid=%s", status.valid)
        return GitHubTokenPayload(token=normalized or None, status=status, checked_at=int(self._clock()))

    def start_background_refresh(self) -> None:
        self._provider.start_background_refresh()

    @property
    def default_refresh_interval(self) -> int:
        return self._provider.refresh_interval

    def _load_token(self, *, force_refresh: bool = False) -> Optional[str]:
        now = self._clock()
        if not force_refresh and self._token_cache is not None and (now - self._token_loaded_at) < 15.0:
            return self._token_cache

        token = self._repository.load_token()
        self._apply_token(token)
        return token

    def _apply_token(self, token: Optional[str]) -> None:
        self._token_cache = (token or "").strip() or None
        self._token_loaded_at = self._clock()
        self._provider.set_token(self._token_cache)

