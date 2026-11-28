package clusterbootstrap

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
)

func TestK3sMembershipProof(t *testing.T) {
	for _, mode := range []string{"voting", "learner", "other registering", "two members", "foreign peer", "wrong client", "duplicate ID", "duplicate IP", "duplicate name", "missing local", "local name", "empty voting", "zero ID", "ID string", "fractional ID", "overflow ID", "alias ID", "null learner", "duplicate JSON", "unknown", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			expected, _ := preparationFixture()
			name := expected.Target.Hostname + "-0123abcd"
			members := []any{}
			local := 0
			for i, address := range expected.PeerAddresses {
				n := "peer-0123abc" + string(rune('a'+i))
				if address == expected.Target.Address {
					n, local = name, i
				}
				members = append(members, map[string]any{"ID": uint64(math.MaxUint64) - uint64(i), "name": n, "peerURLs": []string{"https://" + address + ":2380"}, "clientURLs": []string{"https://" + address + ":2379"}})
			}
			own := members[local].(map[string]any)
			other := members[(local+1)%3].(map[string]any)
			switch mode {
			case "learner":
				own["isLearner"] = true
			case "other registering":
				delete(other, "name")
				delete(other, "clientURLs")
				other["isLearner"] = true
			case "two members":
				members = []any{own, other}
			case "foreign peer":
				own["peerURLs"] = []string{"https://192.168.50.99:2380"}
			case "wrong client":
				own["clientURLs"] = []string{"http://" + expected.Target.Address + ":2379"}
			case "duplicate ID":
				other["ID"] = own["ID"]
			case "duplicate IP":
				other["peerURLs"] = own["peerURLs"]
			case "duplicate name":
				other["name"] = own["name"]
			case "missing local":
				members = []any{other, members[(local+2)%3]}
			case "local name":
				own["name"] = expected.Target.Hostname + "-99999999"
			case "empty voting":
				delete(other, "name")
			case "zero ID":
				own["ID"] = 0
			case "ID string":
				own["ID"] = "18446744073709551615"
			case "fractional ID":
				own["ID"] = 1.5
			case "alias ID":
				own["id"] = own["ID"]
				delete(own, "ID")
			case "null learner":
				own["isLearner"] = nil
			case "unknown":
				own["healthy"] = true
			}
			raw, _ := json.Marshal(map[string]any{"members": members})
			switch mode {
			case "overflow ID":
				raw = bytes.Replace(raw, []byte(`"ID":18446744073709551615`), []byte(`"ID":18446744073709551616`), 1)
			case "duplicate JSON":
				raw = bytes.Replace(raw, []byte(`"members":`), []byte(`"members":[],"members":`), 1)
			case "oversized":
				raw = append(raw, bytes.Repeat([]byte{' '}, 64<<10)...)
			}
			proof, err := ParseK3sMembership(raw, expected, name)
			valid := mode == "voting" || mode == "learner" || mode == "other registering" || mode == "two members"
			if (err == nil) != valid {
				t.Fatal("wrong membership result", err)
			}
			if valid && (proof.LocalID != uint64(math.MaxUint64)-uint64(local) || proof.Voting == (mode == "learner") || len(proof.Members) != len(members)) {
				t.Fatal("membership identity/learner misrepresented")
			}
			if !valid && (proof.Members != nil || proof.LocalID != 0 || proof.Voting) {
				t.Fatal("partial result escaped")
			}
		})
	}
}
