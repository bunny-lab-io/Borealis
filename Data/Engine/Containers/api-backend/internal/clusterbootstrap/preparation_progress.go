package clusterbootstrap

// PreparationProgress identifies only the last retained pre-join journal step.
// It is NOT proof of a completed effect. A resumed dispatcher must first run
// that step's separate reconciliation, which verifies its entire prerequisite
// chain against current source/configuration/identity and actual host state.
// Empty permits only the existing clean-destination source publication path.
// source_absent requires renewed absence proof before source publication and is
// returned only for a fresh generation. Failed inspection is never empty.
type PreparationProgress struct {
	Step string `json:"step"`
}

func (p PreparationProgress) Validate() error {
	switch p.Step {
	case "empty", "source_absent", "stage_source", "prepare_runtime_identity", "prepare_identity_root", "install_identity", "prepare_host", "stage_images", "install_k3s":
		return nil
	default:
		return ErrSessionProtocol
	}
}
