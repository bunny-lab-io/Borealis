package main

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testAuthToken = "eyJ1Ijoib3BlcmF0b3IiLCJyIjoiQWRtaW4iLCJ0cyI6MTcwMDAwMDAwMH0.ZVPxAA.T_nkD4f7np9iU74bxSttSuR_MoY"
const testCompressedAuthToken = ".eJyrVipVslJKySxKTS7JL6rUKy1OLVLSUSoCCoZCmCXFSlaG5gZQUAsAhqAOag.ZVPxAA.-Zu3AisDtRhgTd33co1kzyxIQqw"

type fakeOperatorStore struct {
	profiles             map[string]operatorProfile
	err                  error
	search               []deviceSearchMatch
	searchErr            error
	searchProfile        operatorProfile
	searchQuery          string
	devices              []map[string]any
	deviceErr            error
	deviceProfile        operatorProfile
	deviceFilter         deviceListFilter
	deviceDetail         map[string]any
	deviceDetailStatus   int
	deviceDetailErr      error
	deviceDetailGUID     string
	deviceDetailHost     string
	deviceDescHost       string
	deviceDescText       string
	deviceMutation       map[string]any
	deviceMutationCode   int
	deviceMutationErr    error
	releaseGUID          string
	releaseChannel       any
	releaseBranch        any
	sites                []map[string]any
	siteErr              error
	siteProfile          operatorProfile
	siteMutationPayload  map[string]any
	siteMutationStatus   int
	siteCreatedName      string
	siteCreatedDesc      string
	siteDeletedIDs       []int64
	siteAssignedID       int64
	siteAssignedHosts    []string
	siteRenamedID        int64
	siteRenamedName      string
	siteAutoID           int64
	siteAutoUntil        *int64
	siteMap              map[string]map[string]any
	siteMapErr           error
	siteMapHostnames     []string
	users                []map[string]any
	userErr              error
	passkeys             []map[string]any
	passkeyErr           error
	passkeyUsername      string
	directoryProviders   []map[string]any
	directorySites       []map[string]any
	directoryErr         error
	credentials          []map[string]any
	credentialByID       map[int64]map[string]any
	credentialIDSeen     int64
	credentialErr        error
	enrollmentCodes      []map[string]any
	approvals            []map[string]any
	approvalErr          error
	approvalProfile      operatorProfile
	approvalStatusID     string
	approvalStatus       string
	approvalGUID         string
	approvalResolution   string
	approvalPayload      map[string]any
	approvalHTTPStatus   int
	views                []map[string]any
	viewErr              error
	viewByID             map[int64]map[string]any
	viewIDSeen           int64
	createdView          deviceViewMutation
	updatedView          deviceViewMutation
	deletedViewID        int64
	viewMutationStatus   int
	metadataFields       []map[string]any
	metadataErr          error
	metadataUpdateField  int
	metadataUpdateDesc   string
	metadataUpdateActor  string
	metadataUpdate       map[string]any
	metadataUpdateCode   int
	metadataUpdateErr    error
	deviceMetadata       map[string]any
	deviceMetaStatus     int
	deviceMetaErr        error
	deviceMetaID         string
	deviceMetaProfile    operatorProfile
	deviceMetaField      int
	deviceMetaValue      string
	deviceMetaUpdate     map[string]any
	deviceMetaUpdateCode int
	deviceMetaUpdateErr  error
	serverWorkers        map[string]any
	serverWorkerErr      error
	workerHistory        int
	workerContainers     bool
	githubToken          map[string]any
	tokenRefreshReq      agentTokenRefreshRequest
	tokenRefreshResult   agentTokenRefreshResult
	tokenRefreshStatus   int
	tokenRefreshErr      error
	deviceAuthRecord     deviceBearerAuthRecord
	deviceAuthFound      bool
	requiredVersion      *int
	agentHashLookupGUID  string
	agentHashLookupID    string
	agentHashLookup      map[string]any
	agentHashUpdateReq   agentHashUpdateRequest
	agentHashUpdate      map[string]any
	agentHashStatus      int
	agentHashErr         error
	agentHashList        []map[string]any
	remoteOpsReq         remoteOpsSessionRequest
	remoteOpsProfile     operatorProfile
	remoteOpsResult      remoteOpsSessionResult
	remoteOpsStatus      int
	remoteOpsErr         error
}

func (s *fakeOperatorStore) lookupOperator(_ context.Context, username string, fallbackRole string) (operatorProfile, error) {
	if s.err != nil {
		return operatorProfile{}, s.err
	}
	profile, ok := s.profiles[strings.ToLower(username)]
	if !ok {
		return operatorProfile{}, errOperatorNotFound
	}
	if profile.Role == "" {
		profile.Role = fallbackRole
	}
	return profile, nil
}

func (s *fakeOperatorStore) searchDevicesByHostname(_ context.Context, profile operatorProfile, query string) ([]deviceSearchMatch, error) {
	s.searchProfile = profile
	s.searchQuery = query
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	matches := append([]deviceSearchMatch(nil), s.search...)
	sortDeviceSearchMatches(matches, query)
	return matches, nil
}

func (s *fakeOperatorStore) listDevices(_ context.Context, profile operatorProfile, filter deviceListFilter) ([]map[string]any, error) {
	s.deviceProfile = profile
	s.deviceFilter = filter
	if s.deviceErr != nil {
		return nil, s.deviceErr
	}
	devices := make([]map[string]any, 0, len(s.devices))
	for _, device := range s.devices {
		copyDevice := make(map[string]any, len(device))
		for key, value := range device {
			copyDevice[key] = value
		}
		devices = append(devices, copyDevice)
	}
	return devices, nil
}

func (s *fakeOperatorStore) getDeviceByGUID(_ context.Context, profile operatorProfile, guid string) (map[string]any, int, error) {
	s.deviceProfile = profile
	s.deviceDetailGUID = guid
	if s.deviceDetailErr != nil {
		return nil, 0, s.deviceDetailErr
	}
	status := s.deviceDetailStatus
	if status == 0 {
		status = http.StatusOK
	}
	return copyMap(s.deviceDetail), status, nil
}

func (s *fakeOperatorStore) getDeviceDetails(_ context.Context, profile operatorProfile, hostname string) (map[string]any, int, error) {
	s.deviceProfile = profile
	s.deviceDetailHost = hostname
	if s.deviceDetailErr != nil {
		return nil, 0, s.deviceDetailErr
	}
	status := s.deviceDetailStatus
	if status == 0 {
		status = http.StatusOK
	}
	return copyMap(s.deviceDetail), status, nil
}

func (s *fakeOperatorStore) setDeviceDescription(_ context.Context, profile operatorProfile, hostname string, description string) (map[string]any, int, error) {
	s.deviceProfile = profile
	s.deviceDescHost = hostname
	s.deviceDescText = description
	if s.deviceMutationErr != nil {
		return nil, 0, s.deviceMutationErr
	}
	status := s.deviceMutationCode
	if status == 0 {
		status = http.StatusOK
	}
	payload := s.deviceMutation
	if payload == nil {
		payload = map[string]any{"status": "ok"}
	}
	return copyMap(payload), status, nil
}

func (s *fakeOperatorStore) setAgentReleaseChannelOverride(_ context.Context, guid string, channel any, branch any) (map[string]any, int, error) {
	s.releaseGUID = guid
	s.releaseChannel = channel
	s.releaseBranch = branch
	if s.deviceMutationErr != nil {
		return nil, 0, s.deviceMutationErr
	}
	status := s.deviceMutationCode
	if status == 0 {
		status = http.StatusOK
	}
	payload := s.deviceMutation
	if payload == nil {
		payload = map[string]any{
			"status":                          "ok",
			"guid":                            guid,
			"agent_release_channel":           cleanText(channel),
			"agent_release_channel_effective": cleanText(channel),
			"agent_release_channel_override":  cleanText(channel),
			"agent_branch":                    cleanText(branch),
			"agent_target_build_id":           "",
			"agent_target_published_at":       "",
		}
	}
	return copyMap(payload), status, nil
}

func (s *fakeOperatorStore) listSites(_ context.Context, profile operatorProfile) ([]map[string]any, error) {
	s.siteProfile = profile
	if s.siteErr != nil {
		return nil, s.siteErr
	}
	sites := make([]map[string]any, 0, len(s.sites))
	for _, site := range s.sites {
		copySite := make(map[string]any, len(site))
		for key, value := range site {
			copySite[key] = value
		}
		sites = append(sites, copySite)
	}
	return sites, nil
}

func (s *fakeOperatorStore) siteDeviceMap(_ context.Context, profile operatorProfile, hostnames []string) (map[string]map[string]any, error) {
	s.siteProfile = profile
	s.siteMapHostnames = append([]string(nil), hostnames...)
	if s.siteMapErr != nil {
		return nil, s.siteMapErr
	}
	mapping := make(map[string]map[string]any, len(s.siteMap))
	for hostname, site := range s.siteMap {
		copySite := make(map[string]any, len(site))
		for key, value := range site {
			copySite[key] = value
		}
		mapping[hostname] = copySite
	}
	return mapping, nil
}

func (s *fakeOperatorStore) createSite(_ context.Context, name string, description string) (map[string]any, int, error) {
	s.siteCreatedName = name
	s.siteCreatedDesc = description
	if s.siteErr != nil {
		return nil, 0, s.siteErr
	}
	status := s.siteMutationStatus
	if status == 0 {
		status = http.StatusCreated
	}
	if s.siteMutationPayload != nil {
		return copyMap(s.siteMutationPayload), status, nil
	}
	return map[string]any{"id": int64(99), "name": name, "description": description}, status, nil
}

func (s *fakeOperatorStore) deleteSites(_ context.Context, ids []int64) (map[string]any, int, error) {
	s.siteDeletedIDs = append([]int64(nil), ids...)
	if s.siteErr != nil {
		return nil, 0, s.siteErr
	}
	return map[string]any{"status": "ok", "deleted": int64(len(ids))}, http.StatusOK, nil
}

func (s *fakeOperatorStore) assignDevicesToSite(_ context.Context, siteID int64, hostnames []string) (map[string]any, int, error) {
	s.siteAssignedID = siteID
	s.siteAssignedHosts = append([]string(nil), hostnames...)
	if s.siteErr != nil {
		return nil, 0, s.siteErr
	}
	return map[string]any{"status": "ok"}, http.StatusOK, nil
}

func (s *fakeOperatorStore) renameSite(_ context.Context, siteID int64, newName string) (map[string]any, int, error) {
	s.siteRenamedID = siteID
	s.siteRenamedName = newName
	if s.siteErr != nil {
		return nil, 0, s.siteErr
	}
	if s.siteMutationPayload != nil {
		return copyMap(s.siteMutationPayload), http.StatusOK, nil
	}
	return map[string]any{"id": siteID, "name": newName}, http.StatusOK, nil
}

