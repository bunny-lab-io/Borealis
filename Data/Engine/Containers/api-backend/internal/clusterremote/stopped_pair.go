package clusterremote

import (
	"context"
	"strings"
)

// This is the stopped-pair read, before fresh credentials are committed. The
// normal supervised native journal still performs effect and quiescence proof
// after fencing. No lock file, journal or installation directory is created.
const stoppedPairRead = `import fcntl, hashlib, os, stat, subprocess
from pathlib import Path
root = Path("/var/lib/borealis/cluster-bootstrap")
def quiescent():
    states = subprocess.check_output(["/usr/bin/systemctl", "list-units", "--no-legend", "--plain",
        "--state=activating,active,reloading,deactivating", "borealis-bootstrap-*.service"])
    if states.strip() or list(Path("/sys/fs/cgroup/system.slice").glob("borealis-bootstrap-*.service")):
        raise ValueError("executor")
quiescent()
for path in ["/var/lib/rancher/k3s/server/db/etcd/member", "/etc/rancher/k3s/config.yaml",
             "/opt/Borealis/Engine/Services/api-backend/secrets", "/var/lib/borealis/cluster-bootstrap/identity-attempt"]:
    if os.path.lexists(path): raise ValueError("host in use")
digest = "absent"
if os.path.lexists(root):
    if root.resolve(strict=True) != root: raise ValueError("journal path")
    lock = os.open(root/"mutation.lock", os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
    fcntl.flock(lock, fcntl.LOCK_EX|fcntl.LOCK_NB)
    with os.fdopen(os.open(root/"mutation.json",os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK),"rb") as file:
        info=os.fstat(file.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or info.st_mode & 0o077 or info.st_size > 1048576: raise ValueError("journal")
        digest=hashlib.sha256(file.read()).hexdigest()
quiescent()
print(Path("/etc/machine-id").read_text().strip(),Path("/proc/sys/kernel/random/boot_id").read_text().strip(),digest)
`

func (c *Client) CheckStoppedPairTarget(ctx context.Context, sudo []byte, machine, boot, journal string) error {
	cmd := buildPrivilegedInspectionCommand("/usr/sbin:/usr/bin:/sbin:/bin", "exec /usr/bin/python3 -I -B -c "+shellConstant(stoppedPairRead)+" 2>/dev/null")
	out, err := c.inspectPrivilegedOutput(ctx, sudo, cmd)
	if err != nil || strings.TrimSpace(string(out)) != machine+" "+boot+" "+journal {
		return ErrBootstrapVerification
	}
	return nil
}
