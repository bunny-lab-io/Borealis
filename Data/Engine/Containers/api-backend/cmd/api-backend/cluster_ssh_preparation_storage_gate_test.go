package main

import (
	"borealis/api-backend/internal/clusterbootstrap"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func sshPreparationStorageGateFixture(t *testing.T, setup ...func(*sshWaitingCapacityFixture)) (*sshWaitingCapacityFixture, func(context.Context, string) (string, error), clusterSSHPreparationStorageGateSourceRead) {
	t.Helper()
	f := newSSHWaitingCapacityFixture(t, false, "absent")
	for _, configure := range setup {
		configure(f)
	}
	_, configuration := clusterSSHPreparationConfigurationProjectionReader(f.snapshot.work, f.check, f.get,
		func(ctx context.Context, member clusterSSHSourceMember) (clusterbootstrap.SourceNetwork, error) {
			f.networkReads++
			return sshSourceNetworkFixture(member, clusterbootstrap.K3sPins().Version), ctx.Err()
		})
	targets := func(ctx context.Context, plan string) (clusterSSHPreparationCleanObservation, error) {
		f.targetReads++
		digest, err := clusterSSHPreparationTargetCPUDigest(f.snapshot, f.targets)
		if plan != f.plan || ctx.Err() != nil {
			err = errClusterConflict
		}
		return clusterSSHPreparationCleanObservation{Digest: digest, Targets: f.targets, Packages: sshPreparationTargetPackagesFixture(f.targets)}, err
	}
	capacity, _, source := newClusterSSHPreparationCapacityStorageRead(f.snapshot, f.check, f.get, configuration, targets,
		func(ctx context.Context, window int64) ([]int64, error) { f.censusReads++; return f.sites, ctx.Err() })
	return f, capacity, source
}

func TestClusterSSHPreparationStorageGateProposalReaderMinima(t *testing.T) {
	for _, mode := range []string{"fitting decrease", "byte shortage", "inode shortage", "mount drift", "demand drift", "body close failure"} {
		t.Run(mode, func(t *testing.T) {
			f, capacity, source := sshPreparationStorageGateFixture(t, func(f *sshWaitingCapacityFixture) {
				body := marshalClusterJSON(map[string]any{"request_id": newClusterUUID(), "plan_sha256": f.plan, "confirmation": "PREPARE ENGINE NODES", "source_id": f.snapshot.work.Proof.Source.Members[0].NodeID})
				intent, err := newClusterSSHPreparationConfirmation([]byte(body), "admin", time.Now().Unix(), f.snapshot.work.Authorization, f.snapshot.work.Proof.Source)
				if err != nil {
					t.Fatal(err)
				}
				f.snapshot.work.Payload = marshalClusterJSON(map[string]any{"ssh_preparation_request": intent, "source_k3s_version": clusterbootstrap.K3sPins().Version})
			})
			ctx := context.Background()
			if _, err := capacity(ctx, f.plan); err != nil {
				t.Fatal(err)
			}
			calls, closed := 0, 0
			var client *clusterSSHPreparationCleanBrokerClient
			client = sshPreparationCleanBrokerTestClient(t, bootstrapRoundTripper(func(request *http.Request) (*http.Response, error) {
				calls++
				raw, err := io.ReadAll(request.Body)
				if err != nil || request.Body.Close() != nil {
					return nil, errClusterConflict
				}
				var input clusterSSHPreparationCleanBrokerRequest
				if openClusterSSHVIP(client.requestCipher, clusterSSHPreparationCleanBrokerPath, raw, &input, clusterSSHPreparationCleanBrokerLimit) != nil || !slices.Equal(input.FilesystemPaths, []string{"/opt/Borealis", "/var/lib/longhorn"}) {
					return nil, errClusterConflict
				}
				binding, _ := clusterSSHPreparationDigest(input)
				response := clusterSSHPreparationCleanBrokerResponse{Version: clusterSSHPreparationCleanBrokerVersion, ID: input.ID, RequestSHA256: binding, Targets: slices.Clone(f.targets)}
				response.Packages = sshPreparationTargetPackagesFixture(response.Targets)
				response.Digest, _ = clusterSSHPreparationTargetCPUDigest(f.snapshot, response.Targets)
				for _, target := range f.targets {
					e := sshCapacityEvidence()
					e.MountNamespace, e.Receipt = 1234, strings.Repeat("c", 64)
					if calls > 1 {
						e.Filesystems[0].AvailableBytes -= 1 << 20
						e.Filesystems[0].AvailableInodes--
						switch mode {
						case "byte shortage":
							e.Filesystems[0].AvailableBytes = 1
						case "inode shortage":
							e.Filesystems[0].AvailableInodes = 10
						case "mount drift":
							e.Paths[0].MountID++
						}
					}
					if calls > 2 {
						e.Filesystems[0].AvailableBytes = 91 << 30
						e.Filesystems[0].AvailableInodes = 900000
					}
					response.Filesystems = append(response.Filesystems, clusterSSHTargetFilesystemEvidence{TargetID: target.TargetID, Storage: e})
				}
				raw, err = sealClusterSSHVIP(client.responseCipher, clusterSSHPreparationCleanBrokerPath, response, clusterSSHPreparationCleanBrokerResponseLimit)
				if err != nil {
					return nil, err
				}
				body := &sshPreparationCleanBrokerBody{Reader: bytes.NewReader(raw), close: func() error {
					closed++
					if calls > 1 && mode == "body close failure" {
						return errClusterConflict
					}
					return nil
				}}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{clusterSSHSourceBrokerMedia}}, Body: body, Request: request}, nil
			}))
			var observed clusterSSHPreparationCleanObservation
			var readErr error
			factory := func(paths []string) clusterSSHPreparationTargetsRead {
				read := client.preparationTargetsFilesystemRead(f.snapshot, f.check, paths)
				return func(ctx context.Context, plan string) (clusterSSHPreparationCleanObservation, error) {
					observed, readErr = read(ctx, plan)
					return observed, readErr
				}
			}
			complete := func(context.Context, string, clusterbootstrap.PreparationPlan) ([]clusterSSHFilesystemDemand, error) {
				bytes := uint64(8 << 30)
				if mode == "demand drift" && calls > 0 {
					bytes++
				}
				return []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: bytes, Entries: 10}}, nil
			}
			gate := newClusterSSHPreparationStorageRead(f.snapshot, f.check, source, complete, factory)
			first, err := gate(ctx, f.plan)
			if err != nil {
				t.Fatal("initial proposal-reader storage failed", err)
			}
			second, err := gate(ctx, f.plan)
			if mode == "fitting decrease" {
				if err != nil || first != second || readErr != nil || observed.Filesystems[0].Storage.Filesystems[0].AvailableBytes != (90<<30)-(1<<20) || observed.Filesystems[0].Storage.Filesystems[0].AvailableInodes != 799999 {
					t.Fatal("fresh fitting proposal minima rejected", err, readErr)
				}
				third, err := gate(ctx, f.plan)
				if err != nil || third != first || observed.Filesystems[0].Storage.Filesystems[0].AvailableBytes != (90<<30)-(1<<20) || observed.Filesystems[0].Storage.Filesystems[0].AvailableInodes != 799999 {
					t.Fatal("rebound discarded conservative minima", err)
				}
			} else {
				var blocked clusterSSHPreparationGateBlocked
				if second != "" || !errors.As(err, &blocked) || blocked.Gate != "persistent_storage" {
					t.Fatal("proposal reader/gate accepted failed fit or drift", err)
				}
				if (mode == "byte shortage" || mode == "inode shortage") && readErr != nil {
					t.Fatal("counter policy blocked before actual budget check", readErr)
				}
			}
			if calls != closed {
				t.Fatal("gate returned before broker response cleanup", calls, closed)
			}
		})
	}
}

