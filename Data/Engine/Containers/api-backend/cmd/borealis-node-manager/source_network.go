package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

var sourceNetworkHostname = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)

// Fixed read-only source action. Existing host kubelet identity authenticates
// only to the local TLS supervisor. No K3s token, kubeconfig, process environment
// or response-supplied URL is used. The controller must independently compare
// the public result with its current source roster and sole-operation lease.
func (m *manager) inspectSourceNetwork(ctx context.Context) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	root, err := os.OpenRoot("/var/lib/rancher/k3s/agent")
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	defer root.Close()
	read := func(name string) ([]byte, error) {
		file, err := root.Open(name)
		if err != nil {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 64<<10 {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		raw, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
		if err != nil || len(raw) > 64<<10 {
			return nil, clusterbootstrap.ErrPreparationConfig
		}
		return raw, nil
	}
	ca, caErr := read("server-ca.crt")
	cert, certErr := read("client-kubelet.crt")
	key, keyErr := read("client-kubelet.key")
	roots := x509.NewCertPool()
	pair, pairErr := tls.X509KeyPair(cert, key)
	if caErr != nil || certErr != nil || keyErr != nil || pairErr != nil || !roots.AppendCertsFromPEM(ca) {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	get := func(ctx context.Context, path string) ([]byte, error) {
		return sourceNetworkHTTPGet(ctx, client, "https://127.0.0.1:6443", path)
	}
	identity := func() (string, string, error) {
		machine, err := os.ReadFile("/etc/machine-id")
		boot, bootErr := os.ReadFile("/proc/sys/kernel/random/boot_id")
		if err != nil || bootErr != nil || len(machine) > 64 || len(boot) > 64 {
			return "", "", clusterbootstrap.ErrPreparationConfig
		}
		return strings.TrimSpace(string(machine)), strings.TrimSpace(string(boot)), nil
	}
	observed, err := observeSourceNetwork(ctx, m.nodeName, identity, get, sourceManagementLink)
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return map[string]any{"source_network": observed}, nil
}

func sourceNetworkHTTPGet(ctx context.Context, client *http.Client, base, path string) ([]byte, error) {
	if client == nil || !strings.HasPrefix(base, "https://") {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	request.Header.Set("Accept", "application/json")
	response, err := copyClient.Do(request)
	if err != nil {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (128<<10)+1))
	if err != nil || ctx.Err() != nil || len(raw) == 0 || len(raw) > 128<<10 {
		return nil, clusterbootstrap.ErrPreparationConfig
	}
	return raw, nil
}

func observeSourceNetwork(ctx context.Context, name string, identity func() (string, string, error), get func(context.Context, string) ([]byte, error), linkRead func(context.Context, string) (clusterbootstrap.ManagementLink, error)) (clusterbootstrap.SourceNetwork, error) {
	fail := func() (clusterbootstrap.SourceNetwork, error) {
		return clusterbootstrap.SourceNetwork{}, clusterbootstrap.ErrPreparationConfig
	}
	// Validate name before constructing the only Node request path.
	if !sourceNetworkHostname.MatchString(name) || identity == nil || get == nil || linkRead == nil || ctx.Err() != nil {
		return fail()
	}
	machine, boot, err := identity()
	if err != nil {
		return fail()
	}
	before, err := get(ctx, "/v1-k3s/config")
	if err != nil {
		return fail()
	}
	rawNode, err := get(ctx, "/api/v1/nodes/"+name)
	if err != nil {
		return fail()
	}
	var node struct {
		Metadata struct{ UID string }
		Status   struct {
			NodeInfo  struct{ KubeletVersion string }
			Addresses []struct{ Type, Address string }
		}
	}
	if json.Unmarshal(rawNode, &node) != nil {
		return fail()
	}
	address := ""
	for _, item := range node.Status.Addresses {
		if item.Type == "InternalIP" {
			if address != "" {
				return fail()
			}
			address = item.Address
		}
	}
	link, err := linkRead(ctx, address)
	if err != nil || !link.MatchesAddress(address) {
		return fail()
	}
	base := clusterbootstrap.SourceNetwork{NodeUID: node.Metadata.UID, Hostname: name, MachineID: machine, BootID: boot, K3sVersion: node.Status.NodeInfo.KubeletVersion, ManagementLink: link}
	network, err := clusterbootstrap.ParseSourceNetwork(before, base)
	if err != nil {
		return fail()
	}
	if clusterbootstrap.ValidateSourceNode(rawNode, network, address) != nil {
		return fail()
	}
	version, err := get(ctx, "/version")
	if err != nil || clusterbootstrap.ValidateSourceVersion(version, network.K3sVersion) != nil {
		return fail()
	}
	after, err := get(ctx, "/v1-k3s/config")
	if err != nil {
		return fail()
	}
	rechecked, err := clusterbootstrap.ParseSourceNetwork(after, base)
	version, versionErr := get(ctx, "/version")
	currentNode, nodeErr := get(ctx, "/api/v1/nodes/"+name)
	currentLink, linkErr := linkRead(ctx, address)
	currentMachine, currentBoot, identityErr := identity()
	if nodeErr != nil || clusterbootstrap.ValidateSourceNode(currentNode, network, address) != nil || linkErr != nil || currentLink != link || err != nil || versionErr != nil || clusterbootstrap.ValidateSourceVersion(version, network.K3sVersion) != nil || identityErr != nil || rechecked != network || currentMachine != machine || currentBoot != boot || ctx.Err() != nil {
		return fail()
	}
	return network, nil
}
