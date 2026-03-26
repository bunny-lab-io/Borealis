import os
import importlib.util
import sys
from pathlib import Path
from typing import Dict, List, Optional


class RoleManager:
    """
    Discovers and loads role modules from Data/Agent/Roles.
    Each role module should expose:
      - ROLE_NAME: str
      - ROLE_CONTEXTS: List[str] (e.g., ["interactive"], ["system"], or ["interactive","system"])
      - class Role(ctx): with methods:
            - register_events(): optional, bind socket events
            - on_config(roles: List[dict]): optional, apply server config
            - stop_all(): optional, cancel tasks/cleanup

    The ctx passed to each Role is a simple object storing common references.
    """

    class Ctx:
        def __init__(self, sio, agent_id, config, loop, hooks: Optional[dict] = None):
            self.sio = sio
            self._agent_id = agent_id
            self.config = config
            self.loop = loop
            self.hooks = hooks or {}
            try:
                self.service_mode = (self.hooks.get('service_mode') or '').strip().lower()
            except Exception:
                self.service_mode = ''

        @property
        def agent_id(self):
            try:
                resolver = self.hooks.get('get_agent_id')
                if callable(resolver):
                    current = str(resolver() or '').strip()
                    if current:
                        return current
            except Exception:
                pass
            return self._agent_id

        @agent_id.setter
        def agent_id(self, value):
            self._agent_id = value

    def __init__(self, base_dir: str, context: str, sio, agent_id: str, config, loop, hooks: Optional[dict] = None):
        self.base_dir = base_dir
        self.context = context  # "interactive" or "system"
        self.sio = sio
        self._agent_id = agent_id
        self.config = config
        self.loop = loop
        self.hooks = hooks or {}
        self._log_hook = self.hooks.get('log_agent')
        self._role_health_registry = self.hooks.get('role_health_registry')
        self.roles: Dict[str, object] = {}
        self._registered_role_health: List[str] = []

        # Ensure role helpers alongside Roles/ are importable (e.g., signature_utils.py).
        try:
            base_path = Path(self.base_dir).resolve()
            parent_path = base_path.parent
            for candidate in (base_path, parent_path):
                if candidate and str(candidate) not in sys.path:
                    sys.path.insert(0, str(candidate))
        except Exception:
            pass

    @property
    def agent_id(self) -> str:
        try:
            resolver = self.hooks.get('get_agent_id')
            if callable(resolver):
                current = str(resolver() or '').strip()
                if current:
                    return current
        except Exception:
            pass
        return self._agent_id

    @agent_id.setter
    def agent_id(self, value: str) -> None:
        self._agent_id = value

    def _log(self, message: str, *, error: bool = False) -> None:
        if callable(self._log_hook):
            try:
                target = "agent.error.log" if error else "agent.log"
                self._log_hook(message, fname=target)
            except Exception:
                pass

    def _iter_role_files(self) -> List[str]:
        roles_dir = os.path.join(self.base_dir, 'Roles')
        if not os.path.isdir(roles_dir):
            return []
        files = []
        for fn in os.listdir(roles_dir):
            if fn.lower().startswith('role_') and fn.lower().endswith('.py'):
                files.append(os.path.join(roles_dir, fn))
        return sorted(files)

    def load(self):
        for path in self._iter_role_files():
            try:
                spec = importlib.util.spec_from_file_location(os.path.splitext(os.path.basename(path))[0], path)
                mod = importlib.util.module_from_spec(spec)
                assert spec and spec.loader
                spec.loader.exec_module(mod)
            except Exception as exc:
                self._log(f"Role load failed during import path={path} error={exc}", error=True)
                continue

            role_name = getattr(mod, 'ROLE_NAME', None)
            role_contexts = getattr(mod, 'ROLE_CONTEXTS', ['interactive', 'system'])
            RoleClass = getattr(mod, 'Role', None)

            if not role_name or not RoleClass:
                continue
            if self.context not in (role_contexts or []):
                continue

            try:
                ctx = RoleManager.Ctx(self.sio, self.agent_id, self.config, self.loop, hooks=self.hooks)
                role_obj = RoleClass(ctx)
                # Optional event registration
                if hasattr(role_obj, 'register_events'):
                    try:
                        role_obj.register_events()
                    except Exception as exc:
                        self._log(f"Role register_events failed name={role_name} error={exc}", error=True)
                if self._role_health_registry is not None and hasattr(self._role_health_registry, 'register_role'):
                    try:
                        reporter = getattr(role_obj, 'health_report', None)
                        role_label = getattr(role_obj, 'role_health_label', None)
                        role_id = self._role_health_registry.register_role(
                            role_name,
                            context=self.context,
                            reporter=reporter if callable(reporter) else None,
                            role_label=role_label,
                        )
                        if role_id:
                            self._registered_role_health.append(str(role_id))
                    except Exception as exc:
                        self._log(f"Role health registration failed name={role_name} error={exc}", error=True)
                self.roles[role_name] = role_obj
                self._log(f"Role loaded name={role_name} context={self.context}")
            except Exception as exc:
                self._log(f"Role init failed name={role_name} path={path} error={exc}", error=True)
                continue

    def on_config(self, roles_cfg: List[dict]):
        for role in list(self.roles.values()):
            try:
                if hasattr(role, 'on_config'):
                    role.on_config(roles_cfg)
            except Exception:
                pass

    def stop_all(self):
        for role in list(self.roles.values()):
            try:
                if hasattr(role, 'stop_all'):
                    role.stop_all()
            except Exception:
                pass
        if self._role_health_registry is not None and hasattr(self._role_health_registry, 'unregister_role'):
            for role_name in list(self.roles.keys()):
                try:
                    self._role_health_registry.unregister_role(role_name, context=self.context)
                except Exception:
                    pass