func TestClusterSSHPreparationStorageSourceBorrowsVerifiedCapacity(t *testing.T) {
	ctx := context.Background()
	f, capacity, source := sshPreparationStorageGateFixture(t)
	if _, err := capacity(ctx, f.plan); err != nil {
		t.Fatal(err)
	}
	before := map[string]int{}
	for key, value := range f.storage.calls {
		before[key] = value
	}
	network, target, census := f.networkReads, f.targetReads, f.censusReads
	one, err := source(ctx, f.plan)
	if err != nil {
		t.Fatal(err)
	}
	want, err := one.clone()
	if err != nil {
		t.Fatal(err)
	}
	one.Storage.Requirements.Applications[0].MemoryLimitBytes++
	one.Configuration.Sources[0].PodCIDR = "10.99.0.0/16"
	one.Configuration.Applications[0].MemoryLimitBytes++
	if !validClusterSSHPreparationPackages(one.Targets.Targets, one.Targets.Packages) {
		t.Fatal("storage discarded package projection")
	}
	one.Targets.Packages[0].Selection.Missing[0] = "foreign"
	one.Targets.Targets[0].CPU.MemoryKiB++
	one.Targets.Capacity.Targets[0].Capacity.MemoryLimitBytes++
	two, err := source(ctx, f.plan)
	if err != nil || !reflect.DeepEqual(two, want) {
		t.Fatal("storage getter lent mutable retained state", err)
	}
	if !reflect.DeepEqual(before, f.storage.calls) || network != f.networkReads || target != f.targetReads || census != f.censusReads {
		t.Fatal("getter repeated source inventory or native reads")
	}
	f.lost = true
	if got, err := source(ctx, f.plan); err == nil || !reflect.DeepEqual(got, clusterSSHPreparationStorageGateSource{}) {
		t.Fatal("lost proposal returned source")
	}
	f.lost = false
	if _, err := source(ctx, f.plan); err == nil {
		t.Fatal("failed projection revived")
	}
	if _, err := capacity(ctx, f.plan); err == nil {
		t.Fatal("failed projection did not invalidate capacity")
	}
	other, _, early := sshPreparationStorageGateFixture(t)
	if _, err := early(ctx, other.plan); err == nil {
		t.Fatal("storage source exists before capacity proof")
	}
}

