package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type clusterSSHSiteWorkerInventoryRead func(context.Context) (*operatorSiteWorkerInventory, error)
type clusterSSHRuntimeSecretRead func(context.Context) (clusterbootstrap.PreparationRuntimeSecret, error)

// This callable ingredient has no production caller. The eventual source scope
// must supply its original full Secret observation and fenced native rereader.
// It owns only a cloned standard transport; borrowed TLS/timeouts remain intact.
func newClusterSSHSiteWorkerInventoryRead(borrowed *borealisOperatorClient, original clusterbootstrap.PreparationRuntimeSecret, network clusterbootstrap.SourceNetwork,
	observe clusterSSHRuntimeSecretRead) (clusterSSHSiteWorkerInventoryRead, func(), error) {
	if borrowed == nil || borrowed.httpClient == nil || borrowed.httpClient.Jar != nil || observe == nil ||
		!clusterSSHSiteWorkerOperatorBinding(borrowed, original, network) {
		return nil, nil, clusterbootstrap.ErrPreparationConfig
	}
	transport := borrowed.httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	standard, ok := transport.(*http.Transport)
	if !ok || standard == nil {
		return nil, nil, clusterbootstrap.ErrPreparationConfig
	}
	owned := standard.Clone()
	owned.Proxy = nil
	client := *borrowed.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout <= 0 || client.Timeout > 10*time.Second {
		client.Timeout = 10 * time.Second
	}
	private := *borrowed
	private.httpClient = &client
	read, stop := clusterSSHSiteWorkerInventoryReader(&private, original, network, observe, owned)
	return read, func() { stop(); owned.CloseIdleConnections() }, nil
}

// Kept separate so deterministic owned-body tests can exercise failure during
// Close. Production always constructs the copied standard transport above.
func clusterSSHSiteWorkerInventoryReader(client *borealisOperatorClient, original clusterbootstrap.PreparationRuntimeSecret, network clusterbootstrap.SourceNetwork,
	observe clusterSSHRuntimeSecretRead, transport http.RoundTripper) (clusterSSHSiteWorkerInventoryRead, func()) {
	// Production and deterministic body fixtures share this exact composition.
	// The request-context guard sits inside joined cleanup accounting, so shorter
	// http.Client.Timeout expiry during Close cannot escape the sticky tracker.
	cleanup := &clusterSSHMembershipHTTP{transport: clusterSSHPreparationConfigurationHTTP{transport}}
	if client != nil && client.httpClient != nil {
		private, copied := *client, *client.httpClient
		copied.Transport = cleanup
		private.httpClient = &copied
		client = &private
	}
	var serial sync.Mutex
	failed := false
	stop := func() { serial.Lock(); failed = true; serial.Unlock() }
	return func(parent context.Context) (_ *operatorSiteWorkerInventory, err error) {
		serial.Lock()
		defer serial.Unlock()
		defer func() {
			if err != nil {
				failed = true
			}
		}()
		fail := func() (*operatorSiteWorkerInventory, error) { return nil, clusterbootstrap.ErrPreparationConfig }
		if failed || parent.Err() != nil || transport == nil || !cleanup.clean() || observe == nil || !clusterSSHSiteWorkerOperatorBinding(client, original, network) {
			return fail()
		}
		current := func() bool {
			value, err := observe(parent)
			return err == nil && parent.Err() == nil && original.SameObservation(value) && clusterSSHSiteWorkerOperatorBinding(client, value, network)
		}
		if !current() {
			return fail()
		}
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		body, _ := json.Marshal(borealisOperatorCommandRequest{Verb: "ListSiteWorkers"})
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/v1/command", bytes.NewReader(body))
		if err != nil {
			return fail()
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(borealisOperatorTokenHeader, client.token)
		response, err := client.httpClient.Do(request)
		if err != nil {
			return fail()
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, operatorSiteWorkerInventoryMaxBytes+1))
		closeErr := response.Body.Close() // Join owned cleanup before any source recheck or result.
		if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || ctx.Err() != nil || !cleanup.clean() {
			return fail()
		}
		inventory, err := decodeClusterSSHSiteWorkerInventory(raw)
		if err != nil || !current() || !cleanup.clean() || ctx.Err() != nil {
			return fail()
		}
		return inventory, nil
	}, stop
}

func clusterSSHSiteWorkerOperatorBinding(client *borealisOperatorClient, secret clusterbootstrap.PreparationRuntimeSecret, network clusterbootstrap.SourceNetwork) bool {
	if client == nil || client.httpClient == nil || network.Validate() != nil {
		return false
	}
	raw, err := secret.OperatorBaseURL()
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	u, parseErr := url.Parse(base)
	if err != nil || parseErr != nil || u == nil || u.Scheme != "http" || u.Host == "" || u.Hostname() == "" ||
		u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" ||
		u.String() != base || client.baseURL != base {
		return false
	}
	// Engine.sh's supported default resolves the operator Service ClusterIP and
	// emits HTTP on its fixed default port. Custom endpoints/ports are unavailable
	// here; bootstrap Settings do not inherit an operator URL override. The source
	// scope supplies this original observed service network and must recheck it.
	services, prefixErr := netip.ParsePrefix(network.ServiceCIDR)
	ip, ipErr := netip.ParseAddr(u.Hostname())
	if ipErr != nil || prefixErr != nil || !services.Contains(ip) || ip == services.Addr() || !services.Contains(ip.Next()) || !ip.Is4() || ip.String() != u.Hostname() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || u.Port() != "8088" || u.Host != ip.String()+":8088" {
		return false
	}
	key := strings.TrimSpace(secret.Settings()["BOREALIS_OPERATOR_SECRET"])
	return key != "" && subtle.ConstantTimeCompare([]byte(client.token), []byte(goBorealisOperatorToken([]byte(key)))) == 1
}

