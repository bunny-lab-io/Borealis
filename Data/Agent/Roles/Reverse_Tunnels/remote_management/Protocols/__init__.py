"""Protocol handlers for remote management tunnels (Agent side)."""

from .SSH import SSHChannel
from .WinRM import WinRMChannel

__all__ = ["SSHChannel", "WinRMChannel"]
