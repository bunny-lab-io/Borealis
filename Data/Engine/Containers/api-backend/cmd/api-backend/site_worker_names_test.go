package main

import (
	"math"
	"strings"
	"testing"
)

func TestSiteWorkerServiceReservationUsesImmutableSiteID(t *testing.T) {
	for _, name := range []string{"Original Site", "Renamed Site", "!!!", strings.Repeat("a", 100)} {
		for _, guid := range []string{newClusterUUID(), newClusterUUID()} {
			if got := siteWorkerServiceNameForSite(7, name, guid); got != "site-worker-site-7" {
				t.Fatalf("mutable input changed reservation: %q", got)
			}
			if siteWorkerPodNameForSite(7) != siteWorkerServiceNameForSite(7, name, guid) {
				t.Fatal("primary Pod and Service reservation names differ")
			}
		}
	}
	for _, site := range []int64{1, 42, math.MaxInt64} {
		name := siteWorkerServiceNameForSite(site, "Same Name", "same-guid")
		if !borealisOperatorKubernetesObjectNameAllowed(name) || name == siteWorkerServiceNameForSite(site-1, "Same Name", "same-guid") {
			t.Fatalf("invalid or aliased site reservation %d: %q", site, name)
		}
	}
	for _, site := range []int64{0, -1} {
		if got := siteWorkerServiceNameForSite(site, "Named Site", "worker"); got != "" {
			t.Fatalf("invalid site accepted: %q", got)
		}
	}
}

func TestSiteWorkerNameForSiteUsesSanitizedSiteSlug(t *testing.T) {
	workerGUID := "4f4e4578-23c9-4e5f-bfa9-ce99a6f0b78c"
	got := siteWorkerNameForSite(7, "Bunny's Lab", workerGUID)
	want := "site-worker-bunnys-lab"
	if got != want {
		t.Fatalf("unexpected site-worker name: got %q want %q", got, want)
	}
}

func TestSiteWorkerNameForSiteBoundsKubernetesName(t *testing.T) {
	workerGUID := "4f4e4578-23c9-4e5f-bfa9-ce99a6f0b78c"
	got := siteWorkerNameForSite(7, strings.Repeat("a", siteWorkerSiteSlugMax+20), workerGUID)
	if len(got) > siteWorkerKubernetesNameMax {
		t.Fatalf("site-worker name exceeds Kubernetes label length: %d %q", len(got), got)
	}
	if !strings.HasPrefix(got, siteWorkerNamePrefix) {
		t.Fatalf("expected bounded site slug in site-worker name, got %q", got)
	}
	if strings.Contains(got, workerGUID) {
		t.Fatalf("did not expect worker guid suffix in site-worker name, got %q", got)
	}
}

func TestSiteWorkerNameForSiteFallsBackToSiteID(t *testing.T) {
	workerGUID := "worker-safe"
	got := siteWorkerNameForSite(42, "!!!", workerGUID)
	want := "site-worker-site-42"
	if got != want {
		t.Fatalf("unexpected fallback site-worker name: got %q want %q", got, want)
	}
}

func TestValidateSiteWorkerCompatibleSiteName(t *testing.T) {
	if slug, payload, status := validateSiteWorkerCompatibleSiteName("Bunny's Lab"); slug != "bunnys-lab" || payload != nil || status != 0 {
		t.Fatalf("unexpected valid site-name result slug=%q payload=%#v status=%d", slug, payload, status)
	}
	if _, payload, status := validateSiteWorkerCompatibleSiteName("!!!"); payload == nil || status != 400 {
		t.Fatalf("expected invalid site-name rejection payload=%#v status=%d", payload, status)
	}
	if _, payload, status := validateSiteWorkerCompatibleSiteName(strings.Repeat("a", siteWorkerSiteSlugMax+1)); payload == nil || status != 400 {
		t.Fatalf("expected long site-name rejection payload=%#v status=%d", payload, status)
	}
}
