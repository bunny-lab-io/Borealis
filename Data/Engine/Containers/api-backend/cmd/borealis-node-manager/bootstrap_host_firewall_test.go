package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

const bootstrapFirewallInputFixture = `-P INPUT ACCEPT
-A INPUT -p udp -m udp --dport 51821 -j BOREALIS-K3S-API
-A INPUT -p udp -m udp --dport 51820 -j BOREALIS-K3S-API
-A INPUT -p udp -m udp --dport 8472 -j BOREALIS-K3S-API
-A INPUT -p tcp -m tcp --dport 5001 -j BOREALIS-K3S-API
-A INPUT -p tcp -m tcp --dport 10250 -j BOREALIS-K3S-API
-A INPUT -p tcp -m tcp --dport 2379:2381 -j BOREALIS-K3S-API
-A INPUT -p tcp -m tcp --dport 6443 -j BOREALIS-K3S-API
-A INPUT -p tcp -m tcp --dport 22 -j ACCEPT
`

const bootstrapFirewallPeerFixture = `-N BOREALIS-K3S-API
-A BOREALIS-K3S-API -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
-A BOREALIS-K3S-API -i lo -j ACCEPT
-A BOREALIS-K3S-API -s 127.0.0.0/8 -j ACCEPT
-A BOREALIS-K3S-API -s 10.42.0.0/16 -i cni+ -j ACCEPT
-A BOREALIS-K3S-API -s 10.42.0.0/16 -i flannel+ -j ACCEPT
-A BOREALIS-K3S-API -s 10.43.0.0/16 -i cni+ -j ACCEPT
-A BOREALIS-K3S-API -s 10.43.0.0/16 -i flannel+ -j ACCEPT
-A BOREALIS-K3S-API -s 192.168.3.250/32 -j ACCEPT
-A BOREALIS-K3S-API -s 192.168.3.251/32 -j ACCEPT
-A BOREALIS-K3S-API -s 192.168.3.252/32 -j ACCEPT
-A BOREALIS-K3S-API -j DROP
`

func TestBootstrapHostFirewallRules(t *testing.T) {
	for _, fixture := range []struct{ chain, raw string }{{"INPUT", bootstrapFirewallInputFixture}, {bootstrapHostFirewallChain, bootstrapFirewallPeerFixture}} {
		t.Run(fixture.chain, func(t *testing.T) {
			for _, mode := range []string{"valid", "empty", "truncated", "oversized", "control", "unknown chain", "invalid expected", "extra rule", "missing rule", "reordered", "broad source", "early accept"} {
				t.Run(mode, func(t *testing.T) {
					raw, chain, expected := fixture.raw, fixture.chain, bootstrapHostFilesFixture()
					switch mode {
					case "empty":
						raw = ""
					case "truncated":
						raw = strings.TrimSuffix(raw, "\n")
					case "oversized":
						raw = strings.Repeat(raw, bootstrapHostFirewallLimit/len(raw)+1)
					case "control":
						raw = strings.Replace(raw, " -", "\t-", 1)
					case "unknown chain":
						chain = "OUTPUT"
					case "invalid expected":
						expected.Target.Generation = 0
					case "extra rule":
						raw += "-A " + chain + " -j " + bootstrapHostFirewallChain + "\n"
					case "missing rule":
						lines := strings.Split(raw, "\n")
						raw = strings.Join(append(lines[:2], lines[3:]...), "\n")
					case "reordered":
						lines := strings.Split(raw, "\n")
						lines[1], lines[2] = lines[2], lines[1]
						raw = strings.Join(lines, "\n")
					case "broad source":
						if chain == "INPUT" {
							raw = strings.Replace(raw, "--dport 6443", "--dport 6443:6500", 1)
						} else {
							raw = strings.Replace(raw, "192.168.3.250/32", "192.168.3.0/24", 1)
						}
					case "early accept":
						first, rest, _ := strings.Cut(raw, "\n")
						raw = first + "\n-A " + chain + " -j ACCEPT\n" + rest
					}
					if err := validateBootstrapHostFirewall([]byte(raw), chain, expected); (err == nil) != (mode == "valid") {
						t.Fatal("unexpected firewall proof", mode, err)
					}
				})
			}
		})
	}
	// Unrelated INPUT rules after all seven unconditional guards are allowed;
	// their changes still invalidate the two-snapshot observation below.
	if validateBootstrapHostFirewall([]byte(strings.Replace(bootstrapFirewallInputFixture, "-P INPUT ACCEPT", "-P INPUT DROP", 1)), "INPUT", bootstrapHostFilesFixture()) != nil {
		t.Fatal("safe built-in policy rejected")
	}
	expected := bootstrapHostFilesFixture()
	expected.PodCIDR, expected.ServiceCIDR = "172.20.0.0/16", "172.21.0.0/16"
	raw := strings.NewReplacer("10.42.0.0/16", expected.PodCIDR, "10.43.0.0/16", expected.ServiceCIDR).Replace(bootstrapFirewallPeerFixture)
	if validateBootstrapHostFirewall([]byte(raw), bootstrapHostFirewallChain, expected) != nil {
		t.Fatal("source network inheritance missing")
	}
}

