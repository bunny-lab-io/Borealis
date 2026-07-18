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
	"strings"
	"time"
)

const (
	borealisOperatorTokenHeader     = "X-Borealis-Operator-Token"
	borealisOperatorTokenContext    = "borealis-operator-internal-v1"
	borealisOperatorServiceAccount  = "/var/run/secrets/kubernetes.io/serviceaccount"
	borealisOperatorDefaultPort     = "8088"
	borealisOperatorDefaultWorkload = "borealis-operator"
)

var borealisOperatorAllowedVerbs = []string{
	"GetClusterSummary",
	"ListWorkloads",
	"GetWorkloadStatus",
	"ListSiteWorkers",
}

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
		"mutation_verbs":    []any{},
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
	if serviceKey != borealisOperatorDefaultWorkload {
		return nil, http.StatusBadRequest, fmt.Errorf("unsupported workload service_key %q", serviceKey)
	}
	item, err := o.kubeGet(ctx, "apps", "deployments", borealisOperatorDefaultWorkload)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	return summarizeKubernetesWorkload("Deployment", item), http.StatusOK, nil
}

func (o *borealisOperator) listSiteWorkers(ctx context.Context) ([]map[string]any, error) {
	pods, err := o.kubeListItems(ctx, "core", "pods")
	if err != nil {
		return nil, err
	}
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
		workers = append(workers, summarizeKubernetesPod(pod))
	}
	return workers, nil
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
	if c == nil || c.httpClient == nil {
		return errors.New("Kubernetes client unavailable")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, strings.TrimRight(c.baseURL, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("Kubernetes API %s returned HTTP %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
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
			payload["site_workers"] = borealisOperatorResultValue(siteWorkers, "site_workers")
		} else {
			payload["site_workers_error"] = workerErr.Error()
		}
		writeJSON(w, http.StatusOK, payload)
	}
}
