package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"borealis/api-backend/internal/clusterremote"
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"time"
)

// Explicit path choices for every original target in cohort order. Neither
// historical /opt figures nor an assumed Longhorn default choose these paths.
type clusterSSHTargetFilesystemSelection struct {
	TargetID          string
	Paths             []string
	RequirePersistent bool
}

type clusterSSHTargetFilesystemEvidence struct {
	TargetID string
	Storage  clusterremote.FilesystemEvidence
}

// This read-only scope observes physical filesystem availability; it neither
// reserves space nor proves Longhorn scheduling/replica/workload capacity.
func withClusterSSHTargetFilesystemEvidence(parent context.Context, readers clusterSSHNetworkTargetReaders,
	selection []clusterSSHTargetFilesystemSelection,
	consume func(context.Context, []clusterSSHTargetFilesystemEvidence) error) error {
	if readers.within == nil || readers.Authority == nil || readers.Refresh == nil || readers.Filesystem == nil || consume == nil || parent.Err() != nil {
		return clusterbootstrap.ErrPreparationConfig
	}
	selection = slices.Clone(selection)
	for i := range selection {
		selection[i].Paths = slices.Clone(selection[i].Paths)
	}
	return readers.within(parent, func(parent context.Context) error {
		ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
		defer cancel()
		frozen, err := readers.Authority(ctx)
		if err != nil || validateClusterSSHInspectionCohort(frozen.Cohort, frozen.Source) != nil || len(selection) != len(frozen.Cohort.Targets) {
			return clusterbootstrap.ErrPreparationConfig
		}
		raw, err := json.Marshal(frozen)
		var owned clusterSSHPreparationAuthority
		if err != nil || json.Unmarshal(raw, &owned) != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		frozen = owned
		frozen.Cohort.ObservedAt = 0
		for i, item := range frozen.Cohort.Targets {
			request := clusterremote.FilesystemRequest{MachineID: item.Report.MachineID, BootID: item.Report.BootID, Paths: selection[i].Paths, RequirePersistent: selection[i].RequirePersistent}
			if selection[i].TargetID != item.Binding.TargetID || request.Validate() != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
		}
		current := func() error {
			if readers.Refresh(ctx) != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			a, err := readers.Authority(ctx)
			a.Cohort.ObservedAt = 0
			if err != nil || !reflect.DeepEqual(a, frozen) || ctx.Err() != nil {
				return clusterbootstrap.ErrPreparationConfig
			}
			return nil
		}
		read := func() ([]clusterSSHTargetFilesystemEvidence, error) {
			values := make([]clusterSSHTargetFilesystemEvidence, 0, len(selection))
			for i, item := range frozen.Cohort.Targets {
				if current() != nil {
					return nil, clusterbootstrap.ErrPreparationConfig
				}
				request := clusterremote.FilesystemRequest{MachineID: item.Report.MachineID, BootID: item.Report.BootID, Paths: slices.Clone(selection[i].Paths), RequirePersistent: selection[i].RequirePersistent}
				input := item
				input.Key.PublicKey = bytes.Clone(item.Key.PublicKey)
				input.Report.Nodes = slices.Clone(item.Report.Nodes)
				started := time.Now()
				observation, err := readers.Filesystem(ctx, input, request)
				if err != nil || current() != nil {
					return nil, clusterbootstrap.ErrPreparationConfig
				}
				// Independent request copy prevents a reader from rewriting the
				// selected path set before its returned proof is checked.
				request.Paths = slices.Clone(selection[i].Paths)
				storage, err := observation.Evidence(started, clusterremote.Target{Address: item.Binding.Address, Port: item.Binding.Port}, item.Key, request)
				if err != nil {
					return nil, clusterbootstrap.ErrPreparationConfig
				}
				values = append(values, clusterSSHTargetFilesystemEvidence{TargetID: item.Binding.TargetID, Storage: storage})
			}
			return values, current()
		}
		first, err := read()
		if err != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		second, err := read()
		if err != nil || !mergeClusterSSHFilesystemAvailability(first, second, false) {
			return clusterbootstrap.ErrPreparationConfig
		}
		out := slices.Clone(first)
		for i := range out {
			out[i].Storage = out[i].Storage.Clone()
		}
		if current() != nil || consume(ctx, out) != nil || current() != nil {
			return clusterbootstrap.ErrPreparationConfig
		}
		final, err := read()
		if err != nil || !mergeClusterSSHFilesystemAvailability(first, final, true) {
			return clusterbootstrap.ErrPreparationConfig
		}
		return current()
	})
}

func mergeClusterSSHFilesystemAvailability(first, next []clusterSSHTargetFilesystemEvidence, final bool) bool {
	if len(first) != len(next) {
		return false
	}
	for i := range first {
		left, right := first[i].Storage.Clone(), next[i].Storage.Clone()
		if first[i].TargetID != next[i].TargetID || len(left.Filesystems) != len(right.Filesystems) {
			return false
		}
		for j := range left.Filesystems {
			if final && (right.Filesystems[j].AvailableBytes < left.Filesystems[j].AvailableBytes || right.Filesystems[j].AvailableInodes < left.Filesystems[j].AvailableInodes) {
				return false
			}
			left.Filesystems[j].AvailableBytes = min(left.Filesystems[j].AvailableBytes, right.Filesystems[j].AvailableBytes)
			right.Filesystems[j].AvailableBytes = left.Filesystems[j].AvailableBytes
			left.Filesystems[j].AvailableInodes = min(left.Filesystems[j].AvailableInodes, right.Filesystems[j].AvailableInodes)
			right.Filesystems[j].AvailableInodes = left.Filesystems[j].AvailableInodes
		}
		if !reflect.DeepEqual(left, right) {
			return false
		}
		first[i].Storage = left
	}
	return true
}
