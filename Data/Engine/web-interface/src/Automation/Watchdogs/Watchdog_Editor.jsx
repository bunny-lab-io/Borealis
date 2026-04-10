import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";
import {
  Alert,
  Autocomplete,
  Box,
  Button,
  Checkbox,
  Chip,
  FormControlLabel,
  IconButton,
  LinearProgress,
  MenuItem,
  Paper,
  Stack,
  Tab,
  Tabs,
  TextField,
  Typography,
} from "@mui/material";
import {
  AddRounded as AddIcon,
  CloseRounded as CancelIcon,
  DeleteRounded as DeleteIcon,
  PreviewRounded as PreviewIcon,
  SaveRounded as SaveIcon,
  SecurityRounded as HeaderIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { AllCommunityModule, ModuleRegistry } from "ag-grid-community";

import PageBodyFrame from "../../PageBodyFrame.jsx";
import { buildAssemblyIndex, parseAssembliesCollectionPayload } from "../../Assemblies/assemblyUtils";
import { useAppNotifications } from "../../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../../app/hooks/useRoutePageChrome.js";
import { APP_PATHS } from "../../app/routes/paths.js";
import {
  GRID_WRAPPER_SX,
  buildNavTabsSx,
  formatTimestamp,
  gridTheme,
  severityColor,
  summarizeRuleResults,
} from "./shared.js";

ModuleRegistry.registerModules([AllCommunityModule]);

const MAIN_TABS = [
  { value: "name", label: "Name" },
  { value: "scope", label: "Scope" },
  { value: "targets", label: "Targets" },
  { value: "rules", label: "Rules" },
  { value: "actions", label: "Actions" },
  { value: "preview", label: "Preview" },
];

const RULE_TYPE_OPTIONS = [
  { value: "device_offline", label: "Device Offline" },
  { value: "storage_usage_percent", label: "Storage Usage" },
  { value: "service_state", label: "Service State" },
  { value: "agent_role_health", label: "Agent Role Health" },
  { value: "software_presence_or_version", label: "Software Presence / Version" },
  { value: "agent_version_status", label: "Agent Version Status" },
];

const ACTION_TYPE_OPTIONS = [
  { value: "notification", label: "Send In-App Alert" },
  { value: "service_control", label: "Control Service" },
  { value: "assembly", label: "Run Assembly" },
];

const ROLE_STATUS_OPTIONS = [
  "healthy",
  "recovering",
  "unhealthy",
  "pending",
  "unsupported",
  "unknown",
];

const PREVIEW_AUTO_SIZE_COLUMNS = ["hostname", "site_name", "state", "status"];

function makeId(prefix) {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return `${prefix}-${crypto.randomUUID()}`;
  }
  return `${prefix}-${Math.random().toString(36).slice(2, 10)}`;
}

function makeRule(type = "device_offline") {
  if (type === "storage_usage_percent") {
    return { id: makeId("rule"), type, threshold: 90, drive: "" };
  }
  if (type === "service_state") {
    return { id: makeId("rule"), type, service_name: "", expected_status: "running" };
  }
  if (type === "agent_role_health") {
    return { id: makeId("rule"), type, role_name: "", trigger_statuses: ["unhealthy"] };
  }
  if (type === "software_presence_or_version") {
    return {
      id: makeId("rule"),
      type,
      software_name: "",
      software_source: "",
      require_present: true,
      version_operator: "",
      version_value: "",
    };
  }
  if (type === "agent_version_status") {
    return { id: makeId("rule"), type, expected_status: "Up-to-Date" };
  }
  return { id: makeId("rule"), type: "device_offline", offline_after_seconds: 300 };
}

function makeAction(type = "notification") {
  if (type === "service_control") {
    return { id: makeId("action"), type, enabled: true, service_name: "", action: "restart" };
  }
  if (type === "assembly") {
    return {
      id: makeId("action"),
      type,
      enabled: true,
      assembly_guid: "",
      run_mode: "system",
      execution_context: "local",
    };
  }
  return {
    id: makeId("action"),
    type: "notification",
    enabled: true,
    variant: "warning",
    title: "",
    message_template: "",
  };
}

function normalizeArray(value) {
  return Array.isArray(value) ? value : [];
}

function normalizeTargets(targets = []) {
  const seen = new Set();
  return normalizeArray(targets)
    .map((target) => {
      if (typeof target === "string") {
        const hostname = String(target).trim();
        if (!hostname) return null;
        return { kind: "device", hostname, device_guid: "", site_id: null, site_name: "" };
      }
      if (!target || typeof target !== "object") return null;
      if (String(target.kind || target.type || "").toLowerCase() === "filter" || target.filter_id != null) {
        const filterId = Number(target.filter_id || target.id || 0);
        if (!Number.isFinite(filterId) || filterId <= 0) return null;
        return { kind: "filter", filter_id: filterId, name: String(target.name || "").trim() };
      }
      const hostname = String(target.hostname || "").trim();
      if (!hostname) return null;
      return {
        kind: "device",
        hostname,
        device_guid: String(target.device_guid || target.guid || "").trim(),
        site_id: target.site_id == null || target.site_id === "" ? null : Number(target.site_id),
        site_name: String(target.site_name || target.site || "").trim(),
      };
    })
    .filter(Boolean)
    .filter((target) => {
      const dedupeKey =
        target.kind === "filter"
          ? `filter:${target.filter_id}`
          : `device:${String(target.device_guid || "").toLowerCase() || String(target.hostname || "").toLowerCase()}`;
      if (seen.has(dedupeKey)) return false;
      seen.add(dedupeKey);
      return true;
    });
}

