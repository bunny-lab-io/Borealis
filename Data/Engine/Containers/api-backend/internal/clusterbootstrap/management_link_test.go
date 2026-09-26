package clusterbootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const managementLinkFixture = `[{"ifindex":2,"ifname":"ens18","flags":["BROADCAST","MULTICAST","UP","LOWER_UP"],"mtu":1500,"operstate":"UP","link_type":"ether","address":"02:00:00:00:00:01","broadcast":"ff:ff:ff:ff:ff:ff","addr_info":[{"family":"inet","local":"192.168.90.20","prefixlen":24,"scope":"global","valid_life_time":4294967295,"preferred_life_time":4294967295}]}]`

func TestManagementLinkPublicIdentity(t *testing.T) {
	link, err := ParseManagementLink([]byte(managementLinkFixture), 1234, "192.168.90.20")
	if err != nil || link != (ManagementLink{"ens18", 2, "192.168.90.20/24", "02:00:00:00:00:01", 1234}) || !link.MatchesAddress("192.168.90.20") || link.MatchesAddress("192.168.90.21") {
		t.Fatalf("management identity: %v", err)
	}
	for name, change := range map[string]func(*ManagementLink){
		"missing":               func(l *ManagementLink) { *l = ManagementLink{} },
		"index":                 func(l *ManagementLink) { l.Index = 0 },
		"large index":           func(l *ManagementLink) { l.Index = 2147483648 },
		"namespace":             func(l *ManagementLink) { l.NetworkNamespace = 0 },
		"large namespace":       func(l *ManagementLink) { l.NetworkNamespace = 1 << 53 },
		"interface syntax":      func(l *ManagementLink) { l.Interface = "$(id)" },
		"long interface":        func(l *ManagementLink) { l.Interface = strings.Repeat("n", 16) },
		"network IP":            func(l *ManagementLink) { l.Address = "192.168.90.0/24" },
		"broadcast IP":          func(l *ManagementLink) { l.Address = "192.168.90.255/24" },
		"public subnet":         func(l *ManagementLink) { l.Address = "192.168.90.20/15" },
		"point to point subnet": func(l *ManagementLink) { l.Address = "192.168.90.20/31" },
		"host prefix":           func(l *ManagementLink) { l.Address = "192.168.90.20/32" },
		"IPv6":                  func(l *ManagementLink) { l.Address = "fd00::1/64" },
		"mapped IPv4":           func(l *ManagementLink) { l.Address = "::ffff:192.168.90.20/120" },
		"short MAC":             func(l *ManagementLink) { l.MAC = "02:00:00:01" },
		"long MAC":              func(l *ManagementLink) { l.MAC = "02:00:00:00:00:00:00:01" },
		"zero MAC":              func(l *ManagementLink) { l.MAC = "00:00:00:00:00:00" },
		"multicast MAC":         func(l *ManagementLink) { l.MAC = "01:00:00:00:00:01" },
		"broadcast MAC":         func(l *ManagementLink) { l.MAC = "ff:ff:ff:ff:ff:ff" },
		"noncanonical MAC":      func(l *ManagementLink) { l.MAC = "02-00-00-00-00-01" },
		"uppercase MAC":         func(l *ManagementLink) { l.MAC = "02:00:00:00:00:AB" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := link
			change(&bad)
			if bad.Validate() != ErrPreparationConfig || bad.MatchesAddress("192.168.90.20") {
				t.Fatal("invalid link accepted")
			}
		})
	}
}

