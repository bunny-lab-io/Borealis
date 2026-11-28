package clusterbootstrap

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func preparedAdmissionFixture(t *testing.T) PreparedAdmissionRequest {
	t.Helper()
	r := artifactSessionRequestFixture(t)
	e, _ := r.Expected()
	return PreparedAdmissionRequest{Version: 1, Action: "PromoteCandidate", StartBefore: 2000000000, Expected: e, Receipt: preparationReceiptFixture(e, *r.IdentitySource)}
}

func TestPreparedAdmissionProtocol(t *testing.T) {
	r := preparedAdmissionFixture(t)
	for _, action := range []string{"RunK3sProbeConformance", "RedeployRevision", "InspectCandidateHealth", "PromoteCandidate", "InspectHealth", "EnterApplicationDrain", "ExitApplicationDrain", "ReconcilePreparedAdmissionStopped"} {
		r.Action = action
		encoded, err := EncodePreparedAdmissionRequest(r)
		got, decodeErr := DecodePreparedAdmissionRequest(encoded)
		if err != nil || decodeErr != nil || !reflect.DeepEqual(got, r) {
			t.Fatal("round trip", err, decodeErr)
		}
		body, _ := json.Marshal(map[string]any{"verb": PreparedAdmissionVerb, "params": map[string]any{"payload": encoded}})
		if got, err := ParsePreparedAdmissionActionRequest(body); err != nil || !reflect.DeepEqual(got, r) {
			t.Fatal("action", err)
		}
		ack, _ := json.Marshal(map[string]any{"ok": true, "verb": PreparedAdmissionVerb, "result": map[string]any{"version": 1, "request_sha256": r.SHA256()}})
		if ValidatePreparedAdmissionAcknowledgement(ack, r) != nil {
			t.Fatal("ack")
		}
		for _, changed := range [][]byte{bytes.Replace(ack, []byte(`"version":1`), []byte(`"version":0`), 1), bytes.Replace(ack, []byte(`"ok":true`), []byte(`"OK":true`), 1), bytes.Replace(ack, []byte(r.SHA256()), []byte(strings.Repeat("a", 64)), 1), []byte(`{"ok":true,"verb":"PromoteCandidate","result":{}}`), append(ack, []byte(`{}`)...)} {
			if ValidatePreparedAdmissionAcknowledgement(changed, r) == nil {
				t.Fatal("unbound ack accepted")
			}
		}
		for _, changed := range [][]byte{bytes.Replace(body, []byte(`"verb":`), []byte(`"verb":"PromoteCandidate","Verb":`), 1), bytes.Replace(body, []byte(`"params":{`), []byte(`"params":{"target_sha":"x",`), 1), bytes.Replace(body, []byte(PreparedAdmissionVerb), []byte("PromoteCandidate"), 1), append(body, []byte(`{}`)...)} {
			if _, err := ParsePreparedAdmissionActionRequest(changed); err == nil {
				t.Fatal("malformed action accepted")
			}
		}
	}
	r.Action = "ExitApplicationDrain"
	raw, _ := json.Marshal(r)
	for _, changed := range [][]byte{bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":2`), 1), bytes.Replace(raw, []byte(`"version":1`), []byte(`"Version":1`), 1), bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1), bytes.Replace(raw, []byte(`"action":"ExitApplicationDrain"`), []byte(`"action":"EnterApplicationDrainAgain"`), 1), bytes.Replace(raw, []byte(`"steps":[`), []byte(`"steps":[{},`), 1), append(raw, 10)} {
		if _, err := DecodePreparedAdmissionRequest(base64.RawStdEncoding.EncodeToString(changed)); err == nil {
			t.Fatal("noncanonical request accepted")
		}
	}
	if _, err := DecodePreparedAdmissionRequest(strings.Repeat("A", PreparedAdmissionRequestLimit+1)); err == nil {
		t.Fatal("oversized")
	}
}