function normalizeRules(rules = []) {
  return normalizeArray(rules).map((rule, index) => {
    const next = { ...makeRule(rule?.type || "device_offline"), ...(rule || {}) };
    if (!next.id) next.id = makeId(`rule-${index + 1}`);
    if (next.type === "agent_role_health") {
      next.trigger_statuses = normalizeArray(next.trigger_statuses).map((item) => String(item).toLowerCase());
      if (!next.trigger_statuses.length) next.trigger_statuses = ["unhealthy"];
    }
    return next;
  });
}

function normalizeActions(actions = []) {
  return normalizeArray(actions).map((action, index) => {
    const next = { ...makeAction(action?.type || "notification"), ...(action || {}) };
    if (!next.id) next.id = makeId(`action-${index + 1}`);
    next.enabled = action?.enabled !== false;
    return next;
  });
}

function defaultWatchdog(draft = {}) {
  return {
    id: draft?.id || null,
    name: String(draft?.name || "").trim(),
    description: String(draft?.description || "").trim(),
    enabled: draft?.enabled !== false,
    archived: Boolean(draft?.archived),
    severity: String(draft?.severity || "warning").trim() || "warning",
    site_mode: String(draft?.site_mode || "global").trim() || "global",
    site_ids: normalizeArray(draft?.site_ids).map((value) => Number(value)).filter((value) => Number.isFinite(value)),
    evaluation_interval_seconds: Number(draft?.evaluation_interval_seconds || 60),
    cooldown_seconds: Number(draft?.cooldown_seconds || 900),
    auto_resolve_after_seconds: Number(draft?.auto_resolve_after_seconds || 300),
    min_consecutive_matches: Number(draft?.min_consecutive_matches || 1),
    boot_grace_seconds: Number(draft?.boot_grace_seconds || 0),
    criteria: {
      match_mode: String(draft?.criteria?.match_mode || draft?.match_mode || "all").trim() || "all",
      rules: normalizeRules(draft?.criteria?.rules || draft?.rules || [makeRule()]),
    },
    actions: {
      actions: normalizeActions(draft?.actions?.actions || draft?.action_list || draft?.actions || [makeAction()]),
    },
    targets: normalizeTargets(draft?.targets || []),
  };
}

function buildPayload(state) {
  return {
    id: state?.id || undefined,
    name: String(state?.name || "").trim(),
    description: String(state?.description || "").trim(),
    enabled: Boolean(state?.enabled),
    archived: Boolean(state?.archived),
    severity: String(state?.severity || "warning").trim() || "warning",
    site_mode: String(state?.site_mode || "global").trim() || "global",
    site_ids: normalizeArray(state?.site_ids).map((value) => Number(value)).filter((value) => Number.isFinite(value)),
    evaluation_interval_seconds: Number(state?.evaluation_interval_seconds || 60),
    cooldown_seconds: Number(state?.cooldown_seconds || 900),
    auto_resolve_after_seconds: Number(state?.auto_resolve_after_seconds || 300),
    min_consecutive_matches: Number(state?.min_consecutive_matches || 1),
    boot_grace_seconds: Number(state?.boot_grace_seconds || 0),
    criteria: {
      match_mode: String(state?.criteria?.match_mode || "all").trim() || "all",
      rules: normalizeRules(state?.criteria?.rules || []),
    },
    actions: {
      actions: normalizeActions(state?.actions?.actions || []),
    },
    targets: normalizeTargets(state?.targets || []),
  };
}

function watchDraftFromLocation(locationState) {
  if (!locationState || typeof locationState !== "object") return {};
  return locationState.watchdogDraft && typeof locationState.watchdogDraft === "object"
    ? locationState.watchdogDraft
    : {};
}

function SectionCard({ title, subtitle, children, action }) {
  return (
    <Paper
      variant="outlined"
      sx={{
        p: 2,
        borderColor: "rgba(148,163,184,0.24)",
        background: "rgba(15,23,42,0.45)",
      }}
    >
      <Stack spacing={1.5}>
        <Stack direction={{ xs: "column", md: "row" }} justifyContent="space-between" spacing={1}>
          <Box>
            <Typography sx={{ color: "#f8fafc", fontWeight: 700 }}>{title}</Typography>
            {subtitle ? (
              <Typography variant="body2" sx={{ color: "#94a3b8", mt: 0.35 }}>
                {subtitle}
              </Typography>
            ) : null}
          </Box>
          {action || null}
        </Stack>
        {children}
      </Stack>
    </Paper>
  );
}

