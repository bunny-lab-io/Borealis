package clusterremote

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func arpFixture(t *testing.T) ARPRequest {
	t.Helper()
	return ARPRequest{Version: 1, MachineID: strings.Repeat("a", 32), BootID: "11111111-1111-4111-8111-111111111111", Link: targetManagementFixture(t).Link,
		Peers: []ARPPeer{{"192.168.3.249", "02:00:00:00:00:02"}, {"192.168.3.250", "02:00:00:00:00:03"}, {"192.168.3.252", "02:00:00:00:00:02"}}}
}

func runARPFixture(t *testing.T, raw []byte, script string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	library := persistentNetworkLibraryScript + activeNetworkLibraryScript + routedNetworkLibraryScript + managementLinkLibraryScript + arpLibraryScript
	command := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", library+script, base64.StdEncoding.EncodeToString(raw))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("ARP fixture: %v %s", err, output)
	}
}

func TestARPRequestBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "version", "machine", "boot", "interface", "index", "namespace", "prefix", "mac", "local", "public", "network", "duplicate", "order", "empty", "many", "peer mac", "local mac"} {
		t.Run(mode, func(t *testing.T) {
			r := arpFixture(t)
			switch mode {
			case "version":
				r.Version++
			case "machine":
				r.MachineID = strings.Repeat("0", 32)
			case "boot":
				r.BootID = ""
			case "interface":
				r.Link.Interface = "$(id)"
			case "index":
				r.Link.Index = 0
			case "namespace":
				r.Link.NetworkNamespace = 0
			case "prefix":
				r.Link.Address = "192.168.3.251/31"
			case "mac":
				r.Link.MAC = "01:00:00:00:00:01"
			case "local":
				r.Peers[2].Address = "192.168.3.251"
			case "public":
				r.Peers[0].Address = "8.8.8.8"
			case "network":
				r.Peers[0].Address = "192.168.3.0"
			case "duplicate":
				r.Peers[1] = r.Peers[0]
			case "order":
				r.Peers[0], r.Peers[1] = r.Peers[1], r.Peers[0]
			case "empty":
				r.Peers = nil
			case "many":
				r.Peers = append(r.Peers, r.Peers[0])
			case "peer mac":
				r.Peers[0].MAC = "00:00:00:00:00:00"
			case "local mac":
				r.Peers[0].MAC = r.Link.MAC
			}
			valid := mode == "valid"
			if (r.Validate() == nil) != valid {
				t.Fatal("Go request validation")
			}
			if command, err := arpCommand(r); (err == nil) != valid || !valid && command != "" {
				t.Fatal("invalid command")
			}
			raw, _ := json.Marshal(r)
			want := "False"
			if valid {
				want = "True"
			}
			runARPFixture(t, raw, "\ntry:\n    arp_request()\n    accepted=True\nexcept ValueError:\n    accepted=False\nassert accepted == "+want+"\n")
		})
	}
	raw, _ := json.Marshal(arpFixture(t))
	for _, value := range []string{"null", "{}", string(raw) + string(raw), strings.Replace(string(raw), `"version":1`, `"version":true`, 1), strings.Replace(string(raw), `"index":2`, `"index":2,"Index":2`, 1), strings.Replace(string(raw), `"mac":`, `"unknown":null,"mac":`, 1), strings.Repeat(" ", 1025)} {
		runARPFixture(t, []byte(value), `
def forbidden(*args):
    raise AssertionError("invalid request reached host")
socket.socket = forbidden
observe_management_link = forbidden
try:
    observe_arp()
except (ValueError, TypeError):
    pass
else:
    raise AssertionError("invalid request accepted")
`)
	}
}

