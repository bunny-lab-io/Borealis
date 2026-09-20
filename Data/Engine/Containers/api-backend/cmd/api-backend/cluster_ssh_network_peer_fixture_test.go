package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type sshNetworkFixtureDialer struct{ address string }

func (d sshNetworkFixtureDialer) DialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, d.address)
}

func sshNetworkFixtureKeys(t *testing.T, cohort *clusterSSHInspectionCohort) map[string]ssh.Signer {
	t.Helper()
	keys := map[string]ssh.Signer{}
	for i := range cohort.Targets {
		_, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		signer, err := ssh.NewSignerFromKey(private)
		if err != nil {
			t.Fatal(err)
		}
		item := &cohort.Targets[i]
		item.Key = clusterremote.HostKey{Algorithm: signer.PublicKey().Type(), Fingerprint: ssh.FingerprintSHA256(signer.PublicKey()), PublicKey: signer.PublicKey().Marshal()}
		item.Binding.Fingerprint = item.Key.Fingerprint
		keys[item.Binding.TargetID] = signer
	}
	return keys
}

// A real pinned SSH transport produces the otherwise-unconstructible peer.
// Only synthetic protocol observations are served; no host commands execute.
func sshNetworkFixturePeer(t *testing.T, ctx context.Context, item clusterSSHInspectedTarget, signer ssh.Signer,
	link clusterbootstrap.ManagementLink, peers []string) (clusterremote.TargetManagementPeer, error) {
	var result clusterremote.TargetManagementPeer
	err := sshNetworkFixtureObserve(t, ctx, item, signer, link, peers, nil, func(client *clusterremote.Client) error {
		var err error
		result, err = readClusterSSHTargetNetwork(ctx, client, []byte("fixture-sudo"), item, peers, func(ctx context.Context) error { return ctx.Err() })
		return err
	})
	return result, err
}

func sshNetworkFixtureObserve(t *testing.T, ctx context.Context, item clusterSSHInspectedTarget, signer ssh.Signer,
	link clusterbootstrap.ManagementLink, peers []string, observationWire []byte, consume func(*clusterremote.Client) error) error {
	t.Helper()
	transport, done, closeServer := sshNetworkFixtureTransport(t, item, signer, link, peers, observationWire, "fixture", "fixture-private")
	defer closeServer()
	credential, err := clusterremote.PasswordCredential("fixture", []byte("fixture-private"))
	if err != nil {
		t.Fatal(err)
	}
	defer credential.Destroy()
	client, err := transport.Connect(ctx,
		clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, item.Key, credential)
	if err != nil {
		return err
	}
	defer func() { client.Close(); <-done }()
	return consume(client)
}