func TestBootstrapHostFirewallObservation(t *testing.T) {
	for _, mode := range []string{"valid", "read failure", "initial authority", "late authority", "cancel", "late drift", "aliased drift", "nil check", "nil read"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, checks := 0, 0
			check := func(context.Context) error {
				checks++
				if mode == "initial authority" || mode == "late authority" && checks == 9 {
					return errors.New("private authority error")
				}
				return nil
			}
			shared := []byte(bootstrapFirewallInputFixture)
			read := func(_ context.Context, chain string) ([]byte, error) {
				calls++
				if mode == "read failure" {
					return nil, errors.New("private observation error")
				}
				if mode == "cancel" {
					cancel()
				}
				if chain == "INPUT" {
					if calls == 3 && (mode == "late drift" || mode == "aliased drift") {
						// Both snapshots individually valid; only exact comparison
						// catches this unrelated SSH rule change. Mutate in place.
						copy(shared, strings.Replace(bootstrapFirewallInputFixture, "--dport 22", "--dport 23", 1))
					}
					if mode == "aliased drift" {
						return shared, nil
					}
					return bytes.Clone(shared), nil
				}
				return []byte(bootstrapFirewallPeerFixture), nil
			}
			if mode == "nil read" {
				read = nil
			}
			if mode == "nil check" {
				check = nil
			}
			err := proveBootstrapPrejoinFirewallWith(ctx, bootstrapHostFilesFixture(), check, read)
			if (err == nil) != (mode == "valid") || err != nil && err != errBootstrapSource {
				t.Fatal("observation accepted drift or leaked diagnostics", err)
			}
			if mode == "valid" && (calls != 4 || checks != 9) || mode == "initial authority" && calls != 0 {
				t.Fatal("proof boundary count changed", calls, checks)
			}
		})
	}
}

func TestBootstrapHostFirewallOutputBound(t *testing.T) {
	out := &bootstrapHostFirewallOutput{}
	if n, err := out.Write(bytes.Repeat([]byte{'x'}, bootstrapHostFirewallLimit)); err != nil || n != bootstrapHostFirewallLimit {
		t.Fatal("limit rejected", n, err)
	}
	if n, err := out.Write([]byte{'y'}); err != errBootstrapSource || n != 0 || out.buffer.Len() != bootstrapHostFirewallLimit {
		t.Fatal("overflow accepted", n, err)
	}
	if _, err := readBootstrapHostFirewall(context.Background(), "INPUT; invalid"); err != errBootstrapSource {
		t.Fatal("non-fixed command accepted", err)
	}
}
