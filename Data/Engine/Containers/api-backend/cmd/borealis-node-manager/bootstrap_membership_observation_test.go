package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func membershipReceiptFixture(e clusterbootstrap.PreparationExpected, source engineidentity.Binding, uid string, id uint64) clusterbootstrap.PrejoinPreparationReceipt {
	digest := func(value any) string {
		raw, _ := json.Marshal(value)
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
	hash := func(value string) string { sum := sha256.Sum256([]byte(value)); return hex.EncodeToString(sum[:]) }
	r := clusterbootstrap.PrejoinPreparationReceipt{Version: 1, ExpectedSHA256: digest(e), Source: source,
		Node: clusterbootstrap.K3sJoinedNode{NodeUID: uid, Hostname: e.Target.Hostname, MachineID: e.Target.MachineID, BootID: e.TargetBootID, Address: e.Target.Address, K3sVersion: e.K3sVersion, Ready: true}, MemberID: id, MemberName: e.Target.Hostname + "-12345678", ImageTargetsSHA256: strings.Repeat("e", 64)}
	for i, name := range []string{"stage_source", "prepare_runtime_identity", "prepare_identity_root", "install_identity", "prepare_host", "stage_images", "install_k3s", "configure_k3s", "start_k3s", "verify_images", "activate_identity"} {
		r.Steps[i] = clusterbootstrap.PreparationStepReceipt{Step: name, InputSHA256: strings.Repeat("a", 64), ResultSHA256: strings.Repeat("a", 64)}
	}
	r.Steps[8].InputSHA256 = hash("prepared-k3s-start-v1\x00" + r.Steps[7].ResultSHA256)
	r.Steps[8].ResultSHA256 = digest(struct {
		Domain, Input string
		Node          clusterbootstrap.K3sJoinedNode
		MemberID      uint64
		MemberName    string
	}{"prepared-k3s-started-v1", r.Steps[8].InputSHA256, r.Node, r.MemberID, r.MemberName})
	r.Steps[9].InputSHA256 = hash("prepared-image-verification-v1\x00" + r.Steps[8].InputSHA256 + "\x00" + r.Steps[8].ResultSHA256 + "\x00" + r.ImageTargetsSHA256)
	r.Steps[9].ResultSHA256 = digest(struct{ Domain, Input, Startup, Targets string }{"prepared-image-verification-receipt-v1", r.Steps[9].InputSHA256, r.Steps[8].ResultSHA256, r.ImageTargetsSHA256})
	r.Steps[10].InputSHA256 = hash("prejoin-identity-activation-v1\x00" + r.Steps[9].InputSHA256 + "\x00" + r.Steps[9].ResultSHA256)
	r.Steps[10].ResultSHA256 = r.Steps[10].InputSHA256
	return r
}

func membershipObservationFixture(t *testing.T, pair bool) bootstrapMembershipObservationRequest {
	t.Helper()
	join, _, _ := nativeJoinRequestFixture(t)
	r := bootstrapMembershipObservationRequest{Source: join.Source, Network: join.Network, KubeSystemUID: join.Expected.KubeSystemUID}
	r.Nodes = append(r.Nodes, bootstrapMembershipNodeExpectation{Node: clusterbootstrap.K3sJoinedNode{NodeUID: r.Source.SourceUID, Hostname: r.Network.Hostname, MachineID: r.Network.MachineID, BootID: r.Network.BootID, Address: strings.Split(r.Network.ManagementLink.Address, "/")[0], K3sVersion: r.Network.K3sVersion, Ready: true}, MemberName: r.Network.Hostname + "-12345678"})
	for i, address := range join.Expected.PeerAddresses {
		if address == r.Nodes[0].Node.Address {
			continue
		}
		e := join.Expected
		e.PeerAddresses = slices.Clone(e.PeerAddresses)
		uid := "99999999-9999-4999-8999-999999999999"
		if address != e.Target.Address {
			e.Target.Hostname, e.Target.Address, e.Target.MachineID = "engine-03", address, strings.Repeat("3", 32)
			e.Target.TargetID, e.TargetBootID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			uid = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
		}
		receipt := membershipReceiptFixture(e, r.Source, uid, math.MaxUint64-uint64(i))
		if e.Validate() != nil || receipt.Validate(e, r.Source) != nil {
			t.Fatal("invalid immutable receipt fixture")
		}
		r.Nodes = append(r.Nodes, bootstrapMembershipNodeExpectation{Node: receipt.Node, MemberName: receipt.MemberName})
		if pair || e.Target.Address == join.Expected.Target.Address {
			r.Prepared = append(r.Prepared, bootstrapMembershipPreparedTarget{Expected: e, Receipt: receipt})
		}
	}
	if !r.valid() {
		t.Fatal("invalid observation fixture")
	}
	return r
}

func membershipNativeDocuments(r bootstrapMembershipObservationRequest) (map[string]any, map[string]any) {
	members := []any{}
	for i, expected := range r.Nodes {
		node := expected.Node
		id := uint64(math.MaxUint64 - uint64(i))
		for _, target := range r.Prepared {
			if target.Receipt.Node.NodeUID == node.NodeUID {
				id = target.Receipt.MemberID
			}
		}
		members = append(members, map[string]any{"ID": id, "name": expected.MemberName, "peerURLs": []string{"https://" + node.Address + ":2380"}, "clientURLs": []string{"https://" + node.Address + ":2379"}, "isLearner": false})
	}
	local := r.Nodes[0]
	for _, expected := range r.Nodes {
		if expected.Node.NodeUID == clusterbootstrap.MembershipObservationRequest(r).ObserverUID() {
			local = expected
		}
	}
	node := local.Node
	labels := map[string]any{"borealis.io/engine-node": "true", "borealis.io/application-state": "active"}
	if r.Observer != nil {
		for _, target := range r.Prepared {
			if target.Receipt.Node.NodeUID == node.NodeUID {
				labels["borealis.io/application-state"] = "drained"
				for _, role := range []string{"control-plane", "edge", "scheduler", "postgres-primary"} {
					labels["borealis.io/"+role+"-eligible"] = "false"
				}
			}
		}
	}
	return map[string]any{"apiVersion": "v1", "kind": "Node", "metadata": map[string]any{"name": node.Hostname, "uid": node.NodeUID, "resourceVersion": "12", "labels": labels, "annotations": map[string]any{"etcd.k3s.cattle.io/node-name": local.MemberName}}, "status": map[string]any{
		"nodeInfo": map[string]any{"machineID": node.MachineID, "bootID": node.BootID, "kubeletVersion": node.K3sVersion, "architecture": "amd64", "operatingSystem": "linux"}, "addresses": []any{map[string]any{"type": "InternalIP", "address": node.Address}}, "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}}, map[string]any{"members": members}
}

func membershipObserverRequestFixture(t *testing.T, pair bool, index int) bootstrapMembershipObservationRequest {
	t.Helper()
	r := membershipObservationFixture(t, pair)
	node := r.Nodes[index].Node
	r.Observer = &clusterbootstrap.MembershipObserver{NodeUID: node.NodeUID}
	r.OriginalSourceNodeUIDs = []string{r.Source.SourceUID}
	if !pair {
		r.OriginalSourceNodeUIDs = append(r.OriginalSourceNodeUIDs, r.Nodes[2].Node.NodeUID)
	}
	r.Network.NodeUID, r.Network.Hostname, r.Network.MachineID, r.Network.BootID = node.NodeUID, node.Hostname, node.MachineID, node.BootID
	r.Network.ManagementLink.Address = node.Address + "/24"
	if !r.valid() {
		t.Fatal("invalid explicit observer request")
	}
	return r
}

func TestBootstrapMembershipObserverNativeTLS(t *testing.T) {
	for _, mode := range []string{"source", "first target", "second target", "other source", "target active", "target eligible", "source drained", "wrong local UID", "membership drift", "second Node drift", "observer frozen", "original roster frozen", "network drift", "unknown condition", "cancel before", "cancel during"} {
		t.Run(mode, func(t *testing.T) {
			index := 1
			if mode == "source" || mode == "source drained" {
				index = 0
			}
			if mode == "second target" || mode == "other source" {
				index = 2
			}
			r := membershipObserverRequestFixture(t, mode != "other source", index)
			original, uid, wantNode := r.Source, r.Observer.NodeUID, r.Nodes[index].Node
			wantDigest := clusterbootstrap.MembershipObservationRequest(r).SHA256()
			network := r.Network
			root, _, _, serverCert, roots := bootstrapJoinedTLSFixture(t, r.Prepared[0].Expected, r.Network.Hostname)
			if root.MkdirAll("var/lib/rancher/k3s/server/db/etcd", 0700) != nil || root.WriteFile("var/lib/rancher/k3s/server/db/etcd/name", []byte(r.Network.Hostname+"-12345678"), 0600) != nil {
				t.Fatal("observer etcd-name fixture")
			}
			node, members := membershipNativeDocuments(r)
			metadata := node["metadata"].(map[string]any)
			labels := metadata["labels"].(map[string]any)
			switch mode {
			case "target active":
				labels["borealis.io/application-state"] = "active"
			case "target eligible":
				labels["borealis.io/scheduler-eligible"] = "true"
			case "source drained":
				labels["borealis.io/application-state"] = "drained"
			case "wrong local UID":
				metadata["uid"] = r.Source.SourceUID
			case "unknown condition":
				node["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "MemoryPressure", "status": "invalid"}}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel before" {
				cancel()
			}
			counts := map[string]int{}
			var mu sync.Mutex
			ownPath := "/api/v1/nodes/" + network.Hostname
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				counts[req.URL.Path]++
				if req.Method != "GET" || req.Host != "127.0.0.1:6443" || req.Header.Get("Authorization") != "" || len(req.TLS.PeerCertificates) != 1 || req.TLS.PeerCertificates[0].Subject.CommonName != "system:node:"+network.Hostname {
					t.Error("observer escaped fixed local mTLS identity")
				}
				if mode == "cancel during" {
					cancel()
				}
				var body any
				switch req.URL.Path {
				case "/version":
					body = map[string]any{"gitVersion": network.K3sVersion}
				case "/db/info":
					if mode == "membership drift" && counts[req.URL.Path] == 2 {
						members["members"].([]any)[1].(map[string]any)["ID"] = uint64(42)
					}
					body = members
				case ownPath:
					if mode == "second Node drift" && counts[req.URL.Path] == 2 {
						metadata["uid"] = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
					}
					body = node
				default:
					t.Error("observer requested remote Node or new path")
					w.WriteHeader(http.StatusForbidden)
					return
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			server.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
			server.StartTLS()
			defer server.Close()
			factory := func(config *tls.Config) *http.Client {
				client := newBootstrapJoinedNodeClient(config)
				client.Transport.(*http.Transport).DialContext = func(ctx context.Context, kind, address string) (net.Conn, error) {
					if address != "127.0.0.1:6443" {
						t.Error("observer dialed foreign address")
					}
					return (&net.Dialer{}).DialContext(ctx, kind, server.Listener.Addr().String())
				}
				return client
			}
			reads := 0
			source := func(context.Context) (clusterbootstrap.SourceNetwork, error) {
				reads++
				if mode == "observer frozen" && reads == 1 {
					r.Observer.NodeUID = r.Source.SourceUID
				}
				if mode == "original roster frozen" && reads == 1 {
					r.OriginalSourceNodeUIDs[0] = uid
				}
				current := network
				if mode == "network drift" && reads == 2 {
					current.NodeUID = r.Source.SourceUID
				}
				return current, nil
			}
			got, err := observeBootstrapMembershipAt(ctx, root, r, func(ctx context.Context) error { return ctx.Err() }, source, factory, membershipListenerFixture)
			valid := mode == "source" || mode == "first target" || mode == "second target" || mode == "other source" || mode == "observer frozen" || mode == "original roster frozen"
			if (err == nil) != valid || !valid && !reflect.DeepEqual(got, bootstrapMembershipObservation{}) {
				t.Fatal("observer native evidence escaped changed phase or identity", err)
			}
			if valid {
				mu.Lock()
				complete := len(counts) == 3 && counts["/version"] == 2 && counts["/db/info"] == 2 && counts[ownPath] == 2
				mu.Unlock()
				if !complete || reads != 2 || got.Observer == nil || got.Observer.NodeUID != uid || got.Source != original || got.LocalNode != wantNode || got.RequestSHA256 != wantDigest {
					t.Fatal("observer lost original provenance or native read brackets")
				}
			}
		})
	}
}

// All responses pass through the actual fixed-loopback HTTPS/mTLS transport.
// No host runtime, service, network target or original-source key is used.
func TestBootstrapMembershipObservationNativeTLS(t *testing.T) {
	modes := []string{"pair", "replacement", "provided remote UID", "reordered", "request frozen", "forbidden paths unused", "learner source", "learner target", "only two", "four members", "duplicate ID", "duplicate name", "duplicate peer", "foreign peer", "foreign client", "string ID", "fraction ID", "overflow ID", "alias ID", "membership changed", "target ID changed", "target name changed", "source name changed", "stale original mapping", "source UID changed", "second Node changed", "source annotation changed", "removed member", "deleting Node", "Node namespace", "Node alias", "source machine alias", "source address alias", "not Ready", "Ready duplicate", "unknown condition", "wrong address", "source drained", "Node kind", "Node name", "version changed", "trailing JSON", "duplicate JSON", "oversize", "redirect", "non200", "own Node forbidden", "authority before", "authority during", "authority after", "cancel before", "cancel during", "source before", "source after", "final name changed", "final key changed", "final CA changed", "final parent changed", "nil client"}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			r := membershipObservationFixture(t, mode != "replacement" && mode != "provided remote UID" && mode != "stale original mapping")
			root, _, _, serverCert, roots := bootstrapJoinedTLSFixture(t, r.Prepared[0].Expected, r.Network.Hostname)
			if root.MkdirAll("var/lib/rancher/k3s/server/db/etcd", 0700) != nil || root.WriteFile("var/lib/rancher/k3s/server/db/etcd/name", []byte(r.Network.Hostname+"-12345678"), 0600) != nil {
				t.Fatal("etcd name fixture")
			}
			node, members := membershipNativeDocuments(r)
			memberItems := members["members"].([]any)
			member := func(i int) map[string]any { return memberItems[i].(map[string]any) }
			metadata := node["metadata"].(map[string]any)
			status := node["status"].(map[string]any)
			switch mode {
			case "learner source":
				member(0)["isLearner"] = true
			case "learner target":
				member(1)["isLearner"] = true
			case "only two":
				members["members"] = memberItems[:2]
			case "four members":
				members["members"] = append(memberItems, memberItems[0])
			case "duplicate ID":
				member(1)["ID"] = member(0)["ID"]
			case "duplicate name":
				member(1)["name"] = member(0)["name"]
			case "duplicate peer":
				member(1)["peerURLs"] = member(0)["peerURLs"]
			case "foreign peer":
				member(1)["peerURLs"] = []string{"https://192.168.3.253:2380"}
			case "foreign client":
				member(1)["clientURLs"] = []string{"https://192.168.3.253:2379"}
			case "string ID":
				member(1)["ID"] = "18446744073709551614"
			case "fraction ID":
				member(1)["ID"] = json.RawMessage("1.5")
			case "overflow ID":
				member(1)["ID"] = json.RawMessage("18446744073709551616")
			case "alias ID":
				member(1)["Id"] = member(1)["ID"]
				delete(member(1), "ID")
			case "target ID changed":
				member(1)["ID"] = uint64(42)
			case "target name changed":
				member(1)["name"] = r.Nodes[1].Node.Hostname + "-87654321"
			case "source name changed":
				member(0)["name"] = r.Nodes[0].Node.Hostname + "-87654321"
			case "stale original mapping":
				r.Nodes[2].MemberName = r.Nodes[2].Node.Hostname + "-87654321"
			case "source UID changed":
				metadata["uid"] = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
			case "source annotation changed":
				metadata["annotations"].(map[string]any)["etcd.k3s.cattle.io/node-name"] = r.Nodes[0].Node.Hostname + "-87654321"
			case "removed member":
				metadata["annotations"].(map[string]any)["etcd.k3s.cattle.io/removed-node-name"] = r.Nodes[0].Node.Hostname + "-12345678"
			case "deleting Node":
				metadata["deletionTimestamp"] = "2026-10-28T00:00:00Z"
			case "Node namespace":
				metadata["namespace"] = "default"
			case "Node alias":
				metadata["DeletionTimestamp"] = "2026-10-28T00:00:00Z"
			case "source machine alias":
				info := status["nodeInfo"].(map[string]any)
				info["MachineID"] = info["machineID"]
				delete(info, "machineID")
			case "source address alias":
				status["Addresses"] = status["addresses"]
				delete(status, "addresses")
			case "not Ready":
				status["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
			case "Ready duplicate":
				status["conditions"] = []any{map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "Ready", "status": "True"}}
			case "unknown condition":
				status["conditions"] = []any{map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "MemoryPressure", "status": "invalid"}}
			case "wrong address":
				status["addresses"] = []any{map[string]any{"type": "InternalIP", "address": "192.168.3.253"}}
			case "source drained":
				metadata["labels"].(map[string]any)["borealis.io/application-state"] = "drained"
			case "Node kind":
				node["kind"] = "Namespace"
			case "Node name":
				metadata["name"] = "foreign"
			}
			frozenWire, _ := json.Marshal(struct {
				Domain  string
				Request bootstrapMembershipObservationRequest
			}{"original-source-membership-observation-v1", r})
			frozenDigest := sha256.Sum256(frozenWire)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel before" {
				cancel()
			}
			var lost atomic.Bool
			if mode == "authority before" {
				lost.Store(true)
			}
			var requests, sourceReads atomic.Int32
			counts := map[string]int{}
			var responseMu sync.Mutex
			ownPath := "/api/v1/nodes/" + r.Network.Hostname
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				responseMu.Lock()
				defer responseMu.Unlock()
				requests.Add(1)
				counts[req.URL.Path]++
				if req.Method != "GET" || req.Host != "127.0.0.1:6443" || req.Header.Get("Authorization") != "" || len(req.TLS.PeerCertificates) != 1 || req.TLS.PeerCertificates[0].Subject.CommonName != "system:node:"+r.Network.Hostname {
					t.Error("unbound TLS request")
				}
				// Default node authority permits only own Node here. Forbidden namespace,
				// whole-list and remote-Node paths must never be requested, even on success.
				if req.URL.Path != "/version" && req.URL.Path != "/db/info" && req.URL.Path != ownPath {
					t.Error("forbidden native path")
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if mode == "redirect" {
					http.Redirect(w, req, "https://foreign.invalid/", http.StatusFound)
					return
				}
				if mode == "non200" {
					w.WriteHeader(http.StatusPartialContent)
					return
				}
				if mode == "own Node forbidden" && req.URL.Path == ownPath {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				if mode == "oversize" {
					_, _ = w.Write([]byte(strings.Repeat("x", (128<<10)+1)))
					return
				}
				if mode == "authority during" || mode == "authority after" && req.URL.Path == "/version" && counts[req.URL.Path] == 2 {
					lost.Store(true)
				}
				if mode == "cancel during" {
					cancel()
				}
				var body any
				switch req.URL.Path {
				case ownPath:
					if mode == "second Node changed" && counts[req.URL.Path] == 2 {
						metadata["uid"] = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
					}
					body = node
				case "/db/info":
					if mode == "membership changed" && counts[req.URL.Path] == 2 {
						member(1)["ID"] = uint64(42)
					}
					if mode == "reordered" && counts[req.URL.Path] == 2 {
						slices.Reverse(memberItems)
					}
					body = members
				case "/version":
					version := r.Network.K3sVersion
					if mode == "version changed" && counts[req.URL.Path] == 2 {
						version = "v1.36.4+k3s1"
					}
					body = map[string]any{"gitVersion": version}
				}
				raw, _ := json.Marshal(body)
				if mode == "trailing JSON" {
					raw = append(raw, []byte("{}")...)
				}
				if mode == "duplicate JSON" && req.URL.Path == ownPath {
					raw = []byte(strings.Replace(string(raw), `"kind":"Node"`, `"kind":"Node","kind":"Node"`, 1))
				}
				_, _ = w.Write(raw)
			}))
			server.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
			server.StartTLS()
			defer server.Close()
			var retained *tls.Config
			factory := func(config *tls.Config) *http.Client {
				retained = config
				if mode == "nil client" {
					return nil
				}
				client := newBootstrapJoinedNodeClient(config)
				transport := client.Transport.(*http.Transport)
				if transport.Proxy != nil || !transport.DisableKeepAlives || !transport.DisableCompression {
					t.Fatal("ambient HTTP behavior")
				}
				transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					if address != "127.0.0.1:6443" {
						t.Error("foreign dial")
					}
					return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
				}
				return client
			}
			check := func(ctx context.Context) error {
				if ctx.Err() != nil || lost.Load() {
					return errBootstrapSource
				}
				return nil
			}
			source := func(context.Context) (clusterbootstrap.SourceNetwork, error) {
				n := sourceReads.Add(1)
				network := r.Network
				if mode == "source before" || mode == "source after" && n == 2 {
					network.Kubelet.StartTicks++
				}
				if n == 2 {
					var err error
					switch mode {
					case "final name changed":
						err = root.WriteFile("var/lib/rancher/k3s/server/db/etcd/name", []byte(r.Network.Hostname+"-87654321"), 0600)
					case "final key changed":
						err = root.WriteFile("var/lib/rancher/k3s/agent/client-kubelet.key", []byte("changed"), 0600)
					case "final CA changed":
						err = root.WriteFile("var/lib/rancher/k3s/agent/server-ca.crt", []byte("changed"), 0600)
					case "final parent changed":
						err = root.Chmod("var/lib/rancher/k3s/agent", 0777)
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				if mode == "request frozen" && n == 1 {
					r.Nodes[1].Node.NodeUID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
					r.Nodes[2].MemberName = "engine-03-87654321"
					r.Prepared[0].Expected.PeerAddresses[0] = "192.168.3.253"
				}
				return network, nil
			}
			got, err := observeBootstrapMembershipAt(ctx, root, r, check, source, factory, membershipListenerFixture)
			valid := mode == "pair" || mode == "replacement" || mode == "provided remote UID" || mode == "reordered" || mode == "request frozen" || mode == "forbidden paths unused"
			if (err == nil) != valid {
				t.Fatal("wrong membership result", err)
			}
			if !valid && !reflect.DeepEqual(got, bootstrapMembershipObservation{}) {
				t.Fatal("partial evidence escaped")
			}
			if retained != nil && (retained.Certificates != nil || retained.RootCAs != nil) {
				t.Fatal("private TLS scope retained")
			}
			if valid {
				responseMu.Lock()
				completeReads := len(counts) == 3 && counts["/version"] == 2 && counts["/db/info"] == 2 && counts[ownPath] == 2
				responseMu.Unlock()
				if !completeReads {
					t.Fatal("missing exact path brackets")
				}
				if requests.Load() != 6 || sourceReads.Load() != 2 || got.Source != r.Source || got.LocalNode != r.Nodes[0].Node || len(got.Membership.Members) != 3 || got.Membership.LocalID != math.MaxUint64 || !got.Membership.Voting || got.RequestSHA256 != hex.EncodeToString(frozenDigest[:]) {
					t.Fatal("incomplete bracketing, rounded uint64 or unbound expectations")
				}
				for _, target := range r.Prepared {
					found := false
					for _, member := range got.Membership.Members {
						found = found || member.ID == target.Receipt.MemberID && member.Name == target.Receipt.MemberName && member.Address == target.Receipt.Node.Address && !member.Learner
					}
					if !found {
						t.Fatal("missing target receipt mapping")
					}
				}
			}
			if mode == "provided remote UID" {
				// Other original member has no target receipt and is never read by
				// this native adapter. Controller must freshly verify its identity.
				r.Nodes[2].Node.NodeUID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
				changed, err := observeBootstrapMembershipAt(ctx, root, r, check, source, factory, membershipListenerFixture)
				if err != nil || changed.LocalNode != got.LocalNode || !reflect.DeepEqual(changed.Membership, got.Membership) || changed.RequestSHA256 == got.RequestSHA256 {
					t.Fatal("provided remote UID was misrepresented as native proof or not bound")
				}
			}
			if mode == "authority before" || mode == "cancel before" || mode == "source before" {
				if requests.Load() != 0 {
					t.Fatal("invalid authority reached network")
				}
			}
		})
	}
}

func TestBootstrapMembershipObservationPrivateCustody(t *testing.T) {
	for _, mode := range []string{"wrong client", "wrong mode", "linked key", "unsafe parent", "missing name", "linked name", "symlink name", "wrong name", "name newline", "name changed", "key changed", "CA changed", "parent changed", "final custody"} {
		t.Run(mode, func(t *testing.T) {
			r := membershipObservationFixture(t, true)
			clientName := r.Network.Hostname
			if mode == "wrong client" {
				clientName = "foreign"
			}
			root, _, _, _, _ := bootstrapJoinedTLSFixture(t, r.Prepared[0].Expected, clientName)
			namePath, keyPath := "var/lib/rancher/k3s/server/db/etcd/name", "var/lib/rancher/k3s/agent/client-kubelet.key"
			if root.MkdirAll("var/lib/rancher/k3s/server/db/etcd", 0700) != nil || root.WriteFile(namePath, []byte(r.Network.Hostname+"-12345678"), 0600) != nil {
				t.Fatal("name fixture")
			}
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "wrong mode":
				must(root.Chmod(keyPath, 0644))
			case "linked key":
				must(root.Link(keyPath, "alias"))
			case "unsafe parent":
				must(root.Chmod("var/lib/rancher/k3s/agent", 0777))
			case "missing name":
				must(root.Remove(namePath))
			case "linked name":
				must(root.Link(namePath, "alias"))
			case "symlink name":
				must(root.Rename(namePath, "real-name"))
				must(root.Symlink("../../../../../../../real-name", namePath))
			case "wrong name":
				must(root.WriteFile(namePath, []byte("foreign-12345678"), 0600))
			case "name newline":
				must(root.WriteFile(namePath, []byte(r.Network.Hostname+"-12345678\n"), 0600))
			}
			calls := 0
			factory := func(*tls.Config) *http.Client {
				calls++
				switch mode {
				case "name changed":
					must(root.WriteFile(namePath, []byte(r.Network.Hostname+"-87654321"), 0600))
				case "key changed":
					must(root.WriteFile(keyPath, []byte("changed"), 0600))
				case "CA changed":
					must(root.WriteFile("var/lib/rancher/k3s/agent/server-ca.crt", []byte("changed"), 0600))
				case "parent changed":
					must(root.Chmod("var/lib/rancher/k3s/agent", 0777))
				}
				return &http.Client{Transport: membershipRejectTransport{t}}
			}
			check := func(context.Context) error { return nil }
			if mode == "final custody" {
				err := withBootstrapK3sObservationFiles(context.Background(), root, r.Network.Hostname, true, check, func(_ []bootstrapHostFile, _ func(context.Context) error, _ string) error {
					must(root.WriteFile(namePath, []byte(r.Network.Hostname+"-87654321"), 0600))
					return nil
				})
				if err == nil {
					t.Fatal("shared helper skipped final custody")
				}
				return
			}
			got, err := observeBootstrapMembershipAt(context.Background(), root, r, check, func(context.Context) (clusterbootstrap.SourceNetwork, error) { return r.Network, nil }, factory, membershipListenerFixture)
			if err == nil || !reflect.DeepEqual(got, bootstrapMembershipObservation{}) {
				t.Fatal("private file failure accepted")
			}
			if !strings.HasSuffix(mode, "changed") && mode != "parent changed" && calls != 0 {
				t.Fatal("invalid private identity reached client")
			}
		})
	}
}

