package clusterremote

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func installNetworkBootFixture(t *testing.T, root string) {
	t.Helper()
	write := func(path, data string) {
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, unit := range []string{"systemd-networkd.service", "systemd-networkd.socket", "multi-user.target", "graphical.target", "sockets.target"} {
		write("usr/lib/systemd/system/"+unit, "[Unit]\nDescription=synthetic unit; native properties supplied by fixture\n")
	}
	for path, target := range map[string]string{
		"lib":                               "usr/lib",
		"etc/systemd/system/default.target": "multi-user.target",
		"etc/systemd/system/multi-user.target.wants/systemd-networkd.service": "/usr/lib/systemd/system/systemd-networkd.service",
		"etc/systemd/system/sockets.target.wants/systemd-networkd.socket":     "/lib/systemd/system/systemd-networkd.socket",
	} {
		path = filepath.Join(root, path)
		if _, err := os.Lstat(path); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	write("proc/self/ns/mnt", "mount namespace")
	if err := os.MkdirAll(filepath.Join(root, "proc/1/ns"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, "proc/self/ns/mnt"), filepath.Join(root, "proc/1/ns/mnt")); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkBootNativeCorrespondence(t *testing.T) {
	t.Run("graphical", func(t *testing.T) { testNetworkRenderCorrespondence(t, "boot graphical", true) })
	for _, mode := range []string{"success", "boot file drift", "boot manager drift", "boot property drift", "boot reload", "boot namespace", "boot hang", "boot overflow", "boot malformed", "boot boolean job", "boot condition", "boot runtime enabled", "boot PID", "boot unit path", "boot default", "boot environment", "boot executable", "boot transient", "boot fragment", "boot dropins", "boot invocation", "boot job", "boot signature", "boot missing property", "boot hook", "boot requires", "boot condition type", "boot manager reload", "boot socket PID", "host drift", "link drift", "tool drift"} {
		t.Run(mode, func(t *testing.T) { testNetworkRenderCorrespondence(t, mode, true) })
	}
}

// The production observer reads real fixture directories and spawns this fixed
// command child. Every accepted call checks exact bus destination/arguments;
// unrelated calls fall through to existing networkd/kernel command fixtures.
const networkBootCommandFixture = `
if pathlib.Path(sys.argv[0]).name == "busctl":
    assert args[:7] == ["--system", "--auto-start=no", "--allow-interactive-authorization=no", "--no-pager", "--timeout=2s", "--json=short", "call"]
    destination, path, interface, method = args[7:11]
    manager = "/org/freedesktop/systemd1"
    manager_interface = "org.freedesktop.systemd1.Manager"
    def emit(signature, value):
        print(json.dumps({"type": signature, "data": [value]}))
        sys.exit(0)
    if method == "GetNameOwner" and args[11:] == ["s", "org.freedesktop.systemd1"]:
        assert (destination, path, interface) == ("org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus")
        counter = root/"boot-round"
        n = int(counter.read_text())+1 if counter.exists() else 1
        counter.write_text(str(n))
        if MODE == "boot cloud marker drift" and n > 1:
            (root/"etc/cloud/cloud-init.disabled").unlink(missing_ok=True)
        if MODE == "boot namespace": (root/"proc/1/ns/mnt").unlink()
        if MODE == "boot file drift":
            with (root/"usr/lib/systemd/system/systemd-networkd.service").open("a") as output: output.write("# drift\n")
        emit("s", ":1.8" if MODE == "boot manager drift" and n > 1 else ":1.7")
    if method == "GetConnectionUnixProcessID":
        assert (destination, path, interface) == ("org.freedesktop.DBus", "/org/freedesktop/DBus", "org.freedesktop.DBus")
        assert args[11:] in (["s", ":1.7"], ["s", ":1.8"], ["s", ":1.42"])
        emit("u", 42 if args[-1] == ":1.42" else 2 if MODE == "boot PID" else 1)
    if destination in (":1.7", ":1.8"):
        if method == "ListUnitsByPatterns":
            assert (path, interface) == (manager, manager_interface)
            assert args[11:] == ["asas", "0", "3", "cloud-init*", "cloud-config.*", "cloud-final.*"]
            rows=[]
            if MODE.startswith("boot cloud ") and MODE != "boot cloud unloaded":
                unit="cloud-init-local.service"
                if MODE == "boot cloud unknown": unit="cloud-init-foreign.service"
                rows=[[unit,"Cloud-init local","loaded","active" if MODE == "boot cloud active" else "inactive","dead","",manager+"/unit/"+unit.replace("-","_2d").replace(".","_2e"),1 if MODE == "boot cloud job" else 0,"","/"]]
                if MODE == "boot cloud duplicate": rows += rows
            emit("a(ssssssouso)", rows)
        if method == "GetUnitProcesses":
            assert (path, interface) == (manager, manager_interface) and args[11:] == ["s","cloud-init-local.service"]
            emit("a(sus)", [["/system.slice/cloud-init-local.service",44,"private-argument"]] if MODE == "boot cloud orphan" else [])
        if MODE == "boot hang":
            (root/"generator-pid").write_text(str(os.getpid()))
            time.sleep(20)
        if MODE == "boot overflow":
            print("x"*131073); sys.exit(0)
        if MODE == "boot malformed":
            print('private-fixture'); sys.exit(0)
        if method == "GetUnit":
            assert (path, interface) == (manager, manager_interface) and args[11] == "s" and len(args) == 13
            unit = args[-1]
            if unit == "default.target": unit = "graphical.target" if MODE in ("boot default", "boot graphical") else "multi-user.target"
            emit("o", manager+"/unit/"+unit.replace("-", "_2d").replace(".", "_2e"))
        assert interface == "org.freedesktop.DBus.Properties"
        if method == "Get":
            assert path == manager and args[11:13] == ["ss", manager_interface] and len(args) == 14
            if args[-1] == "SystemState": emit("v", {"type": "s", "data": "running"})
            if args[-1] == "UnitsLoadTimestampMonotonic":
                counter = pathlib.Path(ROOT)/"manager-generation-reads"
                calls = int(counter.read_text())+1 if counter.exists() else 1
                counter.write_text(str(calls))
                emit("v", {"type": "t", "data": calls if MODE == "boot manager reload" else 0})
            assert args[-1] == "UnitPath"
            paths = ["/"+p for p in ("etc/systemd/system.control", "run/systemd/system.control", "run/systemd/transient", "run/systemd/generator.early", "etc/systemd/system", "etc/systemd/system.attached", "run/systemd/system", "run/systemd/system.attached", "run/systemd/generator", "usr/local/lib/systemd/system", "usr/lib/systemd/system", "run/systemd/generator.late")]
            if MODE == "boot unit path": paths.reverse()
            emit("v", {"type": "as", "data": paths})
        assert method == "GetAll" and args[11] == "s" and len(args) == 13
        unit = path.removeprefix(manager+"/unit/").replace("_2d", "-").replace("_2e", ".")
        values = {}
        def prop(name, signature, data): values[name] = {"type": signature, "data": data}
        if unit == "cloud-init-local.service":
            if args[-1] == "org.freedesktop.systemd1.Service":
                prop("MainPID","u",44 if MODE == "boot cloud process" else 0); prop("ControlPID","u",0)
            else:
                assert args[-1] == "org.freedesktop.systemd1.Unit"
                for name,data in {"Id":unit,"LoadState":"loaded","ActiveState":"inactive","SubState":"dead","SourcePath":"","FragmentPath":"/usr/lib/systemd/system/"+unit}.items(): prop(name,"s",data)
                prop("Names","as",[unit]); prop("DropInPaths","as",["/etc/systemd/system/cloud-init-local.service.d/private.conf"] if MODE == "boot cloud loaded dropin" else [])
                prop("Transient","b",False); prop("NeedDaemonReload","b",MODE == "boot cloud reload")
                prop("Job","(uo)",[0,"/"]); prop("LoadError","(ss)",["",""])
                prop("Conditions","a(sbbsi)",[["ConditionPathExists",0 if MODE == "boot cloud condition type" else MODE == "boot cloud trigger", MODE != "boot cloud condition","/etc/cloud/cloud-init.disabled",0]])
            emit("a{sv}",values)
        if args[-1] == "org.freedesktop.systemd1.Unit":
            assert unit in ("systemd-networkd.service", "systemd-networkd.socket", "multi-user.target", "sockets.target", "graphical.target")
            prop("Id", "s", unit)
            for name, data in {"LoadState": "loaded", "ActiveState": "active", "SubState": "running" if unit.endswith(".service") else "listening" if unit.endswith(".socket") else "active", "SourcePath": "", "FragmentPath": "/usr/lib/systemd/system/"+unit, "UnitFileState": "enabled" if unit.endswith((".service", ".socket")) else "static"}.items(): prop(name, "s", data)
            for name, data in {"Transient": False, "NeedDaemonReload": MODE == "boot reload", "ConditionResult": True, "AssertResult": True}.items(): prop(name, "b", data)
            prop("Names", "as", [unit]); prop("DropInPaths", "as", [])
            prop("InvocationID", "ay", [1]*16)
            prop("Job", "(uo)", [False if MODE == "boot boolean job" else 0, "/"])
            prop("LoadError", "(ss)", ["", ""])
            prop("Conditions", "a(sbbsi)", [["ConditionCapability", False, False, "CAP_NET_ADMIN", 0 if MODE == "boot condition" else 1]] if unit.endswith((".service", ".socket")) else [])
            prop("Asserts", "a(sbbsi)", [])
            prop("Requires", "as", ["basic.target"] if unit == "multi-user.target" else [])
            if unit == "graphical.target": prop("Requires", "as", ["multi-user.target"])
            prop("Before", "as", ["multi-user.target"]); prop("After", "as", ["systemd-networkd.socket"])
            prop("Wants", "as", ["systemd-networkd.service"] if unit == "multi-user.target" else ["systemd-networkd.socket"] if unit == "sockets.target" else ["network.target", "systemd-networkd.socket"] if unit.endswith(".service") else [])
            if MODE == "boot property drift" and unit.endswith(".service") and int((root/"boot-round").read_text()) > 1: values["Wants"]["data"].append("netplan-ovs-cleanup.service")
            if MODE == "boot runtime enabled": prop("UnitFileState", "s", "enabled-runtime")
            if MODE == "boot transient": prop("Transient", "b", True)
            if MODE == "boot fragment": prop("FragmentPath", "s", "/etc/systemd/system/"+unit)
            if MODE == "boot dropins": prop("DropInPaths", "as", ["/etc/systemd/system/service.d/override.conf"])
            if MODE == "boot invocation": prop("InvocationID", "ay", [0]*16)
            if MODE == "boot job": prop("Job", "(uo)", [2, "/org/freedesktop/systemd1/job/2"])
            if MODE == "boot signature": prop("NeedDaemonReload", "u", 0)
            if MODE == "boot missing property": del values["NeedDaemonReload"]
            if MODE == "boot requires": prop("Requires", "as", ["foreign.service"])
            if MODE == "boot condition type" and unit.endswith(".service"): values["Conditions"]["data"][0][1] = 0
        else:
            assert args[-1] == "org.freedesktop.systemd1.Service" and unit == "systemd-networkd.service"
            prop("MainPID", "u", 42); prop("ControlPID", "u", 0)
            if MODE == "boot socket PID": prop("MainPID", "u", 43)
            for name, data in {"BusName": "org.freedesktop.network1", "Type": "notify-reload", "User": "systemd-network", "RootDirectory": "", "RootImage": "", "WorkingDirectory": ""}.items(): prop(name, "s", data)
            for name in ("Environment", "PassEnvironment", "UnsetEnvironment"): prop(name, "as", ["OVERRIDE=yes"] if MODE == "boot environment" else [])
            prop("EnvironmentFiles", "a(sb)", [])
            for name in ("ExecStartPre", "ExecStartPost", "ExecCondition", "ExecReload"): prop(name, "a(sasbttttuii)", [])
            if MODE == "boot hook": prop("ExecStartPre", "a(sasbttttuii)", [["/tmp/hook", ["/tmp/hook"], False, 0, 0, 0, 0, 0, 0, 0]])
            executable = "/tmp/foreign-networkd" if MODE == "boot executable" else "/usr/lib/systemd/systemd-networkd"
            prop("ExecStart", "a(sasbttttuii)", [[executable, [executable], False, 1, 1, 0, 0, 42, 0, 0]])
        emit("a{sv}", values)
`

func TestNetworkBootFiles(t *testing.T) {
	// Exercise actual no-follow opens and real precedence/alias/drop-in trees,
	// not a second implementation of filesystem selection in Go.
	for _, mode := range []string{"success", "graphical", "gpt mask", "gpt runtime mask", "gpt executable", "runtime enablement", "missing socket", "default runtime", "default rescue", "service override", "service mask", "attached override", "early override", "late override", "generator run", "generator etc", "generator local", "alias", "named dropin", "alias dropin", "prefix dropin", "type dropin", "default dropin", "socket dropin", "dependency mask", "dependency alias", "unknown dependency", "cloud-init", "NetworkManager", "symlink directory", "writable unit", "hardlinked unit", "large unit", "entry bound"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			installNetworkBootFixture(t, root)
			mutate := `
r = pathlib.Path(ROOT)
os.umask(0o077)
def write(path, text="fixture"):
    p = r/path; p.parent.mkdir(parents=True, exist_ok=True); p.write_text(text)
def link(path, target):
    p = r/path; p.parent.mkdir(parents=True, exist_ok=True)
    if p.exists() or p.is_symlink(): p.unlink()
    p.symlink_to(target)
service = "systemd-networkd.service"
vendor = "usr/lib/systemd/system/"
persistent = "etc/systemd/system/multi-user.target.wants/"+service
if MODE == "graphical": link("etc/systemd/system/default.target", "/lib/systemd/system/graphical.target")
if MODE == "runtime enablement":
    (r/persistent).unlink(); link("run/systemd/system/multi-user.target.wants/"+service, "/"+vendor+service)
if MODE == "missing socket": (r/"etc/systemd/system/sockets.target.wants/systemd-networkd.socket").unlink()
if MODE == "default runtime": link("run/systemd/generator.early/default.target", "multi-user.target")
if MODE == "default rescue": link("etc/systemd/system/default.target", "rescue.target")
if MODE in ("service override", "attached override", "early override", "late override"):
    area = {"service override":"etc/systemd/system", "attached override":"etc/systemd/system.attached", "early override":"run/systemd/generator.early", "late override":"run/systemd/generator.late"}[MODE]
    write(area+"/"+service)
if MODE == "service mask": link("etc/systemd/system/"+service, "/dev/null")
if MODE == "gpt mask": link("etc/systemd/system-generators/systemd-gpt-auto-generator", "/dev/null")
if MODE == "gpt runtime mask": link("run/systemd/system-generators/systemd-gpt-auto-generator", "/dev/null")
if MODE == "gpt executable": write("etc/systemd/system-generators/systemd-gpt-auto-generator")
if MODE.startswith("generator "):
    area = {"generator run":"run", "generator etc":"etc", "generator local":"usr/local/lib"}[MODE]
    link(area+"/systemd/system-generators/netplan", "/dev/null")
if MODE == "alias": link("etc/systemd/system/foreign.service", service)
if MODE.endswith("dropin"):
    name = {"named dropin":service, "alias dropin":"dbus-org.freedesktop.network1.service", "prefix dropin":"systemd-.service", "type dropin":"service", "default dropin":"default.target", "socket dropin":"systemd-networkd.socket"}[MODE]
    write("etc/systemd/system/"+name+".d/change.conf")
if MODE == "dependency mask": link("run/systemd/system/multi-user.target.wants/"+service, "/dev/null")
if MODE == "dependency alias": link("etc/systemd/system/default.target.wants/"+service, "/tmp/foreign.service")
if MODE == "unknown dependency": link("etc/systemd/system/"+service+".wants/foreign.service", "/"+vendor+"foreign.service")
if MODE == "cloud-init": write(vendor+"cloud-init-local.service")
if MODE == "NetworkManager": link("etc/systemd/system/NetworkManager.service", "/dev/null")
if MODE == "symlink directory":
    (r/"etc/systemd/system").rename(r/"etc/systemd/other"); link("etc/systemd/system", "other")
if MODE == "writable unit": (r/(vendor+service)).chmod(0o666)
if MODE == "hardlinked unit": os.link(r/(vendor+service), r/"linked")
if MODE == "large unit": write(vendor+service, "x"*65537)
if MODE == "entry bound":
    for i in range(8193): write(vendor+"unrelated-"+str(i))
`
			script := persistentNetworkLibraryScript + activeNetworkLibraryScript + networkBootLibraryScript + "\nimport pathlib\nMODE=" + strconv.Quote(mode) + mutate + "\nmain(lambda: bool(boot_files(os.open(ROOT, os.O_RDONLY|os.O_DIRECTORY))))\n"
			// Keep the production entrypoint's private diagnostics and limits.
			script = strings.Replace(script, `ROOT = "/"`, "ROOT = "+strconv.Quote(root), 1)
			script = strings.Replace(script, "EXPECTED_UID = 0", "EXPECTED_UID = "+strconv.Itoa(os.Getuid()), 1)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script).Output()
			want := mode == "success" || mode == "graphical" || mode == "late override" || mode == "gpt mask"
			if ctx.Err() != nil || (err == nil) != want || want && !bytes.Equal(bytes.TrimSpace(out), []byte("true")) || !want && len(out) != 0 {
				t.Fatalf("boot file outcome: error=%v timeout=%v output bytes=%d", err, ctx.Err(), len(out))
			}
		})
	}
}

func TestNetworkBootNativePinnedScope(t *testing.T) { testNetworkRenderNativePinnedScope(t, true) }

func TestNetworkBootShellData(t *testing.T) { testNetworkRenderShellData(t, true) }
