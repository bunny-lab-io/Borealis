package clusterbootstrap

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Deliberately one public repository: no caller URL, proxy, credential helper,
// registry challenge realm, cookie jar or configured PostgreSQL tag is used.
const PostgresImageRepository = "ghcr.io/cloudnative-pg/postgresql"

func ValidPostgresImageReference(reference string) bool {
	digest, ok := strings.CutPrefix(reference, PostgresImageRepository+"@sha256:")
	return ok && digestPattern.MatchString(digest) && digest != strings.Repeat("0", 64)
}

type postgresRegistry struct {
	client *http.Client
	token  string
}

func newPostgresRegistry() *postgresRegistry {
	transport := &http.Transport{
		DialContext:     (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConnsPerHost: 2,
		DisableCompression: true, ForceAttemptHTTP2: true,
	}
	return &postgresRegistry{client: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (r *postgresRegistry) close() { r.token = ""; r.client.CloseIdleConnections() }

func (r *postgresRegistry) authenticate(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ghcr.io/token?service=ghcr.io&scope=repository%3Acloudnative-pg%2Fpostgresql%3Apull", nil)
	if err != nil {
		return ErrImageArchive
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := r.client.Do(req)
	if err != nil {
		return ErrImageArchive
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !postgresIdentityEncoding(resp) {
		return ErrImageArchive
	}
	raw, err := io.ReadAll(io.LimitReader(contextReader{ctx, resp.Body}, 32769))
	if err != nil || len(raw) > 32768 {
		return ErrImageArchive
	}
	var value struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if imageJSON(raw, &value) != nil {
		return ErrImageArchive
	}
	if value.Token == "" {
		value.Token = value.AccessToken
	}
	if len(value.Token) < 1 || len(value.Token) > 16384 {
		return ErrImageArchive
	}
	for _, ch := range value.Token {
		if ch < 33 || ch > 126 {
			return ErrImageArchive
		}
	}
	if ctx.Err() != nil {
		return ErrImageArchive
	}
	r.token = value.Token
	return nil
}

func postgresIdentityEncoding(resp *http.Response) bool {
	encoding := resp.Header.Get("Content-Encoding")
	return encoding == "" || encoding == "identity"
}

func postgresRegistryRedirect(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.User == nil && u.Fragment == "" && (u.Port() == "" || u.Port() == "443") && (u.Hostname() == "ghcr.io" || u.Hostname() == "pkg-containers.githubusercontent.com")
}

// Unknown length is permitted only for bounded root metadata. Every body is
// authenticated by requested digest; diagnostics never include signed URLs.
func (r *postgresRegistry) fetch(parent context.Context, digest string, size int64, manifest bool, output io.Writer) error {
	limit := MaxImageArchiveBytes
	if manifest {
		limit = 1 << 20
	}
	if parent.Err() != nil || !validImageDigest(digest) || size == 0 || size < -1 || size > limit || size == -1 && !manifest || output == nil {
		return ErrImageArchive
	}
	if size > 0 {
		limit = size
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	endpoint := "blobs"
	if manifest {
		endpoint = "manifests"
	}
	address := "https://ghcr.io/v2/cloudnative-pg/postgresql/" + endpoint + "/" + digest
	redirected, authenticated := false, false
	for attempt := 0; attempt < 7; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return ErrImageArchive
		}
		req.Header.Set("Accept-Encoding", "identity")
		if manifest {
			req.Header.Set("Accept", strings.Join([]string{ociIndexType, dockerIndexType, ociManifestType, dockerManifestType}, ", "))
		}
		if r.token != "" && !redirected {
			req.Header.Set("Authorization", "Bearer "+r.token)
		}
		resp, err := r.client.Do(req)
		if err != nil {
			return ErrImageArchive
		}
		if resp.StatusCode == http.StatusUnauthorized && !authenticated && !redirected {
			_ = resp.Body.Close()
			if r.authenticate(ctx) != nil {
				return ErrImageArchive
			}
			authenticated = true
			continue
		}
		if resp.StatusCode == 301 || resp.StatusCode == 302 || resp.StatusCode == 303 || resp.StatusCode == 307 || resp.StatusCode == 308 {
			next, err := resp.Location()
			_ = resp.Body.Close()
			if err != nil || !postgresRegistryRedirect(next.String()) {
				return ErrImageArchive
			}
			address, redirected = next.String(), true
			continue
		}
		if resp.StatusCode != http.StatusOK || !postgresIdentityEncoding(resp) || resp.ContentLength > limit || size > 0 && resp.ContentLength >= 0 && resp.ContentLength != size {
			_ = resp.Body.Close()
			return ErrImageArchive
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(output, h), io.LimitReader(contextReader{ctx, resp.Body}, limit+1))
		_ = resp.Body.Close()
		if err != nil || n < 1 || n > limit || size > 0 && n != size || "sha256:"+hex.EncodeToString(h.Sum(nil)) != digest || ctx.Err() != nil {
			return ErrImageArchive
		}
		return nil
	}
	return ErrImageArchive
}