type membershipRejectTransport struct{ t *testing.T }

func (m membershipRejectTransport) RoundTrip(*http.Request) (*http.Response, error) {
	m.t.Error("changed custody reached HTTP")
	return nil, errBootstrapSource
}

func TestBootstrapMembershipObservationRejectsRequest(t *testing.T) {
	for _, mode := range []string{"missing receipt", "bad receipt", "wrong cluster", "wrong source UID", "wrong namespace", "wrong attempt", "wrong cohort", "wrong source baseline", "duplicate target ID", "target replaced", "target member mapping", "invalid member mapping", "missing Node", "duplicate address", "bad version", "zero machine", "not Ready", "observer missing partition", "observer omitted target", "observer unknown UID", "observer wrong machine"} {
		t.Run(mode, func(t *testing.T) {
			r := membershipObservationFixture(t, true)
			if strings.HasPrefix(mode, "observer ") {
				r = membershipObserverRequestFixture(t, true, 1)
			}
			switch mode {
			case "observer missing partition":
				r.OriginalSourceNodeUIDs = nil
			case "observer omitted target":
				r.Prepared = r.Prepared[:1]
			case "observer unknown UID":
				r.Observer.NodeUID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
			case "observer wrong machine":
				r.Network.MachineID = strings.Repeat("f", 32)
			case "missing receipt":
				r.Prepared = nil
			case "bad receipt":
				r.Prepared[0].Receipt.Steps[10].ResultSHA256 = strings.Repeat("f", 64)
			case "wrong cluster":
				r.Source.ClusterID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
			case "wrong source UID":
				r.Source.SourceUID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
			case "wrong namespace":
				r.KubeSystemUID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
			case "wrong attempt", "wrong cohort", "wrong source baseline", "duplicate target ID":
				p := &r.Prepared[1]
				if mode == "duplicate target ID" {
					p.Expected.Target.TargetID = r.Prepared[0].Expected.Target.TargetID
				}
				if mode == "wrong attempt" {
					p.Expected.Target.OperationAttempt++
				}
				if mode == "wrong cohort" {
					p.Expected.CohortSHA256 = strings.Repeat("f", 64)
				}
				if mode == "wrong source baseline" {
					p.Expected.Source.SourceSHA = strings.Repeat("f", 40)
				}
				p.Receipt = membershipReceiptFixture(p.Expected, r.Source, p.Receipt.Node.NodeUID, p.Receipt.MemberID)
			case "target member mapping":
				r.Nodes[1].MemberName = r.Nodes[1].Node.Hostname + "-87654321"
			case "invalid member mapping":
				r.Nodes[0].MemberName = "foreign-12345678"
			case "target replaced":
				r.Nodes[1].Node.NodeUID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
			case "missing Node":
				r.Nodes = r.Nodes[:2]
			case "duplicate address":
				r.Nodes[1].Node.Address = r.Nodes[0].Node.Address
			case "bad version":
				r.Network.K3sVersion = "v1.36.4+k3s1"
			case "zero machine":
				r.Nodes[1].Node.MachineID = strings.Repeat("0", 32)
			case "not Ready":
				r.Nodes[1].Node.Ready = false
			}
			root, err := os.OpenRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			called := false
			got, err := observeBootstrapMembershipAt(context.Background(), root, r, func(context.Context) error { called = true; return nil }, func(context.Context) (clusterbootstrap.SourceNetwork, error) { called = true; return r.Network, nil }, func(*tls.Config) *http.Client { called = true; return nil }, membershipListenerFixture)
			if err == nil || called || !reflect.DeepEqual(got, bootstrapMembershipObservation{}) {
				t.Fatal("invalid request reached authority or native reader")
			}
		})
	}
}

// Other native fixtures inject an independent lifetime. Listener-specific tests
// exercise the actual procfs producer and every composition boundary.
func membershipListenerFixture(_ context.Context, _ clusterbootstrap.SourceKubelet, proof uint8) (*bootstrapMembershipListenerScope, error) {
	return &bootstrapMembershipListenerScope{check: func(ctx context.Context) error { return ctx.Err() }, close: func() error { return nil }, proofVersion: proof}, nil
}
