package clusterremote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func filesystemFixture() (FilesystemRequest, filesystemObservation) {
	request := FilesystemRequest{MachineID: strings.Repeat("a", 32), BootID: "11111111-1111-4111-8111-111111111111", Paths: []string{"/opt/Borealis", "/var/lib/longhorn"}}
	wire := filesystemObservation{Version: 1, MachineID: request.MachineID, BootID: request.BootID, Evidence: FilesystemEvidence{
		MountNamespace: 4026531840, Receipt: strings.Repeat("a", 64),
		Paths: []FilesystemPath{
			{Path: request.Paths[0], Ancestor: "/opt", Inode: 123, MountID: 29, MountRoot: "/", MountPoint: "/", Filesystem: "0000000000000001"},
			{Path: request.Paths[1], Ancestor: "/var/lib", Inode: 124, MountID: 29, MountRoot: "/", MountPoint: "/", Filesystem: "0000000000000001"},
		},
		Filesystems: []FilesystemCapacity{{ID: "0000000000000001", Device: "8:1", Type: "ext4", TotalBytes: 100 << 30, AvailableBytes: 70 << 30}},
	}}
	return request, wire
}

func TestFilesystemPinnedObservation(t *testing.T) {
	for _, mode := range []string{"success", "persistent", "persistent downgrade", "wrong port", "wrong key", "machine", "boot", "namespace", "receipt", "path", "ancestor", "mount point", "mount ID", "unknown FS", "duplicate FS", "FS type", "FS device", "overflow", "free overflow", "unknown field", "duplicate field", "alias field", "null", "trailing", "private error", "oversized", "authority lost", "cancel", "joined heartbeat", "late heartbeat failure"} {
		t.Run(mode, func(t *testing.T) {
			request, wire := filesystemFixture()
			if strings.HasPrefix(mode, "persistent") {
				request.RequirePersistent = true
				if mode == "persistent" {
					wire.Version = 2
				}
			}
			switch mode {
			case "machine":
				wire.MachineID = strings.Repeat("b", 32)
			case "boot":
				wire.BootID = "22222222-2222-4222-8222-222222222222"
			case "namespace":
				wire.Evidence.MountNamespace = 0
			case "receipt":
				wire.Evidence.Receipt = "invalid"
			case "path":
				wire.Evidence.Paths[0].Path = "/different"
			case "ancestor":
				wire.Evidence.Paths[0].Ancestor = "/opt/Borealis/missing"
			case "mount point":
				wire.Evidence.Paths[0].MountPoint = "/other"
			case "mount ID":
				wire.Evidence.Paths[0].MountID = 0
			case "unknown FS":
				wire.Evidence.Paths[0].Filesystem = "0000000000000002"
			case "duplicate FS":
				wire.Evidence.Filesystems = append(wire.Evidence.Filesystems, wire.Evidence.Filesystems[0])
			case "FS type":
				wire.Evidence.Filesystems[0].Type = "overlay"
			case "FS device":
				wire.Evidence.Filesystems[0].Device = "08:1"
			case "overflow":
				wire.Evidence.Filesystems[0].TotalBytes = 1 << 53
			case "free overflow":
				wire.Evidence.Filesystems[0].AvailableBytes = wire.Evidence.Filesystems[0].TotalBytes + 1
			}
			raw, _ := json.Marshal(wire)
			switch mode {
			case "unknown field":
				raw = append([]byte(`{"extra":1,`), raw[1:]...)
			case "duplicate field":
				raw = append([]byte(`{"version":1,`), raw[1:]...)
			case "alias field":
				raw = bytes.Replace(raw, []byte(`"version"`), []byte(`"Version"`), 1)
			case "null":
				raw = []byte("null")
			case "trailing":
				raw = append(raw, []byte("{}")...)
			case "oversized":
				raw = bytes.Repeat([]byte("x"), MaxOutputBytes+1)
			}
			command, err := filesystemCommand(request)
			if err != nil {
				t.Fatal(err)
			}
			entered, returned := make(chan struct{}), make(chan struct{})
			server := newFakeSSH(t, "filesystem", func(got string, ch ssh.Channel) uint32 {
				stdin, _ := io.ReadAll(ch)
				if got != command || !bytes.Equal(stdin, []byte("private-sudo\n")) {
					return 1
				}
				if mode == "private error" {
					ch.Stderr().Write([]byte("private-sudo"))
					return 1
				}
				if strings.Contains(mode, "heartbeat") {
					<-entered
				}
				ch.Write(raw)
				return 0
			})
			credential, err := PasswordCredential("operator", server.password)
			if err != nil {
				t.Fatal(err)
			}
			defer credential.Destroy()
			client, err := server.transport.Connect(context.Background(), server.target, server.key, credential)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var checks atomic.Int32
			check := func(ctx context.Context) error {
				n := checks.Add(1)
				if n == 2 {
					switch mode {
					case "authority lost":
						return errors.New("private-authority")
					case "cancel":
						cancel()
					case "joined heartbeat", "late heartbeat failure":
						close(entered)
						select {
						case <-ctx.Done():
							return errors.New("premature heartbeat cancellation")
						case <-time.After(60 * time.Millisecond):
						}
						close(returned)
						if mode == "late heartbeat failure" {
							return errors.New("late private error")
						}
					}
				}
				return nil
			}
			target, key := server.target, server.key
			if mode == "wrong port" {
				target.Port++
			}
			if mode == "wrong key" {
				key = newFakeSSH(t, "other").key
			}
			started := time.Now()
			value, err := client.InspectFilesystem(ctx, []byte("private-sudo"), target, key, request, check)
			if mode != "success" && mode != "persistent" && mode != "joined heartbeat" {
				if err != ErrFilesystem || !reflect.DeepEqual(value, TargetFilesystem{}) {
					t.Fatal("unsafe observation", err)
				}
				if strings.HasPrefix(mode, "wrong ") && (checks.Load() != 0 || server.execCalls.Load() != 0) {
					t.Fatal("approval crossed boundary")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "joined heartbeat" {
				select {
				case <-returned:
				default:
					t.Fatal("callback outlived success")
				}
			}
			got, err := value.Evidence(started, target, key, request)
			if err != nil || !reflect.DeepEqual(got, wire.Evidence) {
				t.Fatal("valid observation rejected", err)
			}
			got.Paths[0].Path = "/mutated"
			got.Filesystems[0].AvailableBytes = 0
			again, _ := value.Evidence(started, target, key, request)
			if !reflect.DeepEqual(again, wire.Evidence) {
				t.Fatal("mutable result alias")
			}
			for _, bad := range []string{"old", "zero time", "endpoint", "key", "machine", "boot", "paths", "proof mode", "serialized"} {
				floor, endpoint, pin, req, copy := started, target, key, request, value
				switch bad {
				case "old":
					floor = time.Now()
				case "zero time":
					floor = time.Time{}
				case "endpoint":
					endpoint.Port++
				case "key":
					pin = newFakeSSH(t, "changed").key
				case "machine":
					req.MachineID = strings.Repeat("b", 32)
				case "boot":
					req.BootID = "22222222-2222-4222-8222-222222222222"
				case "paths":
					req.Paths = []string{"/other"}
				case "proof mode":
					req.RequirePersistent = !req.RequirePersistent
				case "serialized":
					encoded, _ := json.Marshal(value)
					copy = TargetFilesystem{}
					_ = json.Unmarshal(encoded, &copy)
				}
				if _, err := copy.Evidence(floor, endpoint, pin, req); err != ErrFilesystem {
					t.Fatal("unbound evidence", bad)
				}
			}
		})
	}
}

func TestFilesystemNativeObservation(t *testing.T) {
	for _, mode := range []string{"success", "decreasing", "increasing", "xfs", "bind mount", "spaces", "symlink", "file", "mount drift", "identity drift", "directory drift", "namespace drift", "FSID drift", "device mismatch", "readonly mount", "readonly super", "readonly stat", "unsupported", "negative free", "reserved overflow", "free overflow", "total overflow", "zero unit", "zero FSID", "duplicate mount", "unknown escape", "mount bound", "missing mount", "extra fdinfo", "same device different FSID", "permission denied"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "python3", "-I", "-B", "-c", filesystemLibraryScript+filesystemNativeFixture, t.TempDir(), mode)
			out, err := cmd.CombinedOutput()
			good := mode == "success" || mode == "decreasing" || mode == "increasing" || mode == "xfs" || mode == "bind mount" || mode == "spaces"
			if (err == nil) != good || ctx.Err() != nil {
				t.Fatalf("native outcome %v: %s", err, out)
			}
			if good {
				var wire filesystemObservation
				if json.Unmarshal(out, &wire) != nil || len(wire.Evidence.Paths) != 2 || len(wire.Evidence.Filesystems) != 1 {
					t.Fatal("shared filesystem not combined")
				}
				want := uint64(40 * 4096)
				if mode == "decreasing" {
					want = 20 * 4096
				}
				if wire.Evidence.Filesystems[0].AvailableBytes != want {
					t.Fatal("reserved blocks or higher reading credited")
				}
				paths := []string{wire.Evidence.Paths[0].Path, wire.Evidence.Paths[1].Path}
				if wire.Evidence.validate(paths) != nil {
					t.Fatal("invalid native projection")
				}
			}
		})
	}
}

