package clusterbootstrap

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

const vipLeaseFixture = `{"apiVersion":"coordination.k8s.io/v1","kind":"Lease","metadata":{"name":"borealis-cluster-vip","namespace":"kube-system","uid":"11111111-1111-4111-8111-111111111111","resourceVersion":"opaque-1","annotations":{"fixture":"excluded"}},"spec":{"holderIdentity":"engine-01","leaseDurationSeconds":10,"acquireTime":"2000-01-01T00:00:00.000000Z","renewTime":"2000-01-01T00:00:02.000000Z","leaseTransitions":0}}`

func TestVIPLeaseStrictProjection(t *testing.T) {
	v, err := ParseVIPLease([]byte(vipLeaseFixture))
	if err != nil || v.Validate() != nil || v.ResourceVersion != "opaque-1" {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(vipLeaseFixture, `"kind":"Lease"`, `"kind":"Secret"`, 1),
		strings.Replace(vipLeaseFixture, `"leaseTransitions":0`, `"leaseTransitions":null`, 1),
		strings.Replace(vipLeaseFixture, `"leaseTransitions":0`, `"leaseTransitions":0,"strategy":"OldestEmulationVersion"`, 1),
		strings.Replace(vipLeaseFixture, `"leaseTransitions":0`, `"leaseTransitions":0,"leaseTransitions":1`, 1),
		strings.Replace(vipLeaseFixture, `"leaseTransitions":0`, `"LeaseTransitions":0`, 1),
		strings.Replace(vipLeaseFixture, `"leaseDurationSeconds":10`, `"leaseDurationSeconds":1`, 1),
		strings.Replace(vipLeaseFixture, `"uid":`, `"deletionTimestamp":"2026-09-12T00:00:00Z","uid":`, 1),
		strings.Replace(vipLeaseFixture, `"holderIdentity":"engine-01"`, `"holderIdentity":""`, 1),
		strings.Replace(vipLeaseFixture, "opaque-1", strings.Repeat("x", 129), 1),
		strings.Replace(vipLeaseFixture, "2000-01-01T00:00:02.000000Z", "1999-01-01T00:00:02.000000Z", 1),
		vipLeaseFixture + `{}`,
	} {
		if got, err := ParseVIPLease([]byte(bad)); err != ErrPreparationConfig || got != (VIPLease{}) {
			t.Fatal("unsafe lease accepted", err)
		}
	}
}

func TestVIPLeaseProgressUsesLocalRenewalLifetime(t *testing.T) {
	for _, mode := range []string{"success", "future remote clock", "metadata only", "expired", "epoch", "holder", "UID", "duration", "backward renewal", "same revision changed", "replayed revision", "slow read", "backward local", "version bound"} {
		t.Run(mode, func(t *testing.T) {
			v, _ := ParseVIPLease([]byte(vipLeaseFixture))
			if mode == "future remote clock" {
				v.AcquireTime = "2099-01-01T00:00:00Z"
				v.RenewTime = "2099-01-01T00:00:02Z"
			}
			var p VIPLeaseProgress
			start := time.Unix(1800000000, 0)
			ready, deadline, err := p.Observe(v, start, start.Add(time.Millisecond))
			if err != nil || ready || !deadline.Equal(start.Add(5*time.Second)) {
				t.Fatal("first read granted ownership")
			}
			meta := v
			meta.ResourceVersion = "metadata-only"
			ready, next, err := p.Observe(meta, start.Add(time.Second), start.Add(time.Second+time.Millisecond))
			if err != nil || ready || !next.Equal(deadline) {
				t.Fatal("metadata update extended lease")
			}
			now := start.Add(2 * time.Second)
			current := meta
			current.ResourceVersion = "renewed"
			renewed, _ := vipLeaseTime(v.RenewTime)
			current.RenewTime = renewed.Add(2 * time.Second).Format(time.RFC3339Nano)
			switch mode {
			case "metadata only":
				current.RenewTime = meta.RenewTime
			case "expired":
				now = deadline
			case "epoch":
				current.Transitions++
			case "holder":
				current.Holder = "engine-02"
			case "UID":
				current.UID = "22222222-2222-4222-8222-222222222222"
			case "duration":
				current.DurationSeconds = 20
			case "backward renewal":
				current.RenewTime = v.AcquireTime
			case "same revision changed":
				current.ResourceVersion = meta.ResourceVersion
			case "replayed revision":
				current.ResourceVersion = v.ResourceVersion
			case "backward local":
				now = start
			}
			finished := now.Add(time.Millisecond)
			if mode == "slow read" {
				finished = now.Add(time.Second + time.Nanosecond)
			}
			ready, next, err = p.Observe(current, now, finished)
			ok := mode == "success" || mode == "future remote clock" || mode == "metadata only" || mode == "version bound"
			if (err == nil) != ok {
				t.Fatal("lease lifetime outcome", err)
			}
			if !ok {
				if _, _, err := p.Observe(meta, start.Add(3*time.Second), start.Add(3*time.Second)); err == nil {
					t.Fatal("failed tracker resurrected")
				}
				return
			}
			if mode == "metadata only" {
				if ready || !next.Equal(deadline) {
					t.Fatal("old renewal accepted")
				}
				return
			}
			if !ready || !next.Equal(now.Add(5*time.Second)) {
				t.Fatal("fresh renewal not bound to local read start")
			}
			if mode == "version bound" {
				for i := 0; i < 126; i++ {
					current.ResourceVersion = fmt.Sprintf("meta-%d", i)
					_, _, err = p.Observe(current, now, finished)
				}
				if err != ErrPreparationConfig {
					t.Fatal("unbounded revision history")
				}
			}
		})
	}
	if _, err := ParseVIPLease(append([]byte(vipLeaseFixture), bytes.Repeat([]byte(" "), 64<<10)...)); err == nil {
		t.Fatal("unbounded lease response")
	}
}
