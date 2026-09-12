package clusterbootstrap

import "encoding/json"

// SourceNetworkReceiptLimit stays below Kubernetes' 4096-byte termination
// message limit. Only this public projection may enter Pod status.
const SourceNetworkReceiptLimit = 2048

type sourceNetworkReceipt struct {
	Version int           `json:"version"`
	Nonce   string        `json:"nonce"`
	JobUID  string        `json:"job_uid"`
	PodUID  string        `json:"pod_uid"`
	Network SourceNetwork `json:"source_network"`
}

func ValidSourceReceiptIdentity(nonce, jobUID, podUID string) bool {
	return nonzeroPreparationUUID(nonce) && nonzeroPreparationUUID(jobUID) && nonzeroPreparationUUID(podUID)
}

func sourceExactObject(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	object, err := sourceJSONObject(raw, SourceNetworkReceiptLimit)
	if err != nil || len(object) != len(keys) {
		return nil, ErrPreparationConfig
	}
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			return nil, ErrPreparationConfig
		}
	}
	return object, nil
}

func parsePublicSourceNetwork(raw []byte) (SourceNetwork, error) {
	var network SourceNetwork
	object, err := sourceExactObject(raw, "node_uid", "hostname", "machine_id", "boot_id", "k3s_version", "pod_cidr", "service_cidr", "management_link")
	if err != nil ||
		json.Unmarshal(raw, &network) != nil || network.Validate() != nil {
		return SourceNetwork{}, ErrPreparationConfig
	}
	if _, err := sourceExactObject(object["management_link"], "interface", "index", "address", "mac", "network_namespace"); err != nil {
		return SourceNetwork{}, ErrPreparationConfig
	}
	return network, nil
}

// The fixed client validates the entire local manager response before writing
// a receipt. No raw manager error, supervisor data or private field is copied.
func NewSourceNetworkReceipt(raw []byte, nonce, jobUID, podUID string) ([]byte, error) {
	if !ValidSourceReceiptIdentity(nonce, jobUID, podUID) {
		return nil, ErrPreparationConfig
	}
	object, err := sourceExactObject(raw, "ok", "verb", "result")
	var ok bool
	var verb string
	if err != nil || json.Unmarshal(object["ok"], &ok) != nil || !ok || json.Unmarshal(object["verb"], &verb) != nil || verb != "InspectSourceNetwork" {
		return nil, ErrPreparationConfig
	}
	result, err := sourceExactObject(object["result"], "source_network")
	if err != nil {
		return nil, ErrPreparationConfig
	}
	network, err := parsePublicSourceNetwork(result["source_network"])
	if err != nil {
		return nil, ErrPreparationConfig
	}
	receipt, err := json.Marshal(sourceNetworkReceipt{2, nonce, jobUID, podUID, network})
	if err != nil || len(receipt) > SourceNetworkReceiptLimit {
		return nil, ErrPreparationConfig
	}
	return receipt, nil
}

func ParseSourceNetworkReceipt(raw []byte, nonce, jobUID, podUID string) (SourceNetwork, error) {
	fail := func() (SourceNetwork, error) { return SourceNetwork{}, ErrPreparationConfig }
	if !ValidSourceReceiptIdentity(nonce, jobUID, podUID) {
		return fail()
	}
	object, err := sourceExactObject(raw, "version", "nonce", "job_uid", "pod_uid", "source_network")
	var receipt sourceNetworkReceipt
	if err != nil || json.Unmarshal(raw, &receipt) != nil || receipt.Version != 2 || receipt.Nonce != nonce || receipt.JobUID != jobUID || receipt.PodUID != podUID {
		return fail()
	}
	return parsePublicSourceNetwork(object["source_network"])
}
