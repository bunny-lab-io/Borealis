package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func sourceVIPFixture(t *testing.T, present bool) (clusterbootstrap.SourceNetwork, []byte) {
	t.Helper()
	link, err := clusterbootstrap.ParseManagementLink([]byte(sourceManagementFixture), 1234, "192.168.90.20")
	if err != nil {
		t.Fatal(err)
	}
	n := clusterbootstrap.SourceNetwork{NodeUID: "11111111-1111-4111-8111-111111111111", Hostname: "engine-01", MachineID: strings.Repeat("b", 32), BootID: "22222222-2222-4222-8222-222222222222", K3sVersion: "v1.36.3+k3s1", PodCIDR: "10.42.0.0/16", ServiceCIDR: "10.43.0.0/16", ManagementLink: link}
	var rows []map[string]any
	_ = json.Unmarshal([]byte(sourceManagementFixture), &rows)
	if present {
		rows[0]["addr_info"] = append(rows[0]["addr_info"].([]any), map[string]any{"family": "inet", "local": "192.168.90.250", "prefixlen": 32, "scope": "global", "valid_life_time": 4294967295, "preferred_life_time": 4294967295})
	}
	raw, _ := json.Marshal(rows)
	return n, raw
}

func TestSourceVIPNetworkRepeatedHostAndKernelObservations(t *testing.T) {
	for _, mode := range []string{"present", "absent", "invalid VIP", "namespace changed", "namespace failure", "source failure", "Node changed", "machine changed", "boot changed", "link changed", "read failure", "malformed", "oversize", "presence changed", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			n, raw := sourceVIPFixture(t, mode != "absent")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sources, spaces, reads := 0, 0, 0
			source := func(context.Context) (clusterbootstrap.SourceNetwork, error) {
				sources++
				v := n
				if mode == "source failure" {
					return v, errors.New("private supervisor")
				}
				if sources > 1 {
					switch mode {
					case "Node changed":
						v.NodeUID = v.BootID
					case "machine changed":
						v.MachineID = strings.Repeat("c", 32)
					case "boot changed":
						v.BootID = v.NodeUID
					case "link changed":
						v.ManagementLink.Index++
					}
				}
				return v, nil
			}
			namespace := func() (uint64, error) {
				spaces++
				if mode == "namespace failure" {
					return 0, errors.New("private namespace")
				}
				if mode == "namespace changed" && spaces > 1 {
					return 1235, nil
				}
				return 1234, nil
			}
			read := func(ctx context.Context) ([]byte, error) {
				reads++
				switch mode {
				case "read failure":
					return nil, errors.New("private command")
				case "malformed":
					return []byte(`[{"private":"diagnostic"}]`), nil
				case "oversize":
					return []byte(strings.Repeat(" ", 131073)), nil
				case "presence changed":
					if reads > 1 {
						return []byte(sourceManagementFixture), nil
					}
				case "cancel":
					cancel()
					<-ctx.Done()
				}
				return raw, nil
			}
			address := "192.168.90.250"
			if mode == "invalid VIP" {
				address = "$(id)"
			}
			v, err := observeSourceVIPNetwork(ctx, address, source, namespace, read)
			if mode == "present" || mode == "absent" {
				if err != nil || v.Network != n || v.VIP.Present != (mode == "present") || sources != 2 || spaces != 4 || reads != 2 {
					t.Fatal("incomplete ownership observation", err)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || v != (clusterbootstrap.SourceVIPNetwork{}) {
				t.Fatal("unsafe observation escaped", err)
			}
			if mode == "invalid VIP" && sources+spaces+reads != 0 {
				t.Fatal("invalid input reached host")
			}
		})
	}
}

