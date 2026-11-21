package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func nativeJoinRequestFixture(t *testing.T) (clusterbootstrap.K3sJoinSourceRequest, *os.Root, []byte) {
	t.Helper()
	root, token, source := sourceJoinFilesFixture(t)
	e := bootstrapHostFilesFixture()
	n := clusterbootstrap.SourceNetwork{NodeUID: source.SourceUID, Hostname: "engine-01", MachineID: strings.Repeat("b", 32), BootID: "22222222-2222-4222-8222-222222222222", K3sVersion: e.K3sVersion, PodCIDR: e.PodCIDR, ServiceCIDR: e.ServiceCIDR,
		ManagementLink: clusterbootstrap.ManagementLink{Interface: "ens18", Index: 2, MAC: "02:00:00:00:00:01", NetworkNamespace: 1234},
		Kubelet: clusterbootstrap.SourceKubelet{ExecutableSHA256: clusterbootstrap.K3sPins().ServerExecutable.SHA256, Root: "/var/lib/kubelet", PID: 42, StartTicks: 100, Invocation: strings.Repeat("a", 32), ListenerInode: 5678, NetworkNamespace: 1234, MountNamespace: 1235, ExecutableDevice: 8, ExecutableInode: 9012, HostRootDevice: 8, HostRootInode: 2,
			CSISocket: clusterbootstrap.SourceCSISocket{Listener: clusterbootstrap.SourceCSIListener{PodUID: "11111111-1111-4111-8111-111111111111", ContainerID: strings.Repeat("d", 64), IdentitySHA256: strings.Repeat("e", 64)}, DirectoryDevice: 8, DirectoryInode: 12, DirectoryMount: 1, SocketDevice: 8, SocketInode: 13, SocketMount: 1, PathSHA256: strings.Repeat("c", 64)}}}
	for _, peer := range e.PeerAddresses {
		if peer != e.Target.Address {
			n.ManagementLink.Address = peer + "/24"
			break
		}
	}
	r := clusterbootstrap.K3sJoinSourceRequest{Version: 1, ID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ExpiresAt: time.Now().Unix() + 60, Expected: e, Source: source, Network: n}
	if err := r.Validate(time.Now()); err != nil {
		t.Fatal(err)
	}
	return r, root, token
}

func fixtureJoinRead(root *os.Root) sourceJoinRead {
	return func(ctx context.Context, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding, prove func(context.Context) error, consume func(context.Context, *clusterbootstrap.K3sJoinMaterial, func(context.Context) error) error) error {
		return withSourceK3sJoinMaterialAt(ctx, root, expected, source, prove, consume)
	}
}

func TestSourceJoinHandlerCustodyAndBufferedFailure(t *testing.T) {
	for _, mode := range []string{"complete", "before drift", "after drift", "private read failure", "identity drift", "cancel", "invalid request", "query", "media"} {
		t.Run(mode, func(t *testing.T) {
			request, root, token := nativeJoinRequestFixture(t)
			raw, _ := json.Marshal(request)
			if mode == "invalid request" {
				raw = append(raw, ' ')
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := httptest.NewRequest("POST", clusterbootstrap.K3sJoinSourceSocketPath, bytes.NewReader(raw)).WithContext(ctx)
			r.Header.Set("Content-Type", "application/json")
			if mode == "query" {
				r.URL.RawQuery = "target=other"
			}
			if mode == "media" {
				r.Header.Set("Content-Type", "text/plain")
			}
			observations, reads := 0, 0
			observe := func(context.Context) (clusterbootstrap.SourceNetwork, error) {
				observations++
				network := request.Network
				if mode == "before drift" || (mode == "after drift" && observations == 2) {
					network.BootID = request.ID
				}
				return network, nil
			}
			identity := func(clusterbootstrap.K3sJoinSourceRequest) error {
				if mode == "identity drift" && reads > 0 {
					return errors.New("private failure must not escape")
				}
				return nil
			}
			read := func(ctx context.Context, e clusterbootstrap.PreparationExpected, s engineidentity.Binding, prove func(context.Context) error, consume func(context.Context, *clusterbootstrap.K3sJoinMaterial, func(context.Context) error) error) error {
				reads++
				if mode == "cancel" {
					cancel()
				}
				err := fixtureJoinRead(root)(ctx, e, s, prove, consume)
				if mode == "private read failure" {
					return errors.New(string(token))
				}
				return err
			}
			w := httptest.NewRecorder()
			serveSourceJoinMaterial(w, r, observe, identity, read)
			if mode == "complete" {
				if w.Code != 200 || observations != 2 || reads != 1 || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("source proof missing", w.Code, observations, reads)
				}
				material, err := clusterbootstrap.ReceiveK3sJoinMaterial(ctx, request.Expected, request.Source, w.Body, func(context.Context) error { return nil })
				if err != nil || w.Body.Len() != 0 {
					t.Fatal("private frame invalid", err)
				}
				material.Close()
			} else if w.Code != http.StatusConflict || w.Body.String() != "source join material unavailable\n" {
				t.Fatal("failed source read exposed partial/private result", w.Code)
			}
		})
	}
}

