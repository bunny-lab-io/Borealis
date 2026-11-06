"""Linux process-lifetime identity fence shared with Go Engine consumers.

Lock directory inode, not a replaceable file. Read-only secrets mounts work.
Caller must retain returned fd until process exit, including background users.
"""

import fcntl
import os
import stat


RECOVERY_MARKER = ".borealis-identity-recovery.json"


def acquire_identity_consumer(root: str) -> int:
    path = os.path.abspath(root)
    if os.path.realpath(path) != path:
        raise RuntimeError("identity directory must exist without symlinks")
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        fcntl.flock(fd, fcntl.LOCK_SH | fcntl.LOCK_NB)
        held = os.fstat(fd)
        current = os.stat(path, follow_symlinks=False)
        if (
            not stat.S_ISDIR(current.st_mode)
            or held.st_mode & 0o022
            or (held.st_dev, held.st_ino) != (current.st_dev, current.st_ino)
            or os.path.realpath(path) != path
        ):
            raise RuntimeError("identity directory changed or unsafe permissions")
        try:
            os.stat(RECOVERY_MARKER, dir_fd=fd, follow_symlinks=False)
        except FileNotFoundError:
            return fd
        raise RuntimeError("identity recovery incomplete; retain journal and keep consumers stopped")
    except BaseException:
        os.close(fd)
        raise
