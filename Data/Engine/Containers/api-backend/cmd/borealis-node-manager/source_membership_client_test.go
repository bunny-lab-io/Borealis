package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func sourceMembershipResultFixture(r clusterbootstrap.MembershipObservationRequest) clusterbootstrap.MembershipObservation {
	result := clusterbootstrap.MembershipObservation{Source: r.Source, Observer: r.Observer, RequestSHA256: r.SHA256(), Membership: clusterbootstrap.K3sMembership{Voting: true}}
	for i, node := range r.Nodes {
		id := uint64(math.MaxUint64 - uint64(i))
		for _, target := range r.Prepared {
			if target.Receipt.Node == node.Node {
				id = target.Receipt.MemberID
			}
		}
		result.Membership.Members = append(result.Membership.Members, clusterbootstrap.K3sEtcdMember{ID: id, Name: node.MemberName, Address: node.Node.Address})
		if node.Node.NodeUID == r.ObserverUID() {
			result.LocalNode = node.Node
			result.Membership.LocalID = id
		}
	}
	slices.SortFunc(result.Membership.Members, func(a, b clusterbootstrap.K3sEtcdMember) int { return strings.Compare(a.Address, b.Address) })
	return result
}

func TestSourceMembershipClientGenericActionPrecisionAndFailures(t *testing.T) {
	for _, mode := range []string{"success", "observer source", "observer first target", "observer second target", "wrong observer", "auth", "bad status", "redirect", "oversize", "private field", "wrong request", "wrong verb", "cancel", "bad identity"} {
		t.Run(mode, func(t *testing.T) {
			request := clusterbootstrap.MembershipObservationRequest(membershipObservationFixture(t, true))
			if strings.HasPrefix(mode, "observer ") || mode == "wrong observer" {
				index := 0
				if mode == "observer first target" || mode == "wrong observer" {
					index = 1
				}
				if mode == "observer second target" {
					index = 2
				}
				request = clusterbootstrap.MembershipObservationRequest(membershipObserverRequestFixture(t, true, index))
			}
			payload, err := clusterbootstrap.EncodeMembershipObservationRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			want := sourceMembershipResultFixture(request)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			nonce, job, pod := request.Source.ClusterID, request.Source.SourceID, request.Source.SourceUID
			calls := 0
			m := &manager{secret: []byte(strings.Repeat("t", 32))}
			handler := m.authorized(func(w http.ResponseWriter, r *http.Request) {
				calls++
				raw, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || r.URL.String() != "http://node-manager/v1/action" {
					t.Error("fixed endpoint changed")
				}
				parsed, err := clusterbootstrap.ParseMembershipActionRequest(raw)
				var generic actionRequest
				if err != nil || json.Unmarshal(raw, &generic) != nil {
					t.Fatal("action request", err)
				}
				decoded, err := clusterbootstrap.DecodeMembershipObservationRequest(generic.Params["payload"].(string))
				if err != nil || !reflect.DeepEqual(decoded, parsed) || !reflect.DeepEqual(decoded, request) {
					t.Fatal("generic request changed uint64")
				}
				switch mode {
				case "bad status":
					w.WriteHeader(500)
					return
				case "redirect":
					http.Redirect(w, r, "https://forbidden.invalid", 302)
					return
				case "oversize":
					w.Write([]byte(strings.Repeat("x", 4097)))
					return
				case "cancel":
					cancel()
					return
				}
				result := want
				if mode == "wrong request" {
					result.RequestSHA256 = strings.Repeat("a", 64)
				}
				encoded, err := clusterbootstrap.EncodeMembershipObservationResult(want, request)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "wrong request" { // Emit a different valid request result, not a producer validation bypass.
					altered := request
					altered.Nodes = slices.Clone(request.Nodes)
					altered.Nodes[0], altered.Nodes[1] = altered.Nodes[1], altered.Nodes[0]
					result.RequestSHA256 = altered.SHA256()
					encoded, err = clusterbootstrap.EncodeMembershipObservationResult(result, altered)
					if err != nil {
						t.Fatal(err)
					}
				}
				if mode == "wrong observer" {
					other := clusterbootstrap.MembershipObservationRequest(membershipObserverRequestFixture(t, true, 2))
					encoded, err = clusterbootstrap.EncodeMembershipObservationResult(sourceMembershipResultFixture(other), other)
					if err != nil {
						t.Fatal(err)
					}
				}
				response := map[string]any{"ok": true, "verb": "InspectSourceMembership", "result": map[string]any{"membership_observation": encoded}}
				if mode == "private field" {
					response["private"] = "never publish"
				}
				if mode == "wrong verb" {
					response["verb"] = "InspectHealth"
				}
				// Exercise the same generic map response path without numeric conversion.
				wire, _ := json.Marshal(response)
				var genericResult map[string]any
				if json.Unmarshal(wire, &genericResult) != nil {
					t.Fatal("generic result")
				}
				writeJSON(w, http.StatusOK, genericResult)
			})
			client := &http.Client{Transport: sourceClientRoundTrip(func(r *http.Request) (*http.Response, error) {
				w := httptest.NewRecorder()
				handler(w, r)
				return w.Result(), nil
			})}
			n, token := nonce, strings.Repeat("t", 32)
			if mode == "bad identity" {
				n = "invalid"
			}
			if mode == "auth" {
				token = strings.Repeat("x", 32)
			}
			receipt, err := readSourceMembershipReceipt(ctx, client, token, n, job, pod, payload)
			if mode == "success" || strings.HasPrefix(mode, "observer ") {
				got, parseErr := clusterbootstrap.ParseMembershipObservationReceipt(receipt, request, nonce, job, pod)
				if err != nil || parseErr != nil || !reflect.DeepEqual(got, want) {
					t.Fatal("receipt", err, parseErr)
				}
			} else if err == nil || receipt != nil {
				t.Fatal("failure accepted or leaked")
			}
			if calls > 1 || mode == "bad identity" && calls != 0 || mode == "auth" && calls != 0 {
				t.Fatal("replayed or unauthorized request")
			}
		})
	}
	if sourceMembershipClient(nil) == nil {
		t.Fatal("missing args")
	}
}

func TestSourceMembershipActionRejectsBeforeNativeReads(t *testing.T) {
	r := clusterbootstrap.MembershipObservationRequest(membershipObservationFixture(t, true))
	payload, _ := clusterbootstrap.EncodeMembershipObservationRequest(r)
	for _, raw := range []string{`{"verb":"InspectSourceMembership","params":{"payload":"` + payload + `"},"verb":"InspectSourceMembership"}`, `{"verb":"InspectSourceMembership","params":{"payload":"` + payload + `","extra":true}}`, `{"verb":"InspectSourceMembership","params":{"payload":"` + payload + `"}} {}`, `{"verb":"InspectSourceMembership","params":{"Payload":"` + payload + `"}}`} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/action", strings.NewReader(raw))
		(&manager{}).handleAction(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatal("malformed action reached execution", w.Code)
		}
	}
	if nodeManagerActionTimeout("InspectSourceMembership").Seconds() != 15 {
		t.Fatal("unbounded action")
	}
}