func sshNetworkFixtureTransport(t *testing.T, item clusterSSHInspectedTarget, signer ssh.Signer,
	link clusterbootstrap.ManagementLink, peers []string, observationWire []byte, username, expectedPassword string) (clusterremote.Transport, <-chan struct{}, func()) {
	t.Helper()
	prefix := netip.MustParsePrefix(link.Address)
	routing := clusterremote.RoutedNetworkOwnership{Version: 1, Targets: clusterremote.RouteTargets{Management: item.Binding.Address, Peers: peers}, Resolved: peers,
		Active: clusterremote.ActiveNetworkOwnership{Version: 1, Declarations: clusterremote.PersistentNetworkDeclarations{Version: 1, MachineID: item.Report.MachineID, BootID: item.Report.BootID,
			Interfaces: []clusterremote.PersistentNetworkInterface{{Name: link.Interface, Addresses: []string{link.Address}}}}, BusID: strings.Repeat("b", 32), Owner: ":1.42", NetworkNamespace: link.NetworkNamespace,
			Interfaces: []clusterremote.ActiveNetworkInterface{{Name: link.Interface, Index: link.Index, Addresses: []string{link.Address}}}},
		Networks: []clusterremote.DirectNetwork{{Interface: link.Interface, Index: link.Index, Address: link.Address, Excluded: []string{item.Binding.Address + "/32"}}}}
	wire, _ := json.Marshal(struct {
		Version int                                  `json:"version"`
		Routing clusterremote.RoutedNetworkOwnership `json:"routing"`
		Link    clusterbootstrap.ManagementLink      `json:"link"`
	}{1, routing, link})
	host := fmt.Sprintf("kernel=Linux\narchitecture=x86_64\nuid=1000\nhostname=%s\nos_id=ubuntu\nos_version=24.04\ncpu_count=16\nmemory_kib=33554432\ndisk_total_kib=524288000\ndisk_free_kib=314572800\ndisk_scope=opt\nborealis_path=absent\nk3s_unit=not-found\n", item.Report.Hostname)
	privileged := fmt.Sprintf("protocol=1\nuid=0\nmachine_id=%s\nboot_id=%s\nborealis_path=absent\nborealis_config=absent\nk3s_config=absent\nk3s_data=absent\nk3s_binary=absent\nk3s_load=not-found\nk3s_active=inactive\nk3s_agent_load=not-found\nk3s_agent_active=inactive\naddresses=[{\"ifindex\":%d,\"ifname\":%q,\"flags\":[\"UP\",\"LOWER_UP\"],\"operstate\":\"UP\",\"addr_info\":[{\"family\":\"inet\",\"local\":%q,\"prefixlen\":%d,\"scope\":\"global\",\"valid_life_time\":4294967295,\"preferred_life_time\":4294967295}]}]\nroutes=[{\"dst\":%q,\"dev\":%q,\"scope\":\"link\",\"protocol\":\"kernel\",\"flags\":[]}]\nkube_system_uid=not-present\nnodes=not-present\n",
		item.Report.MachineID, item.Report.BootID, link.Index, link.Interface, item.Binding.Address, prefix.Bits(), prefix.Masked().String(), link.Interface)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		config := &ssh.ServerConfig{PasswordCallback: func(metadata ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if metadata.User() != username || !bytes.Equal(password, []byte(expectedPassword)) {
				return nil, fmt.Errorf("fixture authentication")
			}
			return nil, nil
		}}
		config.AddHostKey(signer)
		server, channels, requests, err := ssh.NewServerConn(conn, config)
		if err != nil {
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		for incoming := range channels {
			if incoming.ChannelType() != "session" {
				_ = incoming.Reject(ssh.UnknownChannelType, "unsupported")
				continue
			}
			channel, requests, err := incoming.Accept()
			if err != nil {
				return
			}
			for request := range requests {
				var command struct{ Command string }
				if request.Type != "exec" || ssh.Unmarshal(request.Payload, &command) != nil {
					_ = request.Reply(false, nil)
					continue
				}
				_ = request.Reply(true, nil)
				status := uint32(0)
				if strings.HasPrefix(command.Command, "LC_ALL=C /bin/sh") {
					_, _ = io.WriteString(channel, host)
				} else {
					stdin, err := io.ReadAll(channel)
					if err != nil || !bytes.Equal(stdin, []byte("fixture-sudo\n")) {
						status = 1
					} else if observationWire != nil && (strings.Contains(command.Command, "observe_arp") || strings.Contains(command.Command, "observe_network_render")) {
						_, _ = channel.Write(observationWire)
					} else if strings.Contains(command.Command, "observe_network_render") {
						version := 1
						if strings.Contains(command.Command, "def boot_snapshot(root):") {
							version = 2
						}
						result, _ := json.Marshal(struct {
							Version    int             `json:"version"`
							Management json.RawMessage `json:"management"`
						}{version, wire})
						_, _ = channel.Write(result)
					} else if strings.Contains(command.Command, "observe_management_link") {
						_, _ = channel.Write(wire)
					} else if strings.Contains(command.Command, "machine_id") {
						_, _ = io.WriteString(channel, privileged)
					} else {
						status = 1
					}
				}
				_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
				_ = channel.Close()
				break
			}
		}
	}()
	closeServer := func() {
		listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("native fixture session not closed and joined")
		}
	}
	t.Cleanup(closeServer)
	return clusterremote.Transport{Dialer: sshNetworkFixtureDialer{listener.Addr().String()}}, done, closeServer
}
