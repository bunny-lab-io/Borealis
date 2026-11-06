package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

const bootstrapDockerKeyURL = "https://download.docker.com/linux/ubuntu/gpg"
const bootstrapDockerKeySHA256 = "1500c1f56fa9e26b9b8f42452a553675796ade0807cdce11975eb98170b3a570"
const bootstrapDockerKeyLimit = 16 << 10

// Exact reviewed public-key bytes, not a caller-supplied fingerprint or trust
// on first use. A changed upstream key needs a reviewed source update.
func validBootstrapDockerKey(raw []byte) bool {
	digest := sha256.Sum256(raw)
	return len(raw) > 0 && len(raw) <= bootstrapDockerKeyLimit && hex.EncodeToString(digest[:]) == bootstrapDockerKeySHA256
}

func downloadBootstrapDockerKey(ctx context.Context) ([]byte, error) {
	client := newBootstrapDockerKeyClient()
	defer client.CloseIdleConnections()
	return downloadBootstrapDockerKeyWith(ctx, client.Do)
}

func newBootstrapDockerKeyClient() *http.Client {
	transport := &http.Transport{
		DialContext:     (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second, MaxResponseHeaderBytes: 16 << 10,
		DisableCompression: true, DisableKeepAlives: true,
	}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func downloadBootstrapDockerKeyWith(ctx context.Context, do func(*http.Request) (*http.Response, error)) ([]byte, error) {
	if do == nil || ctx.Err() != nil {
		return nil, errBootstrapSource
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, bootstrapDockerKeyURL, nil)
	if err != nil {
		return nil, errBootstrapSource
	}
	request.Header.Set("Accept-Encoding", "identity")
	response, err := do(request)
	if err != nil || response == nil || response.Body == nil {
		return nil, errBootstrapSource
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > bootstrapDockerKeyLimit ||
		(response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity") {
		return nil, errBootstrapSource
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, bootstrapDockerKeyLimit+1))
	if err != nil || ctx.Err() != nil || !validBootstrapDockerKey(raw) {
		return nil, errBootstrapSource
	}
	return raw, nil
}

func bootstrapDockerRepositoryFiles(expected clusterbootstrap.PreparationExpected, suite string, key []byte) ([]bootstrapHostFile, error) {
	if expected.Validate() != nil || !bootstrapDockerSuite.MatchString(suite) || !validBootstrapDockerKey(key) {
		return nil, errBootstrapSource
	}
	files := []bootstrapHostFile{
		{path: "etc/apt/keyrings/docker.asc", mode: 0644, data: bytes.Clone(key)},
		{path: "etc/apt/sources.list.d/docker.sources", mode: 0644, data: []byte("Types: deb\nURIs: https://download.docker.com/linux/ubuntu\nSuites: " + suite + "\nComponents: stable\nArchitectures: amd64\nSigned-By: /etc/apt/keyrings/docker.asc\n")},
	}
	target := expected.Target
	target.HolderID, target.Generation, target.OperationAttempt = "", 0, 0
	for i := range files {
		raw, err := json.Marshal(struct {
			Domain, Source, Path string
			Target               clusterbootstrap.SessionBinding
			Data                 []byte
		}{"prejoin-docker-repository-v1", expected.Source.SourceSHA, files[i].path, target, files[i].data})
		if err != nil {
			return nil, errBootstrapSource
		}
		digest := sha256.Sum256(raw)
		files[i].pending = ".borealis-docker-" + hex.EncodeToString(digest[:])
	}
	return files, nil
}

// Only a future journaled prepare_host action may call this while retaining
// source/identity custody and both locks. No production caller exists. This
// writes two files only: it never invokes APT, starts Docker or releases fences.
func installBootstrapDockerRepository(ctx context.Context, expected clusterbootstrap.PreparationExpected, check func(context.Context) error) error {
	if os.Geteuid() != 0 || os.Getegid() != 0 {
		return errBootstrapSource
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return errBootstrapSource
	}
	defer root.Close()
	return installBootstrapDockerRepositoryWith(ctx, root, expected, check, readBootstrapDockerProbe, downloadBootstrapDockerKey)
}

func installBootstrapDockerRepositoryWith(ctx context.Context, root *os.Root, expected clusterbootstrap.PreparationExpected,
	check func(context.Context) error, read func(context.Context, string) ([]byte, error), download func(context.Context) ([]byte, error)) error {
	if expected.Validate() != nil || root == nil || check == nil || read == nil || download == nil {
		return errBootstrapSource
	}
	boundary := func() error {
		if ctx.Err() != nil || check(ctx) != nil || ctx.Err() != nil {
			return errBootstrapSource
		}
		return nil
	}
	before, err := observeBootstrapDockerHost(ctx, root, nil, check, read)
	if err != nil || boundary() != nil {
		return errBootstrapSource
	}
	key, err := download(ctx)
	if err != nil || boundary() != nil {
		return errBootstrapSource
	}
	files, err := bootstrapDockerRepositoryFiles(expected, before.suite, key)
	if err != nil {
		return errBootstrapSource
	}
	// Repeat clean-host proof after network IO, before any filesystem effect.
	var installed []bootstrapHostFile
	observe := func() error {
		current, err := observeBootstrapDockerHost(ctx, root, installed, check, read)
		if err != nil || !reflect.DeepEqual(before, current) {
			return errBootstrapSource
		}
		return nil
	}
	if observe() != nil {
		return errBootstrapSource
	}
	// Pin every existing ancestor from the original preflight across network IO
	// and directory creation. Only this call's safe absent-to-created parents
	// may extend the snapshot; replacing an existing directory is never adopted.
	originalBoundary := boundary
	boundary = func() error {
		current, err := bootstrapDockerParents(root, before.osLink != "")
		if err != nil || originalBoundary() != nil {
			return errBootstrapSource
		}
		for path, identity := range before.parents {
			if current[path] != identity {
				return errBootstrapSource
			}
		}
		return nil
	}
	for _, file := range files {
		if bootstrapHostParents(ctx, root, filepath.Dir(file.path), boundary, false) != nil {
			return errBootstrapSource
		}
		// Reject both final and deterministic scratch collisions before writing
		// either file. Never adopt a previous attempt, even with identical bytes.
		for _, path := range []string{file.path, filepath.Join(filepath.Dir(file.path), file.pending)} {
			if !bootstrapDockerPathAbsent(root, path) {
				return errBootstrapSource
			}
		}
	}
	for _, file := range files {
		if bootstrapHostParents(ctx, root, filepath.Dir(file.path), boundary, true) != nil {
			return errBootstrapSource
		}
	}
	if boundary() != nil {
		return errBootstrapSource
	}
	before.parents, err = bootstrapDockerParents(root, before.osLink != "")
	if err != nil {
		return errBootstrapSource
	}
	parents, err := bootstrapHostFileAncestors(root, files)
	if err != nil {
		return errBootstrapSource
	}
	pinned := func() error {
		current, err := bootstrapHostFileAncestors(root, files)
		if err != nil || !reflect.DeepEqual(parents, current) || boundary() != nil {
			return errBootstrapSource
		}
		for _, file := range installed {
			if proveBootstrapHostFile(root, file) != nil {
				return errBootstrapSource
			}
		}
		return nil
	}
	for _, file := range files {
		if pinned() != nil || observe() != nil || publishBootstrapHostFile(root, file, pinned) != nil {
			return errBootstrapSource
		}
		installed = append(installed, file)
		if pinned() != nil || observe() != nil {
			return errBootstrapSource
		}
	}
	return pinned()
}
