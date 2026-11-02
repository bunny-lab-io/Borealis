package main

import (
	"fmt"
	"net/http"
)

func doWorkerOperationRequest(req *http.Request) (*http.Response, error) {
	// A redirect can follow accepted work. It cannot authorize a second dispatch
	// or forward the internal caller's token to a different route.
	client := *http.DefaultClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err == nil && resp.StatusCode >= 300 && resp.StatusCode < 400 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("site-worker redirect denied")
	}
	return resp, err
}