func TestSourceVIPActionRejectsAmbiguousRequestBeforeHostAccess(t *testing.T) {
	m := &manager{}
	for _, params := range []map[string]any{nil, {"vip": nil}, {"vip": 123}, {"vip": "8.8.8.8"}, {"vip": "192.168.90.250", "path": "/private"}} {
		if out, err := m.execute(context.Background(), actionRequest{Verb: "InspectVIPNetwork", Params: params}); err != clusterbootstrap.ErrPreparationConfig || out != nil {
			t.Fatal("invalid params reached host")
		}
	}
	if nodeManagerActionTimeout("InspectVIPNetwork") != 15*time.Second {
		t.Fatal("unbounded action")
	}
	for _, raw := range []string{
		`{"verb":" InspectVIPNetwork ","params":{"vip":"192.168.90.250"}}`,
		`{"verb":"InspectVIPNetwork","params":{"vip":"192.168.90.250","VIP":"192.168.90.249"}}`,
		`{"Verb":"InspectVIPNetwork","params":{"vip":"192.168.90.250"}}`,
		`{"verb":"InspectVIPNetwork","params":{"vip":"192.168.90.250"},"extra":true}`,
		`{"verb":"InspectVIPNetwork","verb":"InspectVIPNetwork","params":{"vip":"192.168.90.250"}}`,
	} {
		w := httptest.NewRecorder()
		m.handleAction(w, httptest.NewRequest("POST", "/v1/action", strings.NewReader(raw)))
		if w.Code != 400 {
			t.Fatal("ambiguous request reached host", w.Code)
		}
	}
	for _, args := range [][]string{nil, {"invalid"}, {"11111111-1111-4111-8111-111111111111", "$(id)"}, {"11111111-1111-4111-8111-111111111111", "192.168.90.250", "extra"}} {
		if sourceObservationClient(args, true) != clusterbootstrap.ErrPreparationConfig {
			t.Fatal("invalid CLI accessed host")
		}
	}
}

func TestSourceVIPClientFixedRequestAndReceipt(t *testing.T) {
	n, raw := sourceVIPFixture(t, true)
	vip, _ := clusterbootstrap.ParseVIPAddress(raw, n.ManagementLink, "192.168.90.250")
	value := clusterbootstrap.SourceVIPNetwork{Network: n, VIP: vip}
	nonce, job, pod := n.NodeUID, n.BootID, "33333333-3333-4333-8333-333333333333"
	for _, mode := range []string{"success", "redirect", "wrong VIP", "legacy receipt", "private field", "bad request", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := &http.Client{Transport: sourceClientRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				body, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || r.URL.String() != "http://node-manager/v1/action" || string(body) != `{"verb":"InspectVIPNetwork","params":{"vip":"192.168.90.250"}}` || r.Header.Get(managerTokenHeader) != strings.Repeat("t", 32) {
					t.Fatal("unexpected request")
				}
				w := httptest.NewRecorder()
				if mode == "redirect" {
					w.Header().Set("Location", "http://forbidden.example")
					w.WriteHeader(302)
					return w.Result(), nil
				}
				if mode == "cancel" {
					cancel()
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				v := value
				if mode == "wrong VIP" {
					v.VIP.Address = "192.168.90.249"
				}
				verb := "InspectVIPNetwork"
				result := map[string]any{"source_vip_network": v}
				if mode == "legacy receipt" {
					verb = "InspectSourceNetwork"
					result = map[string]any{"source_network": n}
				}
				if mode == "private field" {
					result["private"] = "never publish"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "verb": verb, "result": result})
				return w.Result(), nil
			})}
			address := vip.Address
			if mode == "bad request" {
				address = "8.8.8.8"
			}
			out, err := readSourceObservationReceipt(ctx, client, strings.Repeat("t", 32), nonce, job, pod, address)
			if mode == "success" {
				got, parseErr := clusterbootstrap.ParseSourceVIPReceipt(out, nonce, job, pod, address)
				if err != nil || parseErr != nil || got != value {
					t.Fatal("receipt failed", err, parseErr)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || out != nil {
				t.Fatal("unsafe response accepted", err)
			}
			if calls > 1 || (mode == "bad request" && calls != 0) {
				t.Fatal("unexpected request/replay")
			}
		})
	}
}
