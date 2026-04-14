# ======================================================
# Data\Engine\services\RemoteDesktop\__init__.py
# Description: Remote desktop services (VNC proxy + session management).
#
# API Endpoints (if applicable): None
# ======================================================

"""Remote desktop service helpers for the Borealis Engine runtime."""

from .vnc_proxy import VNC_WS_PATH, VncProxyServer, VncSessionRegistry, ensure_vnc_proxy
from .vnc_sessions import (
    VncCollaborationManager,
    VncCollaborationSession,
    VncParticipant,
    ensure_vnc_collaboration_manager,
)

__all__ = [
    "VNC_WS_PATH",
    "VncProxyServer",
    "VncSessionRegistry",
    "ensure_vnc_proxy",
    "VncCollaborationManager",
    "VncCollaborationSession",
    "VncParticipant",
    "ensure_vnc_collaboration_manager",
]
