package clusterbootstrap

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func vipKernelFixture(t *testing.T, present bool) []byte {
	t.Helper()
	var rows []map[string]any
	if json.Unmarshal([]byte(managementLinkFixture), &rows) != nil {
		t.Fatal("fixture")
	}
	if present {
		rows[0]["addr_info"] = append(rows[0]["addr_info"].([]any), map[string]any{"family": "inet", "local": "192.168.90.250", "prefixlen": 32, "scope": "global", "valid_life_time": 4294967295, "preferred_life_time": 4294967295})
	}
	raw, _ := json.Marshal(rows)
	return raw
}

func TestVIPAddressCompleteOwnershipAndAbsence(t *testing.T) {
	link, err := ParseManagementLink([]byte(managementLinkFixture), 1234, "192.168.90.20")
	if err != nil {
		t.Fatal(err)
	}
	for _, present := range []bool{false, true} {
		value, err := ParseVIPAddress(vipKernelFixture(t, present), link, "192.168.90.250")
		if err != nil || value.Present != present || value.Validate(link) != nil {
			t.Fatal("VIP observation", err)
		}
		if present && (value.MAC != link.MAC || value.Index != link.Index || value.Interface != link.Interface) {
			t.Fatal("actual owner identity missing")
		}
	}
	for _, mode := range []string{"duplicate", "other interface", "wrong mask", "dynamic", "expired", "scope", "null presence", "malformed other address", "missing inventory", "duplicate JSON", "host IP", "public", "outside", "broadcast", "namespace", "MAC changed"} {
		t.Run(mode, func(t *testing.T) {
			raw := vipKernelFixture(t, true)
			var rows []map[string]any
			_ = json.Unmarshal(raw, &rows)
			addresses := rows[0]["addr_info"].([]any)
			vip := addresses[1].(map[string]any)
			address := "192.168.90.250"
			expected := link
			switch mode {
			case "duplicate":
				rows[0]["addr_info"] = append(addresses, vip)
			case "other interface":
				other := map[string]any{}
				for k, v := range rows[0] {
					other[k] = v
				}
				rows[0]["addr_info"] = addresses[:1]
				other["ifname"], other["ifindex"], other["addr_info"] = "ens19", 3, []any{vip}
				rows = append(rows, other)
			case "wrong mask":
				vip["prefixlen"] = 24
			case "dynamic":
				vip["dynamic"] = true
			case "expired":
				vip["valid_life_time"] = 1
			case "scope":
				vip["scope"] = "host"
			case "null presence":
				vip["prefixlen"] = nil
			case "malformed other address":
				addresses[0].(map[string]any)["local"] = nil
			case "missing inventory":
				delete(rows[0], "addr_info")
			case "host IP":
				address = "192.168.90.20"
			case "public":
				address = "8.8.8.8"
			case "outside":
				address = "192.168.91.250"
			case "broadcast":
				address = "192.168.90.255"
			case "namespace":
				expected.NetworkNamespace = 0
			case "MAC changed":
				rows[0]["address"] = "02:00:00:00:00:02"
			}
			raw, _ = json.Marshal(rows)
			if mode == "duplicate JSON" {
				raw = bytes.Replace(raw, []byte(`"ifindex":2`), []byte(`"ifindex":2,"IFINDEX":2`), 1)
			}
			if got, err := ParseVIPAddress(raw, expected, address); err != ErrPreparationConfig || got != (VIPAddress{}) {
				t.Fatal("unsafe VIP inventory", err)
			}
		})
	}
}

func TestSourceVIPReceiptAndRequestBoundaries(t *testing.T) {
	raw, identity := sourceNetworkFixture(t)
	network, err := ParseSourceNetwork(raw, identity)
	if err != nil {
		t.Fatal(err)
	}
	address := "192.168.90.250"
	vip := VIPAddress{Address: address, NetworkNamespace: network.ManagementLink.NetworkNamespace}
	nonce, job, pod := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	value := SourceVIPNetwork{network, vip}
	action, _ := json.Marshal(map[string]any{"ok": true, "verb": "InspectVIPNetwork", "result": map[string]any{"source_vip_network": value}})
	receipt, err := NewSourceVIPReceipt(action, nonce, job, pod, address)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ParseSourceVIPReceipt(receipt, nonce, job, pod, address); err != nil || got != value {
		t.Fatal("VIP receipt", err)
	}
	for _, bad := range [][]byte{
		bytes.Replace(receipt, []byte(`"present":false`), []byte(`"present":null`), 1),
		bytes.Replace(receipt, []byte(`"present":false`), []byte(`"present":true`), 1),
		bytes.Replace(receipt, []byte(`"present":false`), []byte(`"Present":false`), 1),
		bytes.Replace(receipt, []byte(`"present":false`), []byte(`"present":false,"present":false`), 1),
		bytes.Replace(receipt, []byte(`"present":false`), []byte(`"private":"never publish","present":false`), 1),
		bytes.Replace(receipt, []byte(`"version":1`), []byte(`"version":2`), 1),
		bytes.ReplaceAll(receipt, []byte(pod), []byte(job)),
		bytes.Replace(receipt, []byte(address), []byte("192.168.90.249"), 1),
		append(bytes.Clone(receipt), bytes.Repeat([]byte(" "), SourceNetworkReceiptLimit)...),
	} {
		if got, err := ParseSourceVIPReceipt(bad, nonce, job, pod, address); err != ErrPreparationConfig || got != (SourceVIPNetwork{}) {
			t.Fatal("unsafe VIP receipt", err)
		}
	}
	if out, err := NewSourceVIPReceipt(bytes.Replace(action, []byte(`"ok":true`), []byte(`"ok":true,"private":"secret"`), 1), nonce, job, pod, address); err != ErrPreparationConfig || out != nil {
		t.Fatal("private action projected")
	}
	valid := `{"verb":"InspectVIPNetwork","params":{"vip":"192.168.90.250"}}`
	if got, err := ParseVIPActionRequest([]byte(valid)); err != nil || got != address {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(valid, `"vip":`, `"VIP":`, 1), strings.Replace(valid, `"params":`, `"extra":{},"params":`, 1),
		strings.Replace(valid, `"vip":`, `"vip":"192.168.90.20","vip":`, 1), strings.Replace(valid, address, "$(id)", 1),
		strings.Replace(valid, address, "8.8.8.8", 1), strings.Replace(valid, address, "::ffff:192.168.90.250", 1),
		strings.Replace(valid, `"`+address+`"`, "null", 1), valid + `{}`,
	} {
		if _, err := ParseVIPActionRequest([]byte(bad)); err != ErrPreparationConfig {
			t.Fatal("invalid VIP request accepted")
		}
	}
}
