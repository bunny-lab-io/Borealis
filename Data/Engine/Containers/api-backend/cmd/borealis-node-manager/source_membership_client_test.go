package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func sourceMembershipResultFixture(r clusterbootstrap.MembershipObservationRequest) clusterbootstrap.MembershipObservation {
	result := clusterbootstrap.MembershipObservation{Source: r.Source, Observer: r.Observer, RequestSHA256: r.SHA256(), Membership: clusterbootstrap.K3sMembership{Voting: true}}
	if r.RequireListenerProof {
		result.ListenerProofVersion = clusterbootstrap.MembershipListenerProofVersion
	}
	if r.RequiredListenerProofVersion == clusterbootstrap.MembershipLocalEtcdProofVersion {
		result.ListenerProofVersion = clusterbootstrap.MembershipLocalEtcdProofVersion
	}
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
	if r.RequiredListenerProofVersion == clusterbootstrap.MembershipQuorumProofVersion {
		result.ListenerProofVersion = clusterbootstrap.MembershipQuorumProofVersion
		result.Quorum = &clusterbootstrap.MembershipQuorumRead{ClusterID: 100, MemberID: result.Membership.LocalID}
	}
	slices.SortFunc(result.Membership.Members, func(a, b clusterbootstrap.K3sEtcdMember) int { return strings.Compare(a.Address, b.Address) })
	return result
}

type membershipProofClientTransport struct {
	roundTrip func(*http.Request) (*http.Response, error)
	idle      atomic.Int32
}

func (t *membershipProofClientTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.roundTrip(r)
}
func (t *membershipProofClientTransport) CloseIdleConnections() { t.idle.Add(1) }

type membershipProofClientBody struct {
	io.ReadCloser
	close func() error
}

func (b *membershipProofClientBody) Close() error {
	err := b.ReadCloser.Close()
	if closeErr := b.close(); closeErr != nil {
		return closeErr
	}
	return err
}

func TestSourceMembershipClientListenerProofCleanup(t *testing.T) {
	exerciseSourceMembershipClientProofCleanup(t, clusterbootstrap.MembershipListenerProofVersion)
}

func TestSourceMembershipClientLocalEtcdProofCleanup(t *testing.T) {
	exerciseSourceMembershipClientProofCleanup(t, clusterbootstrap.MembershipLocalEtcdProofVersion)
}

func TestSourceMembershipClientQuorumProofCleanup(t *testing.T) {
	exerciseSourceMembershipClientProofCleanup(t, clusterbootstrap.MembershipQuorumProofVersion)
}

func exerciseSourceMembershipClientProofCleanup(t *testing.T, proof uint8) {
	for _, mode := range []string{"success", "old producer", "close error", "cancel during close", "blocked close success", "status failure cleanup"} {
		t.Run(mode, func(t *testing.T) {
			r := clusterbootstrap.MembershipObservationRequest(membershipObserverRequestFixture(t, true, 1))
			r.RequireListenerProof = true
			if proof == clusterbootstrap.MembershipLocalEtcdProofVersion || proof == clusterbootstrap.MembershipQuorumProofVersion {
				r.RequiredListenerProofVersion = proof
			}
			payload, err := clusterbootstrap.EncodeMembershipObservationRequest(r)
			if err != nil {
				t.Fatal(err)
			}
			nonce, job, pod := r.Source.ClusterID, r.Source.SourceID, r.Source.SourceUID
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var unblock sync.Once
			t.Cleanup(func() { cancel(); unblock.Do(func() { close(release) }) })
			var calls, closes atomic.Int32
			transport := &membershipProofClientTransport{roundTrip: func(req *http.Request) (*http.Response, error) {
				call := calls.Add(1)
				raw, err := io.ReadAll(req.Body)
				parsed, parseErr := clusterbootstrap.ParseMembershipActionRequest(raw)
				if err != nil || parseErr != nil || !reflect.DeepEqual(parsed, r) || req.Method != http.MethodPost || req.URL.String() != "http://node-manager/v1/action" || req.Header.Get(managerTokenHeader) != strings.Repeat("t", 32) {
					t.Error("capability request lost fixed private transport or binding", err, parseErr)
				}
				value := sourceMembershipResultFixture(r)
				if call == 1 && mode == "old producer" {
					value.ListenerProofVersion = proof - 1
				}
				// Raw producer response tests actual parser instead of its encoder.
				encoded, _ := json.Marshal(value)
				raw, _ = json.Marshal(map[string]any{"ok": true, "verb": "InspectSourceMembership", "result": map[string]any{"membership_observation": base64.RawStdEncoding.EncodeToString(encoded)}})
				status := http.StatusOK
				if call == 1 && mode == "status failure cleanup" {
					status = http.StatusForbidden
				}
				body := &membershipProofClientBody{io.NopCloser(strings.NewReader(string(raw))), func() error {
					closes.Add(1)
					if call == 1 && (mode == "cancel during close" || mode == "blocked close success") {
						close(entered)
						<-release
					}
					if call == 1 && mode == "close error" {
						return errors.New("private close diagnostic")
					}
					return nil
				}}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: body}, nil
			}}
			client := &http.Client{Transport: transport}
			type outcome struct {
				receipt []byte
				err     error
			}
			done := make(chan outcome, 1)
			go func() {
				receipt, err := readSourceMembershipReceipt(ctx, client, strings.Repeat("t", 32), nonce, job, pod, payload)
				done <- outcome{receipt, err}
			}()
			if mode == "cancel during close" || mode == "blocked close success" {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("owned body cleanup not entered")
				}
				if mode == "cancel during close" {
					cancel()
				}
				select {
				case <-done:
					t.Fatal("receipt escaped unjoined body cleanup")
				case <-time.After(20 * time.Millisecond):
				}
				unblock.Do(func() { close(release) })
			}
			var result outcome
			select {
			case result = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("receipt cleanup failed to join")
			}
			if mode == "success" || mode == "blocked close success" {
				value, err := clusterbootstrap.ParseMembershipObservationReceipt(result.receipt, r, nonce, job, pod)
				if result.err != nil || err != nil || value.ListenerProofVersion != proof {
					t.Fatal("joined capability rejected", result.err, err)
				}
			} else if result.err != clusterbootstrap.ErrPreparationConfig || result.receipt != nil {
				t.Fatal("unproven receipt or private diagnostic escaped", result.err)
			}
			if closes.Load() != 1 || transport.idle.Load() != 0 {
				t.Fatal("owned body cleanup or shared client changed", closes.Load(), transport.idle.Load())
			}
			// A failed request cannot cancel or close a sibling's shared client.
			receipt, err := readSourceMembershipReceipt(context.Background(), client, strings.Repeat("t", 32), nonce, job, pod, payload)
			if err != nil || len(receipt) == 0 || closes.Load() != 2 || transport.idle.Load() != 0 {
				t.Fatal("sibling request lost independent lifetime", err)
			}
		})
	}
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
