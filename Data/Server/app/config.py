"""Application configuration profiles for the Borealis server."""

from __future__ import annotations

from dataclasses import dataclass
import os
from typing import Type


@dataclass(frozen=True)
class BaseConfig:
    """Common configuration shared by all environments."""

    name: str
    debug: bool = False
    testing: bool = False
    log_level: str = "INFO"
    log_file: str = "server.log"

    @property
    def environment(self) -> str:
        return self.name


class DevelopmentConfig(BaseConfig):
    def __init__(self) -> None:
        super().__init__(name="development", debug=True, log_level="DEBUG")


class ProductionConfig(BaseConfig):
    def __init__(self) -> None:
        super().__init__(name="production", log_level="WARNING")


_CONFIG_MAP: dict[str, Type[BaseConfig]] = {
    "development": DevelopmentConfig,
    "dev": DevelopmentConfig,
    "production": ProductionConfig,
    "prod": ProductionConfig,
}


def resolve_config(name: str | None) -> BaseConfig:
    """Return a configuration instance for the given name."""

    candidate = (name or os.getenv("BOREALIS_ENV") or "development").lower().strip()
    factory = _CONFIG_MAP.get(candidate, DevelopmentConfig)
    return factory()