const arpPythonFixture = `
r = arp_request()
local_ip = ipaddress.IPv4Interface(r["link"]["address"]).ip.packed
local_mac = arp_mac(r["link"]["mac"])
peer = ipaddress.IPv4Address(r["peers"][0]["address"]).packed
peer_mac = arp_mac(r["peers"][0]["mac"])
def reply(p=peer, mac=peer_mac):
    return local_mac+mac+struct.pack("!HHHBBH", ARP_PROTOCOL, 1, 0x0800, 6, 4, 2)+mac+p+local_mac+local_ip
def metadata(frame, stamp=100000000000):
    return [(socket.SOL_SOCKET, SO_TIMESTAMPNS_NEW, struct.pack("=qq", stamp//1000000000, stamp%1000000000)),
            (SOL_PACKET, PACKET_AUXDATA, struct.pack("=IIIHHHH", 1, len(frame), len(frame), 0, 14, 0, 0))]
def address(mac=peer_mac):
    return (r["link"]["interface"], ARP_PROTOCOL, 0, 1, mac)
`

func TestARPPacketAndAncillaryValidation(t *testing.T) {
	raw, _ := json.Marshal(arpFixture(t))
	runARPFixture(t, raw, arpPythonFixture+`
frame = reply()
sent = {peer: 99000000000}
assert arp_reply(frame, metadata(frame), address(), 0, r, sent, 101000000000) == peer
request = arp_frame(local_ip, local_mac, peer)
assert len(request) == 42 and request[:6] == bytes.fromhex("ffffffffffff") and request[6:12] == local_mac
assert struct.unpack("!HHHBBH", request[12:22]) == (ARP_PROTOCOL, 1, 0x0800, 6, 4, 1)
assert request[22:28] == local_mac and request[28:32] == local_ip and request[32:38] == bytes(6) and request[38:] == peer
for n in range(42):
    short = frame[:n]
    try: arp_reply(short, metadata(short), address(), 0, r, sent, 101000000000)
    except ValueError: pass
    else: raise AssertionError("truncated frame")
for offset in (6, 12, 14, 16, 18, 19, 20, 22):
    bad = bytearray(frame); bad[offset] ^= 1; bad = bytes(bad)
    try: arp_reply(bad, metadata(bad), address(), 0, r, sent, 101000000000)
    except ValueError: pass
    else: raise AssertionError(("bad field", offset))
for kind in (2, 3, 5):
    a = list(address()); a[2] = kind
    try: arp_reply(frame, metadata(frame), a, 0, r, sent, 101000000000)
    except ValueError: pass
    else: raise AssertionError("wrong packet kind")
for name in ("interface", "protocol", "hardware", "sender", "flags", "huge", "timestamp missing", "timestamp short", "timestamp future", "timestamp duplicate", "vlan", "loss", "length", "offset", "aux missing"):
    f, m, a, flags = frame, metadata(frame), list(address()), 0
    if name == "interface": a[0] = "ens19"
    if name == "protocol": a[1] = 0x0800
    if name == "hardware": a[3] = 2
    if name == "sender": a[4] = local_mac
    if name == "flags": flags = socket.MSG_CTRUNC
    if name == "huge": f += bytes(19); m = metadata(f)
    if name == "timestamp missing": m = m[1:]
    if name == "timestamp short": m[0] = (socket.SOL_SOCKET, SO_TIMESTAMPNS_NEW, bytes(8))
    if name == "timestamp future": m = metadata(f, 102000000000)
    if name == "timestamp duplicate": m = [m[0], m[0]]
    if name in ("vlan", "loss", "length", "offset"):
        values = [1, len(f), len(f), 0, 14, 0, 0]
        if name == "vlan": values[0] |= 16; values[5] = 1
        if name == "loss": values[0] |= 4
        if name == "length": values[1] += 1
        if name == "offset": values[4] = 18
        m[1] = (SOL_PACKET, PACKET_AUXDATA, struct.pack("=IIIHHHH", *values))
    if name == "aux missing": m = m[:1]
    try: arp_reply(f, m, a, flags, r, sent, 101000000000)
    except ValueError: pass
    else: raise AssertionError(name)
for offset in (0, 32, 38):
    f = bytearray(frame); f[offset] ^= 2; f = bytes(f)
    try: result = arp_reply(f, metadata(f), address(), 0, r, sent, 101000000000)
    except ValueError: result = None
    assert result is None
assert arp_reply(frame, metadata(frame, 98000000000), address(), 0, r, sent, 101000000000) is None
assert arp_reply(frame, metadata(frame), address(), 0, r, {}, 101000000000) is None
wrong = bytes.fromhex("020000000009")
f = reply(mac=wrong)
try: arp_reply(f, metadata(f), address(wrong), 0, r, sent, 101000000000)
except ValueError: pass
else: raise AssertionError("conflicting host MAC")
f = reply(p=local_ip, mac=peer_mac)
try: arp_reply(f, metadata(f), address(), 0, r, sent, 101000000000)
except ValueError: pass
else: raise AssertionError("local address conflict")
f = reply()+bytes(18)
assert arp_reply(f, metadata(f), address(), 0, r, sent, 101000000000) == peer
`)
}

