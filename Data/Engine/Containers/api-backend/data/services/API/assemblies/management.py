# ======================================================
# Data\Engine\services\API\assemblies\management.py
# Description: Shared assembly API service helpers retained for Aurora catalog fallback routes.
#
# API Endpoints (if applicable): None
# ======================================================

"""Shared assembly API helpers backed by AssemblyCache."""

from __future__ import annotations

import logging
from pathlib import Path
from typing import TYPE_CHECKING, Any, Dict, Optional, Tuple

from ....assembly_management.models import AssemblyDomain
from ...assemblies.official_catalog import OfficialAssemblyCatalogService
from ...assemblies.service import AssemblyRuntimeService
from ...auth import RequestAuthContext

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import EngineServiceAdapters


def _coerce_refresh_seconds(value: Any, default: int = 300) -> int:
    try:
        parsed = int(value)
    except Exception:
        return default
    if parsed < 30:
        return 30
    if parsed > 86400:
        return 86400
    return parsed


def _coerce_bool(value: Any) -> bool:
    if value is None:
        return False
    text = str(value).strip().lower()
    return text in {"1", "true", "yes", "on", "refresh", "force"}


class AssemblyAPIService:
    """Facilitates retained Aurora catalog routes with auth and audit helpers."""

    def __init__(self, app, adapters: "EngineServiceAdapters") -> None:
        self.app = app
        self.adapters = adapters
        self.logger = adapters.context.logger or logging.getLogger(__name__)
        cache = adapters.context.assembly_cache
        if cache is None:
            raise RuntimeError("Assembly cache not initialised; ensure Engine bootstrap executed.")
        self.runtime = AssemblyRuntimeService(cache, logger=self.logger)
        catalog_root_raw = adapters.config.get("official_assemblies_root") or adapters.config.get("OFFICIAL_ASSEMBLIES_ROOT")
        bundled_root = Path(str(catalog_root_raw)).expanduser() if catalog_root_raw else None
        checkout_root_raw = (
            adapters.config.get("official_assemblies_checkout_root")
            or adapters.config.get("OFFICIAL_ASSEMBLIES_CHECKOUT_ROOT")
        )
        checkout_root = Path(str(checkout_root_raw)).expanduser() if checkout_root_raw else None
        self.catalog = OfficialAssemblyCatalogService(
            cache=cache,
            database_manager=cache.database_manager,
            logger=self.logger,
            github_integration=adapters.github_integration,
            bundled_root=bundled_root,
            checkout_root=checkout_root,
            repo_url=str(
                adapters.config.get("official_assemblies_repo_url")
                or adapters.config.get("OFFICIAL_ASSEMBLIES_REPO_URL")
                or "https://github.com/bunny-lab-io/Aurora"
            ).strip()
            or "https://github.com/bunny-lab-io/Aurora",
            repo_git_url=str(
                adapters.config.get("official_assemblies_repo_git_url")
                or adapters.config.get("OFFICIAL_ASSEMBLIES_REPO_GIT_URL")
                or "https://github.com/bunny-lab-io/Aurora.git"
            ).strip()
            or "https://github.com/bunny-lab-io/Aurora.git",
            repo_ref=str(
                adapters.config.get("official_assemblies_repo_ref")
                or adapters.config.get("OFFICIAL_ASSEMBLIES_REPO_REF")
                or "main"
            ).strip()
            or "main",
            manifest_url=str(
                adapters.config.get("official_assemblies_manifest_url")
                or adapters.config.get("OFFICIAL_ASSEMBLIES_MANIFEST_URL")
                or ""
            ).strip(),
            refresh_seconds=_coerce_refresh_seconds(
                adapters.config.get("official_assemblies_refresh_seconds")
                or adapters.config.get("OFFICIAL_ASSEMBLIES_REFRESH_SECONDS")
            ),
        )
        self.service_log = adapters.service_log
        self.auth = RequestAuthContext(
            app=app,
            dev_mode_manager=adapters.dev_mode_manager,
            config=adapters.config,
            logger=self.logger,
            db_conn_factory=adapters.db_conn_factory,
            aegis_cipher_service=adapters.aegis_cipher_service,
        )

    def require_user(self) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
        return self.auth.require_user()

    def require_admin(
        self,
        *,
        dev_mode_required: bool = False,
        user: Optional[Dict[str, Any]] = None,
    ) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
        actor = user
        if actor is None:
            actor, error = self.require_user()
            if error:
                detail = error[0].get("message") or error[0].get("error") or "authentication required"
                self._audit(user=None, action="admin_check", status="denied", detail=detail)
                return actor, error

        if not RequestAuthContext.is_admin(actor):
            payload = {
                "error": "forbidden",
                "message": "Administrator permissions are required for this action.",
            }
            self._audit(user=actor, action="admin_check", status="denied", detail=payload["message"])
            return actor, (payload, 403)

        if dev_mode_required and not self.auth.dev_mode_enabled(user=actor):
            payload = {
                "error": "dev_mode_required",
                "message": "Enable Dev Mode from the Assemblies admin controls to continue.",
            }
            self._audit(user=actor, action="dev_mode_check", status="denied", detail=payload["message"])
            return actor, (payload, 403)

        return actor, None

    def require_mutation_for_domain(
        self,
        domain: AssemblyDomain,
    ) -> Tuple[Optional[Dict[str, Any]], Optional[Tuple[Dict[str, Any], int]]]:
        user, error = self.require_user()
        if error:
            detail = error[0].get("message") or error[0].get("error") or "authentication required"
            self._audit(user=None, action="mutation_check", domain=domain, status="denied", detail=detail)
            return user, error
        if domain == AssemblyDomain.USER:
            return user, None
        _, admin_error = self.require_admin(dev_mode_required=True, user=user)
        if admin_error:
            return user, admin_error
        return user, None

    def _audit(
        self,
        *,
        user: Optional[Dict[str, Any]],
        action: str,
        domain: Optional[AssemblyDomain] = None,
        assembly_guid: Optional[str] = None,
        status: str = "success",
        detail: Optional[str] = None,
    ) -> None:
        actor = user or {}
        username = (actor.get("username") or "").strip() or "anonymous"
        role = (actor.get("role") or "").strip() or "unknown"
        domain_value = domain.value if isinstance(domain, AssemblyDomain) else (domain or "n/a")
        dev_mode_flag = self.auth.dev_mode_enabled(user=user) if user else False
        parts = [
            f"user={username}",
            f"role={role}",
            f"action={action}",
            f"domain={domain_value}",
            f"status={status}",
            f"dev_mode={'true' if dev_mode_flag else 'false'}",
        ]
        if assembly_guid:
            parts.append(f"assembly={assembly_guid}")
        if detail:
            parts.append(f"detail={detail}")
        message = " ".join(parts)
        self.logger.info("Assemblies audit - %s", message)
        try:
            self.service_log("assemblies", message, scope="ADMIN")
        except Exception:  # pragma: no cover - logging safeguard
            self.logger.debug("Failed to write assemblies service log entry.", exc_info=True)

    @staticmethod
    def parse_domain(value: Any) -> Optional[AssemblyDomain]:
        if value is None:
            return None
        candidate = str(value).strip().lower()
        for domain in AssemblyDomain:
            if domain.value == candidate:
                return domain
        return None
