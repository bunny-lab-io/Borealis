"""Compatibility shim for code that still imports ``job_scheduler`` directly."""

from Data.Server.app.runtime.job_scheduler import *  # noqa: F401,F403
