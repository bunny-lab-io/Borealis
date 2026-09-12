package clusterremote

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestTargetManagementPeerFreshPinnedHostScope(t *testing.T) {
	for _, mode := range []string{"success", "wrong port", "wrong pin", "host changed", "boot changed", "machine changed", "index changed", "existing installation", "unsupported", "authority lost", "cancel", "callback deadline", "private error"} {
		t.Run(mode, func(t *testing.T) {
			wire, _ := json.Marshal(targetManagementFixture(t))
			targets := routedNetworkFixture(t).Targets
			linkCommand, _ := managementLinkCommand(targets)
			var hosts, privileges, checks atomic.Int32
			server := newFakeSSH(t, "peer", func(command string, channel ssh.Channel) uint32 {
				switch command {
				case inspectionCommand:
					n := hosts.Add(1)
					out := inspectedHostFixture
					if mode == "host changed" && n > 1 {
						out = strings.Replace(out, "new-engine", "different-engine", 1)
					}
					if mode == "unsupported" {
						out = strings.Replace(out, "ubuntu", "other", 1)
					}
					channel.Write([]byte(out))
				case privilegedInspectionCommand, linkCommand:
					stdin, err := io.ReadAll(channel)
					if err != nil || !bytes.Equal(stdin, []byte("private-sudo\n")) {
						return 1
					}
					if mode == "private error" {
						channel.Stderr().Write([]byte("private-sudo"))
						return 1
					}
					if command == linkCommand {
						channel.Write(wire)
						return 0
					}
					n := privileges.Add(1)
					out := strings.Replace(privilegedHostFixture, `"ifname":`, `"ifindex":2,"ifname":`, 1)
					if n > 1 {
						switch mode {
						case "boot changed":
							out = strings.Replace(out, "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", 1)
						case "machine changed":
							out = strings.Replace(out, strings.Repeat("a", 32), strings.Repeat("b", 32), 1)
						case "index changed":
							out = strings.Replace(out, `"ifindex":2`, `"ifindex":3`, 1)
						case "existing installation":
							out = strings.Replace(out, "k3s_data=absent", "k3s_data=directory", 1)
						}
					}
					channel.Write([]byte(out))
				default:
					return 1
				}
				return 0
			})
			server.target.Address = targets.Management
			credential, err := PasswordCredential("operator", server.password)
			if err != nil {
				t.Fatal(err)
			}
			defer credential.Destroy()
			client, err := server.transport.Connect(context.Background(), server.target, server.key, credential)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			cleaned := false
			check := func(ctx context.Context) error {
				n := checks.Add(1)
				if mode == "authority lost" && n > 3 {
					return errors.New("private-authority")
				}
				if mode == "cancel" && n > 3 {
					cancel()
				}
				if mode == "callback deadline" {
					<-ctx.Done()
					cleaned = true
				}
				return nil
			}
			started := time.Now()
			expected, approved := server.target, server.key
			if mode == "wrong port" {
				expected.Port++
			}
			if mode == "wrong pin" {
				approved = newFakeSSH(t, "other").key
			}
			value, err := client.InspectManagementPeer(ctx, []byte("private-sudo"), targets, expected, approved, check)
			if mode != "success" {
				if err != ErrManagementLink || !reflect.DeepEqual(value, TargetManagementPeer{}) {
					t.Fatal("unsafe peer observation", err)
				}
				if mode == "callback deadline" && (!cleaned || server.execCalls.Load() != 0) {
					t.Fatal("callback not joined before return")
				}
				if (mode == "wrong port" || mode == "wrong pin") && (server.execCalls.Load() != 0 || checks.Load() != 0) {
					t.Fatal("wrong approval crossed acquisition boundary")
				}
				return
			}
			facts := routedPrivilegedFixture(t)
			if err != nil || hosts.Load() != 2 || privileges.Load() != 2 || checks.Load() != 6 {
				t.Fatal("incomplete observation", err)
			}
			link, err := value.ManagementLink(started, "new-engine", facts.MachineID, facts.BootID, server.target, server.key, targets.Peers)
			if err != nil || link != targetManagementFixture(t).Link {
				t.Fatal("valid peer rejected", err)
			}
			for _, mode := range []string{"old", "zero clock", "future clock", "host", "machine", "boot", "endpoint", "pin", "peers", "serialized"} {
				floor, host, machine, boot, target, key, peers, copy := started, "new-engine", facts.MachineID, facts.BootID, server.target, server.key, targets.Peers, value
				switch mode {
				case "old":
					floor = time.Now()
				case "zero clock":
					floor = time.Time{}
				case "future clock":
					copy.finished = time.Now().Add(time.Hour)
				case "host":
					host = "other"
				case "machine":
					machine = strings.Repeat("b", 32)
				case "boot":
					boot = "22222222-2222-4222-8222-222222222222"
				case "endpoint":
					target.Port++
				case "pin":
					key = newFakeSSH(t, "different").key
				case "peers":
					peers = peers[:1]
				case "serialized":
					raw, _ := json.Marshal(value)
					copy = TargetManagementPeer{}
					_ = json.Unmarshal(raw, &copy)
				}
				if got, err := copy.ManagementLink(floor, host, machine, boot, target, key, peers); err != ErrManagementLink || got != (clusterbootstrap.ManagementLink{}) {
					t.Fatal("stale/changed peer accepted", mode)
				}
			}
		})
	}
}
