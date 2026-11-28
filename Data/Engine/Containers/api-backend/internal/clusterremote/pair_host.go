package clusterremote

import (
	"context"
	"net/netip"
	"strconv"
	"strings"
)

// Check the root backing disk and the endpoint K3s will actually use. The
// existing pinned SSH connection and privileged runner supply target custody.
func (client *Client) InspectPairSystem(ctx context.Context, sudo []byte, vip string) (uint64, error) {
	ip, err := netip.ParseAddr(vip)
	if err != nil || !ip.Is4() || !ip.IsPrivate() || ip.String() != vip {
		return 0, ErrPreparationProposal
	}
	const program = `import os,pathlib,socket,sys
device=os.stat("/").st_dev
root=pathlib.Path("/sys/dev/block/%d:%d"%(os.major(device),os.minor(device))).resolve(strict=True)
def disks(node, depth):
    if depth>12: raise ValueError()
    if (node/"partition").exists(): return disks(node.parent,depth+1)
    slaves=list((node/"slaves").iterdir())
    if slaves: return set.union(*(disks(n.resolve(strict=True),depth+1) for n in slaves))
    if "/virtual/" in str(node): raise ValueError()
    return {node}
physical=disks(root,0)
if len(physical)!=1: raise ValueError()
size=int((physical.pop()/"size").read_text())*512
with socket.create_connection((sys.argv[1],6443),timeout=4): pass
print(size)`
	command := buildPrivilegedInspectionCommand("/usr/sbin:/usr/bin:/sbin:/bin", "exec /usr/bin/python3 -I -B -c "+shellConstant(program)+" "+shellConstant(vip)+" 2>/dev/null")
	out, err := client.inspectPrivilegedOutput(ctx, sudo, command)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || n == 0 {
		return 0, ErrPreparationProposal
	}
	return n, nil
}
