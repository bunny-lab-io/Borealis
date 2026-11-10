package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func preparationProposalFixture() PreparationProposal {
	plan, _ := clusterbootstrap.PreparationTargetCPUPlanDigest(clusterbootstrap.K3sPins().Version)
	return PreparationProposal{Version: PreparationProposalVersion, CPUPlanSHA256: plan,
		Source:    clusterbootstrap.Expected{Repository: "bunny-lab-io/Borealis", Release: "2026.11.1", SourceSHA: strings.Repeat("a", 40)},
		ClusterID: "11111111-1111-4111-8111-111111111111", OperationID: "22222222-2222-4222-8222-222222222222", OperationAttempt: 1,
		ControllerHolder: "original-controller", PlanSHA256: strings.Repeat("b", 64), TargetID: "33333333-3333-4333-8333-333333333333",
		QualificationGeneration: 4, Address: "192.168.3.251", Port: 22, HostKeyFingerprint: "SHA256:" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)),
		Hostname: "engine-02", MachineID: strings.Repeat("a", 32), BootID: "44444444-4444-4444-8444-444444444444", Nonce: strings.Repeat("c", 64)}
}

func preparationProofFixture(p PreparationProposal) PreparationProposalProof {
	digest, _ := p.RequestSHA256()
	return PreparationProposalProof{Version: PreparationProposalVersion, RequestSHA256: digest, Nonce: p.Nonce, ObservationSHA256: strings.Repeat("d", 64), CPU: clusterbootstrap.PreparationTargetCPU{Version: 1, PlanSHA256: p.CPUPlanSHA256, OnlineCPUs: "0-15", AllocatableMilli: 16000, MemoryKiB: 33554432, ObservationSHA256: strings.Repeat("e", 64)}}
}