func (s *fakeOperatorStore) updateSiteAutoApproval(_ context.Context, siteID int64, autoApproveUntil *int64) (map[string]any, int, error) {
	s.siteAutoID = siteID
	s.siteAutoUntil = autoApproveUntil
	if s.siteErr != nil {
		return nil, 0, s.siteErr
	}
	until := int64(0)
	if autoApproveUntil != nil {
		until = *autoApproveUntil
	}
	return map[string]any{"id": siteID, "auto_approve_until": until}, http.StatusOK, nil
}

func (s *fakeOperatorStore) listUsers(_ context.Context) ([]map[string]any, error) {
	if s.userErr != nil {
		return nil, s.userErr
	}
	users := make([]map[string]any, 0, len(s.users))
	for _, user := range s.users {
		copyUser := make(map[string]any, len(user))
		for key, value := range user {
			copyUser[key] = value
		}
		users = append(users, copyUser)
	}
	return users, nil
}

func (s *fakeOperatorStore) listUserPasskeys(_ context.Context, username string) ([]map[string]any, error) {
	s.passkeyUsername = username
	if s.passkeyErr != nil {
		return nil, s.passkeyErr
	}
	passkeys := make([]map[string]any, 0, len(s.passkeys))
	for _, passkey := range s.passkeys {
		copyPasskey := make(map[string]any, len(passkey))
		for key, value := range passkey {
			copyPasskey[key] = value
		}
		passkeys = append(passkeys, copyPasskey)
	}
	return passkeys, nil
}

func (s *fakeOperatorStore) listDirectoryProviders(_ context.Context) ([]map[string]any, error) {
	if s.directoryErr != nil {
		return nil, s.directoryErr
	}
	providers := make([]map[string]any, 0, len(s.directoryProviders))
	for _, provider := range s.directoryProviders {
		providers = append(providers, copyMap(provider))
	}
	return providers, nil
}

func (s *fakeOperatorStore) listDirectorySites(_ context.Context) ([]map[string]any, error) {
	if s.directoryErr != nil {
		return nil, s.directoryErr
	}
	sites := make([]map[string]any, 0, len(s.directorySites))
	for _, site := range s.directorySites {
		sites = append(sites, copyMap(site))
	}
	return sites, nil
}

func (s *fakeOperatorStore) listCredentials(_ context.Context) ([]map[string]any, error) {
	if s.credentialErr != nil {
		return nil, s.credentialErr
	}
	credentials := make([]map[string]any, 0, len(s.credentials))
	for _, credential := range s.credentials {
		credentials = append(credentials, copyMap(credential))
	}
	return credentials, nil
}

func (s *fakeOperatorStore) getCredential(_ context.Context, credentialID int64) (map[string]any, bool, error) {
	s.credentialIDSeen = credentialID
	if s.credentialErr != nil {
		return nil, false, s.credentialErr
	}
	credential, ok := s.credentialByID[credentialID]
	if !ok {
		return nil, false, nil
	}
	return copyMap(credential), true, nil
}

func (s *fakeOperatorStore) listEnrollmentCodes(_ context.Context) ([]map[string]any, error) {
	if s.approvalErr != nil {
		return nil, s.approvalErr
	}
	codes := make([]map[string]any, 0, len(s.enrollmentCodes))
	for _, code := range s.enrollmentCodes {
		codes = append(codes, copyMap(code))
	}
	return codes, nil
}

func (s *fakeOperatorStore) listDeviceApprovals(_ context.Context, profile operatorProfile, _ string) ([]map[string]any, error) {
	s.approvalProfile = profile
	if s.approvalErr != nil {
		return nil, s.approvalErr
	}
	approvals := make([]map[string]any, 0, len(s.approvals))
	for _, approval := range s.approvals {
		approvals = append(approvals, copyMap(approval))
	}
	return approvals, nil
}

func (s *fakeOperatorStore) setDeviceApprovalStatus(_ context.Context, profile operatorProfile, approvalID string, status string, guid string, resolution string) (map[string]any, int, error) {
	s.approvalProfile = profile
	s.approvalStatusID = approvalID
	s.approvalStatus = status
	s.approvalGUID = guid
	s.approvalResolution = resolution
	if s.approvalErr != nil {
		return nil, 0, s.approvalErr
	}
	responseStatus := s.approvalHTTPStatus
	if responseStatus == 0 {
		responseStatus = http.StatusOK
	}
	if s.approvalPayload != nil {
		return copyMap(s.approvalPayload), responseStatus, nil
	}
	return map[string]any{"status": status}, responseStatus, nil
}

func (s *fakeOperatorStore) listDeviceViews(_ context.Context) ([]map[string]any, error) {
	if s.viewErr != nil {
		return nil, s.viewErr
	}
	views := make([]map[string]any, 0, len(s.views))
	for _, view := range s.views {
		copyView := make(map[string]any, len(view))
		for key, value := range view {
			copyView[key] = value
		}
		views = append(views, copyView)
	}
	return views, nil
}

func (s *fakeOperatorStore) getDeviceView(_ context.Context, viewID int64) (map[string]any, bool, error) {
	s.viewIDSeen = viewID
	if s.viewErr != nil {
		return nil, false, s.viewErr
	}
	view, ok := s.viewByID[viewID]
	if !ok {
		return nil, false, nil
	}
	copyView := make(map[string]any, len(view))
	for key, value := range view {
		copyView[key] = value
	}
	return copyView, true, nil
}

func (s *fakeOperatorStore) createDeviceView(_ context.Context, request deviceViewMutation) (map[string]any, int, error) {
	s.createdView = request
	if s.viewErr != nil {
		return nil, 0, s.viewErr
	}
	status := s.viewMutationStatus
	if status == 0 {
		status = http.StatusCreated
	}
	return map[string]any{
		"id":      int64(99),
		"name":    *request.Name,
		"columns": stringSliceToAny(request.Columns),
		"filters": request.Filters,
	}, status, nil
}

func (s *fakeOperatorStore) updateDeviceView(_ context.Context, viewID int64, request deviceViewMutation) (map[string]any, int, error) {
	s.viewIDSeen = viewID
	s.updatedView = request
	if s.viewErr != nil {
		return nil, 0, s.viewErr
	}
	name := "Updated View"
	if request.Name != nil {
		name = *request.Name
	}
	status := s.viewMutationStatus
	if status == 0 {
		status = http.StatusOK
	}
	return map[string]any{
		"id":      viewID,
		"name":    name,
		"columns": stringSliceToAny(request.Columns),
		"filters": request.Filters,
	}, status, nil
}

func (s *fakeOperatorStore) deleteDeviceView(_ context.Context, viewID int64) (map[string]any, int, error) {
	s.deletedViewID = viewID
	if s.viewErr != nil {
		return nil, 0, s.viewErr
	}
	status := s.viewMutationStatus
	if status == 0 {
		status = http.StatusOK
	}
	return map[string]any{"status": "ok"}, status, nil
}

func stringSliceToAny(values []string) []any {
	items := make([]any, 0, len(values))
	for _, value := range values {
		items = append(items, value)
	}
	return items
}

func (s *fakeOperatorStore) listMetadataDefinitions(_ context.Context) ([]map[string]any, error) {
	if s.metadataErr != nil {
		return nil, s.metadataErr
	}
	fields := make([]map[string]any, 0, len(s.metadataFields))
	for _, field := range s.metadataFields {
		copyField := make(map[string]any, len(field))
		for key, value := range field {
			copyField[key] = value
		}
		fields = append(fields, copyField)
	}
	return fields, nil
}

func (s *fakeOperatorStore) updateMetadataDefinition(_ context.Context, fieldNumber int, description string, actor string) (map[string]any, int, error) {
	s.metadataUpdateField = fieldNumber
	s.metadataUpdateDesc = description
	s.metadataUpdateActor = actor
	if s.metadataUpdateErr != nil {
		return nil, 0, s.metadataUpdateErr
	}
	status := s.metadataUpdateCode
	if status == 0 {
		status = http.StatusOK
	}
	if s.metadataUpdate != nil {
		return s.metadataUpdate, status, nil
	}
	return map[string]any{
		"field": map[string]any{
			"field_number": fieldNumber,
			"description":  description,
			"updated_by":   actor,
		},
	}, status, nil
}

func (s *fakeOperatorStore) deviceMetadataFields(_ context.Context, profile operatorProfile, deviceID string) (map[string]any, int, error) {
	s.deviceMetaProfile = profile
	s.deviceMetaID = deviceID
	if s.deviceMetaErr != nil {
		return nil, 0, s.deviceMetaErr
	}
	payload := make(map[string]any, len(s.deviceMetadata))
	for key, value := range s.deviceMetadata {
		payload[key] = value
	}
	status := s.deviceMetaStatus
	if status == 0 {
		status = http.StatusOK
	}
	return payload, status, nil
}

func (s *fakeOperatorStore) updateDeviceMetadataField(_ context.Context, profile operatorProfile, deviceID string, fieldNumber int, value string) (map[string]any, int, error) {
	s.deviceMetaProfile = profile
	s.deviceMetaID = deviceID
	s.deviceMetaField = fieldNumber
	s.deviceMetaValue = value
	if s.deviceMetaUpdateErr != nil {
		return nil, 0, s.deviceMetaUpdateErr
	}
	status := s.deviceMetaUpdateCode
	if status == 0 {
		status = http.StatusOK
	}
	if s.deviceMetaUpdate != nil {
		return s.deviceMetaUpdate, status, nil
	}
	return map[string]any{
		"field": map[string]any{
			"field_number": fieldNumber,
			"value":        value,
			"has_value":    value != "",
		},
	}, status, nil
}

func (s *fakeOperatorStore) serverWorkerPayload(_ context.Context, historySeconds int, includeContainerMetadata bool) (map[string]any, error) {
	s.workerHistory = historySeconds
	s.workerContainers = includeContainerMetadata
	if s.serverWorkerErr != nil {
		return nil, s.serverWorkerErr
	}
	payload := make(map[string]any, len(s.serverWorkers))
	for key, value := range s.serverWorkers {
		payload[key] = value
	}
	return payload, nil
}

func (s *fakeOperatorStore) githubTokenState(_ context.Context) map[string]any {
	if s.githubToken == nil {
		return defaultGithubTokenState()
	}
	payload := make(map[string]any, len(s.githubToken))
	for key, value := range s.githubToken {
		payload[key] = value
	}
	return payload
}

