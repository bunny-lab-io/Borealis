"""Build-side storage for pinned, inert inputs. Consumers keep their own output files."""

import argparse
import contextlib
import fcntl
import hashlib
import os
from pathlib import Path
import re
import shutil
import stat
import tempfile


DIGEST = re.compile(r"[0-9a-f]{64}")


def regular_reader(path, size):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    reader = os.fdopen(fd, "rb")
    info = os.fstat(fd)
    if not stat.S_ISREG(info.st_mode) or info.st_size != size:
        reader.close()
        raise ValueError("artifact size or file type changed")
    return reader


class ArtifactCache:
    def __init__(self, root, max_bytes, max_objects, reserve_bytes, reserve_inodes):
        self.root = Path(root)
        if (not self.root.is_absolute() or self.root.resolve() != self.root
                or min(max_bytes, max_objects) <= 0 or min(reserve_bytes, reserve_inodes) < 0):
            raise ValueError("invalid artifact cache path or budgets")
        self.root.mkdir(mode=0o700, exist_ok=True)
        info = self.root.lstat()
        if info.st_uid != os.geteuid() or not stat.S_ISDIR(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o700:
            raise ValueError("artifact cache requires a private owner-only directory")
        self.max_bytes = max_bytes
        self.max_objects = max_objects
        self.reserve_bytes = reserve_bytes
        self.reserve_inodes = reserve_inodes

    def _space(self, path, needed, inodes):
        fs = os.statvfs(path)
        return (fs.f_bavail * fs.f_frsize >= needed + self.reserve_bytes
                and fs.f_favail >= inodes + self.reserve_inodes)

    def _make_room(self, size, duplicate, inodes):
        objects = []
        for p in self.root.iterdir():
            if not DIGEST.fullmatch(p.name):
                raise ValueError("unexpected artifact cache entry")
            info = p.lstat()
            if not stat.S_ISREG(info.st_mode):
                raise ValueError("invalid artifact cache entry")
            objects.append((info.st_mtime_ns, p, info))
        used = sum(info.st_size for _, _, info in objects)
        count = len(objects)
        def fits():
            return (size + used <= self.max_bytes and count < self.max_objects
                    and self._space(self.root, size + duplicate, inodes))
        if size > self.max_bytes:
            raise ValueError("artifact exceeds cache budget")
        for _, p, info in sorted(objects):
            if fits():
                break
            # An independent candidate/recovery hard link pins retained bytes.
            if info.st_nlink != 1:
                continue
            with regular_reader(p, info.st_size) as obj:
                try:
                    fcntl.flock(obj, fcntl.LOCK_EX | fcntl.LOCK_NB)
                except BlockingIOError:
                    continue
                p.unlink()
            used -= info.st_size
            count -= 1
        if not fits():
            raise ValueError("insufficient unpinned artifact cache capacity")

    def materialize(self, digest, size, destination, acquire, *, offline=False, retain=False):
        if not DIGEST.fullmatch(digest) or type(size) is not int or size <= 0:
            raise ValueError("invalid pinned artifact identity")
        destination = Path(destination)
        if destination.parent.resolve().is_relative_to(self.root):
            raise ValueError("packaging output cannot reside inside the object store")
        obj = self.root / digest
        incoming = self.root / ".incoming"
        root_fd = os.open(self.root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        with contextlib.ExitStack() as stack:
            stack.callback(os.close, root_fd)
            # One cache population at a time bounds in-flight disk use. Reads
            # retain a shared object lock while copying outside the catalog lock.
            fcntl.flock(root_fd, fcntl.LOCK_EX)
            incoming.unlink(missing_ok=True)
            hit = obj.exists()
            if not hit:
                if offline:
                    raise ValueError("offline artifact missing: " + digest)
                same_fs = not retain and destination.parent.stat().st_dev == self.root.stat().st_dev
                self._make_room(size, size if same_fs else 0, 2 if same_fs else 1)
                try:
                    acquire(incoming)
                    with regular_reader(incoming, size) as reader:
                        if hashlib.file_digest(reader, "sha256").hexdigest() != digest:
                            raise ValueError("acquired artifact digest mismatch")
                        os.fchmod(reader.fileno(), 0o400)
                        os.fsync(reader.fileno())
                    os.replace(incoming, obj)
                    os.fsync(root_fd)
                finally:
                    incoming.unlink(missing_ok=True)
            reader = stack.enter_context(regular_reader(obj, size))
            fcntl.flock(reader, fcntl.LOCK_SH)
            os.utime(reader.fileno())
            if retain:
                # Both names must live on the same provisioned filesystem. A
                # durable hard link protects active or recovery content from LRU.
                if not self._space(destination.parent, 0, 1):
                    raise ValueError("insufficient retained artifact capacity")
                if hashlib.file_digest(reader, "sha256").hexdigest() != digest:
                    raise ValueError("cached artifact digest mismatch")
                try:
                    os.link(obj, destination, follow_symlinks=False)
                except FileExistsError:
                    if destination.is_symlink() or not destination.samefile(obj):
                        raise ValueError("retained artifact reference conflicts")
                dir_fd = os.open(destination.parent, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
                try:
                    os.fsync(dir_fd)
                finally:
                    os.close(dir_fd)
                return hit
            if not self._space(destination.parent, size, 1):
                raise ValueError("insufficient staging capacity for verified object")
            # Keep private in-progress bytes invisible. link() publishes with
            # no replacement. The delivered file is a separate inode.
            with tempfile.NamedTemporaryFile(dir=destination.parent) as out:
                os.posix_fallocate(out.fileno(), 0, size)
                fcntl.flock(root_fd, fcntl.LOCK_UN)
                check = hashlib.sha256()
                total = 0
                while chunk := reader.read(1 << 20):
                    total += len(chunk)
                    if total > size:
                        raise ValueError("cached artifact grew")
                    check.update(chunk)
                    out.write(chunk)
                if total != size or check.hexdigest() != digest:
                    raise ValueError("cached artifact digest mismatch")
                out.flush()
                os.link(out.name, destination)
            return hit


def configured_store():
    budgets = [int(os.environ["BOREALIS_BUILD_CACHE_" + key])
               for key in ("MAX_BYTES", "MAX_OBJECTS", "RESERVE_BYTES", "RESERVE_INODES")]
    return ArtifactCache(os.environ["BOREALIS_BUILD_ARTIFACT_CACHE"], *budgets)


def pinned_input(digest, size, destination, acquire):
    root = os.environ.get("BOREALIS_BUILD_ARTIFACT_CACHE")
    offline = os.environ.get("BOREALIS_BUILD_INPUTS_OFFLINE", "0")
    if offline not in ("0", "1"):
        raise ValueError("invalid offline input setting")
    if not root:
        if offline == "1":
            raise ValueError("offline packaging requires a configured artifact cache")
        acquire(destination)
        return
    configured_store().materialize(digest, size, destination, acquire, offline=offline == "1")


def main():
    parser = argparse.ArgumentParser(description="Retain an inert asset from a verified release or development manifest.")
    parser.add_argument("--artifact", type=Path, required=True)
    parser.add_argument("--sha256", required=True)
    parser.add_argument("--bytes", type=int, required=True)
    parser.add_argument("--reference-directory", type=Path, required=True)
    args = parser.parse_args()
    reference = args.reference_directory.resolve(strict=True)
    if reference != args.reference_directory.absolute() or not reference.is_dir():
        raise ValueError("invalid retention directory")
    def copy(path):
        with regular_reader(args.artifact, args.bytes) as src, path.open("xb") as dst:
            shutil.copyfileobj(src, dst)
    hit = configured_store().materialize(args.sha256, args.bytes, reference / args.sha256, copy, retain=True)
    print("retained", args.sha256, "cached" if hit else "acquired")


if __name__ == "__main__":
    main()
