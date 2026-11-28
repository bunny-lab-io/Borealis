package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestBootstrapK3sStartupMembership(t *testing.T) {
	for _, mode := range []string{"voting", "learner", "promoted", "member replaced", "member disappeared", "voter demoted", "wrong name", "name changed", "name mode", "foreign peer", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			expected := bootstrapHostFilesFixture()
			root, source, join, serverCert, roots := bootstrapJoinedTLSFixture(t, expected, expected.Target.Hostname)
			name := expected.Target.Hostname + "-0123abcd"
			path := "var/lib/rancher/k3s/server/db/etcd/name"
			if root.MkdirAll("var/lib/rancher/k3s/server/db/etcd", 0700) != nil || root.WriteFile(path, []byte(name), 0600) != nil {
				t.Fatal("name fixture")
			}
			if mode == "wrong name" {
				if root.WriteFile(path, []byte("foreign-0123abcd"), 0600) != nil {
					t.Fatal("name mutation")
				}
			}
			if mode == "name mode" {
				if root.Chmod(path, 0644) != nil {
					t.Fatal("mode mutation")
				}
			}
			var mu sync.Mutex
			calls, memberships := 0, 0
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls++
				if r.Method != "GET" || r.Host != "127.0.0.1:6443" || r.Header.Get("Authorization") != "" {
					t.Error("unbound membership transport")
				}
				if mode == "redirect" {
					http.Redirect(w, r, "https://foreign.invalid/", 302)
					return
				}
				if mode == "name changed" && calls == 3 {
					if root.WriteFile(path, []byte(expected.Target.Hostname+"-99999999"), 0600) != nil {
						t.Error("name change")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/version":
					json.NewEncoder(w).Encode(map[string]any{"gitVersion": expected.K3sVersion})
				case "/api/v1/nodes/" + expected.Target.Hostname:
					json.NewEncoder(w).Encode(bootstrapJoinedNodeDocument(expected))
				case "/db/info":
					memberships++
					members := []any{}
					for i, address := range expected.PeerAddresses {
						n := "peer-0123abc" + string(rune('a'+i))
						id := uint64(i + 1)
						learner := false
						if address == expected.Target.Address {
							n = name
							learner = mode == "learner" || mode == "promoted" && memberships == 1 || mode == "voter demoted" && memberships == 2
							if mode == "member replaced" && memberships == 2 {
								id += 100
							}
							if mode == "foreign peer" {
								address = "192.168.50.99"
							}
						} else if mode == "member disappeared" && memberships == 2 {
							continue
						}
						members = append(members, map[string]any{"ID": id, "name": n, "peerURLs": []string{"https://" + address + ":2380"}, "clientURLs": []string{"https://" + address + ":2379"}, "isLearner": learner})
					}
					json.NewEncoder(w).Encode(map[string]any{"members": members})
				default:
					t.Error("unapproved path")
					http.NotFound(w, r)
				}
			}))
			server.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
			server.StartTLS()
			defer server.Close()
			factory := func(config *tls.Config) *http.Client {
				client := newBootstrapJoinedNodeClient(config)
				client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					if address != "127.0.0.1:6443" {
						t.Error("unapproved dial")
					}
					return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
				}
				return client
			}
			proof, err := observeBootstrapK3sStartupAt(context.Background(), root, expected, source, join, func(context.Context) error { return nil }, factory)
			valid := mode == "voting" || mode == "learner" || mode == "promoted"
			if (err == nil) != valid {
				t.Fatal("wrong startup membership observation", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if valid && (calls != 6 || memberships != 2 || !proof.Node.Ready || proof.Membership.Voting == (mode == "learner")) {
				t.Fatal("missing independent proof/learner conflated")
			}
			if !valid && (proof.Node.NodeUID != "" || proof.Membership.Members != nil) {
				t.Fatal("partial observation escaped")
			}
			if (mode == "wrong name" || mode == "name mode") && calls != 0 {
				t.Fatal("foreign private file reached network")
			}
		})
	}
}
