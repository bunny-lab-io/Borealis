package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"slices"
	"time"
)

var ErrARP = errors.New("SSH fresh ARP observation unavailable; host diagnostics withheld")

// ARPRequest contains independently observed identities, never expectations
// learned from ARP itself. Only the current whole-cohort/VIP scope supplies it.
type ARPRequest struct {
	Version   int                             `json:"version"`
	MachineID string                          `json:"machine_id"`
	BootID    string                          `json:"boot_id"`
	Link      clusterbootstrap.ManagementLink `json:"link"`
	Peers     []ARPPeer                       `json:"peers"`
}

type ARPPeer struct {
	Address string `json:"address"`
	MAC     string `json:"mac"`
}

func (r ARPRequest) Validate() error {
	if r.Version != 1 || !machineIDPattern.MatchString(r.MachineID) || r.MachineID == "00000000000000000000000000000000" ||
		!validPublicUUID(r.BootID) || r.Link.Validate() != nil || len(r.Peers) != 3 {
		return ErrARP
	}
	p, _ := netip.ParsePrefix(r.Link.Address)
	previous := ""
	for _, peer := range r.Peers {
		a, err := netip.ParseAddr(peer.Address)
		mac, macErr := net.ParseMAC(peer.MAC)
		if err != nil || a.String() != peer.Address || !UsableManagementAddress(p.Masked(), a) || a == p.Addr() || peer.Address <= previous ||
			macErr != nil || len(mac) != 6 || mac.String() != peer.MAC || mac[0]&1 != 0 || peer.MAC == "00:00:00:00:00:00" || peer.MAC == r.Link.MAC {
			return ErrARP
		}
		previous = peer.Address
	}
	// VIP and its owner intentionally share a MAC; cohort validation separately
	// rejects duplicate host MACs. There are two remote hosts and one shared VIP.
	return nil
}

type arpObservation struct {
	Version int        `json:"version"`
	Request ARPRequest `json:"request"`
	Rounds  int        `json:"rounds"`
}

// TargetARPObservation has native pinned SSH provenance and local acquisition
// times. It cannot be imported from a receipt or used as a continuing grant.
type TargetARPObservation struct {
	wire              arpObservation
	target            Target
	key               HostKey
	started, finished time.Time
}

func (v TargetARPObservation) Matches(notBefore time.Time, target Target, key HostKey, request ARPRequest) error {
	if notBefore.IsZero() || v.started.Before(notBefore) || v.finished.Before(v.started) || v.finished.After(time.Now()) ||
		v.finished.Sub(v.started) > 25*time.Second || target.Validate() != nil || key.Validate() != nil || v.target != target ||
		v.key.Algorithm != key.Algorithm || v.key.Fingerprint != key.Fingerprint || !bytes.Equal(v.key.PublicKey, key.PublicKey) ||
		v.wire.Version != 1 || v.wire.Rounds != 2 || request.Validate() != nil || !request.Link.MatchesAddress(target.Address) || !reflect.DeepEqual(v.wire.Request, request) {
		return ErrARP
	}
	return nil
}

func arpCommand(request ARPRequest) (string, error) {
	if request.Validate() != nil {
		return "", ErrARP
	}
	raw, err := json.Marshal(request)
	if err != nil || len(raw) > 1024 {
		return "", ErrARP
	}
	return buildPrivilegedInspectionCommand("/usr/sbin:/usr/bin:/sbin:/bin", "exec /usr/bin/python3 -I -B -c "+shellConstant(arpScript)+" "+shellConstant(base64.StdEncoding.EncodeToString(raw))+" 2>/dev/null"), nil
}

