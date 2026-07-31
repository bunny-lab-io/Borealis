package main

import "testing"

func TestAgentHeartbeatReleaseChannelInstructionWaitsThenFallsBackWhenBranchMissing(t *testing.T) {
	originalFetch := repoHashFetchHead
	repoHashFetchHead = func(repo string, branch string, token string) (string, string) {
		if repo != defaultAgentReleaseRepo || branch != "feature/closed-branch" {
			t.Fatalf("unexpected repo lookup repo=%s branch=%s", repo, branch)
		}
		return "", "GitHub REST API repo head lookup failed: HTTP 404"
	}
	t.Cleanup(func() {
		repoHashFetchHead = originalFetch
		resetAgentReleaseRecoveryState()
	})
	resetAgentReleaseRecoveryState()

	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	if instruction := agentHeartbeatReleaseChannelInstruction(guid, "unstable", "feature/closed-branch", 1000); instruction != nil {
		t.Fatalf("instruction before grace window = %+v", instruction)
	}
	if instruction := agentHeartbeatReleaseChannelInstruction(guid, "unstable", "feature/closed-branch", 1299); instruction != nil {
		t.Fatalf("instruction before grace window expiry = %+v", instruction)
	}
	instruction := agentHeartbeatReleaseChannelInstruction(guid, "unstable", "feature/closed-branch", 1300)
	if instruction == nil {
		t.Fatalf("expected stable fallback instruction after grace window")
	}
	if instruction["release_channel"] != "stable" || instruction["branch"] != "main" {
		t.Fatalf("unexpected instruction target %+v", instruction)
	}
	if instruction["kind"] != "branch_retired_fallback" || instruction["first_missing_at"] != int64(1000) {
		t.Fatalf("unexpected instruction metadata %+v", instruction)
	}
}

func TestAgentHeartbeatReleaseChannelInstructionPreservesResolvableBranch(t *testing.T) {
	originalFetch := repoHashFetchHead
	repoHashFetchHead = func(repo string, branch string, token string) (string, string) {
		return "abc123", ""
	}
	t.Cleanup(func() {
		repoHashFetchHead = originalFetch
		resetAgentReleaseRecoveryState()
	})
	resetAgentReleaseRecoveryState()

	instruction := agentHeartbeatReleaseChannelInstruction("2540DA38-E2B1-45B9-9113-BF7CF0E1778A", "unstable", "feature/live-branch", 1000)
	if instruction != nil {
		t.Fatalf("valid branch should not receive instruction: %+v", instruction)
	}
}

func TestAgentHeartbeatNoOverrideReleaseInstructionCorrectsStaleUnstableMain(t *testing.T) {
	instruction := agentHeartbeatNoOverrideReleaseInstruction(
		"2540DA38-E2B1-45B9-9113-BF7CF0E1778A",
		"",
		"unstable",
		"main",
		2000,
	)
	if instruction == nil {
		t.Fatalf("expected no-override unstable/main report to receive stable fallback")
	}
	if instruction["release_channel"] != "stable" || instruction["branch"] != "main" {
		t.Fatalf("unexpected instruction target %+v", instruction)
	}
	if instruction["reason"] != "device has no release-channel override; default is stable/main" {
		t.Fatalf("unexpected reason %+v", instruction)
	}

	if got := agentHeartbeatNoOverrideReleaseInstruction(
		"2540DA38-E2B1-45B9-9113-BF7CF0E1778A",
		"unstable",
		"unstable",
		"main",
		2000,
	); got != nil {
		t.Fatalf("explicit override should not be corrected by no-override path: %+v", got)
	}
}

func TestAgentHeartbeatReleaseChannelInstructionIgnoresAmbiguousLookupFailure(t *testing.T) {
	originalFetch := repoHashFetchHead
	repoHashFetchHead = func(repo string, branch string, token string) (string, string) {
		return "", "GitHub REST API repo head lookup raised: timeout"
	}
	t.Cleanup(func() {
		repoHashFetchHead = originalFetch
		resetAgentReleaseRecoveryState()
	})
	resetAgentReleaseRecoveryState()

	instruction := agentHeartbeatReleaseChannelInstruction("2540DA38-E2B1-45B9-9113-BF7CF0E1778A", "unstable", "feature/maybe-live", 2000)
	if instruction != nil {
		t.Fatalf("ambiguous lookup failure should not force fallback: %+v", instruction)
	}
}

func resetAgentReleaseRecoveryState() {
	agentReleaseRecoveryMu.Lock()
	defer agentReleaseRecoveryMu.Unlock()
	agentReleaseRecoveryState = map[string]agentReleaseRecoveryEntry{}
}
