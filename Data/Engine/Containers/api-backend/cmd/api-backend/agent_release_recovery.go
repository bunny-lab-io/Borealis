package main

import (
	"fmt"
	"strings"
	"sync"
)

const (
	agentReleaseBranchRecoveryGraceSeconds = int64(5 * 60)
	agentReleaseBranchCheckTTLSeconds      = int64(60)
)

var (
	agentReleaseRecoveryMu    sync.Mutex
	agentReleaseRecoveryState = map[string]agentReleaseRecoveryEntry{}
)

type agentReleaseRecoveryEntry struct {
	FirstMissingAt int64
	LastCheckedAt  int64
	LastError      string
}

func agentHeartbeatReleaseChannelInstruction(guid string, rawChannel string, rawBranch string, now int64) map[string]any {
	guid = firstText(normalizeCanonicalGUID(guid), cleanText(guid))
	channelText := strings.ToLower(cleanText(rawChannel))
	branch := normalizeAgentBranch(rawBranch)
	if guid == "" {
		return nil
	}
	normalizedChannel := normalizeAgentReleaseChannel(channelText, "")
	if channelText != "" && normalizedChannel == "" {
		firstMissingAt := agentReleaseTrackMissing("invalid-channel:"+guid+":"+channelText, now, "invalid release channel")
		if now-firstMissingAt < agentReleaseBranchRecoveryGraceSeconds {
			return nil
		}
		return stableMainAgentReleaseInstruction(guid, "reported release channel is no longer valid", firstMissingAt, now)
	}
	if normalizedChannel != "unstable" {
		clearAgentReleaseRecoveryState(guid)
		return nil
	}
	if branch == "" || strings.EqualFold(branch, defaultAgentReleaseBranch) {
		return nil
	}

	settings := collectAgentReleaseChannelSettings()
	repo := firstText(agentReleaseSettingsRepo(settings), defaultAgentReleaseRepo)
	missing, firstMissingAt, errText := agentReleaseBranchMissing(repo, branch, now)
	if !missing {
		return nil
	}
	if now-firstMissingAt < agentReleaseBranchRecoveryGraceSeconds {
		return nil
	}
	return stableMainAgentReleaseInstruction(guid, fmt.Sprintf("reported branch %q no longer resolves: %s", branch, firstText(errText, "repository head missing")), firstMissingAt, now)
}

func agentHeartbeatNoOverrideReleaseInstruction(guid string, override string, rawChannel string, rawBranch string, now int64) map[string]any {
	if normalizeAgentReleaseChannel(override, "") != "" {
		return nil
	}
	channel := normalizeAgentReleaseChannel(rawChannel, "")
	branch := normalizeAgentBranch(rawBranch)
	if channel == "" {
		return nil
	}
	if channel == defaultAgentReleaseChannel && strings.EqualFold(branch, defaultAgentReleaseBranch) {
		return nil
	}
	return stableMainAgentReleaseInstruction(guid, "device has no release-channel override; default is stable/main", now, now)
}

func agentReleaseTrackMissing(key string, now int64, errText string) int64 {
	agentReleaseRecoveryMu.Lock()
	defer agentReleaseRecoveryMu.Unlock()
	entry := agentReleaseRecoveryState[key]
	if entry.FirstMissingAt <= 0 {
		entry.FirstMissingAt = now
	}
	entry.LastCheckedAt = now
	entry.LastError = errText
	agentReleaseRecoveryState[key] = entry
	return entry.FirstMissingAt
}

func agentReleaseBranchMissing(repo string, branch string, now int64) (bool, int64, string) {
	repo = firstText(strings.TrimSpace(repo), defaultAgentReleaseRepo)
	branch = normalizeAgentBranch(branch)
	if branch == "" || strings.EqualFold(branch, defaultAgentReleaseBranch) {
		return false, 0, ""
	}
	key := "branch:" + repo + ":" + branch

	agentReleaseRecoveryMu.Lock()
	entry := agentReleaseRecoveryState[key]
	if entry.FirstMissingAt > 0 && now-entry.LastCheckedAt >= 0 && now-entry.LastCheckedAt < agentReleaseBranchCheckTTLSeconds {
		agentReleaseRecoveryMu.Unlock()
		return true, entry.FirstMissingAt, entry.LastError
	}
	agentReleaseRecoveryMu.Unlock()

	sha, errText := repoHashFetchHead(repo, branch, "")
	if strings.TrimSpace(sha) != "" {
		agentReleaseRecoveryMu.Lock()
		delete(agentReleaseRecoveryState, key)
		agentReleaseRecoveryMu.Unlock()
		return false, 0, ""
	}
	if !repoRefLookupMissing(errText) {
		return false, 0, errText
	}
	firstMissingAt := agentReleaseTrackMissing(key, now, errText)
	return true, firstMissingAt, errText
}

func repoRefLookupMissing(errText string) bool {
	text := strings.ToLower(strings.TrimSpace(errText))
	if text == "" {
		return false
	}
	for _, marker := range []string{
		"http 404",
		"http status 404",
		"statuscode=404",
		"404 not found",
		"not found",
		"http 422",
		"statuscode=422",
		"no commit found",
		"reference does not exist",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func stableMainAgentReleaseInstruction(guid string, reason string, firstMissingAt int64, now int64) map[string]any {
	return map[string]any{
		"operation_id":                  fmt.Sprintf("agent-branch-recovery-%s-%d", strings.ToLower(strings.ReplaceAll(guid, "-", "")), firstMissingAt),
		"kind":                          "branch_retired_fallback",
		"action":                        "branch_retired_fallback",
		"release_channel":               defaultAgentReleaseChannel,
		"channel":                       defaultAgentReleaseChannel,
		"target_channel":                defaultAgentReleaseChannel,
		"branch":                        defaultAgentReleaseBranch,
		"target_branch":                 defaultAgentReleaseBranch,
		"reason":                        reason,
		"first_missing_at":              firstMissingAt,
		"requested_at":                  now,
		"self_remediation_wait_seconds": agentReleaseBranchRecoveryGraceSeconds,
	}
}

func clearAgentReleaseRecoveryState(guid string) {
	guid = strings.ToLower(strings.ReplaceAll(firstText(normalizeCanonicalGUID(guid), cleanText(guid)), "-", ""))
	if guid == "" {
		return
	}
	agentReleaseRecoveryMu.Lock()
	defer agentReleaseRecoveryMu.Unlock()
	for key := range agentReleaseRecoveryState {
		normalizedKey := strings.ToLower(strings.ReplaceAll(key, "-", ""))
		if strings.Contains(normalizedKey, guid) {
			delete(agentReleaseRecoveryState, key)
		}
	}
}
