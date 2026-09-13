package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"time"
)

var ErrNetworkRender = errors.New("SSH network render correspondence unavailable; host configuration withheld")

// NetworkRenderRequest comes from current independent peer/host observations.
// The observer receives only Targets; host/link expectations stay local.
type NetworkRenderRequest struct {
	MachineID string
	BootID    string
	Link      clusterbootstrap.ManagementLink
	Targets   RouteTargets
}

func (r NetworkRenderRequest) Validate() error {
	if !machineIDPattern.MatchString(r.MachineID) || r.MachineID == "00000000000000000000000000000000" ||
		!validPublicUUID(r.BootID) || r.Link.Validate() != nil || r.Targets.validate() != nil || !r.Link.MatchesAddress(r.Targets.Management) {
		return ErrNetworkRender
	}
	return nil
}

type networkRenderObservation struct {
	Version    int                       `json:"version"`
	Management managementLinkObservation `json:"management"`
}

// TargetNetworkRender proves native generated-file correspondence and current
// selection, not loaded bytes for every networkd setting, boot activation or
// reboot survival. It has no durable import or preparation authority.
type TargetNetworkRender struct {
	wire              networkRenderObservation
	target            Target
	key               HostKey
	started, finished time.Time
}

func (v TargetNetworkRender) Matches(notBefore time.Time, target Target, key HostKey, request NetworkRenderRequest) error {
	if notBefore.IsZero() || v.started.Before(notBefore) || v.finished.Before(v.started) || v.finished.After(time.Now()) ||
		v.finished.Sub(v.started) > 25*time.Second || target.Validate() != nil || key.Validate() != nil || v.target != target ||
		v.key.Algorithm != key.Algorithm || v.key.Fingerprint != key.Fingerprint || !bytes.Equal(v.key.PublicKey, key.PublicKey) ||
		v.wire.Version != 1 || v.wire.Management.validate() != nil || request.Validate() != nil || request.Targets.Management != target.Address ||
		v.wire.Management.Link != request.Link || v.wire.Management.Routing.Active.Declarations.MachineID != request.MachineID ||
		v.wire.Management.Routing.Active.Declarations.BootID != request.BootID ||
		v.wire.Management.Routing.Targets.Management != request.Targets.Management || !slices.Equal(v.wire.Management.Routing.Targets.Peers, request.Targets.Peers) {
		return ErrNetworkRender
	}
	return nil
}

func networkRenderCommand(targets RouteTargets) (string, error) {
	if targets.validate() != nil {
		return "", ErrNetworkRender
	}
	raw, err := json.Marshal(targets)
	if err != nil || len(raw) > 1024 {
		return "", ErrNetworkRender
	}
	return buildPrivilegedInspectionCommand("/usr/sbin:/usr/bin:/sbin:/bin", "exec /usr/bin/python3 -I -B -c "+shellConstant(networkRenderScript)+" "+shellConstant(base64.StdEncoding.EncodeToString(raw))+" 2>/dev/null"), nil
}

// InspectNetworkRender never changes installed networking or activates services.
// Native generation uses a private temporary root, cleaned before any result.
// check must retain each original target credential/claim and complete current
// cohort/VIP authority. Callback work honors context and joins its children.
func (client *Client) InspectNetworkRender(parent context.Context, sudoPassword []byte, expected Target, approved HostKey, request NetworkRenderRequest, check func(context.Context) error) (TargetNetworkRender, error) {
	started := time.Now()
	request.Targets.Peers = slices.Clone(request.Targets.Peers)
	approved.PublicKey = bytes.Clone(approved.PublicKey)
	command, err := networkRenderCommand(request.Targets)
	if err != nil || request.Validate() != nil || client == nil || client.ssh == nil || expected.Validate() != nil || approved.Validate() != nil || check == nil ||
		client.target != expected || request.Targets.Management != expected.Address || client.approved.Algorithm != approved.Algorithm ||
		client.approved.Fingerprint != approved.Fingerprint || !bytes.Equal(client.approved.PublicKey, approved.PublicKey) || parent.Err() != nil {
		return TargetNetworkRender{}, ErrNetworkRender
	}
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	boundary := func() bool {
		c, stop := context.WithTimeout(ctx, time.Second)
		defer stop()
		if c.Err() != nil || check(c) != nil || c.Err() != nil {
			cancel()
			return false
		}
		return true
	}
	if !boundary() {
		return TargetNetworkRender{}, ErrNetworkRender
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !boundary() {
					return
				}
			}
		}
	}()
	raw, err := client.inspectPrivilegedOutput(ctx, sudoPassword, command)
	close(stop)
	<-done
	if err != nil || !boundary() {
		return TargetNetworkRender{}, ErrNetworkRender
	}
	var wire networkRenderObservation
	if len(raw) == 0 || len(raw) > MaxOutputBytes || json.Unmarshal(raw, &wire) != nil {
		return TargetNetworkRender{}, ErrNetworkRender
	}
	canonical, err := json.Marshal(wire)
	v := TargetNetworkRender{wire: wire, target: expected, key: approved, started: started, finished: time.Now()}
	if err != nil || !bytes.Equal(canonical, bytes.TrimSpace(raw)) || v.Matches(started, expected, approved, request) != nil || ctx.Err() != nil {
		return TargetNetworkRender{}, ErrNetworkRender
	}
	return v, nil
}

