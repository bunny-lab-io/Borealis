# ======================================================
# Data\Engine\services\API\assemblies\catalog_routes.py
# Description: Retained Aurora official-catalog fallback routes for assembly migration.
#
# API Endpoints (if applicable):
# - GET /api/assemblies?refresh_catalog=1 (Token Authenticated) - Refreshes Aurora catalog metadata.
# - POST /api/assemblies/<assembly_guid>/official-update (Admin) - Updates one Aurora assembly.
# - POST /api/assemblies/official/update-all (Admin) - Updates all Aurora assemblies.
# ======================================================

"""Aurora official-catalog fallback routes retained until Go owns catalog sync."""

from __future__ import annotations

from typing import TYPE_CHECKING

from flask import Blueprint, jsonify, request

from ....assembly_management.models import AssemblyDomain
from .management import AssemblyAPIService, _coerce_bool

if TYPE_CHECKING:  # pragma: no cover - typing aide
    from .. import EngineServiceAdapters


def register_official_catalog_routes(app, adapters: "EngineServiceAdapters") -> None:
    service = AssemblyAPIService(app, adapters)
    blueprint = Blueprint("assemblies_official_catalog", __name__, url_prefix="/api/assemblies")

    @blueprint.route("", methods=["GET"])
    def refresh_official_catalog():
        force_catalog_refresh = _coerce_bool(
            request.args.get("refresh_catalog") or request.args.get("force_catalog_refresh")
        )
        if not force_catalog_refresh:
            return jsonify({"error": "not found"}), 404

        user, error = service.require_user()
        if error:
            return jsonify(error[0]), error[1]

        domain = request.args.get("domain")
        assembly_type = request.args.get("type")
        manifest = service.catalog.manifest(force_remote=True)
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
                "queue": service.runtime.queue_snapshot(),
                "official_catalog": official_catalog_status,
            }
        ), 200

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
            service._audit(
                user=user,
                action="official_update_all",
                domain=AssemblyDomain.OFFICIAL,
                status="success",
                detail=" ".join(detail_parts),
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

    app.register_blueprint(blueprint)
