package clusterbootstrap

import "context"

// InspectPreparationProgress reads the retained journal under its host lock and
// current supervised authority. No mutation, absence resolution or settlement
// occurs. Opening the journal may already have durably adopted a newer owner;
// this method leaves every record/history entry unchanged.
func (p *PreparationInputs) InspectPreparationProgress(ctx context.Context, j *MutationJournal) (PreparationProgress, error) {
	if j == nil {
		return PreparationProgress{}, ErrMutationJournal
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	input, err := p.sourcePublicationBinding(j)
	if err != nil || p.Verify(ctx, p.configuration.wire.Expected, j.authority) != nil {
		return PreparationProgress{}, ErrSessionAuthority
	}
	return j.preparationProgressLocked(ctx, input)
}

func (j *MutationJournal) preparationProgressLocked(ctx context.Context, sourceInput string) (PreparationProgress, error) {
	invalid := PreparationProgress{}
	if j.authority(ctx) != nil || !digestPattern.MatchString(sourceInput) || !j.state.valid() {
		return invalid, ErrSessionAuthority
	}
	steps := [...]string{"stage_source", "prepare_runtime_identity", "prepare_identity_root", "install_identity", "prepare_host", "stage_images", "install_k3s", "configure_k3s"}
	if len(j.state.Steps) > len(steps) {
		return invalid, ErrMutationJournal
	}
	progress := PreparationProgress{Step: "empty"}
	var previous mutationRecord
	for i, step := range steps {
		record, exists := j.state.Steps[step]
		if i >= len(j.state.Steps) {
			if exists { // Holes and unrelated later phases cannot be resumed here.
				return invalid, ErrMutationJournal
			}
			continue
		}
		if !exists || record.Executor == j.executor ||
			i > 0 && (previous.State != "applied" || record.Binding.Generation < previous.Binding.Generation || record.Binding.OperationAttempt < previous.Binding.OperationAttempt) {
			return invalid, ErrMutationJournal
		}
		if record.State == "not_applied" {
			if i != 0 || len(j.state.Steps) != 1 || j.state.Owner.Generation <= record.Binding.Generation || record.ResultSHA256 != sourcePublicationAbsenceDigest(sourceInput) {
				return invalid, ErrMutationUnknown
			}
		} else if record.State != "applied" && record.State != "intent" {
			return invalid, ErrMutationUnknown
		}
		if i == 0 && (record.InputSHA256 != sourceInput || record.State == "applied" && record.ResultSHA256 != sourceInput) {
			return invalid, ErrMutationJournal
		}
		progress.Step, previous = step, record
		if record.State == "not_applied" {
			progress.Step = "source_absent"
		}
	}
	for _, prior := range j.state.History {
		current, exists := j.state.Steps[prior.Step]
		if !exists || prior.Record.Binding.Generation >= current.Binding.Generation || prior.Record.InputSHA256 != current.InputSHA256 || prior.Record.Executor == j.executor {
			return invalid, ErrMutationJournal
		}
	}
	// Stop proof surrounds the observation. Even applied labels cannot justify
	// skipping a live producer; a lost lock does not establish cgroup quiescence.
	if j.priorExecutorsQuiescent(ctx) != nil || j.authority(ctx) != nil || j.priorExecutorsQuiescent(ctx) != nil || j.authority(ctx) != nil {
		return invalid, ErrMutationUnknown
	}
	return progress, nil
}
