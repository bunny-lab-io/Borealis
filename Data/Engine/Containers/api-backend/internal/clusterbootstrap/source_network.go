package clusterbootstrap

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/netip"
	"strings"
	"unicode/utf8"
)

// SourceNetwork is a public projection of the running source supervisor and
// its own Kubernetes Node. Never publish the original supervisor response: it
// includes unrelated operational configuration, paths and private fields.
type SourceNetwork struct {
	NodeUID     string `json:"node_uid"`
	Hostname    string `json:"hostname"`
	MachineID   string `json:"machine_id"`
	BootID      string `json:"boot_id"`
	K3sVersion  string `json:"k3s_version"`
	PodCIDR     string `json:"pod_cidr"`
	ServiceCIDR string `json:"service_cidr"`
}

func ValidateSourceVersion(raw []byte, version string) error {
	object, err := sourceJSONObject(raw, 8<<10)
	var observed string
	if err != nil || json.Unmarshal(object["gitVersion"], &observed) != nil || observed != version || len(version) > 32 || !preparationK3s.MatchString(version) {
		return ErrPreparationConfig
	}
	return nil
}

func (n SourceNetwork) Validate() error {
	pods, podOK := preparationPrefix(n.PodCIDR)
	services, serviceOK := preparationPrefix(n.ServiceCIDR)
	if !nonzeroPreparationUUID(n.NodeUID) || !nonzeroPreparationUUID(n.BootID) || !sessionHostname.MatchString(n.Hostname) ||
		!sessionMachineID.MatchString(n.MachineID) || n.MachineID == strings.Repeat("0", 32) ||
		len(n.K3sVersion) > 32 || !preparationK3s.MatchString(n.K3sVersion) || !podOK || !serviceOK || pods.Overlaps(services) {
		return ErrPreparationConfig
	}
	return nil
}

// ParseSourceNetwork uses the actual K3s Control response, not an installer
// file, environment default or the smaller per-node allocated pod subnet.
// This deliberately supports the Borealis IPv4 baseline only. Custom critical
// settings and component argument overrides need an expanded inheritance
// contract before they can authorize preparing another control-plane host.
func ParseSourceNetwork(raw []byte, identity SourceNetwork) (SourceNetwork, error) {
	fail := func() (SourceNetwork, error) { return SourceNetwork{}, ErrPreparationConfig }
	fields, err := sourceJSONObject(raw, 128<<10)
	if err != nil {
		return fail()
	}
	get := func(name string, out any) bool {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
		return json.Unmarshal(value, out) == nil
	}
	for key, wanted := range map[string]string{"ServerNodeName": identity.Hostname, "ClusterDomain": "cluster.local", "FlannelBackend": "vxlan", "EgressSelectorMode": "agent", "EncryptProvider": "aescbc"} {
		var value string
		if !get(key, &value) || value != wanted {
			return fail()
		}
	}
	for key, wanted := range map[string]bool{"DisableCCM": false, "DisableHelmController": false, "DisableNPC": false,
		"DisableServiceLB": true, "EncryptSecrets": true, "EmbeddedRegistry": true, "FlannelIPv6Masq": false,
		"FlannelExternalIP": false, "DisableKubeProxy": false, "DisableAgent": false, "DisableAPIServer": false,
		"DisableControllerManager": false, "DisableETCD": false, "DisableScheduler": false, "Rootless": false} {
		var value bool
		if !get(key, &value) || value != wanted {
			return fail()
		}
	}
	for _, key := range []string{"HTTPSPort", "SupervisorPort"} {
		var value int
		if !get(key, &value) || value != 6443 {
			return fail()
		}
	}
	for _, key := range []string{"ExtraAPIArgs", "ExtraControllerArgs", "ExtraCloudControllerArgs", "ExtraEtcdArgs", "ExtraSchedulerArgs"} {
		var values []string
		value, exists := fields[key]
		if !exists || json.Unmarshal(value, &values) != nil || len(values) != 0 {
			return fail()
		}
	}
	var disabled map[string]bool
	if !get("Disables", &disabled) || len(disabled) != 2 || !disabled["traefik"] || !disabled["servicelb"] {
		return fail()
	}
	for _, pair := range []struct {
		single, multiple string
		destination      *string
	}{
		{"ClusterIPRange", "ClusterIPRanges", &identity.PodCIDR}, {"ServiceIPRange", "ServiceIPRanges", &identity.ServiceCIDR},
	} {
		var multiple []json.RawMessage
		if !get(pair.multiple, &multiple) || len(multiple) != 1 {
			return fail()
		}
		prefix, singleErr := sourceSupervisorPrefix(fields[pair.single])
		other, multipleErr := sourceSupervisorPrefix(multiple[0])
		if singleErr != nil || multipleErr != nil || prefix != other {
			return fail()
		}
		*pair.destination = prefix.String()
	}
	var dns string
	var allDNS []string
	if !get("ClusterDNS", &dns) || !get("ClusterDNSs", &allDNS) || len(allDNS) != 1 || allDNS[0] != dns {
		return fail()
	}
	services, _ := preparationPrefix(identity.ServiceCIDR)
	expectedDNS := services.Addr()
	for range 10 {
		expectedDNS = expectedDNS.Next()
	}
	if dns != expectedDNS.String() || !preparationHost(services, dns) || identity.Validate() != nil {
		return fail()
	}
	return identity, nil
}