func TestPreparedAdmissionNodeIdentity(t *testing.T) {
	e, raw := joinedNodeFixture(t)
	r := preparedAdmissionFixture(t)
	r.Expected = e
	r.Receipt = preparationReceiptFixture(e, r.Receipt.Source)
	for _, state := range []string{"drained", "active"} {
		for _, eligible := range []string{"false", "true"} {
			t.Run(state+"/"+eligible, func(t *testing.T) {
				var node map[string]any
				_ = json.Unmarshal(raw, &node)
				metadata := node["metadata"].(map[string]any)
				labels := metadata["labels"].(map[string]any)
				labels["borealis.io/application-state"], labels["borealis.io/hmr-target"] = state, "false"
				for _, role := range []string{"control-plane", "edge", "scheduler", "postgres-primary"} {
					labels["borealis.io/"+role+"-eligible"] = eligible
				}
				valid, _ := json.Marshal(node)
				if got, err := ParsePreparedAdmissionNode(valid, r); err != nil || got != r.Receipt.Node {
					t.Fatal("valid phase", err)
				}
				if state != "drained" || eligible != "false" {
					if _, err := ParseK3sJoinedNode(valid, e); err == nil {
						t.Fatal("legacy drained check relaxed")
					}
				}
				for _, field := range []string{"uid", "machineID", "bootID", "deletionTimestamp", "role", "missing role", "hmr", "empty hmr", "state", "alias"} {
					var changed map[string]any
					_ = json.Unmarshal(valid, &changed)
					meta := changed["metadata"].(map[string]any)
					ls := meta["labels"].(map[string]any)
					info := changed["status"].(map[string]any)["nodeInfo"].(map[string]any)
					switch field {
					case "uid":
						meta[field] = "88888888-8888-4888-8888-888888888888"
					case "machineID":
						info[field] = strings.Repeat("e", 32)
					case "bootID":
						info[field] = "88888888-8888-4888-8888-888888888888"
					case "deletionTimestamp":
						meta[field] = "2026-11-10T00:00:00Z"
					case "role":
						ls["borealis.io/edge-eligible"] = map[string]string{"true": "false", "false": "true"}[eligible]
					case "missing role":
						delete(ls, "borealis.io/edge-eligible")
					case "hmr":
						ls["borealis.io/hmr-target"] = "true"
					case "empty hmr":
						ls["borealis.io/hmr-target"] = ""
					case "state":
						ls["borealis.io/application-state"] = "unknown"
					case "alias":
						meta["UID"] = meta["uid"]
						delete(meta, "uid")
					}
					bad, _ := json.Marshal(changed)
					if _, err := ParsePreparedAdmissionNode(bad, r); err == nil {
						t.Fatal("changed identity accepted", field)
					}
				}
			})
		}
	}
}

func TestPreparedAdmissionNativeJoinHMRLabel(t *testing.T) {
	e, raw := joinedNodeFixture(t)
	r := preparedAdmissionFixture(t)
	r.Expected = e
	r.Receipt = preparationReceiptFixture(e, r.Receipt.Source)
	config, err := PrejoinK3sJoinConfiguration(e)
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]any{}
	for _, line := range strings.Split(config, "\n") {
		if label, ok := strings.CutPrefix(line, "  - borealis.io/"); ok {
			key, value, found := strings.Cut(label, "=")
			if !found {
				t.Fatal("invalid rendered label")
			}
			labels["borealis.io/"+key] = value
		}
	}
	if _, exists := labels["borealis.io/hmr-target"]; exists {
		t.Fatal("fixture no longer exercises omitted native HMR marker")
	}
	for _, mode := range []string{"absent", "false", "true", "empty", "invalid", "null", "boolean", "alias", "duplicate alias"} {
		t.Run(mode, func(t *testing.T) {
			var node map[string]any
			if json.Unmarshal(raw, &node) != nil {
				t.Fatal("fixture")
			}
			actual := map[string]any{}
			for key, value := range labels {
				actual[key] = value
			}
			switch mode {
			case "false", "true":
				actual["borealis.io/hmr-target"] = mode
			case "empty":
				actual["borealis.io/hmr-target"] = ""
			case "invalid":
				actual["borealis.io/hmr-target"] = "False"
			case "null":
				actual["borealis.io/hmr-target"] = nil
			case "boolean":
				actual["borealis.io/hmr-target"] = false
			case "alias":
				actual["borealis.io/HMR-target"] = "false"
			case "duplicate alias":
				actual["borealis.io/hmr-target"], actual["borealis.io/HMR-target"] = "false", "true"
			}
			node["metadata"].(map[string]any)["labels"] = actual
			encoded, _ := json.Marshal(node)
			got, err := ParsePreparedAdmissionNode(encoded, r)
			valid := mode == "absent" || mode == "false"
			if (err == nil) != valid || valid && got != r.Receipt.Node {
				t.Fatal("native HMR label policy", err)
			}
		})
	}
}

func TestPreparedAdmissionMembershipOptIn(t *testing.T) {
	r, _ := membershipObserverProtocolFixture(t, true, 1)
	r.RequireListenerProof, r.RequiredListenerProofVersion = true, MembershipQuorumProofVersion
	legacy, _ := EncodeMembershipObservationRequest(r)
	oldDigest := r.SHA256()
	r.PreparedAdmission = true
	encoded, err := EncodeMembershipObservationRequest(r)
	got, decodeErr := DecodeMembershipObservationRequest(encoded)
	if err != nil || decodeErr != nil || !got.PreparedAdmission || r.SHA256() == oldDigest || legacy == encoded {
		t.Fatal("phase binding", err, decodeErr)
	}
	for _, version := range []uint8{0, 1, 2} {
		bad := r
		bad.RequiredListenerProofVersion = version
		if bad.Valid() {
			t.Fatal("capability downgrade")
		}
	}
	bad := r
	bad.Observer = nil
	if bad.Valid() {
		t.Fatal("legacy observer opted in")
	}
	raw, _ := base64.RawStdEncoding.DecodeString(encoded)
	for _, field := range []string{`"prepared_admission":false`, `"Prepared_Admission":true`, `"prepared_admission":null`, `"prepared_admission":true,"prepared_admission":true`} {
		bad := bytes.Replace(raw, []byte(`"prepared_admission":true`), []byte(field), 1)
		if _, err := DecodeMembershipObservationRequest(base64.RawStdEncoding.EncodeToString(bad)); err == nil {
			t.Fatal("noncanonical opt-in", field)
		}
	}
}