func TestPreparationProposalContract(t *testing.T) {
	p := preparationProposalFixture()
	proof := preparationProofFixture(p)
	if p.Validate() != nil || proof.Validate(p) != nil {
		t.Fatal("valid waiting proposal rejected")
	}
	maximum := p
	maximum.ControllerHolder = strings.Repeat("x", 1024)
	if maximum.Validate() != nil {
		t.Fatal("bounded maximum holder rejected")
	}
	maximum.ControllerHolder = strings.Repeat("<", 1024)
	if maximum.Validate() == nil {
		t.Fatal("escaped public argument exceeds fixed remote bound")
	}
	command := preparationProposalCommand(p)
	if len(command) >= 64<<10 || exec.Command("/bin/sh", "-n", "-c", command).Run() != nil {
		t.Fatal("fixed command exceeds protocol bound or has invalid shell syntax")
	}
	for _, change := range []func(*PreparationProposal){
		// Original clusterSSHInspectionReport.valid requires lowercase hostname;
		// proposal must preserve it exactly, never normalize into an approval.
		func(p *PreparationProposal) { p.Hostname = "Engine-02" },
		func(p *PreparationProposal) { p.Hostname = "engine.example" },
		func(p *PreparationProposal) { p.Hostname = strings.Repeat("x", 64) },
		func(p *PreparationProposal) { p.ControllerHolder = "private\x00holder" },
		func(p *PreparationProposal) { p.ControllerHolder = string([]byte{0xff}) },
		func(p *PreparationProposal) { p.Address = "8.8.8.8" },
		func(p *PreparationProposal) { p.Port = 0 },
		func(p *PreparationProposal) { p.HostKeyFingerprint += "=" },
		func(p *PreparationProposal) { p.MachineID = strings.Repeat("0", 32) },
		func(p *PreparationProposal) { p.Nonce = strings.Repeat("0", 64) },
		func(p *PreparationProposal) { p.TargetID = "00000000-0000-0000-0000-000000000000" },
	} {
		invalid := p
		change(&invalid)
		if digest, err := invalid.RequestSHA256(); invalid.Validate() == nil || err == nil || digest != "" {
			t.Fatal("invalid public proposal returned request identity")
		}
	}
	for name, change := range map[string]func(*PreparationProposal){
		"old clean version":    func(p *PreparationProposal) { p.Version = 1 },
		"version":              func(p *PreparationProposal) { p.Version++ },
		"source":               func(p *PreparationProposal) { p.Source.SourceSHA = strings.Repeat("e", 40) },
		"release":              func(p *PreparationProposal) { p.Source.Release = "2026.11.2" },
		"source qualification": func(p *PreparationProposal) { p.Source.AllowQualification = true },
		"cluster":              func(p *PreparationProposal) { p.ClusterID = p.TargetID },
		"operation":            func(p *PreparationProposal) { p.OperationID = p.TargetID },
		"attempt":              func(p *PreparationProposal) { p.OperationAttempt++ },
		"controller":           func(p *PreparationProposal) { p.ControllerHolder += "changed" },
		"plan":                 func(p *PreparationProposal) { p.PlanSHA256 = strings.Repeat("e", 64) },
		"target":               func(p *PreparationProposal) { p.TargetID = p.ClusterID },
		"reserved generation":  func(p *PreparationProposal) { p.QualificationGeneration++ },
		"address":              func(p *PreparationProposal) { p.Address = "192.168.3.250" },
		"port":                 func(p *PreparationProposal) { p.Port++ },
		"host key": func(p *PreparationProposal) {
			p.HostKeyFingerprint = "SHA256:" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
		},
		"hostname":         func(p *PreparationProposal) { p.Hostname += "changed" },
		"machine":          func(p *PreparationProposal) { p.MachineID = strings.Repeat("e", 32) },
		"boot":             func(p *PreparationProposal) { p.BootID = p.ClusterID },
		"nonce":            func(p *PreparationProposal) { p.Nonce = strings.Repeat("e", 64) },
		"zero nonce":       func(p *PreparationProposal) { p.Nonce = strings.Repeat("0", 64) },
		"upper nonce":      func(p *PreparationProposal) { p.Nonce = strings.Repeat("A", 64) },
		"zero generation":  func(p *PreparationProposal) { p.QualificationGeneration = 0 },
		"negative attempt": func(p *PreparationProposal) { p.OperationAttempt = -1 },
		"control":          func(p *PreparationProposal) { p.ControllerHolder += "\n" },
		"oversized holder": func(p *PreparationProposal) { p.ControllerHolder = strings.Repeat("x", 1025) },
		"invalid UUID":     func(p *PreparationProposal) { p.TargetID = strings.Repeat("0", 36) },
		"invalid source":   func(p *PreparationProposal) { p.Source.Release = "main" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := p
			change(&changed)
			if proof.Validate(changed) == nil {
				t.Fatal("changed waiting proposal accepted")
			}
		})
	}
	raw, _ := json.Marshal(proof)
	for _, bad := range []string{
		strings.Replace(string(raw), `"version":3`, `"version":3,"version":2`, 1),
		strings.Replace(string(raw), `"version":3`, `"Version":3`, 1),
		strings.Replace(string(raw), `"version":3`, `"version":1`, 1),
		strings.Replace(string(raw), `"version":3`, `"version":3,"inventory":"private-account-fixture"`, 1),
		strings.Replace(string(raw), `"version":3,`, "", 1),
		strings.Replace(string(raw), proof.ObservationSHA256, strings.Repeat("0", 64), 1),
		strings.Replace(string(raw), `"nonce":"`+p.Nonce+`"`, `"nonce":null`, 1),
		string(raw) + string(raw), strings.Repeat("x", MaxOutputBytes+1),
	} {
		if got, err := parsePreparationProposalProof([]byte(bad), p); err == nil || got != (PreparationProposalProof{}) {
			t.Fatal("malformed receipt returned usable proof")
		}
	}
}

