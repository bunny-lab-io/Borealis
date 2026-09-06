package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type clusterJoinConfig struct {
	Server    string `json:"k3s_server"`
	Version   string `json:"k3s_version"`
	PeerCIDRs string `json:"peer_cidrs"`
}

type clusterJoinAdmission struct {
	ID     string            `json:"admission_id"`
	State  string            `json:"state"`
	Config clusterJoinConfig `json:"join_config"`
}

func validateClusterJoinEndpoint(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("requires HTTPS origin without credentials, path, query or fragment")
	}
	u.Path = ""
	return u.String(), nil
}

func clusterJoinHTTPClient(caFile string) (*http.Client, error) {
	var roots *x509.CertPool
	if caFile != "" {
		pem, err := os.ReadFile(filepath.Clean(caFile))
		if err != nil {
			return nil, err
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("CA file has no valid certificate")
		}
	}
	return &http.Client{Timeout: 30 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("cluster join redirects are forbidden") },
	}, nil
}

func clusterJoinRequest(ctx context.Context, client *http.Client, method, endpoint, path, bundle string, body []byte) (clusterJoinAdmission, int, error) {
	base, err := validateClusterJoinEndpoint(endpoint)
	if err != nil {
		return clusterJoinAdmission{}, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
	if err != nil {
		return clusterJoinAdmission{}, 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	if bundle != "" {
		request.Header.Set("X-Borealis-Cluster-Invite", bundle)
	}
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := bounded.Do(request)
	if err != nil {
		return clusterJoinAdmission{}, 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		return clusterJoinAdmission{}, response.StatusCode, fmt.Errorf("cluster admission returned HTTP %d; redirects are not followed", response.StatusCode)
	}
	var admission clusterJoinAdmission
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&admission); err != nil {
		return admission, response.StatusCode, errors.New("cluster admission response is invalid")
	}
	if !clusterUUIDPattern.MatchString(admission.ID) {
		return admission, response.StatusCode, errors.New("cluster admission response lacks canonical identity")
	}
	return admission, response.StatusCode, nil
}

func submitClusterAdmission(ctx context.Context, client *http.Client, endpoint string, payload map[string]any) (clusterJoinAdmission, error) {
	if _, err := validateClusterJoinEndpoint(endpoint); err != nil {
		return clusterJoinAdmission{}, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return clusterJoinAdmission{}, err
	}
	for {
		admission, status, err := clusterJoinRequest(ctx, client, http.MethodPost, endpoint, "/api/bootstrap/cluster/join", "", raw)
		if err == nil {
			return admission, nil
		}
		if status > 0 && status < 500 {
			return admission, err
		}
		select {
		case <-ctx.Done():
			return admission, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func readClusterAdmission(ctx context.Context, client *http.Client, endpoint, bundle, id string) (clusterJoinAdmission, error) {
	if !clusterUUIDPattern.MatchString(id) {
		return clusterJoinAdmission{}, errors.New("admission identity is invalid")
	}
	admission, _, err := clusterJoinRequest(ctx, client, http.MethodGet, endpoint, "/api/bootstrap/cluster/join/"+id+"/events", bundle, nil)
	if err == nil && admission.ID != id {
		return admission, errors.New("admission response changed requested identity")
	}
	return admission, err
}

func waitForClusterAdmission(ctx context.Context, client *http.Client, endpoint, bundle, id string) (clusterJoinAdmission, error) {
	if _, err := validateClusterJoinEndpoint(endpoint); err != nil {
		return clusterJoinAdmission{}, err
	}
	if !clusterUUIDPattern.MatchString(id) {
		return clusterJoinAdmission{}, errors.New("admission identity is invalid")
	}
	for {
		admission, status, err := clusterJoinRequest(ctx, client, http.MethodGet, endpoint, "/api/bootstrap/cluster/join/"+id+"/events", bundle, nil)
		if err != nil && status > 0 && status < 500 {
			return admission, err
		}
		if err == nil {
			if admission.ID != id {
				return admission, errors.New("admission response changed requested identity")
			}
			switch admission.State {
			case "Approved", "Admitted":
				return admission, nil
			case "Pending Quorum":
			default:
				return admission, fmt.Errorf("admission is %s; inspect original admission/operation in Cluster Management", admission.State)
			}
		}
		select {
		case <-ctx.Done():
			return admission, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func validateClusterJoinConfig(config clusterJoinConfig, managementIP, expectedServer, expectedVersion, expectedPeers string) error {
	u, err := url.Parse(config.Server)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "6443" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("authority returned invalid K3s server origin")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || ip.To4() == nil || !ip.IsPrivate() || !k3sPattern.MatchString(config.Version) {
		return errors.New("authority returned invalid private Cluster VIP or K3s version")
	}
	peers, err := normalizePeerCIDRs(config.PeerCIDRs)
	if err != nil {
		return fmt.Errorf("authority returned invalid peer roster: %w", err)
	}
	covered := false
	for _, raw := range strings.Split(peers, ",") {
		_, network, _ := net.ParseCIDR(raw)
		covered = covered || network.Contains(net.ParseIP(managementIP))
	}
	if !covered {
		return errors.New("authoritative peer roster omits this target")
	}
	if strings.TrimSpace(expectedServer) != "" && strings.TrimSpace(expectedServer) != config.Server || strings.TrimSpace(expectedVersion) != "" && strings.TrimSpace(expectedVersion) != config.Version {
		return errors.New("caller K3s assertion differs from authoritative cluster settings")
	}
	if strings.TrimSpace(expectedPeers) != "" {
		normalized, err := normalizePeerCIDRs(expectedPeers)
		if err != nil {
			return errors.New("caller peer assertion differs from authoritative cluster roster")
		}
		expected := strings.Split(normalized, ",")
		actual := strings.Split(peers, ",")
		sort.Strings(expected)
		sort.Strings(actual)
		if strings.Join(expected, ",") != strings.Join(actual, ",") {
			return errors.New("caller peer assertion differs from authoritative cluster roster")
		}
	}
	return nil
}