func TestFilesystemPathContract(t *testing.T) {
	corpus := [][]string{
		{"/"}, {"/opt/Borealis", "/var/lib/longhorn"}, {"/space <&> ' \" $()"},
		{}, {""}, {"relative"}, {"//opt"}, {"/opt/"}, {"/opt//sub"}, {"/opt/../sub"}, {"/opt/./sub"},
		{"/b", "/a"}, {"/a", "/a"}, {"/line\nbreak"}, {"/tab\there"}, {"/nul\x00"}, {"/nonascii-é"},
		{"/" + strings.Repeat("a", 256)}, {strings.Repeat("/a", 65)}, {"/a", "/b", "/c", "/d", "/e", "/f", "/g", "/h", "/i"},
	}
	for i, paths := range corpus {
		request, _ := filesystemFixture()
		request.Paths = paths
		good := i < 3
		if (request.Validate() == nil) != good {
			t.Fatalf("Go path contract: %d", i)
		}
	}
	raw, _ := json.Marshal(corpus)
	script := filesystemLibraryScript + `
def forbidden(paths):
    return {"evidence": {"filesystems": []}}
fs_snapshot = forbidden
for index, paths in enumerate(json.loads(sys.argv[1])):
    try:
        observe_filesystems(paths)
        good = True
    except ValueError:
        good = False
    assert good == (index < 3), index
`
	cmd := exec.Command("python3", "-I", "-B", "-c", script, string(raw))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Python path contract %v: %s", err, out)
	}
}