func (s *fakeOperatorStore) refreshAgentToken(_ context.Context, request agentTokenRefreshRequest) (agentTokenRefreshResult, int, error) {
	s.tokenRefreshReq = request
	if s.tokenRefreshErr != nil {
		status := s.tokenRefreshStatus
		if status == 0 {
			status = http.StatusUnauthorized
		}
		return agentTokenRefreshResult{}, status, s.tokenRefreshErr
	}
	status := s.tokenRefreshStatus
	if status == 0 {
		status = http.StatusOK
	}
	result := s.tokenRefreshResult
	if result.GUID == "" {
		result.GUID = request.GUID
	}
	if result.TokenVersion == 0 {
		result.TokenVersion = 1
	}
	return result, status, nil
}

func (s *fakeOperatorStore) requiredDeviceTokenVersion(_ context.Context, _ string) (*int, error) {
	return s.requiredVersion, nil
}

func (s *fakeOperatorStore) deviceBearerAuthRecord(_ context.Context, _ string) (deviceBearerAuthRecord, bool, error) {
	if !s.deviceAuthFound {
		return deviceBearerAuthRecord{}, false, nil
	}
	return s.deviceAuthRecord, true, nil
}

func (s *fakeOperatorStore) lookupAgentHash(_ context.Context, _ string, agentGUID string, agentID string) (map[string]any, int, error) {
	s.agentHashLookupGUID = agentGUID
	s.agentHashLookupID = agentID
	if s.agentHashErr != nil {
		status := s.agentHashStatus
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return nil, status, s.agentHashErr
	}
	status := s.agentHashStatus
	if status == 0 {
		status = http.StatusOK
	}
	return copyMap(s.agentHashLookup), status, nil
}

func (s *fakeOperatorStore) updateAgentHash(_ context.Context, _ string, request agentHashUpdateRequest) (map[string]any, int, error) {
	s.agentHashUpdateReq = request
	if s.agentHashErr != nil {
		status := s.agentHashStatus
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return nil, status, s.agentHashErr
	}
	status := s.agentHashStatus
	if status == 0 {
		status = http.StatusOK
	}
	return copyMap(s.agentHashUpdate), status, nil
}

func (s *fakeOperatorStore) listAgentHashes(_ context.Context) ([]map[string]any, error) {
	agents := make([]map[string]any, 0, len(s.agentHashList))
	for _, agent := range s.agentHashList {
		agents = append(agents, copyMap(agent))
	}
	return agents, nil
}

func (s *fakeOperatorStore) createRemoteOpsSession(_ context.Context, profile operatorProfile, request remoteOpsSessionRequest) (remoteOpsSessionResult, int, error) {
	s.remoteOpsProfile = profile
	s.remoteOpsReq = request
	if s.remoteOpsErr != nil {
		status := s.remoteOpsStatus
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return remoteOpsSessionResult{}, status, s.remoteOpsErr
	}
	status := s.remoteOpsStatus
	if status == 0 {
		status = http.StatusOK
	}
	return s.remoteOpsResult, status, nil
}

func testAuthService(profile operatorProfile) *authService {
	auth, _ := testAuthServiceWithStore(profile)
	return auth
}

func testAuthServiceWithStore(profile operatorProfile) (*authService, *fakeOperatorStore) {
	if profile.Username == "" {
		profile.Username = "operator"
	}
	if profile.DisplayName == "" {
		profile.DisplayName = profile.Username
	}
	if profile.Role == "" {
		profile.Role = "Admin"
	}
	if profile.AuthSource == "" {
		profile.AuthSource = "local"
	}
	store := &fakeOperatorStore{
		profiles: map[string]operatorProfile{
			strings.ToLower(profile.Username): profile,
		},
	}
	return &authService{
		verifier: &tokenVerifier{
			secret: []byte("test-secret"),
			maxAge: time.Hour,
			now:    func() time.Time { return time.Unix(1700000010, 0) },
		},
		store:   store,
		timeout: time.Second,
	}, store
}

func TestSetEnvReplacesExistingValue(t *testing.T) {
	env := setEnv([]string{"PATH=/bin", "BOREALIS_ENGINE_PORT=5000"}, "BOREALIS_ENGINE_PORT", "5001")
	got := strings.Join(env, "\n")
	if strings.Count(got, "BOREALIS_ENGINE_PORT=") != 1 {
		t.Fatalf("expected one BOREALIS_ENGINE_PORT entry, got %q", got)
	}
	if !strings.Contains(got, "BOREALIS_ENGINE_PORT=5001") {
		t.Fatalf("expected replaced port, got %q", got)
	}
}

func TestEnvDurationSecondsRejectsInvalidValues(t *testing.T) {
	t.Setenv("BOREALIS_TEST_DURATION", "-1")
	if got := envDurationSeconds("BOREALIS_TEST_DURATION", 3*time.Second); got != 3*time.Second {
		t.Fatalf("expected fallback for negative duration, got %s", got)
	}
	t.Setenv("BOREALIS_TEST_DURATION", "1.5")
	if got := envDurationSeconds("BOREALIS_TEST_DURATION", 3*time.Second); got != 1500*time.Millisecond {
		t.Fatalf("expected parsed duration, got %s", got)
	}
}