const networkRenderScript = persistentNetworkLibraryScript + activeNetworkLibraryScript + routedNetworkLibraryScript + managementLinkLibraryScript + networkRenderLibraryScript + `
if __name__ == "__main__":
    main(observe_network_render)
`

const networkRenderLibraryScript = `
import shutil, signal, tempfile

def render_payload(state):
    import yaml
    output = io.StringIO()
    state._dump_yaml(output)
    raw = output.getvalue()
    if len(raw) > 65536:
        raise ValueError()
    network = yaml.safe_load(raw)["network"]
    if set(network) - {"version", "renderer", "ethernets"} or network.get("renderer", "networkd") != "networkd":
        raise ValueError()
    ethernets = network.get("ethernets", {})
    if not 1 <= len(ethernets) <= 64 or len(state) != len(ethernets):
        raise ValueError()
    # No authentication, device creation, match/rename, SR-IOV, OVS, hooks or
    # udev/link changes. Unknown settings stay unsupported. Native Netplan owns
    # validation and rendering of the complete remaining settings, including
    # DNS/routes and other Ethernet definitions, not only management addresses.
    allowed = {"renderer", "addresses", "dhcp4", "dhcp6", "optional", "accept-ra",
               "link-local", "nameservers", "routes", "gateway4", "gateway6", "ipv6-privacy"}
    for name, config in ethernets.items():
        if not re.fullmatch(r"[A-Za-z0-9_-]{1,15}", name) or set(config) - allowed or config.get("renderer", "networkd") != "networkd":
            raise ValueError()
    # Dumped state removes source comments/origins; excluded secret-bearing
    # definitions never reach disk. Preserve the whole accepted merged state.
    return raw.encode(), {"10-netplan-"+name+".network" for name in ethernets}

def render_tools(root):
    result = {}
    fd = directory(root, "usr/lib/systemd/system-generators")
    try:
        info = os.stat("netplan", dir_fd=fd, follow_symlinks=False)
        link = os.readlink("netplan", dir_fd=fd)
        if info.st_uid != EXPECTED_UID or not stat.S_ISLNK(info.st_mode) or link != "../../../libexec/netplan/generate":
            raise ValueError()
        result["generator"] = (info.st_dev, info.st_ino, info.st_mtime_ns, info.st_ctime_ns, link)
    finally:
        os.close(fd)
    for path in ("usr/libexec/netplan/generate", "usr/lib/x86_64-linux-gnu/libnetplan.so.1"):
        parent, name = path.rsplit("/", 1)
        fd = directory(root, parent)
        try:
            child = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC, dir_fd=fd)
            try:
                info = os.fstat(child)
                if not stat.S_ISREG(info.st_mode) or info.st_uid != EXPECTED_UID or info.st_mode & 0o022 or info.st_nlink != 1 or not 0 < info.st_size <= 4194304:
                    raise ValueError()
                if name == "generate" and not info.st_mode & 0o111:
                    raise ValueError()
                result[path] = (info.st_dev, info.st_ino, info.st_mode, info.st_uid, info.st_size, info.st_mtime_ns, info.st_ctime_ns)
            finally:
                os.close(child)
        finally:
            os.close(fd)
    return result

def network_file_snapshot(root):
    result, count, total = {}, 0, 0
    areas = ["lib", "usr/lib", "usr/local/lib", "run", "etc"]
    try:
        if stat.S_ISLNK(os.stat("lib", dir_fd=root, follow_symlinks=False).st_mode):
            if os.readlink("lib", dir_fd=root) not in ("usr/lib", "/usr/lib"):
                raise ValueError()
            areas.remove("lib")
    except FileNotFoundError:
        pass
    for area in areas:
        try:
            fd = directory(root, area+"/systemd/network")
        except FileNotFoundError:
            continue
        try:
            with os.scandir(fd) as entries:
                for entry in entries:
                    count += 1
                    if count > 1024:
                        raise ValueError()
                    if entry.name.endswith(".network"):
                        value = read_file(fd, entry.name)
                        total += len(value[1])
                        if len(result) >= 256 or total > 1048576:
                            raise ValueError()
                        result[(area, entry.name)] = value
                    elif entry.name.endswith(".network.d") or entry.name == "network.d":
                        child = directory(fd, entry.name)
                        try:
                            with os.scandir(child) as dropins:
                                for dropin in dropins:
                                    count += 1
                                    if count > 1024 or dropin.name.endswith(".conf"):
                                        raise ValueError()
                        finally:
                            os.close(child)
        finally:
            os.close(fd)
    return result

def native_render(root, payload, names):
    # Never invoke netplan CLI: even --root-dir calls udev/systemd reload.
    # Nor invoke generate in CLI mode: its JIT branch can start real units.
    # The installed C generator's fixed argv[0] selects generator mode. Every
    # root/output argument is our private directory, never a caller path.
    run = directory(root, "run")
    os.close(run)
    if not shutil.rmtree.avoids_symlink_attacks:
        raise ValueError()
    with tempfile.TemporaryDirectory(prefix=".borealis-network-render-", dir=ROOT+"/run") as scratch:
        os.chmod(scratch, 0o700)
        os.makedirs(scratch+"/etc/netplan", mode=0o700)
        with open(scratch+"/etc/netplan/observation.yaml", "xb") as output:
            os.chmod(output.fileno(), 0o600)
            output.write(payload)
        outputs = [scratch+"/run/systemd/"+name for name in ("generator", "generator.early", "generator.late")]
        for path in outputs:
            os.makedirs(path, mode=0o700)
        command = ["/usr/lib/systemd/system-generators/netplan", "--root-dir", scratch, *outputs]
        bounded_command(command)
        generated = os.open(scratch, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
        try:
            files = network_file_snapshot(generated)
            if set(files) != {("run", name) for name in names}:
                raise ValueError()
            return {name: files[("run", name)][1] for name in sorted(names)}
        finally:
            os.close(generated)

def render_selection(files, generated, name):
    selected = "10-netplan-"+name+".network"
    if selected not in generated:
        raise ValueError()
    effective = {}
    for area in ("lib", "usr/lib", "usr/local/lib", "run", "etc"):
        for (source, filename), value in files.items():
            if source == area:
                effective[filename] = (source, value[1])
    # Every generated named Ethernet basename must retain exactly the native
    # /run output at highest priority. Any earlier file may match management (including an alternative
    # interface name) at reload; do not guess its Match semantics or infer
    # exclusion from today's selected networkd file.
    for filename, data in generated.items():
        if not data or effective.get(filename) != ("run", data):
            raise ValueError()
    if any(filename < selected for filename in effective):
        raise ValueError()

def observe_network_render():
    route_targets() # Validate public argv before host reads or scratch writes.
    if os.geteuid() != EXPECTED_UID:
        raise ValueError()
    def interrupted(signum, frame):
        raise ValueError()
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGALRM, interrupted)
    signal.setitimer(signal.ITIMER_REAL, 12)
    resource.setrlimit(resource.RLIMIT_FSIZE, (65536, 65536))
    os.umask(0o077)
    root = os.open(ROOT, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC)
    try:
        checked(os.fstat(root), True)
        host, files, tools = identity(root), snapshot(root), render_tools(root)
        installed = network_file_snapshot(root)
        payload, names = render_payload(parse_snapshot(files))
        before = observe_management_link()
        generated = native_render(root, payload, names)
        management = observe_management_link()
        declarations = management["routing"]["active"]["declarations"]
        if before != management or [declarations["machine_id"], declarations["boot_id"]] != host:
            raise ValueError()
        render_selection(installed, generated, management["link"]["interface"])
        if host != identity(root) or files != snapshot(root) or tools != render_tools(root) or installed != network_file_snapshot(root):
            raise ValueError()
        return {"version": 1, "management": management}
    finally:
        os.close(root)
        signal.setitimer(signal.ITIMER_REAL, 0)
`
