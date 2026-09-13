package clusterbootstrap

import "encoding/json"

type sourceVIPReceipt struct {
	Version     int              `json:"version"`
	Nonce       string           `json:"nonce"`
	JobUID      string           `json:"job_uid"`
	PodUID      string           `json:"pod_uid"`
	Observation SourceVIPNetwork `json:"source_vip_network"`
}

func ParseVIPActionRequest(raw []byte) (string, error) {
	object, err := sourceExactObject(raw, "verb", "params")
	var verb, address string
	if err != nil || json.Unmarshal(object["verb"], &verb) != nil || verb != "InspectVIPNetwork" {
		return "", ErrPreparationConfig
	}
	params, err := sourceExactObject(object["params"], "vip")
	if err != nil || json.Unmarshal(params["vip"], &address) != nil || !ValidVIPRequest(address) {
		return "", ErrPreparationConfig
	}
	return address, nil
}

func parseSourceVIPNetwork(raw []byte) (SourceVIPNetwork, error) {
	fail := func() (SourceVIPNetwork, error) { return SourceVIPNetwork{}, ErrPreparationConfig }
	object, err := sourceExactObject(raw, "source_network", "vip")
	if err != nil {
		return fail()
	}
	network, err := parsePublicSourceNetwork(object["source_network"])
	if err != nil {
		return fail()
	}
	if _, err := sourceExactObject(object["vip"], "address", "present", "interface", "index", "mac", "network_namespace"); err != nil {
		return fail()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(object["vip"], &fields) != nil {
		return fail()
	}
	for _, field := range fields {
		if string(field) == "null" {
			return fail()
		}
	}
	var vip VIPAddress
	if json.Unmarshal(object["vip"], &vip) != nil || vip.Validate(network.ManagementLink) != nil {
		return fail()
	}
	return SourceVIPNetwork{network, vip}, nil
}

func NewSourceVIPReceipt(raw []byte, nonce, jobUID, podUID, address string) ([]byte, error) {
	if !ValidSourceReceiptIdentity(nonce, jobUID, podUID) || !ValidVIPRequest(address) {
		return nil, ErrPreparationConfig
	}
	object, err := sourceExactObject(raw, "ok", "verb", "result")
	var ok bool
	var verb string
	if err != nil || json.Unmarshal(object["ok"], &ok) != nil || !ok || json.Unmarshal(object["verb"], &verb) != nil || verb != "InspectVIPNetwork" {
		return nil, ErrPreparationConfig
	}
	result, err := sourceExactObject(object["result"], "source_vip_network")
	if err != nil {
		return nil, ErrPreparationConfig
	}
	value, err := parseSourceVIPNetwork(result["source_vip_network"])
	if err != nil || value.VIP.Address != address {
		return nil, ErrPreparationConfig
	}
	out, err := json.Marshal(sourceVIPReceipt{1, nonce, jobUID, podUID, value})
	if err != nil || len(out) > SourceNetworkReceiptLimit {
		return nil, ErrPreparationConfig
	}
	return out, nil
}

func ParseSourceVIPReceipt(raw []byte, nonce, jobUID, podUID, address string) (SourceVIPNetwork, error) {
	fail := func() (SourceVIPNetwork, error) { return SourceVIPNetwork{}, ErrPreparationConfig }
	if !ValidSourceReceiptIdentity(nonce, jobUID, podUID) || !ValidVIPRequest(address) {
		return fail()
	}
	object, err := sourceExactObject(raw, "version", "nonce", "job_uid", "pod_uid", "source_vip_network")
	var receipt sourceVIPReceipt
	if err != nil || json.Unmarshal(raw, &receipt) != nil || receipt.Version != 1 || receipt.Nonce != nonce || receipt.JobUID != jobUID || receipt.PodUID != podUID {
		return fail()
	}
	value, err := parseSourceVIPNetwork(object["source_vip_network"])
	if err != nil || value.VIP.Address != address {
		return fail()
	}
	return value, nil
}