func TestFilesystemHostIdentity(t *testing.T) {
	for _, mode := range []string{"success", "machine", "zero machine", "boot", "namespace", "root"} {
		t.Run(mode, func(t *testing.T) {
			script := filesystemLibraryScript + `
import types
mode = sys.argv[1]
def read(path, limit):
    if path == "/etc/machine-id": return b"bad" if mode == "machine" else b"0"*32 if mode == "zero machine" else b"a"*32+b"\n"
    if path == "/proc/sys/kernel/random/boot_id": return b"bad" if mode == "boot" else b"11111111-1111-4111-8111-111111111111\n"
    raise AssertionError(path)
fs_read = read
def info(path):
    ino = 4 if path.endswith("/mnt") else 2
    if path == "/proc/1/ns/mnt" and mode == "namespace": ino += 1
    if path == "/proc/1/root" and mode == "root": ino += 1
    return types.SimpleNamespace(st_dev=1, st_ino=ino)
os.stat = info
assert fs_identity() == ("a"*32, "11111111-1111-4111-8111-111111111111", 4, 1, 2)
`
			cmd := exec.Command("python3", "-I", "-B", "-c", script, mode)
			if out, err := cmd.CombinedOutput(); (err == nil) != (mode == "success") {
				t.Fatalf("identity boundary %v: %s", err, out)
			}
		})
	}
}

func TestFilesystemShellDataArgument(t *testing.T) {
	for _, mode := range []string{"password", "nopasswd", "root"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(root, "must-not-execute")
			for name, body := range map[string]string{
				"id": "#!/bin/sh\nif [ \"${FIXTURE_ROOT:-}\" = 1 ]; then printf 0; else printf 1000; fi\n",
				"sudo": `#!/bin/sh
test "$1" = -k && test "$2" = -S && test "$3" = -p && test "$4" = '' && test "$5" = -- || exit 2
shift 5
if [ "$FIXTURE_MODE" = password ]; then IFS= read -r supplied || exit 3; fi
export FIXTURE_ROOT=1
exec "$@"
`,
			} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			request, _ := filesystemFixture()
			request.Paths = []string{"/quotes ' \" <&> $(touch " + marker + ")"}
			command, err := filesystemCommand(request)
			if err != nil || strings.Contains(command, request.MachineID) || strings.Contains(command, request.BootID) {
				t.Fatal("private expected identity crossed argv", err)
			}
			command = strings.ReplaceAll(command, "/usr/sbin:/usr/bin:/sbin:/bin", root+":/usr/bin:/bin")
			// Preserve full production quoting and decoding; replace only final
			// host observation so this fixture never probes installed storage.
			command = strings.ReplaceAll(command, "result = observe_filesystems(paths)", "result = [fs_path(item) for item in paths]")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			process := exec.CommandContext(ctx, "/bin/sh", "-c", command)
			process.Env = append(os.Environ(), "FIXTURE_MODE="+mode)
			if mode == "root" {
				process.Env = append(process.Env, "FIXTURE_ROOT=1")
			}
			process.Stdin = strings.NewReader("touch " + shellConstant(marker) + "; $(touch " + shellConstant(marker) + ")\n")
			raw, err := process.Output()
			expected, _ := json.Marshal(request.Paths)
			if err != nil || !bytes.Equal(bytes.TrimSpace(raw), expected) {
				t.Fatal("selected paths or sudo framing", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("data became executable source")
			}
		})
	}
}