// Source objects use existing complete capacity readers. Filesystem and demand
// producers are explicit component doubles; no fixture claims complete native
// installation evidence or removes the production storage blocker.
func TestClusterSSHPreparationStorageGate(t *testing.T) {
	for _, mode := range []string{"valid", "missing demand", "unavailable family", "empty demand", "missing reader", "reader failure", "source unavailable", "configuration drift", "storage drift", "collector provenance", "CPU drift", "namespace drift", "foreign target", "missing filesystem", "temporary filesystem", "existing storage", "byte shortage", "inode shortage", "late authority", "cancel", "source changed after filesystem", "wrong plan"} {
		t.Run(mode, func(t *testing.T) {
			f, capacity, source := sshPreparationStorageGateFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if _, err := capacity(ctx, f.plan); err != nil {
				t.Fatal(err)
			}
			sourceCalls, nativeReads, factories := 0, 0, 0
			readSource := func(ctx context.Context, plan string) (clusterSSHPreparationStorageGateSource, error) {
				sourceCalls++
				v, err := source(ctx, plan)
				switch mode {
				case "source unavailable":
					return clusterSSHPreparationStorageGateSource{}, errClusterConflict
				case "configuration drift":
					v.Configuration.Digest = strings.Repeat("f", 64)
				case "storage drift":
					v.Storage.Observation = strings.Repeat("f", 64)
				case "collector provenance":
					v.Storage.Requirements.observation = strings.Repeat("f", 64)
				case "CPU drift":
					v.Targets.Targets[0].CPU.MemoryKiB++
				case "namespace drift":
					v.Configuration.Sources[0].NodeUID = "99999999-9999-4999-8999-999999999999"
				case "source changed after filesystem":
					if sourceCalls > 1 {
						v.Storage.Requirements.ArtifactReplicaBytes++
					}
				}
				return v, err
			}
			complete := clusterSSHPreparationCompleteStorageDemandRead(func(ctx context.Context, plan string, p clusterbootstrap.PreparationPlan) ([]clusterSSHFilesystemDemand, error) {
				if plan != f.plan || p.PlanSHA256 != f.plan || p.Source != f.snapshot.work.Baseline || len(p.TargetAddresses) != len(f.targets) {
					t.Fatal("demand not bound to current proposal")
				}
				if mode == "unavailable family" {
					return nil, errClusterConflict
				}
				if mode == "empty demand" {
					return nil, nil
				}
				return []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: 8 << 30, Entries: 10}}, ctx.Err()
			})
			if mode == "missing demand" {
				complete = nil
			}
			factory := func(paths []string) clusterSSHPreparationTargetsRead {
				factories++
				if !slices.Equal(paths, []string{"/opt/Borealis", "/var/lib/longhorn"}) {
					t.Fatal("wrong source-policy/demand destinations", paths)
				}
				if mode == "missing reader" {
					return nil
				}
				return func(ctx context.Context, plan string) (clusterSSHPreparationCleanObservation, error) {
					nativeReads++
					value, err := source(ctx, plan)
					if err != nil {
						return clusterSSHPreparationCleanObservation{}, err
					}
					v := value.Targets
					for _, target := range f.targets {
						e := sshCapacityEvidence()
						e.MountNamespace = 1234
						e.Receipt = strings.Repeat("c", 64)
						switch mode {
						case "temporary filesystem":
							e.Filesystems[0].Type = "tmpfs"
						case "existing storage":
							e.Paths[1].Ancestor = e.Paths[1].Path
						case "byte shortage":
							e.Filesystems[0].AvailableBytes = 1
						case "inode shortage":
							e.Filesystems[0].AvailableInodes = 10
						}
						v.Filesystems = append(v.Filesystems, clusterSSHTargetFilesystemEvidence{TargetID: target.TargetID, Storage: e})
					}
					switch mode {
					case "reader failure":
						return clusterSSHPreparationCleanObservation{}, errClusterConflict
					case "foreign target":
						v.Filesystems[0].TargetID = "99999999-9999-4999-8999-999999999999"
					case "missing filesystem":
						v.Filesystems = nil
					case "late authority":
						f.lost = true
					case "cancel":
						cancel()
					}
					return v, ctx.Err()
				}
			}
			gate := newClusterSSHPreparationStorageRead(f.snapshot, f.check, readSource, complete, factory)
			plan := f.plan
			if mode == "wrong plan" {
				plan = strings.Repeat("f", 64)
			}
			digest, err := gate(ctx, plan)
			if mode == "valid" {
				if err != nil || !validClusterSSHPreparationDigest(digest) {
					t.Fatal("supported component storage rejected", err)
				}
				again, err := gate(ctx, plan)
				if err != nil || again != digest || factories != 1 || nativeReads != 2 {
					t.Fatal("storage read reused stale evidence or rebuilt reader", err, factories, nativeReads)
				}
			} else {
				var blocked clusterSSHPreparationGateBlocked
				if digest != "" || !errors.As(err, &blocked) || blocked.Gate != "persistent_storage" {
					t.Fatal("unsafe storage produced gate proof", digest, err)
				}
				if next, err := gate(context.Background(), f.plan); next != "" || err == nil {
					t.Fatal("failed storage gate revived")
				}
			}
		})
	}
}

