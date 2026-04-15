# ======================================================
# Data\Engine\services\RemoteDesktop\vnc_sessions.py
# Description: Collaboration-aware VNC session management for Borealis remote desktop.
#
# API Endpoints (if applicable): None
# ======================================================

"""Collaboration-aware VNC session management for Borealis remote desktop."""

from __future__ import annotations

import logging
import threading
import time
import uuid
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional, Tuple


_STALE_PARTICIPANT_AFTER_SECONDS = 90.0


def _now_ts() -> float:
    return time.time()


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


@dataclass
class VncParticipant:
    participant_id: str
    operator_id: str
    role: str
    joined_at: float
    last_activity_at: float
    active_connections: int = 0
    last_connected_at: Optional[float] = None
    last_disconnected_at: Optional[float] = None

    def touch(self) -> None:
        self.last_activity_at = _now_ts()

    def mark_open(self) -> None:
        now = _now_ts()
        self.active_connections += 1
        self.last_connected_at = now
        self.last_activity_at = now

    def mark_close(self) -> None:
        now = _now_ts()
        if self.active_connections > 0:
            self.active_connections -= 1
        self.last_disconnected_at = now
        self.last_activity_at = now

    @property
    def is_connected(self) -> bool:
        return self.active_connections > 0


@dataclass
class VncCollaborationSession:
    session_id: str
    agent_id: str
    created_at: float
    updated_at: float
    state: str
    controller_operator_id: Optional[str]
    controller_participant_id: Optional[str]
    controller_password: str
    credential_revision: int = 1
    remove_wallpaper: bool = True
    last_error: str = ""
    tunnel_id: str = ""
    allowed_ips: str = ""
    engine_virtual_ip: str = ""
    last_backend_ready_at: float = 0.0
    participants: Dict[str, VncParticipant] = field(default_factory=dict)

    def touch(self) -> None:
        self.updated_at = _now_ts()

    def participant_for_operator(self, operator_id: str) -> Optional[VncParticipant]:
        normalized = _clean_text(operator_id)
        if not normalized:
            return None
        for participant in self.participants.values():
            if participant.operator_id == normalized:
                return participant
        return None

    def connected_participant_count(self) -> int:
        return sum(1 for participant in self.participants.values() if participant.is_connected)

    def participant_count(self) -> int:
        return len(self.participants)


@dataclass
class AgentVncCredential:
    agent_id: str
    controller_password: str
    credential_revision: int
    updated_at: float

    def touch(self) -> None:
        self.updated_at = _now_ts()


