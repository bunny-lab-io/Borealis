"""Windows compatibility layer for the POSIX fcntl module."""
from __future__ import annotations

import logging
import msvcrt
import os
import shutil
import struct
from typing import Any, Dict, Tuple

log = logging.getLogger(__name__)
log.debug("Borealis fcntl shim active (Windows)")

F_DUPFD = 0
F_GETFD = 1
F_SETFD = 2
F_GETFL = 3
F_SETFL = 4

LOCK_UN = 8
LOCK_SH = 1
LOCK_EX = 2
LOCK_NB = 4

__fd_flags: Dict[int, int] = {}
__locks: Dict[int, Tuple[int, int]] = {}


def _winsize() -> bytes:
    rows, cols = shutil.get_terminal_size(fallback=(24, 80))
    rows = max(1, int(rows))
    cols = max(1, int(cols))
    return struct.pack("HHHH", rows, cols, 0, 0)


def ioctl(fd: int, op: int, arg: Any = 0) -> bytes:
    return _winsize()


def fcntl(fd: int, op: int, arg: Any = 0) -> Any:
    if op == F_GETFL:
        return __fd_flags.get(fd, 0)
    if op == F_SETFL:
        try:
            __fd_flags[fd] = int(arg)
        except Exception:
            __fd_flags[fd] = 0
        return 0
    return arg


def flock(fd: int, op: int) -> None:
    if op & LOCK_UN:
        if fd in __locks:
            _apply_lock(fd, msvcrt.LK_UNLCK, *__locks.pop(fd))
        return

    length = 1
    nb = bool(op & LOCK_NB)
    mode = msvcrt.LK_LOCK if not nb else msvcrt.LK_NBLCK
    try:
        _apply_lock(fd, mode, 0, length)
        __locks[fd] = (0, length)
    except OSError:
        raise


def lockf(fd: int, cmd: int, length: int = 0) -> None:
    if length <= 0:
        length = 1
    if cmd & LOCK_UN:
        if fd in __locks:
            _apply_lock(fd, msvcrt.LK_UNLCK, *__locks.pop(fd))
        return
    nb = bool(cmd & LOCK_NB)
    mode = msvcrt.LK_LOCK if not nb else msvcrt.LK_NBLCK
    _apply_lock(fd, mode, 0, length)
    __locks[fd] = (0, length)


def _apply_lock(fd: int, mode: int, offset: int, length: int) -> None:
    cur = os.lseek(fd, 0, os.SEEK_CUR)
    os.lseek(fd, offset, os.SEEK_SET)
    try:
        msvcrt.locking(fd, mode, length)
    finally:
        os.lseek(fd, cur, os.SEEK_SET)


__all__ = [name for name in globals() if name.isupper() or name in {'ioctl', 'fcntl', 'flock', 'lockf'}]
