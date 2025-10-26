"""GitHub REST API integration with caching support."""

from __future__ import annotations

import json
import logging
import threading
import time
import os
from pathlib import Path
from typing import Dict, Optional

from Data.Engine.domain.github import GitHubRepoRef, GitHubTokenStatus, RepoHeadSnapshot, GitHubRateLimit

try:  # pragma: no cover - optional dependency guard
    import requests
    from requests import Response
except Exception:  # pragma: no cover - fallback when requests is unavailable
    requests = None  # type: ignore[assignment]
    Response = object  # type: ignore[misc,assignment]

__all__ = ["GitHubArtifactProvider"]


class GitHubArtifactProvider:
    """Resolve repository heads and token metadata from the GitHub API."""

    def __init__(
        self,
        *,
        cache_file: Path,
        default_repo: str,
        default_branch: str,
        refresh_interval: int,
        allow_proxies: Optional[bool] = None,
        logger: Optional[logging.Logger] = None,
    ) -> None:
        self._cache_file = cache_file
        self._default_repo = default_repo
        self._default_branch = default_branch
        self._refresh_interval = max(30, min(refresh_interval, 3600))
        self._log = logger or logging.getLogger("borealis.engine.integrations.github")
        self._token: Optional[str] = None
        self._cache_lock = threading.Lock()
        self._cache: Dict[str, Dict[str, float | str]] = {}
        self._worker: Optional[threading.Thread] = None
        if allow_proxies is None:
            allow_proxies = _env_flag("BOREALIS_GITHUB_ALLOW_PROXIES", default=False)
        self._allow_proxies = allow_proxies
        self._hydrate_cache_from_disk()

    def set_token(self, token: Optional[str]) -> None:
        self._token = (token or "").strip() or None

    @property
    def default_repo(self) -> str:
        return self._default_repo

    @property
    def default_branch(self) -> str:
        return self._default_branch

    @property
    def refresh_interval(self) -> int:
        return self._refresh_interval

    def fetch_repo_head(
        self,
        repo: GitHubRepoRef,
        *,
        ttl_seconds: int,
        force_refresh: bool = False,
    ) -> RepoHeadSnapshot:
        key = f"{repo.full_name}:{repo.branch}"
        now = time.time()

        cached_entry = None
        with self._cache_lock:
            cached_entry = self._cache.get(key, {}).copy()

        cached_sha = (cached_entry.get("sha") if cached_entry else None)  # type: ignore[assignment]
        cached_ts = cached_entry.get("timestamp") if cached_entry else None  # type: ignore[assignment]
        cached_age = None
        if isinstance(cached_ts, (int, float)):
            cached_age = max(0.0, now - float(cached_ts))

        ttl = max(30, min(ttl_seconds, 3600))
        if cached_sha and not force_refresh and cached_age is not None and cached_age < ttl:
            return RepoHeadSnapshot(
                repository=repo,
                sha=str(cached_sha),
                cached=True,
                age_seconds=cached_age,
                source="cache",
                error=None,
            )

        if requests is None:
            return RepoHeadSnapshot(
                repository=repo,
                sha=str(cached_sha) if cached_sha else None,
                cached=bool(cached_sha),
                age_seconds=cached_age,
                source="unavailable",
                error="requests library not available",
            )

        headers = {
            "Accept": "application/vnd.github+json",
            "User-Agent": "Borealis-Engine",
        }
        if self._token:
            headers["Authorization"] = f"Bearer {self._token}"

        url = f"https://api.github.com/repos/{repo.full_name}/branches/{repo.branch}"
        error: Optional[str] = None
        sha: Optional[str] = None

        try:
            response: Response = self._http_get(url, headers=headers, timeout=20)
            if response.status_code == 200:
                data = response.json()
                sha = (data.get("commit", {}).get("sha") or "").strip()  # type: ignore[assignment]
            else:
                error = f"GitHub REST API repo head lookup failed: HTTP {response.status_code} {response.text[:200]}"
        except Exception as exc:  # pragma: no cover - defensive logging
            error = f"GitHub REST API repo head lookup raised: {exc}"

        if sha:
            payload = {"sha": sha, "timestamp": now}
            with self._cache_lock:
                self._cache[key] = payload
            self._persist_cache()
            return RepoHeadSnapshot(
                repository=repo,
                sha=sha,
                cached=False,
                age_seconds=0.0,
                source="github",
                error=None,
            )

        if error:
            self._log.warning("repo-head-lookup failure repo=%s branch=%s error=%s", repo.full_name, repo.branch, error)

        return RepoHeadSnapshot(
            repository=repo,
            sha=str(cached_sha) if cached_sha else None,
            cached=bool(cached_sha),
            age_seconds=cached_age,
            source="cache-stale" if cached_sha else "github",
            error=error or ("using cached value" if cached_sha else "unable to resolve repository head"),
        )

    def refresh_default_repo_head(self, *, force: bool = False) -> RepoHeadSnapshot:
        repo = GitHubRepoRef.parse(self._default_repo, self._default_branch)
        return self.fetch_repo_head(repo, ttl_seconds=self._refresh_interval, force_refresh=force)

    def verify_token(self, token: Optional[str]) -> GitHubTokenStatus:
        token = (token or "").strip()
        if not token:
            return GitHubTokenStatus(
                has_token=False,
                valid=False,
                status="missing",
                message="API Token Not Configured",
                rate_limit=None,
                error=None,
            )

        if requests is None:
            return GitHubTokenStatus(
                has_token=True,
                valid=False,
                status="unknown",
                message="requests library not available",
                rate_limit=None,
                error="requests library not available",
            )

        headers = {
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {token}",
            "User-Agent": "Borealis-Engine",
        }
        try:
            response: Response = self._http_get(
                "https://api.github.com/rate_limit",
                headers=headers,
                timeout=10,
            )
        except Exception as exc:  # pragma: no cover - defensive logging
            message = f"GitHub token verification raised: {exc}"
            self._log.warning("github-token-verify error=%s", message)
            return GitHubTokenStatus(
                has_token=True,
                valid=False,
                status="error",
                message="API Token Invalid",
                rate_limit=None,
                error=message,
            )

        if response.status_code != 200:
            message = f"GitHub API error (HTTP {response.status_code})"
            self._log.warning("github-token-verify http_status=%s", response.status_code)
            return GitHubTokenStatus(
                has_token=True,
                valid=False,
                status="error",
                message="API Token Invalid",
                rate_limit=None,
                error=message,
            )

        data = response.json()
        core = (data.get("resources", {}).get("core", {}) if isinstance(data, dict) else {})
        rate_limit = GitHubRateLimit(
            limit=_safe_int(core.get("limit")),
            remaining=_safe_int(core.get("remaining")),
            reset_epoch=_safe_int(core.get("reset")),
            used=_safe_int(core.get("used")),
        )

        message = "API Token Valid" if rate_limit.remaining is not None else "API Token Verified"
        return GitHubTokenStatus(
            has_token=True,
            valid=True,
            status="valid",
            message=message,
            rate_limit=rate_limit,
            error=None,
        )

    def start_background_refresh(self) -> None:
        if self._worker and self._worker.is_alive():  # pragma: no cover - guard
            return

        def _loop() -> None:
            interval = max(30, self._refresh_interval)
            while True:
                try:
                    self.refresh_default_repo_head(force=True)
                except Exception as exc:  # pragma: no cover - defensive logging
                    self._log.warning("default-repo-refresh failure: %s", exc)
                time.sleep(interval)

        self._worker = threading.Thread(target=_loop, name="github-repo-refresh", daemon=True)
        self._worker.start()

    def _hydrate_cache_from_disk(self) -> None:
        path = self._cache_file
        try:
            if not path.exists():
                return
            data = json.loads(path.read_text(encoding="utf-8"))
            if isinstance(data, dict):
                with self._cache_lock:
                    self._cache = {
                        key: value
                        for key, value in data.items()
                        if isinstance(value, dict) and "sha" in value and "timestamp" in value
                    }
        except Exception as exc:  # pragma: no cover - defensive logging
            self._log.warning("failed to load repo cache: %s", exc)

    def _persist_cache(self) -> None:
        path = self._cache_file
        try:
            path.parent.mkdir(parents=True, exist_ok=True)
            payload = json.dumps(self._cache, ensure_ascii=False)
            tmp = path.with_suffix(".tmp")
            tmp.write_text(payload, encoding="utf-8")
            tmp.replace(path)
        except Exception as exc:  # pragma: no cover - defensive logging
            self._log.warning("failed to persist repo cache: %s", exc)

    def _http_get(self, url: str, *, headers: Dict[str, str], timeout: float) -> Response:
        if requests is None:  # pragma: no cover - defensive guard
            raise RuntimeError("requests library not available")

        kwargs: Dict[str, object] = {"headers": headers, "timeout": timeout}
        if not self._allow_proxies:
            kwargs["proxies"] = {}
        return requests.get(url, **kwargs)


def _safe_int(value: object) -> Optional[int]:
    try:
        return int(value)
    except Exception:
        return None


def _env_flag(name: str, *, default: bool) -> bool:
    raw = os.getenv(name)
    if raw is None:
        return default
    return raw.strip().lower() in {"1", "true", "yes", "on"}

