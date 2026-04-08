# ======================================================
# Data\Engine\services\API\assemblies\management.py
# Description: Assembly REST API routes backed by AssemblyCache for multi-domain persistence.
#
# API Endpoints (if applicable):
# - GET /api/assemblies (Token Authenticated) - Lists assemblies by domain/type with summary fields.
# - GET /api/assemblies/<assembly_guid> (Token Authenticated) - Returns assembly summary and payload.
# - POST /api/assemblies (Token Authenticated) - Creates a new assembly within the allowed domain.
# - PUT /api/assemblies/<assembly_guid> (Token Authenticated) - Updates an existing assembly and stages persistence.
# - DELETE /api/assemblies/<assembly_guid> (Token Authenticated) - Marks an assembly for deletion.
# - POST /api/assemblies/<assembly_guid>/clone (Token Authenticated (Admin+Dev Mode for non-user domains)) - Clones an assembly into a target domain.
# - POST /api/assemblies/dev-mode/switch (Token Authenticated (Admin)) - Enables or disables Dev Mode overrides for the current session.
# - POST /api/assemblies/dev-mode/write (Token Authenticated (Admin+Dev Mode)) - Flushes queued assembly writes immediately.
# - POST /api/assemblies/import (Token Authenticated (Domain write permissions)) - Imports a legacy assembly JSON document into the selected domain.
# - GET /api/assemblies/<assembly_guid>/export (Token Authenticated) - Exports an assembly as JSON.
# - POST /api/assemblies/<assembly_guid>/official-update (Token Authenticated (Admin)) - Updates one official Aurora assembly.
# - POST /api/assemblies/official/update-all (Token Authenticated (Admin)) - Syncs official Aurora assemblies, including newly added catalog items.
# ======================================================

"""Assembly CRUD REST endpoints backed by AssemblyCache."""

from __future__ import annotations

import logging
from pathlib import Path
from typing import TYPE_CHECKING, Any, Dict, Optional, Tuple

from flask import Blueprint, jsonify, request

from ....assembly_management.models import AssemblyDomain
from ...assemblies.official_catalog import OfficialAssemblyCatalogService
from ...assemblies.serialization import AssemblySerializationError
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
    """Facilitates assembly API routes with authentication and permission checks."""

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

    # ------------------------------------------------------------------
    # Authentication helpers
    # ------------------------------------------------------------------
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

    # ------------------------------------------------------------------
    # Audit helpers
    # ------------------------------------------------------------------
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

    # ------------------------------------------------------------------
    # Domain helpers
    # ------------------------------------------------------------------
    @staticmethod
    def parse_domain(value: Any) -> Optional[AssemblyDomain]:
        if value is None:
            return None
        candidate = str(value).strip().lower()
        for domain in AssemblyDomain:
            if domain.value == candidate:
                return domain
        return None


