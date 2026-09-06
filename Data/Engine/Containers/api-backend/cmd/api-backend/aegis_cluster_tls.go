package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const aegisClusterMaxPEMBytes = 64 * 1024

// The holder belongs to one auth service. Published configurations, certificates
// and pools are immutable; neither reload nor TLS expiry touches the Aegis key.
type aegisClusterTLSReloader struct {
	mu          sync.Mutex
	paths       [3]string // certificate, private key, independently managed CA bundle
	serverName  string
	now         func() time.Time
	certificate *tls.Certificate
	roots       *x509.CertPool
	lastFailure string
}

func (auth *authService) clusterTLS() *aegisClusterTLSReloader {
	auth.aegisTLSMu.Lock()
	defer auth.aegisTLSMu.Unlock()
	if auth.aegisTLS == nil {
		auth.aegisTLS = &aegisClusterTLSReloader{
			paths: [3]string{
				envDefault("BOREALIS_AEGIS_CLUSTER_TLS_CERT", "/var/run/secrets/borealis-aegis-mtls/tls.crt"),
				envDefault("BOREALIS_AEGIS_CLUSTER_TLS_KEY", "/var/run/secrets/borealis-aegis-mtls/tls.key"),
				envDefault("BOREALIS_AEGIS_CLUSTER_TLS_CA", "/var/run/secrets/borealis-aegis-mtls/ca.crt"),
			},
			serverName: envDefault("BOREALIS_AEGIS_CLUSTER_PEER_HOST", "api-backend-aegis.borealis.svc"),
			now:        time.Now,
		}
	}
	return auth.aegisTLS
}

func (r *aegisClusterTLSReloader) serverConfig() (*tls.Config, error) {
	if _, err := r.config(true); err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, SessionTicketsDisabled: true,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) { return r.config(true) },
	}, nil
}

func (r *aegisClusterTLSReloader) config(server bool) (*tls.Config, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	files, updateErr := readAegisTLSProjection(r.paths)
	defer func() {
		for _, file := range files {
			zeroBytes(file.data)
		}
	}()
	if updateErr == nil {
		// Accept valid trust changes independently of the leaf. In particular,
		// removing a CA must not resurrect it just because our leaf is stale.
		roots, err := parseAegisTrustBundle(files[2].data)
		if files[2].err != nil {
			err = files[2].err
		}
		if err == nil {
			r.roots = roots
		} else {
			updateErr = fmt.Errorf("load Aegis cluster trust: %w", err)
		}
		certificate, err := tls.X509KeyPair(files[0].data, files[1].data)
		if files[0].err != nil || files[1].err != nil {
			err = errors.Join(files[0].err, files[1].err)
		}
		if err == nil && certificate.Leaf == nil {
			certificate.Leaf, err = x509.ParseCertificate(certificate.Certificate[0])
		}
		if err == nil {
			err = r.verifyIdentity(&certificate)
		}
		if err == nil {
			r.certificate = &certificate
		} else {
			updateErr = errors.Join(updateErr, fmt.Errorf("load Aegis cluster identity: %w", err))
		}
	}
	// Cached identity is usable only under the newest accepted roots and current
	// clock. Malformed projection never extends certificate or CA validity.
	err := r.verifyIdentity(r.certificate)
	failure := ""
	if combined := errors.Join(updateErr, err); combined != nil {
		failure = combined.Error()
	}
	if failure != r.lastFailure {
		if failure != "" {
			log.Printf("Aegis cluster TLS reload: %s", failure)
		} else if r.lastFailure != "" {
			log.Print("Aegis cluster TLS projection recovered")
		}
		r.lastFailure = failure
	}
	if err != nil {
		return nil, fmt.Errorf("Aegis cluster TLS unavailable: %w", errors.Join(updateErr, err))
	}
	config := &tls.Config{
		MinVersion: tls.VersionTLS13, SessionTicketsDisabled: true,
		Certificates: []tls.Certificate{*r.certificate}, Time: r.now,
	}
	if server {
		config.ClientAuth, config.ClientCAs = tls.RequireAndVerifyClientCert, r.roots
	} else {
		config.ServerName, config.RootCAs = r.serverName, r.roots
	}
	return config, nil
}

