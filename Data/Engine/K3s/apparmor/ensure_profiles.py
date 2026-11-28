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
import stat
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


WG_PROJECTED_HASH = "c0db79965e1839874765fca6fda09220bebbcf5e9dc0642440f01a74622bc1df"
WG_STANDALONE_KEY = b"  /opt/Borealis/Engine/Services/wireguard-tunnel/secrets/server_private.key r,\n"


def projected_wireguard_key_rule():
    # Kubernetes' AtomicWriter names a generation ..YYYY_MM_DD_HH_MM_SS.<uint32>.
    digit = "[0-9]"
    timestamp = "_".join(digit * width for width in (4, 2, 2, 2, 2, 2))
    suffix = "{" + ",".join(digit * width for width in range(1, 11)) + "}"
    root = "/opt/Borealis/Engine/Services/wireguard-tunnel/secrets"
    return f"  {root}/..{timestamp}.{suffix}/server_private.key r,\n".encode()


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


def extend_projected_wireguard_include(target, vendor, evidence, prior, addition, corrected_hash):
    # Only extend the exact previously installed and kernel-verified Borealis rule.
    # Append on its verified inode to avoid replacing a concurrent administrator edit.
    previous = checked(compiled(vendor), WG_PROJECTED_HASH)
    verify_kernel("wg", previous, 0)
    with os.fdopen(os.open(target, os.O_RDWR | os.O_APPEND | os.O_NOFOLLOW), "r+b", buffering=0) as stream:
        current = os.fstat(stream.fileno())
        if (not stat.S_ISREG(current.st_mode) or current.st_nlink != 1
                or not os.path.samestat(current, target.stat()) or stream.read() != prior):
            raise RuntimeError("Preserve changed WireGuard administrator policy")
        (evidence / "compiled.projected").write_bytes(previous)
        (evidence / "local.projected").write_bytes(prior)
        (evidence / "standalone.diff").write_bytes(b"".join(difflib.diff_bytes(
            difflib.unified_diff, prior.splitlines(True), addition.splitlines(True),
            fromfile=b"projected", tofile=bytes(target))))
        try:
            if stream.write(WG_STANDALONE_KEY) != len(WG_STANDALONE_KEY):
                raise OSError("Incomplete WireGuard policy append")
            os.fsync(stream.fileno())
            corrected = checked(compiled(vendor), corrected_hash)
            subprocess.run(["apparmor_parser", "-K", "-r", vendor], check=True)
            verify_kernel("wg", corrected, 0)
        except BaseException:
            stream.seek(0)
            after = stream.read()
            if os.path.samestat(current, target.stat()) and after.startswith(prior) and addition.startswith(after):
                stream.truncate(len(prior))
                os.fsync(stream.fileno())
                subprocess.run(["apparmor_parser", "-K", "-r", vendor], check=True)
                verify_kernel("wg", previous, 0)
            raise


def reconcile(name, target, source, original, addition, baseline_hash, corrected_hash, children):
    if target.is_symlink() or target.parent.is_symlink():
        raise RuntimeError(f"Refusing symlink at {target}")
    existing = target.read_bytes() if target.exists() else None
    evidence = EVIDENCE / name
    if name == "wg" and existing == projected_wireguard_key_rule() and addition == existing + WG_STANDALONE_KEY:
        checked((evidence / "compiled.original").read_bytes(), baseline_hash)
        extend_projected_wireguard_include(target, source, evidence, existing, addition, corrected_hash)
        return
    if existing is not None and existing != addition:
        raise RuntimeError(f"Preserve existing administrator policy at {target}")
    if existing is not None:
        corrected = checked(compiled(source), corrected_hash)
        try:
            verify_kernel(name, corrected, children)
        except (ValueError, RuntimeError):
            # A previous run may have persisted the include just before a reboot.
            # Only replace the recorded, reviewed baseline in that case.
            baseline = checked((evidence / "compiled.original").read_bytes(), baseline_hash)
            try:
                verify_kernel(name, baseline, children, allow_absent=name == CRI)
            except (ValueError, RuntimeError):
                if name != "wg" or not (evidence / "compiled.projected").is_file():
                    raise
                previous = checked((evidence / "compiled.projected").read_bytes(), WG_PROJECTED_HASH)
                verify_kernel(name, previous, children)
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


def main(root, k3s_binary):
    distro = platform.freedesktop_os_release()
    if (distro.get("ID"), distro.get("VERSION_ID")) != ("ubuntu", "26.04"):
        return
    enabled = Path("/sys/module/apparmor/parameters/enabled")
    if not enabled.exists() or not enabled.read_text().startswith("Y"):
        return
    version = subprocess.check_output(["dpkg-query", "-W", "-f=${Version}", "apparmor"], text=True)
    if version != "5.0.2-0ubuntu1~26.04.1":
        if EVIDENCE.exists() and any((EVIDENCE / name).is_dir() for name in (CRI, "wg-quick", "wg")):
            raise RuntimeError("AppArmor package changed. Review installed exceptions under #543 and #544.")
        return
    EVIDENCE.mkdir(mode=0o700, parents=True, exist_ok=True)
    with (EVIDENCE / ".lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        k3s = subprocess.check_output([k3s_binary, "--version"], text=True).splitlines()[0].split()
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
        vendor = POLICY / "wg"
        if root == Path("/opt/Borealis") and vendor.exists():
            checked(vendor.read_bytes(), "8113198a4ae411072bdeb8774c5353512b7e501c24f5d04161eed8d5b17a5c08")
            reconcile("wg", POLICY / "local/wg", vendor, vendor,
                      projected_wireguard_key_rule() + WG_STANDALONE_KEY,
                      "90e72cb9a3ed22b813a55b16a5fd3091135c2c4b8ae515120b009d6c798119bd",
                      "328e5e0222cb6ffaf695f3d40a439d073494715c11186b206b87e8c4e4c77aa0", 0)


if __name__ == "__main__":
    main(Path(sys.argv[1]), sys.argv[2])