// ProbeARP uses the existing root observation timeout, fixed script and pinned
// SSH connection. check must retain this target's original credential/claim and
// current complete peer/VIP scope. All local authority callbacks are joined.
// Loss of SSH cannot prove immediate remote exit; the fixed remote timeout still
// applies and no successful observation is returned after cancellation.
func (client *Client) ProbeARP(parent context.Context, sudoPassword []byte, expected Target, approved HostKey, request ARPRequest, check func(context.Context) error) (TargetARPObservation, error) {
	started := time.Now()
	request.Peers = slices.Clone(request.Peers)
	approved.PublicKey = bytes.Clone(approved.PublicKey)
	command, err := arpCommand(request)
	if err != nil || client == nil || client.ssh == nil || expected.Validate() != nil || approved.Validate() != nil || check == nil ||
		client.target != expected || !request.Link.MatchesAddress(expected.Address) || client.approved.Algorithm != approved.Algorithm ||
		client.approved.Fingerprint != approved.Fingerprint || !bytes.Equal(client.approved.PublicKey, approved.PublicKey) || parent.Err() != nil {
		return TargetARPObservation{}, ErrARP
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
		return TargetARPObservation{}, ErrARP
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
		return TargetARPObservation{}, ErrARP
	}
	var wire arpObservation
	if len(raw) == 0 || len(raw) > 2048 || json.Unmarshal(raw, &wire) != nil {
		return TargetARPObservation{}, ErrARP
	}
	canonical, err := json.Marshal(wire)
	v := TargetARPObservation{wire: wire, target: expected, key: HostKey{Algorithm: approved.Algorithm, Fingerprint: approved.Fingerprint, PublicKey: bytes.Clone(approved.PublicKey)}, started: started, finished: time.Now()}
	if err != nil || !bytes.Equal(canonical, bytes.TrimSpace(raw)) || v.Matches(started, expected, approved, request) != nil || ctx.Err() != nil {
		return TargetARPObservation{}, ErrARP
	}
	return v, nil
}

const arpScript = persistentNetworkLibraryScript + activeNetworkLibraryScript + routedNetworkLibraryScript + managementLinkLibraryScript + arpLibraryScript + `
if __name__ == "__main__":
    main(observe_arp)
`

