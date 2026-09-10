package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type sourceClientRoundTrip func(*http.Request) (*http.Response, error)

func (f sourceClientRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSourceNetworkClientFixedRequestAndStaticFailures(t *testing.T) {
	nonce, job, pod := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	network := clusterbootstrap.SourceNetwork{NodeUID: pod, Hostname: "engine-01", MachineID: strings.Repeat("b", 32), BootID: job, K3sVersion: "v1.36.3+k3s1", PodCIDR: "10.42.0.0/16", ServiceCIDR: "10.43.0.0/16"}
	for _, mode := range []string{"success", "bad status", "redirect", "oversize", "private field", "cancel", "bad identity"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			calls := 0
			client := &http.Client{Transport: sourceClientRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				body, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || r.URL.String() != "http://node-manager/v1/action" || string(body) != `{"verb":"InspectSourceNetwork","params":{}}` || r.Header.Get(managerTokenHeader) != strings.Repeat("t", 32) {
					t.Fatal("request contract changed")
				}
				if mode == "cancel" {
					cancel()
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				w := httptest.NewRecorder()
				if mode == "bad status" {
					w.WriteHeader(500)
					w.WriteString("private error")
					return w.Result(), nil
				}
				if mode == "redirect" {
					w.Header().Set("Location", "http://forbidden.example")
					w.WriteHeader(302)
					return w.Result(), nil
				}
				if mode == "oversize" {
					w.WriteString(strings.Repeat("x", 4096))
					return w.Result(), nil
				}
				result := map[string]any{"source_network": network}
				if mode == "private field" {
					result["private"] = "never publish"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "verb": "InspectSourceNetwork", "result": result})
				return w.Result(), nil
			})}
			n := nonce
			if mode == "bad identity" {
				n = "wrong"
			}
			out, err := readSourceNetworkReceipt(ctx, client, strings.Repeat("t", 32), n, job, pod)
			if mode == "success" {
				got, parseErr := clusterbootstrap.ParseSourceNetworkReceipt(out, nonce, job, pod)
				if err != nil || parseErr != nil || got != network {
					t.Fatalf("receipt: %v/%v", err, parseErr)
				}
			} else if err != clusterbootstrap.ErrPreparationConfig || out != nil {
				t.Fatal("failure disclosed data or accepted")
			}
			if calls > 1 || (mode == "bad identity" && calls != 0) {
				t.Fatal("unexpected request/replay")
			}
		})
	}
	if sourceNetworkClient(nil) != clusterbootstrap.ErrPreparationConfig {
		t.Fatal("missing identity allowed host read")
	}
}