func decodeClusterSSHSiteWorkerInventory(raw []byte) (*operatorSiteWorkerInventory, error) {
	fail := func() (*operatorSiteWorkerInventory, error) { return nil, clusterbootstrap.ErrPreparationConfig }
	if len(raw) == 0 || len(raw) > operatorSiteWorkerInventoryMaxBytes || !utf8.Valid(raw) {
		return fail()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, err := clusterSSHPrivateJSONValue(d, 0)
	object, ok := value.(map[string]any)
	if err != nil || !ok || len(object) != 3 || object["ok"] != true || object["verb"] != "ListSiteWorkers" {
		return fail()
	}
	if _, err := d.Token(); err != io.EOF {
		return fail()
	}
	result, ok := object["result"].(map[string]any)
	if !ok || len(result) != 2 {
		return fail()
	}
	if workers, ok := result["workers"].([]any); !ok || workers == nil {
		return fail()
	}
	projection, ok := result["source_inventory"].(map[string]any)
	if !ok || projection == nil {
		return fail()
	}
	encoded, err := json.Marshal(projection)
	var inventory operatorSiteWorkerInventory
	if err != nil || json.Unmarshal(encoded, &inventory) != nil {
		return fail()
	}
	// Closed spelling/shape, including required explicit nulls and empty arrays.
	// Exact re-encoding also rejects case aliases, extra fields and exponent ints.
	canonical, err := json.Marshal(inventory)
	var typed any
	typedDecoder := json.NewDecoder(bytes.NewReader(canonical))
	typedDecoder.UseNumber()
	if err != nil || typedDecoder.Decode(&typed) != nil || !reflect.DeepEqual(projection, typed) || !clusterSSHSiteWorkerInventoryValid(inventory) {
		return fail()
	}
	return &inventory, nil
}

func clusterSSHSiteWorkerInventoryValid(value operatorSiteWorkerInventory) bool {
	if value.Version != 1 || value.Pods == nil || value.Services == nil || len(value.Pods) > clusterSSHSiteWorkerLimit || len(value.Services) > clusterSSHSiteWorkerLimit {
		return false
	}
	validHash := func(hash string) bool {
		decoded, err := hex.DecodeString(hash)
		return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == hash
	}
	validMeta := func(meta operatorSiteWorkerInventoryMetadata) bool {
		if !borealisOperatorKubernetesObjectNameAllowed(meta.Name) || meta.Namespace != "borealis" || !vipScopeUUID(meta.UID) ||
			len(meta.ResourceVersion) < 1 || len(meta.ResourceVersion) > 128 || meta.Labels == nil || meta.Annotations == nil {
			return false
		}
		for _, c := range meta.ResourceVersion {
			if c < 33 || c > 126 {
				return false
			}
		}
		if meta.DeletionTimestamp != nil {
			if _, err := time.Parse(time.RFC3339Nano, *meta.DeletionTimestamp); err != nil {
				return false
			}
		}
		for i, fields := range []map[string]string{meta.Labels, meta.Annotations} {
			allowed := operatorSiteWorkerInventoryLabels
			if i == 1 {
				allowed = operatorSiteWorkerInventoryAnnotations
			}
			for key, text := range fields {
				if !slices.Contains(allowed, key) || !utf8.ValidString(text) {
					return false
				}
			}
		}
		return true
	}
	last := ""
	for _, pod := range value.Pods {
		if !validMeta(pod.Metadata) || !validHash(pod.SHA256) || pod.Metadata.Name <= last || (pod.NodeName != nil && !schedulerPlacementName(*pod.NodeName)) {
			return false
		}
		last = pod.Metadata.Name
	}
	last = ""
	for _, service := range value.Services {
		if !validMeta(service.Metadata) || !validHash(service.SHA256) || service.Metadata.Name <= last || service.Type != "ClusterIP" ||
			service.Selector == nil || len(service.Selector) > clusterSSHSiteWorkerLimit || service.Ports == nil || len(service.Ports) > clusterSSHSiteWorkerLimit {
			return false
		}
		last = service.Metadata.Name
		if service.ClusterIP != nil {
			ip, err := netip.ParseAddr(*service.ClusterIP)
			if err != nil || ip.String() != *service.ClusterIP || ip.IsUnspecified() || ip.IsMulticast() {
				return false
			}
		}
		for key, text := range service.Selector {
			if key == "" || !utf8.ValidString(key) || !utf8.ValidString(text) {
				return false
			}
		}
		for _, port := range service.Ports {
			if !borealisOperatorKubernetesObjectNameAllowed(port.Name) || !borealisOperatorKubernetesObjectNameAllowed(port.TargetPort) || port.Port < 1 || port.Port > 65535 || port.Protocol != "TCP" {
				return false
			}
		}
	}
	return true
}