func TestPreparationProposalPinnedTransport(t *testing.T) {
	for _, mode := range []string{"password", "nopasswd", "malformed", "foreign nonce", "stdout overflow", "stderr overflow", "remote failure", "lost authority", "cancel", "changed target", "changed pin", "invalid request"} {
		t.Run(mode, func(t *testing.T) {
			p := preparationProposalFixture()
			var calls atomic.Int32
			started, release, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
			server := newFakeSSH(t, "proposal fixture", func(command string, channel ssh.Channel) uint32 {
				defer close(joined)
				calls.Add(1)
				if command != preparationProposalCommand(p) || strings.Contains(command, "sudo-fixture-password") {
					return 1
				}
				input, err := io.ReadAll(channel)
				want := "sudo-fixture-password\n"
				if mode == "nopasswd" {
					want = "\n"
				}
				if err != nil || string(input) != want {
					return 1
				}
				proof := preparationProofFixture(p)
				if mode == "foreign nonce" {
					proof.Nonce = strings.Repeat("e", 64)
				}
				raw, _ := json.Marshal(proof)
				switch mode {
				case "malformed":
					raw = []byte(`{"private":"private-account-fixture"}`)
				case "stdout overflow":
					raw = bytes.Repeat([]byte("x"), MaxOutputBytes+1)
				case "stderr overflow":
					channel.Stderr().Write(bytes.Repeat([]byte("x"), MaxOutputBytes+1))
				case "remote failure":
					channel.Stderr().Write([]byte("private-account-fixture"))
					return 1
				case "cancel":
					close(started)
					<-release
					return 0
				}
				channel.Write(raw)
				return 0
			})
			p.Address, p.Port, p.HostKeyFingerprint = server.target.Address, server.target.Port, server.key.Fingerprint
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
			if mode == "changed target" {
				p.Port++
			}
			if mode == "changed pin" {
				p.HostKeyFingerprint = preparationProposalFixture().HostKeyFingerprint
			}
			if mode == "invalid request" {
				p.QualificationGeneration = 0
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cancelJoined := make(chan struct{})
			if mode == "cancel" {
				go func() { defer close(cancelJoined); <-started; cancel(); close(release) }()
			}
			checks := 0
			check := func(context.Context) error {
				checks++
				if mode == "lost authority" && checks == 2 {
					return errors.New("authority changed")
				}
				return nil
			}
			password := []byte("sudo-fixture-password")
			if mode == "nopasswd" {
				password = nil
			}
			got, err := client.InspectPreparationProposal(ctx, password, p, check)
			if mode == "cancel" {
				<-cancelJoined
			}
			if calls.Load() != 0 {
				<-joined
			}
			if mode == "password" || mode == "nopasswd" {
				if err != nil || got.Validate(p) != nil || checks != 2 {
					t.Fatal("matching pinned proposal rejected", err)
				}
			} else if err == nil || got != (PreparationProposalProof{}) || strings.Contains(err.Error(), "private-account-fixture") {
				t.Fatal("unsafe proposal proof returned", err)
			}
			if (mode == "changed target" || mode == "changed pin" || mode == "invalid request") && calls.Load() != 0 {
				t.Fatal("invalid proposal reached SSH")
			}
		})
	}
}

const preparationNativeMountinfo = "23 1 8:1 / / rw,relatime - ext4 /dev/vda1 rw\n" +
	"24 23 0:5 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw\n" +
	"25 23 0:6 / /sys ro,nosuid,nodev,noexec,relatime - sysfs sysfs ro\n" +
	"26 25 0:24 / /sys/fs/cgroup rw,nosuid,nodev,noexec,relatime - cgroup2 cgroup rw\n"

func preparationNativeFixture(t *testing.T) (string, func(string, string), string) {
	t.Helper()
	root := t.TempDir()
	write := func(path, value string) {
		t.Helper()
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := preparationProposalFixture()
	write("etc/machine-id", p.MachineID+"\n")
	write("proc/sys/kernel/random/boot_id", p.BootID+"\n")
	write("proc/sys/kernel/hostname", p.Hostname+"\n")
	write("proc/123/status", "Name:\tfixture\nUid:\t0\t0\t0\t0\n")
	write("proc/1/status", "Name:\tsystemd\nUid:\t0\t0\t0\t0\nCpus_allowed_list:\t0-15\n")
	write("proc/1/comm", "systemd\n")
	write("proc/1/cgroup", "0::/init.scope\n")
	if err := os.Symlink("/usr/lib/systemd/systemd", filepath.Join(root, "proc/1/exe")); err != nil {
		t.Fatal(err)
	}
	write("proc/1/mountinfo", preparationNativeMountinfo)
	write("sys/fs/cgroup/cgroup.controllers", "cpuset cpu io memory hugetlb pids rdma misc\n")
	write("sys/fs/cgroup/cgroup.subtree_control", "cpuset cpu memory pids\n")
	write("sys/fs/cgroup/system.slice/cgroup.events", "populated 1\n")
	write("proc/meminfo", "MemTotal:       33554432 kB\nMemFree:        20000000 kB\n")
	write("sys/devices/system/cpu/online", "0-15\n")
	write("sys/devices/system/cpu/present", "0-15\n")
	for i := 0; i < 16; i++ {
		write("sys/devices/system/cpu/cpu"+strconv.Itoa(i)+"/topology/core_id", strconv.Itoa(i)+"\n")
	}
	for _, path := range []string{"sys/fs/cgroup", "sys/fs/cgroup/system.slice", "sys/fs/cgroup/init.scope"} {
		write(path+"/cpuset.cpus.effective", "0-15\n")
		write(path+"/cpuset.mems.effective", "0\n")
		if path != "sys/fs/cgroup" {
			write(path+"/cpu.max", "max 100000\n")
			write(path+"/memory.max", "max\n")
			write(path+"/memory.high", "max\n")
		}
	}
	write("etc/os-release", "ID=ubuntu\nVERSION_ID=24.04\nVERSION_CODENAME=noble\nUBUNTU_CODENAME=noble\n")
	write("etc/apt/sources.list.d/ubuntu.sources", "Types: deb\nURIs: http://archive.ubuntu.com/ubuntu\nSuites: noble\n")
	write("etc/nsswitch.conf", "passwd: files systemd\ngroup: files systemd\n")
	// Every command is replaced by fixed fixture bytes. No test invokes host
	// package tools, systemctl, getent or ip; filesystem reads remain under root.
	prelude := `fixture_packages = b"apt\tamd64\tinstall ok installed\t2.7.14\ndpkg\tamd64\tinstall ok installed\t1.22.6\nbase-files\tamd64\tinstall ok installed\t13\n"
fixture_passwd = b"root:x:0:0:private-account-fixture:/root:/bin/bash\n"
fixture_groups = b"root:x:0:\n"
fixture_unit = b"LoadState=not-found\nActiveState=inactive\nSubState=dead\nNeedDaemonReload=no\nJob=\n"
fixture_pids = b"MainPID=0\nControlPID=0\n"
fixture_addresses = b'[{"ifindex":2,"ifname":"ens18","flags":["UP"],"addr_info":[{"local":"192.168.3.251","family":"inet","prefixlen":24}]}]'
fixture_manager = b"Version=255.4-1ubuntu8.11\nFeatures=+PAM +AUDIT\n"
fixture_runner = b"systemd 255 (255.4-1ubuntu8.11)\n+PAM +AUDIT\n"
fixture_bootstrap_units = b""
def command(args, limit=LIMIT, seconds=3):
    if args == ["/usr/bin/dpkg", "--print-architecture"]:
        return b"amd64\n"
    if args == ["/usr/bin/dpkg-query", "--show", "--showformat=${Package}\t${Architecture}\t${Status}\t${Version}\n"]:
        return fixture_packages
    if len(args) == 6 and args[:5] == ["/usr/bin/systemctl", "show", "--no-pager", "--property=" + UNIT_PROPERTIES, "--"] and args[5] in UNITS:
        return fixture_unit
    if len(args) == 6 and args[:5] == ["/usr/bin/systemctl", "show", "--no-pager", "--property=" + SOURCE_UNIT_PROPERTIES, "--"] and args[5] in UNITS:
        return fixture_unit + fixture_pids
    if args == ["/usr/bin/getent", "passwd"]:
        return fixture_passwd
    if args == ["/usr/bin/getent", "group"]:
        return fixture_groups
    if args == ["/usr/sbin/ip", "-j", "-4", "address", "show"]:
        return fixture_addresses
    if args == ["/usr/bin/systemctl", "show", "--no-pager", "--property=Version,Features"]:
        return fixture_manager
    if args == ["/usr/bin/systemd-run", "--version"]:
        return fixture_runner
    if args == ["/usr/bin/systemctl", "list-units", "--all", "--type=service", "--no-legend", "--plain", "borealis-bootstrap-*.service"]:
        return fixture_bootstrap_units
    raise ValueError()
`
	return root, write, prelude
}

func runPreparationNativeFixture(t *testing.T, root, prelude string, p PreparationProposal) ([]byte, error) {
	t.Helper()
	script := strings.Replace(preparationProposalLibraryScript, `ROOT = "/"`, "ROOT = "+strconv.Quote(root), 1)
	script = strings.Replace(script, "EXPECTED_UID = 0", "EXPECTED_UID = "+strconv.Itoa(os.Getuid()), 1)
	script = strings.Replace(script, "EXPECTED_GID = 0", "EXPECTED_GID = "+strconv.Itoa(os.Getgid()), 1)
	raw, _ := json.Marshal(p)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script+clusterbootstrap.PreparationTargetCPUObserverLibrary+"\n"+prelude+"\nmain()\n", base64.RawStdEncoding.EncodeToString(raw))
	output, err := command.Output()
	if ctx.Err() != nil {
		t.Fatal("fixture deadline", ctx.Err())
	}
	return output, err
}

func TestPreparationProposalNativeMountinfoComposition(t *testing.T) {
	for _, mode := range []string{"stable full mount table", "drift before CPU read", "drift between rounds"} {
		t.Run(mode, func(t *testing.T) {
			root, _, prelude := preparationNativeFixture(t)
			p := preparationProposalFixture()
			if mode != "stable full mount table" {
				// Change only an ordinary mount row. Retain the complete original
				// supervision observation, including drift outside cgroup mounts.
				function := "supervision"
				if mode == "drift between rounds" {
					function = "observe"
				}
				changed := strings.Replace(preparationNativeMountinfo, "/dev/vda1", "/dev/vdb1", 1)
				prelude += "original_mount_observer = " + function + "\ndef " + function + "(value):\n" +
					"    result = original_mount_observer(value)\n" +
					"    with open(ROOT + '/proc/1/mountinfo', 'w') as output:\n" +
					"        output.write(" + strconv.Quote(changed) + ")\n" +
					"    return result\n"
			}
			raw, err := runPreparationNativeFixture(t, root, prelude, p)
			if mode == "stable full mount table" {
				proof, parseErr := parsePreparationProposalProof(raw, p)
				if err != nil || parseErr != nil || proof.Validate(p) != nil {
					t.Fatal("full proposal rejected stable ordinary mounts", err, parseErr)
				}
			} else if err == nil || len(raw) != 0 {
				t.Fatal("mount drift produced full proposal proof", err)
			}
		})
	}
}

func TestPreparationProposalNativeSnapshots(t *testing.T) {
	for _, mode := range []string{"fresh", "maximum holder", "compatible account", "compatible partial account", "OS link", "old OS", "foreign arch", "missing apt", "second apt foreign arch", "residual docker", "unpacked package", "duplicate package", "oversized packages", "runtime binary", "K3s data", "kubelet state", "Kubernetes config", "source checkout", "generated file", "pending host file", "pending repository", "Docker source", "missing sources", "source symlink", "source hardlink", "ancestor symlink", "unsafe parent", "unsafe file", "FIFO", "oversized source", "too many sources", "loaded unit", "unit job", "unit main PID", "unit control PID", "manager unit", "duplicate unit", "NSS provider", "NSS duplicate", "UID collision", "GID collision", "supplementary group", "login account", "UID process", "malformed process", "oversized account", "changed package", "changed source", "replaced source", "changed parent", "changed machine", "changed boot", "changed hostname", "extra identity newline", "foreign address", "duplicate address", "duplicate address key", "malformed flags", "command failure"} {
		t.Run(mode, func(t *testing.T) {
			root, write, prelude := preparationNativeFixture(t)
			p := preparationProposalFixture()
			success := false
			switch mode {
			case "fresh":
				success = true
			case "maximum holder":
				success = true
				p.ControllerHolder = strings.Repeat("x", 1024)
			case "compatible account":
				success = true
				prelude += "fixture_passwd += b'borealis-engine:x:64646:64646::/nonexistent:/usr/sbin/nologin\\n'\nfixture_groups += b'borealis-engine:x:64646:\\n'\n"
			case "compatible partial account":
				success = true
				prelude += "fixture_groups += b'borealis-engine:x:64646:\\n'\n"
			case "OS link":
				success = true
				write("usr/lib/os-release", "ID=ubuntu\nVERSION_ID=24.04\nVERSION_CODENAME=noble\n")
				if err := os.Remove(filepath.Join(root, "etc/os-release")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../usr/lib/os-release", filepath.Join(root, "etc/os-release")); err != nil {
					t.Fatal(err)
				}
			case "old OS":
				write("etc/os-release", "ID=ubuntu\nVERSION_ID=22.04\nVERSION_CODENAME=jammy\n")
			case "foreign arch":
				prelude = strings.Replace(prelude, `return b"amd64\n"`, `return b"arm64\n"`, 1)
			case "missing apt":
				prelude += "fixture_packages = fixture_packages.split(b'\\n', 1)[1]\n"
			case "second apt foreign arch":
				prelude += "fixture_packages += b'apt\\tarm64\\tinstall ok installed\\t2.7.14\\n'\n"
			case "residual docker":
				prelude += "fixture_packages += b'docker-ce\\tamd64\\tdeinstall ok config-files\\t5:27.0\\n'\n"
			case "unpacked package":
				prelude += "fixture_packages = fixture_packages.replace(b'install ok installed', b'install ok unpacked')\n"
			case "duplicate package":
				prelude += "fixture_packages += fixture_packages\n"
			case "oversized packages":
				prelude += "fixture_packages = b'x' * (LIMIT + 1)\n"
			case "runtime binary":
				write("usr/bin/docker", "foreign")
			case "K3s data":
				write("var/lib/rancher/k3s/state", "foreign")
			case "kubelet state":
				write("var/lib/kubelet/state", "foreign")
			case "Kubernetes config":
				write("etc/kubernetes/config", "foreign")
			case "source checkout":
				write("opt/Borealis/Engine.sh", "foreign")
			case "generated file":
				write("usr/local/lib/borealis/k3s-api-firewall.sh", "foreign")
			case "pending host file":
				write("etc/systemd/system/.borealis-host-retained", "foreign")
			case "pending repository":
				write("etc/apt/keyrings/.borealis-docker-retained", "foreign")
			case "Docker source":
				write("etc/apt/sources.list.d/other.list", "deb https://download.docker.com/linux/ubuntu noble stable\n")
			case "missing sources":
				if err := os.Remove(filepath.Join(root, "etc/apt/sources.list.d/ubuntu.sources")); err != nil {
					t.Fatal(err)
				}
			case "source symlink":
				if err := os.Symlink("ubuntu.sources", filepath.Join(root, "etc/apt/sources.list.d/other.sources")); err != nil {
					t.Fatal(err)
				}
			case "source hardlink":
				if err := os.Link(filepath.Join(root, "etc/apt/sources.list.d/ubuntu.sources"), filepath.Join(root, "etc/apt/sources.list.d/other.sources")); err != nil {
					t.Fatal(err)
				}
			case "ancestor symlink":
				prelude += "os.rename(ROOT + '/etc/apt', ROOT + '/etc/saved')\nos.symlink('saved', ROOT + '/etc/apt')\n"
			case "unsafe parent":
				if err := os.Chmod(filepath.Join(root, "etc/apt"), 0o777); err != nil {
					t.Fatal(err)
				}
			case "unsafe file":
				if err := os.Chmod(filepath.Join(root, "etc/apt/sources.list.d/ubuntu.sources"), 0o666); err != nil {
					t.Fatal(err)
				}
			case "FIFO":
				prelude += "os.mkfifo(ROOT + '/etc/apt/sources.list.d/fifo.sources', 0o600)\n"
			case "oversized source":
				write("etc/apt/sources.list.d/ubuntu.sources", strings.Repeat("x", 65537))
			case "too many sources":
				for i := 0; i < 129; i++ {
					write("etc/apt/sources.list.d/extra"+strconv.Itoa(i), "")
				}
			case "loaded unit":
				prelude += "fixture_unit = fixture_unit.replace(b'not-found', b'loaded')\n"
			case "unit job":
				prelude += "fixture_unit = fixture_unit.replace(b'Job=\\n', b'Job=55\\n')\n"
			case "unit main PID":
				prelude += "fixture_pids = fixture_pids.replace(b'MainPID=0', b'MainPID=55')\n"
			case "unit control PID":
				prelude += "fixture_pids = fixture_pids.replace(b'ControlPID=0', b'ControlPID=55')\n"
			case "manager unit":
				prelude += "original_command = command\ndef command(args, *rest):\n    raw = original_command(args, *rest)\n    if args[-1] == 'borealis-node-manager.service':\n        raw = raw.replace(b'not-found', b'loaded')\n    return raw\n"
			case "duplicate unit":
				prelude += "fixture_unit += b'Job=\\n'\n"
			case "NSS provider":
				write("etc/nsswitch.conf", "passwd: files ldap\ngroup: files\n")
			case "NSS duplicate":
				write("etc/nsswitch.conf", "passwd: files\npasswd: files\ngroup: files\n")
			case "UID collision":
				prelude += "fixture_passwd += b'foreign:x:64646:1000::/home/foreign:/bin/bash\\n'\n"
			case "GID collision":
				prelude += "fixture_groups += b'foreign:x:64646:\\n'\n"
			case "supplementary group":
				prelude += "fixture_groups += b'sudo:x:27:borealis-engine\\n'\n"
			case "login account":
				prelude += "fixture_passwd += b'borealis-engine:x:64646:64646::/nonexistent:/bin/bash\\n'\n"
			case "UID process":
				write("proc/123/status", "Name:\tfixture\nUid:\t0\t0\t64646\t0\n")
			case "malformed process":
				write("proc/123/status", "Name:\tfixture\n")
			case "oversized account":
				prelude += "fixture_passwd = b'x' * (ACCOUNT_LIMIT + 1)\n"
			case "changed package":
				prelude += "original_observe = observe\ndef observe(p):\n    global fixture_packages\n    result = original_observe(p)\n    fixture_packages = fixture_packages.replace(b'2.7.14', b'2.7.15')\n    return result\n"
			case "changed source", "replaced source", "changed parent", "changed machine", "changed boot", "changed hostname":
				path := "/etc/apt/sources.list.d/ubuntu.sources"
				if mode == "changed machine" {
					path = "/etc/machine-id"
				}
				if mode == "changed boot" {
					path = "/proc/sys/kernel/random/boot_id"
				}
				if mode == "changed hostname" {
					path = "/proc/sys/kernel/hostname"
				}
				mutation := "    with open(ROOT + " + strconv.Quote(path) + ", 'a') as f:\n        f.write('changed')\n"
				if mode == "replaced source" {
					mutation = "    path = ROOT + '/etc/apt/sources.list.d/ubuntu.sources'\n    raw = open(path, 'rb').read()\n    os.rename(path, path + '.old')\n    with open(path, 'wb') as f:\n        f.write(raw)\n    os.chmod(path, 0o600)\n    os.unlink(path + '.old')\n"
				}
				if mode == "changed parent" {
					mutation = "    os.rename(ROOT + '/etc/apt/sources.list.d', ROOT + '/etc/apt/saved')\n    os.mkdir(ROOT + '/etc/apt/sources.list.d', 0o700)\n    os.rename(ROOT + '/etc/apt/saved/ubuntu.sources', ROOT + '/etc/apt/sources.list.d/ubuntu.sources')\n    os.rmdir(ROOT + '/etc/apt/saved')\n"
				}
				prelude += "original_observe = observe\ndef observe(p):\n    result = original_observe(p)\n" + mutation + "    return result\n"
			case "foreign address":
				prelude += "fixture_addresses = fixture_addresses.replace(b'192.168.3.251', b'192.168.3.250')\n"
			case "extra identity newline":
				write("proc/sys/kernel/random/boot_id", p.BootID+"\n\n")
			case "duplicate address":
				prelude += "fixture_addresses = json.dumps(json.loads(fixture_addresses) * 2).encode()\n"
			case "duplicate address key":
				prelude += "fixture_addresses = fixture_addresses.replace(b'\"local\":', b'\"local\":\"192.168.3.250\",\"local\":')\n"
			case "malformed flags":
				prelude += "fixture_addresses = fixture_addresses.replace(b'[\"UP\"]', b'\"UP\"')\n"
			case "command failure":
				prelude += "def command(*args, **kwargs):\n    raise ValueError()\n"
			}
			raw, err := runPreparationNativeFixture(t, root, prelude, p)
			if (err == nil) != success || strings.Contains(string(raw), "private-account-fixture") {
				t.Fatalf("wrong native outcome: %v, output bytes %d", err, len(raw))
			}
			if !success {
				if len(raw) != 0 {
					t.Fatal("failed native observer emitted partial evidence")
				}
				return
			}
			proof, err := parsePreparationProposalProof(raw, p)
			if err != nil {
				t.Fatal(err)
			}
			p.Nonce = strings.Repeat("e", 64)
			again, err := runPreparationNativeFixture(t, root, prelude, p)
			if err != nil {
				t.Fatal("fresh reread failed", err)
			}
			second, err := parsePreparationProposalProof(again, p)
			if err != nil || second.ObservationSHA256 != proof.ObservationSHA256 || second.RequestSHA256 == proof.RequestSHA256 {
				t.Fatal("stable observation/fresh request binding lost", err)
			}
		})
	}
}

func TestPreparationProposalCommandBounds(t *testing.T) {
	// Exercise actual incremental pipe bound, timeout and wait only with synthetic
	// Python children. Never call the workstation's package/service/account tools.
	for _, code := range []string{"print('x' * 1024)", "import time; time.sleep(10)", "raise SystemExit(1)"} {
		script := preparationProposalLibraryScript + "\ncommand(['/usr/bin/python3', '-I', '-B', '-c', " + strconv.Quote(code) + "], 16, 0.1)\n"
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cmd := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script)
		raw, err := cmd.Output()
		cancel()
		if err == nil || len(raw) != 0 || ctx.Err() == context.DeadlineExceeded {
			t.Fatal("command bound did not fail and join", err)
		}
	}
}

