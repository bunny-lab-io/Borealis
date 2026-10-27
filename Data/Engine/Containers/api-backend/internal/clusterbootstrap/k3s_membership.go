package clusterbootstrap

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
)

// Exact pinned K3s /db/info projection. Numeric IDs remain uint64, never float64.
// Learner observation and incomplete cohort are not final admission evidence.
type K3sEtcdMember struct {
	ID      uint64 `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Learner bool   `json:"learner"`
}

type K3sMembership struct {
	Members []K3sEtcdMember `json:"members"`
	LocalID uint64          `json:"local_id"`
	Voting  bool            `json:"voting"`
}

var k3sMemberSuffix = regexp.MustCompile(`^[0-9a-f]{8}$`)

func ValidK3sMemberName(name, hostname string) bool {
	return sessionHostname.MatchString(hostname) && strings.HasPrefix(name, hostname+"-") && k3sMemberSuffix.MatchString(strings.TrimPrefix(name, hostname+"-"))
}

// localName must come from the actual root-owned K3s etcd/name file. The fixed
// TLS loopback reader must prove its custody alongside the current join claim.
func ParseK3sMembership(raw []byte, expected PreparationExpected, localName string) (K3sMembership, error) {
	fail := func() (K3sMembership, error) { return K3sMembership{}, ErrPreparationConfig }
	if expected.Validate() != nil || expected.K3sVersion != K3sPins().Version || !ValidK3sMemberName(localName, expected.Target.Hostname) {
		return fail()
	}
	object, err := sourceJSONObject(raw, 64<<10)
	if err != nil || len(object) != 1 {
		return fail()
	}
	var entries []json.RawMessage
	if json.Unmarshal(object["members"], &entries) != nil || len(entries) < 2 || len(entries) > 3 {
		return fail()
	}
	result := K3sMembership{}
	ids, addresses, names := map[uint64]bool{}, map[string]bool{}, map[string]bool{}
	for _, entry := range entries {
		fields, err := sourceJSONObject(entry, 8<<10)
		if err != nil {
			return fail()
		}
		for key := range fields {
			switch key {
			case "ID", "name", "peerURLs", "clientURLs", "isLearner":
			default:
				return fail()
			}
			if string(fields[key]) == "null" {
				return fail()
			}
		}
		var member struct {
			ID         uint64   `json:"ID"`
			Name       string   `json:"name"`
			PeerURLs   []string `json:"peerURLs"`
			ClientURLs []string `json:"clientURLs"`
			IsLearner  bool     `json:"isLearner"`
		}
		if json.Unmarshal(entry, &member) != nil || member.ID == 0 || ids[member.ID] || len(member.PeerURLs) != 1 || len(member.ClientURLs) > 1 {
			return fail()
		}
		address := ""
		for _, allowed := range expected.PeerAddresses {
			if member.PeerURLs[0] == "https://"+allowed+":2380" {
				address = allowed
				break
			}
		}
		if address == "" || addresses[address] || member.Name != "" && names[member.Name] {
			return fail()
		}
		if len(member.ClientURLs) == 1 && member.ClientURLs[0] != "https://"+address+":2379" {
			return fail()
		}
		// Unstarted other learner may have neither name nor client URL. It must
		// not appear voting or substitute for this target's fully started member.
		if member.Name == "" && (!member.IsLearner || len(member.ClientURLs) != 0) || !member.IsLearner && len(member.ClientURLs) != 1 {
			return fail()
		}
		if member.Name != "" {
			last := strings.LastIndexByte(member.Name, '-')
			if last < 1 || !ValidK3sMemberName(member.Name, member.Name[:last]) {
				return fail()
			}
		}
		ids[member.ID], addresses[address], names[member.Name] = true, true, true
		if address == expected.Target.Address {
			if member.Name != localName || len(member.ClientURLs) != 1 {
				return fail()
			}
			result.LocalID, result.Voting = member.ID, !member.IsLearner
		} else if member.Name == localName {
			return fail()
		}
		result.Members = append(result.Members, K3sEtcdMember{member.ID, member.Name, address, member.IsLearner})
	}
	if result.LocalID == 0 {
		return fail()
	}
	slices.SortFunc(result.Members, func(a, b K3sEtcdMember) int { return strings.Compare(a.Address, b.Address) })
	return result, nil
}
