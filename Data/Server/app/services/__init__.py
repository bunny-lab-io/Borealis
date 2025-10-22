"""Service container wiring for the Borealis server."""

from __future__ import annotations

from threading import RLock
from typing import Any, Callable, Dict

from ..config import BaseConfig
from .integrations import IntegrationRegistry

ServiceFactory = Callable[["ServiceContainer"], Any]


class ServiceContainer:
    """A lightweight dependency registry for Borealis services."""

    def __init__(self, config: BaseConfig) -> None:
        self.config = config
        self._factories: Dict[str, ServiceFactory] = {}
        self._instances: Dict[str, Any] = {}
        self._lock = RLock()
        self.integrations = IntegrationRegistry()

    def register_factory(self, name: str, factory: ServiceFactory) -> None:
        with self._lock:
            self._factories[name] = factory
            if name in self._instances:
                del self._instances[name]

    def register_instance(self, name: str, instance: Any) -> None:
        with self._lock:
            self._instances[name] = instance

    def resolve(self, name: str) -> Any:
        with self._lock:
            if name in self._instances:
                return self._instances[name]
            if name not in self._factories:
                raise KeyError(f"No service registered under key '{name}'")
            instance = self._factories[name](self)
            self._instances[name] = instance
            return instance


__all__ = ["ServiceContainer"]
