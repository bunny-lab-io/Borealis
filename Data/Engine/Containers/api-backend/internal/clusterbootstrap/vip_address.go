package clusterbootstrap

import (
	"encoding/json"
	"net/netip"
)

// VIPAddress records actual /32 ownership, or complete absence, on one host.
// A present VIP must use the freshly observed plain management Ethernet link.
// Namespace/index values remain scoped to that SourceNetwork's machine/boot.
type VIPAddress struct {
	Address          string `json:"address"`
	Present          bool   `json:"present"`
	Interface        string `json:"interface"`
	Index            int    `json:"index"`
	MAC              string `json:"mac"`
	NetworkNamespace uint64 `json:"network_namespace"`
}

type SourceVIPNetwork struct {
	Network SourceNetwork `json:"source_network"`
	VIP     VIPAddress    `json:"vip"`
}

func ValidVIPRequest(address string) bool {
	ip, err := netip.ParseAddr(address)
	return err == nil && len(address) <= 15 && ip.Is4() && ip.IsPrivate() && ip.String() == address
}

func (v VIPAddress) Validate(link ManagementLink) error {
	prefix, err := netip.ParsePrefix(link.Address)
	if link.Validate() != nil || err != nil || !ValidVIPRequest(v.Address) || !preparationHost(prefix.Masked(), v.Address) || prefix.Addr().String() == v.Address || v.NetworkNamespace != link.NetworkNamespace {
		return ErrPreparationConfig
	}
	if v.Present {
		if v.Interface != link.Interface || v.Index != link.Index || v.MAC != link.MAC {
			return ErrPreparationConfig
		}
	} else if v.Interface != "" || v.Index != 0 || v.MAC != "" {
		return ErrPreparationConfig
	}
	return nil
}

func (v SourceVIPNetwork) Validate() error {
	if v.Network.Validate() != nil {
		return ErrPreparationConfig
	}
	return v.VIP.Validate(v.Network.ManagementLink)
}

// ParseVIPAddress examines the entire bounded kernel address inventory. It
// never infers absence from a filtered interface or from a command failure.
// ParseManagementLink validates every interface/index/local address, recursive
// JSON ambiguity and aggregate limits before this second, VIP-specific pass.
func ParseVIPAddress(raw []byte, expected ManagementLink, address string) (VIPAddress, error) {
	fail := func() (VIPAddress, error) { return VIPAddress{}, ErrPreparationConfig }
	prefix, err := netip.ParsePrefix(expected.Address)
	if err != nil || !ValidVIPRequest(address) {
		return fail()
	}
	link, err := ParseManagementLink(raw, expected.NetworkNamespace, prefix.Addr().String())
	if err != nil || link != expected {
		return fail()
	}
	var interfaces []map[string]json.RawMessage
	if json.Unmarshal(raw, &interfaces) != nil {
		return fail()
	}
	v := VIPAddress{Address: address, NetworkNamespace: link.NetworkNamespace}
	matches := 0
	for _, iface := range interfaces {
		var rows []map[string]json.RawMessage
		if json.Unmarshal(iface["addr_info"], &rows) != nil {
			return fail()
		}
		for _, row := range rows {
			var local string
			if json.Unmarshal(row["local"], &local) != nil {
				return fail()
			}
			if local != address {
				continue
			}
			matches++
			var bits int
			if matches != 1 || !managementEthernet(iface) || !managementPermanentAddress(row) || json.Unmarshal(row["prefixlen"], &bits) != nil || bits != 32 ||
				json.Unmarshal(iface["ifname"], &v.Interface) != nil || json.Unmarshal(iface["ifindex"], &v.Index) != nil || json.Unmarshal(iface["address"], &v.MAC) != nil {
				return fail()
			}
			v.Present = true
		}
	}
	if v.Validate(link) != nil {
		return fail()
	}
	return v, nil
}
