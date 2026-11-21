package clusterremote

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestFilesystemHostPackageDirectories(t *testing.T) {
	library, err := persistentFilesystemLibraries()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"all", "missing tails", "only etc", "unselected", "symlink", "dangling symlink", "regular file", "unsafe mode", "foreign owner", "mount escape", "device escape", "root drift"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", library+filesystemHostPackageFixture, t.TempDir(), mode).CombinedOutput()
			good := mode == "all" || mode == "missing tails" || mode == "only etc" || mode == "unselected"
			if (err == nil) != good || ctx.Err() != nil {
				t.Fatalf("package ancestor proof: %v; %s", err, out)
			}
		})
	}
}

func TestFilesystemHostPackageCommandBound(t *testing.T) {
	request, _ := filesystemFixture()
	request.RequirePersistent = true
	request.Paths = []string{"/etc", "/usr", "/var"}
	command, err := filesystemCommand(request)
	if err != nil || len(command) > 120<<10 || !strings.Contains(command, "pfs_package_directories") || !strings.Contains(command, "PFS_PACKAGE_DIRECTORIES") {
		t.Fatal("missing/unbounded package proof", len(command), err)
	}
}

// Real descriptor traversal and no-follow failures use a temporary directory.
// Only otherwise-unavailable owner/mount/root-drift values are synthetic.
const filesystemHostPackageFixture = `
import pathlib
ROOT, mode = sys.argv[1:]
EXPECTED_UID = os.getuid()
r = pathlib.Path(ROOT)
r.chmod(0o700)
os.umask(0o077)
paths = ["/etc", "/usr", "/var"]
if mode == "all":
    for p in PFS_PACKAGE_DIRECTORIES:
        (r/p.lstrip("/")).mkdir(parents=True,exist_ok=True)
else:
    for p in ("etc", "usr/share", "var"):
        (r/p).mkdir(parents=True,exist_ok=True)
if mode == "only etc": paths = ["/etc"]
if mode == "unselected": paths = ["/opt/Borealis"]
selected = r/"usr/share/doc"
if mode in ("symlink", "only etc", "unselected", "dangling symlink"):
    selected.symlink_to("/missing" if mode=="dangling symlink" else str(r/"etc"))
elif mode == "regular file": selected.write_text("not a directory")
elif mode in ("unsafe mode", "foreign owner", "mount escape", "device escape"):
    selected.mkdir()
    if mode == "unsafe mode": selected.chmod(0o777)
native_node, native_checked = fs_node, checked
root_reads=0
def fixture_node(fd):
    global root_reads
    node=list(native_node(fd))
    actual=os.readlink("/proc/self/fd/"+str(fd))
    if actual==ROOT:
        root_reads+=1
        if mode=="root drift" and root_reads>1: node[0]+=1
    if actual==str(selected):
        if mode=="mount escape": node[5]+=1
        if mode=="device escape": node[0]+=1
    return tuple(node)
fs_node=fixture_node
if mode=="foreign owner":
    def fixture_checked(info,directory=False):
        if info.st_ino==selected.stat().st_ino: raise ValueError()
        return native_checked(info,directory)
    checked=fixture_checked
root=os.open(ROOT,FS_OPEN)
try:
    count=len(os.listdir("/proc/self/fd"))
    observed=pfs_package_directories(root,paths)
    assert len(observed)==(40 if mode=="only etc" else 0 if mode=="unselected" else 360)
    assert len(os.listdir("/proc/self/fd"))==count
    if mode=="all": assert all(value is not None for _,value in observed)
    if mode=="missing tails": assert any(value is None for _,value in observed)
finally:
    os.close(root)
`