func (r *aegisClusterTLSReloader) verifyIdentity(certificate *tls.Certificate) error {
	if certificate == nil || r.roots == nil {
		return errors.New("validated certificate and trust bundle required")
	}
	leaf := certificate.Leaf
	if leaf == nil {
		return errors.New("parsed leaf certificate required")
	}
	intermediates := x509.NewCertPool()
	for _, der := range certificate.Certificate[1:] {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return err
		}
		intermediates.AddCert(cert)
	}
	// Both usages are required, rather than Verify's OR semantics for a list.
	for _, usage := range []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth} {
		if _, err := leaf.Verify(x509.VerifyOptions{
			Roots: r.roots, Intermediates: intermediates, CurrentTime: r.now(),
			DNSName: r.serverName, KeyUsages: []x509.ExtKeyUsage{usage},
		}); err != nil {
			return err
		}
	}
	return nil
}

func parseAegisTrustBundle(data []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	count := 0
	for len(bytes.TrimSpace(data)) > 0 {
		data = bytes.TrimSpace(data)
		if !bytes.HasPrefix(data, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errors.New("trust bundle must contain only PEM CA certificates")
		}
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || bytes.Count(data[:len(data)-len(rest)], []byte("-----BEGIN ")) != 1 {
			return nil, errors.New("malformed CA certificate PEM")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		if !cert.BasicConstraintsValid || !cert.IsCA || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
			return nil, errors.New("trust certificate must be a signing CA")
		}
		pool.AddCert(cert)
		count++
		data = rest
	}
	if count == 0 {
		return nil, errors.New("empty CA trust bundle")
	}
	return pool, nil
}

type aegisTLSFile struct {
	path string
	data []byte
	err  error
}

func readAegisTLSFile(path string) aegisTLSFile {
	resolved, err := filepath.EvalSymlinks(path)
	result := aegisTLSFile{path: resolved, err: err}
	if err != nil {
		return result
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() > aegisClusterMaxPEMBytes {
		result.err = errors.New("TLS projection requires regular files of at most 64 KiB")
		return result
	}
	file, err := os.Open(resolved)
	if err != nil {
		result.err = err
		return result
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		result.err = err
	} else if !info.Mode().IsRegular() || info.Size() > aegisClusterMaxPEMBytes {
		result.err = errors.New("TLS projection requires regular files of at most 64 KiB")
	} else {
		result.data, result.err = io.ReadAll(io.LimitReader(file, aegisClusterMaxPEMBytes+1))
		if len(result.data) > aegisClusterMaxPEMBytes {
			result.err = errors.New("TLS projection exceeds 64 KiB")
		}
	}
	return result
}

func readAegisTLSProjection(paths [3]string) ([3]aegisTLSFile, error) {
	var files [3]aegisTLSFile
	for i, path := range paths {
		files[i] = readAegisTLSFile(path)
	}
	// A projected volume switches ..data atomically. Re-resolve all paths and
	// compare contents too, covering both Kubernetes and direct file updates.
	for i, path := range paths {
		again := readAegisTLSFile(path)
		equalErrors := fmt.Sprint(files[i].err) == fmt.Sprint(again.err)
		if files[i].path != again.path || !bytes.Equal(files[i].data, again.data) || !equalErrors {
			zeroBytes(again.data)
			return files, errors.New("Aegis TLS projection changed while reading")
		}
		zeroBytes(again.data)
	}
	// Reject a persistently mixed generation inside one projected directory.
	if filepath.Dir(paths[0]) == filepath.Dir(paths[1]) && filepath.Dir(paths[1]) == filepath.Dir(paths[2]) {
		parent := ""
		for _, file := range files {
			if file.err == nil {
				dir := filepath.Dir(file.path)
				if parent != "" && parent != dir {
					return files, errors.New("Aegis TLS projection contains mixed generations")
				}
				parent = dir
			}
		}
	}
	return files, nil
}
