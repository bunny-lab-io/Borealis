import React, { useCallback, useEffect, useMemo, useState } from "react";
import { useLoaderData } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControl,
  InputLabel,
  MenuItem,
  Paper,
  Select,
  Stack,
  Tab,
  Tabs,
  TextField,
  Typography,
} from "@mui/material";
import { HubRounded as ClusterIcon, RefreshRounded as RefreshIcon } from "@mui/icons-material";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import {
  createRouteRequestPlan,
  getRouteErrorMessage,
  requireAdminRequest,
  rethrowIfRouteRedirect,
} from "../app/routes/routeData.js";
import {
  FIELD_CLASS,
  sanitizeSingleLineInput,
  validateInputValue,
} from "../app/utils/inputValidation.js";

const HMR_WARNING =
  "This moves all Borealis application traffic to this node and places every other Engine node in drained standby. Cluster loses application HA until production mode is restored.";
const TABS = ["Overview", "Nodes", "Database", "Updates", "Operations", "Maintenance"];
const CARD_SX = {
  p: 2.25,
  borderRadius: 3,
  border: "1px solid rgba(125,183,255,0.18)",
  background: "linear-gradient(155deg, rgba(13,20,38,0.94), rgba(7,11,26,0.9))",
  color: "#e2e8f0",
};
const NODE_NAME_PATTERN = /^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$/;
const K3S_VERSION_PATTERN = /^v\d+\.\d+\.\d+\+k3s\d+$/;

function validIPv4(value) {
  const parts = String(value || "").split(".");
  return parts.length === 4 && parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255 && String(Number(part)) === part);
}

export async function loadClusterManagementPageData(request) {
  const progress = createRouteRequestPlan(request, 3);
  try {
    await requireAdminRequest(request, progress);
    const cluster = await progress.fetchJson("/api/server/cluster");
    let releases = { releases: [] };
    let releaseError = "";
    try {
      releases = await progress.fetchJson("/api/server/cluster/releases");
    } catch (error) {
      // Cluster state remains usable during GitHub outage.
      releaseError = getRouteErrorMessage(error, "Stable release catalog could not be loaded.");
    }
    return { cluster, releases, releaseError, initialError: "" };
  } catch (error) {
    rethrowIfRouteRedirect(error);
    return { cluster: null, releases: { releases: [] }, initialError: getRouteErrorMessage(error, "Cluster state could not be loaded.") };
  } finally {
    progress.finalize();
  }
}

function valueLabel(value, fallback = "—") {
  const text = String(value ?? "").trim();
  return text || fallback;
}

function StatusChip({ value, prefix = "" }) {
  const label = valueLabel(value, "Unknown");
  const normalized = label.toLowerCase();
  const color = normalized.includes("healthy") || normalized.includes("active") || normalized.includes("passed")
    ? "success"
    : normalized.includes("degraded") || normalized.includes("failed")
      ? "error"
      : "warning";
  return <Chip size="small" color={color} variant="outlined" label={prefix ? `${prefix}: ${label}` : label} />;
}

function operatorNodeLabel(value, nodes) {
  const id = String(value || "").trim();
  return nodes.find((node) => node?.id === id)?.node_name || value;
}

function readableIdentifier(value) {
  return String(value || "unknown").replaceAll("_", " ");
}

function operationErrorSummary(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  const httpMatch = text.match(/^(.*?returned HTTP \d+)/);
  const summary = httpMatch?.[1] || text.split("\n", 1)[0];
  return summary.length > 220 ? `${summary.slice(0, 217)}...` : summary;
}

function KeyValueGrid({ entries }) {
  return (
    <Box sx={{ display: "grid", gridTemplateColumns: { xs: "1fr", md: "repeat(3, minmax(0, 1fr))" }, gap: 1.5 }}>
      {entries.map(([label, value]) => (
        <Paper key={label} sx={CARD_SX}>
          <Typography variant="caption" sx={{ color: "#94a3b8", textTransform: "uppercase", letterSpacing: 0.8 }}>{label}</Typography>
          <Typography sx={{ mt: 0.75, fontWeight: 650, overflowWrap: "anywhere" }}>{valueLabel(value)}</Typography>
        </Paper>
      ))}
    </Box>
  );
}

