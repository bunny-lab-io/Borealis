"""Reconcile the reviewed Ubuntu 26.04.1 host policy exceptions (#543, #544).

cri-containerd.original is rendered from the Apache-2.0 licensed containerd
profile. Copyright The docker Authors, The Moby Authors, The containerd Authors.
See Docs/Reference/SBOM.md for the exact source revision and license.
"""
import difflib
import fcntl
import hashlib
import os
from pathlib import Path
import platform
import subprocess
import sys
import tempfile

ASSETS = Path(__file__).resolve().parent
POLICY = Path("/etc/apparmor.d")
LOADED = Path("/sys/kernel/security/apparmor/policy/profiles")
EVIDENCE = Path("/var/lib/borealis/apparmor")
CRI = "cri-containerd.apparmor.d"
SIGNAL = b"  signal (send,receive) peer=cri-containerd.apparmor.d,\n"
PEER = b"  signal (send,receive) peer=cri-containerd.apparmor.d//&unconfined,\n"


def checked(data, digest):
    if hashlib.sha256(data).hexdigest() != digest:
        raise RuntimeError("Host AppArmor policy differs from its reviewed baseline")
    return data


def compiled(source):
    return subprocess.check_output(["apparmor_parser", "-K", "-c0", "-S", source])


def verify_kernel(name, expected, children, allow_absent=False):
    roots = list(LOADED.glob(name + ".*"))
    if not roots and allow_absent:
        return
    if len(roots) != 1:
        raise RuntimeError(f"Expected one loaded {name} profile")
    profiles = [roots[0], *roots[0].glob("profiles/*")]
    if len(profiles) != children + 1 or any((p / "mode").read_text().strip() != "enforce" for p in profiles):
        raise RuntimeError(f"Unexpected {name} policy set or mode")
    parts = {(p / "raw_data").read_bytes() for p in profiles}
    if b"".join(sorted(parts, key=expected.index)) != expected:
        raise RuntimeError(f"Kernel policy differs from compiled {name}")


def reconcile(name, target, source, original, addition, baseline_hash, corrected_hash, children):
    if target.is_symlink() or target.parent.is_symlink():
        raise RuntimeError(f"Refusing symlink at {target}")
    existing = target.read_bytes() if target.exists() else None
    if existing is not None and existing != addition:
        raise RuntimeError(f"Preserve existing administrator policy at {target}")
    evidence = EVIDENCE / name
    if existing is not None:
        corrected = checked(compiled(source), corrected_hash)
        try:
            verify_kernel(name, corrected, children)
        except (ValueError, RuntimeError):
            # A previous run may have persisted the include just before a reboot.
            # Only replace the recorded, reviewed baseline in that case.
            baseline = checked((evidence / "compiled.original").read_bytes(), baseline_hash)
            verify_kernel(name, baseline, children, allow_absent=name == CRI)
            subprocess.run(["apparmor_parser", "-K", "-r", source], check=True)
            verify_kernel(name, corrected, children)
        return
    baseline = checked(compiled(original), baseline_hash)
    verify_kernel(name, baseline, children, allow_absent=name == CRI)
    before = original.read_bytes() if name == CRI else b""
    evidence.mkdir(mode=0o700, exist_ok=True)
    (evidence / "vendor.original").write_bytes(original.read_bytes())
    (evidence / "compiled.original").write_bytes(baseline)
    (evidence / "local.diff").write_bytes(b"".join(difflib.diff_bytes(
        difflib.unified_diff, before.splitlines(True), addition.splitlines(True),
        fromfile=b"original", tofile=bytes(target))))
    # Link a complete file without replacing a concurrent administrator edit.
    with tempfile.NamedTemporaryFile(dir=target.parent, prefix=".borealis-") as stage:
        stage.write(addition)
        stage.flush()
        os.fchmod(stage.fileno(), 0o644)
        os.link(stage.name, target)
        try:
            corrected = checked(compiled(source), corrected_hash)
            subprocess.run(["apparmor_parser", "-K", "-r", source], check=True)
            verify_kernel(name, corrected, children)
        except BaseException:
            if os.path.samefile(stage.name, target):
                target.unlink()
                subprocess.run(["apparmor_parser", "-K", "-r", original], check=True)
            raise
    print(f"Verified enforcing {name}. Original and diff in {evidence}", flush=True)


def main(root):
    distro = platform.freedesktop_os_release()
    if (distro.get("ID"), distro.get("VERSION_ID")) != ("ubuntu", "26.04"):
        return
    enabled = Path("/sys/module/apparmor/parameters/enabled")
    if not enabled.exists() or not enabled.read_text().startswith("Y"):
        return
    version = subprocess.check_output(["dpkg-query", "-W", "-f=${Version}", "apparmor"], text=True)
    if version != "5.0.2-0ubuntu1~26.04.1":
        if EVIDENCE.exists() and any((EVIDENCE / name).is_dir() for name in (CRI, "wg-quick")):
            raise RuntimeError("AppArmor package changed. Review installed exceptions under #543 and #544.")
        return
    EVIDENCE.mkdir(mode=0o700, parents=True, exist_ok=True)
    with (EVIDENCE / ".lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        k3s = subprocess.check_output(["k3s", "--version"], text=True).splitlines()[0].split()
        if k3s[:3] == ["k3s", "version", "v1.36.3+k3s1"]:
            original = ASSETS / "cri-containerd.original"
            base = original.read_bytes()
            if base.count(SIGNAL) != 1:
                raise RuntimeError("Unexpected containerd policy template")
            addition = base.replace(SIGNAL, SIGNAL + PEER)
            reconcile(CRI, POLICY / CRI, POLICY / CRI, original, addition,
                      "56ae019dce23a1fb014baacce3e3b831ead438b682a62fa76473088ab1e7870d",
                      "c630a70d68d83a36637fa6f307cd064d458086d9a9dc13f5eadf433f73dcafbf", 0)
        elif (EVIDENCE / CRI).is_dir() and (POLICY / CRI).exists():
            raise RuntimeError("K3s version changed. Review the persisted containerd policy under #543.")
        vendor = POLICY / "wg-quick"
        if root == Path("/opt/Borealis") and vendor.exists():
            checked(vendor.read_bytes(), "2490a571adad6c67a269bb61555ee8182a91cbb798f1dee5134b7d7c629c5cc1")
            reconcile("wg-quick", POLICY / "local/wg-quick", vendor, vendor,
                      (ASSETS / "wg-quick.local").read_bytes(),
                      "6a39756f1e20f229b6bc31e643c4558c40078ab117c0f8de79cc6a77f8637f26",
                      "1ece7aeda07278518e0e6375f4c6c4ffa23c4a704588422831eab44a831396f6", 3)


if __name__ == "__main__":
    main(Path(sys.argv[1]))
