package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const bootstrapHostFirewallChain = "BOREALIS-K3S-API"
const bootstrapHostFirewallLimit = 64 << 10

// This proves only the installed IPv4 filter contract. It neither executes the
// generated script nor treats files, enabled units or an exit code as rules.
// Complete host proof must additionally retain source/account/identity custody,
// prove packages/kernel/services and recheck the exact generated host files.
func proveBootstrapPrejoinFirewall(ctx context.Context, expected clusterbootstrap.PreparationExpected, check func(context.Context) error) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return errBootstrapSource
	}
	return proveBootstrapPrejoinFirewallWith(ctx, expected, check, readBootstrapHostFirewall)
}

func proveBootstrapPrejoinFirewallWith(ctx context.Context, expected clusterbootstrap.PreparationExpected, check func(context.Context) error,
	read func(context.Context, string) ([]byte, error)) error {
	if expected.Validate() != nil || check == nil || read == nil {
		return errBootstrapSource
	}
	boundary := func() error {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		return nil
	}
	// Two full snapshots detect changes across separate kernel reads. Keep owned
	// bytes: an injected reader must not rewrite the first observation by alias.
	previous := make(map[string][]byte, 2)
	for pass := 0; pass < 2; pass++ {
		for _, chain := range []string{"INPUT", bootstrapHostFirewallChain} {
			if boundary() != nil {
				return errBootstrapSource
			}
			raw, err := read(ctx, chain)
			if err != nil || boundary() != nil || validateBootstrapHostFirewall(raw, chain, expected) != nil {
				return errBootstrapSource
			}
			if pass == 0 {
				previous[chain] = bytes.Clone(raw)
			} else if !bytes.Equal(previous[chain], raw) {
				return errBootstrapSource
			}
		}
	}
	return boundary()
}

func readBootstrapHostFirewall(parent context.Context, chain string) ([]byte, error) {
	if chain != "INPUT" && chain != bootstrapHostFirewallChain {
		return nil, errBootstrapSource
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	// Fixed command, fixed chains, no shell. Prevent automatic module loading:
	// observation cannot turn a missing filter table into a prepared host.
	command := exec.CommandContext(ctx, "/usr/sbin/iptables", "--wait", "2", "--modprobe=/bin/false", "--table", "filter", "--list-rules", chain)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	command.WaitDelay = 250 * time.Millisecond
	output := &bootstrapHostFirewallOutput{}
	command.Stdout, command.Stderr = output, io.Discard
	if command.Run() != nil || ctx.Err() != nil {
		return nil, errBootstrapSource
	}
	return output.buffer.Bytes(), nil
}

type bootstrapHostFirewallOutput struct{ buffer bytes.Buffer }

func (out *bootstrapHostFirewallOutput) Write(raw []byte) (int, error) {
	if len(raw) > bootstrapHostFirewallLimit-out.buffer.Len() {
		return 0, errBootstrapSource
	}
	return out.buffer.Write(raw)
}

func validateBootstrapHostFirewall(raw []byte, chain string, expected clusterbootstrap.PreparationExpected) error {
	if expected.Validate() != nil || len(raw) == 0 || len(raw) > bootstrapHostFirewallLimit || raw[len(raw)-1] != '\n' {
		return errBootstrapSource
	}
	for _, value := range raw {
		if value != '\n' && (value < ' ' || value > '~') {
			return errBootstrapSource
		}
	}
	lines := strings.Split(string(raw[:len(raw)-1]), "\n")
	if chain == "INPUT" {
		if len(lines) < 8 || lines[0] != "-P INPUT ACCEPT" && lines[0] != "-P INPUT DROP" {
			return errBootstrapSource
		}
		// The installer inserts each jump at position one. Require that exact
		// prefix, not merely -C membership: an earlier ACCEPT would bypass it.
		for i, port := range []string{"51821", "51820", "8472", "5001", "10250", "2379:2381", "6443"} {
			protocol := "tcp"
			if i < 3 {
				protocol = "udp"
			}
			want := "-A INPUT -p " + protocol + " -m " + protocol + " --dport " + port + " -j " + bootstrapHostFirewallChain
			if lines[i+1] != want {
				return errBootstrapSource
			}
		}
		for _, line := range lines[8:] {
			if !strings.HasPrefix(line, "-A INPUT ") || strings.Contains(line, bootstrapHostFirewallChain) {
				return errBootstrapSource
			}
		}
		return nil
	}
	if chain != bootstrapHostFirewallChain {
		return errBootstrapSource
	}
	prefix := "-A " + bootstrapHostFirewallChain
	want := []string{
		"-N " + bootstrapHostFirewallChain,
		prefix + " -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT",
		prefix + " -i lo -j ACCEPT",
		prefix + " -s 127.0.0.0/8 -j ACCEPT",
	}
	for _, cidr := range []string{expected.PodCIDR, expected.ServiceCIDR} {
		for _, iface := range []string{"cni+", "flannel+"} {
			want = append(want, prefix+" -s "+cidr+" -i "+iface+" -j ACCEPT")
		}
	}
	for _, peer := range expected.PeerAddresses {
		want = append(want, prefix+" -s "+peer+"/32 -j ACCEPT")
	}
	want = append(want, prefix+" -j DROP")
	if len(lines) != len(want) {
		return errBootstrapSource
	}
	for i := range want {
		if lines[i] != want[i] {
			return errBootstrapSource
		}
	}
	return nil
}