def register_assemblies(app, adapters: "EngineServiceAdapters") -> None:
    """Register assembly CRUD endpoints on the Flask app."""

    service = AssemblyAPIService(app, adapters)
    blueprint = Blueprint("assemblies", __name__, url_prefix="/api/assemblies")

    # ------------------------------------------------------------------
    # Collections
    # ------------------------------------------------------------------
    @blueprint.route("", methods=["GET"])
    def list_assemblies():
        user, error = service.require_user()
        if error:
            return jsonify(error[0]), error[1]

        domain = request.args.get("domain")
        assembly_type = request.args.get("type")
        force_catalog_refresh = _coerce_bool(
            request.args.get("refresh_catalog") or request.args.get("force_catalog_refresh")
        )
        manifest = service.catalog.manifest(force_remote=force_catalog_refresh)
        cleanup_result = {
            "cleanup_performed": False,
            "deleted": [],
            "deleted_items": [],
            "deleted_count": 0,
            "state_pruned_count": 0,
            "failed": [],
        }
        if force_catalog_refresh:
            cleanup_result = service.catalog.cleanup_deleted_official_assemblies(manifest=manifest)
            deleted_count = int(cleanup_result.get("deleted_count") or 0)
            failed_count = len(cleanup_result.get("failed") or [])
            if deleted_count or failed_count:
                detail_parts = []
                if deleted_count:
                    detail_parts.append(f"deleted={deleted_count}")
                if failed_count:
                    detail_parts.append(f"failed={failed_count}")
                service._audit(
                    user=user,
                    action="official_catalog_cleanup",
                    domain=AssemblyDomain.OFFICIAL,
                    status="success" if not failed_count else "failed",
                    detail=" ".join(detail_parts),
                )
        items = service.runtime.list_assemblies(domain=domain, assembly_type=assembly_type)
        items = service.catalog.annotate_collection(items, manifest=manifest)
        queue_state = service.runtime.queue_snapshot()
        official_catalog_status = service.catalog.catalog_status(items, manifest=manifest)
        official_catalog_status.update(
            {
                "cleanup_performed": bool(cleanup_result.get("cleanup_performed")),
                "deleted_assembly_count": int(cleanup_result.get("deleted_count") or 0),
                "deleted_assemblies": cleanup_result.get("deleted") or [],
                "deleted_items": cleanup_result.get("deleted_items") or [],
                "cleanup_failed": cleanup_result.get("failed") or [],
                "state_pruned_count": int(cleanup_result.get("state_pruned_count") or 0),
            }
        )
        return jsonify(
            {
                "items": items,
                "queue": queue_state,
                "official_catalog": official_catalog_status,
            }
        ), 200

    # ------------------------------------------------------------------
    # Single assembly retrieval
    # ------------------------------------------------------------------
    @blueprint.route("/<string:assembly_guid>", methods=["GET"])
    def get_assembly(assembly_guid: str):
        _, error = service.require_user()
        if error:
            return jsonify(error[0]), error[1]
        data = service.runtime.get_assembly(assembly_guid)
        if not data:
            return jsonify({"error": "not found"}), 404
        if str(data.get("source") or "").strip().lower() == AssemblyDomain.OFFICIAL.value:
            data = service.catalog.annotate_collection([data])[0]
        data["queue"] = service.runtime.queue_snapshot()
        return jsonify(data), 200

    # ------------------------------------------------------------------
    # Creation
    # ------------------------------------------------------------------
    @blueprint.route("", methods=["POST"])
    def create_assembly():
        payload = request.get_json(silent=True) or {}
        domain = service.parse_domain(payload.get("domain"))
        if domain is None:
            return jsonify({"error": "invalid domain"}), 400
        user, error = service.require_mutation_for_domain(domain)
        pending_guid = str(payload.get("assembly_guid") or "").strip() or None
        if error:
            detail = error[0].get("message") or error[0].get("error") or "permission denied"
            service._audit(user=user, action="create", domain=domain, assembly_guid=pending_guid, status="denied", detail=detail)
            return jsonify(error[0]), error[1]
        try:
            record = service.runtime.create_assembly(payload)
            service._audit(
                user=user,
                action="create",
                domain=domain,
                assembly_guid=record.get("assembly_guid"),
                status="success",
                detail="queued",
            )
            return jsonify(record), 201
        except ValueError as exc:
            service._audit(
                user=user,
                action="create",
                domain=domain,
                assembly_guid=pending_guid,
                status="failed",
                detail=str(exc),
            )
            return jsonify({"error": str(exc)}), 400
        except Exception:  # pragma: no cover - runtime guard
            service.logger.exception("Failed to create assembly.")
            service._audit(
                user=user,
                action="create",
                domain=domain,
                assembly_guid=pending_guid,
                status="error",
                detail="internal server error",
            )
            return jsonify({"error": "internal server error"}), 500

    # ------------------------------------------------------------------
    # Update
    # ------------------------------------------------------------------
    @blueprint.route("/<string:assembly_guid>", methods=["PUT"])
    def update_assembly(assembly_guid: str):
        payload = request.get_json(silent=True) or {}
        existing = service.runtime.get_cached_entry(assembly_guid)
        if not existing:
            return jsonify({"error": "not found"}), 404
        user, error = service.require_mutation_for_domain(existing.domain)
        if error:
            detail = error[0].get("message") or error[0].get("error") or "permission denied"
            service._audit(
                user=user,
                action="update",
                domain=existing.domain,
                assembly_guid=assembly_guid,
                status="denied",
                detail=detail,
            )
            return jsonify(error[0]), error[1]
        try:
            record = service.runtime.update_assembly(assembly_guid, payload)
            service._audit(
                user=user,
                action="update",
                domain=existing.domain,
                assembly_guid=assembly_guid,
                status="success",
                detail="queued",
            )
            return jsonify(record), 200
        except ValueError as exc:
            service._audit(
                user=user,
                action="update",
                domain=existing.domain,
                assembly_guid=assembly_guid,
                status="failed",
                detail=str(exc),
            )
            return jsonify({"error": str(exc)}), 400
        except Exception:  # pragma: no cover - runtime guard
            service.logger.exception("Failed to update assembly %s.", assembly_guid)
            service._audit(
                user=user,
                action="update",
                domain=existing.domain,
                assembly_guid=assembly_guid,
                status="error",
                detail="internal server error",
            )
            return jsonify({"error": "internal server error"}), 500

    # ------------------------------------------------------------------
    # Deletion
    # ------------------------------------------------------------------
    @blueprint.route("/<string:assembly_guid>", methods=["DELETE"])
    def delete_assembly(assembly_guid: str):
        existing = service.runtime.get_cached_entry(assembly_guid)
        if not existing:
            return jsonify({"error": "not found"}), 404
        user, error = service.require_mutation_for_domain(existing.domain)
        if error:
            detail = error[0].get("message") or error[0].get("error") or "permission denied"
            service._audit(
                user=user,
                action="delete",
                domain=existing.domain,
                assembly_guid=assembly_guid,
                status="denied",
                detail=detail,
            )
            return jsonify(error[0]), error[1]
        try:
            service.runtime.delete_assembly(assembly_guid)
            service._audit(
                user=user,
                action="delete",
                domain=existing.domain,
                assembly_guid=assembly_guid,
                status="success",
                detail="queued",
            )
            return jsonify({"status": "queued"}), 202
        except ValueError as exc:
            service._audit(
                user=user,
                action="delete",
                domain=existing.domain,
                assembly_guid=assembly_guid,
                status="failed",
                detail=str(exc),
            )
            return jsonify({"error": str(exc)}), 400
        except Exception:  # pragma: no cover
            service.logger.exception("Failed to delete assembly %s.", assembly_guid)
            service._audit(
                user=user,
                action="delete",
                domain=existing.domain,
                assembly_guid=assembly_guid,
                status="error",
                detail="internal server error",
            )
            return jsonify({"error": "internal server error"}), 500

    # ------------------------------------------------------------------
    # Clone between domains
    # ------------------------------------------------------------------
    @blueprint.route("/<string:assembly_guid>/clone", methods=["POST"])
    def clone_assembly(assembly_guid: str):
        payload = request.get_json(silent=True) or {}
        target_domain_value = payload.get("target_domain")
        domain = service.parse_domain(target_domain_value)
        if domain is None:
            return jsonify({"error": "invalid target domain"}), 400
        user, error = service.require_mutation_for_domain(domain)
        pending_guid = str(payload.get("new_assembly_guid") or "").strip() or None
        if error:
            detail = error[0].get("message") or error[0].get("error") or "permission denied"
            service._audit(
                user=user,
                action="clone",
                domain=domain,
                assembly_guid=pending_guid or assembly_guid,
                status="denied",
                detail=detail,
            )
            return jsonify(error[0]), error[1]
        new_guid = payload.get("new_assembly_guid")
        try:
            record = service.runtime.clone_assembly(
                assembly_guid,
                target_domain=domain.value,
                new_assembly_guid=new_guid,
            )
            service._audit(
                user=user,
                action="clone",
                domain=domain,
                assembly_guid=record.get("assembly_guid"),
                status="success",
                detail=f"source={assembly_guid}",
            )
            return jsonify(record), 201
        except ValueError as exc:
            service._audit(
                user=user,
                action="clone",
                domain=domain,
                assembly_guid=pending_guid or assembly_guid,
                status="failed",
                detail=str(exc),
            )
            return jsonify({"error": str(exc)}), 400
        except Exception:  # pragma: no cover
            service.logger.exception("Failed to clone assembly %s.", assembly_guid)
            service._audit(
                user=user,
                action="clone",
                domain=domain,
                assembly_guid=pending_guid or assembly_guid,
                status="error",
                detail="internal server error",
            )
            return jsonify({"error": "internal server error"}), 500

    # ------------------------------------------------------------------
    # Import legacy assembly JSON
    # ------------------------------------------------------------------
    @blueprint.route("/import", methods=["POST"])
    def import_assembly():
        payload = request.get_json(silent=True) or {}
        document = payload.get("document")
        if document is None:
            document = payload.get("payload")
        if document is None:
            return jsonify({"error": "missing document"}), 400

        domain = service.parse_domain(payload.get("domain")) or AssemblyDomain.USER
        user, error = service.require_mutation_for_domain(domain)
        pending_guid = str(payload.get("assembly_guid") or "").strip() or None
        if error:
            detail = error[0].get("message") or error[0].get("error") or "permission denied"
            service._audit(
                user=user,
                action="import",
                domain=domain,
                assembly_guid=pending_guid,
                status="denied",
                detail=detail,
            )
            return jsonify(error[0]), error[1]

        try:
            record = service.runtime.import_assembly(
                domain=domain,
                document=document,
                assembly_guid=pending_guid,
            )
            record["queue"] = service.runtime.queue_snapshot()
            service._audit(
                user=user,
                action="import",
                domain=domain,
                assembly_guid=record.get("assembly_guid"),
                status="success",
                detail="queued",
            )
            return jsonify(record), 201
        except AssemblySerializationError as exc:
            service._audit(
                user=user,
                action="import",
                domain=domain,
                assembly_guid=pending_guid,
                status="failed",
                detail=str(exc),
            )
            return jsonify({"error": str(exc)}), 400
        except ValueError as exc:
            service._audit(
                user=user,
                action="import",
                domain=domain,
                assembly_guid=pending_guid,
                status="failed",
                detail=str(exc),
            )
            return jsonify({"error": str(exc)}), 400
        except Exception:  # pragma: no cover
            service.logger.exception("Failed to import assembly.")
            service._audit(
                user=user,
                action="import",
                domain=domain,
                assembly_guid=pending_guid,
                status="error",
                detail="internal server error",
            )
            return jsonify({"error": "internal server error"}), 500

    # ------------------------------------------------------------------
    # Export assembly authoring JSON
    # ------------------------------------------------------------------
    @blueprint.route("/<string:assembly_guid>/export", methods=["GET"])
    def export_assembly(assembly_guid: str):
        user, error = service.require_user()
        if error:
            return jsonify(error[0]), error[1]
        try:
            data = service.runtime.export_assembly(assembly_guid)
            service._audit(
                user=user,
                action="export",
                domain=AssemblyAPIService.parse_domain(data.get("domain")),
                assembly_guid=assembly_guid,
                status="success",
                detail="authoring export",
            )
            return jsonify(data), 200
        except ValueError:
            return jsonify({"error": "not found"}), 404
        except Exception:  # pragma: no cover
            service.logger.exception("Failed to export assembly %s.", assembly_guid)
            return jsonify({"error": "internal server error"}), 500

    # ------------------------------------------------------------------
    # Official catalog updates
    # ------------------------------------------------------------------
    @blueprint.route("/<string:assembly_guid>/official-update", methods=["POST"])
    def update_official_assembly(assembly_guid: str):
        user, error = service.require_admin()
        if error:
            return jsonify(error[0]), error[1]
        try:
            record = service.catalog.update_official_assembly(assembly_guid)
            annotated = service.catalog.annotate_collection([record])[0]
            service._audit(
                user=user,
                action="official_update",
                domain=AssemblyDomain.OFFICIAL,
                assembly_guid=annotated.get("assembly_guid"),
                status="success",
                detail=f"source={annotated.get('official_catalog_source') or 'catalog'}",
            )
            return jsonify(annotated), 200
        except RuntimeError as exc:
            service._audit(
                user=user,
                action="official_update",
                domain=AssemblyDomain.OFFICIAL,
                assembly_guid=assembly_guid,
                status="failed",
                detail=str(exc),
            )
            return jsonify({"error": str(exc)}), 502
        except ValueError as exc:
            service._audit(
                user=user,
                action="official_update",
                domain=AssemblyDomain.OFFICIAL,
                assembly_guid=assembly_guid,
                status="failed",
                detail=str(exc),
            )
            return jsonify({"error": str(exc)}), 404
        except Exception:  # pragma: no cover - runtime guard
            service.logger.exception("Failed to update official assembly %s.", assembly_guid)
            service._audit(
                user=user,
                action="official_update",
                domain=AssemblyDomain.OFFICIAL,
                assembly_guid=assembly_guid,
                status="error",
                detail="internal server error",
            )
            return jsonify({"error": "internal server error"}), 500

    @blueprint.route("/official/update-all", methods=["POST"])
    def update_all_official_assemblies():
        user, error = service.require_admin()
        if error:
            return jsonify(error[0]), error[1]
        try:
            result = service.catalog.update_all_official_assemblies()
            if result.get("error"):
                detail = str(result.get("error"))
                service._audit(
                    user=user,
                    action="official_update_all",
                    domain=AssemblyDomain.OFFICIAL,
                    status="failed",
                    detail=detail,
                )
                return jsonify(result), 502
            detail_parts = [f"updated={len(result.get('updated') or [])}"]
            installed_count = int(result.get("installed_count") or 0)
            if installed_count:
                detail_parts.append(f"installed={installed_count}")
            deleted_count = int(result.get("deleted_count") or 0)
            if deleted_count:
                detail_parts.append(f"deleted={deleted_count}")
            failed_count = len(result.get("failed") or [])
            if failed_count:
                detail_parts.append(f"failed={failed_count}")
            detail = " ".join(detail_parts)
            service._audit(
                user=user,
                action="official_update_all",
                domain=AssemblyDomain.OFFICIAL,
                status="success",
                detail=detail,
            )
            return jsonify(result), 200
        except Exception:  # pragma: no cover - runtime guard
            service.logger.exception("Failed to update all official assemblies.")
            service._audit(
                user=user,
                action="official_update_all",
                domain=AssemblyDomain.OFFICIAL,
                status="error",
                detail="internal server error",
            )
            return jsonify({"error": "internal server error"}), 500

    # ------------------------------------------------------------------
    # Dev Mode toggle
    # ------------------------------------------------------------------
    @blueprint.route("/dev-mode/switch", methods=["POST"])
    def switch_dev_mode():
        user, error = service.require_admin()
        if error:
            return jsonify(error[0]), error[1]
        payload = request.get_json(silent=True) or {}
        enabled = bool(payload.get("enabled"))
        try:
            final_state = service.auth.set_dev_mode(enabled)
        except PermissionError:  # pragma: no cover - defensive
            service._audit(user=user, action="dev_mode_toggle", status="error", detail="state persistence failure")
            return jsonify({"error": "unable to update dev mode state"}), 500
        detail = "enabled" if final_state else "disabled"
        service._audit(user=user, action="dev_mode_toggle", status="success", detail=detail)
        return jsonify({"dev_mode": final_state}), 200

    # ------------------------------------------------------------------
    # Immediate flush
    # ------------------------------------------------------------------
    @blueprint.route("/dev-mode/write", methods=["POST"])
    def flush_assemblies():
        user, error = service.require_admin(dev_mode_required=True)
        if error:
            return jsonify(error[0]), error[1]
        try:
            service.runtime.flush_writes()
            service._audit(user=user, action="flush_queue", status="success", detail="manual flush")
            return jsonify({"status": "flushed"}), 200
        except Exception:  # pragma: no cover
            service.logger.exception("Failed to flush assembly queue.")
            service._audit(user=user, action="flush_queue", status="error", detail="internal server error")
            return jsonify({"error": "internal server error"}), 500

    app.register_blueprint(blueprint)
