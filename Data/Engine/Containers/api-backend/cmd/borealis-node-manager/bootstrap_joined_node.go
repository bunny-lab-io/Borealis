package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/engineidentity"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

// Independent target observation through its existing kubelet identity. No
// kubeconfig, bearer credential, arbitrary URL or Kubernetes mutation is used.
// This grants no service start or admission; caller still owns phase authority.
func observeBootstrapJoinedK3sNode(ctx context.Context, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding, join *clusterbootstrap.K3sJoinMaterial, check func(context.Context) error) (clusterbootstrap.K3sJoinedNode, error) {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return clusterbootstrap.K3sJoinedNode{}, errBootstrapSource
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return clusterbootstrap.K3sJoinedNode{}, errBootstrapSource
	}
	defer root.Close()
	identity := bootstrapJoinedHostCheck(expected, check)
	return observeBootstrapJoinedK3sNodeAt(ctx, root, expected, source, join, identity, newBootstrapJoinedNodeClient)
}

func bootstrapJoinedHostCheck(expected clusterbootstrap.PreparationExpected, check func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		if check == nil || ctx.Err() != nil || check(ctx) != nil {
			return errBootstrapSource
		}
		hostname, err := os.Hostname()
		if err != nil || hostname != expected.Target.Hostname || !hostHasIPv4(expected.Target.Address) {
			return errBootstrapSource
		}
		for path, want := range map[string]string{"/etc/machine-id": expected.Target.MachineID, "/proc/sys/kernel/random/boot_id": expected.TargetBootID} {
			raw, err := readBootstrapPublicFile(path, 128)
			if err != nil || strings.TrimSpace(string(raw)) != want {
				return errBootstrapSource
			}
		}
		return nil
	}
}

