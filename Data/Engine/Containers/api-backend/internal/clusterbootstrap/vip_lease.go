package clusterbootstrap

import (
	"encoding/json"
	"strings"
	"time"
)

const VIPLeasePath = "/apis/coordination.k8s.io/v1/namespaces/kube-system/leases/borealis-cluster-vip"

// VIPLease is a bounded public projection, not a fencing token. client-go's
// remote clock is not a local expiry clock. Consumers must observe renewal
// changes locally and also prove actual address ownership on every source.
type VIPLease struct {
	UID             string
	ResourceVersion string
	Holder          string
	AcquireTime     string
	RenewTime       string
	Transitions     int64
	DurationSeconds int64
}

func vipLeaseTime(value string) (time.Time, error) {
	if len(value) < 20 || len(value) > 32 || !strings.HasSuffix(value, "Z") {
		return time.Time{}, ErrPreparationConfig
	}
	v, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || v.IsZero() {
		return time.Time{}, ErrPreparationConfig
	}
	return v, nil
}

func (v VIPLease) Validate() error {
	acquired, err := vipLeaseTime(v.AcquireTime)
	renewed, other := vipLeaseTime(v.RenewTime)
	if !nonzeroPreparationUUID(v.UID) || !sessionHostname.MatchString(v.Holder) || v.DurationSeconds != 10 || v.Transitions < 0 || v.Transitions > 2147483647 ||
		err != nil || other != nil || renewed.Before(acquired) || len(v.ResourceVersion) == 0 || len(v.ResourceVersion) > 128 {
		return ErrPreparationConfig
	}
	// Kubernetes resourceVersion is opaque. Do not parse or order it as an int.
	for _, b := range []byte(v.ResourceVersion) {
		if b < 33 || b > 126 {
			return ErrPreparationConfig
		}
	}
	return nil
}

func (v VIPLease) SameEpoch(other VIPLease) bool {
	return v.Validate() == nil && other.Validate() == nil && v.UID == other.UID && v.Holder == other.Holder && v.AcquireTime == other.AcquireTime &&
		v.Transitions == other.Transitions && v.DurationSeconds == other.DurationSeconds
}

func ParseVIPLease(raw []byte) (VIPLease, error) {
	fail := func() (VIPLease, error) { return VIPLease{}, ErrPreparationConfig }
	object, err := sourceJSONObject(raw, 64<<10)
	if err != nil || len(object) != 4 {
		return fail()
	}
	var kind, api string
	if json.Unmarshal(object["kind"], &kind) != nil || kind != "Lease" || json.Unmarshal(object["apiVersion"], &api) != nil || api != "coordination.k8s.io/v1" {
		return fail()
	}
	metadata, err := sourceJSONObject(object["metadata"], 64<<10)
	if err != nil {
		return fail()
	}
	var name, namespace string
	var v VIPLease
	if json.Unmarshal(metadata["name"], &name) != nil || name != "borealis-cluster-vip" || json.Unmarshal(metadata["namespace"], &namespace) != nil || namespace != "kube-system" ||
		json.Unmarshal(metadata["uid"], &v.UID) != nil || json.Unmarshal(metadata["resourceVersion"], &v.ResourceVersion) != nil {
		return fail()
	}
	if deleted, ok := metadata["deletionTimestamp"]; ok && string(deleted) != "null" {
		return fail()
	}
	spec, err := sourceExactObject(object["spec"], "holderIdentity", "leaseDurationSeconds", "acquireTime", "renewTime", "leaseTransitions")
	for _, field := range spec {
		if string(field) == "null" {
			return fail()
		}
	}
	if err != nil || json.Unmarshal(spec["holderIdentity"], &v.Holder) != nil || json.Unmarshal(spec["leaseDurationSeconds"], &v.DurationSeconds) != nil ||
		json.Unmarshal(spec["acquireTime"], &v.AcquireTime) != nil || json.Unmarshal(spec["renewTime"], &v.RenewTime) != nil || json.Unmarshal(spec["leaseTransitions"], &v.Transitions) != nil || v.Validate() != nil {
		return fail()
	}
	return v, nil
}

// VIPLeaseProgress owns local elapsed-time evidence. First sighting establishes
// no live owner. Only a newly observed renewal within the same epoch can make
// it ready. Metadata-only updates cannot extend freshness; expiry is terminal.
type VIPLeaseProgress struct {
	current       VIPLease
	deadline      time.Time
	ready, failed bool
	versions      map[string]bool
	lastRead      time.Time
}

func (p *VIPLeaseProgress) Observe(value VIPLease, started, now time.Time) (bool, time.Time, error) {
	fail := func() (bool, time.Time, error) { p.failed = true; return false, time.Time{}, ErrPreparationConfig }
	if p.failed || value.Validate() != nil || started.IsZero() || now.Before(started) || started.Before(p.lastRead) || now.Sub(started) > time.Second ||
		(!p.deadline.IsZero() && !now.Before(p.deadline)) {
		return fail()
	}
	p.lastRead = started
	if p.versions == nil {
		p.versions = map[string]bool{value.ResourceVersion: true}
		p.current = value
		p.deadline = started.Add(5 * time.Second)
		return false, p.deadline, nil
	}
	if !value.SameEpoch(p.current) {
		return fail()
	}
	if value.ResourceVersion == p.current.ResourceVersion {
		if value != p.current {
			return fail()
		}
		return p.ready, p.deadline, nil
	}
	if p.versions[value.ResourceVersion] || len(p.versions) >= 128 {
		return fail()
	}
	p.versions[value.ResourceVersion] = true
	if value.RenewTime != p.current.RenewTime {
		before, _ := vipLeaseTime(p.current.RenewTime)
		after, _ := vipLeaseTime(value.RenewTime)
		// Absolute skew is allowed. Backward steps/replayed older renewals fail
		// this qualification attempt instead of extending a local lifetime.
		if !after.After(before) {
			return fail()
		}
		p.ready = true
		p.deadline = started.Add(5 * time.Second)
	}
	p.current = value
	return p.ready, p.deadline, nil
}