func TestPreparationProposalNativeSupervision(t *testing.T) {
	for _, mode := range []string{"fresh", "old request", "PID1 name", "PID1 cgroup", "PID1 executable", "deleted executable", "plain executable", "manager version", "duplicate manager field", "manager alias", "missing manager features", "runner version", "runner mismatch", "cgroup v1", "cgroup submount", "cgroup rebound", "missing cgroup mount", "duplicate cgroup mount", "missing CPU controller", "missing memory controller", "missing PID controller", "duplicate controller", "retained journal", "journal symlink", "journal unsafe parent", "bootstrap unit", "bootstrap failed unit", "bootstrap cgroup", "bootstrap transient", "changed manager", "changed controllers", "late journal", "late producer"} {
		t.Run(mode, func(t *testing.T) {
			root, write, prelude := preparationNativeFixture(t)
			p := preparationProposalFixture()
			switch mode {
			case "old request":
				p.Version = 1
			case "PID1 name":
				write("proc/1/comm", "other-init\n")
			case "PID1 cgroup":
				write("proc/1/cgroup", "0::/container.scope\n")
			case "PID1 executable", "deleted executable", "plain executable":
				if err := os.Remove(filepath.Join(root, "proc/1/exe")); err != nil {
					t.Fatal(err)
				}
				if mode == "plain executable" {
					write("proc/1/exe", "lookalike")
				} else {
					link := "/usr/bin/other-init"
					if mode == "deleted executable" {
						link = "/usr/lib/systemd/systemd (deleted)"
					}
					if err := os.Symlink(link, filepath.Join(root, "proc/1/exe")); err != nil {
						t.Fatal(err)
					}
				}
			case "manager version":
				prelude += "fixture_manager = fixture_manager.replace(b'255.4-1ubuntu8.11', b'unknown')\n"
			case "duplicate manager field":
				prelude += "fixture_manager += b'Version=255.4-1ubuntu8.11\\n'\n"
			case "manager alias":
				prelude += "fixture_manager = fixture_manager.replace(b'Version=', b'version=')\n"
			case "missing manager features":
				prelude += "fixture_manager = fixture_manager.split(b'Features=')[0]\n"
			case "runner version":
				prelude += "fixture_runner = b'not systemd\\n'\n"
			case "runner mismatch":
				prelude += "fixture_runner = fixture_runner.replace(b'255', b'256')\n"
			case "cgroup v1":
				write("proc/1/mountinfo", "21 20 0:24 / /sys/fs/cgroup rw - tmpfs tmpfs rw\n")
			case "cgroup submount":
				write("proc/1/mountinfo", "21 20 0:24 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n22 21 0:25 / /sys/fs/cgroup/foreign rw - tmpfs tmpfs rw\n")
			case "cgroup rebound":
				write("proc/1/mountinfo", "21 20 0:24 /foreign /sys/fs/cgroup rw - cgroup2 cgroup rw\n")
			case "missing cgroup mount":
				write("proc/1/mountinfo", "21 20 0:24 / /other rw - cgroup2 cgroup rw\n")
			case "duplicate cgroup mount":
				write("proc/1/mountinfo", strings.Repeat("21 20 0:24 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n", 2))
			case "missing CPU controller":
				write("sys/fs/cgroup/cgroup.controllers", "memory pids\n")
			case "missing memory controller":
				write("sys/fs/cgroup/cgroup.controllers", "cpu pids\n")
			case "missing PID controller":
				write("sys/fs/cgroup/cgroup.controllers", "cpu memory\n")
			case "duplicate controller":
				write("sys/fs/cgroup/cgroup.controllers", "cpu memory pids cpu\n")
			case "retained journal":
				write("var/lib/borealis/cluster-bootstrap/mutation.json", "retained")
			case "journal symlink":
				write("var/lib/borealis/placeholder", "")
				if err := os.Symlink("missing", filepath.Join(root, "var/lib/borealis/cluster-bootstrap")); err != nil {
					t.Fatal(err)
				}
			case "journal unsafe parent":
				write("var/lib/borealis/placeholder", "")
				if err := os.Chmod(filepath.Join(root, "var/lib/borealis"), 0o777); err != nil {
					t.Fatal(err)
				}
			case "bootstrap unit", "bootstrap failed unit":
				state := "active running"
				if mode == "bootstrap failed unit" {
					state = "failed failed"
				}
				prelude += "fixture_bootstrap_units = b'borealis-bootstrap-retained.service loaded " + state + " retained\\n'\n"
			case "bootstrap cgroup":
				write("sys/fs/cgroup/system.slice/borealis-bootstrap-retained.service/cgroup.events", "populated 0\n")
			case "bootstrap transient":
				write("run/systemd/transient/borealis-bootstrap-retained.service", "retained")
			case "changed manager":
				prelude += "original_observe = observe\ndef observe(p):\n    global fixture_manager\n    result = original_observe(p)\n    fixture_manager = fixture_manager.replace(b'255.4', b'255.5')\n    return result\n"
			case "changed controllers", "late journal", "late producer":
				path, value := "/sys/fs/cgroup/cgroup.controllers", "cpu memory pids\n"
				if mode == "late journal" {
					write("var/lib/borealis/placeholder", "")
					path, value = "/var/lib/borealis/cluster-bootstrap", "retained"
				}
				if mode == "late producer" {
					path, value = "/sys/fs/cgroup/system.slice/borealis-bootstrap-late.service", "retained"
				}
				prelude += "original_observe = observe\ndef observe(p):\n    result = original_observe(p)\n    with open(ROOT + " + strconv.Quote(path) + ", 'w') as output:\n        output.write(" + strconv.Quote(value) + ")\n    return result\n"
			}
			raw, err := runPreparationNativeFixture(t, root, prelude, p)
			if mode == "fresh" {
				if err != nil {
					t.Fatal("fresh supervision rejected", err)
				}
				proof, err := parsePreparationProposalProof(raw, p)
				if err != nil || proof.Version != PreparationProposalVersion {
					t.Fatal("stronger proof lacks native semantic version", err)
				}
			} else if err == nil || len(raw) != 0 {
				t.Fatal("unsafe or stale supervision produced proof", err)
			}
		})
	}
}
