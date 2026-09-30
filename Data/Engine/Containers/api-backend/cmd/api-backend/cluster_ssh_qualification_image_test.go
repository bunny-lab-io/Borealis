package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"context"
	"errors"
	"testing"
)

type qualificationPostgresFixture struct {
	demand                clusterbootstrap.PostgresImageDemand
	reads, closes         int
	readError, closeError bool
}

func newQualificationPostgresFixture() *qualificationPostgresFixture {
	return &qualificationPostgresFixture{demand: clusterbootstrap.PostgresImageDemand{
		Reference: sshPostgresResolvedFixture, IncomingBytes: 100, IncomingEntries: 1,
		RuntimeBytes: 1000, RuntimeEntries: 10, WorkerPeakBytes: 190, WorkerPeakEntries: 5,
	}}
}

func (f *qualificationPostgresFixture) StorageDemand(ctx context.Context, check func(context.Context) error) (clusterbootstrap.PostgresImageDemand, error) {
	f.reads++
	if f.readError || f.closes != 0 || check(ctx) != nil {
		return clusterbootstrap.PostgresImageDemand{}, errors.New("unavailable owned archive")
	}
	return f.demand, nil
}

func (f *qualificationPostgresFixture) Close() error {
	f.closes++
	if f.closeError {
		return errors.New("cleanup failed")
	}
	return nil
}

func TestClusterSSHQualificationPostgresAcquisitionLifetime(t *testing.T) {
	for _, mode := range []string{"valid", "missing source", "authority before", "acquire failure", "no archive", "acquire partial failure", "cancel acquisition", "authority after acquisition", "measurement failure", "consumer failure", "measurement drift", "authority after consumption", "cleanup failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			image := newQualificationPostgresFixture()
			image.readError = mode == "measurement failure"
			image.closeError = mode == "cleanup failure"
			source := clusterSSHPostgresImage{Configured: sshPostgresConfiguredFixture, Resolved: sshPostgresResolvedFixture, Bootstrap: sshPostgresBootstrapFixture()}
			if mode == "missing source" {
				source = clusterSSHPostgresImage{}
			}
			acquired, consumed := false, false
			check := func(ctx context.Context) error {
				if ctx.Err() != nil || mode == "authority before" || (acquired && mode == "authority after acquisition") || (consumed && mode == "authority after consumption") {
					return errors.New("authority lost")
				}
				return nil
			}
			acquire := func(ctx context.Context, reference string, boundary func(context.Context) error) (clusterSSHQualificationPostgresArchive, error) {
				if reference != source.Resolved || boundary(ctx) != nil {
					t.Fatal("acquisition not bound to original source/authority")
				}
				acquired = true
				switch mode {
				case "acquire failure":
					return nil, errors.New("allocation/download failed")
				case "no archive":
					return nil, nil
				case "acquire partial failure":
					return image, errors.New("partial acquisition")
				case "cancel acquisition":
					cancel()
				}
				return image, nil
			}
			err := withClusterSSHQualificationPostgresCapacity(ctx, source, []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 50, Entries: 1}}, check, acquire,
				func(ctx context.Context, c clusterSSHPreparedImageCapacity) error {
					consumed = true
					if image.closes != 0 || c.PostgresWorkerPeakBytes != 190 || c.PostgresWorkerPeakEntries != 5 || len(c.Target) != 2 || c.Target[0].Bytes != 150 || c.Target[1].Bytes != 1000 {
						t.Fatal("archive lifetime or separate worker/target demand lost")
					}
					if mode == "consumer failure" {
						return errors.New("target evidence failed")
					}
					if mode == "measurement drift" {
						image.demand.RuntimeBytes++
					}
					return nil
				})
			if (err == nil) != (mode == "valid") {
				t.Fatalf("unexpected outcome: %v", err)
			}
			wantAcquired := mode != "missing source" && mode != "authority before"
			wantClosed := wantAcquired && mode != "acquire failure" && mode != "no archive"
			if acquired != wantAcquired || (image.closes == 1) != wantClosed || image.closes > 1 {
				t.Fatal("owned acquisition not cleaned exactly once")
			}
			wantConsumed := mode == "valid" || mode == "consumer failure" || mode == "measurement drift" || mode == "authority after consumption" || mode == "cleanup failure"
			if consumed != wantConsumed {
				t.Fatal("failed prerequisite reached capacity consumer")
			}
			if mode == "valid" && image.reads != 2 {
				t.Fatal("archive not remeasured after consumption")
			}
		})
	}
}
