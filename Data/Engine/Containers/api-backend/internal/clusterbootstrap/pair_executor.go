package clusterbootstrap

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
)

// Only the separately built, temporary development executors carry this pin.
// The dev release already authenticates every catalog inventory, including the
// bootstrap archive. The replacement executable is never published into it.
var pairExecutorPin string

type PairBootHandoff struct {
	SourceUID string `json:"source_uid"`
	Before    string `json:"before"`
	After     string `json:"after"`
	Database  string `json:"database_system_id"`
	Journal   string `json:"retained_journal_sha256"`
}

type PairExecutorPin struct {
	Boot           PairBootHandoff `json:"source_boot"`
	Operation      string          `json:"operation"`
	Plan           string          `json:"plan"`
	Release        string          `json:"release"`
	Targets        [2]string       `json:"targets"`
	Generation     int64           `json:"generation"`
	CatalogManager string          `json:"catalog_manager"`
}

var installedPairExecutor = sync.OnceValues(func() (*PairExecutorPin, error) {
	if pairExecutorPin == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(pairExecutorPin)
	var p PairExecutorPin
	if err != nil || len(raw) > 1024 || json.Unmarshal(raw, &p) != nil || !sessionUUID.MatchString(p.Operation) ||
		!sessionUUID.MatchString(p.Targets[0]) || !sessionUUID.MatchString(p.Targets[1]) || p.Targets[0] == p.Targets[1] ||
		!digestPattern.MatchString(p.Plan) || !digestPattern.MatchString(p.CatalogManager) || !strings.HasPrefix(p.Release, "dev-") ||
		!digestPattern.MatchString(strings.TrimPrefix(p.Release, "dev-")) || p.Generation < 2 {
		return nil, ErrPreparationConfig
	}
	db, err := strconv.ParseUint(p.Boot.Database, 10, 64)
	if err != nil || db == 0 || !sessionUUID.MatchString(p.Boot.SourceUID) || !sessionUUID.MatchString(p.Boot.Before) || !sessionUUID.MatchString(p.Boot.After) || p.Boot.Before == p.Boot.After || !digestPattern.MatchString(p.Boot.Journal) {
		return nil, ErrPreparationConfig
	}
	return &p, nil
})

func IsPairExecutor() bool { return pairExecutorPin != "" }

func CheckPairExecutorPlan(operation, plan string) error {
	p, err := installedPairExecutor()
	if err != nil || p != nil && (p.Operation != operation || p.Plan != plan) {
		return ErrPreparationConfig
	}
	return nil
}

// CatalogManagerForExecution separates the supervised executable's identity
// from the immutable helper inside the original source archive. In a normal
// build the two must be identical.
func CatalogManagerForExecution(e PreparationExpected, executable string) (string, error) {
	p, err := installedPairExecutor()
	if err != nil || !digestPattern.MatchString(executable) {
		return "", ErrPreparationConfig
	}
	if p == nil {
		return executable, nil
	}
	if e.Validate() != nil || e.Source.Development == "" || e.Source.Release != p.Release || e.Target.OperationID != p.Operation ||
		e.Target.Generation != p.Generation || (e.Target.TargetID != p.Targets[0] && e.Target.TargetID != p.Targets[1]) {
		return "", ErrPreparationConfig
	}
	return p.CatalogManager, nil
}

// Recovery of this reserved pair starts with the first target's retained
// source and runtime account. A missing journal must never mean a fresh host.
func CheckPairExecutorCursor(targetID, step string) error {
	p, err := installedPairExecutor()
	if err != nil || p == nil {
		return err
	}
	if (targetID == p.Targets[0] && step == "prepare_runtime_identity") || (targetID == p.Targets[1] && step == "empty") {
		return nil
	}
	return ErrMutationUnknown
}

func RecoveryPair() (PairExecutorPin, error) {
	p, err := installedPairExecutor()
	if err != nil || p == nil {
		return PairExecutorPin{}, ErrPreparationConfig
	}
	return *p, nil
}
