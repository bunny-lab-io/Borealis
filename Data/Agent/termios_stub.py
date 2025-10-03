"""Windows compatibility shim for termios used by Ansible."""
from __future__ import annotations

import logging
from typing import Iterable, List

log = logging.getLogger(__name__)
log.debug("Borealis termios shim active (Windows)")

NCCS = 32

VINTR = 0
VQUIT = 1
VERASE = 2
VKILL = 3
VEOF = 4
VTIME = 5
VMIN = 6
VSTART = 8
VSTOP = 9
VSUSP = 10
VEOL = 11
VREPRINT = 12
VDISCARD = 13
VWERASE = 14
VLNEXT = 15
VEOL2 = 16

IGNBRK = 0x0001
BRKINT = 0x0002
IGNPAR = 0x0004
PARMRK = 0x0008
INPCK = 0x0010
ISTRIP = 0x0020
INLCR = 0x0040
IGNCR = 0x0080
ICRNL = 0x0100
IXON = 0x0400
IXOFF = 0x1000

OPOST = 0x0001
ONLCR = 0x0004
OCRNL = 0x0008
ONOCR = 0x0010
ONLRET = 0x0020
OFILL = 0x0040

CSIZE = 0x0030
CS8 = 0x0030
CSTOPB = 0x0040
CREAD = 0x0080
PARENB = 0x0100
PARODD = 0x0200
CLOCAL = 0x0800

ISIG = 0x0001
ICANON = 0x0002
ECHO = 0x0008
ECHOE = 0x0010
ECHOK = 0x0020
ECHONL = 0x0040
IEXTEN = 0x0200

TCSANOW = 0
TCSADRAIN = 1
TCSAFLUSH = 2

_default_cc: List[int] = [0] * NCCS


def _make_termios() -> list:
    return [0, 0, 0, 0, 0, 0, _default_cc.copy()]


def tcgetattr(fd: int) -> list:
    return _make_termios()


def tcsetattr(fd: int, when: int, attributes: Iterable) -> None:
    return None


def cfgetospeed(attrs: Iterable) -> int:
    return 9600


def cfgetispeed(attrs: Iterable) -> int:
    return 9600


def cfsetospeed(attrs: Iterable, speed: int) -> None:
    return None


def cfsetispeed(attrs: Iterable, speed: int) -> None:
    return None


def tcflush(fd: int, queue: int) -> None:
    return None


def tcflow(fd: int, action: int) -> None:
    return None


def tcsendbreak(fd: int, duration: int) -> None:
    return None


def tcdrain(fd: int) -> None:
    return None


def tcgetsid(fd: int) -> int:
    raise OSError("tcgetsid not supported on Windows")

__all__ = [name for name in globals() if name.isupper() or name in {
    'tcgetattr', 'tcsetattr', 'cfgetospeed', 'cfgetispeed', 'cfsetospeed',
    'cfsetispeed', 'tcflush', 'tcflow', 'tcsendbreak', 'tcdrain', 'tcgetsid'
}]
