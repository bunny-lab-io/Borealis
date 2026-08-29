package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	borealisOperatorTokenHeader     = "X-Borealis-Operator-Token"
	borealisOperatorTokenContext    = "borealis-operator-internal-v1"
	borealisOperatorServiceAccount  = "/var/run/secrets/kubernetes.io/serviceaccount"
	borealisOperatorDefaultPort     = "8088"
	borealisOperatorDefaultWorkload = "borealis-operator"
	borealisOperatorTransientDrain  = "trap 'rm -f /tmp/borealis-draining' EXIT; touch /tmp/borealis-draining; sleep 10"
)

var borealisOperatorReadOnlyVerbs = []string{
	"GetClusterSummary",
	"ListWorkloads",
	"GetWorkloadStatus",
	"ListSiteWorkers",
}

var borealisOperatorMutationVerbs = []string{
	"RolloutKnownWorkload",
	"RestartKnownWorkload",
	"ScaleKnownWorkload",
	"LaunchSiteWorker",
	"RetireSiteWorker",
	"StartPostRestoreRefresh",
}

var borealisOperatorAllowedVerbs = append(append([]string{}, borealisOperatorReadOnlyVerbs...), borealisOperatorMutationVerbs...)

var (
	borealisOperatorImmutableImageRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*(?::sha-[A-Fa-f0-9]{12,64}|@sha256:[A-Fa-f0-9]{64})$`)
	borealisOperatorKubernetesNamePattern    = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

type borealisOperator struct {
	secret    []byte
	namespace string
	kube      *kubernetesAPIClient
}

type borealisOperatorCommandRequest struct {
	Verb   string         `json:"verb"`
	Params map[string]any `json:"params"`
}

type borealisOperatorClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type kubernetesAPIClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type kubernetesAPIError struct {
	Path       string
	StatusCode int
	Body       string
}

func (e *kubernetesAPIError) Error() string {
	return fmt.Sprintf("Kubernetes API %s returned HTTP %d: %s", e.Path, e.StatusCode, e.Body)
}

func kubernetesAPIErrorHasStatus(err error, status int) bool {
	var apiErr *kubernetesAPIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == status
}

type borealisOperatorKnownWorkload struct {
	ServiceKey    string
	Kind          string
	APIGroup      string
	Resource      string
	Name          string
	ContainerName string
	MinReplicas   int64
	MaxReplicas   int64
}

type borealisOperatorRolloutRequest struct {
	ServiceKey string `json:"service_key"`
	ImageRef   string `json:"image_ref"`
}

type borealisOperatorRestartRequest struct {
	ServiceKey string `json:"service_key"`
}

type borealisOperatorScaleRequest struct {
	ServiceKey string `json:"service_key"`
	Replicas   int64  `json:"replicas"`
}

type borealisOperatorLaunchSiteWorkerRequest struct {
	SiteID            int64  `json:"site_id"`
	SiteName          string `json:"site_name"`
	WorkerGUID        string `json:"worker_guid"`
	ImageRef          string `json:"image_ref"`
	ResourceProfile   string `json:"resource_profile"`
	RemoteOpsPort     int64  `json:"remote_ops_port"`
	RemoteDesktopPort int64  `json:"remote_desktop_port"`
}

type borealisOperatorRetireSiteWorkerRequest struct {
	WorkerGUID string `json:"worker_guid"`
	Reason     string `json:"reason"`
}

type borealisOperatorPostRestoreRefreshRequest struct {
	DelaySeconds int64 `json:"delay_seconds"`
}

type borealisOperatorResourceProfile struct {
	RequestCPU    string
	RequestMemory string
	LimitCPU      string
	LimitMemory   string
}

func borealisOperatorMode() bool {
	if explicitHealthcheckArgMode() {
		return false
	}
	role := processRoleValue()
	if role != "" {
		return textInSet(role, "borealis-operator")
	}
	return processArgMatches("borealis-operator")
}

func borealisOperatorClientHealthcheckMode() bool {
	if processArgMatches("borealis-operator-healthcheck", "operator-healthcheck") {
		return true
	}
	return processRoleMatches("borealis-operator-healthcheck", "operator-healthcheck")
}

func runBorealisOperator(ctx context.Context, _ gatewayConfig) error {
	secret := strings.TrimSpace(os.Getenv("BOREALIS_OPERATOR_SECRET"))
	if secret == "" {
		return errors.New("BOREALIS_OPERATOR_SECRET is required")
	}
	kube, err := newInClusterKubernetesAPIClient()
	if err != nil {
		return err
	}
	operator := &borealisOperator{
		secret:    []byte(secret),
		namespace: borealisOperatorNamespace(),
		kube:      kube,
	}
	host := envDefault("BOREALIS_OPERATOR_LISTEN_HOST", "0.0.0.0")
	port := envDefault("BOREALIS_OPERATOR_LISTEN_PORT", borealisOperatorDefaultPort)
	server := &http.Server{
		Addr:              net.JoinHostPort(host, port),
		Handler:           operator.handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	exited := make(chan error, 1)
	go func() {
		log.Printf("borealis-operator listening on http://%s namespace=%s", server.Addr, operator.namespace)
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		exited <- err
	}()
	select {
	case <-ctx.Done():
	case err := <-exited:
		return err
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return err
	}
	return <-exited
}

func borealisOperatorNamespace() string {
	if namespace := strings.TrimSpace(os.Getenv("BOREALIS_OPERATOR_NAMESPACE")); namespace != "" {
		return namespace
	}
	raw, err := os.ReadFile(filepath.Join(borealisOperatorServiceAccount, "namespace"))
	if err == nil && strings.TrimSpace(string(raw)) != "" {
		return strings.TrimSpace(string(raw))
	}
	return "borealis"
}

func (o *borealisOperator) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", o.handleHealthz)
	mux.HandleFunc("/startup", o.handleHealthz)
	mux.HandleFunc("/live", o.handleHealthz)
	mux.HandleFunc("/ready", o.handleReady)
	mux.HandleFunc("/v1/command", o.authenticated(o.handleCommand))
	return mux
}

func (o *borealisOperator) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (o *borealisOperator) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	if _, err := os.Stat(envDefault("BOREALIS_DRAIN_FILE", "/tmp/borealis-draining")); err == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "reason": "draining"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (o *borealisOperator) authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		expected := goBorealisOperatorToken(o.secret)
		presented := strings.TrimSpace(r.Header.Get(borealisOperatorTokenHeader))
		if expected == "" || presented == "" || !hmac.Equal([]byte(expected), []byte(presented)) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (o *borealisOperator) handleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req borealisOperatorCommandRequest
	if err := decodeBorealisOperatorJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json", "message": err.Error()})
		return
	}
	verb := strings.TrimSpace(req.Verb)
	if verb == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "verb_required", "allowed_verbs": borealisOperatorAllowedVerbs})
		return
	}
	result, status, err := o.executeCommand(r.Context(), verb, req.Params)
	if err != nil {
		writeJSON(w, status, map[string]any{"error": "operator_command_failed", "message": err.Error(), "allowed_verbs": borealisOperatorAllowedVerbs})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "verb": verb, "result": result})
}

func (o *borealisOperator) executeCommand(ctx context.Context, verb string, params map[string]any) (map[string]any, int, error) {
	switch verb {
	case "GetClusterSummary":
		return o.clusterSummary(ctx)
	case "ListWorkloads":
		workloads, err := o.listWorkloads(ctx)
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		return map[string]any{"workloads": workloads}, http.StatusOK, nil
	case "GetWorkloadStatus":
		return o.getWorkloadStatus(ctx, cleanText(params["service_key"]))
	case "ListSiteWorkers":
		workers, err := o.listSiteWorkers(ctx)
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		return map[string]any{"workers": workers}, http.StatusOK, nil
	case "RolloutKnownWorkload":
		var req borealisOperatorRolloutRequest
		if err := decodeBorealisOperatorParams(params, &req); err != nil {
			return nil, http.StatusBadRequest, err
		}
		return o.rolloutKnownWorkload(ctx, req)
	case "RestartKnownWorkload":
		var req borealisOperatorRestartRequest
		if err := decodeBorealisOperatorParams(params, &req); err != nil {
			return nil, http.StatusBadRequest, err
		}
		return o.restartKnownWorkload(ctx, req)
	case "ScaleKnownWorkload":
		var req borealisOperatorScaleRequest
		if err := decodeBorealisOperatorParams(params, &req); err != nil {
			return nil, http.StatusBadRequest, err
		}
		return o.scaleKnownWorkload(ctx, req)
	case "LaunchSiteWorker":
		var req borealisOperatorLaunchSiteWorkerRequest
		if err := decodeBorealisOperatorParams(params, &req); err != nil {
			return nil, http.StatusBadRequest, err
		}
		return o.launchSiteWorker(ctx, req)
	case "RetireSiteWorker":
		var req borealisOperatorRetireSiteWorkerRequest
		if err := decodeBorealisOperatorParams(params, &req); err != nil {
			return nil, http.StatusBadRequest, err
		}
		return o.retireSiteWorker(ctx, req)
	case "StartPostRestoreRefresh":
		var req borealisOperatorPostRestoreRefreshRequest
		if err := decodeBorealisOperatorParams(params, &req); err != nil {
			return nil, http.StatusBadRequest, err
		}
		return o.startPostRestoreRefresh(req), http.StatusAccepted, nil
	default:
		return nil, http.StatusBadRequest, fmt.Errorf("unsupported Borealis operator verb %q", verb)
	}
}

func (o *borealisOperator) clusterSummary(ctx context.Context) (map[string]any, int, error) {
	workloads, err := o.listWorkloads(ctx)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	workers, err := o.listSiteWorkers(ctx)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	pods, err := o.kubeListItems(ctx, "core", "pods")
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	services, err := o.kubeListItems(ctx, "core", "services")
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	return map[string]any{
		"namespace":         o.namespace,
		"rbac_scope":        "namespace",
		"mutation_verbs":    borealisOperatorMutationVerbs,
		"read_only_verbs":   borealisOperatorReadOnlyVerbs,
		"allowed_verbs":     borealisOperatorAllowedVerbs,
		"workload_count":    len(workloads),
		"pod_count":         len(pods),
		"service_count":     len(services),
		"site_worker_count": len(workers),
		"workloads":         workloads,
	}, http.StatusOK, nil
}

func (o *borealisOperator) listWorkloads(ctx context.Context) ([]map[string]any, error) {
	deployments, err := o.kubeListItems(ctx, "apps", "deployments")
	if err != nil {
		return nil, err
	}
	statefulSets, err := o.kubeListItems(ctx, "apps", "statefulsets")
	if err != nil {
		return nil, err
	}
	workloads := make([]map[string]any, 0, len(deployments)+len(statefulSets))
	for _, item := range deployments {
		workloads = append(workloads, summarizeKubernetesWorkload("Deployment", item))
	}
	for _, item := range statefulSets {
		workloads = append(workloads, summarizeKubernetesWorkload("StatefulSet", item))
	}
	return workloads, nil
}

func (o *borealisOperator) getWorkloadStatus(ctx context.Context, serviceKey string) (map[string]any, int, error) {
	serviceKey = strings.ToLower(strings.TrimSpace(serviceKey))
	if serviceKey == "" {
		return nil, http.StatusBadRequest, errors.New("service_key is required")
	}
	spec, ok := borealisOperatorKnownWorkloadForService(serviceKey)
	if !ok {
		return nil, http.StatusBadRequest, fmt.Errorf("unsupported workload service_key %q", serviceKey)
	}
	item, err := o.kubeGet(ctx, spec.APIGroup, spec.Resource, spec.Name)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	return summarizeKubernetesWorkload(spec.Kind, item), http.StatusOK, nil
}

func (o *borealisOperator) rolloutKnownWorkload(ctx context.Context, req borealisOperatorRolloutRequest) (map[string]any, int, error) {
	spec, status, err := o.requireKnownWorkload(req.ServiceKey)
	if err != nil {
		return nil, status, err
	}
	imageRef := strings.TrimSpace(req.ImageRef)
	if !borealisOperatorImageAllowedForService(spec.ServiceKey, imageRef) {
		return nil, http.StatusForbidden, fmt.Errorf("image_ref %q is not allowed for %s", imageRef, spec.ServiceKey)
	}
	before, err := o.kubeGet(ctx, spec.APIGroup, spec.Resource, spec.Name)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	previousImage := kubernetesWorkloadContainerImage(before, spec.ContainerName)
	if previousImage == "" {
		return nil, http.StatusConflict, fmt.Errorf("container %q missing from %s", spec.ContainerName, spec.Name)
	}
	if previousImage == imageRef {
		return map[string]any{
			"ok":             true,
			"changed":        false,
			"service_key":    spec.ServiceKey,
			"image_ref":      imageRef,
			"previous_image": previousImage,
			"workload":       summarizeKubernetesWorkload(spec.Kind, before),
		}, http.StatusOK, nil
	}
	patch := borealisOperatorWorkloadImagePatch(spec.ContainerName, imageRef, map[string]string{
		"borealis.io/rollout-at":    time.Now().UTC().Format(time.RFC3339Nano),
		"borealis.io/rollout-image": imageRef,
	})
	patched, err := o.kubePatch(ctx, spec.APIGroup, spec.Resource, spec.Name, patch)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	if err := o.waitForWorkloadRollout(ctx, spec); err != nil {
		rollbackPatch := borealisOperatorWorkloadImagePatch(spec.ContainerName, previousImage, map[string]string{
			"borealis.io/rollback-at":     time.Now().UTC().Format(time.RFC3339Nano),
			"borealis.io/rollback-reason": "rollout_failed",
		})
		_, rollbackErr := o.kubePatch(context.Background(), spec.APIGroup, spec.Resource, spec.Name, rollbackPatch)
		if rollbackErr == nil {
			rollbackCtx, cancel := context.WithTimeout(context.Background(), borealisOperatorRollbackTimeout())
			_ = o.waitForWorkloadRollout(rollbackCtx, spec)
			cancel()
		}
		return nil, http.StatusBadGateway, fmt.Errorf("rollout failed for %s: %v; rollback_error=%v", spec.ServiceKey, err, rollbackErr)
	}
	current, err := o.kubeGet(ctx, spec.APIGroup, spec.Resource, spec.Name)
	if err != nil {
		current = patched
	}
	return map[string]any{
		"ok":             true,
		"changed":        true,
		"service_key":    spec.ServiceKey,
		"image_ref":      imageRef,
		"previous_image": previousImage,
		"workload":       summarizeKubernetesWorkload(spec.Kind, current),
	}, http.StatusOK, nil
}

func (o *borealisOperator) restartKnownWorkload(ctx context.Context, req borealisOperatorRestartRequest) (map[string]any, int, error) {
	spec, status, err := o.requireKnownWorkload(req.ServiceKey)
	if err != nil {
		return nil, status, err
	}
	restartedAt := time.Now().UTC().Format(time.RFC3339Nano)
	patch := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]string{
						"borealis.io/restarted-at": restartedAt,
					},
				},
			},
		},
	}
	patched, err := o.kubePatch(ctx, spec.APIGroup, spec.Resource, spec.Name, patch)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	if err := o.waitForWorkloadRollout(ctx, spec); err != nil {
		return nil, http.StatusBadGateway, err
	}
	current, err := o.kubeGet(ctx, spec.APIGroup, spec.Resource, spec.Name)
	if err != nil {
		current = patched
	}
	return map[string]any{
		"ok":           true,
		"service_key":  spec.ServiceKey,
		"restarted_at": restartedAt,
		"workload":     summarizeKubernetesWorkload(spec.Kind, current),
	}, http.StatusOK, nil
}

func (o *borealisOperator) scaleKnownWorkload(ctx context.Context, req borealisOperatorScaleRequest) (map[string]any, int, error) {
	spec, status, err := o.requireKnownWorkload(req.ServiceKey)
	if err != nil {
		return nil, status, err
	}
	if req.Replicas < spec.MinReplicas || req.Replicas > spec.MaxReplicas {
		return nil, http.StatusBadRequest, fmt.Errorf("replicas for %s must be between %d and %d", spec.ServiceKey, spec.MinReplicas, spec.MaxReplicas)
	}
	before, err := o.kubeGet(ctx, spec.APIGroup, spec.Resource, spec.Name)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	previousReplicas := coerceInt64(nestedMap(before, "spec")["replicas"])
	if previousReplicas == req.Replicas {
		return map[string]any{
			"ok":                true,
			"changed":           false,
			"service_key":       spec.ServiceKey,
			"replicas":          req.Replicas,
			"previous_replicas": previousReplicas,
			"workload":          summarizeKubernetesWorkload(spec.Kind, before),
		}, http.StatusOK, nil
	}
	patch := map[string]any{"spec": map[string]any{"replicas": req.Replicas}}
	patched, err := o.kubePatch(ctx, spec.APIGroup, spec.Resource, spec.Name, patch)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	if err := o.waitForWorkloadRollout(ctx, spec); err != nil {
		_, rollbackErr := o.kubePatch(context.Background(), spec.APIGroup, spec.Resource, spec.Name, map[string]any{"spec": map[string]any{"replicas": previousReplicas}})
		return nil, http.StatusBadGateway, fmt.Errorf("scale failed for %s: %v; rollback_error=%v", spec.ServiceKey, err, rollbackErr)
	}
	current, err := o.kubeGet(ctx, spec.APIGroup, spec.Resource, spec.Name)
	if err != nil {
		current = patched
	}
	return map[string]any{
		"ok":                true,
		"changed":           true,
		"service_key":       spec.ServiceKey,
		"replicas":          req.Replicas,
		"previous_replicas": previousReplicas,
		"workload":          summarizeKubernetesWorkload(spec.Kind, current),
	}, http.StatusOK, nil
}

func (o *borealisOperator) launchSiteWorker(ctx context.Context, req borealisOperatorLaunchSiteWorkerRequest) (map[string]any, int, error) {
	workerGUID := strings.ToLower(cleanText(req.WorkerGUID))
	if req.SiteID <= 0 || workerGUID == "" {
		return nil, http.StatusBadRequest, errors.New("site_id and worker_guid are required")
	}
	if !borealisOperatorKubernetesNameAllowed(workerGUID) {
		return nil, http.StatusBadRequest, errors.New("worker_guid must be a Kubernetes DNS label segment")
	}
	imageRef := strings.TrimSpace(req.ImageRef)
	if !borealisOperatorImageAllowedForService("site-worker", imageRef) {
		return nil, http.StatusForbidden, fmt.Errorf("image_ref %q is not allowed for site-worker", imageRef)
	}
	profileName := strings.ToLower(firstText(cleanText(req.ResourceProfile), "standard"))
	profile, ok := borealisOperatorSiteWorkerResourceProfile(profileName)
	if !ok {
		return nil, http.StatusBadRequest, fmt.Errorf("unsupported resource_profile %q", profileName)
	}
	remoteOpsPort := req.RemoteOpsPort
	if remoteOpsPort <= 0 {
		return nil, http.StatusBadRequest, errors.New("remote_ops_port is required")
	}
	remoteDesktopPort := req.RemoteDesktopPort
	if remoteDesktopPort <= 0 {
		return nil, http.StatusBadRequest, errors.New("remote_desktop_port is required")
	}
	existing, err := o.siteWorkerPodsByGUID(ctx, workerGUID)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	if len(existing) > 0 {
		return nil, http.StatusConflict, fmt.Errorf("site-worker %s already has %d K3s pod(s)", workerGUID, len(existing))
	}
	staleServices, err := o.siteWorkerServicesByGUID(ctx, workerGUID)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	for _, service := range staleServices {
		metadata := nestedMap(service, "metadata")
		name := cleanText(metadata["name"])
		if name == "" {
			continue
		}
		if err := o.kubeDelete(ctx, "core", "services", name, map[string]any{
			"apiVersion": "v1",
			"kind":       "DeleteOptions",
		}); err != nil {
			return nil, http.StatusBadGateway, err
		}
	}
	siteName := cleanText(req.SiteName)
	siteSlug := siteWorkerSiteSlug(req.SiteID, siteName)
	podName := siteWorkerNameForSite(req.SiteID, siteName, workerGUID)
	if !borealisOperatorKubernetesObjectNameAllowed(podName) {
		return nil, http.StatusBadRequest, errors.New("generated site-worker pod name must be a Kubernetes DNS label segment")
	}
	serviceName := siteWorkerServiceNameForSite(req.SiteID, siteName, workerGUID)
	if !borealisOperatorKubernetesObjectNameAllowed(serviceName) {
		return nil, http.StatusBadRequest, errors.New("generated site-worker service name must be a Kubernetes DNS label segment")
	}
	service := o.siteWorkerServiceManifest(serviceName, req.SiteID, siteName, siteSlug, workerGUID, remoteOpsPort, remoteDesktopPort)
	createdService, err := o.kubeCreate(ctx, "core", "services", service)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	serviceSummary := summarizeBorealisSiteWorkerService(createdService)
	serviceHost := firstText(cleanText(serviceSummary["cluster_ip"]), siteWorkerServiceDNSName(serviceName, o.namespace))
	pod := o.siteWorkerPodManifest(podName, serviceName, serviceHost, req.SiteID, siteName, siteSlug, workerGUID, imageRef, profileName, profile, remoteOpsPort, remoteDesktopPort)
	created, err := o.kubeCreate(ctx, "core", "pods", pod)
	if err != nil {
		_ = o.kubeDelete(context.Background(), "core", "services", serviceName, map[string]any{
			"apiVersion": "v1",
			"kind":       "DeleteOptions",
		})
		return nil, http.StatusBadGateway, err
	}
	podSummary := summarizeBorealisSiteWorkerPod(created)
	attachBorealisSiteWorkerServiceSummary(podSummary, serviceSummary, o.namespace)
	return map[string]any{
		"ok":                  true,
		"launched":            true,
		"pod_name":            podName,
		"service_name":        serviceName,
		"worker_guid":         workerGUID,
		"site_id":             req.SiteID,
		"site_name":           siteName,
		"site_slug":           siteSlug,
		"image_ref":           imageRef,
		"resource_profile":    profileName,
		"remote_ops_port":     remoteOpsPort,
		"remote_desktop_port": remoteDesktopPort,
		"remote_ops_host":     firstText(cleanText(podSummary["remote_ops_host"]), serviceHost),
		"service_cluster_ip":  cleanText(serviceSummary["cluster_ip"]),
		"service":             serviceSummary,
		"pod":                 podSummary,
	}, http.StatusAccepted, nil
}

func (o *borealisOperator) retireSiteWorker(ctx context.Context, req borealisOperatorRetireSiteWorkerRequest) (map[string]any, int, error) {
	workerGUID := strings.ToLower(cleanText(req.WorkerGUID))
	if workerGUID == "" {
		return nil, http.StatusBadRequest, errors.New("worker_guid is required")
	}
	if !borealisOperatorKubernetesNameAllowed(workerGUID) {
		return nil, http.StatusBadRequest, errors.New("worker_guid must be a Kubernetes DNS label segment")
	}
	pods, err := o.siteWorkerPodsByGUID(ctx, workerGUID)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	services, err := o.siteWorkerServicesByGUID(ctx, workerGUID)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	if len(pods) == 0 && len(services) == 0 {
		return map[string]any{"ok": true, "missing": true, "worker_guid": workerGUID}, http.StatusOK, nil
	}
	retired := make([]string, 0, len(pods))
	for _, pod := range pods {
		metadata := nestedMap(pod, "metadata")
		name := cleanText(metadata["name"])
		if name == "" {
			continue
		}
		if err := o.kubeDelete(ctx, "core", "pods", name, map[string]any{
			"apiVersion":         "v1",
			"kind":               "DeleteOptions",
			"gracePeriodSeconds": 15,
		}); err != nil {
			return nil, http.StatusBadGateway, err
		}
		retired = append(retired, name)
	}
	retiredServices := make([]string, 0, len(services))
	for _, service := range services {
		metadata := nestedMap(service, "metadata")
		name := cleanText(metadata["name"])
		if name == "" {
			continue
		}
		if err := o.kubeDelete(ctx, "core", "services", name, map[string]any{
			"apiVersion": "v1",
			"kind":       "DeleteOptions",
		}); err != nil {
			return nil, http.StatusBadGateway, err
		}
		retiredServices = append(retiredServices, name)
	}
	return map[string]any{
		"ok":                    true,
		"worker_guid":           workerGUID,
		"reason":                cleanText(req.Reason),
		"retired_pods":          retired,
		"retired_services":      retiredServices,
		"retired_count":         len(retired),
		"retired_service_count": len(retiredServices),
	}, http.StatusOK, nil
}

func (o *borealisOperator) startPostRestoreRefresh(req borealisOperatorPostRestoreRefreshRequest) map[string]any {
	delaySeconds := req.DelaySeconds
	if delaySeconds <= 0 {
		delaySeconds = 2
	}
	if delaySeconds > 30 {
		delaySeconds = 30
	}
	workloads := []string{"api-backend", "wireguard-tunnel", "traefik-edge", "job-scheduler"}
	go o.runPostRestoreRefresh(time.Duration(delaySeconds)*time.Second, workloads)
	return map[string]any{
		"scheduled":            true,
		"starts_after_seconds": delaySeconds,
		"site_workers":         "retire_existing",
		"workloads":            workloads,
	}
}

func (o *borealisOperator) runPostRestoreRefresh(delay time.Duration, workloads []string) {
	if o == nil || o.kube == nil {
		log.Printf("post-restore runtime refresh skipped: Kubernetes client unavailable")
		return
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	workers, err := o.listSiteWorkers(ctx)
	if err != nil {
		log.Printf("post-restore runtime refresh failed to list site-workers: %v", err)
	} else {
		seenWorkers := map[string]bool{}
		for _, worker := range workers {
			workerGUID := strings.ToLower(cleanText(worker["worker_guid"]))
			if workerGUID == "" || seenWorkers[workerGUID] {
				continue
			}
			seenWorkers[workerGUID] = true
			if _, status, err := o.retireSiteWorker(ctx, borealisOperatorRetireSiteWorkerRequest{
				WorkerGUID: workerGUID,
				Reason:     "backup_restore_runtime_refresh",
			}); err != nil {
				log.Printf("post-restore runtime refresh failed to retire site-worker worker_guid=%s status=%d err=%v", workerGUID, status, err)
			}
		}
	}

	for _, serviceKey := range workloads {
		if _, status, err := o.restartKnownWorkload(ctx, borealisOperatorRestartRequest{ServiceKey: serviceKey}); err != nil {
			log.Printf("post-restore runtime refresh failed to restart service=%s status=%d err=%v", serviceKey, status, err)
			continue
		}
		log.Printf("post-restore runtime refresh restarted service=%s", serviceKey)
	}
}

func (o *borealisOperator) listSiteWorkers(ctx context.Context) ([]map[string]any, error) {
	pods, err := o.kubeListItems(ctx, "core", "pods")
	if err != nil {
		return nil, err
	}
	services, err := o.kubeListItems(ctx, "core", "services")
	if err != nil {
		return nil, err
	}
	servicesByGUID := borealisSiteWorkerServiceSummariesByGUID(services)
	podMetricsByName, _ := o.kubernetesPodMetricsByName(ctx)
	workers := make([]map[string]any, 0)
	for _, pod := range pods {
		metadata := nestedMap(pod, "metadata")
		labels := mapStringAny(metadata["labels"])
		name := cleanText(metadata["name"])
		component := strings.ToLower(cleanText(labels["app.kubernetes.io/component"]))
		workload := strings.ToLower(cleanText(labels["borealis.io/workload"]))
		if component != "site-worker" && workload != "site-worker" && !strings.HasPrefix(name, "site-worker-") {
			continue
		}
		worker := summarizeBorealisSiteWorkerPod(pod)
		if service := servicesByGUID[cleanText(worker["worker_guid"])]; len(service) > 0 {
			attachBorealisSiteWorkerServiceSummary(worker, service, o.namespace)
		}
		if metrics := podMetricsByName[name]; len(metrics) > 0 {
			attachKubernetesPodMetrics(worker, pod, metrics)
		}
		workers = append(workers, worker)
	}
	return workers, nil
}

func (o *borealisOperator) kubernetesPodMetricsByName(ctx context.Context) (map[string]map[string]any, error) {
	items, err := o.kubeListItems(ctx, "metrics.k8s.io", "pods")
	if err != nil {
		return nil, err
	}
	result := make(map[string]map[string]any, len(items))
	for _, item := range items {
		metadata := nestedMap(item, "metadata")
		name := cleanText(metadata["name"])
		if name == "" {
			continue
		}
		result[name] = item
	}
	return result, nil
}

func (o *borealisOperator) requireKnownWorkload(serviceKey string) (borealisOperatorKnownWorkload, int, error) {
	serviceKey = strings.ToLower(strings.TrimSpace(serviceKey))
	if serviceKey == "" {
		return borealisOperatorKnownWorkload{}, http.StatusBadRequest, errors.New("service_key is required")
	}
	spec, ok := borealisOperatorKnownWorkloadForService(serviceKey)
	if !ok {
		return borealisOperatorKnownWorkload{}, http.StatusBadRequest, fmt.Errorf("unsupported workload service_key %q", serviceKey)
	}
	return spec, http.StatusOK, nil
}

func borealisOperatorKnownWorkloadForService(serviceKey string) (borealisOperatorKnownWorkload, bool) {
	serviceKey = strings.ToLower(strings.TrimSpace(serviceKey))
	workloads := map[string]borealisOperatorKnownWorkload{
		"borealis-operator": {
			ServiceKey:    "borealis-operator",
			Kind:          "Deployment",
			APIGroup:      "apps",
			Resource:      "deployments",
			Name:          "borealis-operator",
			ContainerName: "borealis-operator",
			MinReplicas:   1,
			MaxReplicas:   1,
		},
		"webui-frontend": {
			ServiceKey:    "webui-frontend",
			Kind:          "Deployment",
			APIGroup:      "apps",
			Resource:      "deployments",
			Name:          "webui-frontend",
			ContainerName: "webui-frontend",
			MinReplicas:   1,
			MaxReplicas:   4,
		},
		"remote-desktop-guacd": {
			ServiceKey:    "remote-desktop-guacd",
			Kind:          "Deployment",
			APIGroup:      "apps",
			Resource:      "deployments",
			Name:          "remote-desktop-guacd",
			ContainerName: "remote-desktop-guacd",
			MinReplicas:   1,
			MaxReplicas:   2,
		},
		"traefik-edge": {
			ServiceKey:    "traefik-edge",
			Kind:          "Deployment",
			APIGroup:      "apps",
			Resource:      "deployments",
			Name:          "traefik-edge",
			ContainerName: "traefik-edge",
			MinReplicas:   1,
			MaxReplicas:   1,
		},
		"api-backend": {
			ServiceKey:    "api-backend",
			Kind:          "Deployment",
			APIGroup:      "apps",
			Resource:      "deployments",
			Name:          "api-backend",
			ContainerName: "api-backend",
			MinReplicas:   1,
			MaxReplicas:   1,
		},
		"job-scheduler": {
			ServiceKey:    "job-scheduler",
			Kind:          "Deployment",
			APIGroup:      "apps",
			Resource:      "deployments",
			Name:          "job-scheduler",
			ContainerName: "job-scheduler",
			MinReplicas:   1,
			MaxReplicas:   1,
		},
		"postgres-db": {
			ServiceKey:    "postgres-db",
			Kind:          "StatefulSet",
			APIGroup:      "apps",
			Resource:      "statefulsets",
			Name:          "postgres-db",
			ContainerName: "postgres-db",
			MinReplicas:   1,
			MaxReplicas:   1,
		},
		"wireguard-tunnel": {
			ServiceKey:    "wireguard-tunnel",
			Kind:          "Deployment",
			APIGroup:      "apps",
			Resource:      "deployments",
			Name:          "wireguard-tunnel",
			ContainerName: "wireguard-tunnel",
			MinReplicas:   1,
			MaxReplicas:   1,
		},
	}
	spec, ok := workloads[serviceKey]
	return spec, ok
}

func borealisOperatorKnownWorkloadNames(resource string) []string {
	names := map[string]bool{}
	for _, serviceKey := range borealisOperatorKnownServiceKeys() {
		spec, ok := borealisOperatorKnownWorkloadForService(serviceKey)
		if ok && spec.Resource == resource {
			names[spec.Name] = true
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func borealisOperatorKnownServiceKeys() []string {
	keys := []string{
		"api-backend",
		"borealis-operator",
		"job-scheduler",
		"postgres-db",
		"remote-desktop-guacd",
		"traefik-edge",
		"webui-frontend",
		"wireguard-tunnel",
	}
	sort.Strings(keys)
	return keys
}

func borealisOperatorImageAllowedForService(serviceKey string, imageRef string) bool {
	serviceKey = strings.ToLower(strings.TrimSpace(serviceKey))
	imageRef = strings.TrimSpace(imageRef)
	if !borealisOperatorImmutableImageRefPattern.MatchString(imageRef) {
		return false
	}
	allowed := map[string]bool{}
	if serviceKey == "site-worker" {
		for _, image := range borealisOperatorSplitAllowlist(os.Getenv("BOREALIS_OPERATOR_SITE_WORKER_IMAGE_ALLOWLIST")) {
			allowed[image] = true
		}
	} else {
		for key, image := range borealisOperatorParseWorkloadImageAllowlist(os.Getenv("BOREALIS_OPERATOR_WORKLOAD_IMAGE_ALLOWLIST")) {
			if key == serviceKey || key == "*" {
				allowed[image] = true
			}
		}
	}
	return allowed[imageRef]
}

func borealisOperatorParseWorkloadImageAllowlist(raw string) map[string]string {
	result := map[string]string{}
	for _, token := range borealisOperatorSplitAllowlist(raw) {
		key, value, ok := strings.Cut(token, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		result[key] = value
	}
	return result
}

func borealisOperatorSplitAllowlist(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

func borealisOperatorWorkloadImagePatch(containerName string, imageRef string, annotations map[string]string) map[string]any {
	metadata := map[string]any{}
	if len(annotations) > 0 {
		metadata["annotations"] = annotations
	}
	return map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": metadata,
				"spec": map[string]any{
					"containers": []map[string]any{
						{
							"name":  containerName,
							"image": imageRef,
						},
					},
				},
			},
		},
	}
}

func (o *borealisOperator) waitForWorkloadRollout(ctx context.Context, spec borealisOperatorKnownWorkload) error {
	deadline := time.NewTimer(borealisOperatorRolloutTimeout())
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		item, err := o.kubeGet(ctx, spec.APIGroup, spec.Resource, spec.Name)
		if err == nil && kubernetesWorkloadRolloutReady(item) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if err != nil {
				return err
			}
			return fmt.Errorf("%s rollout timed out", spec.ServiceKey)
		case <-ticker.C:
		}
	}
}

func borealisOperatorRolloutTimeout() time.Duration {
	seconds := parseEnvIntMin("BOREALIS_OPERATOR_ROLLOUT_TIMEOUT_SECONDS", 90, 5)
	return time.Duration(seconds) * time.Second
}

func borealisOperatorRollbackTimeout() time.Duration {
	seconds := parseEnvIntMin("BOREALIS_OPERATOR_ROLLBACK_TIMEOUT_SECONDS", 45, 5)
	return time.Duration(seconds) * time.Second
}

func kubernetesWorkloadContainerImage(item map[string]any, containerName string) string {
	podSpec := nestedMap(nestedMap(nestedMap(item, "spec"), "template"), "spec")
	containers, _ := podSpec["containers"].([]any)
	for _, raw := range containers {
		container, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if cleanText(container["name"]) == containerName {
			return cleanText(container["image"])
		}
	}
	return ""
}

func kubernetesWorkloadRolloutReady(item map[string]any) bool {
	metadata := nestedMap(item, "metadata")
	spec := nestedMap(item, "spec")
	status := nestedMap(item, "status")
	generation := coerceInt64(metadata["generation"])
	observedGeneration := coerceInt64(status["observedGeneration"])
	replicas := coerceInt64(spec["replicas"])
	if replicas == 0 {
		return observedGeneration >= generation
	}
	ready := coerceInt64(status["readyReplicas"])
	available := coerceInt64(status["availableReplicas"])
	updated := coerceInt64(status["updatedReplicas"])
	return observedGeneration >= generation && ready >= replicas && available >= replicas && updated >= replicas
}

func borealisOperatorKubernetesNameAllowed(value string) bool {
	return len(value) <= 48 && borealisOperatorKubernetesNamePattern.MatchString(value)
}

func borealisOperatorKubernetesObjectNameAllowed(value string) bool {
	return len(value) <= siteWorkerKubernetesNameMax && borealisOperatorKubernetesNamePattern.MatchString(value)
}

func borealisOperatorSiteWorkerResourceProfile(name string) (borealisOperatorResourceProfile, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "small":
		return borealisOperatorResourceProfile{RequestCPU: "25m", RequestMemory: "96Mi", LimitCPU: "250m", LimitMemory: "256Mi"}, true
	case "", "standard":
		return borealisOperatorResourceProfile{RequestCPU: "50m", RequestMemory: "128Mi", LimitCPU: "1000m", LimitMemory: "512Mi"}, true
	case "large":
		return borealisOperatorResourceProfile{RequestCPU: "100m", RequestMemory: "256Mi", LimitCPU: "2000m", LimitMemory: "1024Mi"}, true
	default:
		return borealisOperatorResourceProfile{}, false
	}
}

func (o *borealisOperator) siteWorkerPodsByGUID(ctx context.Context, workerGUID string) ([]map[string]any, error) {
	pods, err := o.kubeListItems(ctx, "core", "pods")
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0)
	for _, pod := range pods {
		metadata := nestedMap(pod, "metadata")
		labels := mapStringAny(metadata["labels"])
		if cleanText(labels["borealis.io/worker-guid"]) != workerGUID {
			continue
		}
		if strings.ToLower(cleanText(labels["app.kubernetes.io/component"])) != "site-worker" {
			continue
		}
		if cleanText(labels["app.kubernetes.io/managed-by"]) != "borealis-operator" {
			continue
		}
		result = append(result, pod)
	}
	return result, nil
}

func (o *borealisOperator) siteWorkerServicesByGUID(ctx context.Context, workerGUID string) ([]map[string]any, error) {
	services, err := o.kubeListItems(ctx, "core", "services")
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0)
	for _, service := range services {
		metadata := nestedMap(service, "metadata")
		labels := mapStringAny(metadata["labels"])
		if cleanText(labels["borealis.io/worker-guid"]) != workerGUID {
			continue
		}
		if strings.ToLower(cleanText(labels["app.kubernetes.io/component"])) != "site-worker" {
			continue
		}
		if cleanText(labels["app.kubernetes.io/managed-by"]) != "borealis-operator" {
			continue
		}
		result = append(result, service)
	}
	return result, nil
}

func siteWorkerServiceNameForSite(siteID int64, siteName string, workerGUID string) string {
	return siteWorkerNameForSite(siteID, siteName, workerGUID)
}

func siteWorkerServiceDNSName(serviceName string, namespace string) string {
	serviceName = strings.TrimSpace(serviceName)
	namespace = firstText(strings.TrimSpace(namespace), "borealis")
	if serviceName == "" {
		return ""
	}
	return serviceName + "." + namespace + ".svc.cluster.local"
}

func (o *borealisOperator) siteWorkerServiceManifest(serviceName string, siteID int64, siteName string, siteSlug string, workerGUID string, remoteOpsPort int64, remoteDesktopPort int64) map[string]any {
	labels := map[string]string{
		"app.kubernetes.io/name":       "site-worker",
		"app.kubernetes.io/part-of":    "borealis",
		"app.kubernetes.io/managed-by": "borealis-operator",
		"app.kubernetes.io/component":  "site-worker",
		"borealis.io/workload":         "site-worker",
		"borealis.io/worker-guid":      workerGUID,
		"borealis.io/site-id":          fmt.Sprintf("%d", siteID),
	}
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      serviceName,
			"namespace": o.namespace,
			"labels":    labels,
			"annotations": map[string]string{
				"borealis.io/stage":               "site-worker-migration",
				"borealis.io/site-name":           siteName,
				"borealis.io/site-slug":           siteSlug,
				"borealis.io/route-owner":         "job-scheduler",
				"borealis.io/network-mode":        "cluster-ip",
				"borealis.io/service-dns":         siteWorkerServiceDNSName(serviceName, o.namespace),
				"borealis.io/remote-ops-port":     fmt.Sprintf("%d", remoteOpsPort),
				"borealis.io/remote-desktop-port": fmt.Sprintf("%d", remoteDesktopPort),
			},
		},
		"spec": map[string]any{
			"type":     "ClusterIP",
			"selector": labels,
			"ports": []map[string]any{
				{
					"name":       "remote-ops",
					"port":       remoteOpsPort,
					"targetPort": "remote-ops",
					"protocol":   "TCP",
				},
				{
					"name":       "remote-desktop",
					"port":       remoteDesktopPort,
					"targetPort": "remote-desktop",
					"protocol":   "TCP",
				},
			},
		},
	}
}

func (o *borealisOperator) siteWorkerPodManifest(podName string, serviceName string, serviceHost string, siteID int64, siteName string, siteSlug string, workerGUID string, imageRef string, profileName string, profile borealisOperatorResourceProfile, remoteOpsPort int64, remoteDesktopPort int64) map[string]any {
	projectRoot := envDefault("BOREALIS_PROJECT_ROOT", "/opt/Borealis")
	apiRoot := filepath.Join(projectRoot, "Engine", "Services", "api-backend")
	runtimeSecretName := envDefault("BOREALIS_SITE_WORKER_RUNTIME_SECRET_NAME", "borealis-site-worker-runtime-env")
	runtimeConfigHash := strings.TrimSpace(os.Getenv("BOREALIS_SITE_WORKER_RUNTIME_CONFIG_HASH"))
	logRoot := filepath.Join(apiRoot, "logs", "site-workers")
	runtimeUID := borealisOperatorRuntimeIDEnv("BOREALIS_ENGINE_RUNTIME_OWNER_UID", 64646)
	runtimeGID := borealisOperatorRuntimeIDEnv("BOREALIS_ENGINE_RUNTIME_OWNER_GID", 64646)
	serviceHost = firstText(strings.TrimSpace(serviceHost), siteWorkerServiceDNSName(serviceName, o.namespace))
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      podName,
			"namespace": o.namespace,
			"labels": map[string]string{
				"app.kubernetes.io/name":       "site-worker",
				"app.kubernetes.io/part-of":    "borealis",
				"app.kubernetes.io/managed-by": "borealis-operator",
				"app.kubernetes.io/component":  "site-worker",
				"borealis.io/workload":         "site-worker",
				"borealis.io/worker-guid":      workerGUID,
				"borealis.io/site-id":          fmt.Sprintf("%d", siteID),
				"borealis.io/resource-profile": profileName,
			},
			"annotations": map[string]string{
				"borealis.io/image-ref":           imageRef,
				"borealis.io/created-at":          time.Now().UTC().Format(time.RFC3339Nano),
				"borealis.io/stage":               "site-worker-migration",
				"borealis.io/site-name":           siteName,
				"borealis.io/site-slug":           siteSlug,
				"borealis.io/route-owner":         "job-scheduler",
				"borealis.io/network-mode":        "cluster-ip",
				"borealis.io/service-name":        serviceName,
				"borealis.io/service-dns":         siteWorkerServiceDNSName(serviceName, o.namespace),
				"borealis.io/remote-ops-host":     serviceHost,
				"borealis.io/remote-ops-port":     fmt.Sprintf("%d", remoteOpsPort),
				"borealis.io/remote-desktop-port": fmt.Sprintf("%d", remoteDesktopPort),
				"borealis.io/runtime-config-hash": runtimeConfigHash,
			},
		},
		"spec": map[string]any{
			"automountServiceAccountToken":  false,
			"enableServiceLinks":            false,
			"hostNetwork":                   false,
			"dnsPolicy":                     "ClusterFirst",
			"restartPolicy":                 "OnFailure",
			"terminationGracePeriodSeconds": int64(60),
			"affinity": map[string]any{
				"nodeAffinity": map[string]any{
					"requiredDuringSchedulingIgnoredDuringExecution": map[string]any{
						"nodeSelectorTerms": []map[string]any{
							{
								"matchExpressions": []map[string]any{
									{
										"key":      "borealis.io/application-state",
										"operator": "In",
										"values":   []string{"active"},
									},
								},
							},
						},
					},
				},
			},
			"securityContext": map[string]any{
				"runAsNonRoot": true,
				"runAsUser":    runtimeUID,
				"runAsGroup":   runtimeGID,
				"fsGroup":      runtimeGID,
				"seccompProfile": map[string]any{
					"type": "RuntimeDefault",
				},
			},
			"initContainers": []map[string]any{
				{
					"name":            "prepare-site-worker-logs",
					"image":           imageRef,
					"imagePullPolicy": "IfNotPresent",
					"command":         []string{"sh", "-c"},
					"args": []string{
						fmt.Sprintf("mkdir -p %s && chown %d:%d %s && chmod 0775 %s", shellQuote(logRoot), runtimeUID, runtimeGID, shellQuote(logRoot), shellQuote(logRoot)),
					},
					"securityContext": map[string]any{
						"runAsNonRoot":             false,
						"runAsUser":                int64(0),
						"runAsGroup":               int64(0),
						"allowPrivilegeEscalation": false,
						"readOnlyRootFilesystem":   true,
					},
					"volumeMounts": []map[string]any{
						{"name": "api-logs-site-workers", "mountPath": logRoot},
					},
				},
			},
			"containers": []map[string]any{
				{
					"name":            "site-worker",
					"image":           imageRef,
					"imagePullPolicy": "IfNotPresent",
					"ports": []map[string]any{
						{"name": "remote-ops", "containerPort": remoteOpsPort, "protocol": "TCP"},
						{"name": "remote-desktop", "containerPort": remoteDesktopPort, "protocol": "TCP"},
					},
					"startupProbe": map[string]any{
						"httpGet": map[string]any{
							"path": "/startup",
							"port": "remote-ops",
						},
						"periodSeconds":    int64(2),
						"timeoutSeconds":   int64(1),
						"failureThreshold": int64(60),
					},
					"readinessProbe": map[string]any{
						"httpGet": map[string]any{
							"path": "/ready",
							"port": "remote-ops",
						},
						"periodSeconds":    int64(2),
						"timeoutSeconds":   int64(1),
						"failureThreshold": int64(3),
					},
					"livenessProbe": map[string]any{
						"httpGet": map[string]any{
							"path": "/live",
							"port": "remote-ops",
						},
						"initialDelaySeconds": int64(130),
						"periodSeconds":       int64(10),
						"timeoutSeconds":      int64(2),
						"failureThreshold":    int64(3),
					},
					"lifecycle": map[string]any{"preStop": map[string]any{"exec": map[string]any{"command": []string{"sh", "-c", borealisOperatorTransientDrain}}}},
					"envFrom": []map[string]any{
						{"secretRef": map[string]any{"name": runtimeSecretName}},
					},
					"env": []map[string]any{
						{"name": "BOREALIS_PROCESS_ROLE", "value": "site-worker"},
						{"name": "BOREALIS_SITE_WORKER_GUID", "value": workerGUID},
						{"name": "BOREALIS_SITE_WORKER_SITE_ID", "value": fmt.Sprintf("%d", siteID)},
						{"name": "BOREALIS_SITE_WORKER_CONTAINER_NAME", "value": podName},
						{"name": "BOREALIS_SITE_WORKER_SERVICE_NAME", "value": serviceName},
						{"name": "BOREALIS_SITE_WORKER_BIND_HOST", "value": "0.0.0.0"},
						{"name": "BOREALIS_SITE_WORKER_REMOTE_OPS_HOST", "value": serviceHost},
						{"name": "BOREALIS_SITE_WORKER_REMOTE_OPS_PORT", "value": fmt.Sprintf("%d", remoteOpsPort)},
						{"name": "BOREALIS_SITE_WORKER_REMOTE_DESKTOP_PORT", "value": fmt.Sprintf("%d", remoteDesktopPort)},
						{"name": "BOREALIS_SITE_WORKER_ROUTE_FILE_WRITES", "value": "0"},
						{"name": "BOREALIS_SITE_WORKER_RESOURCE_PROFILE", "value": profileName},
						{"name": "BOREALIS_SITE_WORKER_RUNTIME_CONFIG_HASH", "value": runtimeConfigHash},
						{"name": "BOREALIS_K3S_SITE_WORKER_BRIDGE", "value": "1"},
						{"name": "BOREALIS_LOG_FILE", "value": filepath.Join(logRoot, workerGUID+".log")},
						{"name": "BOREALIS_ERROR_LOG_FILE", "value": filepath.Join(logRoot, workerGUID+"-error.log")},
						{"name": "BOREALIS_API_LOG_FILE", "value": filepath.Join(logRoot, workerGUID+"-api.log")},
						{"name": "BOREALIS_VPN_TUNNEL_LOG_FILE", "value": filepath.Join(logRoot, workerGUID+"-vpn.log")},
						{"name": "BOREALIS_WIREGUARD_LOG_FILE", "value": filepath.Join(logRoot, workerGUID+"-vpn.log")},
						{"name": "HOME", "value": "/tmp"},
					},
					"resources": map[string]any{
						"requests": map[string]string{
							"cpu":    profile.RequestCPU,
							"memory": profile.RequestMemory,
						},
						"limits": map[string]string{
							"cpu":    profile.LimitCPU,
							"memory": profile.LimitMemory,
						},
					},
					"securityContext": map[string]any{
						"allowPrivilegeEscalation": false,
						"readOnlyRootFilesystem":   true,
						"capabilities": map[string]any{
							"drop": []string{"ALL"},
						},
					},
					"volumeMounts": []map[string]any{
						{"name": "tmp", "mountPath": "/tmp"},
						{"name": "host-localtime", "mountPath": "/etc/localtime", "readOnly": true},
						{"name": "host-zoneinfo", "mountPath": "/usr/share/zoneinfo", "readOnly": true},
						{"name": "api-logs-site-workers", "mountPath": filepath.Join(apiRoot, "logs", "site-workers")},
						{"name": "api-cache", "mountPath": filepath.Join(apiRoot, "cache")},
						{"name": "api-config", "mountPath": filepath.Join(apiRoot, "config"), "readOnly": true},
						{"name": "api-secrets", "mountPath": filepath.Join(apiRoot, "secrets"), "readOnly": true},
					},
				},
			},
			"volumes": []map[string]any{
				{
					"name": "tmp",
					"emptyDir": map[string]any{
						"medium":    "Memory",
						"sizeLimit": "128Mi",
					},
				},
				borealisOperatorHostPathVolume("api-logs-site-workers", filepath.Join(apiRoot, "logs", "site-workers"), "DirectoryOrCreate"),
				borealisOperatorHostPathVolume("api-cache", filepath.Join(apiRoot, "cache"), "DirectoryOrCreate"),
				borealisOperatorHostPathVolume("api-config", filepath.Join(apiRoot, "config"), "Directory"),
				borealisOperatorHostPathVolume("api-secrets", filepath.Join(apiRoot, "secrets"), "Directory"),
				borealisOperatorHostPathVolume("host-localtime", "/etc/localtime", "File"),
				borealisOperatorHostPathVolume("host-zoneinfo", "/usr/share/zoneinfo", "Directory"),
			},
		},
	}
}

func borealisOperatorHostPathVolume(name string, path string, pathType string) map[string]any {
	return map[string]any{
		"name": name,
		"hostPath": map[string]any{
			"path": path,
			"type": pathType,
		},
	}
}

func borealisOperatorRuntimeIDEnv(name string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" || value == "0" {
		return fallback
	}
	var parsed int64
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return fallback
		}
		parsed = parsed*10 + int64(ch-'0')
	}
	if parsed <= 0 {
		return fallback
	}
	return parsed
}

func (o *borealisOperator) kubeListItems(ctx context.Context, apiGroup string, resource string) ([]map[string]any, error) {
	var payload map[string]any
	if err := o.kube.getJSON(ctx, o.kubePath(apiGroup, resource, ""), &payload); err != nil {
		return nil, err
	}
	items, _ := payload["items"].([]any)
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if mapped, ok := item.(map[string]any); ok {
			result = append(result, mapped)
		}
	}
	return result, nil
}

func (o *borealisOperator) kubeGet(ctx context.Context, apiGroup string, resource string, name string) (map[string]any, error) {
	var payload map[string]any
	if err := o.kube.getJSON(ctx, o.kubePath(apiGroup, resource, name), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (o *borealisOperator) kubePath(apiGroup string, resource string, name string) string {
	resource = strings.Trim(resource, "/")
	name = strings.Trim(name, "/")
	if apiGroup == "apps" {
		if name != "" {
			return fmt.Sprintf("/apis/apps/v1/namespaces/%s/%s/%s", o.namespace, resource, name)
		}
		return fmt.Sprintf("/apis/apps/v1/namespaces/%s/%s", o.namespace, resource)
	}
	if apiGroup == "metrics.k8s.io" {
		if name != "" {
			return fmt.Sprintf("/apis/metrics.k8s.io/v1beta1/namespaces/%s/%s/%s", o.namespace, resource, name)
		}
		return fmt.Sprintf("/apis/metrics.k8s.io/v1beta1/namespaces/%s/%s", o.namespace, resource)
	}
	if name != "" {
		return fmt.Sprintf("/api/v1/namespaces/%s/%s/%s", o.namespace, resource, name)
	}
	return fmt.Sprintf("/api/v1/namespaces/%s/%s", o.namespace, resource)
}

func summarizeKubernetesWorkload(kind string, item map[string]any) map[string]any {
	metadata := nestedMap(item, "metadata")
	spec := nestedMap(item, "spec")
	status := nestedMap(item, "status")
	labels := mapStringAny(metadata["labels"])
	replicas := coerceInt64(spec["replicas"])
	return map[string]any{
		"kind":               kind,
		"name":               cleanText(metadata["name"]),
		"namespace":          cleanText(metadata["namespace"]),
		"labels":             labels,
		"service_key":        firstText(cleanText(labels["borealis.io/service-key"]), cleanText(metadata["name"])),
		"replicas":           replicas,
		"ready_replicas":     coerceInt64(status["readyReplicas"]),
		"available_replicas": coerceInt64(status["availableReplicas"]),
		"updated_replicas":   coerceInt64(status["updatedReplicas"]),
		"desired_ready":      replicas > 0 && coerceInt64(status["readyReplicas"]) >= replicas,
		"managed_by":         cleanText(labels["app.kubernetes.io/managed-by"]),
	}
}

func summarizeKubernetesPod(item map[string]any) map[string]any {
	metadata := nestedMap(item, "metadata")
	status := nestedMap(item, "status")
	labels := mapStringAny(metadata["labels"])
	return map[string]any{
		"name":          cleanText(metadata["name"]),
		"namespace":     cleanText(metadata["namespace"]),
		"labels":        labels,
		"phase":         cleanText(status["phase"]),
		"node_name":     cleanText(status["nodeName"]),
		"pod_ip":        cleanText(status["podIP"]),
		"started_at":    cleanText(status["startTime"]),
		"ready":         kubernetesPodReady(status),
		"restart_count": kubernetesPodRestartCount(status),
	}
}

func summarizeBorealisSiteWorkerPod(pod map[string]any) map[string]any {
	metadata := nestedMap(pod, "metadata")
	labels := mapStringAny(metadata["labels"])
	annotations := mapStringAny(metadata["annotations"])
	spec := nestedMap(pod, "spec")
	status := nestedMap(pod, "status")
	workerGUID := cleanText(labels["borealis.io/worker-guid"])
	siteID := coerceInt64(labels["borealis.io/site-id"])
	remoteOpsPort := coerceInt64(firstNonEmptyAny(annotations["borealis.io/remote-ops-port"], labels["borealis.io/remote-ops-port"]))
	remoteDesktopPort := coerceInt64(firstNonEmptyAny(annotations["borealis.io/remote-desktop-port"], labels["borealis.io/remote-desktop-port"]))
	summary := summarizeKubernetesPod(pod)
	createdAt := kubernetesTimestampUnix(cleanText(metadata["creationTimestamp"]))
	if createdAt <= 0 {
		createdAt = kubernetesTimestampUnix(cleanText(annotations["borealis.io/created-at"]))
	}
	phase := cleanText(status["phase"])
	summary["site_id"] = siteID
	summary["worker_guid"] = workerGUID
	summary["container_name"] = firstText(cleanText(metadata["name"]), "site-worker-"+workerGUID)
	summary["configured_image"] = firstText(cleanText(annotations["borealis.io/image-ref"]), kubernetesPodContainerImage(spec, "site-worker"))
	summary["resource_profile"] = cleanText(labels["borealis.io/resource-profile"])
	summary["remote_ops_port"] = remoteOpsPort
	summary["remote_desktop_port"] = remoteDesktopPort
	summary["remote_ops_host"] = firstText(cleanText(annotations["borealis.io/remote-ops-host"]), "127.0.0.1")
	summary["network_mode"] = firstText(cleanText(annotations["borealis.io/network-mode"]), "host-loopback")
	summary["created_at"] = createdAt
	summary["kubernetes_phase"] = phase
	summary["docker_state"] = phase
	summary["lifecycle_owner"] = "borealis-operator"
	return summary
}

func summarizeBorealisSiteWorkerService(service map[string]any) map[string]any {
	metadata := nestedMap(service, "metadata")
	labels := mapStringAny(metadata["labels"])
	annotations := mapStringAny(metadata["annotations"])
	spec := nestedMap(service, "spec")
	name := cleanText(metadata["name"])
	namespace := cleanText(metadata["namespace"])
	if namespace == "" {
		namespace = "borealis"
	}
	ports := []map[string]any{}
	for _, rawPort := range schedulerAnyList(spec["ports"]) {
		port := mapStringAny(rawPort)
		if len(port) == 0 {
			continue
		}
		ports = append(ports, map[string]any{
			"name":        cleanText(port["name"]),
			"port":        coerceInt64(port["port"]),
			"target_port": cleanText(port["targetPort"]),
			"protocol":    firstText(cleanText(port["protocol"]), "TCP"),
		})
	}
	return map[string]any{
		"name":         name,
		"namespace":    namespace,
		"labels":       labels,
		"annotations":  annotations,
		"worker_guid":  cleanText(labels["borealis.io/worker-guid"]),
		"site_id":      coerceInt64(labels["borealis.io/site-id"]),
		"cluster_ip":   cleanText(spec["clusterIP"]),
		"dns_name":     firstText(cleanText(annotations["borealis.io/service-dns"]), siteWorkerServiceDNSName(name, namespace)),
		"network_mode": firstText(cleanText(annotations["borealis.io/network-mode"]), "cluster-ip"),
		"service_type": firstText(cleanText(spec["type"]), "ClusterIP"),
		"ports":        ports,
	}
}

func borealisSiteWorkerServiceSummariesByGUID(services []map[string]any) map[string]map[string]any {
	result := map[string]map[string]any{}
	for _, service := range services {
		metadata := nestedMap(service, "metadata")
		labels := mapStringAny(metadata["labels"])
		workerGUID := cleanText(labels["borealis.io/worker-guid"])
		if workerGUID == "" {
			continue
		}
		if strings.ToLower(cleanText(labels["app.kubernetes.io/component"])) != "site-worker" {
			continue
		}
		if cleanText(labels["app.kubernetes.io/managed-by"]) != "borealis-operator" {
			continue
		}
		result[workerGUID] = summarizeBorealisSiteWorkerService(service)
	}
	return result
}

func attachBorealisSiteWorkerServiceSummary(worker map[string]any, service map[string]any, namespace string) {
	if worker == nil || len(service) == 0 {
		return
	}
	serviceName := cleanText(service["name"])
	clusterIP := cleanText(service["cluster_ip"])
	dnsName := firstText(cleanText(service["dns_name"]), siteWorkerServiceDNSName(serviceName, namespace))
	serviceHost := firstText(clusterIP, dnsName)
	if serviceName != "" {
		worker["service_name"] = serviceName
	}
	if clusterIP != "" {
		worker["service_cluster_ip"] = clusterIP
	}
	if dnsName != "" {
		worker["service_dns"] = dnsName
	}
	if serviceHost != "" {
		worker["remote_ops_host"] = serviceHost
	}
	worker["network_mode"] = "cluster-ip"
	worker["service"] = service
}

func attachKubernetesPodMetrics(summary map[string]any, pod map[string]any, metrics map[string]any) {
	if summary == nil || pod == nil || metrics == nil {
		return
	}
	cpuCores, memoryBytes := kubernetesPodMetricUsage(metrics, "site-worker")
	if cpuCores <= 0 && memoryBytes <= 0 {
		return
	}
	spec := nestedMap(pod, "spec")
	memoryLimitBytes := kubernetesPodContainerMemoryLimitBytes(spec, "site-worker")
	cpuLimitCores := kubernetesPodContainerCPULimitCores(spec, "site-worker")
	memoryPercent := float64(0)
	if memoryLimitBytes > 0 && memoryBytes >= 0 {
		memoryPercent = round2((float64(memoryBytes) / float64(memoryLimitBytes)) * 100)
	}
	timestamp := cleanText(metrics["timestamp"])
	window := cleanText(metrics["window"])
	stats := map[string]any{
		"source":             "metrics.k8s.io",
		"cpu_percent":        round2(cpuCores * 100),
		"memory_usage_bytes": maxInt64(memoryBytes, 0),
		"memory_limit_bytes": maxInt64(memoryLimitBytes, 0),
		"memory_percent":     memoryPercent,
		"net_input_bytes":    int64(0),
		"net_output_bytes":   int64(0),
		"block_input_bytes":  int64(0),
		"block_output_bytes": int64(0),
		"pids":               int64(0),
	}
	if timestamp != "" {
		stats["read_at"] = timestamp
	}
	if window != "" {
		stats["window"] = window
	}
	kubernetesMetrics := map[string]any{
		"source":                 "metrics.k8s.io",
		"timestamp":              timestamp,
		"window":                 window,
		"cpu_usage_cores":        cpuCores,
		"cpu_usage_millicores":   round2(cpuCores * 1000),
		"cpu_limit_cores":        cpuLimitCores,
		"memory_usage_bytes":     maxInt64(memoryBytes, 0),
		"memory_limit_bytes":     maxInt64(memoryLimitBytes, 0),
		"memory_usage_percent":   memoryPercent,
		"network_metrics_status": "unavailable",
		"disk_metrics_status":    "unavailable",
	}
	summary["docker_stats"] = stats
	summary["kubernetes_metrics"] = kubernetesMetrics
	summary["container_metrics_source"] = "metrics.k8s.io"
}

func kubernetesPodMetricUsage(metrics map[string]any, preferredContainer string) (float64, int64) {
	containers, _ := metrics["containers"].([]any)
	var totalCPU float64
	var totalMemory int64
	var foundPreferred bool
	for _, rawContainer := range containers {
		container := mapStringAny(rawContainer)
		if len(container) == 0 {
			continue
		}
		name := cleanText(container["name"])
		if preferredContainer != "" && name == preferredContainer {
			usage := mapStringAny(container["usage"])
			return parseKubernetesCPUQuantityCores(usage["cpu"]), parseKubernetesByteQuantity(usage["memory"])
		}
		usage := mapStringAny(container["usage"])
		totalCPU += parseKubernetesCPUQuantityCores(usage["cpu"])
		totalMemory += parseKubernetesByteQuantity(usage["memory"])
		if preferredContainer == "" || name == preferredContainer {
			foundPreferred = true
		}
	}
	if foundPreferred || preferredContainer == "" {
		return totalCPU, totalMemory
	}
	return totalCPU, totalMemory
}

func kubernetesPodContainerMemoryLimitBytes(spec map[string]any, containerName string) int64 {
	return parseKubernetesByteQuantity(kubernetesPodContainerResourceQuantity(spec, containerName, "limits", "memory"))
}

func kubernetesPodContainerCPULimitCores(spec map[string]any, containerName string) float64 {
	return parseKubernetesCPUQuantityCores(kubernetesPodContainerResourceQuantity(spec, containerName, "limits", "cpu"))
}

func kubernetesPodContainerResourceQuantity(spec map[string]any, containerName string, bucket string, resource string) any {
	containers, _ := spec["containers"].([]any)
	for _, rawContainer := range containers {
		container := mapStringAny(rawContainer)
		if len(container) == 0 || cleanText(container["name"]) != containerName {
			continue
		}
		resources := mapStringAny(container["resources"])
		values := mapStringAny(resources[bucket])
		return values[resource]
	}
	return nil
}

func parseKubernetesCPUQuantityCores(value any) float64 {
	raw := strings.TrimSpace(cleanText(value))
	if raw == "" {
		return 0
	}
	multiplier := float64(1)
	numeric := raw
	for _, unit := range []struct {
		suffix     string
		multiplier float64
	}{
		{"n", 0.000000001},
		{"u", 0.000001},
		{"m", 0.001},
	} {
		if strings.HasSuffix(raw, unit.suffix) {
			multiplier = unit.multiplier
			numeric = strings.TrimSuffix(raw, unit.suffix)
			break
		}
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(numeric), 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed * multiplier
}

func parseKubernetesByteQuantity(value any) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return parsed
		}
		floatParsed, err := typed.Float64()
		if err == nil {
			return int64(floatParsed)
		}
	}
	raw := strings.TrimSpace(cleanText(value))
	if raw == "" {
		return 0
	}
	units := []struct {
		suffix     string
		multiplier float64
	}{
		{"Ki", 1024},
		{"Mi", 1024 * 1024},
		{"Gi", 1024 * 1024 * 1024},
		{"Ti", 1024 * 1024 * 1024 * 1024},
		{"Pi", 1024 * 1024 * 1024 * 1024 * 1024},
		{"Ei", 1024 * 1024 * 1024 * 1024 * 1024 * 1024},
		{"K", 1000},
		{"M", 1000 * 1000},
		{"G", 1000 * 1000 * 1000},
		{"T", 1000 * 1000 * 1000 * 1000},
		{"P", 1000 * 1000 * 1000 * 1000 * 1000},
		{"E", 1000 * 1000 * 1000 * 1000 * 1000 * 1000},
	}
	multiplier := float64(1)
	numeric := raw
	for _, unit := range units {
		if strings.HasSuffix(raw, unit.suffix) {
			multiplier = unit.multiplier
			numeric = strings.TrimSuffix(raw, unit.suffix)
			break
		}
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(numeric), 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return int64(parsed * multiplier)
}

func kubernetesPodContainerImage(spec map[string]any, containerName string) string {
	containers, _ := spec["containers"].([]any)
	for _, raw := range containers {
		container, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if cleanText(container["name"]) == containerName {
			return cleanText(container["image"])
		}
	}
	return ""
}

func kubernetesTimestampUnix(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0
	}
	return parsed.Unix()
}

func kubernetesPodReady(status map[string]any) bool {
	conditions, _ := status["conditions"].([]any)
	for _, item := range conditions {
		condition, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if cleanText(condition["type"]) == "Ready" && cleanText(condition["status"]) == "True" {
			return true
		}
	}
	return false
}

func kubernetesPodRestartCount(status map[string]any) int64 {
	containers, _ := status["containerStatuses"].([]any)
	var total int64
	for _, item := range containers {
		container, ok := item.(map[string]any)
		if !ok {
			continue
		}
		total += coerceInt64(container["restartCount"])
	}
	return total
}

func decodeBorealisOperatorJSON(r *http.Request, out any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

func decodeBorealisOperatorParams(params map[string]any, out any) error {
	if params == nil {
		params = map[string]any{}
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("params must contain one JSON object")
		}
		return err
	}
	return nil
}

func goBorealisOperatorToken(secret []byte) string {
	if len(secret) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(borealisOperatorTokenContext))
	return hex.EncodeToString(mac.Sum(nil))
}

func newInClusterKubernetesAPIClient() (*kubernetesAPIClient, error) {
	host := strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST"))
	port := firstText(strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT_HTTPS")), strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_PORT")))
	if host == "" || port == "" {
		return nil, errors.New("Kubernetes service environment is unavailable")
	}
	token, err := os.ReadFile(filepath.Join(borealisOperatorServiceAccount, "token"))
	if err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(filepath.Join(borealisOperatorServiceAccount, "ca.crt"))
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("failed to load Kubernetes service account CA")
	}
	return &kubernetesAPIClient{
		baseURL: "https://" + net.JoinHostPort(host, port),
		token:   strings.TrimSpace(string(token)),
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
			},
		},
	}, nil
}

func (c *kubernetesAPIClient) getJSON(ctx context.Context, path string, out any) error {
	return c.doJSON(ctx, http.MethodGet, path, nil, "", out, 10*time.Second)
}

func (o *borealisOperator) kubePatch(ctx context.Context, apiGroup string, resource string, name string, body any) (map[string]any, error) {
	var payload map[string]any
	err := o.kube.doJSON(ctx, http.MethodPatch, o.kubePath(apiGroup, resource, name), body, "application/strategic-merge-patch+json", &payload, 30*time.Second)
	return payload, err
}

func (o *borealisOperator) kubeCreate(ctx context.Context, apiGroup string, resource string, body any) (map[string]any, error) {
	var payload map[string]any
	err := o.kube.doJSON(ctx, http.MethodPost, o.kubePath(apiGroup, resource, ""), body, "application/json", &payload, 30*time.Second)
	return payload, err
}

func (o *borealisOperator) kubeDelete(ctx context.Context, apiGroup string, resource string, name string, body any) error {
	var payload map[string]any
	return o.kube.doJSON(ctx, http.MethodDelete, o.kubePath(apiGroup, resource, name), body, "application/json", &payload, 30*time.Second)
}

func (c *kubernetesAPIClient) doJSON(ctx context.Context, method string, path string, body any, contentType string, out any, timeout time.Duration) error {
	if c == nil || c.httpClient == nil {
		return errors.New("Kubernetes client unavailable")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(requestCtx, method, strings.TrimRight(c.baseURL, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", firstText(contentType, "application/json"))
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &kubernetesAPIError{Path: path, StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<20))
		return nil
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

func newBorealisOperatorClientFromEnv() (*borealisOperatorClient, bool) {
	secret := strings.TrimSpace(os.Getenv("BOREALIS_OPERATOR_SECRET"))
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("BOREALIS_OPERATOR_BASE_URL")), "/")
	if secret == "" || baseURL == "" {
		return nil, false
	}
	return &borealisOperatorClient{
		baseURL:    baseURL,
		token:      goBorealisOperatorToken([]byte(secret)),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}, true
}

func (c *borealisOperatorClient) command(ctx context.Context, verb string, params map[string]any) (map[string]any, error) {
	if c == nil || c.httpClient == nil {
		return nil, errors.New("borealis-operator client unavailable")
	}
	raw, err := json.Marshal(borealisOperatorCommandRequest{Verb: verb, Params: params})
	if err != nil {
		return nil, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, c.baseURL+"/v1/command", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(borealisOperatorTokenHeader, c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload map[string]any
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload)
	if resp.StatusCode >= 400 {
		return payload, fmt.Errorf("borealis-operator %s returned HTTP %d", verb, resp.StatusCode)
	}
	return payload, nil
}

func (c *borealisOperatorClient) rolloutKnownWorkload(ctx context.Context, serviceKey string, imageRef string) (map[string]any, error) {
	return c.command(ctx, "RolloutKnownWorkload", map[string]any{
		"service_key": serviceKey,
		"image_ref":   imageRef,
	})
}

func (c *borealisOperatorClient) restartKnownWorkload(ctx context.Context, serviceKey string) (map[string]any, error) {
	return c.command(ctx, "RestartKnownWorkload", map[string]any{
		"service_key": serviceKey,
	})
}

func (c *borealisOperatorClient) scaleKnownWorkload(ctx context.Context, serviceKey string, replicas int64) (map[string]any, error) {
	return c.command(ctx, "ScaleKnownWorkload", map[string]any{
		"service_key": serviceKey,
		"replicas":    replicas,
	})
}

func (c *borealisOperatorClient) listSiteWorkers(ctx context.Context) ([]map[string]any, error) {
	payload, err := c.command(ctx, "ListSiteWorkers", nil)
	if err != nil {
		return nil, err
	}
	result := schedulerAnyMap(payload["result"])
	items, _ := result["workers"].([]any)
	workers := make([]map[string]any, 0, len(items))
	for _, item := range items {
		worker, ok := item.(map[string]any)
		if ok {
			workers = append(workers, worker)
		}
	}
	return workers, nil
}

func (c *borealisOperatorClient) launchSiteWorker(ctx context.Context, req borealisOperatorLaunchSiteWorkerRequest) (map[string]any, error) {
	return c.command(ctx, "LaunchSiteWorker", map[string]any{
		"site_id":             req.SiteID,
		"site_name":           req.SiteName,
		"worker_guid":         req.WorkerGUID,
		"image_ref":           req.ImageRef,
		"resource_profile":    req.ResourceProfile,
		"remote_ops_port":     req.RemoteOpsPort,
		"remote_desktop_port": req.RemoteDesktopPort,
	})
}

func (c *borealisOperatorClient) retireSiteWorker(ctx context.Context, workerGUID string, reason string) (map[string]any, error) {
	return c.command(ctx, "RetireSiteWorker", map[string]any{
		"worker_guid": workerGUID,
		"reason":      reason,
	})
}

func (c *borealisOperatorClient) startPostRestoreRefresh(ctx context.Context) (map[string]any, error) {
	return c.command(ctx, "StartPostRestoreRefresh", map[string]any{
		"delay_seconds": 2,
	})
}

func runBorealisOperatorClientHealthcheck(ctx context.Context, _ gatewayConfig) error {
	client, configured := newBorealisOperatorClientFromEnv()
	if !configured {
		return errors.New("borealis-operator client is not configured")
	}
	payload, err := client.command(ctx, "GetClusterSummary", nil)
	if err != nil {
		return err
	}
	ok, _ := payload["ok"].(bool)
	if !ok {
		return errors.New("borealis-operator healthcheck returned non-ok payload")
	}
	return nil
}

func registerBorealisOperatorRoutes(mux *http.ServeMux, auth *authService) {
	mux.HandleFunc("/api/server/k3s/operator", borealisOperatorStatusHandler(auth))
}

func borealisOperatorResultValue(response map[string]any, key string) any {
	result, ok := response["result"].(map[string]any)
	if !ok {
		return response["result"]
	}
	return result[key]
}

func borealisOperatorStatusHandler(auth *authService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		if _, failure := requireAdmin(r.Context(), auth, r); failure != nil {
			failure.write(w)
			return
		}
		ctx, cancel := requestTimeout(r.Context(), auth)
		defer cancel()
		client, configured := newBorealisOperatorClientFromEnv()
		payload := map[string]any{
			"configured": configured,
			"base_url":   strings.TrimSpace(os.Getenv("BOREALIS_OPERATOR_BASE_URL")),
		}
		if !configured {
			payload["status"] = "unconfigured"
			writeJSON(w, http.StatusOK, payload)
			return
		}
		summary, err := client.command(ctx, "GetClusterSummary", nil)
		if err != nil {
			payload["status"] = "unreachable"
			payload["error"] = err.Error()
			if summary != nil {
				payload["operator_response"] = summary
			}
			writeJSON(w, http.StatusBadGateway, payload)
			return
		}
		workloads, workloadErr := client.command(ctx, "ListWorkloads", nil)
		siteWorkers, workerErr := client.command(ctx, "ListSiteWorkers", nil)
		payload["status"] = "ready"
		payload["summary"] = summary["result"]
		if workloadErr == nil {
			payload["workloads"] = borealisOperatorResultValue(workloads, "workloads")
		} else {
			payload["workloads_error"] = workloadErr.Error()
		}
		if workerErr == nil {
			payload["site_workers"] = borealisOperatorResultValue(siteWorkers, "workers")
		} else {
			payload["site_workers_error"] = workerErr.Error()
		}
		writeJSON(w, http.StatusOK, payload)
	}
}
