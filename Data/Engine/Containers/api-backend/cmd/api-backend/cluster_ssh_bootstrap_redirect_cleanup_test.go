package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type bootstrapRedirectCleanupBody struct {
	io.Reader
	close func() error
}

func (b bootstrapRedirectCleanupBody) Close() error { return b.close() }

func TestClusterSSHBootstrapRedirectJoinsCleanup(t *testing.T) {
	const cdn = "https://release-assets.githubusercontent.com/github-production-release-asset/123/fixture?signature=private-fixture-value"
	for _, mode := range []string{"valid", "close failure", "cancel during Close", "deadline during Close"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("BOREALIS_GITHUB_API_BASE_URL", "https://api.github.com")
			t.Setenv("BOREALIS_ENGINE_GITHUB_REPOSITORY", "bunny-lab-io/Borealis")
			previous := http.DefaultClient
			t.Cleanup(func() { http.DefaultClient = previous })
			bound := 5 * time.Second
			if mode == "deadline during Close" {
				bound = time.Second
			}
			ctx, cancel := context.WithTimeout(bootstrapContext(), bound)
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var unblock sync.Once
			defer unblock.Do(func() { close(release) })
			var calls, redirectCloses, finalCloses atomic.Int64
			http.DefaultClient = &http.Client{Transport: bootstrapRoundTripper(func(r *http.Request) (*http.Response, error) {
				call := calls.Add(1)
				if call == 1 {
					if r.URL.Host != "api.github.com" || r.Header.Get("Authorization") != "Bearer "+bootstrapTestToken {
						t.Error("API request lost original destination or credentials")
					}
					body := bootstrapRedirectCleanupBody{strings.NewReader("redirect"), func() error {
						if redirectCloses.Add(1) != 1 {
							return errors.New("redirect body closed repeatedly")
						}
						close(entered)
						<-release
						if mode == "close failure" {
							return errors.New("private cleanup failure " + cdn)
						}
						return nil
					}}
					return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{cdn}}, Body: body, ContentLength: 8, Request: r}, nil
				}
				if call != 2 || r.URL.String() != cdn || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != "" {
					t.Error("redirect escaped exact destination or forwarded credentials")
				}
				body := bootstrapRedirectCleanupBody{strings.NewReader("binary"), func() error { finalCloses.Add(1); return nil }}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, ContentLength: 6, Request: r}, nil
			})}
			type result struct {
				response *http.Response
				err      error
			}
			done := make(chan result, 1)
			go func() {
				asset := clusterBootstrapAsset{ID: 1, Size: 6, URL: "https://api.github.com/repos/bunny-lab-io/Borealis/releases/assets/1"}
				response, err := openClusterBootstrapAsset(ctx, asset)
				done <- result{response, err}
			}()
			joined := false
			defer func() {
				cancel()
				unblock.Do(func() { close(release) })
				if !joined {
					select {
					case got := <-done:
						if got.response != nil {
							_ = got.response.Body.Close()
						}
					case <-time.After(5 * time.Second):
						t.Error("download did not join released cleanup")
					}
				}
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("redirect cleanup was not reached")
			}
			if mode == "cancel during Close" {
				cancel()
			} else if mode == "deadline during Close" {
				<-ctx.Done()
				if ctx.Err() != context.DeadlineExceeded {
					t.Fatal("fixture missed actual request deadline")
				}
			}
			select {
			case got := <-done:
				joined = true
				if got.response != nil {
					_ = got.response.Body.Close()
				}
				t.Fatal("download escaped blocked redirect cleanup", got.err)
			case <-time.After(20 * time.Millisecond):
			}
			if calls.Load() != 1 {
				t.Fatal("CDN contacted before redirect cleanup joined")
			}
			unblock.Do(func() { close(release) })
			var got result
			select {
			case got = <-done:
				joined = true
			case <-time.After(5 * time.Second):
				t.Fatal("download did not return after redirect cleanup")
			}
			if mode == "valid" {
				if got.err != nil || got.response == nil || calls.Load() != 2 {
					t.Fatal("valid redirect failed", got.err)
				}
				raw, err := io.ReadAll(got.response.Body)
				closeErr := got.response.Body.Close()
				if err != nil || closeErr != nil || string(raw) != "binary" || finalCloses.Load() != 1 {
					t.Fatal("valid redirect lost final body ownership", err, closeErr)
				}
			} else {
				if got.response != nil {
					_ = got.response.Body.Close()
				}
				if got.err == nil || got.response != nil || calls.Load() != 1 || finalCloses.Load() != 0 || strings.Contains(got.err.Error(), "private-fixture-value") || strings.Contains(got.err.Error(), bootstrapTestToken) {
					t.Fatal("failed cleanup acquired final body or leaked remote diagnostics", got.err)
				}
			}
			if redirectCloses.Load() != 1 {
				t.Fatal("redirect response cleanup not joined exactly once")
			}
		})
	}
}