func TestARPSocketBoundsAndCleanup(t *testing.T) {
	for _, mode := range []string{"success", "missing", "old", "late", "conflict after success", "flood", "truncated", "dropped", "queued", "send short", "send error", "bind error", "option error", "namespace", "index", "link", "clock forward", "clock backward", "slow", "interrupt"} {
		t.Run(mode, func(t *testing.T) {
			raw, _ := json.Marshal(arpFixture(t))
			name, _ := json.Marshal(mode)
			runARPFixture(t, raw, arpPythonFixture+"\nMODE="+string(name)+arpSocketFixture)
		})
	}
}

const arpSocketFixture = `
class Clock:
    value = 100000000000
    def mono(self): return self.value
    def real(self):
        return self.value + (100000000 if MODE == "clock forward" and self.value > 100000000000 else -100000000 if MODE == "clock backward" and self.value > 100000000000 else 0)
clock = Clock()
time.monotonic_ns, time.time_ns = clock.mono, clock.real
opened = []
class Socket:
    def __init__(self, family, kind, protocol):
        assert (family, kind, protocol) == (socket.AF_PACKET, socket.SOCK_RAW|socket.SOCK_CLOEXEC, 0)
        self.closed, self.queue, self.received, self.sends = False, [], 0, 0
        self.timeout = 0.05
        opened.append(self)
    def __enter__(self): return self
    def __exit__(self, *args): self.closed = True
    def setsockopt(self, level, option, value):
        assert (level, option, value) in ((socket.SOL_SOCKET, socket.SO_RCVBUF, 65536), (socket.SOL_SOCKET, SO_TIMESTAMPNS_NEW, 1), (SOL_PACKET, PACKET_AUXDATA, 1))
        if MODE == "option error": raise OSError()
    def settimeout(self, value):
        assert 0 < value <= 0.05
        self.timeout = value
    def bind(self, addr):
        assert addr == (r["link"]["interface"], ARP_PROTOCOL)
        if MODE == "bind error": raise OSError()
    def getsockname(self): return (r["link"]["interface"], ARP_PROTOCOL, 0, 1, local_mac)
    def send(self, frame):
        self.sends += 1
        if MODE == "send error": raise OSError()
        if MODE == "send short": return len(frame)-1
        p = frame[38:42]
        mac = next(arp_mac(v["mac"]) for v in r["peers"] if ipaddress.IPv4Address(v["address"]).packed == p)
        f = reply(p, mac)
        self.queue.append((f, mac, clock.value-1 if MODE == "old" else clock.value))
        return len(frame)
    def recvmsg(self, size, capacity):
        assert size == 61 and capacity == socket.CMSG_SPACE(16)+socket.CMSG_SPACE(20)
        if MODE == "interrupt": raise KeyboardInterrupt()
        if MODE == "slow": clock.value += 3000000000
        if MODE == "late": clock.value += 600000000
        if MODE == "flood":
            self.received += 1
            f = reply()
            return (f, metadata(f, clock.value), 0, address())
        if self.queue and MODE != "missing":
            f, mac, stamp = self.queue.pop(0)
            self.received += 1
            if MODE == "conflict after success" and self.received == 3:
                bad = bytes.fromhex("020000000009")
                self.queue.append((reply(peer, bad), bad, stamp))
            return (f, metadata(f, stamp), socket.MSG_TRUNC if MODE == "truncated" else 0, address(mac))
        clock.value += round(self.timeout*1000000000)
        raise socket.timeout()
    def getsockopt(self, level, option, length):
        assert (level, option, length) == (SOL_PACKET, PACKET_STATISTICS, 8)
        return struct.pack("=II", self.received+(1 if MODE == "queued" else 0), 1 if MODE == "dropped" else 0)
socket.socket = Socket
socket.if_nametoindex = lambda name: r["link"]["index"]+(1 if MODE == "index" and opened else 0)
management_namespace = lambda: r["link"]["network_namespace"]+(1 if MODE == "namespace" and opened else 0)
def read_link(ip, timeout):
    assert 0 < timeout <= 2
    value = dict(r["link"])
    if MODE == "link" and opened: value["index"] += 1
    return value
read_management_link = read_link
try:
    arp_exchange(r)
    success = True
except (ValueError, OSError, KeyboardInterrupt):
    success = False
assert success == (MODE == "success"), MODE
assert all(s.closed for s in opened), "socket survived exit"
assert len(opened) <= 2 and sum(s.sends for s in opened) <= 6 and sum(s.received for s in opened) <= 257
if success:
    assert len(opened) == 2 and sum(s.sends for s in opened) == 6 and clock.value-100000000000 == 1000000000
`