func TestClusterSSHPreparationStorageGateAvailabilityRereads(t *testing.T) {
	for _, mode := range []string{"more available", "less but fitting", "byte shortage", "inode shortage", "receipt", "namespace", "ancestor inode", "mount", "device", "filesystem type", "total bytes", "total inodes", "allocation", "demand"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f, capacity, source := sshPreparationStorageGateFixture(t)
			if _, err := capacity(ctx, f.plan); err != nil {
				t.Fatal(err)
			}
			reads := 0
			var borrowed []clusterSSHTargetFilesystemEvidence
			var before []clusterSSHTargetFilesystemEvidence
			factory := func([]string) clusterSSHPreparationTargetsRead {
				return func(ctx context.Context, plan string) (clusterSSHPreparationCleanObservation, error) {
					reads++
					input, err := source(ctx, plan)
					if err != nil {
						return clusterSSHPreparationCleanObservation{}, err
					}
					value := input.Targets
					for _, target := range f.targets {
						e := sshCapacityEvidence()
						e.MountNamespace, e.Receipt = 1234, strings.Repeat("c", 64)
						if reads > 1 {
							switch mode {
							case "more available":
								e.Filesystems[0].AvailableBytes += 1 << 20
								e.Filesystems[0].AvailableInodes += 10
							case "less but fitting":
								e.Filesystems[0].AvailableBytes -= 1 << 20
								e.Filesystems[0].AvailableInodes -= 10
							case "byte shortage":
								e.Filesystems[0].AvailableBytes = 1
							case "inode shortage":
								e.Filesystems[0].AvailableInodes = 10
							case "receipt":
								e.Receipt = strings.Repeat("d", 64)
							case "namespace":
								e.MountNamespace++
							case "ancestor inode":
								e.Paths[0].Inode++
							case "mount":
								e.Paths[0].MountID++
							case "device":
								e.Filesystems[0].Device = "8:9"
							case "filesystem type":
								e.Filesystems[0].Type = "xfs"
							case "total bytes":
								e.Filesystems[0].TotalBytes += 1 << 20
							case "total inodes":
								e.Filesystems[0].TotalInodes++
							case "allocation":
								e.Filesystems[0].AllocationUnit *= 2
							}
						}
						value.Filesystems = append(value.Filesystems, clusterSSHTargetFilesystemEvidence{TargetID: target.TargetID, Storage: e})
					}
					borrowed = value.Filesystems
					before = cloneClusterSSHPreparationFilesystems(borrowed)
					return value, nil
				}
			}
			complete := func(context.Context, string, clusterbootstrap.PreparationPlan) ([]clusterSSHFilesystemDemand, error) {
				bytes := uint64(8 << 30)
				if mode == "demand" && reads > 0 {
					bytes++
				}
				return []clusterSSHFilesystemDemand{{Path: "/opt/Borealis", Bytes: bytes, Entries: 10}}, nil
			}
			gate := newClusterSSHPreparationStorageRead(f.snapshot, f.check, source, complete, factory)
			first, err := gate(ctx, f.plan)
			if err != nil || !validClusterSSHPreparationDigest(first) {
				t.Fatal("initial fitting observation failed", err)
			}
			second, err := gate(ctx, f.plan)
			if mode == "more available" || mode == "less but fitting" {
				if err != nil || second != first || reads != 2 {
					t.Fatal("fresh fitting counters changed approval fingerprint", first, second, err, reads)
				}
			} else {
				var blocked clusterSSHPreparationGateBlocked
				if second != "" || !errors.As(err, &blocked) || blocked.Gate != "persistent_storage" {
					t.Fatal("changed identity/demand or insufficient counters passed", second, err)
				}
			}
			if !reflect.DeepEqual(borrowed, before) {
				t.Fatal("fingerprint altered actual filesystem evidence")
			}
		})
	}
}
