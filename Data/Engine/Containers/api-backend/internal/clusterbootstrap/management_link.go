package clusterbootstrap

import (
	"encoding/json"
	"net"
	"net/netip"
	"regexp"
)

var managementInterfaceName = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,15}$`)

// ManagementLink is public, host-observed current link identity. It is not
// permanent hardware identity, persistent configuration or fresh ARP proof.
// Its containing observation must bind machine/boot and current authority.
type ManagementLink struct {
	Interface        string `json:"interface"`
	Index            int    `json:"index"`
	Address          string `json:"address"`
	MAC              string `json:"mac"`
	NetworkNamespace uint64 `json:"network_namespace"`
}

func (link ManagementLink) Validate() error {
	prefix, err := netip.ParsePrefix(link.Address)
	mac, macErr := net.ParseMAC(link.MAC)
	if err != nil || !prefix.Addr().Is4() || prefix.String() != link.Address || prefix.Bits() > 30 ||
		!preparationHost(prefix.Masked(), prefix.Addr().String()) ||
		!managementInterfaceName.MatchString(link.Interface) || link.Index < 1 || link.Index > 2147483647 ||
		link.NetworkNamespace == 0 || link.NetworkNamespace > 1<<53-1 ||
		macErr != nil || len(mac) != 6 || mac.String() != link.MAC || mac[0]&1 != 0 || link.MAC == "00:00:00:00:00:00" {
		return ErrPreparationConfig
	}
	if _, ok := preparationPrefix(prefix.Masked().String()); !ok {
		return ErrPreparationConfig
	}
	return nil
}

func (link ManagementLink) MatchesAddress(address string) bool {
	prefix, err := netip.ParsePrefix(link.Address)
	return err == nil && link.Validate() == nil && prefix.Addr().String() == address
}

// ParseManagementLink consumes fixed `ip -j -d -4 address show` output.
// -d is required: without it IPv4 output can omit Ethernet type and linkinfo.
// Only plain, up, broadcast-capable Ethernet with unique permanent IPv4
// ownership qualifies. Other interfaces are counted for duplicate ownership.
func ParseManagementLink(raw []byte, namespace uint64, address string) (ManagementLink, error) {
	fail := func() (ManagementLink, error) { return ManagementLink{}, ErrPreparationConfig }
	ip, err := netip.ParseAddr(address)
	if err != nil || !ip.Is4() || !ip.IsPrivate() || ip.String() != address || len(raw) == 0 || len(raw) > 128<<10 {
		return fail()
	}
	// Reuse strict recursive JSON validation, including unselected fields.
	wrapped := append(append([]byte(`{"interfaces":`), raw...), '}')
	object, err := sourceJSONObject(wrapped, (128<<10)+32)
	var interfaces []map[string]json.RawMessage
	if err != nil || len(object) != 1 || json.Unmarshal(object["interfaces"], &interfaces) != nil || interfaces == nil || len(interfaces) > 128 {
		return fail()
	}
	names, indices := map[string]bool{}, map[int]bool{}
	var found ManagementLink
	matches, total := 0, 0
	for _, iface := range interfaces {
		var name string
		var index int
		var rows []map[string]json.RawMessage
		if json.Unmarshal(iface["ifname"], &name) != nil || !managementInterfaceName.MatchString(name) || names[name] ||
			json.Unmarshal(iface["ifindex"], &index) != nil || index < 1 || index > 2147483647 || indices[index] ||
			json.Unmarshal(iface["addr_info"], &rows) != nil || rows == nil || len(rows) > 256 {
			return fail()
		}
		names[name], indices[index] = true, true
		total += len(rows)
		if total > 1024 {
			return fail()
		}
		for _, row := range rows {
			var local string
			if row == nil || json.Unmarshal(row["local"], &local) != nil {
				return fail()
			}
			observed, err := netip.ParseAddr(local)
			if err != nil || !observed.Is4() || observed.String() != local {
				return fail()
			}
			if local != address {
				continue
			}
			matches++
			var bits int
			var mac string
			if !managementEthernet(iface) || !managementPermanentAddress(row) ||
				json.Unmarshal(row["prefixlen"], &bits) != nil || bits < 0 || bits > 32 || json.Unmarshal(iface["address"], &mac) != nil {
				return fail()
			}
			found = ManagementLink{name, index, netip.PrefixFrom(ip, bits).String(), mac, namespace}
		}
	}
	if matches != 1 || !found.MatchesAddress(address) {
		return fail()
	}
	return found, nil
}

func managementEthernet(iface map[string]json.RawMessage) bool {
	for _, key := range []string{"master", "link", "link_index", "link_netnsid", "linkinfo", "link_pointtopoint"} {
		if _, exists := iface[key]; exists {
			return false
		}
	}
	for key, want := range map[string]string{"link_type": "ether", "operstate": "UP", "broadcast": "ff:ff:ff:ff:ff:ff"} {
		var value string
		if json.Unmarshal(iface[key], &value) != nil || value != want {
			return false
		}
	}
	var flags []string
	if json.Unmarshal(iface["flags"], &flags) != nil || len(flags) < 3 || len(flags) > 4 {
		return false
	}
	seen := map[string]bool{}
	for _, flag := range flags {
		if seen[flag] || (flag != "UP" && flag != "LOWER_UP" && flag != "BROADCAST" && flag != "MULTICAST") {
			return false
		}
		seen[flag] = true
	}
	return seen["UP"] && seen["LOWER_UP"] && seen["BROADCAST"]
}

func managementPermanentAddress(row map[string]json.RawMessage) bool {
	if _, exists := row["peer"]; exists {
		return false
	}
	for key, want := range map[string]string{"family": "inet", "scope": "global"} {
		var value string
		if json.Unmarshal(row[key], &value) != nil || value != want {
			return false
		}
	}
	for _, key := range []string{"dynamic", "deprecated", "tentative", "dadfailed"} {
		if raw, exists := row[key]; exists && string(raw) != "false" {
			return false
		}
	}
	for _, key := range []string{"valid_life_time", "preferred_life_time"} {
		var value uint64
		if json.Unmarshal(row[key], &value) != nil || value != 4294967295 {
			return false
		}
	}
	return true
}
