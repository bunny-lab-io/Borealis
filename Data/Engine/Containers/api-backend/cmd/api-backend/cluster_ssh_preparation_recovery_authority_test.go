package main

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"
)

func TestClusterSSHPreparationRecoveryLeaseCheckSerialCustody(t *testing.T) {
	work, record, _ := sshPreparationRecoveryRecordFixture(t)
	expiry := map[string]int64{}
	for _, target := range record.Targets {
		expiry[target.TargetID] = 100
	}
	custody := &clusterSSHPreparationRecoveryCustody{assigned: maps.Clone(expiry)}
	custody.started.Store(true)
	value := clusterSSHPreparationRecoveryAuthority{Reservation: record, Source: work.Proof.Source, Cohort: work.Proof.Cohort,
		Authorization: work.Authorization, ExpiresAt: maps.Clone(expiry)}
	renewals := 0
	check, snapshot := newClusterSSHPreparationRecoveryLeaseCheck(custody, expiry, func(context.Context) error { return nil },
		func(_ context.Context, expected map[string]int64, renew bool) (clusterSSHPreparationRecoveryAuthority, map[string]int64, error) {
			if !maps.Equal(expected, expiry) {
				t.Fatal("owner lost expected expiry")
			}
			if renew {
				renewals++
				for id := range expiry {
					expiry[id]++
				}
			}
			value.ExpiresAt = maps.Clone(expiry)
			return value, maps.Clone(expiry), nil
		})
	for range 3 {
		if err := check(context.Background()); err != nil {
			t.Fatal(err)
		}
		copy, err := snapshot()
		if err != nil || !maps.Equal(copy.ExpiresAt, expiry) {
			t.Fatal("snapshot", err)
		}
		copy.Reservation.Targets[0].PreviousHolder = "changed"
		copy.Cohort.Targets[0].Key.PublicKey[0] ^= 1
		copy.ExpiresAt[record.Targets[0].TargetID] = 0
	}
	if renewals != 3 || custody.failed.Load() || !reflect.DeepEqual(custody.assigned, map[string]int64{record.Targets[0].TargetID: 100, record.Targets[1].TargetID: 100}) {
		t.Fatal("renewal mutated issued custody or aliased consumer output")
	}
}

func TestClusterSSHPreparationRecoveryLeaseCheckPoisonsUncertainty(t *testing.T) {
	for _, mode := range []string{"preflight key", "read", "unexpected expiry", "renewal", "post-renewal key", "lineage drift", "missing sibling", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			work, record, _ := sshPreparationRecoveryRecordFixture(t)
			expiry := map[string]int64{}
			for _, target := range record.Targets {
				expiry[target.TargetID] = 100
			}
			custody := &clusterSSHPreparationRecoveryCustody{assigned: maps.Clone(expiry)}
			custody.started.Store(true)
			value := clusterSSHPreparationRecoveryAuthority{Reservation: record, Source: work.Proof.Source, ExpiresAt: maps.Clone(expiry)}
			verifications, reads := 0, 0
			check, snapshot := newClusterSSHPreparationRecoveryLeaseCheck(custody, expiry, func(context.Context) error {
				verifications++
				if mode == "preflight key" || mode == "post-renewal key" && verifications == 2 {
					return errors.New("synthetic key failure")
				}
				return nil
			}, func(_ context.Context, expected map[string]int64, renew bool) (clusterSSHPreparationRecoveryAuthority, map[string]int64, error) {
				reads++
				if mode == "read" || mode == "renewal" && renew {
					return value, nil, errors.New("synthetic uncertainty")
				}
				next := maps.Clone(expected)
				if mode == "unexpected expiry" || mode == "lineage drift" && renew {
					next[record.Targets[0].TargetID]++
				}
				if mode == "missing sibling" && renew {
					delete(next, record.Targets[0].TargetID)
				}
				if mode == "lineage drift" && renew {
					value.Source.ClusterID = "changed"
				}
				value.ExpiresAt = maps.Clone(next)
				return value, next, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			if check(ctx) == nil || !custody.failed.Load() {
				t.Fatal("uncertainty accepted")
			}
			count := reads
			if check(context.Background()) == nil || reads != count {
				t.Fatal("failed owner replayed authority/renewal")
			}
			if _, err := snapshot(); err == nil {
				t.Fatal("failed owner exported authority")
			}
		})
	}
}