// Real directory descriptors, O_NOFOLLOW walking and proc fdinfo; synthetic
// statvfs/mount inventory avoid requiring root or mounting anything in tests.
const filesystemNativeFixture = `
import pathlib, types
root, mode = pathlib.Path(sys.argv[1]), sys.argv[2]
if mode == "spaces":
    root = root/"spaces <&> ' $()"
    root.mkdir()
(root/"install").mkdir()
(root/"storage").mkdir()
if mode == "symlink":
    (root/"storage").rmdir()
    (root/"storage").symlink_to(root/"install")
if mode == "file":
    (root/"storage").rmdir()
    (root/"storage").write_text("file")
fd = os.open(str(root), FS_OPEN)
mnt, dev = fs_mnt_id(fd), os.fstat(fd).st_dev
os.close(fd)
device = str(os.major(dev))+":"+str(os.minor(dev))
raw = (str(mnt)+" 1 "+device+" / / rw,relatime - ext4 /dev/fixture rw\n").encode()
if mode == "bind mount":
    # Distinct descriptor mount ID, same physical device and f_fsid.
    native_mnt_id = fs_mnt_id
    def bind_mnt_id(fd):
        return mnt+1 if os.readlink("/proc/self/fd/"+str(fd)).startswith(str(root/"storage")) else native_mnt_id(fd)
    fs_mnt_id = bind_mnt_id
    raw += (str(mnt+1)+" "+str(mnt)+" "+device+" /original "+str(root/"storage")+" rw - ext4 /dev/fixture rw\n").encode()
if mode == "device mismatch": raw = raw.replace(device.encode(), b"123:456", 1)
if mode == "readonly mount": raw = raw.replace(b"rw,relatime", b"ro,relatime")
if mode == "readonly super": raw = raw.replace(b"fixture rw", b"fixture ro")
if mode == "unsupported": raw = raw.replace(b"ext4", b"btrfs")
if mode == "xfs": raw = raw.replace(b"ext4", b"xfs")
if mode == "duplicate mount": raw += raw
if mode == "unknown escape": raw = raw.replace(b" / / ", br" / /bad\999 ")
if mode == "mount bound": raw *= 4097
if mode == "missing mount": raw = raw.replace(str(mnt).encode(), str(mnt+1).encode(), 1)
if mode == "permission denied":
    native_open = os.open
    def denied(path, flags, **kwargs):
        if path == "storage": raise PermissionError()
        return native_open(path, flags, **kwargs)
    os.open = denied
real_read, reads, identities, calls = fs_read, 0, 0, 0
def read(path, limit):
    global reads
    if path == "/proc/self/mountinfo":
        reads += 1
        value = raw
        if mode == "mount drift" and reads > 1: value = raw.replace(b"relatime", b"noatime")
        if len(value) > limit: raise ValueError()
        return value
    value = real_read(path, limit)
    if mode == "extra fdinfo" and "/fdinfo/" in path: value += b"mnt_id:\t123\n"
    return value
fs_read = read
def identity():
    global identities
    identities += 1
    return ("b"*32 if mode == "identity drift" and identities > 1 else "a"*32, "11111111-1111-4111-8111-111111111111", 22 if mode == "namespace drift" and identities > 1 else 21, dev, 2)
fs_identity = identity
def capacity(fd):
    global calls
    calls += 1
    available = 20 if mode == "decreasing" and calls > 2 else 60 if mode == "increasing" and calls > 2 else 40
    values = dict(f_frsize=4096, f_blocks=100, f_bavail=available, f_bfree=80, f_fsid=123, f_flag=0)
    if mode == "readonly stat": values["f_flag"] = os.ST_RDONLY
    if mode == "negative free": values["f_bavail"] = -1
    if mode == "reserved overflow": values["f_bavail"] = 81
    if mode == "free overflow": values["f_bfree"] = 101
    if mode == "total overflow": values["f_blocks"] = FS_LIMIT
    if mode == "zero unit": values["f_frsize"] = 0
    if mode == "zero FSID": values["f_fsid"] = 0
    if mode == "FSID drift" and calls > 2: values["f_fsid"] = 124
    if mode == "same device different FSID" and calls == 2: values["f_fsid"] = 124
    if mode == "directory drift" and calls == 2: (root/"install").chmod(0o700)
    return types.SimpleNamespace(**values)
os.fstatvfs = capacity
paths = [str(root/"install"/"new"), str(root/"storage"/"new")]
result = observe_filesystems(paths)
assert not (root/"install"/"new").exists() and not (root/"storage"/"new").exists()
print(json.dumps(result, separators=(",", ":")))
`