func TestARPHostObservationBracketsExchange(t *testing.T) {
	for _, mode := range []string{"success", "machine", "boot", "link before", "link after", "backend after", "exchange error"} {
		t.Run(mode, func(t *testing.T) {
			raw, _ := json.Marshal(arpFixture(t))
			wire, _ := json.Marshal(targetManagementFixture(t))
			name, _ := json.Marshal(mode)
			runARPFixture(t, raw, "\nMODE="+string(name)+"\nwire=json.loads("+string(mustJSONForARP(t, string(wire)))+")\n"+`
calls, exchanges = 0, 0
def observation():
    global calls
    calls += 1
    v = json.loads(json.dumps(wire))
    if MODE == "machine": v["routing"]["active"]["declarations"]["machine_id"] = "b"*32
    if MODE == "boot": v["routing"]["active"]["declarations"]["boot_id"] = "22222222-2222-4222-8222-222222222222"
    if MODE == "link before" or MODE == "link after" and calls > 1: v["link"]["index"] += 1
    if MODE == "backend after" and calls > 1: v["routing"]["active"]["owner"] = ":2.3"
    return v
def exchange(r):
    global exchanges
    exchanges += 1
    if MODE == "exchange error": raise ValueError()
observe_management_link, arp_exchange = observation, exchange
try:
    result=observe_arp()
    success=True
except ValueError: success=False
assert success == (MODE == "success")
if MODE in ("machine", "boot", "link before"): assert exchanges == 0
if success: assert result == {"version":1,"request":arp_request(),"rounds":2} and calls == 2 and exchanges == 1
`)
		})
	}
}