class VncCollaborationManager:
    """Tracks operator collaboration state for one active VNC session per device."""

    def __init__(self, *, logger: logging.Logger) -> None:
        self.logger = logger
        self._lock = threading.Lock()
        self._sessions_by_id: Dict[str, VncCollaborationSession] = {}
        self._session_ids_by_agent: Dict[str, str] = {}
        self._credentials_by_agent: Dict[str, AgentVncCredential] = {}

    def upsert_agent_credential(
        self,
        *,
        agent_id: str,
        controller_password: str,
        credential_revision: Any = None,
    ) -> AgentVncCredential:
        normalized_agent_id = _clean_text(agent_id)
        normalized_password = _clean_text(controller_password)[:8]
        if not normalized_agent_id or not normalized_password:
            raise ValueError("agent_id and controller_password are required")
        try:
            revision_value = int(credential_revision)
        except Exception:
            revision_value = int(_now_ts() * 1000)
        if revision_value <= 0:
            revision_value = int(_now_ts() * 1000)
        with self._lock:
            credential = self._credentials_by_agent.get(normalized_agent_id)
            if credential is None:
                credential = AgentVncCredential(
                    agent_id=normalized_agent_id,
                    controller_password=normalized_password,
                    credential_revision=revision_value,
                    updated_at=_now_ts(),
                )
                self._credentials_by_agent[normalized_agent_id] = credential
            else:
                credential.controller_password = normalized_password
                credential.credential_revision = revision_value
                credential.touch()
            session_id = self._session_ids_by_agent.get(normalized_agent_id)
            session = self._sessions_by_id.get(session_id or "")
            if session is not None:
                session.controller_password = normalized_password
                session.credential_revision = revision_value
                session.touch()
            return AgentVncCredential(
                agent_id=credential.agent_id,
                controller_password=credential.controller_password,
                credential_revision=credential.credential_revision,
                updated_at=credential.updated_at,
            )

    def get_agent_credential(self, agent_id: str) -> Optional[AgentVncCredential]:
        normalized_agent_id = _clean_text(agent_id)
        if not normalized_agent_id:
            return None
        with self._lock:
            credential = self._credentials_by_agent.get(normalized_agent_id)
            if credential is None:
                return None
            return AgentVncCredential(
                agent_id=credential.agent_id,
                controller_password=credential.controller_password,
                credential_revision=credential.credential_revision,
                updated_at=credential.updated_at,
            )

    def _assign_owner_locked(
        self,
        session: VncCollaborationSession,
        *,
        preferred_participant_id: str = "",
    ) -> Optional[VncParticipant]:
        if not session.participants:
            session.controller_operator_id = None
            session.controller_participant_id = None
            return None
        normalized_preferred = _clean_text(preferred_participant_id)
        participant: Optional[VncParticipant] = None
        if normalized_preferred:
            participant = session.participants.get(normalized_preferred)
        if participant is None:
            participant = sorted(
                session.participants.values(),
                key=lambda item: (
                    str(item.operator_id or "").lower(),
                    str(item.participant_id or ""),
                ),
            )[0]
        for existing in session.participants.values():
            existing.role = "controller"
        session.controller_operator_id = participant.operator_id
        session.controller_participant_id = participant.participant_id
        session.state = "active"
        return participant

    def _cleanup_stale_locked(self) -> None:
        now = _now_ts()
        stale_sessions: List[str] = []
        for session_id, session in self._sessions_by_id.items():
            stale_participants = [
                participant_id
                for participant_id, participant in session.participants.items()
                if (
                    not participant.is_connected
                    and (now - float(participant.last_activity_at or session.updated_at or session.created_at))
                    >= _STALE_PARTICIPANT_AFTER_SECONDS
                )
            ]
            for participant_id in stale_participants:
                participant = session.participants.pop(participant_id, None)
                if participant is not None and participant.operator_id == session.controller_operator_id:
                    self._assign_owner_locked(session)
                    session.touch()
            if not session.participants:
                stale_sessions.append(session_id)
        for session_id in stale_sessions:
            session = self._sessions_by_id.pop(session_id, None)
            if session is not None:
                self._session_ids_by_agent.pop(session.agent_id, None)

    def _new_session(
        self,
        agent_id: str,
        operator_id: str,
        *,
        controller_password: str,
        credential_revision: int,
        remove_wallpaper: bool,
    ) -> Tuple[VncCollaborationSession, VncParticipant]:
        now = _now_ts()
        participant = VncParticipant(
            participant_id=uuid.uuid4().hex,
            operator_id=operator_id,
            role="controller",
            joined_at=now,
            last_activity_at=now,
        )
        session = VncCollaborationSession(
            session_id=uuid.uuid4().hex,
            agent_id=agent_id,
            created_at=now,
            updated_at=now,
            state="active",
            controller_operator_id=operator_id,
            controller_participant_id=participant.participant_id,
            controller_password=controller_password,
            credential_revision=max(1, int(credential_revision or 1)),
            remove_wallpaper=bool(remove_wallpaper),
            participants={participant.participant_id: participant},
        )
        self._sessions_by_id[session.session_id] = session
        self._session_ids_by_agent[agent_id] = session.session_id
        return session, participant

    def ensure_session(
        self,
        *,
        agent_id: str,
        operator_id: str,
        controller_password: str,
        credential_revision: Any = None,
        remove_wallpaper: bool,
    ) -> Tuple[VncCollaborationSession, VncParticipant, bool]:
        normalized_agent_id = _clean_text(agent_id)
        normalized_operator_id = _clean_text(operator_id)
        normalized_password = _clean_text(controller_password)[:8]
        if not normalized_agent_id or not normalized_operator_id or not normalized_password:
            raise ValueError("agent_id, operator_id, and controller_password are required")
        try:
            revision_value = int(credential_revision)
        except Exception:
            revision_value = 1
        if revision_value <= 0:
            revision_value = 1
        with self._lock:
            self._cleanup_stale_locked()
            credential = self._credentials_by_agent.get(normalized_agent_id)
            if credential is None:
                self._credentials_by_agent[normalized_agent_id] = AgentVncCredential(
                    agent_id=normalized_agent_id,
                    controller_password=normalized_password,
                    credential_revision=revision_value,
                    updated_at=_now_ts(),
                )
            else:
                credential.controller_password = normalized_password
                credential.credential_revision = revision_value
                credential.touch()
            existing_session_id = self._session_ids_by_agent.get(normalized_agent_id)
            session = self._sessions_by_id.get(existing_session_id or "")
            if session is None:
                session, participant = self._new_session(
                    normalized_agent_id,
                    normalized_operator_id,
                    controller_password=normalized_password,
                    credential_revision=revision_value,
                    remove_wallpaper=remove_wallpaper,
                )
                return session, participant, True
            session.remove_wallpaper = bool(remove_wallpaper)
            if session.controller_password != normalized_password or session.credential_revision != revision_value:
                session.controller_password = normalized_password
                session.credential_revision = revision_value
                session.touch()
            participant = session.participant_for_operator(normalized_operator_id)
            if participant is None:
                now = _now_ts()
                participant = VncParticipant(
                    participant_id=uuid.uuid4().hex,
                    operator_id=normalized_operator_id,
                    role="controller",
                    joined_at=now,
                    last_activity_at=now,
                )
                session.participants[participant.participant_id] = participant
                if not session.controller_operator_id:
                    self._assign_owner_locked(session, preferred_participant_id=participant.participant_id)
                else:
                    participant.role = "controller"
                    if session.state == "reconnect_pending":
                        session.state = "active"
                session.touch()
                return session, participant, False
            participant.role = "controller"
            if session.state == "reconnect_pending":
                session.state = "active"
            if not session.controller_operator_id:
                self._assign_owner_locked(session, preferred_participant_id=participant.participant_id)
            participant.touch()
            session.touch()
            return session, participant, False

    def record_backend_ready(
        self,
        session_id: str,
        *,
        tunnel_id: str = "",
        allowed_ips: str = "",
        engine_virtual_ip: str = "",
    ) -> None:
        normalized_session_id = _clean_text(session_id)
        if not normalized_session_id:
            return
        with self._lock:
            session = self._sessions_by_id.get(normalized_session_id)
            if session is None:
                return
            if tunnel_id:
                session.tunnel_id = _clean_text(tunnel_id)
            if allowed_ips:
                session.allowed_ips = _clean_text(allowed_ips)
            if engine_virtual_ip:
                session.engine_virtual_ip = _clean_text(engine_virtual_ip)
            session.last_backend_ready_at = _now_ts()
            session.last_error = ""
            session.touch()

    def record_error(self, session_id: str, error: str) -> None:
        normalized_session_id = _clean_text(session_id)
        if not normalized_session_id:
            return
        with self._lock:
            session = self._sessions_by_id.get(normalized_session_id)
            if session is None:
                return
            session.last_error = _clean_text(error)
            session.touch()

    def record_proxy_open(self, session_id: str, participant_id: str) -> None:
        with self._lock:
            session = self._sessions_by_id.get(_clean_text(session_id))
            if session is None:
                return
            participant = session.participants.get(_clean_text(participant_id))
            if participant is None:
                return
            participant.mark_open()
            session.touch()

    def record_proxy_close(self, session_id: str, participant_id: str, *, reason: str = "") -> None:
        with self._lock:
            session = self._sessions_by_id.get(_clean_text(session_id))
            if session is None:
                return
            participant = session.participants.get(_clean_text(participant_id))
            if participant is None:
                return
            participant.mark_close()
            if reason:
                session.last_error = _clean_text(reason)
            session.touch()

    def handoff(self, *, session_id: str, actor_operator_id: str, target_operator_id: Optional[str]) -> Dict[str, Any]:
        normalized_session_id = _clean_text(session_id)
        actor = _clean_text(actor_operator_id)
        target = _clean_text(target_operator_id)
        with self._lock:
            self._cleanup_stale_locked()
            session = self._sessions_by_id.get(normalized_session_id)
            if session is None:
                raise KeyError("session_not_found")
            if session.controller_operator_id and session.controller_operator_id != actor:
                raise PermissionError("controller_required")
            actor_participant = session.participant_for_operator(actor)
            if actor_participant is None:
                raise PermissionError("participant_required")
            target_participant = session.participant_for_operator(target or actor)
            if target_participant is None:
                raise KeyError("target_not_found")
            if target_participant.operator_id == actor and session.controller_operator_id == actor:
                raise ValueError("target_already_controller")
            target_participant.role = "controller"
            actor_participant.role = "controller"
            self._assign_owner_locked(session, preferred_participant_id=target_participant.participant_id)
            session.touch()
            return {
                "session": session,
                "reconnect_participants": [],
            }

    def leave_or_close(
        self,
        *,
        session_id: str,
        operator_id: str,
        close_session: bool,
    ) -> Dict[str, Any]:
        normalized_session_id = _clean_text(session_id)
        normalized_operator_id = _clean_text(operator_id)
        with self._lock:
            self._cleanup_stale_locked()
            session = self._sessions_by_id.get(normalized_session_id)
            if session is None:
                raise KeyError("session_not_found")
            participant = session.participant_for_operator(normalized_operator_id)
            if participant is None:
                raise PermissionError("participant_required")
            participant_ids = sorted(session.participants.keys())
            closed = bool(close_session)
            controller_vacant = False
            reconnect_pending = False
            if close_session:
                self._sessions_by_id.pop(session.session_id, None)
                self._session_ids_by_agent.pop(session.agent_id, None)
            else:
                participant.mark_close()
                remaining_participant_ids = [
                    participant_id
                    for participant_id in session.participants.keys()
                    if participant_id != participant.participant_id
                ]
                if (
                    participant.operator_id == session.controller_operator_id
                    and not remaining_participant_ids
                ):
                    reconnect_pending = True
                    session.state = "reconnect_pending"
                    session.touch()
                else:
                    session.participants.pop(participant.participant_id, None)
                    if not session.participants:
                        closed = True
                        self._sessions_by_id.pop(session.session_id, None)
                        self._session_ids_by_agent.pop(session.agent_id, None)
                    else:
                        if participant.operator_id == session.controller_operator_id:
                            self._assign_owner_locked(session)
                        else:
                            for existing in session.participants.values():
                                existing.role = "controller"
                            session.state = "active"
                        session.touch()
            return {
                "closed": closed,
                "controller_vacant": controller_vacant and not closed,
                "reconnect_pending": reconnect_pending and not closed,
                "participant_id": participant.participant_id,
                "agent_id": session.agent_id,
                "disconnect_participants": participant_ids if closed else [participant.participant_id],
            }

    def close_session(self, *, session_id: str, reason: str = "") -> Dict[str, Any]:
        normalized_session_id = _clean_text(session_id)
        with self._lock:
            session = self._sessions_by_id.pop(normalized_session_id, None)
            if session is None:
                raise KeyError("session_not_found")
            self._session_ids_by_agent.pop(session.agent_id, None)
            if reason:
                session.last_error = _clean_text(reason)
            return {
                "session": session,
                "disconnect_participants": sorted(session.participants.keys()),
            }

    def revoke_agent(self, agent_id: str, *, reason: str = "") -> Optional[Dict[str, Any]]:
        normalized_agent_id = _clean_text(agent_id)
        if not normalized_agent_id:
            return None
        with self._lock:
            self._credentials_by_agent.pop(normalized_agent_id, None)
            session_id = self._session_ids_by_agent.pop(normalized_agent_id, None)
            if not session_id:
                return None
            session = self._sessions_by_id.pop(session_id, None)
            if session is None:
                return None
            if reason:
                session.last_error = _clean_text(reason)
            return {
                "session": session,
                "disconnect_participants": sorted(session.participants.keys()),
            }

    def get_session_by_id(self, session_id: str) -> Optional[VncCollaborationSession]:
        with self._lock:
            self._cleanup_stale_locked()
            return self._sessions_by_id.get(_clean_text(session_id))

    def get_session_for_agent(self, agent_id: str) -> Optional[VncCollaborationSession]:
        with self._lock:
            self._cleanup_stale_locked()
            session_id = self._session_ids_by_agent.get(_clean_text(agent_id))
            if not session_id:
                return None
            return self._sessions_by_id.get(session_id)

    def list_sessions(self) -> List[VncCollaborationSession]:
        with self._lock:
            self._cleanup_stale_locked()
            sessions = list(self._sessions_by_id.values())
        return sorted(
            sessions,
            key=lambda session: (
                -float(session.updated_at or session.created_at),
                str(session.agent_id or "").lower(),
                str(session.session_id or ""),
            ),
        )

    def session_snapshot(
        self,
        session: VncCollaborationSession,
        *,
        current_operator_id: str = "",
    ) -> Dict[str, Any]:
        current_operator = _clean_text(current_operator_id)
        participants: List[Dict[str, Any]] = []
        current_participant_role = ""
        current_participant_id = ""
        for participant in sorted(
            session.participants.values(),
            key=lambda item: (
                item.role != "controller",
                str(item.operator_id or "").lower(),
                str(item.participant_id or ""),
            ),
        ):
            if participant.operator_id == current_operator:
                current_participant_role = participant.role
                current_participant_id = participant.participant_id
            participants.append(
                {
                    "participant_id": participant.participant_id,
                    "operator_id": participant.operator_id,
                    "role": participant.role,
                    "connected": participant.is_connected,
                    "joined_at": participant.joined_at,
                    "last_activity_at": participant.last_activity_at,
                    "last_connected_at": participant.last_connected_at,
                    "last_disconnected_at": participant.last_disconnected_at,
                }
            )
        return {
            "session_id": session.session_id,
            "agent_id": session.agent_id,
            "state": session.state,
            "controller_operator_id": session.controller_operator_id or "",
            "controller_participant_id": session.controller_participant_id or "",
            "participant_count": session.participant_count(),
            "connected_participant_count": session.connected_participant_count(),
            "credential_revision": session.credential_revision,
            "created_at": session.created_at,
            "updated_at": session.updated_at,
            "remove_wallpaper": bool(session.remove_wallpaper),
            "last_error": session.last_error or "",
            "tunnel_id": session.tunnel_id or "",
            "allowed_ips": session.allowed_ips or "",
            "engine_virtual_ip": session.engine_virtual_ip or "",
            "last_backend_ready_at": session.last_backend_ready_at or 0.0,
            "participants": participants,
            "current_operator_role": current_participant_role,
            "current_participant_id": current_participant_id,
            "controller_vacant": session.state == "controller_vacant",
            "reconnect_pending": session.state == "reconnect_pending",
            "can_handoff": False,
            "can_claim_control": False,
        }


def ensure_vnc_collaboration_manager(context: Any, *, logger: Optional[logging.Logger] = None) -> VncCollaborationManager:
    existing = getattr(context, "vnc_collaboration_manager", None)
    if isinstance(existing, VncCollaborationManager):
        return existing
    resolved_logger = logger or getattr(context, "logger", logging.getLogger("borealis.engine.vnc"))
    manager = VncCollaborationManager(logger=resolved_logger.getChild("vnc_sessions"))
    setattr(context, "vnc_collaboration_manager", manager)
    return manager


__all__ = [
    "AgentVncCredential",
    "VncCollaborationManager",
    "VncCollaborationSession",
    "VncParticipant",
    "ensure_vnc_collaboration_manager",
]