function RuleCard({ rule, onChange, onRemove }) {
  const type = String(rule?.type || "device_offline");
  return (
    <SectionCard
      title={RULE_TYPE_OPTIONS.find((item) => item.value === type)?.label || "Watchdog Rule"}
      subtitle="Evaluate existing Borealis inventory and telemetry for this condition."
      action={
        <IconButton color="error" onClick={onRemove}>
          <DeleteIcon />
        </IconButton>
      }
    >
      <TextField
        select
        label="Rule Type"
        value={type}
        onChange={(event) => onChange({ ...makeRule(event.target.value), id: rule.id })}
      >
        {RULE_TYPE_OPTIONS.map((option) => (
          <MenuItem key={option.value} value={option.value}>
            {option.label}
          </MenuItem>
        ))}
      </TextField>
      {type === "device_offline" ? (
        <TextField
          type="number"
          label="Offline After Seconds"
          value={rule.offline_after_seconds ?? 300}
          onChange={(event) =>
            onChange({ ...rule, offline_after_seconds: Math.max(60, Number(event.target.value || 300)) })
          }
        />
      ) : null}
      {type === "storage_usage_percent" ? (
        <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
          <TextField
            label="Drive"
            value={rule.drive || ""}
            onChange={(event) => onChange({ ...rule, drive: event.target.value })}
            helperText="Leave blank to evaluate the fullest disk on the device."
            sx={{ flex: 1 }}
          />
          <TextField
            type="number"
            label="Threshold (%)"
            value={rule.threshold ?? 90}
            onChange={(event) =>
              onChange({ ...rule, threshold: Math.max(1, Math.min(100, Number(event.target.value || 90))) })
            }
            sx={{ width: { xs: "100%", md: 180 } }}
          />
        </Stack>
      ) : null}
      {type === "service_state" ? (
        <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
          <TextField
            label="Service Name"
            value={rule.service_name || ""}
            onChange={(event) => onChange({ ...rule, service_name: event.target.value })}
            sx={{ flex: 1 }}
          />
          <TextField
            select
            label="Expected Status"
            value={rule.expected_status || "running"}
            onChange={(event) => onChange({ ...rule, expected_status: event.target.value })}
            sx={{ width: { xs: "100%", md: 200 } }}
          >
            <MenuItem value="running">Running</MenuItem>
            <MenuItem value="stopped">Stopped</MenuItem>
          </TextField>
        </Stack>
      ) : null}
      {type === "agent_role_health" ? (
        <Stack spacing={1.25}>
          <TextField
            label="Role Name"
            value={rule.role_name || ""}
            onChange={(event) => onChange({ ...rule, role_name: event.target.value })}
            helperText="Leave blank to match any reporting role."
          />
          <Autocomplete
            multiple
            options={ROLE_STATUS_OPTIONS}
            value={normalizeArray(rule.trigger_statuses)}
            onChange={(_event, nextValue) => onChange({ ...rule, trigger_statuses: nextValue })}
            renderInput={(params) => <TextField {...params} label="Trigger Statuses" />}
          />
        </Stack>
      ) : null}
      {type === "software_presence_or_version" ? (
        <Stack spacing={1.25}>
          <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
            <TextField
              label="Software Name"
              value={rule.software_name || ""}
              onChange={(event) => onChange({ ...rule, software_name: event.target.value })}
              sx={{ flex: 1 }}
            />
            <TextField
              label="Software Source"
              value={rule.software_source || ""}
              onChange={(event) => onChange({ ...rule, software_source: event.target.value })}
              helperText="Optional inventory source like local_installed or rpm."
              sx={{ flex: 1 }}
            />
          </Stack>
          <FormControlLabel
            control={
              <Checkbox
                checked={rule.require_present !== false}
                onChange={(event) => onChange({ ...rule, require_present: event.target.checked })}
              />
            }
            label="Alert when the software is missing."
          />
          <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
            <TextField
              select
              label="Version Operator"
              value={rule.version_operator || ""}
              onChange={(event) => onChange({ ...rule, version_operator: event.target.value })}
              sx={{ flex: 1 }}
            >
              <MenuItem value="">No Version Check</MenuItem>
              <MenuItem value="matches">Matches</MenuItem>
              <MenuItem value="older_than">Older Than</MenuItem>
              <MenuItem value="newer_than">Newer Than</MenuItem>
            </TextField>
            <TextField
              label="Version Value"
              value={rule.version_value || ""}
              onChange={(event) => onChange({ ...rule, version_value: event.target.value })}
              disabled={!rule.version_operator}
              sx={{ flex: 1 }}
            />
          </Stack>
        </Stack>
      ) : null}
      {type === "agent_version_status" ? (
        <TextField
          select
          label="Expected Version Status"
          value={rule.expected_status || "Up-to-Date"}
          onChange={(event) => onChange({ ...rule, expected_status: event.target.value })}
        >
          <MenuItem value="Up-to-Date">Up-to-Date</MenuItem>
          <MenuItem value="Needs Updated">Needs Updated</MenuItem>
        </TextField>
      ) : null}
    </SectionCard>
  );
}