function NodeCard({ node, onEmergencyRemove, onMaintenance, onUpdate, onRemove, emergencyRemovalEnabled, normalOperationsEnabled, removalEnabled }) {
  const roles = node?.roles || {};
  const roleLabels = Object.entries(roles).filter(([, active]) => active === true).map(([role]) => readableIdentifier(role));
  return (
    <Paper sx={CARD_SX}>
      <Stack direction={{ xs: "column", md: "row" }} justifyContent="space-between" gap={2}>
        <Box>
          <Typography variant="h6">{valueLabel(node?.node_name)}</Typography>
          <Typography variant="body2" sx={{ color: "#94a3b8" }}>{valueLabel(node?.management_ip)} · {valueLabel(node?.release_tag, "No release")} · K3s {valueLabel(roles?.k3s_version, "not observed")}</Typography>
        </Box>
        <Stack direction="row" spacing={1} flexWrap="wrap">
          <StatusChip prefix="Membership" value={node?.membership_state} />
          <StatusChip prefix="Application" value={node?.application_state} />
        </Stack>
      </Stack>
      <Typography variant="body2" sx={{ mt: 1.5, color: "#cbd5e1" }}>
        Roles: {roleLabels.length ? roleLabels.join(", ") : "standby"}
      </Typography>
      <Typography variant="body2" sx={{ mt: 0.75, color: "#cbd5e1" }}>
        Probes: {Object.entries(node?.probe_health || {}).map(([name, status]) => `${name}=${status}`).join(", ") || "not reported"}
      </Typography>
      <Stack direction="row" spacing={1} sx={{ mt: 2 }}>
        <Button size="small" variant="outlined" disabled={!normalOperationsEnabled && node?.application_state !== "drained"} onClick={() => onMaintenance(node)}>{node?.application_state === "drained" ? "Exit Maintenance" : "Enter Maintenance"}</Button>
        <Button size="small" variant="outlined" disabled={!normalOperationsEnabled} onClick={() => onUpdate(node)}>Update Node</Button>
        <Button size="small" color="warning" variant="outlined" disabled={!removalEnabled} onClick={() => onRemove(node)}>Remove Pair</Button>
        <Button size="small" color="error" variant="outlined" disabled={!emergencyRemovalEnabled} onClick={() => onEmergencyRemove(node)}>Emergency Remove</Button>
      </Stack>
    </Paper>
  );
}

function OperationCard({ operation, busy, mutate }) {
  const supersededBy = String(operation?.superseded_by || "").trim();
  const displayState = supersededBy ? "superseded" : operation?.state;
  const errorSummary = operationErrorSummary(operation?.error);
  return (
    <Paper sx={CARD_SX}>
      <Stack direction={{ xs: "column", md: "row" }} justifyContent="space-between" gap={1}>
        <Box sx={{ minWidth: 0 }}>
          <Typography sx={{ fontWeight: 650 }}>{readableIdentifier(operation?.kind)} · {readableIdentifier(operation?.current_step)}</Typography>
          <Typography variant="body2" sx={{ color: "#94a3b8" }}>{operation?.id} · attempt {operation?.attempt}</Typography>
          {supersededBy ? <Typography variant="body2" sx={{ mt: 1, color: "#94a3b8" }}>Superseded by successful operation {supersededBy}. Historical failure retained for audit.</Typography> : null}
          {!supersededBy && errorSummary ? <Typography variant="body2" color="error.light" sx={{ mt: 1 }}>{errorSummary}</Typography> : null}
          {operation?.error ? (
            <Box component="details" sx={{ mt: 1, color: "#94a3b8" }}>
              <Typography component="summary" variant="body2" sx={{ cursor: "pointer", color: "#7dd3fc" }}>Technical details</Typography>
              <Box component="pre" sx={{ m: 0, mt: 1, p: 1.25, maxHeight: 220, overflow: "auto", whiteSpace: "pre-wrap", overflowWrap: "anywhere", fontSize: 12 }}>{operation.error}</Box>
            </Box>
          ) : null}
        </Box>
        <Stack direction="row" spacing={1} alignItems="center">
          <StatusChip value={displayState} />
          {operation?.state === "failed" && !supersededBy ? <Button size="small" disabled={busy} onClick={() => void mutate(`/api/server/cluster/operations/${operation.id}/retry`, { confirmation: "RETRY OPERATION" })}>Retry</Button> : null}
          {["queued", "waiting"].includes(operation?.state) ? <Button size="small" color="warning" disabled={busy} onClick={() => void mutate(`/api/server/cluster/operations/${operation.id}/cancel`, { confirmation: "CANCEL OPERATION" })}>Cancel</Button> : null}
        </Stack>
      </Stack>
    </Paper>
  );
}