const arpLibraryScript = `
import socket, struct

# Linux UAPI constants; NEW timestamps have fixed signed 64-bit seconds/nsecs.
# Ubuntu amd64 is the supported target. No optional arping program is required.
ARP_PROTOCOL, SOL_PACKET, PACKET_AUXDATA, PACKET_STATISTICS = 0x0806, 263, 8, 6
SO_TIMESTAMPNS_NEW = 64

def arp_mac(text):
    if type(text) is not str or not re.fullmatch(r"[0-9a-f]{2}(?::[0-9a-f]{2}){5}", text):
        raise ValueError()
    raw = bytes.fromhex(text.replace(":", ""))
    if raw == bytes(6) or raw[0] & 1:
        raise ValueError()
    return raw

def arp_request():
    if len(sys.argv) != 2 or len(sys.argv[1]) > 1368:
        raise ValueError()
    raw = base64.b64decode(sys.argv[1], validate=True)
    if len(raw) > 1024 or base64.b64encode(raw).decode() != sys.argv[1]:
        raise ValueError()
    r = management_json(raw)
    if type(r) is not dict or set(r) != {"version", "machine_id", "boot_id", "link", "peers"} or type(r["version"]) is not int or r["version"] != 1:
        raise ValueError()
    for name, pattern in (("machine_id", r"[0-9a-f]{32}"), ("boot_id", r"[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}")):
        value = r[name]
        if type(value) is not str or not re.fullmatch(pattern, value) or not value.replace("0", "").replace("-", ""):
            raise ValueError()
    link = r["link"]
    if type(link) is not dict or set(link) != {"interface", "index", "address", "mac", "network_namespace"}:
        raise ValueError()
    if type(link["interface"]) is not str or not re.fullmatch(r"[A-Za-z0-9_.:-]{1,15}", link["interface"]) or type(link["index"]) is not int or not 0 < link["index"] <= 2147483647 or type(link["network_namespace"]) is not int or not 0 < link["network_namespace"] <= 9007199254740991:
        raise ValueError()
    if type(link["address"]) is not str or len(link["address"]) > 18:
        raise ValueError()
    prefix = ipaddress.IPv4Interface(link["address"])
    if str(prefix) != link["address"] or prefix.network.prefixlen > 30 or prefix.ip in (prefix.network.network_address, prefix.network.broadcast_address) or not any(prefix.network.subnet_of(ipaddress.IPv4Network(v)) for v in ("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16")):
        raise ValueError()
    local_mac = arp_mac(link["mac"])
    if type(r["peers"]) is not list or len(r["peers"]) != 3:
        raise ValueError()
    previous = ""
    for p in r["peers"]:
        if type(p) is not dict or set(p) != {"address", "mac"} or type(p["address"]) is not str or len(p["address"]) > 15:
            raise ValueError()
        a = ipaddress.IPv4Address(p["address"])
        if str(a) != p["address"] or p["address"] <= previous or a not in prefix.network or a in (prefix.ip, prefix.network.network_address, prefix.network.broadcast_address) or arp_mac(p["mac"]) == local_mac:
            raise ValueError()
        previous = p["address"]
    if raw != json.dumps(r, separators=(",", ":")).encode():
        raise ValueError()
    return r

def arp_frame(local_ip, local_mac, peer_ip):
    # Broadcast a solicited request, with ordinary assigned sender IP. This is
    # not duplicate-address detection, an announcement or a neighbor-cache write.
    return bytes.fromhex("ffffffffffff") + local_mac + struct.pack("!HHHBBH", ARP_PROTOCOL, 1, 0x0800, 6, 4, 1) + local_mac + local_ip + bytes(6) + peer_ip

def arp_reply(frame, metadata, address, flags, r, sent, now_ns):
    if flags != 0 or not 42 <= len(frame) <= 60 or len(address) != 5:
        raise ValueError()
    name, protocol, kind, hardware, sender = address
    if name != r["link"]["interface"] or protocol != ARP_PROTOCOL or hardware != 1 or kind not in (0, 1, 4):
        raise ValueError()
    if len(metadata) != 2:
        raise ValueError()
    ancillary = {}
    for level, number, data in metadata:
        key = (level, number)
        if key in ancillary:
            raise ValueError()
        ancillary[key] = data
    stamp = ancillary.get((socket.SOL_SOCKET, SO_TIMESTAMPNS_NEW), b"")
    aux = ancillary.get((SOL_PACKET, PACKET_AUXDATA), b"")
    if len(stamp) != 16 or len(aux) != 20:
        raise ValueError()
    sec, nano = struct.unpack("=qq", stamp)
    status, length, captured, mac_offset, net_offset, vlan, vlan_type = struct.unpack("=IIIHHHH", aux)
    # Reject VLAN stripping, truncation and unsupported metadata. Plain Ethernet
    # is already the independently selected link contract.
    if sec <= 0 or not 0 <= nano < 1000000000 or status != 1 or length != len(frame) or captured != len(frame) or mac_offset != 0 or net_offset != 14 or vlan != 0 or vlan_type != 0:
        raise ValueError()
    received = sec*1000000000+nano
    if received > now_ns:
        raise ValueError()
    dst, src = frame[:6], frame[6:12]
    protocol, hardware, network, hlen, plen, op = struct.unpack("!HHHBBH", frame[12:22])
    sha, spa, tha, tpa = frame[22:28], frame[28:32], frame[32:38], frame[38:42]
    if protocol != ARP_PROTOCOL or hardware != 1 or network != 0x0800 or hlen != 6 or plen != 4 or op not in (1, 2) or sha != src or sender != src or src == bytes(6) or src[0] & 1:
        raise ValueError()
    local_ip = ipaddress.IPv4Interface(r["link"]["address"]).ip.packed
    local_mac = arp_mac(r["link"]["mac"])
    expected = {ipaddress.IPv4Address(p["address"]).packed: arp_mac(p["mac"]) for p in r["peers"]}
    if (spa in expected and sha != expected[spa]) or (spa == local_ip and sha != local_mac):
        raise ValueError()
    if kind == 4:
        if src != local_mac:
            raise ValueError()
        return None
    if kind == 1 and dst != bytes.fromhex("ffffffffffff") or kind == 0 and dst != local_mac:
        raise ValueError()
    # Requests/announcements and unrelated replies never supply positive proof.
    if op != 2 or spa not in expected or dst != local_mac or tha != local_mac or tpa != local_ip:
        return None
    if spa not in sent or received < sent[spa]:
        return None
    return spa

def arp_exchange(r):
    link = r["link"]
    local_ip = ipaddress.IPv4Interface(link["address"]).ip.packed
    local_mac = arp_mac(link["mac"])
    peers = [ipaddress.IPv4Address(p["address"]).packed for p in r["peers"]]
    start, wall = time.monotonic_ns(), time.time_ns()
    packets = 0
    def clock():
        mono, real = time.monotonic_ns(), time.time_ns()
        if mono < start or mono-start > 2000000000 or abs((real-wall)-(mono-start)) > 50000000:
            raise ValueError()
        return mono, real
    def current():
        clock()
        if management_namespace() != link["network_namespace"] or socket.if_nametoindex(link["interface"]) != link["index"]:
            raise ValueError()
    def current_link():
        remaining = (start+2000000000-clock()[0])/1000000000
        if read_management_link(str(ipaddress.IPv4Address(local_ip)), remaining) != link:
            raise ValueError()
    for _ in range(2):
        current()
        current_link()
        # Protocol zero receives nothing until exact interface/protocol binding.
        with socket.socket(socket.AF_PACKET, socket.SOCK_RAW | socket.SOCK_CLOEXEC, 0) as sock:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF, 65536)
            sock.setsockopt(socket.SOL_SOCKET, SO_TIMESTAMPNS_NEW, 1)
            sock.setsockopt(SOL_PACKET, PACKET_AUXDATA, 1)
            sock.settimeout(0.05)
            sock.bind((link["interface"], ARP_PROTOCOL))
            current()
            if sock.getsockname()[:2] != (link["interface"], ARP_PROTOCOL):
                raise ValueError()
            sent, seen, round_packets = {}, set(), 0
            end = clock()[0] + 500000000
            for peer in peers:
                current()
                frame = arp_frame(local_ip, local_mac, peer)
                sent[peer] = clock()[1]
                if sock.send(frame) != len(frame):
                    raise ValueError()
            # Always observe full window, including conflicts after first success.
            while True:
                current()
                remaining = end-clock()[0]
                if remaining <= 0:
                    break
                sock.settimeout(min(0.05, remaining/1000000000))
                try:
                    frame, ancillary, flags, address = sock.recvmsg(61, socket.CMSG_SPACE(16)+socket.CMSG_SPACE(20))
                except socket.timeout:
                    continue
                packets += 1
                round_packets += 1
                if packets > 256:
                    raise ValueError()
                now, real = clock()
                result = arp_reply(frame, ancillary, address, flags, r, sent, real)
                if result is not None and now <= end:
                    seen.add(result)
            statistics = sock.getsockopt(SOL_PACKET, PACKET_STATISTICS, 8)
            if len(statistics) != 8 or struct.unpack("=II", statistics) != (round_packets, 0) or seen != set(peers):
                raise ValueError()
            current()
        current_link()
    clock()

def observe_arp():
    r = arp_request()  # Validate every field before host/socket access.
    global route_targets
    route_targets = lambda: {"management": str(ipaddress.IPv4Interface(r["link"]["address"]).ip), "peers": [p["address"] for p in r["peers"]]}
    before = observe_management_link()
    declarations = before["routing"]["active"]["declarations"]
    if before["link"] != r["link"] or declarations["machine_id"] != r["machine_id"] or declarations["boot_id"] != r["boot_id"]:
        raise ValueError()
    arp_exchange(r)
    if observe_management_link() != before:
        raise ValueError()
    return {"version": 1, "request": r, "rounds": 2}
`
