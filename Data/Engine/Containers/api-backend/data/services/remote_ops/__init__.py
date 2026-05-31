"""Remote-operation session helpers."""

from .agent_routes import (
    build_agent_remote_ops_route_payload,
    fetch_active_site_worker_route,
    join_url,
    site_worker_route_urls,
)
from .sessions import (
    REMOTE_OP_SESSION_AUDIENCE,
    REMOTE_OP_SESSION_ISSUER,
    REMOTE_OP_SESSION_TOKEN_TYPE,
    RemoteOpSessionError,
    issue_remote_op_session,
    normalize_remote_op_capabilities,
    verify_remote_op_session,
)

__all__ = [
    "REMOTE_OP_SESSION_AUDIENCE",
    "REMOTE_OP_SESSION_ISSUER",
    "REMOTE_OP_SESSION_TOKEN_TYPE",
    "RemoteOpSessionError",
    "build_agent_remote_ops_route_payload",
    "fetch_active_site_worker_route",
    "issue_remote_op_session",
    "join_url",
    "normalize_remote_op_capabilities",
    "site_worker_route_urls",
    "verify_remote_op_session",
]