func TestARPRealHostObservationWithIsolatedPackets(t *testing.T) {
	// Actual Netplan/networkd/routing/link functions surround the packet loop.
	// Only host command binaries, namespace files and raw socket are synthetic.
	for _, mode := range []string{"success", "MAC changed after packets", "link hangs during packets"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			write := func(name, body string, mode os.FileMode) {
				p := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), mode); err != nil {
					t.Fatal(err)
				}
			}
			write("etc/machine-id", strings.Repeat("a", 32), 0o600)
			write("proc/sys/kernel/random/boot_id", "11111111-1111-4111-8111-111111111111", 0o600)
			write("proc/self/ns/net", "namespace", 0o600)
			if err := os.MkdirAll(filepath.Join(root, "proc/1/ns"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(filepath.Join(root, "proc/self/ns/net"), filepath.Join(root, "proc/1/ns/net")); err != nil {
				t.Fatal(err)
			}
			write("etc/netplan/10-static.yaml", persistentNetplanFixture, 0o600)
			write("run/systemd/network/10-netplan-ens18.network", "[Match]\nName=ens18\n[Network]\nAddress=192.168.3.251/24\n", 0o600)
			write("daemon.json", networkdDescriptionFixture, 0o600)
			write("kernel.json", targetManagementKernelFixture, 0o600)
			write("routing.json", routingStateFixture, 0o600)
			fixture := strings.Replace(activeNetworkCommandFixture, "args = sys.argv[1:]\n", "args = sys.argv[1:]\n"+targetManagementCommandFixture+routedNetworkCommandFixture, 1)
			fixture = strings.Replace(fixture, `("192.168.3.250", "192.168.3.252")`, `("192.168.3.249", "192.168.3.250", "192.168.3.252")`, 1)
			fixture = strings.Replace(fixture, `if MODE in ("link hang", "link pipe closed hang"):`, "if MODE == 'link hangs during packets' and n > 4:\n        (root/'child-pid').write_text(str(os.getpid()))\n        time.sleep(30)\n    if MODE in (\"link hang\", \"link pipe closed hang\"):", 1)
			stub := "#!/usr/bin/python3\nROOT=" + strconv.Quote(root) + "\nMODE=" + strconv.Quote(mode) + "\n" + fixture
			write("bin/ip", stub, 0o700)
			write("bin/busctl", stub, 0o700)
			r := arpFixture(t)
			// Match namespace through the already tested Python projection instead
			// of assuming a host inode number in the portable Go test.
			script := persistentNetworkLibraryScript + activeNetworkLibraryScript + routedNetworkLibraryScript + managementLinkLibraryScript + arpLibraryScript
			script = strings.Replace(script, `ROOT = "/"`, "ROOT = "+strconv.Quote(root), 1)
			script = strings.Replace(script, "EXPECTED_UID = 0", "EXPECTED_UID = "+strconv.Itoa(os.Getuid()), 1)
			script = strings.ReplaceAll(script, `"/usr/bin/busctl"`, strconv.Quote(filepath.Join(root, "bin/busctl")))
			script = strings.ReplaceAll(script, `"/usr/sbin/ip"`, strconv.Quote(filepath.Join(root, "bin/ip")))
			request, _ := json.Marshal(r)
			script += "\nMODE=" + strconv.Quote(mode) + arpRealSocketFixture
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/usr/bin/python3", "-I", "-B", "-c", script, base64.StdEncoding.EncodeToString(request))
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("real host ARP fixture: %v %s", err, output)
			}
		})
	}
}

const arpRealSocketFixture = `
request = arp_request()
request["link"]["network_namespace"] = management_namespace()
sys.argv[1] = base64.b64encode(json.dumps(request,separators=(",", ":")).encode()).decode()
opened = []
class Socket:
    def __init__(self, *args):
        assert args == (socket.AF_PACKET, socket.SOCK_RAW|socket.SOCK_CLOEXEC, 0)
        self.closed, self.queue, self.received = False, [], 0
        opened.append(self)
    def __enter__(self): return self
    def __exit__(self, *args):
        self.closed = True
        if MODE == "MAC changed after packets":
            with open(ROOT+"/kernel.json", "r") as f: raw = f.read()
            with open(ROOT+"/kernel.json", "w") as f: f.write(raw.replace("02:00:00:00:00:01", "02:00:00:00:00:09"))
    def setsockopt(self, *args): pass
    def settimeout(self, value): self.timeout=value
    def bind(self, addr): assert addr == ("ens18", ARP_PROTOCOL)
    def getsockname(self): return ("ens18", ARP_PROTOCOL, 0, 1, arp_mac(request["link"]["mac"]))
    def send(self, frame):
        p = frame[38:42]
        local_mac, local_ip = frame[22:28], frame[28:32]
        mac = next(arp_mac(v["mac"]) for v in request["peers"] if ipaddress.IPv4Address(v["address"]).packed == p)
        self.queue.append((local_mac+mac+struct.pack("!HHHBBH", ARP_PROTOCOL,1,0x0800,6,4,2)+mac+p+local_mac+local_ip, mac, time.time_ns()))
        return len(frame)
    def recvmsg(self, *args):
        if not self.queue:
            time.sleep(self.timeout)
            raise socket.timeout()
        f, mac, stamp = self.queue.pop(0)
        self.received += 1
        return f, [(socket.SOL_SOCKET, SO_TIMESTAMPNS_NEW, struct.pack("=qq", stamp//1000000000, stamp%1000000000)),
                   (SOL_PACKET, PACKET_AUXDATA, struct.pack("=IIIHHHH",1,len(f),len(f),0,14,0,0))], 0, ("ens18",ARP_PROTOCOL,0,1,mac)
    def getsockopt(self, *args): return struct.pack("=II",self.received,0)
socket.socket = Socket
socket.if_nametoindex = lambda name: 2 if name == "ens18" else 0
try:
    result = observe_arp()
    success = True
except ValueError:
    success = False
assert success == (MODE == "success"), MODE
assert all(s.closed for s in opened)
if MODE == "link hangs during packets":
    with open(ROOT+"/child-pid") as f: pid=int(f.read())
    try: os.kill(pid,0)
    except ProcessLookupError: pass
    else: raise AssertionError("link child survived aggregate deadline")
else: assert len(opened) > 0
if success: assert len(opened) == 2 and result == {"version":1,"request":request,"rounds":2}
`

