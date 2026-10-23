package clusterbootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
)

type postgresRoundTrip func(*http.Request) (*http.Response, error)

func (f postgresRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestPostgresRegistryDigestAndCredentialBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "foreign redirect", "redirect challenge", "bad digest", "wrong length", "encoding", "token redirect", "bad token", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			payload := []byte("bounded public image bytes")
			h := sha256.Sum256(payload)
			digest := "sha256:" + hex.EncodeToString(h[:])
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			r := newPostgresRegistry()
			defer r.close()
			r.client.Transport = postgresRoundTrip(func(req *http.Request) (*http.Response, error) {
				calls++
				resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(payload)), ContentLength: int64(len(payload)), Request: req}
				switch calls {
				case 1:
					if req.URL.Host != "ghcr.io" || !strings.HasSuffix(req.URL.Path, "/blobs/"+digest) || req.Header.Get("Authorization") != "" {
						t.Fatal("wrong initial registry read")
					}
					resp.StatusCode = 401
				case 2:
					if req.URL.Host != "ghcr.io" || req.URL.Path != "/token" || req.URL.Query().Get("scope") != "repository:cloudnative-pg/postgresql:pull" || req.URL.Query().Get("service") != "ghcr.io" || req.Header.Get("Authorization") != "" {
						t.Fatal("wrong token scope")
					}
					resp.Body = io.NopCloser(strings.NewReader(`{"token":"anonymous-fixture-token"}`))
					resp.ContentLength = -1
					if mode == "token redirect" {
						resp.StatusCode = 302
						resp.Header.Set("Location", "https://foreign.invalid/token")
					}
					if mode == "bad token" {
						resp.Body = io.NopCloser(strings.NewReader(`{"token":"bad token"}`))
					}
				case 3:
					if req.Header.Get("Authorization") != "Bearer anonymous-fixture-token" {
						t.Fatal("missing scoped token")
					}
					resp.StatusCode = 307
					resp.Header.Set("Location", "https://pkg-containers.githubusercontent.com/fixture?signature=private")
					if mode == "foreign redirect" {
						resp.Header.Set("Location", "https://foreign.invalid/fixture")
					}
				case 4:
					if req.URL.Host != "pkg-containers.githubusercontent.com" || req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
						t.Fatal("credential crossed redirect")
					}
					switch mode {
					case "redirect challenge":
						resp.StatusCode = 401
					case "bad digest":
						resp.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", len(payload))))
					case "wrong length":
						resp.ContentLength++
					case "encoding":
						resp.Header.Set("Content-Encoding", "gzip")
					}
				default:
					t.Fatal("unexpected request")
				}
				return resp, nil
			})
			var out bytes.Buffer
			err := r.fetch(ctx, digest, int64(len(payload)), false, &out)
			if mode == "valid" {
				if err != nil || !bytes.Equal(out.Bytes(), payload) {
					t.Fatal("download", err)
				}
			} else if err != ErrImageArchive {
				t.Fatal("invalid input accepted", err)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				if err.Error() != ErrImageArchive.Error() {
					t.Fatal("signed URL escaped")
				}
			}
		})
	}
	for _, u := range []string{"http://ghcr.io/x", "https://ghcr.io.evil/x", "https://u:p@ghcr.io/x", "https://ghcr.io:444/x", "https://pkg-containers.githubusercontent.com/x#fragment"} {
		if postgresRegistryRedirect(u) {
			t.Fatal("unsafe redirect", u)
		}
	}
}