function ActionCard({ action, assemblyOptions, onChange, onRemove }) {
  const type = String(action?.type || "notification");
  const selectedAssembly = assemblyOptions.find(
    (item) => String(item?.assemblyGuid || "").toLowerCase() === String(action?.assembly_guid || "").toLowerCase()
  );

  return (
    <SectionCard
      title={ACTION_TYPE_OPTIONS.find((item) => item.value === type)?.label || "Action"}
      subtitle="Choose what Borealis should do once the watchdog has fully triggered."
      action={
        <IconButton color="error" onClick={onRemove}>
          <DeleteIcon />
        </IconButton>
      }
    >
      <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
        <TextField
          select
          label="Action Type"
          value={type}
          onChange={(event) => onChange({ ...makeAction(event.target.value), id: action.id, enabled: action.enabled !== false })}
          sx={{ flex: 1 }}
        >
          {ACTION_TYPE_OPTIONS.map((option) => (
            <MenuItem key={option.value} value={option.value}>
              {option.label}
            </MenuItem>
          ))}
        </TextField>
        <FormControlLabel
          control={
            <Checkbox
              checked={action.enabled !== false}
              onChange={(event) => onChange({ ...action, enabled: event.target.checked })}
            />
          }
          label="Enabled"
          sx={{ alignSelf: "center", ml: 0.25 }}
        />
      </Stack>
      {type === "notification" ? (
        <Stack spacing={1.25}>
          <TextField
            select
            label="Variant"
            value={action.variant || "warning"}
            onChange={(event) => onChange({ ...action, variant: event.target.value })}
          >
            <MenuItem value="info">Info</MenuItem>
            <MenuItem value="warning">Warning</MenuItem>
            <MenuItem value="error">Error</MenuItem>
          </TextField>
          <TextField
            label="Title"
            value={action.title || ""}
            onChange={(event) => onChange({ ...action, title: event.target.value })}
          />
          <TextField
            label="Message Template"
            value={action.message_template || ""}
            onChange={(event) => onChange({ ...action, message_template: event.target.value })}
            multiline
            minRows={2}
            helperText="Optional operator-facing message override. If blank, Borealis will use the rule evaluation message."
          />
        </Stack>
      ) : null}
      {type === "service_control" ? (
        <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
          <TextField
            label="Service Name"
            value={action.service_name || ""}
            onChange={(event) => onChange({ ...action, service_name: event.target.value })}
            sx={{ flex: 1 }}
          />
          <TextField
            select
            label="Action"
            value={action.action || "restart"}
            onChange={(event) => onChange({ ...action, action: event.target.value })}
            sx={{ width: { xs: "100%", md: 180 } }}
          >
            <MenuItem value="start">Start</MenuItem>
            <MenuItem value="stop">Stop</MenuItem>
            <MenuItem value="restart">Restart</MenuItem>
          </TextField>
        </Stack>
      ) : null}
      {type === "assembly" ? (
        <Stack spacing={1.25}>
          <TextField
            select
            label="Assembly"
            value={action.assembly_guid || ""}
            onChange={(event) => onChange({ ...action, assembly_guid: event.target.value })}
          >
            <MenuItem value="">Select an Assembly</MenuItem>
            {assemblyOptions.map((record) => (
              <MenuItem key={record.assemblyGuid} value={record.assemblyGuid}>
                {record.displayName} ({record.kind})
              </MenuItem>
            ))}
          </TextField>
          {selectedAssembly ? (
            <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
              <TextField
                select
                label="Run Mode"
                value={action.run_mode || "system"}
                onChange={(event) => onChange({ ...action, run_mode: event.target.value })}
                disabled={selectedAssembly.kind !== "script"}
                sx={{ flex: 1 }}
              >
                <MenuItem value="system">SYSTEM</MenuItem>
                <MenuItem value="currentuser">Current User</MenuItem>
              </TextField>
              <TextField
                select
                label="Execution Context"
                value={action.execution_context || "local"}
                onChange={(event) => onChange({ ...action, execution_context: event.target.value })}
                disabled={selectedAssembly.kind !== "ansible"}
                sx={{ flex: 1 }}
              >
                <MenuItem value="local">Local</MenuItem>
                <MenuItem value="ssh">SSH</MenuItem>
                <MenuItem value="winrm">WinRM</MenuItem>
              </TextField>
            </Stack>
          ) : null}
        </Stack>
      ) : null}
    </SectionCard>
  );
}

