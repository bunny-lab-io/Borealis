package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestClusterSSHPreparationPublicationPostgresProposalFence(t *testing.T) {
	for _, mode := range []string{"success", "wrong plan", "controller drift", "payload drift", "credential drift", "expired report", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f, snapshot, _, _ := newSSHPreparationApprovalFixture(t, false)
			release := preparationPublicationFixture(t)
			if release.expected != snapshot.work.Baseline {
				t.Fatal("fixture baseline mismatch")
			}
			read := f.c.preparationPublicationRead(snapshot)
			plan, _ := clusterSSHPreparationPlanDigest(snapshot.work.Authorization)
			if mode == "wrong plan" {
				plan = strings.Repeat("f", 64)
			}
			ctx, cancel := context.WithCancel(bootstrapContext())
			defer cancel()
			previous := http.DefaultClient
			t.Cleanup(func() { http.DefaultClient = previous })
			calls := 0
			http.DefaultClient = &http.Client{Transport: bootstrapRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				if f.c.store.db.Stats().InUse != 0 {
					t.Error("publication IO retained database connection")
				}
				if calls == 1 {
					switch mode {
					case "controller drift":
						f.exec(t, `UPDATE engine.cluster_application_leases SET holder='other-controller' WHERE name=$1`, clusterControllerLeaseName)
					case "payload drift":
						f.exec(t, `UPDATE engine.cluster_operations SET payload_json=payload_json||' ' WHERE id=$1`, f.op.ID)
					case "credential drift":
						f.exec(t, `UPDATE engine.cluster_onboarding_credentials SET ciphertext=ciphertext||'changed' WHERE target_id=$1`, f.targets[1].Binding.TargetID)
					case "expired report":
						f.exec(t, `UPDATE engine.cluster_onboarding_targets SET inspected_at=1 WHERE operation_id=$1`, f.op.ID)
					case "cancelled":
						cancel()
					}
				}
				return http.DefaultTransport.RoundTrip(r)
			})}
			before := f.events(t)
			digest, err := read(ctx, plan)
			if (err == nil) != (mode == "success") || (err != nil && digest != "") || f.events(t) != before {
				t.Fatal("unexpected proposal-bound publication result", err)
			}
			if mode == "success" {
				again, err := read(ctx, plan)
				if err != nil || digest != again || !validClusterSSHPreparationDigest(digest) {
					t.Fatal("unchanged proposal did not retain publication digest", err)
				}
			}
			if mode == "wrong plan" && calls != 0 {
				t.Fatal("wrong plan reached network")
			}
			f.assertState(t, "waiting", clusterSSHQualificationStep, true)
		})
	}
}