func TestSourceJoinClientPrivateFrameAndPublicReceipt(t *testing.T) {
	request, root, token := nativeJoinRequestFixture(t)
	job, pod := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	secret := bytes.Repeat([]byte{'s'}, 32)
	for _, mode := range []string{"complete", "redirect", "private error", "trailing", "oversize", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			client := &http.Client{Transport: sourceClientRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != "http://node-manager"+clusterbootstrap.K3sJoinSourceSocketPath || r.Header.Get(managerTokenHeader) != strings.Repeat("t", 32) {
					t.Fatal("fixed endpoint lost")
				}
				w := httptest.NewRecorder()
				if mode == "redirect" {
					w.Header().Set("Location", "http://forbidden.example")
					w.WriteHeader(302)
					return w.Result(), nil
				}
				if mode == "private error" {
					w.WriteHeader(500)
					w.Write(token)
					return w.Result(), nil
				}
				serveSourceJoinMaterial(w, r, func(context.Context) (clusterbootstrap.SourceNetwork, error) { return request.Network, nil }, func(clusterbootstrap.K3sJoinSourceRequest) error { return nil }, fixtureJoinRead(root))
				if mode == "trailing" {
					w.WriteString("junk")
				}
				if mode == "oversize" {
					w.Write(bytes.Repeat([]byte{'x'}, clusterbootstrap.MaxK3sJoinSourceDeliveryBytes))
				}
				if mode == "cancel" {
					cancel()
				}
				return w.Result(), nil
			})}
			wire, receipt, err := readSourceJoinDelivery(ctx, client, strings.Repeat("t", 32), secret, request, job, pod)
			if mode != "complete" {
				if err != clusterbootstrap.ErrK3sJoinMaterial || wire != nil || calls != 1 {
					t.Fatal("invalid private response accepted", err, calls)
				}
				return
			}
			frame, actual, err := clusterbootstrap.OpenK3sJoinSourceDelivery(secret, request, wire, time.Now())
			defer clear(frame)
			if err != nil || actual != receipt || !bytes.Contains(frame, bytes.TrimSpace(token)) || bytes.Contains(wire, bytes.TrimSpace(token)) {
				t.Fatal("private callback invalid", err)
			}
			public, _ := json.Marshal(receipt)
			if bytes.Contains(public, bytes.TrimSpace(token)) {
				t.Fatal("termination receipt leaked token")
			}
		})
	}
}

func TestSourceJoinCallbackFixedPeersAndAcknowledgement(t *testing.T) {
	r, _, _ := nativeJoinRequestFixture(t)
	for _, mode := range []string{"complete", "wrong controller first", "redirect", "private error", "missing", "public peer", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			client := &http.Client{Transport: sourceClientRoundTrip(func(req *http.Request) (*http.Response, error) {
				calls++
				body, _ := io.ReadAll(req.Body)
				if req.Method != "POST" || req.URL.Path != clusterbootstrap.K3sJoinSourcePath || req.Header.Get(clusterbootstrap.K3sJoinSourceRequestHeader) != r.ID || string(body) != "ciphertext" {
					t.Fatal("callback contract changed")
				}
				w := httptest.NewRecorder()
				status := 204
				if mode == "missing" || (mode == "wrong controller first" && calls == 1) {
					status = 404
				}
				if mode == "redirect" {
					status = 302
					w.Header().Set("Location", "http://forbidden.example")
				}
				if mode == "private error" {
					status = 409
				}
				if mode == "cancel" {
					cancel()
				}
				w.WriteHeader(status)
				return w.Result(), nil
			})}
			peers := []string{"http://10.42.0.2:8090", "http://10.42.0.3:8090"}
			if mode == "public peer" {
				peers[0] = "http://8.8.8.8:8090"
			}
			err := deliverSourceJoin(ctx, client, peers, r, []byte("ciphertext"))
			if (err == nil) != (mode == "complete" || mode == "wrong controller first") {
				t.Fatal("wrong delivery result", err)
			}
			if (mode == "public peer" && calls != 0) || (mode == "redirect" && calls != 1) {
				t.Fatal("unsafe endpoint contacted")
			}
		})
	}
}