export default function WatchdogEditor() {
  const location = useLocation();
  const navigate = useNavigate();
  const { watchdogId } = useParams();
  const notifyOperator = useAppNotifications({ title: "Watchdogs", icon: "notification" });
  const previewGridRef = useRef(null);
  const [activeTab, setActiveTab] = useState("name");
  const [formState, setFormState] = useState(() => defaultWatchdog(watchDraftFromLocation(location.state)));
  const [metadata, setMetadata] = useState({});
  const [sites, setSites] = useState([]);
  const [filters, setFilters] = useState([]);
  const [assemblyOptions, setAssemblyOptions] = useState([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [error, setError] = useState("");
  const [validationErrors, setValidationErrors] = useState([]);
  const [previewRows, setPreviewRows] = useState([]);
  const [previewSummary, setPreviewSummary] = useState({ device_count: 0, matched_count: 0 });
  const [previewStale, setPreviewStale] = useState(true);
  const [deviceTargetDraft, setDeviceTargetDraft] = useState("");

  const hydrateFromRecord = useCallback((record) => {
    setFormState(defaultWatchdog(record || {}));
    setPreviewRows([]);
    setPreviewSummary({ device_count: 0, matched_count: 0 });
    setPreviewStale(true);
  }, []);

  const loadInitialData = useCallback(async () => {
    setLoading(true);
    setError("");
    setValidationErrors([]);
    try {
      const [metadataResp, sitesResp, filtersResp, assembliesResp, watchdogResp] = await Promise.all([
        fetch("/api/watchdogs/metadata", { credentials: "include", cache: "no-store" }),
        fetch("/api/sites", { credentials: "include", cache: "no-store" }),
        fetch("/api/device_filters?archived=0", { credentials: "include", cache: "no-store" }),
        fetch("/api/assemblies", { credentials: "include", cache: "no-store" }),
        watchdogId
          ? fetch(`/api/watchdogs/${encodeURIComponent(watchdogId)}`, {
              credentials: "include",
              cache: "no-store",
            })
          : Promise.resolve(null),
      ]);

      const metadataPayload = await metadataResp.json().catch(() => ({}));
      if (!metadataResp.ok) {
        throw new Error(metadataPayload?.errors?.[0] || metadataPayload?.error || "Unable to load watchdog metadata.");
      }
      setMetadata(metadataPayload || {});

      const sitesPayload = await sitesResp.json().catch(() => ({}));
      setSites(Array.isArray(sitesPayload?.sites) ? sitesPayload.sites : []);

      const filtersPayload = await filtersResp.json().catch(() => ({}));
      setFilters(Array.isArray(filtersPayload?.items) ? filtersPayload.items : []);

      const assembliesPayload = await assembliesResp.json().catch(() => ({}));
      const normalizedAssemblies = parseAssembliesCollectionPayload(assembliesPayload);
      setAssemblyOptions(buildAssemblyIndex(normalizedAssemblies.items, normalizedAssemblies.queue).records);

      if (watchdogResp) {
        const watchdogPayload = await watchdogResp.json().catch(() => ({}));
        if (!watchdogResp.ok) {
          throw new Error(watchdogPayload?.errors?.[0] || watchdogPayload?.error || "Unable to load watchdog.");
        }
        hydrateFromRecord(watchdogPayload || {});
      } else {
        hydrateFromRecord(watchDraftFromLocation(location.state));
      }
    } catch (err) {
      setError(String(err?.message || err || "Unable to load watchdog editor."));
    } finally {
      setLoading(false);
    }
  }, [hydrateFromRecord, location.state, watchdogId]);

  useEffect(() => {
    loadInitialData();
  }, [loadInitialData]);

  useEffect(() => {
    setPreviewStale(true);
  }, [formState]);

  useEffect(() => {
    if (!previewRows.length || !previewGridRef.current) return;
    requestAnimationFrame(() => {
      try {
        previewGridRef.current.autoSizeColumns(PREVIEW_AUTO_SIZE_COLUMNS, true);
      } catch {
        /* noop */
      }
    });
  }, [previewRows]);

  const selectedSites = useMemo(() => {
    const ids = new Set(normalizeArray(formState.site_ids).map((value) => Number(value)));
    return sites.filter((site) => ids.has(Number(site.id)));
  }, [formState.site_ids, sites]);

  const selectedFilterTargets = useMemo(() => {
    const ids = new Set(
      normalizeArray(formState.targets)
        .filter((target) => target?.kind === "filter")
        .map((target) => Number(target.filter_id))
    );
    return filters.filter((record) => ids.has(Number(record.id)));
  }, [filters, formState.targets]);

  const explicitDeviceTargets = useMemo(
    () => normalizeArray(formState.targets).filter((target) => target?.kind !== "filter"),
    [formState.targets]
  );

  const previewColumns = useMemo(
    () => [
      { field: "hostname", headerName: "Hostname", minWidth: 200, flex: 1 },
      { field: "site_name", headerName: "Site", minWidth: 180, flex: 0.8 },
      { field: "status", headerName: "Device Status", minWidth: 130, flex: 0.7 },
      {
        field: "state",
        headerName: "Evaluation State",
        minWidth: 140,
        flex: 0.8,
        cellRenderer: (params) => (
          <Chip
            size="small"
            label={String(params.value || "normal").replace(/_/g, " ")}
            variant="outlined"
            sx={{
              color:
                params.value === "triggered"
                  ? severityColor(formState.severity)
                  : params.value === "stale_data"
                    ? "#93c5fd"
                    : "#cbd5e1",
              borderColor:
                params.value === "triggered"
                  ? severityColor(formState.severity)
                  : params.value === "stale_data"
                    ? "#93c5fd"
                    : "rgba(148,163,184,0.35)",
              backgroundColor: "rgba(15,23,42,0.5)",
              textTransform: "capitalize",
            }}
          />
        ),
      },
      {
        field: "matched",
        headerName: "Matched",
        minWidth: 110,
        flex: 0.5,
        valueFormatter: (params) => (params.value ? "Yes" : "No"),
      },
      {
        field: "message",
        headerName: "Message",
        minWidth: 260,
        flex: 1.2,
      },
      {
        field: "sample",
        headerName: "Details",
        minWidth: 320,
        flex: 1.4,
        valueGetter: (params) => summarizeRuleResults(params?.data?.sample),
      },
    ],
    [formState.severity]
  );

  const updateTopLevel = useCallback((patch) => {
    setFormState((prev) => ({ ...prev, ...patch }));
  }, []);

  const updateCriteria = useCallback((patch) => {
    setFormState((prev) => ({ ...prev, criteria: { ...(prev.criteria || {}), ...patch } }));
  }, []);

  const updateActionsRoot = useCallback((patch) => {
    setFormState((prev) => ({ ...prev, actions: { ...(prev.actions || {}), ...patch } }));
  }, []);

  const addDeviceTarget = useCallback(() => {
    const hostname = String(deviceTargetDraft || "").trim();
    if (!hostname) return;
    setFormState((prev) => ({
      ...prev,
      targets: normalizeTargets([...(prev.targets || []), { kind: "device", hostname }]),
    }));
    setDeviceTargetDraft("");
  }, [deviceTargetDraft]);

  const updateRule = useCallback((ruleId, nextRule) => {
    setFormState((prev) => ({
      ...prev,
      criteria: {
        ...(prev.criteria || {}),
        rules: normalizeRules(
          normalizeArray(prev.criteria?.rules).map((rule) => (rule.id === ruleId ? nextRule : rule))
        ),
      },
    }));
  }, []);

  const removeRule = useCallback((ruleId) => {
    setFormState((prev) => {
      const nextRules = normalizeArray(prev.criteria?.rules).filter((rule) => rule.id !== ruleId);
      return {
        ...prev,
        criteria: {
          ...(prev.criteria || {}),
          rules: nextRules.length ? nextRules : [makeRule()],
        },
      };
    });
  }, []);

  const addRule = useCallback(() => {
    setFormState((prev) => ({
      ...prev,
      criteria: {
        ...(prev.criteria || {}),
        rules: [...normalizeArray(prev.criteria?.rules), makeRule()],
      },
    }));
  }, []);

  const updateAction = useCallback((actionId, nextAction) => {
    setFormState((prev) => ({
      ...prev,
      actions: {
        ...(prev.actions || {}),
        actions: normalizeActions(
          normalizeArray(prev.actions?.actions).map((action) => (action.id === actionId ? nextAction : action))
        ),
      },
    }));
  }, []);

  const removeAction = useCallback((actionId) => {
    setFormState((prev) => {
      const nextActions = normalizeArray(prev.actions?.actions).filter((action) => action.id !== actionId);
      return {
        ...prev,
        actions: {
          ...(prev.actions || {}),
          actions: nextActions.length ? nextActions : [makeAction()],
        },
      };
    });
  }, []);

  const addAction = useCallback(() => {
    setFormState((prev) => ({
      ...prev,
      actions: {
        ...(prev.actions || {}),
        actions: [...normalizeArray(prev.actions?.actions), makeAction()],
      },
    }));
  }, []);

  const runPreview = useCallback(async () => {
    setPreviewing(true);
    setError("");
    setValidationErrors([]);
    try {
      const response = await fetch("/api/watchdogs/preview", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(buildPayload(formState)),
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        if (Array.isArray(payload?.errors)) {
          setValidationErrors(payload.errors);
        }
        throw new Error(payload?.errors?.[0] || payload?.error || payload?.message || "Preview failed.");
      }
      const rows = Array.isArray(payload?.devices) ? payload.devices : [];
      setPreviewRows(rows);
      setPreviewSummary({
        device_count: Number(payload?.device_count || rows.length || 0),
        matched_count: Number(payload?.matched_count || 0),
      });
      setPreviewStale(false);
      setActiveTab("preview");
    } catch (err) {
      setPreviewRows([]);
      setPreviewSummary({ device_count: 0, matched_count: 0 });
      setError(String(err?.message || err || "Preview failed."));
    } finally {
      setPreviewing(false);
    }
  }, [formState]);

  const saveWatchdog = useCallback(async () => {
    setSaving(true);
    setError("");
    setValidationErrors([]);
    try {
      const payload = buildPayload(formState);
      const response = await fetch(
        formState.id ? `/api/watchdogs/${encodeURIComponent(formState.id)}` : "/api/watchdogs",
        {
          method: formState.id ? "PUT" : "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload),
        }
      );
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        if (Array.isArray(data?.errors)) {
          setValidationErrors(data.errors);
        }
        throw new Error(data?.errors?.[0] || data?.error || data?.message || "Save failed.");
      }
      const targetId = data?.id || formState.id;
      setFormState(defaultWatchdog(data || payload));
      setPreviewStale(true);
      await notifyOperator({
        message: `${String(data?.name || formState.name || "Watchdog").trim()} saved successfully.`,
        variant: "info",
      });
      if (targetId) {
        navigate(APP_PATHS.watchdog(targetId), { replace: true });
      } else {
        navigate(APP_PATHS.watchdogs);
      }
    } catch (err) {
      setError(String(err?.message || err || "Save failed."));
    } finally {
      setSaving(false);
    }
  }, [formState, navigate, notifyOperator]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "watchdog-editor-cancel",
        label: "Cancel",
        icon: <CancelIcon />,
        tone: "secondary",
        onClick: () => navigate(APP_PATHS.watchdogs),
      },
      {
        id: "watchdog-editor-preview",
        label: "Preview",
        icon: <PreviewIcon />,
        tone: "secondary",
        loading: previewing,
        disabled: loading || saving,
        onClick: runPreview,
      },
      {
        id: "watchdog-editor-save",
        label: "Save Watchdog",
        icon: <SaveIcon />,
        tone: "primary",
        loading: saving,
        disabled: loading || previewing,
        onClick: saveWatchdog,
      },
    ],
    [loading, navigate, previewing, runPreview, saveWatchdog, saving]
  );

  const pageTitle = formState?.id ? `Edit Watchdog: ${formState.name || "Unnamed Watchdog"}` : "Create Watchdog";

  useRoutePageChrome({
    title: pageTitle,
    subtitle: "Define scope, targets, rule logic, remediation, and preview the exact devices that would trigger.",
    Icon: HeaderIcon,
    actions: pageHeaderActions,
  });

  return (
    <PageBodyFrame
      variant="content_panel"
      contentSx={{ gap: 2 }}
      main={
        <Stack spacing={2}>
          <Tabs value={activeTab} onChange={(_event, nextValue) => setActiveTab(nextValue)} sx={buildNavTabsSx()}>
            {MAIN_TABS.map((tab) => (
              <Tab key={tab.value} value={tab.value} label={tab.label} />
            ))}
          </Tabs>
          {loading ? <LinearProgress /> : null}
          {error ? <Alert severity="error">{error}</Alert> : null}
          {validationErrors.length ? (
            <Alert severity="warning">
              <Stack spacing={0.5}>
                {validationErrors.map((message) => (
                  <Typography key={message} variant="body2">
                    {message}
                  </Typography>
                ))}
              </Stack>
            </Alert>
          ) : null}

          {activeTab === "name" ? (
            <Stack spacing={2}>
              <SectionCard title="Identity" subtitle="Give operators a clear name and intent summary for this watchdog.">
                <TextField
                  label="Name"
                  value={formState.name || ""}
                  onChange={(event) => updateTopLevel({ name: event.target.value })}
                />
                <TextField
                  label="Description"
                  value={formState.description || ""}
                  onChange={(event) => updateTopLevel({ description: event.target.value })}
                  multiline
                  minRows={3}
                />
              </SectionCard>
              <SectionCard title="Policy State" subtitle="Enable, archive, and classify the incident severity for this watchdog.">
                <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
                  <TextField
                    select
                    label="Severity"
                    value={formState.severity || "warning"}
                    onChange={(event) => updateTopLevel({ severity: event.target.value })}
                    sx={{ flex: 1 }}
                  >
                    {normalizeArray(metadata?.severities).map((option) => (
                      <MenuItem key={option.value} value={option.value}>
                        {option.label}
                      </MenuItem>
                    ))}
                  </TextField>
                  <FormControlLabel
                    control={
                      <Checkbox
                        checked={Boolean(formState.enabled)}
                        onChange={(event) => updateTopLevel({ enabled: event.target.checked })}
                      />
                    }
                    label="Enabled"
                    sx={{ alignSelf: "center", ml: 0.25 }}
                  />
                  <FormControlLabel
                    control={
                      <Checkbox
                        checked={Boolean(formState.archived)}
                        onChange={(event) => updateTopLevel({ archived: event.target.checked })}
                      />
                    }
                    label="Archived"
                    sx={{ alignSelf: "center", ml: 0.25 }}
                  />
                </Stack>
              </SectionCard>
            </Stack>
          ) : null}

          {activeTab === "scope" ? (
            <Stack spacing={2}>
              <SectionCard title="Scope" subtitle="Decide whether the watchdog applies globally, only to selected sites, or globally with exclusions.">
                <Stack direction={{ xs: "column", lg: "row" }} spacing={1.25}>
                  {normalizeArray(metadata?.site_modes).map((option) => {
                    const selected = formState.site_mode === option.value;
                    return (
                      <Paper
                        key={option.value}
                        variant="outlined"
                        onClick={() => updateTopLevel({ site_mode: option.value })}
                        sx={{
                          flex: 1,
                          px: 2,
                          py: 1.5,
                          cursor: "pointer",
                          borderColor: selected ? "#7dd3fc" : "rgba(148,163,184,0.24)",
                          background: selected ? "rgba(125,211,252,0.12)" : "rgba(15,23,42,0.45)",
                        }}
                      >
                        <Typography sx={{ color: "#f8fafc", fontWeight: 700 }}>{option.label}</Typography>
                      </Paper>
                    );
                  })}
                </Stack>
                <Autocomplete
                  multiple
                  options={sites}
                  value={selectedSites}
                  getOptionLabel={(option) => option?.name || ""}
                  onChange={(_event, nextValue) =>
                    updateTopLevel({
                      site_ids: nextValue
                        .map((site) => Number(site?.id))
                        .filter((value) => Number.isFinite(value)),
                    })
                  }
                  renderInput={(params) => (
                    <TextField
                      {...params}
                      label={
                        formState.site_mode === "global_exclusions" ? "Excluded Sites" : "Scoped Sites"
                      }
                      placeholder="Select sites"
                    />
                  )}
                />
              </SectionCard>
              <SectionCard title="Evaluation Controls" subtitle="Tune cadence, debounce behavior, grace periods, and auto-resolution timing.">
                <Stack direction={{ xs: "column", xl: "row" }} spacing={1.25} flexWrap="wrap" useFlexGap>
                  <TextField
                    type="number"
                    label="Evaluate Every (sec)"
                    value={formState.evaluation_interval_seconds ?? 60}
                    onChange={(event) =>
                      updateTopLevel({
                        evaluation_interval_seconds: Math.max(30, Number(event.target.value || 60)),
                      })
                    }
                    sx={{ minWidth: 180 }}
                  />
                  <TextField
                    type="number"
                    label="Cooldown (sec)"
                    value={formState.cooldown_seconds ?? 900}
                    onChange={(event) =>
                      updateTopLevel({ cooldown_seconds: Math.max(0, Number(event.target.value || 0)) })
                    }
                    sx={{ minWidth: 180 }}
                  />
                  <TextField
                    type="number"
                    label="Auto Resolve After (sec)"
                    value={formState.auto_resolve_after_seconds ?? 300}
                    onChange={(event) =>
                      updateTopLevel({
                        auto_resolve_after_seconds: Math.max(0, Number(event.target.value || 0)),
                      })
                    }
                    sx={{ minWidth: 220 }}
                  />
                  <TextField
                    type="number"
                    label="Min Consecutive Matches"
                    value={formState.min_consecutive_matches ?? 1}
                    onChange={(event) =>
                      updateTopLevel({
                        min_consecutive_matches: Math.max(1, Number(event.target.value || 1)),
                      })
                    }
                    sx={{ minWidth: 220 }}
                  />
                  <TextField
                    type="number"
                    label="Boot Grace (sec)"
                    value={formState.boot_grace_seconds ?? 0}
                    onChange={(event) =>
                      updateTopLevel({ boot_grace_seconds: Math.max(0, Number(event.target.value || 0)) })
                    }
                    sx={{ minWidth: 180 }}
                  />
                </Stack>
              </SectionCard>
            </Stack>
          ) : null}

          {activeTab === "targets" ? (
            <Stack spacing={2}>
              <SectionCard title="Filter Targets" subtitle="Use existing Borealis device filters to keep watchdog assignments dynamic.">
                <Autocomplete
                  multiple
                  options={filters}
                  value={selectedFilterTargets}
                  getOptionLabel={(option) => option?.name || ""}
                  onChange={(_event, nextValue) => {
                    const filterTargets = nextValue.map((record) => ({
                      kind: "filter",
                      filter_id: Number(record.id),
                      name: record.name || "",
                    }));
                    const deviceTargets = normalizeArray(formState.targets).filter((target) => target?.kind !== "filter");
                    updateTopLevel({ targets: normalizeTargets([...deviceTargets, ...filterTargets]) });
                  }}
                  renderTags={(value, getTagProps) =>
                    value.map((option, index) => (
                      <Chip
                        {...getTagProps({ index })}
                        key={`${option.id}-${option.name}`}
                        label={option.name || `Filter ${option.id}`}
                      />
                    ))
                  }
                  renderInput={(params) => <TextField {...params} label="Device Filters" placeholder="Select filters" />}
                />
              </SectionCard>
              <SectionCard title="Explicit Device Targets" subtitle="Add one-off device targets directly. This is especially useful from the per-device Watchdogs tab.">
                <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
                  <TextField
                    label="Hostname"
                    value={deviceTargetDraft}
                    onChange={(event) => setDeviceTargetDraft(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter") {
                        event.preventDefault();
                        addDeviceTarget();
                      }
                    }}
                    helperText="Enter a hostname and press Add."
                    sx={{ flex: 1 }}
                  />
                  <Button startIcon={<AddIcon />} variant="contained" onClick={addDeviceTarget}>
                    Add Device
                  </Button>
                </Stack>
                {!explicitDeviceTargets.length ? (
                  <Typography variant="body2" sx={{ color: "#94a3b8" }}>
                    No explicit devices added yet.
                  </Typography>
                ) : (
                  <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
                    {explicitDeviceTargets.map((target) => (
                      <Chip
                        key={`${target.device_guid || "host"}-${target.hostname}`}
                        label={target.site_name ? `${target.hostname} (${target.site_name})` : target.hostname}
                        onDelete={() =>
                          updateTopLevel({
                            targets: normalizeArray(formState.targets).filter(
                              (item) =>
                                String(item?.hostname || "").toLowerCase() !==
                                String(target.hostname || "").toLowerCase()
                            ),
                          })
                        }
                      />
                    ))}
                  </Stack>
                )}
              </SectionCard>
            </Stack>
          ) : null}

          {activeTab === "rules" ? (
            <Stack spacing={2}>
              <SectionCard title="Rule Logic" subtitle="Combine rule results using all-rules or any-rule semantics.">
                <TextField
                  select
                  label="Match Mode"
                  value={formState.criteria?.match_mode || "all"}
                  onChange={(event) => updateCriteria({ match_mode: event.target.value })}
                  sx={{ maxWidth: 220 }}
                >
                  {normalizeArray(metadata?.match_modes).map((option) => (
                    <MenuItem key={option.value} value={option.value}>
                      {option.label}
                    </MenuItem>
                  ))}
                </TextField>
              </SectionCard>
              {normalizeArray(formState.criteria?.rules).map((rule) => (
                <RuleCard
                  key={rule.id}
                  rule={rule}
                  onChange={(nextRule) => updateRule(rule.id, nextRule)}
                  onRemove={() => removeRule(rule.id)}
                />
              ))}
              <Button startIcon={<AddIcon />} onClick={addRule}>
                Add Rule
              </Button>
            </Stack>
          ) : null}

          {activeTab === "actions" ? (
            <Stack spacing={2}>
              {normalizeArray(formState.actions?.actions).map((action) => (
                <ActionCard
                  key={action.id}
                  action={action}
                  assemblyOptions={assemblyOptions}
                  onChange={(nextAction) => updateAction(action.id, nextAction)}
                  onRemove={() => removeAction(action.id)}
                />
              ))}
              <Button startIcon={<AddIcon />} onClick={addAction}>
                Add Action
              </Button>
            </Stack>
          ) : null}

          {activeTab === "preview" ? (
            <Stack spacing={2}>
              <SectionCard
                title="Live Preview"
                subtitle="Resolve current targets and show exactly how each device evaluates against the watchdog."
                action={
                  <Button
                    startIcon={<PreviewIcon />}
                    variant="contained"
                    onClick={runPreview}
                    disabled={previewing || loading || saving}
                  >
                    Refresh Preview
                  </Button>
                }
              >
                <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
                  <Chip
                    label={`Resolved Devices: ${previewSummary.device_count || 0}`}
                    variant="outlined"
                    sx={{ borderColor: "rgba(148,163,184,0.35)", color: "#e2e8f0" }}
                  />
                  <Chip
                    label={`Matched Devices: ${previewSummary.matched_count || 0}`}
                    variant="outlined"
                    sx={{
                      borderColor: severityColor(formState.severity),
                      color: severityColor(formState.severity),
                    }}
                  />
                  <Chip
                    label={previewStale ? "Preview Out of Date" : "Preview Current"}
                    variant="outlined"
                    sx={{
                      borderColor: previewStale ? "#fcd34d" : "#86efac",
                      color: previewStale ? "#fcd34d" : "#86efac",
                    }}
                  />
                </Stack>
                {previewing ? <LinearProgress /> : null}
                {!previewRows.length ? (
                  <Typography variant="body2" sx={{ color: "#94a3b8" }}>
                    Run Preview to resolve devices and inspect the current watchdog outcome before saving.
                  </Typography>
                ) : (
                  <Box sx={GRID_WRAPPER_SX}>
                    <AgGridReact
                      ref={previewGridRef}
                      theme={gridTheme}
                      rowData={previewRows}
                      columnDefs={previewColumns}
                      defaultColDef={{
                        sortable: true,
                        filter: true,
                        resizable: true,
                      }}
                      animateRows
                      domLayout="autoHeight"
                    />
                  </Box>
                )}
                {previewRows.length ? (
                  <Typography variant="body2" sx={{ color: "#94a3b8" }}>
                    Preview updated {formatTimestamp(Math.floor(Date.now() / 1000))}.
                  </Typography>
                ) : null}
              </SectionCard>
            </Stack>
          ) : null}
        </Stack>
      }
    />
  );
}