func TestHealthHandlerReportsHealthyLegacyBackend(t *testing.T) {
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("unexpected legacy path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer legacy.Close()

	legacyURL, err := url.Parse(legacy.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg := gatewayConfig{LegacyURL: legacyURL, HealthTimeout: time.Second}
	state := &legacyState{}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthHandler(cfg, state).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !state.snapshot().Healthy {
		t.Fatalf("expected state marked healthy")
	}
}

func TestHealthHandlerReportsUnhealthyLegacyBackend(t *testing.T) {
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer legacy.Close()

	legacyURL, err := url.Parse(legacy.URL)
	if err != nil {
		t.Fatal(err)
	}
	cfg := gatewayConfig{LegacyURL: legacyURL, HealthTimeout: time.Second}
	state := &legacyState{}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthHandler(cfg, state).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if state.snapshot().Healthy {
		t.Fatalf("expected state marked unhealthy")
	}
}

func TestTokenVerifierAcceptsItsDangerousFixture(t *testing.T) {
	verifier := &tokenVerifier{
		secret: []byte("test-secret"),
		maxAge: time.Hour,
		now:    func() time.Time { return time.Unix(1700000010, 0) },
	}

	identity, err := verifier.verify(testAuthToken)
	if err != nil {
		t.Fatalf("expected valid token, got %v", err)
	}
	if identity.Username != "operator" || identity.Role != "Admin" {
		t.Fatalf("unexpected identity %+v", identity)
	}

	identity, err = verifier.verify(testCompressedAuthToken)
	if err != nil {
		t.Fatalf("expected compressed token valid, got %v", err)
	}
	if identity.Username != "directory.user" || identity.Role != "User" {
		t.Fatalf("unexpected compressed identity %+v", identity)
	}
}

func TestAuthMeHandlerReturnsOperatorProfile(t *testing.T) {
	auth := testAuthService(operatorProfile{
		Username:            "operator",
		DisplayName:         "Operator",
		Role:                "Admin",
		MFAEnabled:          true,
		PasskeyCount:        2,
		AuthSource:          "local",
		DirectoryProviderID: 0,
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	authMeHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["username"] != "operator" || payload["role"] != "Admin" || payload["passkey_count"].(float64) != 2 {
		t.Fatalf("unexpected /api/auth/me payload %+v", payload)
	}
}

func TestAuthPasskeysHandlerRequiresAuthentication(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/auth/passkeys", nil)
	authPasskeysHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAuthPasskeysHandlerReturnsCurrentUserPasskeys(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.passkeys = []map[string]any{
		{
			"id":           int64(3),
			"label":        "YubiKey",
			"created_at":   int64(1700000000),
			"last_used_at": int64(1700000100),
			"transports":   []string{"usb"},
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/auth/passkeys", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	authPasskeysHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Status       string           `json:"status"`
		Passkeys     []map[string]any `json:"passkeys"`
		PasskeyCount int              `json:"passkey_count"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Status != "ok" || payload.PasskeyCount != 1 || len(payload.Passkeys) != 1 || payload.Passkeys[0]["label"] != "YubiKey" {
		t.Fatalf("unexpected passkey payload %+v", payload)
	}
	if store.passkeyUsername != "operator" {
		t.Fatalf("expected operator passkey lookup, got %q", store.passkeyUsername)
	}
}

func TestAgentTokenRefreshHandlerReturnsAccessTokenAndWorkerRoute(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	siteID := int64(7)
	store.tokenRefreshResult = agentTokenRefreshResult{
		GUID:         "2540DA38-E2B1-45B9-9113-BF7CF0E1778A",
		Fingerprint:  "fingerprint",
		TokenVersion: 3,
		SiteID:       &siteID,
		Route: &agentWorkerRoute{
			WorkerGUID:      "worker-1",
			SiteID:          7,
			RoutePathPrefix: "/_borealis/site-workers/worker-1",
			Generation:      4,
		},
	}
	signer := testAgentJWTSigner(t)
	signer.now = func() time.Time { return time.Unix(1700000000, 0) }

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/agent/token/refresh",
		strings.NewReader(`{"guid":"2540DA38-E2B1-45B9-9113-BF7CF0E1778A","refresh_token":"refresh-secret"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "borealis.example.test")
	agentTokenRefreshHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.tokenRefreshReq.GUID != "2540DA38-E2B1-45B9-9113-BF7CF0E1778A" || store.tokenRefreshReq.RefreshToken != "refresh-secret" {
		t.Fatalf("unexpected token refresh request %+v", store.tokenRefreshReq)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["token_type"] != "Bearer" || payload["expires_in"].(float64) != 900 || payload["access_token"] == "" {
		t.Fatalf("unexpected token payload %+v", payload)
	}
	remoteOps := payload["remote_ops_route"].(map[string]any)
	if remoteOps["available"] != true || remoteOps["socket_url"] != "https://borealis.example.test/_borealis/site-workers/worker-1/socket.io/" {
		t.Fatalf("unexpected remote ops payload %+v", remoteOps)
	}
}

func TestAgentTokenRefreshHandlerRejectsInvalidRequest(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/agent/token/refresh", strings.NewReader(`{"guid":""}`))

	agentTokenRefreshHandler(auth, testAgentJWTSigner(t), &dpopVerifier{seenJTI: map[string]time.Time{}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAgentHashHandlerUpdatesAuthenticatedDeviceHash(t *testing.T) {
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"
	signer := testAgentJWTSigner(t)
	signer.now = func() time.Time { return time.Unix(1700000000, 0) }
	token, err := signer.issueAccessToken(guid, "fingerprint", 3, agentAccessTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.deviceAuthFound = true
	store.deviceAuthRecord = deviceBearerAuthRecord{
		GUID:         guid,
		Fingerprint:  "fingerprint",
		TokenVersion: 3,
		Status:       "active",
	}
	store.agentHashUpdate = map[string]any{
		"status":     "ok",
		"agent_hash": "build-123",
		"agent_guid": guid,
		"agent_id":   "LAB-OPERATOR-01_SYSTEM",
		"hostname":   "LAB-OPERATOR-01",
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/agent/hash",
		strings.NewReader(`{"agent_hash":"build-123","agent_id":"LAB-OPERATOR-01_SYSTEM","agent_guid":"2540DA38-E2B1-45B9-9113-BF7CF0E1778A"}`),
	)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")

	agentHashHandler(auth, signer, &dpopVerifier{seenJTI: map[string]time.Time{}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.agentHashUpdateReq.AgentHash != "build-123" || store.agentHashUpdateReq.AgentID != "LAB-OPERATOR-01_SYSTEM" || store.agentHashUpdateReq.AgentGUID != guid {
		t.Fatalf("unexpected update request %+v", store.agentHashUpdateReq)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "ok" || payload["agent_hash"] != "build-123" || payload["hostname"] != "LAB-OPERATOR-01" {
		t.Fatalf("unexpected agent hash payload %+v", payload)
	}
}

func TestDPoPVerifierRejectsReplay(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0)
	proof := testDPoPProof(t, privateKey, "POST", "https://borealis.example.test/api/agent/token/refresh", "jti-1", now)
	verifier := &dpopVerifier{
		seenJTI: map[string]time.Time{},
		now:     func() time.Time { return now },
	}
	if _, err := verifier.verify("POST", "https://borealis.example.test/api/agent/token/refresh", proof, now, ""); err != nil {
		t.Fatalf("expected first DPoP proof accepted, got %v", err)
	}
	if _, err := verifier.verify("POST", "https://borealis.example.test/api/agent/token/refresh", proof, now, ""); !errors.Is(err, errDPoPReplay) {
		t.Fatalf("expected replay error, got %v", err)
	}
}

func TestRemoteOpsSessionHandlerIssuesScopedToken(t *testing.T) {
	t.Setenv("BOREALIS_PUBLIC_BASE_URL", "https://borealis.example.test")
	auth, store := testAuthServiceWithStore(operatorProfile{ID: 7, Username: "operator", Role: "Admin"})
	siteID := int64(3)
	store.remoteOpsResult = remoteOpsSessionResult{
		Device: remoteOpsSessionDevice{
			GUID:     "00000000-0000-4000-8000-000000000123",
			Hostname: "LAB-OPERATOR-01",
			AgentID:  "LAB-OPERATOR-01_SYSTEM",
			SiteID:   &siteID,
			SiteName: "Bunny Lab",
		},
		Route: &agentWorkerRoute{
			WorkerGUID:      "worker-1",
			SiteID:          siteID,
			RoutePathPrefix: "/_borealis/site-workers/worker-1",
			Generation:      4,
		},
	}
	signer := testAgentJWTSigner(t)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/remote-ops/session", strings.NewReader(`{"hostname":"LAB-OPERATOR-01","capabilities":["shell","remote-desktop","shell"],"ttl_seconds":42}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	remoteOpsSessionHandler(auth, signer).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.remoteOpsReq.Hostname != "LAB-OPERATOR-01" {
		t.Fatalf("expected hostname capture, got %+v", store.remoteOpsReq)
	}
	if got := strings.Join(store.remoteOpsReq.Capabilities, ","); got != "remote_shell,remote_desktop" {
		t.Fatalf("unexpected capabilities %q", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	session := payload["session"].(map[string]any)
	if session["expires_in"] != float64(42) {
		t.Fatalf("expected ttl 42, got %+v", session)
	}
	worker := session["worker"].(map[string]any)
	urls := worker["urls"].(map[string]any)
	if got := urls["socket_io"]; got != "https://borealis.example.test/_borealis/site-workers/worker-1/socket.io/" {
		t.Fatalf("unexpected socket url %#v", got)
	}
	claims, err := signer.verifyAccessToken(cleanText(session["token"]))
	if err != nil {
		t.Fatalf("expected valid remote-op JWT, got %v", err)
	}
	if claims["typ"] != remoteOpSessionTokenType || claims["aud"] != remoteOpSessionAudience || claims["worker_guid"] != "worker-1" {
		t.Fatalf("unexpected claims %+v", claims)
	}
	caps := claims["capabilities"].([]any)
	if len(caps) != 2 || caps[0] != "remote_shell" || caps[1] != "remote_desktop" {
		t.Fatalf("unexpected token caps %+v", caps)
	}
}

func TestRemoteOpsSessionHandlerRejectsInvalidCapability(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/remote-ops/session", strings.NewReader(`{"hostname":"LAB","capability":"bogus"}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)

	remoteOpsSessionHandler(auth, testAgentJWTSigner(t)).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRemoteOpsSessionHandlerReportsMissingWorker(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	siteID := int64(3)
	store.remoteOpsResult = remoteOpsSessionResult{
		Device: remoteOpsSessionDevice{GUID: "00000000-0000-4000-8000-000000000123", Hostname: "LAB", SiteID: &siteID},
		Route:  nil,
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/remote-ops/session", strings.NewReader(`{"hostname":"LAB","capability":"shell"}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	remoteOpsSessionHandler(auth, testAgentJWTSigner(t)).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func testAgentJWTSigner(t *testing.T) *agentJWTSigner {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := newAgentJWTSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func testDPoPProof(t *testing.T, privateKey ed25519.PrivateKey, method string, htu string, jti string, issuedAt time.Time) string {
	t.Helper()
	publicKey := privateKey.Public().(ed25519.PublicKey)
	header := map[string]any{
		"alg": "EdDSA",
		"typ": "dpop+jwt",
		"jwk": map[string]any{
			"kty": "OKP",
			"crv": "Ed25519",
			"x":   base64.RawURLEncoding.EncodeToString(publicKey),
		},
	}
	claims := map[string]any{
		"htm": method,
		"htu": htu,
		"jti": jti,
		"iat": issuedAt.Unix(),
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	signature := ed25519.Sign(privateKey, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestDeviceSearchHandlerRequiresAuthentication(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices/search?hostname=lab", nil)
	deviceSearchHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeviceSearchHandlerReturnsEmptyForShortQueryAfterAuth(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices/search?hostname=la", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceSearchHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.searchQuery != "" {
		t.Fatalf("expected short query to skip DB search, got query %q", store.searchQuery)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["query"] != "la" || payload["count"].(float64) != 0 {
		t.Fatalf("unexpected short search payload %+v", payload)
	}
}

func TestDeviceSearchHandlerReturnsSortedMatches(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.search = []deviceSearchMatch{
		{AgentGUID: "CCCC", AgentID: "agent-c", Hostname: "z-lab", SiteName: "Zeta"},
		{AgentGUID: "BBBB", AgentID: "agent-b", Hostname: "lab-02", SiteName: "Beta"},
		{AgentGUID: "AAAA", AgentID: "agent-a", Hostname: "lab", SiteName: "Alpha"},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices/search?hostname=lab", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceSearchHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Devices []deviceSearchMatch `json:"devices"`
		Query   string              `json:"query"`
		Count   int                 `json:"count"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Query != "lab" || payload.Count != 3 {
		t.Fatalf("unexpected search payload %+v", payload)
	}
	if got := payload.Devices[0].Hostname; got != "lab" {
		t.Fatalf("expected exact hostname first, got %q", got)
	}
	if store.searchProfile.Username != "operator" || store.searchQuery != "lab" {
		t.Fatalf("expected search called with operator profile/query, got %+v %q", store.searchProfile, store.searchQuery)
	}
}

func TestDeviceListHandlerRequiresAuthentication(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	deviceListHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeviceListHandlerReturnsDevicesAndFilters(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.devices = []map[string]any{
		{
			"hostname":   "LAB-OPERATOR-01",
			"agent_guid": "2540DA38-E2B1-45B9-9113-BF7CF0E1778A",
			"status":     "online",
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices?only_agents=true&connection_type=ssh&hostname=lab-operator-01", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceListHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Devices []map[string]any `json:"devices"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Devices) != 1 || payload.Devices[0]["hostname"] != "LAB-OPERATOR-01" {
		t.Fatalf("unexpected device payload %+v", payload)
	}
	if !store.deviceFilter.OnlyAgents || store.deviceFilter.ConnectionType != "ssh" || store.deviceFilter.Hostname != "lab-operator-01" {
		t.Fatalf("unexpected device filters %+v", store.deviceFilter)
	}
	if store.deviceProfile.Username != "operator" {
		t.Fatalf("expected operator profile, got %+v", store.deviceProfile)
	}
}

func TestDeviceByGUIDHandlerReturnsDetail(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.deviceDetail = map[string]any{
		"hostname":   "LAB-OPERATOR-01",
		"agent_guid": "2540DA38-E2B1-45B9-9113-BF7CF0E1778A",
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices/2540DA38-E2B1-45B9-9113-BF7CF0E1778A", nil)
	request.SetPathValue("guid", "2540DA38-E2B1-45B9-9113-BF7CF0E1778A")
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceByGUIDHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.deviceDetailGUID != "2540DA38-E2B1-45B9-9113-BF7CF0E1778A" {
		t.Fatalf("unexpected guid %q", store.deviceDetailGUID)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["hostname"] != "LAB-OPERATOR-01" {
		t.Fatalf("unexpected detail payload %+v", payload)
	}
}

func TestDeviceByGUIDHandlerRejectsInvalidGUID(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices/not-a-guid", nil)
	request.SetPathValue("guid", "not-a-guid")
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceByGUIDHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeviceDetailsHandlerReturnsHostnameDetail(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.deviceDetail = map[string]any{
		"hostname": "LAB-OPERATOR-01",
		"summary":  map[string]any{"hostname": "LAB-OPERATOR-01"},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device/details/LAB-OPERATOR-01", nil)
	request.SetPathValue("hostname", "LAB-OPERATOR-01")
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceDetailsHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.deviceDetailHost != "LAB-OPERATOR-01" {
		t.Fatalf("unexpected hostname %q", store.deviceDetailHost)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["hostname"] != "LAB-OPERATOR-01" {
		t.Fatalf("unexpected detail payload %+v", payload)
	}
}

func TestDeviceDescriptionHandlerUpdatesDescription(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/device/description/LAB-OPERATOR-01", strings.NewReader(`{"description":" Updated workstation "}`))
	request.SetPathValue("hostname", "LAB-OPERATOR-01")
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceDescriptionHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.deviceDescHost != "LAB-OPERATOR-01" || store.deviceDescText != "Updated workstation" {
		t.Fatalf("unexpected description mutation host=%q description=%q", store.deviceDescHost, store.deviceDescText)
	}
	if store.deviceProfile.Username != "operator" {
		t.Fatalf("expected operator profile, got %+v", store.deviceProfile)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("unexpected payload %+v", payload)
	}
}

func TestDeviceAgentReleaseChannelHandlerUpdatesOverride(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	guid := "2540DA38-E2B1-45B9-9113-BF7CF0E1778A"

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/devices/"+guid+"/agent-release-channel", strings.NewReader(`{"channel":"source","branch":"feature/rewrite-api-backend-in-golang"}`))
	request.SetPathValue("guid", guid)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceAgentReleaseChannelHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.releaseGUID != guid || store.releaseChannel != "source" || store.releaseBranch != "feature/rewrite-api-backend-in-golang" {
		t.Fatalf("unexpected release mutation guid=%q channel=%#v branch=%#v", store.releaseGUID, store.releaseChannel, store.releaseBranch)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("unexpected payload %+v", payload)
	}
}

func TestDeviceAgentReleaseChannelHandlerRequiresAdmin(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Technician"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/devices/2540DA38-E2B1-45B9-9113-BF7CF0E1778A/agent-release-channel", strings.NewReader(`{"channel":"stable"}`))
	request.SetPathValue("guid", "2540DA38-E2B1-45B9-9113-BF7CF0E1778A")
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceAgentReleaseChannelHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestNormalizeAgentBranchMatchesPythonRules(t *testing.T) {
	if got := normalizeAgentBranch("feature/rewrite-api-backend-in-golang"); got != "feature/rewrite-api-backend-in-golang" {
		t.Fatalf("expected valid branch, got %q", got)
	}
	invalid := []any{"main branch", ".hidden", "main/", "main..next", "main//next", "main@{1", `main\next`, "https://repo"}
	for _, value := range invalid {
		if got := normalizeAgentBranch(value); got != "" {
			t.Fatalf("expected %q to be rejected, got %q", value, got)
		}
	}
}

func TestAttachAgentVersionStatusUsesReleaseChannelTarget(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "agent_release_channels.json")
	content := `{
		"default_channel": "stable",
		"channels": {
			"stable": {"channel": "stable", "build_id": "stable-build", "published_at": "2026-06-02T00:00:00Z"},
			"unstable": {"channel": "unstable", "build_id": "unstable-build"}
		}
	}`
	if err := os.WriteFile(settingsPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOREALIS_AGENT_RELEASE_CHANNELS_PATH", settingsPath)
	payload := map[string]any{
		"agent_build_id": "stable-build",
		"summary":        map[string]any{"agent_build_id": "stable-build"},
		"details":        map[string]any{"summary": map[string]any{"agent_build_id": "stable-build"}},
	}

	updated := attachAgentVersionStatus(payload)
	if got := updated["agent_version_status"]; got != "Up-to-Date" {
		t.Fatalf("expected Up-to-Date, got %#v", got)
	}
	if got := updated["agent_target_build_id"]; got != "stable-build" {
		t.Fatalf("expected stable target, got %#v", got)
	}
	summary := updated["summary"].(map[string]any)
	if got := summary["agent_release_channel_effective"]; got != "stable" {
		t.Fatalf("expected stable effective channel, got %#v", got)
	}
}

func TestAgentListHandlerReturnsLegacyMapping(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.devices = []map[string]any{
		{
			"hostname":            "LAB-OPERATOR-01",
			"agent_guid":          "2540DA38E2B145B99113BF7CF0E1778A",
			"agent_id":            "LAB-OPERATOR-01-svc",
			"agent_hash":          "build-a",
			"last_seen":           int64(1700000000),
			"status":              "Online",
			"connection_type":     "",
			"connection_endpoint": "",
			"device_type":         "Windows",
			"domain":              "BUNNY",
			"external_ip":         "203.0.113.10",
			"internal_ip":         "10.0.0.5",
			"last_reboot":         "2026-06-01T00:00:00Z",
			"last_user":           "bunny",
			"operating_system":    "Windows 11",
			"uptime":              int64(42),
			"site_id":             int64(1),
			"site_name":           "Bunny Lab",
			"site_description":    "Lab site",
		},
		{
			"hostname":   "LAB-OPERATOR-01",
			"agent_guid": "2540DA38E2B145B99113BF7CF0E1778A",
			"agent_id":   "LAB-OPERATOR-01-svc",
			"last_seen":  int64(1699999999),
			"status":     "Offline",
		},
		{
			"hostname":   "LAB-OPERATOR-01",
			"agent_guid": "2540DA38E2B145B99113BF7CF0E1778A",
			"agent_id":   "LAB-OPERATOR-01-user",
			"last_seen":  int64(1699999900),
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	agentListHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !store.deviceFilter.OnlyAgents {
		t.Fatalf("expected only_agents filter captured")
	}
	var payload map[string]map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	systemAgent := payload["LAB-OPERATOR-01-svc"]
	if systemAgent == nil {
		t.Fatalf("expected system agent key in %+v", payload)
	}
	if got := systemAgent["service_mode"]; got != "system" {
		t.Fatalf("expected system mode, got %#v", got)
	}
	if got := systemAgent["agent_guid"]; got != "2540DA38-E2B1-45B9-9113-BF7CF0E1778A" {
		t.Fatalf("expected normalized guid, got %#v", got)
	}
	if got := systemAgent["status"]; got != "Online" {
		t.Fatalf("expected newest system row selected, got %#v", got)
	}
	if got := systemAgent["site_name"]; got != "Bunny Lab" {
		t.Fatalf("expected site copied, got %#v", got)
	}
	userAgent := payload["LAB-OPERATOR-01-user"]
	if userAgent == nil {
		t.Fatalf("expected current-user agent key in %+v", payload)
	}
	if got := userAgent["service_mode"]; got != "currentuser" {
		t.Fatalf("expected currentuser mode, got %#v", got)
	}
}

func TestSiteListHandlerRequiresAuthentication(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	siteListHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSiteListHandlerReturnsSitesAndPublicMetadata(t *testing.T) {
	t.Setenv("BOREALIS_PUBLIC_BASE_URL", "https://borealis.example.test/")
	t.Setenv("BOREALIS_PUBLIC_HOSTNAME", "borealis.example.test")
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.sites = []map[string]any{
		{
			"id":                   1,
			"name":                 "Bunny Lab",
			"description":          "Lab site",
			"device_count":         21,
			"auto_approval_active": false,
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	siteListHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Sites          []map[string]any `json:"sites"`
		PublicBaseURL  string           `json:"public_base_url"`
		PublicHostname string           `json:"public_hostname"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Sites) != 1 || payload.Sites[0]["name"] != "Bunny Lab" {
		t.Fatalf("unexpected sites payload %+v", payload)
	}
	if payload.PublicBaseURL != "https://borealis.example.test" || payload.PublicHostname != "borealis.example.test" {
		t.Fatalf("unexpected public metadata %+v", payload)
	}
	if store.siteProfile.Username != "operator" {
		t.Fatalf("expected operator profile, got %+v", store.siteProfile)
	}
}

func TestSiteListHandlerCreatesSite(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/sites", strings.NewReader(`{"name":"New Site","description":"Fresh"}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	siteListHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.siteCreatedName != "New Site" || store.siteCreatedDesc != "Fresh" {
		t.Fatalf("unexpected site create request name=%q desc=%q", store.siteCreatedName, store.siteCreatedDesc)
	}
}

func TestSiteDeviceMapHandlerReturnsMapping(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.siteMap = map[string]map[string]any{
		"LAB-OPERATOR-01": {
			"site_id":   1,
			"site_name": "Bunny Lab",
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/sites/device_map?hostnames=LAB-OPERATOR-01,,LAB-OPERATOR-01", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	siteDeviceMapHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Mapping map[string]map[string]any `json:"mapping"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Mapping["LAB-OPERATOR-01"]["site_name"] != "Bunny Lab" {
		t.Fatalf("unexpected mapping %+v", payload.Mapping)
	}
	if len(store.siteMapHostnames) != 1 || store.siteMapHostnames[0] != "LAB-OPERATOR-01" {
		t.Fatalf("unexpected host filter %+v", store.siteMapHostnames)
	}
}

func TestSiteAssignHandlerAssignsDevices(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/sites/assign", strings.NewReader(`{"site_id":7,"hostnames":["LAB-01","LAB-02"]}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	siteAssignHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.siteAssignedID != 7 || len(store.siteAssignedHosts) != 2 || store.siteAssignedHosts[1] != "LAB-02" {
		t.Fatalf("unexpected assignment id=%d hosts=%+v", store.siteAssignedID, store.siteAssignedHosts)
	}
}

func TestSiteAutoApprovalHandlerUpdatesSite(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	until := time.Now().Unix() + 3600

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/sites/7/auto-approval", strings.NewReader(`{"auto_approve_until":`+strconv.FormatInt(until, 10)+`}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	request.SetPathValue("site_id", "7")
	siteAutoApprovalHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.siteAutoID != 7 || store.siteAutoUntil == nil || *store.siteAutoUntil != until {
		t.Fatalf("unexpected auto approval id=%d until=%v", store.siteAutoID, store.siteAutoUntil)
	}
}

func TestUsersHandlerRequiresAdmin(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "User"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	usersHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestUsersHandlerReturnsUsers(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.users = []map[string]any{
		{
			"id":                      int64(1),
			"username":                "administrator",
			"display_name":            "Administrator",
			"role":                    "Admin",
			"last_login":              int64(1700000000),
			"created_at":              int64(1699990000),
			"updated_at":              int64(1700001000),
			"mfa_enabled":             1,
			"auth_reset_required":     0,
			"auth_reset_at":           int64(0),
			"auth_source":             "local",
			"directory_provider_id":   nil,
			"directory_provider_name": "",
			"directory_domain":        "",
			"directory_disabled":      0,
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	usersHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Users) != 1 || payload.Users[0]["username"] != "administrator" {
		t.Fatalf("unexpected users payload %+v", payload)
	}
}

func TestDirectoryProvidersHandlerRequiresAdmin(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "User"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/directory/providers", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	directoryProvidersHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDirectoryProvidersHandlerReturnsProviders(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.directoryProviders = []map[string]any{
		{
			"id":                     int64(1),
			"name":                   "Bunny LDAP",
			"provider_type":          "ldap",
			"enabled":                true,
			"server_urls":            []string{"ldaps://ldap.example.test"},
			"bind_password_present":  true,
			"group_mappings":         []map[string]any{{"group_dn": "cn=admins,dc=example,dc=test", "role": "Admin"}},
			"site_mappings":          []map[string]any{{"id": int64(1), "label": "Lab", "site_ids": []int64{1}}},
			"sync_interval_seconds":  int64(60),
			"username_attribute":     "uid",
			"display_name_attribute": "displayName",
			"email_attribute":        "mail",
			"member_of_attribute":    "memberOf",
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/directory/providers", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	directoryProvidersHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Providers []map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Providers) != 1 || payload.Providers[0]["name"] != "Bunny LDAP" {
		t.Fatalf("unexpected directory providers payload %+v", payload)
	}
}

func TestCredentialsHandlerReturnsCredentials(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "User"})
	store.credentials = []map[string]any{
		{
			"id":                         int64(7),
			"name":                       "Lab SSH",
			"description":                "Bunny Lab SSH",
			"site_id":                    int64(1),
			"site_name":                  "Bunny Lab",
			"credential_type":            "machine",
			"connection_type":            "ssh",
			"username":                   "ops",
			"has_password":               true,
			"has_private_key":            true,
			"has_private_key_passphrase": false,
			"has_become_password":        false,
			"metadata": map[string]any{
				"winrm_transport": "ntlm",
			},
			"secret_reset_required": false,
			"lost_secret_fields":    []string{},
			"reset_at":              int64(0),
			"created_at":            int64(1700000000),
			"updated_at":            int64(1700000100),
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/credentials", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	credentialsHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Credentials []map[string]any `json:"credentials"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Credentials) != 1 || payload.Credentials[0]["name"] != "Lab SSH" {
		t.Fatalf("unexpected credentials payload %+v", payload)
	}
}

func TestCredentialByIDHandlerReturnsCredential(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "User"})
	store.credentialByID = map[int64]map[string]any{
		7: {
			"id":              int64(7),
			"name":            "Lab SSH",
			"connection_type": "ssh",
			"username":        "ops",
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/credentials/7", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	credentialByIDHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Credential map[string]any `json:"credential"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if store.credentialIDSeen != 7 || payload.Credential["username"] != "ops" {
		t.Fatalf("unexpected credential id=%d payload=%+v", store.credentialIDSeen, payload)
	}
}

func TestCredentialByIDHandlerMissingCredential(t *testing.T) {
	auth, _ := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "User"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/credentials/8", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	credentialByIDHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDirectorySitesHandlerReturnsSites(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.directorySites = []map[string]any{
		{
			"id":              int64(1),
			"name":            "Bunny Lab",
			"description":     "Lab site",
			"created_at":      int64(1700000000),
			"device_count":    int64(12),
			"enrollment_code": "BUNNY",
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/directory/sites", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	directorySitesHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Sites []map[string]any `json:"sites"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Sites) != 1 || payload.Sites[0]["name"] != "Bunny Lab" {
		t.Fatalf("unexpected directory sites payload %+v", payload)
	}
}

func TestAdminEnrollmentCodesHandlerReturnsCodes(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.enrollmentCodes = []map[string]any{{"id": "site:1", "site_id": int64(1), "site_name": "Bunny Lab", "code": "ABCD"}}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/enrollment-codes", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	adminEnrollmentCodesHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Codes []map[string]any `json:"codes"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Codes) != 1 || payload.Codes[0]["code"] != "ABCD" {
		t.Fatalf("unexpected codes payload %+v", payload)
	}
}

func TestAdminDeviceApprovalsHandlerReturnsApprovals(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.approvals = []map[string]any{{"id": "approval-1", "status": "pending", "hostname_claimed": "LAB-01"}}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/admin/device-approvals?status=pending", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	adminDeviceApprovalsHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.approvalProfile.Username != "operator" {
		t.Fatalf("expected profile captured, got %+v", store.approvalProfile)
	}
	var payload struct {
		Approvals []map[string]any `json:"approvals"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Approvals) != 1 || payload.Approvals[0]["id"] != "approval-1" {
		t.Fatalf("unexpected approvals payload %+v", payload)
	}
}

func TestAdminDeviceApprovalApproveHandlerUpdatesStatus(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/admin/device-approvals/approval-1/approve", strings.NewReader(`{"guid":"2540DA38-E2B1-45B9-9113-BF7CF0E1778A","conflict_resolution":"overwrite"}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	request.SetPathValue("approval_id", "approval-1")
	adminDeviceApprovalApproveHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.approvalStatusID != "approval-1" || store.approvalStatus != "approved" || store.approvalResolution != "overwrite" {
		t.Fatalf("unexpected approval mutation id=%q status=%q resolution=%q", store.approvalStatusID, store.approvalStatus, store.approvalResolution)
	}
}

func TestDeviceViewListHandlerReturnsViews(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.views = []map[string]any{
		{
			"id":         int64(7),
			"name":       "Lab View",
			"columns":    []any{"status", "hostname"},
			"filters":    map[string]any{"site": "Bunny Lab"},
			"created_at": int64(1700000000),
			"updated_at": int64(1700000100),
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device_list_views", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceViewListHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Views []map[string]any `json:"views"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Views) != 1 || payload.Views[0]["name"] != "Lab View" {
		t.Fatalf("unexpected views payload %+v", payload)
	}
}

func TestDeviceViewGetHandlerReturnsViewOrNotFound(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.viewByID = map[int64]map[string]any{
		7: {
			"id":      int64(7),
			"name":    "Lab View",
			"columns": []any{"status"},
			"filters": map[string]any{},
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/device_list_views/7", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceViewGetHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.viewIDSeen != 7 {
		t.Fatalf("expected view id 7, got %d", store.viewIDSeen)
	}

	missing := httptest.NewRecorder()
	missingRequest := httptest.NewRequest(http.MethodGet, "/api/device_list_views/8", nil)
	missingRequest.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceViewGetHandler(auth, nil).ServeHTTP(missing, missingRequest)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", missing.Code, missing.Body.String())
	}
}

func TestDeviceViewListHandlerCreatesView(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/device_list_views", strings.NewReader(`{"name":"Lab Ops","columns":["status","hostname"],"filters":{"site":"Bunny Lab"}}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceViewListHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.createdView.Name == nil || *store.createdView.Name != "Lab Ops" {
		t.Fatalf("expected created view name, got %+v", store.createdView)
	}
	if len(store.createdView.Columns) != 2 || store.createdView.Columns[0] != "status" || store.createdView.Columns[1] != "hostname" {
		t.Fatalf("unexpected columns %+v", store.createdView.Columns)
	}
	if got := store.createdView.Filters["site"]; got != "Bunny Lab" {
		t.Fatalf("expected site filter, got %#v", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["name"] != "Lab Ops" || payload["id"] != float64(99) {
		t.Fatalf("unexpected create payload %+v", payload)
	}
}

func TestDeviceViewListHandlerRejectsInvalidCreatePayload(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/device_list_views", strings.NewReader(`{"name":"   ","columns":["status"],"filters":{}}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceViewListHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "name is required") {
		t.Fatalf("unexpected validation body %s", recorder.Body.String())
	}
}

func TestDeviceViewGetHandlerUpdatesView(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/device_list_views/7", strings.NewReader(`{"name":"Renamed","columns":["hostname"],"filters":{"status":"Online"}}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceViewGetHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.viewIDSeen != 7 {
		t.Fatalf("expected view id 7, got %d", store.viewIDSeen)
	}
	if store.updatedView.Name == nil || *store.updatedView.Name != "Renamed" {
		t.Fatalf("expected updated name, got %+v", store.updatedView)
	}
	if len(store.updatedView.Columns) != 1 || store.updatedView.Columns[0] != "hostname" {
		t.Fatalf("unexpected updated columns %+v", store.updatedView.Columns)
	}
	if got := store.updatedView.Filters["status"]; got != "Online" {
		t.Fatalf("expected status filter, got %#v", got)
	}
}

func TestDeviceViewGetHandlerDeletesView(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/device_list_views/7", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceViewGetHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.deletedViewID != 7 {
		t.Fatalf("expected deleted view id 7, got %d", store.deletedViewID)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("unexpected delete payload %+v", payload)
	}
}

func TestReadOnlyHandlersProxyNonNativeMethods(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	fallbackHits := 0
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.WriteHeader(http.StatusAccepted)
	})

	for _, entry := range []struct {
		name    string
		handler http.HandlerFunc
		method  string
		path    string
	}{
		{name: "release channel update", handler: agentReleaseChannelsHandler(auth, fallback), method: http.MethodPut, path: "/api/server/agent-release-channels"},
		{name: "server overview update", handler: serverOverviewHandler(auth, fallback), method: http.MethodPost, path: "/api/server/overview"},
		{name: "server workers update", handler: serverWorkersHandler(auth, fallback), method: http.MethodPost, path: "/api/server/workers"},
		{name: "user create", handler: usersHandler(auth, fallback), method: http.MethodPost, path: "/api/users"},
		{name: "passkeys unsupported method", handler: authPasskeysHandler(auth, fallback), method: http.MethodPost, path: "/api/auth/passkeys"},
		{name: "directory provider create", handler: directoryProvidersHandler(auth, fallback), method: http.MethodPost, path: "/api/directory/providers"},
		{name: "credential create", handler: credentialsHandler(auth, fallback), method: http.MethodPost, path: "/api/credentials"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(entry.method, entry.path, nil)
		entry.handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("%s expected fallback 202, got %d", entry.name, recorder.Code)
		}
	}
	if fallbackHits != 7 {
		t.Fatalf("expected 7 fallback hits, got %d", fallbackHits)
	}
}

func TestAgentReleaseChannelsHandlerRequiresAdmin(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "User"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/agent-release-channels", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	agentReleaseChannelsHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAgentReleaseChannelsHandlerReturnsSettings(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "agent_release_channels.json")
	content := `{
		"version": 1,
		"default_channel": "unstable",
		"github": {"repo": "bunny-lab-io/Borealis", "default_branch": "feature/rewrite-api-backend-in-golang"},
		"channels": {
			"stable": {"channel": "stable", "build_id": "stable-build", "published_at": "2026-06-01T00:00:00Z"},
			"unstable": {"channel": "unstable", "build_id": "unstable-build", "branch": "feature/rewrite-api-backend-in-golang"}
		},
		"last_refresh_completed_at": 1780000000
	}`
	if err := os.WriteFile(settingsPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOREALIS_AGENT_RELEASE_CHANNELS_PATH", settingsPath)
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.githubToken = map[string]any{"has_token": true, "reset_required": false, "reset_at": int64(0)}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/agent-release-channels", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	agentReleaseChannelsHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["default_channel"]; got != "unstable" {
		t.Fatalf("expected unstable default, got %#v", got)
	}
	if got := payload["settings_path"]; got != settingsPath {
		t.Fatalf("expected settings path, got %#v", got)
	}
	githubToken := payload["github_token"].(map[string]any)
	if got := githubToken["has_token"]; got != true {
		t.Fatalf("expected github token true, got %#v", got)
	}
	channels := payload["channels"].(map[string]any)
	stable := channels["stable"].(map[string]any)
	if got := stable["build_id"]; got != "stable-build" {
		t.Fatalf("expected stable build, got %#v", got)
	}
	unstable := channels["unstable"].(map[string]any)
	if got := unstable["branch"]; got != "feature/rewrite-api-backend-in-golang" {
		t.Fatalf("expected unstable branch, got %#v", got)
	}
}

func TestAgentReleaseChannelsDefaultsWhenConfigMissing(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "missing", "agent_release_channels.json")
	t.Setenv("BOREALIS_AGENT_RELEASE_CHANNELS_PATH", settingsPath)
	t.Setenv("BOREALIS_UPDATE_REPO", "owner/repo")
	t.Setenv("BOREALIS_UPDATE_BRANCH", "develop")

	payload := collectAgentReleaseChannelSettings()
	if got := payload["default_channel"]; got != "stable" {
		t.Fatalf("expected stable default, got %#v", got)
	}
	github := payload["github"].(map[string]any)
	if got := github["repo"]; got != "owner/repo" {
		t.Fatalf("expected env repo, got %#v", got)
	}
	if got := github["default_branch"]; got != "develop" {
		t.Fatalf("expected env branch, got %#v", got)
	}
	channels := payload["channels"].(map[string]any)
	unstable := channels["unstable"].(map[string]any)
	if got := unstable["version_label"]; got != "develop" {
		t.Fatalf("expected unstable version label from env, got %#v", got)
	}
	if got := unstable["branch"]; got != "develop" {
		t.Fatalf("expected unstable branch from env, got %#v", got)
	}
}

func TestServerWorkersHandlerRequiresAdmin(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "User"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/workers", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverWorkersHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestServerWorkersHandlerReturnsPayload(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.serverWorkers = map[string]any{
		"active_count": int64(1),
		"workers": []any{
			map[string]any{"worker_guid": "worker-1", "status": "running"},
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/workers?history_seconds=999999", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverWorkersHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.workerHistory != 86400 {
		t.Fatalf("expected clamped history 86400, got %d", store.workerHistory)
	}
	if !store.workerContainers {
		t.Fatalf("expected container metadata request")
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["active_count"]; got != float64(1) {
		t.Fatalf("expected active_count 1, got %#v", got)
	}
	workers, ok := payload["workers"].([]any)
	if !ok || len(workers) != 1 {
		t.Fatalf("expected one worker, got %#v", payload["workers"])
	}
}

func TestServerOverviewHandlerRequiresAdmin(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "User"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/overview", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverOverviewHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestServerOverviewHandlerReturnsPayload(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "agent_release_channels.json")
	if err := os.WriteFile(settingsPath, []byte(`{"default_channel":"unstable","channels":{"unstable":{"channel":"unstable","build_id":"build-1"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOREALIS_AGENT_RELEASE_CHANNELS_PATH", settingsPath)
	t.Setenv("BOREALIS_PUBLIC_BASE_URL", "https://borealis.example.test")
	t.Setenv("BOREALIS_PUBLIC_HOSTNAME", "borealis.example.test")
	t.Setenv("BOREALIS_WEBUI_MODE", "prod")
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.serverWorkers = map[string]any{
		"active_count": int64(1),
		"workers":      []any{map[string]any{"worker_guid": "worker-1", "status": "running"}},
	}
	store.githubToken = map[string]any{"has_token": true, "reset_required": false, "reset_at": int64(0)}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/overview", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverOverviewHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"host", "resources", "services", "wireguard", "public_edge", "security", "ansible_runner", "site_worker_settings", "agent_release_channels", "remote_desktop", "operator_session_count", "workers"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("expected overview key %q in %+v", key, payload)
		}
	}
	host := payload["host"].(map[string]any)
	if got := host["public_base_url"]; got != "https://borealis.example.test" {
		t.Fatalf("expected public base url, got %#v", got)
	}
	channels := payload["agent_release_channels"].(map[string]any)
	if got := channels["default_channel"]; got != "unstable" {
		t.Fatalf("expected unstable default, got %#v", got)
	}
	workers := payload["workers"].(map[string]any)
	if got := workers["active_count"]; got != float64(1) {
		t.Fatalf("expected worker active count 1, got %#v", got)
	}
}

func TestServerLogsHandlerReturnsDomains(t *testing.T) {
	logRoot := t.TempDir()
	t.Setenv("BOREALIS_GO_API_LOG_ROOT", logRoot)
	if err := os.WriteFile(filepath.Join(logRoot, "engine.log"), []byte("[2026-06-02T00:00:00Z] [INFO] engine ready\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logRoot, "engine.log.2026-06-01"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/logs", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverLogsHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Logs []map[string]any `json:"logs"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Logs) != 1 || payload.Logs[0]["file"] != "engine.log" {
		t.Fatalf("unexpected logs payload %+v", payload)
	}
	if int(payload.Logs[0]["rotation_count"].(float64)) != 1 {
		t.Fatalf("expected one rotation, got %+v", payload.Logs[0])
	}
}

func TestServerLogEntriesHandlerReturnsParsedEntries(t *testing.T) {
	logRoot := t.TempDir()
	t.Setenv("BOREALIS_GO_API_LOG_ROOT", logRoot)
	content := strings.Join([]string{
		"[2026-06-02T00:00:00Z] [INFO][CONTEXT-ADMIN] first message",
		"2026-06-02 00:01:00,000-engine-ERROR: second message",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(logRoot, "engine.log"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/logs/engine.log/entries?limit=50", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverLogEntriesHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Entries) != 2 {
		t.Fatalf("expected two entries, got %+v", payload)
	}
	if payload.Entries[0]["scope"] != "ADMIN" || payload.Entries[1]["level"] != "ERROR" {
		t.Fatalf("unexpected parsed entries %+v", payload.Entries)
	}
}

func TestServerLogRetentionHandlerUpdatesPolicy(t *testing.T) {
	logRoot := t.TempDir()
	t.Setenv("BOREALIS_GO_API_LOG_ROOT", logRoot)
	if err := os.WriteFile(filepath.Join(logRoot, "engine.log"), []byte("active\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logRoot, "engine.log.2026-06-01"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/server/logs/retention", strings.NewReader(`{"retention":{"engine.log":5}}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverLogEntriesHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	overrides := payload["retention_overrides"].(map[string]any)
	if got := overrides["engine.log"]; got != float64(5) {
		t.Fatalf("expected retention override 5, got %#v", got)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPut, "/api/server/logs/retention", strings.NewReader(`{"retention":{"engine.log":null}}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverLogEntriesHandler(auth, nil).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected clear 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := loadLogRetention(logRoot)["engine.log"]; got != nil {
		t.Fatalf("expected cleared override, got %#v", got)
	}
}

func TestServerLogDeleteHandlerDeletesFamily(t *testing.T) {
	logRoot := t.TempDir()
	t.Setenv("BOREALIS_GO_API_LOG_ROOT", logRoot)
	for _, name := range []string{"engine.log", "engine.log.2026-06-01", "other.log"} {
		if err := os.WriteFile(filepath.Join(logRoot, name), []byte("line\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/server/logs/engine.log?scope=family", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverLogEntriesHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(filepath.Join(logRoot, "engine.log")); !os.IsNotExist(err) {
		t.Fatalf("expected active log deleted, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(logRoot, "engine.log.2026-06-01")); !os.IsNotExist(err) {
		t.Fatalf("expected rotated log deleted, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(logRoot, "other.log")); err != nil {
		t.Fatalf("expected other log retained, err=%v", err)
	}
}

func TestSiteWorkerSettingsHandlerRequiresAdmin(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "User"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/site-worker-settings", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	siteWorkerSettingsHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Administrator permissions") {
		t.Fatalf("expected admin auth message, got %s", recorder.Body.String())
	}
}

func TestSiteWorkerSettingsHandlerReturnsProfileManagedPayload(t *testing.T) {
	t.Setenv("BOREALIS_SITE_WORKER_SCHEDULED_CONCURRENCY", "45")
	t.Setenv("BOREALIS_DEPLOYMENT_PROFILE", "MSP / Production")
	t.Setenv("BOREALIS_DEPLOYMENT_PROFILE_RANK", "4")
	t.Setenv("BOREALIS_DEPLOYMENT_CPU_RANK", "3")
	t.Setenv("BOREALIS_DEPLOYMENT_MEMORY_RANK", "2")
	t.Setenv("BOREALIS_DEPLOYMENT_HOST_VCPU", "16")
	t.Setenv("BOREALIS_DEPLOYMENT_HOST_MEMORY_MIB", "33075")
	t.Setenv("BOREALIS_DEPLOYMENT_HOST_MEMORY_GIB", "32.3")
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/site-worker-settings", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	siteWorkerSettingsHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["scheduled_task_concurrency_limit"]; got != float64(32) {
		t.Fatalf("expected clamped concurrency 32, got %#v", got)
	}
	if got := payload["max_scheduled_task_concurrency_limit"]; got != float64(32) {
		t.Fatalf("expected max concurrency 32, got %#v", got)
	}
	if got := payload["editable"]; got != false {
		t.Fatalf("expected editable false, got %#v", got)
	}
	if got := payload["managed_by"]; got != "deployment_profile" {
		t.Fatalf("expected deployment_profile manager, got %#v", got)
	}
	profile, ok := payload["deployment_profile"].(map[string]any)
	if !ok {
		t.Fatalf("expected deployment profile map, got %#v", payload["deployment_profile"])
	}
	if got := profile["name"]; got != "MSP / Production" {
		t.Fatalf("expected profile name, got %#v", got)
	}
	if got := profile["host_vcpu"]; got != float64(16) {
		t.Fatalf("expected host vcpu 16, got %#v", got)
	}
	if got := profile["host_memory_gib"]; got != "32.3" {
		t.Fatalf("expected host memory gib string, got %#v", got)
	}
}

func TestSiteWorkerSettingsLoadsConfigFileWhenEnvUnset(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "site_worker_settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"scheduled_task_concurrency_limit": 12}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOREALIS_SITE_WORKER_SETTINGS_PATH", settingsPath)
	t.Setenv("BOREALIS_DEPLOYMENT_PROFILE", "")

	payload := collectSiteWorkerSettingsPayload()
	if got := payload["scheduled_task_concurrency_limit"]; got != 12 {
		t.Fatalf("expected file-backed concurrency 12, got %#v", got)
	}
	profile := payload["deployment_profile"].(map[string]any)
	if got := profile["name"]; got != "Unprofiled" {
		t.Fatalf("expected unprofiled fallback, got %#v", got)
	}
}

func TestAnsibleRunnerSettingsHandlerRequiresAdmin(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "User"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/ansible-runner-settings", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	ansibleRunnerSettingsHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestAnsibleRunnerSettingsHandlerReturnsConfigPayload(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "ansible_runner_settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"job_concurrency_limit": 0, "global_concurrency_limit": 18}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOREALIS_ANSIBLE_RUNNER_SETTINGS_PATH", settingsPath)
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/ansible-runner-settings", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	ansibleRunnerSettingsHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["job_concurrency_limit"]; got != float64(1) {
		t.Fatalf("expected clamped job limit 1, got %#v", got)
	}
	if got := payload["global_concurrency_limit"]; got != float64(18) {
		t.Fatalf("expected global limit 18, got %#v", got)
	}
}

func TestAnsibleRunnerSettingsHandlerPersistsConfigPayload(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "ansible_runner_settings.json")
	t.Setenv("BOREALIS_ANSIBLE_RUNNER_SETTINGS_PATH", settingsPath)
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/server/ansible-runner-settings", strings.NewReader(`{"job_concurrency_limit":"8","global_concurrency_limit":18}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	ansibleRunnerSettingsHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("unexpected status %#v", payload["status"])
	}
	ansibleRunner := payload["ansible_runner"].(map[string]any)
	if got := ansibleRunner["job_concurrency_limit"]; got != float64(8) {
		t.Fatalf("expected job limit 8, got %#v", got)
	}
	if got := ansibleRunner["global_concurrency_limit"]; got != float64(18) {
		t.Fatalf("expected global limit 18, got %#v", got)
	}
	loaded := loadJSONSettingsFile(settingsPath)
	if got := loaded["job_concurrency_limit"]; got != float64(8) {
		t.Fatalf("expected persisted job limit 8, got %#v", got)
	}
	if got := loaded["global_concurrency_limit"]; got != float64(18) {
		t.Fatalf("expected persisted global limit 18, got %#v", got)
	}
}

func TestAnsibleRunnerSettingsHandlerRejectsInvalidPayload(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "ansible_runner_settings.json")
	t.Setenv("BOREALIS_ANSIBLE_RUNNER_SETTINGS_PATH", settingsPath)
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/server/ansible-runner-settings", strings.NewReader(`{"job_concurrency_limit":0,"global_concurrency_limit":18}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	ansibleRunnerSettingsHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "invalid_ansible_runner_settings") {
		t.Fatalf("expected invalid settings error, got %s", recorder.Body.String())
	}
	if _, err := os.Stat(settingsPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected settings file to remain absent, err=%v", err)
	}
}

func TestAnsibleRunnerSettingsUsesEnvDefaultsWhenConfigMissing(t *testing.T) {
	t.Setenv("BOREALIS_ANSIBLE_RUNNER_SETTINGS_PATH", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("BOREALIS_ANSIBLE_RUNNER_JOB_CONCURRENCY_LIMIT", "7")
	t.Setenv("BOREALIS_ANSIBLE_RUNNER_GLOBAL_CONCURRENCY_LIMIT", "0")

	payload := collectAnsibleRunnerSettingsPayload()
	if got := payload["job_concurrency_limit"]; got != 7 {
		t.Fatalf("expected env-backed job limit 7, got %#v", got)
	}
	if got := payload["global_concurrency_limit"]; got != 1 {
		t.Fatalf("expected clamped env-backed global limit 1, got %#v", got)
	}
}

func TestMetadataFieldsHandlerRequiresAuthentication(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/metadata_fields", nil)
	metadataFieldsHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMetadataFieldsHandlerReturnsDefinitions(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.metadataFields = []map[string]any{
		{
			"field_number":  1,
			"field_key":     "field_001",
			"default_label": "Field 001",
			"label":         "Rack",
			"description":   "Rack",
			"updated_at":    int64(1700000000),
			"updated_by":    "operator",
			"value_limit":   1024,
		},
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/metadata_fields", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	metadataFieldsHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Fields     []map[string]any `json:"fields"`
		Count      int              `json:"count"`
		ValueLimit int              `json:"value_limit"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 1 || payload.ValueLimit != 1024 {
		t.Fatalf("unexpected metadata payload %+v", payload)
	}
	if got := payload.Fields[0]["label"]; got != "Rack" {
		t.Fatalf("expected custom label, got %#v", got)
	}
}

func TestMetadataFieldDefinitionHandlerUpdatesField(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/metadata_fields/7", strings.NewReader(`{"description":"Location Code"}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	metadataFieldDefinitionHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.metadataUpdateField != 7 || store.metadataUpdateDesc != "Location Code" || store.metadataUpdateActor != "operator" {
		t.Fatalf("unexpected update capture field=%d desc=%q actor=%q", store.metadataUpdateField, store.metadataUpdateDesc, store.metadataUpdateActor)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["field"] == nil {
		t.Fatalf("expected field payload, got %+v", payload)
	}
}

func TestDeviceMetadataFieldsHandlerRequiresAuthentication(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices/LAB-OPERATOR-01/metadata_fields", nil)
	deviceMetadataFieldsHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeviceMetadataFieldsHandlerReturnsPayload(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})
	store.deviceMetadata = map[string]any{
		"device": map[string]any{
			"guid":      "DEVICE-GUID",
			"hostname":  "LAB-OPERATOR-01",
			"site_id":   int64(1),
			"site_name": "Bunny Lab",
		},
		"fields": []any{
			map[string]any{
				"field_number": int64(1),
				"field_key":    "field_001",
				"label":        "Rack",
				"value":        "Rack 7",
				"has_value":    true,
			},
		},
		"count":       int64(1),
		"value_limit": int64(1024),
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices/LAB-OPERATOR-01/metadata_fields", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceMetadataFieldsHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.deviceMetaID != "LAB-OPERATOR-01" {
		t.Fatalf("expected device id capture, got %q", store.deviceMetaID)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if got := payload["count"]; got != float64(1) {
		t.Fatalf("expected count 1, got %#v", got)
	}
	fields := payload["fields"].([]any)
	first := fields[0].(map[string]any)
	if got := first["value"]; got != "Rack 7" {
		t.Fatalf("expected decoded metadata value, got %#v", got)
	}
}

func TestDeviceMetadataFieldsHandlerUpdatesPayload(t *testing.T) {
	auth, store := testAuthServiceWithStore(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/devices/LAB-OPERATOR-01/metadata_fields/9", strings.NewReader(`{"value":"Rack 9"}`))
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	deviceMetadataFieldsHandler(auth, nil).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if store.deviceMetaID != "LAB-OPERATOR-01" || store.deviceMetaField != 9 || store.deviceMetaValue != "Rack 9" {
		t.Fatalf("unexpected device metadata capture id=%q field=%d value=%q", store.deviceMetaID, store.deviceMetaField, store.deviceMetaValue)
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	field := payload["field"].(map[string]any)
	if field["has_value"] != true {
		t.Fatalf("expected has_value true, got %+v", field)
	}
}

func TestDeviceMetadataFieldsHandlerProxiesUnownedDeviceSubpaths(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})
	fallbackHits := 0
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackHits++
		w.WriteHeader(http.StatusAccepted)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/devices/device-guid", nil)
	deviceMetadataFieldsHandler(auth, fallback).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted || fallbackHits != 1 {
		t.Fatalf("expected fallback, got status=%d hits=%d", recorder.Code, fallbackHits)
	}
}

func TestBuildMetadataDefinitionsReturnsDefaultFiveHundredFields(t *testing.T) {
	fields := buildMetadataDefinitions(map[int]metadataDefinitionRow{
		7: {
			FieldNumber: sql.NullInt64{Int64: 7, Valid: true},
			Description: sql.NullString{String: "Location Code", Valid: true},
			UpdatedAt:   sql.NullInt64{Int64: 1700000000, Valid: true},
			UpdatedBy:   sql.NullString{String: "operator", Valid: true},
		},
	})
	if len(fields) != 500 {
		t.Fatalf("expected 500 fields, got %d", len(fields))
	}
	if fields[0]["field_key"] != "field_001" || fields[0]["label"] != "Field 001" {
		t.Fatalf("unexpected first field %+v", fields[0])
	}
	if fields[6]["field_key"] != "field_007" || fields[6]["label"] != "Location Code" {
		t.Fatalf("unexpected custom field %+v", fields[6])
	}
}

func TestServerTimeHandlerRequiresAuthentication(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/time", nil)
	serverTimeHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Authentication required") {
		t.Fatalf("expected normalized auth message, got %s", recorder.Body.String())
	}
}

func TestServerTimezonesHandlerRequiresAdmin(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "User"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/timezones", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverTimezonesHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Administrator permissions") {
		t.Fatalf("expected admin auth message, got %s", recorder.Body.String())
	}
}

func TestServerTimeHandlerReturnsNativePayload(t *testing.T) {
	auth := testAuthService(operatorProfile{Username: "operator", Role: "Admin"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/server/time", nil)
	request.Header.Set("Authorization", "Bearer "+testAuthToken)
	serverTimeHandler(auth).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"epoch", "iso", "utc", "timezone", "timezone_id", "display"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("expected payload key %q in %+v", key, payload)
		}
	}
}

func TestSerializeServerTimeMatchesPythonShape(t *testing.T) {
	location := time.FixedZone("MST", -7*60*60)
	nowLocal := time.Date(2026, 6, 2, 13, 4, 5, 123456789, location)
	nowUTC := nowLocal.UTC()

	payload := serializeServerTime(nowLocal, nowUTC, "America/Denver")

	if got := payload["iso"]; got != "2026-06-02T13:04:05.123456-07:00" {
		t.Fatalf("unexpected local iso %q", got)
	}
	if got := payload["utc"]; got != "2026-06-02T20:04:05.123456+00:00" {
		t.Fatalf("unexpected utc iso %q", got)
	}
	if got := payload["display"]; got != "2026-06-02 13:04:05 MST" {
		t.Fatalf("unexpected display %q", got)
	}
	if got := payload["timezone_id"]; got != "America/Denver" {
		t.Fatalf("unexpected timezone id %q", got)
	}
}

func TestCurrentTimezoneIDPrefersEngineHostTimezone(t *testing.T) {
	t.Setenv("BOREALIS_ENGINE_HOST_TIMEZONE", "America/Denver")
	t.Setenv("TZ", "Etc/UTC")

	if got := currentTimezoneID(); got != "America/Denver" {
		t.Fatalf("expected engine host timezone, got %q", got)
	}
}