export default function ClusterManagement() {
  const loaderData = useLoaderData();
  const notify = useAppNotifications({ title: "Cluster Management", icon: "cluster" });
  const [cluster, setCluster] = useState(loaderData?.cluster || null);
  const [releases, setReleases] = useState(loaderData?.releases?.releases || []);
  const [releaseError, setReleaseError] = useState(loaderData?.releaseError || "");
  const [tab, setTab] = useState(0);
  const [error, setError] = useState(loaderData?.initialError || "");
  const [busy, setBusy] = useState(false);
  const [dialog, setDialog] = useState(null);
  const [confirmation, setConfirmation] = useState("");
  const [reason, setReason] = useState("");
  const [selectedRelease, setSelectedRelease] = useState("");
  const [selectedNode, setSelectedNode] = useState("");
  const [controlVIP, setControlVIP] = useState("");
  const [edgeVIP, setEdgeVIP] = useState("");
  const [nodeName, setNodeName] = useState("");
  const [managementIP, setManagementIP] = useState("");
  const [architecture, setArchitecture] = useState("amd64");
  const [desiredSize, setDesiredSize] = useState(3);
  const [inviteBundle, setInviteBundle] = useState("");
  const [pairedNode, setPairedNode] = useState("");
  const [fencingConfirmation, setFencingConfirmation] = useState("");
  const [k3sTargetVersion, setK3sTargetVersion] = useState("");

  const nodes = useMemo(() => Array.isArray(cluster?.nodes) ? cluster.nodes : [], [cluster]);
  const operations = useMemo(() => Array.isArray(cluster?.operations) ? cluster.operations : [], [cluster]);
  const selectableReleases = useMemo(() => releases.filter((release) => release?.selectable), [releases]);
  const activeSize = Number(cluster?.active_size || 1);
  const desiredMembershipSize = Number(cluster?.desired_size || activeSize);
  const database = cluster?.database || {};
  const databaseRecoveryReady = database?.fully_ready !== false && database?.durability_quorum !== false;
  const applicationCapacityReady = nodes.filter((node) => node?.membership_state === "Active").every((node) => node?.application_state === "active");
  const normalOperationsEnabled = cluster?.status === "Healthy" && databaseRecoveryReady && applicationCapacityReady;
  const rollingUpdateEnabled = (normalOperationsEnabled || cluster?.status === "Mixed Version") && databaseRecoveryReady && applicationCapacityReady;
  const replacementRecovery = activeSize === 2 && desiredMembershipSize === 3 && cluster?.status === "Degraded Quorum";
  const canPrepareMembership = (activeSize === 1 || replacementRecovery) && applicationCapacityReady;
  const canExpandToThree = activeSize === 1;
  const expansionSizes = useMemo(() => Number(cluster?.active_size || 1) === 1 ? [3] : [], [cluster?.active_size]);

  const refresh = useCallback(async ({ quiet = false } = {}) => {
    try {
      const [clusterResponse, releaseResponse] = await Promise.all([
        fetch("/api/server/cluster", { credentials: "include", cache: "no-store" }),
        fetch("/api/server/cluster/releases", { credentials: "include", cache: "no-store" }),
      ]);
      const clusterPayload = await clusterResponse.json().catch(() => ({}));
      const releasePayload = await releaseResponse.json().catch(() => ({}));
      if (!clusterResponse.ok) throw new Error(clusterPayload?.message || "Cluster state request failed.");
      setCluster(clusterPayload);
      if (releaseResponse.ok) {
        setReleases(Array.isArray(releasePayload?.releases) ? releasePayload.releases : []);
        setReleaseError("");
      } else {
        setReleaseError(releasePayload?.message || "Stable release catalog could not be loaded.");
      }
      setError("");
      if (!quiet) void notify("Cluster state refreshed.", { variant: "success" });
    } catch (requestError) {
      if (!quiet) setError(requestError?.message || "Cluster state request failed.");
    }
  }, [notify]);

  useEffect(() => {
    const timer = window.setInterval(() => void refresh({ quiet: true }), 5000);
    return () => window.clearInterval(timer);
  }, [refresh]);

  const mutate = useCallback(async (path, body) => {
    setBusy(true);
    setError("");
    try {
      const response = await fetch(path, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        const validation = Array.isArray(payload?.errors) ? payload.errors.map((item) => item?.message || item).join(" ") : "";
        throw new Error(validation || payload?.message || payload?.error || `Request failed (${response.status}).`);
      }
      setDialog(null);
      setConfirmation("");
      setReason("");
      setPairedNode("");
      setFencingConfirmation("");
      await refresh({ quiet: true });
      void notify(`Cluster operation ${payload?.operation_id || "request"} queued.`, { variant: "success" });
      return payload;
    } catch (requestError) {
      setError(requestError?.message || "Cluster operation failed.");
    } finally {
      setBusy(false);
    }
  }, [notify, refresh]);

  const openAction = useCallback((kind, node = null) => {
    setDialog({ kind, node });
    setConfirmation("");
    setReason("");
    setFencingConfirmation("");
    if (node?.id) setSelectedNode(node.id);
    if (kind === "remove" && node?.id) setPairedNode(nodes.find((candidate) => candidate.id !== node.id && candidate.membership_state === "Active")?.id || "");
  }, [nodes]);

  const submitDialog = useCallback(() => {
    const kind = dialog?.kind;
    const node = dialog?.node;
    const sanitizedReason = sanitizeSingleLineInput(reason).slice(0, 256);
    if (["maintenance", "scale", "remove", "emergency_remove", "switchover", "emergency_failover"].includes(kind)) {
      const validation = validateInputValue("reason", sanitizedReason, FIELD_CLASS.PLAIN_SINGLE_LINE);
      if (validation) return setError(validation);
    }
    if (kind === "hmr_start") return mutate("/api/server/cluster/hmr/start", { node_id: selectedNode, confirmation });
    if (kind === "hmr_exit") return mutate("/api/server/cluster/hmr/exit", { confirmation });
    if (kind === "update_all" || kind === "update_node") {
      const oneNode = Number(cluster?.active_size || 1) === 1;
      return mutate("/api/server/cluster/updates", {
        scope: kind === "update_all" ? "all" : "node",
        node_ids: kind === "update_all" ? [] : [selectedNode],
        release_tag: selectedRelease,
        confirmation,
        maintenance_outage_acknowledgement: oneNode ? "ACCEPT OUTAGE" : "",
      });
    }
    if (kind === "k3s_update") {
      if (!K3S_VERSION_PATTERN.test(k3sTargetVersion) || k3sTargetVersion.length > 32) return setError("K3s target must use stable vX.Y.Z+k3sN form and be no longer than 32 characters.");
      const oneNode = Number(cluster?.active_size || 1) === 1;
      return mutate("/api/server/cluster/updates", {
        update_type: "k3s",
        scope: "all",
        node_ids: [],
        k3s_version: k3sTargetVersion,
        confirmation,
        maintenance_outage_acknowledgement: oneNode ? "ACCEPT OUTAGE" : "",
      });
    }
    if (kind === "maintenance") {
      const action = node?.application_state === "drained" ? "exit" : "enter";
      return mutate(`/api/server/cluster/nodes/${node.id}/maintenance`, { action, reason: sanitizedReason });
    }
    if (kind === "cluster_enable") {
      if (!validIPv4(controlVIP) || !validIPv4(edgeVIP) || controlVIP === edgeVIP || !validIPv4(managementIP)) return setError("Distinct valid IPv4 control-plane and edge VIPs plus valid management IPv4 required.");
      if (!NODE_NAME_PATTERN.test(nodeName)) return setError("Node name must be DNS-label syntax and no longer than 63 characters.");
      return mutate("/api/server/cluster/enable", { control_plane_vip: controlVIP, edge_vip: edgeVIP, node_name: nodeName, management_ip: managementIP, architecture, confirmation });
    }
    if (kind === "invite") {
      if (!canPrepareMembership) return setError("Current release supports node invitations only for one-to-three expansion or degraded-quorum replacement.");
      if (!NODE_NAME_PATTERN.test(nodeName)) return setError("Node name must be DNS-label syntax and no longer than 63 characters.");
      return mutate("/api/server/cluster/invitations", { node_name: nodeName }).then((payload) => setInviteBundle(payload?.invite_bundle || ""));
    }
    if (kind === "scale") {
      if (!canExpandToThree || Number(desiredSize) !== 3) return setError("Current release supports only one-to-three membership expansion.");
      return mutate("/api/server/cluster/membership/scale", { desired_size: 3, reason: sanitizedReason });
    }
    if (kind === "remove") return mutate(`/api/server/cluster/nodes/${node.id}/remove`, { emergency: false, paired_node_id: pairedNode, confirmation, reason: sanitizedReason });
    if (kind === "emergency_remove") return mutate(`/api/server/cluster/nodes/${node.id}/remove`, { emergency: true, confirmation, fencing_confirmation: fencingConfirmation, reason: sanitizedReason });
    if (kind === "switchover") return mutate("/api/server/cluster/postgres/switchover", { target_node_id: selectedNode, confirmation: "", reason: sanitizedReason });
    if (kind === "emergency_failover") return mutate("/api/server/cluster/postgres/emergency-failover", { target_node_id: selectedNode, confirmation, reason: sanitizedReason });
    return undefined;
  }, [architecture, canExpandToThree, canPrepareMembership, cluster?.active_size, confirmation, controlVIP, desiredSize, dialog, edgeVIP, fencingConfirmation, k3sTargetVersion, managementIP, mutate, nodeName, pairedNode, reason, selectedNode, selectedRelease]);

  const pageActions = useMemo(() => [{ id: "cluster-refresh", label: "Refresh", icon: <RefreshIcon />, tone: "secondary", onClick: () => void refresh() }], [refresh]);
  useRoutePageChrome({
    title: "Cluster Management",
    subtitle: "Quorum, role ownership, HMR isolation, node maintenance, and rolling Engine release operations.",
    Icon: ClusterIcon,
    actions: pageActions,
  });

  const leaders = cluster?.leaders || {};
  const configuredDatabaseInstances = Number(database?.configured_instances || activeSize);
  const databaseReadyObserved = database?.ready_instances !== undefined && database?.ready_instances !== null;
  const readyDatabaseInstances = databaseReadyObserved ? Number(database.ready_instances) : null;
  const databaseFullyReady = database?.fully_ready !== false;
  const databaseDurabilityReady = database?.durability_quorum !== false;
  const owner = (value) => operatorNodeLabel(value, nodes);
  return (
    <PageBodyFrame>
      <Stack spacing={2.25} sx={{ p: { xs: 1.5, md: 2.5 } }}>
        {error ? <Alert severity="error" onClose={() => setError("")}>{error}</Alert> : null}
        {cluster?.hmr?.state && cluster.hmr.state !== "inactive" ? <Alert severity="warning"><strong>Cluster-wide non-HA HMR active.</strong> {HMR_WARNING}</Alert> : null}
        {cluster?.status === "Degraded Quorum" ? <Alert severity="error">Cluster degraded. Failed node stays drained; retry or explicit recovery required.</Alert> : null}
        {cluster?.status === "Degraded Database" ? <Alert severity={databaseDurabilityReady ? "warning" : "error"}>PostgreSQL is not fully ready: {readyDatabaseInstances ?? "unknown"} of {configuredDatabaseInstances} instances Ready. Normal cluster-changing operations stay blocked until redundancy recovers; recovery controls remain available.</Alert> : null}
        {!databaseRecoveryReady && cluster?.status !== "Degraded Database" ? <Alert severity={databaseDurabilityReady ? "warning" : "error"}>PostgreSQL recovery remains required even while cluster lifecycle status is {valueLabel(cluster?.status)}. Normal cluster-changing operations stay blocked.</Alert> : null}
        {!applicationCapacityReady ? <Alert severity="warning">One or more active cluster members remain application-drained. Restore drained node or finish explicit recovery before starting normal cluster operations.</Alert> : null}
        <Tabs value={tab} onChange={(_, value) => setTab(value)} variant="scrollable" scrollButtons="auto">
          {TABS.map((label) => <Tab key={label} label={label} />)}
        </Tabs>

        {tab === 0 ? <KeyValueGrid entries={[
          ["Cluster status", cluster?.status], ["Active / desired", `${cluster?.active_size || 1} / ${cluster?.desired_size || 1}`], ["Baseline release", cluster?.baseline_release], ["K3s version", cluster?.k3s_version],
          ["etcd leader", owner(leaders?.etcd_leader)], ["Control VIP owner", owner(leaders?.control_vip_owner)], ["Edge VIP owner", owner(leaders?.edge_vip_owner)],
          ["PostgreSQL primary", owner(leaders?.postgres_primary)], ["Scheduler leader", owner(leaders?.scheduler_leader)], ["WireGuard owner", owner(leaders?.wireguard_owner)],
        ]} /> : null}

        {tab === 1 ? <Stack spacing={1.5}>{nodes.map((node) => <NodeCard key={node.id} node={node} normalOperationsEnabled={normalOperationsEnabled} removalEnabled={normalOperationsEnabled && activeSize === 3 && node?.membership_state === "Active"} emergencyRemovalEnabled={activeSize === 3 && node?.membership_state === "Active"} onMaintenance={(value) => openAction("maintenance", value)} onUpdate={(value) => openAction("update_node", value)} onRemove={(value) => openAction("remove", value)} onEmergencyRemove={(value) => openAction("emergency_remove", value)} />)}</Stack> : null}

        {tab === 2 ? <Stack spacing={2}>
          <KeyValueGrid entries={[["Primary", owner(leaders?.postgres_primary)], ["Configured / ready", `${configuredDatabaseInstances} / ${readyDatabaseInstances ?? "not observed"}`], ["Durability", activeSize > 1 ? `${Number(database?.synchronous_acknowledgements ?? 1)} synchronous acknowledgement` : "Single instance; no standby acknowledgement"], ["Storage", "Strict-local Longhorn, one replica"], ["Snapshots", "Daily, 14 retained + pre-change"], ["Writes", activeSize > 1 ? "Blocked without durability quorum" : "Available while primary is healthy"]]} />
          {databaseReadyObserved && !databaseFullyReady ? <Alert severity={databaseDurabilityReady ? "warning" : "error"}>{readyDatabaseInstances} of {configuredDatabaseInstances} PostgreSQL instances are Ready. {databaseDurabilityReady ? "Writes retain required synchronous durability, but node redundancy is reduced." : "Required durability quorum is unavailable; writes may be blocked."} {database?.phase ? `CloudNativePG: ${database.phase}.` : ""}</Alert> : null}
          <Alert severity="info">Snapshots provide in-cluster recovery. They do not replace off-cluster disaster recovery.</Alert>
          <Paper sx={CARD_SX}><Typography variant="h6">PostgreSQL role control</Typography><Stack direction={{ xs: "column", sm: "row" }} spacing={1.25} sx={{ mt: 2 }}><FormControl sx={{ minWidth: 220 }}><InputLabel id="postgres-node-label">Target node</InputLabel><Select labelId="postgres-node-label" label="Target node" value={selectedNode} onChange={(event) => setSelectedNode(event.target.value)}>{nodes.map((node) => <MenuItem key={node.id} value={node.id}>{node.node_name}</MenuItem>)}</Select></FormControl><Button variant="outlined" disabled={!selectedNode || !normalOperationsEnabled} onClick={() => openAction("switchover")}>Switchover</Button><Button color="error" variant="outlined" disabled={!selectedNode} onClick={() => openAction("emergency_failover")}>Emergency Failover</Button></Stack></Paper>
        </Stack> : null}

        {tab === 3 ? <Stack spacing={2}>
          <Paper sx={CARD_SX}>
            <Typography variant="h6">Stable Engine Release</Typography>
            {releaseError ? <Alert severity="error" sx={{ mt: 2 }}>{releaseError}</Alert> : null}
            {!releaseError && releases.length === 0 ? <Alert severity="info" sx={{ mt: 2 }}>No published stable cluster-compatible release exists at or above baseline {valueLabel(cluster?.baseline_release)}. Current release remains pinned.</Alert> : null}
            {!releaseError && releases.length > 0 && selectableReleases.length === 0 ? <Alert severity="warning" sx={{ mt: 2 }}>Stable releases were found, but none match current rolling-update and K3s compatibility requirements. Open release list for per-release reason.</Alert> : null}
            <FormControl fullWidth sx={{ mt: 2 }}>
              <InputLabel id="cluster-release-label">Release</InputLabel>
              <Select labelId="cluster-release-label" label="Release" value={selectedRelease} onChange={(event) => setSelectedRelease(event.target.value)}>
                {releases.map((release) => <MenuItem key={release.tag} value={release.tag} disabled={!release.selectable}>{release.title || release.tag}{release.selectable ? "" : ` — ${release.reason || "incompatible"}`}</MenuItem>)}
              </Select>
            </FormControl>
            <Stack direction={{ xs: "column", sm: "row" }} spacing={1.25} sx={{ mt: 2 }}>
              <Button variant="contained" disabled={!rollingUpdateEnabled || !selectedRelease || !selectableReleases.length} onClick={() => openAction("update_all")}>Update All One at a Time</Button>
              <FormControl sx={{ minWidth: 220 }}>
                <InputLabel id="update-node-label">Node</InputLabel>
                <Select labelId="update-node-label" label="Node" value={selectedNode} onChange={(event) => setSelectedNode(event.target.value)}>{nodes.map((node) => <MenuItem key={node.id} value={node.id}>{node.node_name}</MenuItem>)}</Select>
              </FormControl>
              <Button variant="outlined" disabled={!rollingUpdateEnabled || !selectedRelease || !selectedNode} onClick={() => openAction("update_node", nodes.find((node) => node.id === selectedNode))}>Update Node</Button>
            </Stack>
          </Paper>
        </Stack> : null}

        {tab === 4 ? <Stack spacing={1.25}>{operations.length ? operations.map((operation) => <OperationCard key={operation.id} operation={operation} busy={busy} mutate={mutate} />) : <Alert severity="info">No cluster operations recorded.</Alert>}</Stack> : null}

        {tab === 5 ? <Stack spacing={2}>
          <Paper sx={CARD_SX}><Typography variant="h6">Cluster-wide HMR isolation</Typography><Alert severity="warning" sx={{ mt: 1.5 }}>{HMR_WARNING}</Alert><Stack direction={{ xs: "column", sm: "row" }} spacing={1.25} sx={{ mt: 2 }}><FormControl sx={{ minWidth: 220 }}><InputLabel id="hmr-node-label">HMR node</InputLabel><Select labelId="hmr-node-label" label="HMR node" value={selectedNode} onChange={(event) => setSelectedNode(event.target.value)}>{nodes.map((node) => <MenuItem key={node.id} value={node.id}>{node.node_name}</MenuItem>)}</Select></FormControl><Button color="warning" variant="contained" disabled={!normalOperationsEnabled || !selectedNode || cluster?.hmr?.state !== "inactive"} onClick={() => openAction("hmr_start")}>Enable HMR</Button><Button variant="outlined" disabled={cluster?.hmr?.state === "inactive"} onClick={() => openAction("hmr_exit")}>Restore Production HA</Button></Stack></Paper>
          <Paper sx={CARD_SX}><Typography variant="h6">K3s server upgrade</Typography><Typography variant="body2" sx={{ mt: 1, color: "#94a3b8" }}>Current: {valueLabel(cluster?.k3s_version)}. Stable target only; current minor patch or next minor. Borealis snapshots etcd, drains one application node, runs immutable system-upgrade Plan, then requires Ready/etcd voter health and probe conformance before next server.</Typography><Button sx={{ mt: 2 }} variant="outlined" color="warning" disabled={!normalOperationsEnabled || cluster?.hmr?.state !== "inactive"} onClick={() => openAction("k3s_update")}>Upgrade K3s One Server at a Time</Button></Paper>
          <Paper sx={CARD_SX}><Typography variant="h6">Pending quorum admissions</Typography><Stack spacing={1} sx={{ mt: 1.5 }}>{(cluster?.admissions || []).map((admission) => <Stack key={admission.id} direction="row" justifyContent="space-between" alignItems="center"><Typography>{admission.node_name} · {admission.state}</Typography><Button size="small" disabled={busy || !canPrepareMembership || admission.state !== "Pending Quorum"} onClick={() => void mutate(`/api/server/cluster/admissions/${admission.id}/approve`, { confirmation: "APPROVE NODE" })}>{replacementRecovery ? "Approve Replacement" : "Approve Pair"}</Button></Stack>)}</Stack></Paper>
          {!cluster?.enabled ? <Paper sx={CARD_SX}><Typography variant="h6">Enable cluster mode</Typography><Typography variant="body2" sx={{ mt: 1, color: "#94a3b8" }}>One-way PostgreSQL migration. Stable K3s probe conformance must pass first.</Typography><Button sx={{ mt: 2 }} variant="contained" color="warning" onClick={() => openAction("cluster_enable")}>Enable Cluster</Button></Paper> : null}
          {cluster?.enabled ? <Paper sx={CARD_SX}><Typography variant="h6">Membership</Typography>{replacementRecovery ? <Alert severity="warning" sx={{ mt: 1.5 }}>Cluster is running on two surviving members after externally fenced emergency removal. Create and approve one replacement invitation to restore three-node membership.</Alert> : !canExpandToThree ? <Alert severity="info" sx={{ mt: 1.5 }}>Three-node release limit reached. Odd-numbered expansion or shrinking beyond three nodes remains future roadmap work.</Alert> : null}<Stack direction={{ xs: "column", sm: "row" }} spacing={1.25} sx={{ mt: 2 }}><Button variant="outlined" disabled={!canPrepareMembership} onClick={() => openAction("invite")}>Create Node Invitation</Button>{canExpandToThree ? <><FormControl sx={{ minWidth: 140 }}><InputLabel id="desired-size-label">Desired size</InputLabel><Select labelId="desired-size-label" label="Desired size" value={expansionSizes.includes(Number(desiredSize)) ? desiredSize : ""} onChange={(event) => setDesiredSize(event.target.value)}>{expansionSizes.map((size) => <MenuItem key={size} value={size}>{size}</MenuItem>)}</Select></FormControl><Button variant="outlined" onClick={() => openAction("scale")}>Request Pair Expansion</Button></> : null}</Stack>{inviteBundle ? <TextField sx={{ mt: 2 }} fullWidth multiline minRows={3} label="One-use invitation bundle" value={inviteBundle} InputProps={{ readOnly: true }} /> : null}</Paper> : null}
        </Stack> : null}
      </Stack>

      <Dialog open={Boolean(dialog)} onClose={() => !busy && setDialog(null)} maxWidth="sm" fullWidth>
        <DialogTitle>Confirm cluster operation</DialogTitle>
        <DialogContent>
          {dialog?.kind === "hmr_start" ? <Alert severity="warning" sx={{ mb: 2 }}>{HMR_WARNING}</Alert> : null}
          {dialog?.kind === "remove" ? <Alert severity="warning" sx={{ mb: 2 }}>Safe downscale removes two nodes sequentially. PostgreSQL replicas must vacate both targets before Borealis self-fences K3s and deletes membership.</Alert> : null}
          {dialog?.kind === "emergency_remove" ? <Alert severity="error" sx={{ mb: 2 }}>Emergency removal is only safe after external power fencing. Target must be powered off and unable to rejoin.</Alert> : null}
          {dialog?.kind === "k3s_update" ? <Alert severity="warning" sx={{ mb: 2 }}>K3s control-plane update stays separate from Engine release update. Failure halts sequence and leaves affected node drained.</Alert> : null}
          {dialog?.kind === "update_node" || dialog?.kind === "update_all" ? (
            <FormControl fullWidth sx={{ mb: 2 }}>
              <InputLabel id="dialog-release-label">Release</InputLabel>
              <Select labelId="dialog-release-label" label="Release" value={selectedRelease} onChange={(event) => setSelectedRelease(event.target.value)}>
                {releases.map((release) => <MenuItem key={release.tag} value={release.tag} disabled={!release.selectable}>{release.title || release.tag}</MenuItem>)}
              </Select>
            </FormControl>
          ) : null}
          {dialog?.kind === "cluster_enable" ? <Stack spacing={1.5} sx={{ mb: 2 }}><TextField label="Control-plane VIP" value={controlVIP} onChange={(event) => setControlVIP(sanitizeSingleLineInput(event.target.value))} inputProps={{ maxLength: 15 }} /><TextField label="Borealis edge VIP" value={edgeVIP} onChange={(event) => setEdgeVIP(sanitizeSingleLineInput(event.target.value))} inputProps={{ maxLength: 15 }} /><TextField label="Current node management IPv4" value={managementIP} onChange={(event) => setManagementIP(sanitizeSingleLineInput(event.target.value))} inputProps={{ maxLength: 15 }} /><TextField label="Current node name" value={nodeName} onChange={(event) => setNodeName(sanitizeSingleLineInput(event.target.value).toLowerCase())} inputProps={{ maxLength: 63 }} /><FormControl><InputLabel id="cluster-architecture-label">Architecture</InputLabel><Select labelId="cluster-architecture-label" label="Architecture" value={architecture} onChange={(event) => setArchitecture(event.target.value)}><MenuItem value="amd64">amd64</MenuItem><MenuItem value="arm64">arm64</MenuItem></Select></FormControl></Stack> : null}
          {dialog?.kind === "invite" ? <TextField fullWidth label="New node name" value={nodeName} onChange={(event) => setNodeName(sanitizeSingleLineInput(event.target.value).toLowerCase())} inputProps={{ maxLength: 63 }} /> : null}
          {dialog?.kind === "k3s_update" ? <TextField fullWidth sx={{ mb: 2 }} label="Stable K3s target" value={k3sTargetVersion} onChange={(event) => setK3sTargetVersion(sanitizeSingleLineInput(event.target.value))} inputProps={{ maxLength: 32 }} helperText="vX.Y.Z+k3sN; immutable upgrade image and source conformance required" /> : null}
          {dialog?.kind === "remove" ? <FormControl fullWidth sx={{ mb: 2 }}><InputLabel id="paired-removal-node-label">Paired removal node</InputLabel><Select labelId="paired-removal-node-label" label="Paired removal node" value={pairedNode} onChange={(event) => setPairedNode(event.target.value)}>{nodes.filter((candidate) => candidate.id !== dialog?.node?.id && candidate.membership_state === "Active").map((candidate) => <MenuItem key={candidate.id} value={candidate.id}>{candidate.node_name}</MenuItem>)}</Select></FormControl> : null}
          {dialog?.kind === "emergency_remove" ? <TextField fullWidth sx={{ mb: 2 }} label="External fencing confirmation" value={fencingConfirmation} onChange={(event) => setFencingConfirmation(sanitizeSingleLineInput(event.target.value))} inputProps={{ maxLength: 21 }} helperText="Type TARGET IS POWERED OFF" /> : null}
          {["maintenance", "scale", "remove", "emergency_remove", "switchover", "emergency_failover"].includes(dialog?.kind) ? <TextField fullWidth label="Reason" value={reason} onChange={(event) => setReason(sanitizeSingleLineInput(event.target.value).slice(0, 256))} inputProps={{ maxLength: 256 }} helperText={`${reason.length}/256 · single-line operational text`} /> : null}
          {!['maintenance', 'invite', 'scale', 'switchover'].includes(dialog?.kind) ? <TextField autoFocus fullWidth label="Typed confirmation" value={confirmation} onChange={(event) => setConfirmation(sanitizeSingleLineInput(event.target.value))} helperText={dialog?.kind === "hmr_start" ? "Type ENABLE HMR" : dialog?.kind === "hmr_exit" ? "Type EXIT HMR" : dialog?.kind === "cluster_enable" ? "Type ENABLE CLUSTER" : dialog?.kind === "remove" ? "Type REMOVE NODE PAIR" : dialog?.kind === "emergency_remove" ? "Type EMERGENCY REMOVE NODE" : dialog?.kind === "k3s_update" ? "Type UPDATE K3S" : dialog?.kind === "emergency_failover" ? "Type EMERGENCY FAILOVER" : "Type UPDATE CLUSTER"} /> : null}
          <Typography variant="body2" sx={{ mt: 2, color: "text.secondary" }}>Administrator access required. Destructive actions also require exact typed confirmation.</Typography>
        </DialogContent>
        <DialogActions><Button onClick={() => setDialog(null)} disabled={busy}>Cancel</Button><Button variant="contained" color={dialog?.kind === "emergency_remove" ? "error" : dialog?.kind === "hmr_start" ? "warning" : "primary"} onClick={() => void submitDialog()} disabled={busy}>Submit</Button></DialogActions>
      </Dialog>
    </PageBodyFrame>
  );
}
