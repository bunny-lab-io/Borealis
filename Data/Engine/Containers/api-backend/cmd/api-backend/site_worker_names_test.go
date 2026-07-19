package main

import (
	"strings"
	"testing"
)

func TestSiteWorkerNameForSiteUsesSanitizedSiteSlug(t *testing.T) {
	workerGUID := "4f4e4578-23c9-4e5f-bfa9-ce99a6f0b78c"
	got := siteWorkerNameForSite(7, "Bunny's Lab", workerGUID)
	want := "site-worker-bunnys-lab-" + workerGUID
	if got != want {
		t.Fatalf("unexpected site-worker name: got %q want %q", got, want)
	}
}

func TestSiteWorkerNameForSiteBoundsKubernetesName(t *testing.T) {
	workerGUID := "4f4e4578-23c9-4e5f-bfa9-ce99a6f0b78c"
	got := siteWorkerNameForSite(7, "DRaaS Infrastructure Production Worker Fleet", workerGUID)
	if len(got) > siteWorkerKubernetesNameMax {
		t.Fatalf("site-worker name exceeds Kubernetes label length: %d %q", len(got), got)
	}
	if !strings.HasPrefix(got, "site-worker-draas-infrastr-") {
		t.Fatalf("expected bounded site slug in site-worker name, got %q", got)
	}
	if !strings.HasSuffix(got, "-"+workerGUID) {
		t.Fatalf("expected worker guid suffix in site-worker name, got %q", got)
	}
}

func TestSiteWorkerNameForSiteFallsBackToSiteID(t *testing.T) {
	workerGUID := "worker-safe"
	got := siteWorkerNameForSite(42, "!!!", workerGUID)
	want := "site-worker-site-42-worker-safe"
	if got != want {
		t.Fatalf("unexpected fallback site-worker name: got %q want %q", got, want)
	}
}