func TestManagementLinkRejectsAmbiguousOrUnsupportedOwnership(t *testing.T) {
	for name, change := range map[string]func(map[string]any, map[string]any){
		"missing hardware":  func(l, a map[string]any) { delete(l, "link_type") },
		"non Ethernet":      func(l, a map[string]any) { l["link_type"] = "infiniband" },
		"down":              func(l, a map[string]any) { l["operstate"] = "DOWN" },
		"broadcast MAC":     func(l, a map[string]any) { l["address"] = "ff:ff:ff:ff:ff:ff" },
		"no broadcast":      func(l, a map[string]any) { l["broadcast"] = "00:00:00:00:00:00" },
		"no carrier":        func(l, a map[string]any) { l["flags"] = []string{"UP", "BROADCAST", "NO-CARRIER"} },
		"no ARP":            func(l, a map[string]any) { l["flags"] = []string{"UP", "BROADCAST", "LOWER_UP", "NOARP"} },
		"point to point":    func(l, a map[string]any) { l["flags"] = []string{"UP", "POINTOPOINT", "LOWER_UP"} },
		"duplicate flag":    func(l, a map[string]any) { l["flags"] = []string{"UP", "BROADCAST", "LOWER_UP", "UP"} },
		"null flags":        func(l, a map[string]any) { l["flags"] = nil },
		"master":            func(l, a map[string]any) { l["master"] = "br0" },
		"null master":       func(l, a map[string]any) { l["master"] = nil },
		"vlan":              func(l, a map[string]any) { l["linkinfo"] = map[string]any{"info_kind": "vlan"} },
		"empty linkinfo":    func(l, a map[string]any) { l["linkinfo"] = map[string]any{} },
		"parent":            func(l, a map[string]any) { l["link"] = "ens19" },
		"parent index":      func(l, a map[string]any) { l["link_index"] = 3 },
		"peer namespace":    func(l, a map[string]any) { l["link_netnsid"] = 0 },
		"boolean index":     func(l, a map[string]any) { l["ifindex"] = true },
		"fraction index":    func(l, a map[string]any) { l["ifindex"] = 2.5 },
		"missing addresses": func(l, a map[string]any) { delete(l, "addr_info") },
		"scope":             func(l, a map[string]any) { a["scope"] = "host" },
		"wrong family":      func(l, a map[string]any) { a["family"] = "inet6" },
		"peer address":      func(l, a map[string]any) { a["peer"] = "192.168.90.21" },
		"DHCP":              func(l, a map[string]any) { a["dynamic"] = true },
		"null dynamic":      func(l, a map[string]any) { a["dynamic"] = nil },
		"deprecated":        func(l, a map[string]any) { a["deprecated"] = true },
		"tentative":         func(l, a map[string]any) { a["tentative"] = true },
		"DAD failed":        func(l, a map[string]any) { a["dadfailed"] = true },
		"expires":           func(l, a map[string]any) { a["valid_life_time"] = 300 },
		"preferred expires": func(l, a map[string]any) { a["preferred_life_time"] = 300 },
		"null lifetime":     func(l, a map[string]any) { a["valid_life_time"] = nil },
		"missing lifetime":  func(l, a map[string]any) { delete(a, "preferred_life_time") },
		"string lifetime":   func(l, a map[string]any) { a["valid_life_time"] = "4294967295" },
		"invalid prefix":    func(l, a map[string]any) { a["prefixlen"] = 33 },
		"missing prefix":    func(l, a map[string]any) { delete(a, "prefixlen") },
		"duplicate address": func(l, a map[string]any) { l["addr_info"] = []any{a, a} },
	} {
		t.Run(name, func(t *testing.T) {
			var rows []map[string]any
			if json.Unmarshal([]byte(managementLinkFixture), &rows) != nil {
				t.Fatal("fixture")
			}
			change(rows[0], rows[0]["addr_info"].([]any)[0].(map[string]any))
			raw, _ := json.Marshal(rows)
			if got, err := ParseManagementLink(raw, 1234, "192.168.90.20"); err != ErrPreparationConfig || got != (ManagementLink{}) {
				t.Fatal("unsupported ownership accepted")
			}
		})
	}
	for _, bad := range []string{
		strings.Replace(managementLinkFixture, `"ifindex":2`, `"ifindex":2,"IFINDEX":2`, 1),
		strings.Replace(managementLinkFixture, `"local":`, `"local":"192.168.90.21","local":`, 1),
		strings.Replace(managementLinkFixture, `"mtu":1500`, `"private":{"x":1,"X":1}`, 1),
		strings.TrimSuffix(managementLinkFixture, "]") + "," + managementLinkFixture[1:],
		strings.TrimSuffix(managementLinkFixture, "]") + "," + strings.ReplaceAll(strings.ReplaceAll(managementLinkFixture[1:], "ens18", "ens19"), `"ifindex":2`, `"ifindex":3`),
		`null`, `[]`, `[null]`, `{"interfaces":[]}`, managementLinkFixture + `{}`, managementLinkFixture + `,"private":1`,
		managementLinkFixture + strings.Repeat(" ", 128<<10), managementLinkFixture + string([]byte{0xff}),
	} {
		if got, err := ParseManagementLink([]byte(bad), 1234, "192.168.90.20"); err != ErrPreparationConfig || got != (ManagementLink{}) {
			t.Fatal("ambiguous JSON or duplicate identity accepted")
		}
	}
	// Current MAC can differ from a vendor permanent address; use actual source
	// MAC for peer expectations and omit unrelated iproute2/private metadata.
	raw := strings.Replace(managementLinkFixture, `"mtu":1500`, `"private":"do-not-publish","permaddr":"02:00:00:00:00:02"`, 1)
	got, err := ParseManagementLink([]byte(raw), 1234, "192.168.90.20")
	encoded, _ := json.Marshal(got)
	if err != nil || got.MAC != "02:00:00:00:00:01" || bytes.Contains(encoded, []byte("private")) || bytes.Contains(encoded, []byte("permaddr")) {
		t.Fatal("current MAC or public projection")
	}
}

func TestManagementLinkInventoryBounds(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		interfaces, addresses, total int
		valid                        bool
	}{
		{"interface limit", 128, 0, 0, true}, {"too many interfaces", 129, 0, 0, false},
		{"address limit", 2, 256, 0, true}, {"too many addresses", 2, 257, 0, false},
		{"total limit", 6, 0, 1023, true}, {"too many total addresses", 6, 0, 1024, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var values []map[string]any
			if json.Unmarshal([]byte(managementLinkFixture), &values) != nil {
				t.Fatal("fixture")
			}
			remaining := tc.total
			for i := 1; i < tc.interfaces; i++ {
				count := tc.addresses
				if tc.total > 0 {
					count = min(remaining, 256)
					remaining -= count
				}
				rows := make([]any, 0, count)
				for range count {
					rows = append(rows, map[string]any{"local": "10.0.0.1"})
				}
				values = append(values, map[string]any{"ifindex": i + 2, "ifname": fmt.Sprintf("ens%d", i+18), "addr_info": rows})
			}
			raw, _ := json.Marshal(values)
			got, err := ParseManagementLink(raw, 1234, "192.168.90.20")
			if (err == nil) != tc.valid || (tc.valid && !got.MatchesAddress("192.168.90.20")) {
				t.Fatalf("bound: %v", err)
			}
		})
	}
}