func sourceSupervisorPrefix(raw []byte) (netip.Prefix, error) {
	object, err := sourceJSONObject(raw, 1024)
	var ip, encoded string
	if err != nil || len(object) != 2 || json.Unmarshal(object["IP"], &ip) != nil || json.Unmarshal(object["Mask"], &encoded) != nil {
		return netip.Prefix{}, ErrPreparationConfig
	}
	mask, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(mask) != 4 || base64.StdEncoding.EncodeToString(mask) != encoded {
		return netip.Prefix{}, ErrPreparationConfig
	}
	address, err := netip.ParseAddr(ip)
	if err != nil || !address.Is4() || address.String() != ip {
		return netip.Prefix{}, ErrPreparationConfig
	}
	network := net.IPNet{IP: net.IP(address.AsSlice()), Mask: net.IPMask(mask)}
	prefix, ok := preparationPrefix(network.String())
	if !ok || prefix.Addr() != address {
		return netip.Prefix{}, ErrPreparationConfig
	}
	return prefix, nil
}

// ValidateSourceNode binds supervisor evidence to the source's actual Node,
// boot and management address. Node allocations only corroborate the observed
// global range; they never select that range.
func ValidateSourceNode(raw []byte, network SourceNetwork, address string) error {
	if network.Validate() != nil {
		return ErrPreparationConfig
	}
	var node struct {
		Metadata struct {
			Name, UID         string
			DeletionTimestamp *string
		}
		Spec struct {
			PodCIDR  string
			PodCIDRs []string
		}
		Status struct {
			NodeInfo   struct{ MachineID, BootID, KubeletVersion string }
			Addresses  []struct{ Type, Address string }
			Conditions []struct{ Type, Status string }
		}
	}
	if _, err := sourceJSONObject(raw, 128<<10); err != nil || json.Unmarshal(raw, &node) != nil {
		return ErrPreparationConfig
	}
	if node.Metadata.Name != network.Hostname || node.Metadata.UID != network.NodeUID || node.Metadata.DeletionTimestamp != nil ||
		node.Status.NodeInfo.MachineID != network.MachineID || node.Status.NodeInfo.BootID != network.BootID || node.Status.NodeInfo.KubeletVersion != network.K3sVersion ||
		len(node.Spec.PodCIDRs) != 1 || node.Spec.PodCIDRs[0] != node.Spec.PodCIDR {
		return ErrPreparationConfig
	}
	allocation, ok := preparationPrefix(node.Spec.PodCIDR)
	pods, _ := preparationPrefix(network.PodCIDR)
	if !ok || allocation.Bits() < pods.Bits() || !pods.Contains(allocation.Addr()) {
		return ErrPreparationConfig
	}
	ready, addresses := 0, 0
	for _, condition := range node.Status.Conditions {
		if condition.Type == "Ready" {
			if condition.Status != "True" {
				return ErrPreparationConfig
			}
			ready++
		}
	}
	for _, observed := range node.Status.Addresses {
		if observed.Type == "InternalIP" {
			if observed.Address != address {
				return ErrPreparationConfig
			}
			addresses++
		}
	}
	ip, err := netip.ParseAddr(address)
	if err != nil || !ip.Is4() || ip.String() != address || ready != 1 || addresses != 1 {
		return ErrPreparationConfig
	}
	return nil
}

// Strict source JSON rejects duplicate and case-aliased keys at every depth,
// including objects outside the selected projection. Decoder structs alone
// would silently accept aliases and last-value-wins evidence.
func sourceJSONObject(raw []byte, maximum int) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maximum || !utf8.Valid(raw) {
		return nil, ErrPreparationConfig
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) bool
	walk = func(depth int) bool {
		if depth > 32 {
			return false
		}
		token, err := d.Token()
		if err != nil {
			return false
		}
		delim, compound := token.(json.Delim)
		if !compound {
			return true
		}
		if delim != '{' && delim != '[' {
			return false
		}
		seen := map[string]bool{}
		for d.More() {
			if delim == '{' {
				token, err := d.Token()
				key, ok := token.(string)
				key = strings.ToLower(key)
				if err != nil || !ok || seen[key] {
					return false
				}
				seen[key] = true
			}
			if !walk(depth + 1) {
				return false
			}
		}
		end, err := d.Token()
		return err == nil && ((delim == '{' && end == json.Delim('}')) || (delim == '[' && end == json.Delim(']')))
	}
	if !walk(0) || d.Decode(&struct{}{}) != io.EOF {
		return nil, ErrPreparationConfig
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, ErrPreparationConfig
	}
	return object, nil
}