func newBootstrapJoinedNodeClient(config *tls.Config) *http.Client {
	transport := &http.Transport{TLSClientConfig: config, DisableKeepAlives: true, DisableCompression: true,
		DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, TLSHandshakeTimeout: 3 * time.Second, ResponseHeaderTimeout: 3 * time.Second}
	return &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Disposable-root/client seam; production always selects / and fixed client.
// All three files are 0600 in pinned K3s agent/config getHostFile/getClientCert.
func observeBootstrapJoinedK3sNodeAt(parent context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding, join *clusterbootstrap.K3sJoinMaterial,
	check func(context.Context) error, clientFor func(*tls.Config) *http.Client) (clusterbootstrap.K3sJoinedNode, error) {
	var result clusterbootstrap.K3sJoinedNode
	err := withBootstrapJoinedK3sClientAt(parent, root, expected, source, join, check, clientFor, false, func(ctx context.Context, refresh func(context.Context) error, get func(context.Context, string) ([]byte, error), _ string) error {
		var err error
		result, err = observeBootstrapJoinedNodeWith(ctx, expected, refresh, get)
		return err
	})
	if err != nil {
		return clusterbootstrap.K3sJoinedNode{}, err
	}
	return result, nil
}

// Fixed private TLS lifetime shared by Node and etcd membership observations.
// Callback stays synchronous; neither raw identity nor HTTP client escapes.
func withBootstrapJoinedK3sClientAt(parent context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected, source engineidentity.Binding, join *clusterbootstrap.K3sJoinMaterial,
	check func(context.Context) error, clientFor func(*tls.Config) *http.Client, membership bool,
	consume func(context.Context, func(context.Context) error, func(context.Context, string) ([]byte, error), string) error) error {
	fail := func() error { return errBootstrapSource }
	if root == nil || check == nil || clientFor == nil || consume == nil || join.Verify(parent, expected, source, check) != nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	files := []bootstrapHostFile{{path: "var/lib/rancher/k3s/agent/server-ca.crt", mode: 0600}, {path: "var/lib/rancher/k3s/agent/client-kubelet.crt", mode: 0600}, {path: "var/lib/rancher/k3s/agent/client-kubelet.key", mode: 0600}}
	if membership {
		files = append(files, bootstrapHostFile{path: "var/lib/rancher/k3s/server/db/etcd/name", mode: 0600})
	}
	ancestors, err := bootstrapHostFileAncestors(root, files)
	if err != nil {
		return fail()
	}
	originals := make([]os.FileInfo, len(files))
	defer func() {
		for _, file := range files {
			clear(file.data)
		}
	}()
	for i := range files {
		file, err := root.OpenFile(files[i].path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return fail()
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Mode() != 0600 || info.Size() < 1 || info.Size() > 64<<10 {
			file.Close()
			return fail()
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(owner.Uid) != os.Geteuid() || int(owner.Gid) != os.Getegid() || owner.Nlink != 1 {
			file.Close()
			return fail()
		}
		raw := make([]byte, int(info.Size())+1)
		n, readErr := io.ReadFull(file, raw)
		closeErr := file.Close()
		files[i].data = raw[:n]
		if readErr != io.EOF && readErr != io.ErrUnexpectedEOF || closeErr != nil || int64(n) != info.Size() || proveBootstrapHostFile(root, files[i]) != nil {
			return fail()
		}
		originals[i] = info
	}
	memberName := ""
	if membership {
		memberName = string(files[3].data)
		if !clusterbootstrap.ValidK3sMemberName(memberName, expected.Target.Hostname) {
			return fail()
		}
	}
	refresh := func(ctx context.Context) error {
		if join.Verify(ctx, expected, source, check) != nil {
			return errBootstrapSource
		}
		current, err := bootstrapHostFileAncestors(root, files)
		if err != nil || len(current) != len(ancestors) {
			return errBootstrapSource
		}
		for path, want := range ancestors {
			if current[path] != want {
				return errBootstrapSource
			}
		}
		for i, file := range files {
			info, err := root.Lstat(file.path)
			if err != nil || !os.SameFile(originals[i], info) || !originals[i].ModTime().Equal(info.ModTime()) || proveBootstrapHostFile(root, file) != nil {
				return errBootstrapSource
			}
		}
		return join.Verify(ctx, expected, source, check)
	}
	if refresh(ctx) != nil {
		return fail()
	}
	// Authenticate the agent's CA against freshly acquired original-source token
	// before using its TLS identity. Token is never sent in the HTTP request.
	storage := make([]byte, clusterbootstrap.MaxK3sJoinTokenBytes)
	defer clear(storage)
	token := bytes.NewBuffer(storage[:0])
	if join.WriteToken(ctx, expected, source, check, token) != nil {
		return fail()
	}
	bound, err := clusterbootstrap.NewK3sJoinMaterial(expected, source, token.Bytes(), files[0].data)
	if err != nil {
		return fail()
	}
	bound.Close()
	pair, err := tls.X509KeyPair(files[1].data, files[2].data)
	if err != nil || len(pair.Certificate) == 0 {
		return fail()
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || leaf.Subject.CommonName != "system:node:"+expected.Target.Hostname || len(leaf.Subject.Organization) != 1 || leaf.Subject.Organization[0] != "system:nodes" {
		return fail()
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(files[0].data) {
		return fail()
	}
	config := &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	defer func() {
		pair.PrivateKey = nil
		config.Certificates = nil
		config.RootCAs = nil
	}()
	client := clientFor(config)
	if client == nil {
		return fail()
	}
	defer client.CloseIdleConnections()
	get := func(ctx context.Context, path string) ([]byte, error) {
		if path != "/version" && path != "/api/v1/nodes/"+expected.Target.Hostname && (!membership || path != "/db/info") || refresh(ctx) != nil {
			return nil, errBootstrapSource
		}
		raw, err := sourceNetworkHTTPGet(ctx, client, "https://127.0.0.1:6443", path)
		if err != nil || refresh(ctx) != nil {
			clear(raw)
			return nil, errBootstrapSource
		}
		return raw, nil
	}
	if consume(ctx, refresh, get, memberName) != nil || refresh(ctx) != nil {
		return fail()
	}
	return nil
}

func observeBootstrapJoinedNodeWith(ctx context.Context, expected clusterbootstrap.PreparationExpected, check func(context.Context) error, get func(context.Context, string) ([]byte, error)) (clusterbootstrap.K3sJoinedNode, error) {
	fail := func() (clusterbootstrap.K3sJoinedNode, error) {
		return clusterbootstrap.K3sJoinedNode{}, errBootstrapSource
	}
	if expected.Validate() != nil || check == nil || get == nil || ctx.Err() != nil || check(ctx) != nil {
		return fail()
	}
	version := func() bool {
		raw, err := get(ctx, "/version")
		defer clear(raw)
		return err == nil && clusterbootstrap.ValidateSourceVersion(raw, expected.K3sVersion) == nil
	}
	if !version() {
		return fail()
	}
	var first, last clusterbootstrap.K3sJoinedNode
	for pass := 0; pass < 2; pass++ {
		raw, err := get(ctx, "/api/v1/nodes/"+expected.Target.Hostname)
		if err != nil {
			clear(raw)
			return fail()
		}
		node, err := clusterbootstrap.ParseK3sJoinedNode(raw, expected)
		clear(raw)
		if err != nil || check(ctx) != nil {
			return fail()
		}
		if pass == 0 {
			first = node
		} else {
			last = node
			first.Ready, node.Ready = false, false
			if first != node {
				return fail()
			}
		}
	}
	if !version() || ctx.Err() != nil || check(ctx) != nil {
		return fail()
	}
	return last, nil
}