func mustJSONForARP(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestARPNativePinnedScope(t *testing.T) {
	for _, mode := range []string{"success", "wrong port", "wrong key", "unbound address", "failed authority", "lost during command", "callback deadline", "cancel", "private output", "changed result", "wrong rounds", "duplicate", "trailing", "empty", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			r := arpFixture(t)
			command, _ := arpCommand(r)
			var commands, checks atomic.Int32
			var joined atomic.Bool
			server := newFakeSSH(t, "arp", func(cmd string, channel ssh.Channel) uint32 {
				commands.Add(1)
				if cmd != command {
					return 1
				}
				input, err := io.ReadAll(channel)
				if err != nil || !bytes.Equal(input, []byte("private-sudo\n")) {
					return 1
				}
				if mode == "lost during command" || mode == "callback deadline" || mode == "cancel" {
					time.Sleep(1300 * time.Millisecond)
				}
				wire := arpObservation{Version: 1, Request: r, Rounds: 2}
				if mode == "private output" {
					channel.Stderr().Write([]byte("private-sudo"))
					return 1
				}
				if mode == "changed result" {
					wire.Request.Link.Index++
				}
				if mode == "wrong rounds" {
					wire.Rounds = 1
				}
				out, _ := json.Marshal(wire)
				if mode == "duplicate" {
					out = bytes.Replace(out, []byte(`"rounds":2`), []byte(`"rounds":2,"rounds":2`), 1)
				}
				if mode == "trailing" {
					out = append(out, out...)
				}
				if mode == "empty" {
					out = nil
				}
				if mode == "overflow" {
					out = bytes.Repeat([]byte("x"), MaxOutputBytes+1)
				}
				channel.Write(out)
				return 0
			})
			server.target.Address = "192.168.3.251"
			credential, _ := PasswordCredential("operator", server.password)
			defer credential.Destroy()
			client, err := server.transport.Connect(context.Background(), server.target, server.key, credential)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			check := func(ctx context.Context) error {
				n := checks.Add(1)
				if mode == "failed authority" || mode == "lost during command" && n > 1 {
					return errors.New("private-authority")
				}
				if mode == "cancel" && n > 1 {
					cancel()
				}
				if mode == "callback deadline" && n > 1 {
					<-ctx.Done()
					joined.Store(true)
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
			if mode == "unbound address" {
				r.Link.Address = "192.168.3.248/24"
			}
			started := time.Now()
			result, err := client.ProbeARP(ctx, []byte("private-sudo"), target, key, r, check)
			if mode != "success" {
				if err != ErrARP || !reflect.DeepEqual(result, TargetARPObservation{}) {
					t.Fatal("unsafe ARP result", err)
				}
				if mode == "callback deadline" && !joined.Load() {
					t.Fatal("callback not joined")
				}
				if (mode == "wrong port" || mode == "wrong key" || mode == "unbound address" || mode == "failed authority") && commands.Load() != 0 {
					t.Fatal("invalid request sent")
				}
				return
			}
			if err != nil || result.Matches(started, target, key, r) != nil {
				t.Fatal("valid observation", err)
			}
			if result.Matches(time.Now(), target, key, r) != ErrARP {
				t.Fatal("old observation accepted")
			}
			r.Peers[0].MAC = "02:00:00:00:00:04"
			if result.Matches(started, target, key, r) != ErrARP {
				t.Fatal("changed request accepted")
			}
		})
	}
}
