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
  Policy as HeaderIcon,
  PreviewRounded as PreviewIcon,
  SaveRounded as SaveIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { AllCommunityModule, ModuleRegistry } from "ag-grid-community";

import PageBodyFrame from "../../PageBodyFrame.jsx";
import {
  buildAssemblyIndex,
  parseAssembliesCollectionPayload,
  parseAssemblyExport,
} from "../../Assemblies/assemblyUtils";
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
} from "./shared.jsx";

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
  { value: "cpu_usage_percent", label: "CPU Usage" },
  { value: "memory_usage_percent", label: "Memory Usage" },
  { value: "uptime_above_seconds", label: "Uptime" },
  { value: "reboot_detected", label: "Reboot Detected" },
  { value: "service_pending_timeout", label: "Service Pending Timeout" },
  { value: "user_session_match", label: "Logged-In User Match" },
  { value: "process_presence", label: "Process Presence" },
  { value: "session_state", label: "Session State" },
  { value: "network_interface_change", label: "Network Interface Change" },
  { value: "drive_presence_change", label: "Drive Presence Change" },
  { value: "software_presence_or_version", label: "Software Presence / Version" },
  { value: "agent_version_status", label: "Agent Version Status" },
];

const ACTION_TYPE_OPTIONS = [
  { value: "do_nothing", label: "Do Nothing" },
  { value: "notification", label: "Engine Toast Notification" },
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
const USER_MATCH_MODE_OPTIONS = ["allowlist", "blocklist"];
const PATTERN_MODE_OPTIONS = ["normalized", "wildcard", "regex"];
const PROCESS_EXPECTATION_OPTIONS = ["present", "missing"];
const SESSION_MODE_OPTIONS = ["current", "transition"];
const SESSION_STATE_OPTIONS = ["active", "locked", "disconnected", "idle", "unknown"];
const SESSION_EVENT_OPTIONS = ["started", "ended", "locked", "unlocked", "rdp_started", "rdp_ended"];
const INTERFACE_CHANGE_OPTIONS = ["added", "removed", "mac_changed"];
const DRIVE_CHANGE_OPTIONS = ["added", "removed"];
const DRIVE_STORAGE_SCOPE_OPTIONS = [
  { value: "all", label: "All Storage" },
  { value: "fixed", label: "Fixed Storage" },
  { value: "removable", label: "Removable Storage" },
];
const DRIVE_WATCH_MODE_OPTIONS = [
  { value: "any", label: "Any Topology Change" },
  { value: "specific", label: "Specific Drives" },
];

const PREVIEW_AUTO_SIZE_COLUMNS = ["hostname", "site_name", "state", "status"];
const TARGET_SEARCH_MIN_CHARS = 3;

function makeId(prefix) {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return `${prefix}-${crypto.randomUUID()}`;
  }
  return `${prefix}-${Math.random().toString(36).slice(2, 10)}`;
}

function normalizeTextList(value) {
  if (Array.isArray(value)) {
    return value
      .map((item) => String(item || "").trim())
      .filter(Boolean)
      .filter((item, index, all) => all.findIndex((candidate) => candidate.toLowerCase() === item.toLowerCase()) === index);
  }
  return String(value || "")
    .split(/[\r\n,]+/)
    .map((item) => item.trim())
    .filter(Boolean)
    .filter((item, index, all) => all.findIndex((candidate) => candidate.toLowerCase() === item.toLowerCase()) === index);
}

function makeRule(type = "device_offline") {
  if (type === "storage_usage_percent") {
    return { id: makeId("rule"), type, threshold: 90, drive_mode: "all", drive: "" };
  }
  if (type === "service_state") {
    return { id: makeId("rule"), type, service_name: "", expected_status: "running" };
  }
  if (type === "agent_role_health") {
    return { id: makeId("rule"), type, role_name: "", trigger_statuses: ["unhealthy"], min_duration_seconds: 0 };
  }
  if (type === "cpu_usage_percent" || type === "memory_usage_percent") {
    return { id: makeId("rule"), type, threshold: 90, duration_seconds: 300 };
  }
  if (type === "uptime_above_seconds") {
    return { id: makeId("rule"), type, threshold_seconds: 2592000 };
  }
  if (type === "reboot_detected") {
    return { id: makeId("rule"), type };
  }
  if (type === "service_pending_timeout") {
    return { id: makeId("rule"), type, service_name: "", pending_action: "", timeout_seconds: 600 };
  }
  if (type === "user_session_match") {
    return { id: makeId("rule"), type, match_mode: "blocklist", pattern_mode: "normalized", patterns: [] };
  }
  if (type === "process_presence") {
    return { id: makeId("rule"), type, process_name: "", expectation: "present" };
  }
  if (type === "session_state") {
    return { id: makeId("rule"), type, session_mode: "current", rdp_only: false, states: ["active"], events: ["started"] };
  }
  if (type === "network_interface_change") {
    return { id: makeId("rule"), type, change_types: ["added", "removed", "mac_changed"] };
  }
  if (type === "drive_presence_change") {
    return {
      id: makeId("rule"),
      type,
      storage_scope: "all",
      watch_mode: "any",
      change_types: ["added", "removed"],
      drive_list: [],
    };
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
  if (type === "do_nothing") {
    return {
      id: makeId("action"),
      type,
      enabled: true,
    };
  }
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

function normalizeVariableDefinitions(vars = []) {
  return normalizeArray(vars)
    .map((raw) => {
      if (!raw || typeof raw !== "object") return null;
      const name =
        typeof raw.name === "string"
          ? raw.name.trim()
          : typeof raw.key === "string"
            ? raw.key.trim()
            : "";
      if (!name) return null;
      const label = typeof raw.label === "string" && raw.label.trim() ? raw.label.trim() : name;
      const type = typeof raw.type === "string" ? raw.type.toLowerCase() : "string";
      const required = Boolean(raw.required);
      const description = typeof raw.description === "string" ? raw.description : "";
      let defaultValue = "";
      if (Object.prototype.hasOwnProperty.call(raw, "default")) defaultValue = raw.default;
      else if (Object.prototype.hasOwnProperty.call(raw, "defaultValue")) defaultValue = raw.defaultValue;
      else if (Object.prototype.hasOwnProperty.call(raw, "default_value")) defaultValue = raw.default_value;
      return { name, label, type, required, description, default: defaultValue };
    })
    .filter(Boolean);
}

function coerceVariableValue(type, value) {
  if (type === "boolean") {
    if (typeof value === "boolean") return value;
    if (typeof value === "number") return value !== 0;
    if (value == null) return false;
    const normalized = String(value).trim().toLowerCase();
    if (!normalized) return false;
    return ["true", "1", "yes", "on"].includes(normalized);
  }
  if (type === "number") {
    if (value == null || value === "") return "";
    if (typeof value === "number" && Number.isFinite(value)) return String(value);
    const parsed = Number(value);
    return Number.isFinite(parsed) ? String(parsed) : "";
  }
  return value == null ? "" : String(value);
}

function mergeAssemblyVariables(docVars = [], storedVars = [], storedValueMap = {}) {
  const definitions = normalizeVariableDefinitions(docVars);
  const overrides = {};
  const storedMeta = {};

  normalizeArray(storedVars).forEach((raw) => {
    if (!raw || typeof raw !== "object") return;
    const name = typeof raw.name === "string" ? raw.name.trim() : "";
    if (!name) return;
    if (Object.prototype.hasOwnProperty.call(raw, "value")) overrides[name] = raw.value;
    else if (Object.prototype.hasOwnProperty.call(raw, "default")) overrides[name] = raw.default;
    storedMeta[name] = {
      label: typeof raw.label === "string" && raw.label.trim() ? raw.label.trim() : name,
      type: typeof raw.type === "string" ? raw.type.toLowerCase() : undefined,
      required: Boolean(raw.required),
      description: typeof raw.description === "string" ? raw.description : "",
      default: Object.prototype.hasOwnProperty.call(raw, "default") ? raw.default : "",
    };
  });

  if (storedValueMap && typeof storedValueMap === "object") {
    Object.entries(storedValueMap).forEach(([nameRaw, value]) => {
      const name = typeof nameRaw === "string" ? nameRaw.trim() : "";
      if (name) overrides[name] = value;
    });
  }

  const used = new Set();
  const merged = definitions.map((definition) => {
    const override = Object.prototype.hasOwnProperty.call(overrides, definition.name)
      ? overrides[definition.name]
      : undefined;
    used.add(definition.name);
    return {
      ...definition,
      value:
        override !== undefined
          ? coerceVariableValue(definition.type, override)
          : coerceVariableValue(definition.type, definition.default),
    };
  });

  normalizeArray(storedVars).forEach((raw) => {
    if (!raw || typeof raw !== "object") return;
    const name = typeof raw.name === "string" ? raw.name.trim() : "";
    if (!name || used.has(name)) return;
    const meta = storedMeta[name] || {};
    const type =
      meta.type ||
      (typeof overrides[name] === "boolean" ? "boolean" : typeof overrides[name] === "number" ? "number" : "string");
    const defaultValue = Object.prototype.hasOwnProperty.call(meta, "default") ? meta.default : "";
    const override = Object.prototype.hasOwnProperty.call(overrides, name)
      ? overrides[name]
      : Object.prototype.hasOwnProperty.call(raw, "value")
        ? raw.value
        : defaultValue;
    merged.push({
      name,
      label: meta.label || name,
      type,
      required: Boolean(meta.required),
      description: meta.description || "",
      default: defaultValue,
      value: coerceVariableValue(type, override),
    });
    used.add(name);
  });

  Object.entries(overrides).forEach(([nameRaw, value]) => {
    const name = typeof nameRaw === "string" ? nameRaw.trim() : "";
    if (!name || used.has(name)) return;
    const type = typeof value === "boolean" ? "boolean" : typeof value === "number" ? "number" : "string";
    merged.push({
      name,
      label: name,
      type,
      required: false,
      description: "",
      default: "",
      value: coerceVariableValue(type, value),
    });
  });

  return merged;
}

function buildVariableValuesMap(variables = []) {
  const next = {};
  normalizeArray(variables).forEach((variable) => {
    if (!variable || typeof variable.name !== "string" || !variable.name.trim()) return;
    if (Object.prototype.hasOwnProperty.call(variable, "value")) {
      next[variable.name.trim()] = variable.value;
    }
  });
  return next;
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
      if (String(target.kind || target.type || "").toLowerCase() === "all_devices" || target.all_devices === true) {
        return {
          kind: "all_devices",
          name: String(target.name || "All Devices in Scope").trim() || "All Devices in Scope",
        };
      }
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
        target.kind === "all_devices"
          ? "all_devices"
          : target.kind === "filter"
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
    if (next.type === "storage_usage_percent") {
      next.drive_mode = String(next.drive_mode || (next.drive ? "specific" : "all")).trim() || "all";
    }
    if (next.type === "agent_role_health") {
      next.trigger_statuses = normalizeArray(next.trigger_statuses).map((item) => String(item).toLowerCase());
      if (!next.trigger_statuses.length) next.trigger_statuses = ["unhealthy"];
      next.min_duration_seconds = Math.max(0, Number(next.min_duration_seconds || 0));
    }
    if (next.type === "cpu_usage_percent" || next.type === "memory_usage_percent") {
      next.threshold = Math.max(1, Math.min(100, Number(next.threshold || 90)));
      next.duration_seconds = Math.max(0, Number(next.duration_seconds || 300));
    }
    if (next.type === "uptime_above_seconds") {
      next.threshold_seconds = Math.max(60, Number(next.threshold_seconds || 2592000));
    }
    if (next.type === "service_pending_timeout") {
      next.pending_action = String(next.pending_action || "").trim().toLowerCase();
      next.timeout_seconds = Math.max(0, Number(next.timeout_seconds || 600));
    }
    if (next.type === "user_session_match") {
      next.match_mode = String(next.match_mode || "blocklist").trim().toLowerCase() || "blocklist";
      next.pattern_mode = String(next.pattern_mode || "normalized").trim().toLowerCase() || "normalized";
      next.patterns = normalizeTextList(next.patterns);
    }
    if (next.type === "process_presence") {
      next.expectation = String(next.expectation || "present").trim().toLowerCase() || "present";
    }
    if (next.type === "session_state") {
      next.session_mode = String(next.session_mode || "current").trim().toLowerCase() || "current";
      next.rdp_only = Boolean(next.rdp_only);
      next.states = normalizeTextList(next.states).map((item) => item.toLowerCase());
      if (!next.states.length) next.states = ["active"];
      next.events = normalizeTextList(next.events).map((item) => item.toLowerCase());
      if (!next.events.length) next.events = ["started"];
    }
    if (next.type === "network_interface_change") {
      next.change_types = normalizeTextList(next.change_types).map((item) => item.toLowerCase());
      if (!next.change_types.length) next.change_types = [...INTERFACE_CHANGE_OPTIONS];
    }
    if (next.type === "drive_presence_change") {
      next.storage_scope = String(next.storage_scope || "all").trim().toLowerCase() || "all";
      next.watch_mode = String(next.watch_mode || "any").trim().toLowerCase() || "any";
      next.change_types = normalizeTextList(next.change_types).map((item) => item.toLowerCase());
      if (!next.change_types.length) next.change_types = [...DRIVE_CHANGE_OPTIONS];
      next.drive_list = normalizeTextList(next.drive_list);
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

function sanitizeActionForPayload(action) {
  const normalized = { ...makeAction(action?.type || "notification"), ...(action || {}) };
  const payload = {
    id: normalized.id || undefined,
    type: String(normalized.type || "notification").trim().toLowerCase() || "notification",
    enabled: normalized.enabled !== false,
  };

  if (payload.type === "notification") {
    payload.variant = String(normalized.variant || "warning").trim().toLowerCase() || "warning";
    payload.title = String(normalized.title || "").trim();
    payload.message_template = String(normalized.message_template || "").trim();
    return payload;
  }

  if (payload.type === "do_nothing") {
    return payload;
  }

  if (payload.type === "service_control") {
    payload.service_name = String(normalized.service_name || "").trim();
    payload.action = String(normalized.action || "restart").trim().toLowerCase() || "restart";
    return payload;
  }

  if (payload.type === "assembly") {
    payload.assembly_guid = String(normalized.assembly_guid || normalized.assemblyGuid || "").trim().toLowerCase();
    payload.run_mode = String(normalized.run_mode || "system").trim().toLowerCase() || "system";
    payload.execution_context = String(normalized.execution_context || "local").trim().toLowerCase() || "local";
    payload.variable_values =
      normalized.variable_values && typeof normalized.variable_values === "object"
        ? { ...normalized.variable_values }
        : buildVariableValuesMap(normalized.variables);
    return payload;
  }

  return payload;
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
      actions: normalizeArray(state?.actions?.actions || [])
        .map((action) => sanitizeActionForPayload(action))
        .filter(Boolean),
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
        <Stack spacing={1.25}>
          <TextField
            select
            label="Drive Scope"
            value={rule.drive_mode || (rule.drive ? "specific" : "all")}
            onChange={(event) =>
              onChange({
                ...rule,
                drive_mode: event.target.value,
                drive: event.target.value === "all" ? "" : rule.drive || "",
              })
            }
            sx={{ maxWidth: 240 }}
          >
            <MenuItem value="all">All Drives</MenuItem>
            <MenuItem value="specific">Specific Drive</MenuItem>
          </TextField>
          <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
            <TextField
              label="Drive Letter / Mount"
              value={rule.drive || ""}
              onChange={(event) => onChange({ ...rule, drive: event.target.value })}
              helperText={
                (rule.drive_mode || (rule.drive ? "specific" : "all")) === "specific"
                  ? "Examples: C, C:, /, /var"
                  : "All drives will be evaluated."
              }
              disabled={(rule.drive_mode || (rule.drive ? "specific" : "all")) !== "specific"}
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
          <TextField
            type="number"
            label="Minimum Duration (Seconds)"
            value={rule.min_duration_seconds ?? 0}
            onChange={(event) =>
              onChange({ ...rule, min_duration_seconds: Math.max(0, Number(event.target.value || 0)) })
            }
            helperText="Wait this long before the role issue triggers an alert."
          />
        </Stack>
      ) : null}
      {type === "cpu_usage_percent" || type === "memory_usage_percent" ? (
        <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
          <TextField
            type="number"
            label="Threshold (%)"
            value={rule.threshold ?? 90}
            onChange={(event) =>
              onChange({ ...rule, threshold: Math.max(1, Math.min(100, Number(event.target.value || 90))) })
            }
            sx={{ flex: 1 }}
          />
          <TextField
            type="number"
            label="Duration (Seconds)"
            value={rule.duration_seconds ?? 300}
            onChange={(event) =>
              onChange({ ...rule, duration_seconds: Math.max(0, Number(event.target.value || 300)) })
            }
            sx={{ flex: 1 }}
          />
        </Stack>
      ) : null}
      {type === "uptime_above_seconds" ? (
        <TextField
          type="number"
          label="Uptime Threshold (Seconds)"
          value={rule.threshold_seconds ?? 2592000}
          onChange={(event) =>
            onChange({ ...rule, threshold_seconds: Math.max(60, Number(event.target.value || 2592000)) })
          }
        />
      ) : null}
      {type === "reboot_detected" ? (
        <Alert severity="info" sx={{ alignItems: "center" }}>
          Borealis will establish a baseline snapshot first, then alert when the device uptime drops on a later
          evaluation.
        </Alert>
      ) : null}
      {type === "service_pending_timeout" ? (
        <Stack spacing={1.25}>
          <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
            <TextField
              label="Service Name"
              value={rule.service_name || ""}
              onChange={(event) => onChange({ ...rule, service_name: event.target.value })}
              helperText="Optional. Leave blank to watch any pending service action."
              sx={{ flex: 1 }}
            />
            <TextField
              select
              label="Pending Action"
              value={rule.pending_action || ""}
              onChange={(event) => onChange({ ...rule, pending_action: event.target.value })}
              sx={{ width: { xs: "100%", md: 220 } }}
            >
              <MenuItem value="">Any Pending Action</MenuItem>
              <MenuItem value="start">Start</MenuItem>
              <MenuItem value="stop">Stop</MenuItem>
              <MenuItem value="restart">Restart</MenuItem>
            </TextField>
          </Stack>
          <TextField
            type="number"
            label="Timeout (Seconds)"
            value={rule.timeout_seconds ?? 600}
            onChange={(event) =>
              onChange({ ...rule, timeout_seconds: Math.max(0, Number(event.target.value || 600)) })
            }
            sx={{ maxWidth: 240 }}
          />
        </Stack>
      ) : null}
      {type === "user_session_match" ? (
        <Stack spacing={1.25}>
          <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
            <TextField
              select
              label="Mode"
              value={rule.match_mode || "blocklist"}
              onChange={(event) => onChange({ ...rule, match_mode: event.target.value })}
              sx={{ flex: 1 }}
            >
              {USER_MATCH_MODE_OPTIONS.map((option) => (
                <MenuItem key={option} value={option}>
                  {option === "allowlist" ? "Allowlist" : "Blocklist"}
                </MenuItem>
              ))}
            </TextField>
            <TextField
              select
              label="Pattern Mode"
              value={rule.pattern_mode || "normalized"}
              onChange={(event) => onChange({ ...rule, pattern_mode: event.target.value })}
              sx={{ flex: 1 }}
            >
              {PATTERN_MODE_OPTIONS.map((option) => (
                <MenuItem key={option} value={option}>
                  {option === "normalized" ? "Normalized" : option === "wildcard" ? "Wildcard" : "Regex"}
                </MenuItem>
              ))}
            </TextField>
          </Stack>
          <TextField
            label="User Patterns"
            value={normalizeTextList(rule.patterns).join("\n")}
            onChange={(event) => onChange({ ...rule, patterns: normalizeTextList(event.target.value) })}
            multiline
            minRows={3}
            helperText="Enter one pattern per line. Normalized mode expands usernames like alice into common domain forms."
          />
        </Stack>
      ) : null}
      {type === "process_presence" ? (
        <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
          <TextField
            label="Executable Name"
            value={rule.process_name || ""}
            onChange={(event) => onChange({ ...rule, process_name: event.target.value })}
            helperText="Examples: explorer.exe, python.exe"
            sx={{ flex: 1 }}
          />
          <TextField
            select
            label="Expectation"
            value={rule.expectation || "present"}
            onChange={(event) => onChange({ ...rule, expectation: event.target.value })}
            sx={{ width: { xs: "100%", md: 200 } }}
          >
            {PROCESS_EXPECTATION_OPTIONS.map((option) => (
              <MenuItem key={option} value={option}>
                {option === "present" ? "Must Be Running" : "Must Be Missing"}
              </MenuItem>
            ))}
          </TextField>
        </Stack>
      ) : null}
      {type === "session_state" ? (
        <Stack spacing={1.25}>
          <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
            <TextField
              select
              label="Session Mode"
              value={rule.session_mode || "current"}
              onChange={(event) => {
                const nextMode = event.target.value;
                const normalizedStates = normalizeTextList(rule.states);
                const normalizedEvents = normalizeTextList(rule.events);
                onChange({
                  ...rule,
                  session_mode: nextMode,
                  states: nextMode === "current" ? (normalizedStates.length ? normalizedStates : ["active"]) : normalizedStates,
                  events: nextMode === "transition" ? (normalizedEvents.length ? normalizedEvents : ["started"]) : normalizedEvents,
                });
              }}
              sx={{ flex: 1 }}
            >
              {SESSION_MODE_OPTIONS.map((option) => (
                <MenuItem key={option} value={option}>
                  {option === "current" ? "Current Session State" : "Session Transition"}
                </MenuItem>
              ))}
            </TextField>
            <FormControlLabel
              control={
                <Checkbox
                  checked={Boolean(rule.rdp_only)}
                  onChange={(event) => onChange({ ...rule, rdp_only: event.target.checked })}
                />
              }
              label="RDP Only"
              sx={{ alignSelf: "center", ml: 0.25 }}
            />
          </Stack>
          {String(rule.session_mode || "current") === "current" ? (
            <Autocomplete
              multiple
              options={SESSION_STATE_OPTIONS}
              value={normalizeTextList(rule.states)}
              onChange={(_event, nextValue) => onChange({ ...rule, states: nextValue })}
              renderInput={(params) => <TextField {...params} label="Trigger States" />}
            />
          ) : (
            <Autocomplete
              multiple
              options={SESSION_EVENT_OPTIONS}
              value={normalizeTextList(rule.events)}
              onChange={(_event, nextValue) => onChange({ ...rule, events: nextValue })}
              renderInput={(params) => <TextField {...params} label="Trigger Events" />}
            />
          )}
        </Stack>
      ) : null}
      {type === "network_interface_change" ? (
        <Autocomplete
          multiple
          options={INTERFACE_CHANGE_OPTIONS}
          value={normalizeTextList(rule.change_types)}
          onChange={(_event, nextValue) => onChange({ ...rule, change_types: nextValue })}
          renderInput={(params) => <TextField {...params} label="Change Types" />}
        />
      ) : null}
      {type === "drive_presence_change" ? (
        <Stack spacing={1.25}>
          <Stack direction={{ xs: "column", md: "row" }} spacing={1.25}>
            <TextField
              select
              label="Storage Scope"
              value={rule.storage_scope || "all"}
              onChange={(event) => onChange({ ...rule, storage_scope: event.target.value })}
              sx={{ flex: 1 }}
            >
              {DRIVE_STORAGE_SCOPE_OPTIONS.map((option) => (
                <MenuItem key={option.value} value={option.value}>
                  {option.label}
                </MenuItem>
              ))}
            </TextField>
            <TextField
              select
              label="Watch Mode"
              value={rule.watch_mode || "any"}
              onChange={(event) => onChange({ ...rule, watch_mode: event.target.value })}
              sx={{ flex: 1 }}
            >
              {DRIVE_WATCH_MODE_OPTIONS.map((option) => (
                <MenuItem key={option.value} value={option.value}>
                  {option.label}
                </MenuItem>
              ))}
            </TextField>
          </Stack>
          <Autocomplete
            multiple
            options={DRIVE_CHANGE_OPTIONS}
            value={normalizeTextList(rule.change_types)}
            onChange={(_event, nextValue) => onChange({ ...rule, change_types: nextValue })}
            renderInput={(params) => <TextField {...params} label="Change Types" />}
          />
          {String(rule.watch_mode || "any") === "specific" ? (
            <TextField
              label="Expected Drives"
              value={normalizeTextList(rule.drive_list).join("\n")}
              onChange={(event) => onChange({ ...rule, drive_list: normalizeTextList(event.target.value) })}
              multiline
              minRows={3}
              helperText="Enter one drive per line, like C:, D:, /mnt/backup, or E: for removable media."
            />
          ) : (
            <Alert severity="info" sx={{ alignItems: "center" }}>
              Borealis will establish a baseline snapshot first, then alert when drives are added or removed.
            </Alert>
          )}
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

function ActionCard({ action, assemblyOptions, onAssemblyChange, onVariableChange, onChange, onRemove }) {
  const type = String(action?.type || "notification");
  const selectedAssembly = assemblyOptions.find(
    (item) => String(item?.assemblyGuid || "").toLowerCase() === String(action?.assembly_guid || "").toLowerCase()
  );
  const variables = normalizeArray(action?.variables).filter(
    (variable) => variable && typeof variable.name === "string" && variable.name.trim()
  );
  const variableLoadError = String(action?.assembly_variable_error || "").trim();
  const variableLoading = Boolean(action?.assembly_variables_loading);

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
      {type === "do_nothing" ? (
        <Alert severity="info" sx={{ alignItems: "center" }}>
          Borealis will still open and track the incident, but it will not send a toast notification or run any remediation.
        </Alert>
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
            onChange={(event) => onAssemblyChange(event.target.value)}
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
          {action.assembly_guid ? (
            <SectionCard
              title="Runtime Variables"
              subtitle="Pass ad-hoc values into the selected assembly the same way Borealis does for scheduled jobs."
            >
              {variableLoading ? <LinearProgress /> : null}
              {variableLoadError ? <Alert severity="warning">{variableLoadError}</Alert> : null}
              {selectedAssembly?.kind === "workflow" ? (
                <Alert severity="info">
                  Workflow-backed watchdog actions store these values in the workflow trigger metadata. Script and
                  Ansible actions apply them directly at runtime.
                </Alert>
              ) : null}
              {!variableLoading && !variableLoadError && variables.length ? (
                <Stack spacing={1.25}>
                  {variables.map((variable) => (
                    <Box key={variable.name}>
                      {variable.type === "boolean" ? (
                        <>
                          <FormControlLabel
                            sx={{
                              color: "#e2e8f0",
                              "& .MuiTypography-root": { color: "#e2e8f0", fontWeight: 500 },
                            }}
                            control={
                              <Checkbox
                                size="small"
                                checked={Boolean(variable.value)}
                                onChange={(event) => onVariableChange(variable.name, event.target.checked)}
                              />
                            }
                            label={`${variable.label || variable.name}${variable.required ? " *" : ""}`}
                          />
                          {variable.description ? (
                            <Typography variant="caption" sx={{ color: "#94a3b8", display: "block", ml: 4 }}>
                              {variable.description}
                            </Typography>
                          ) : null}
                        </>
                      ) : (
                        <TextField
                          fullWidth
                          size="small"
                          label={`${variable.label || variable.name}${variable.required ? " *" : ""}`}
                          type={
                            variable.type === "number"
                              ? "number"
                              : variable.type === "credential"
                                ? "password"
                                : "text"
                          }
                          value={variable.value ?? ""}
                          onChange={(event) => onVariableChange(variable.name, event.target.value)}
                          InputLabelProps={{ shrink: true }}
                          helperText={variable.description || ""}
                        />
                      )}
                    </Box>
                  ))}
                </Stack>
              ) : null}
              {!variableLoading && !variableLoadError && !variables.length ? (
                <Typography variant="body2" sx={{ color: "#94a3b8" }}>
                  No runtime variables defined for this assembly.
                </Typography>
              ) : null}
            </SectionCard>
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
  const assemblyExportCacheRef = useRef(new Map());
  const pendingAssemblyHydrationRef = useRef(new Set());
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
  const [deviceTargetSearch, setDeviceTargetSearch] = useState("");
  const [filterTargetSearch, setFilterTargetSearch] = useState("");
  const [deviceTargetMatches, setDeviceTargetMatches] = useState([]);
  const [deviceTargetSearchLoading, setDeviceTargetSearchLoading] = useState(false);

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
      setFilters(
        Array.isArray(filtersPayload?.filters)
          ? filtersPayload.filters
          : Array.isArray(filtersPayload?.items)
            ? filtersPayload.items
            : []
      );

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

  const loadAssemblyExport = useCallback(async (assemblyGuid) => {
    const cacheKey = String(assemblyGuid || "").trim().toLowerCase();
    if (!cacheKey) {
      throw new Error("Select an assembly before loading runtime variables.");
    }
    if (assemblyExportCacheRef.current.has(cacheKey)) {
      return assemblyExportCacheRef.current.get(cacheKey);
    }
    const response = await fetch(`/api/assemblies/${encodeURIComponent(cacheKey)}/export`, {
      credentials: "include",
      cache: "no-store",
    });
    if (!response.ok) {
      throw new Error(`Unable to load assembly definition (HTTP ${response.status}).`);
    }
    const payload = await response.json().catch(() => ({}));
    assemblyExportCacheRef.current.set(cacheKey, payload);
    return payload;
  }, []);

  const hydrateAssemblyActionVariables = useCallback(
    async (action) => {
      const assemblyGuid = String(action?.assembly_guid || action?.assemblyGuid || "").trim().toLowerCase();
      if (!assemblyGuid) {
        return {
          ...action,
          assembly_guid: "",
          variables: [],
          variable_values: {},
          assembly_variables_source_guid: "",
          assembly_variable_error: "",
          assembly_variables_loading: false,
        };
      }
      try {
        const exportDoc = await loadAssemblyExport(assemblyGuid);
        const parsed = parseAssemblyExport(exportDoc);
        const docVars = Array.isArray(parsed?.rawVariables) ? parsed.rawVariables : [];
        const mergedVariables = mergeAssemblyVariables(docVars, action?.variables, action?.variable_values);
        return {
          ...action,
          assembly_guid: assemblyGuid,
          variables: mergedVariables,
          variable_values: buildVariableValuesMap(mergedVariables),
          assembly_variables_source_guid: assemblyGuid,
          assembly_variable_error: "",
          assembly_variables_loading: false,
        };
      } catch (err) {
        return {
          ...action,
          assembly_guid: assemblyGuid,
          variables: normalizeArray(action?.variables),
          variable_values:
            action?.variable_values && typeof action.variable_values === "object" ? { ...action.variable_values } : {},
          assembly_variables_source_guid: "",
          assembly_variable_error: String(err?.message || err || "Unable to load assembly variables."),
          assembly_variables_loading: false,
        };
      }
    },
    [loadAssemblyExport]
  );

  const selectedFilterTargets = useMemo(() => {
    const ids = new Set(
      normalizeArray(formState.targets)
        .filter((target) => target?.kind === "filter")
        .map((target) => Number(target.filter_id))
    );
    return filters.filter((record) => ids.has(Number(record.id)));
  }, [filters, formState.targets]);

  const scopeWideTargetEnabled = useMemo(
    () => normalizeArray(formState.targets).some((target) => target?.kind === "all_devices"),
    [formState.targets]
  );

  const explicitDeviceTargets = useMemo(
    () => normalizeArray(formState.targets).filter((target) => target?.kind === "device"),
    [formState.targets]
  );

  const selectedDeviceTargetKeys = useMemo(
    () =>
      new Set(
        explicitDeviceTargets.map((target) =>
          `${String(target.device_guid || "").trim().toLowerCase()}::${String(target.hostname || "").trim().toLowerCase()}`
        )
      ),
    [explicitDeviceTargets]
  );

  const selectedFilterTargetIds = useMemo(
    () => new Set(selectedFilterTargets.map((record) => Number(record.id))),
    [selectedFilterTargets]
  );

  useEffect(() => {
    const trimmedQuery = String(deviceTargetSearch || "").trim();
    if (trimmedQuery.length < TARGET_SEARCH_MIN_CHARS) {
      setDeviceTargetSearchLoading(false);
      setDeviceTargetMatches([]);
      return undefined;
    }

    const controller = new AbortController();
    const timer = window.setTimeout(async () => {
      setDeviceTargetSearchLoading(true);
      try {
        const response = await fetch(
          `/api/devices/search?hostname=${encodeURIComponent(trimmedQuery)}`,
          {
            credentials: "include",
            cache: "no-store",
            signal: controller.signal,
          }
        );
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(payload?.errors?.[0] || payload?.error || payload?.message || `HTTP ${response.status}`);
        }
        if (controller.signal.aborted) return;
        const rows = Array.isArray(payload?.devices) ? payload.devices : [];
        setDeviceTargetMatches(
          rows.map((device, index) => ({
            id:
              String(device?.agent_guid || "").trim().toLowerCase() ||
              `${String(device?.hostname || "").trim().toLowerCase()}-${String(device?.site_id ?? "").trim()}-${index}`,
            hostname: String(device?.hostname || "").trim(),
            site_name: String(device?.site_name || "").trim() || "Not Configured",
            site_id: device?.site_id ?? null,
            device_guid: String(device?.agent_guid || "").trim(),
            agent_id: String(device?.agent_id || "").trim(),
            connection_type: String(device?.connection_type || "").trim(),
          }))
        );
      } catch (err) {
        if (!controller.signal.aborted) {
          setDeviceTargetMatches([]);
          setError(String(err?.message || err || "Unable to search devices."));
        }
      } finally {
        if (!controller.signal.aborted) {
          setDeviceTargetSearchLoading(false);
        }
      }
    }, 180);

    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [deviceTargetSearch]);

  const matchingFilterRows = useMemo(() => {
    const trimmedQuery = String(filterTargetSearch || "").trim().toLowerCase();
    if (trimmedQuery.length < TARGET_SEARCH_MIN_CHARS) return [];
    return normalizeArray(filters)
      .filter((record) => {
        const haystack = [
          record?.name,
          record?.description,
          record?.site_mode,
          Array.isArray(record?.site_names) ? record.site_names.join(" ") : "",
        ]
          .filter(Boolean)
          .join(" ")
          .toLowerCase();
        return haystack.includes(trimmedQuery);
      })
      .map((record) => ({
        id: Number(record?.id || 0),
        name: String(record?.name || "").trim() || `Filter ${record?.id || ""}`,
        description: String(record?.description || "").trim(),
        scope:
          String(record?.site_mode || "").trim() === "specific_sites"
            ? "Specific Sites"
            : String(record?.site_mode || "").trim() === "global_exclusions"
              ? "Global w/ Exclusions"
              : "Global",
        device_count: Number(record?.matching_device_count || record?.device_count || 0),
      }))
      .filter((record) => Number.isFinite(record.id) && record.id > 0);
  }, [filterTargetSearch, filters]);

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

  const deviceSearchOverlay = useMemo(() => {
    const trimmedQuery = String(deviceTargetSearch || "").trim();
    if (trimmedQuery.length < TARGET_SEARCH_MIN_CHARS) {
      return `Type at least ${TARGET_SEARCH_MIN_CHARS} characters to search devices.`;
    }
    if (deviceTargetSearchLoading) {
      return "Searching devices...";
    }
    return "No devices match your search.";
  }, [deviceTargetSearch, deviceTargetSearchLoading]);

  const filterSearchOverlay = useMemo(() => {
    const trimmedQuery = String(filterTargetSearch || "").trim();
    if (trimmedQuery.length < TARGET_SEARCH_MIN_CHARS) {
      return `Type at least ${TARGET_SEARCH_MIN_CHARS} characters to search filters.`;
    }
    if (!filters.length) {
      return "No device filters available.";
    }
    return "No filters match your search.";
  }, [filterTargetSearch, filters.length]);

  const targetSearchGridDefaultColDef = useMemo(
    () => ({
      sortable: true,
      filter: false,
      resizable: true,
      floatingFilter: false,
    }),
    []
  );

  const updateTopLevel = useCallback((patch) => {
    setFormState((prev) => ({ ...prev, ...patch }));
  }, []);

  const addDeviceTarget = useCallback((device) => {
    const hostname = String(device?.hostname || "").trim();
    if (!hostname) return;
    setFormState((prev) => ({
      ...prev,
      targets: normalizeTargets([
        ...(prev.targets || []),
        {
          kind: "device",
          hostname,
          device_guid: String(device?.device_guid || device?.agent_guid || "").trim(),
          site_id: device?.site_id ?? null,
          site_name: String(device?.site_name || "").trim(),
        },
      ]),
    }));
  }, []);

  const addFilterTarget = useCallback((record) => {
    const filterId = Number(record?.id || record?.filter_id || 0);
    if (!Number.isFinite(filterId) || filterId <= 0) return;
    setFormState((prev) => ({
      ...prev,
      targets: normalizeTargets([
        ...(prev.targets || []),
        {
          kind: "filter",
          filter_id: filterId,
          name: String(record?.name || "").trim(),
        },
      ]),
    }));
  }, []);

  const toggleAllDevicesTarget = useCallback((enabled) => {
    setFormState((prev) => {
      const otherTargets = normalizeArray(prev.targets).filter((target) => target?.kind !== "all_devices");
      return {
        ...prev,
        targets: enabled
          ? normalizeTargets([{ kind: "all_devices", name: "All Devices in Scope" }, ...otherTargets])
          : normalizeTargets(otherTargets),
      };
    });
  }, []);

  const deviceSearchColumns = useMemo(
    () => [
      { field: "site_name", headerName: "Site", minWidth: 170, width: 190 },
      { field: "hostname", headerName: "Hostname", minWidth: 220, width: 240 },
      { field: "agent_id", headerName: "Agent ID", minWidth: 160, width: 180 },
      { field: "connection_type", headerName: "Connection", minWidth: 140, width: 150 },
      {
        field: "actions",
        headerName: "",
        minWidth: 120,
        width: 120,
        sortable: false,
        filter: false,
        cellRenderer: (params) => {
          const row = params?.data || {};
          const selectedKey = `${String(row.device_guid || "").trim().toLowerCase()}::${String(row.hostname || "").trim().toLowerCase()}`;
          const alreadyAdded = selectedDeviceTargetKeys.has(selectedKey);
          return (
            <Box sx={{ display: "flex", justifyContent: "flex-end", width: "100%" }}>
              <Button
                size="small"
                variant={alreadyAdded ? "outlined" : "contained"}
                disabled={alreadyAdded}
                startIcon={!alreadyAdded ? <AddIcon fontSize="small" /> : null}
                onClick={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                  addDeviceTarget(row);
                }}
                sx={{ minWidth: 92, textTransform: "none" }}
              >
                {alreadyAdded ? "Added" : "Add"}
              </Button>
            </Box>
          );
        },
      },
    ],
    [addDeviceTarget, selectedDeviceTargetKeys]
  );

  const filterSearchColumns = useMemo(
    () => [
      { field: "name", headerName: "Name", minWidth: 220, width: 240 },
      { field: "description", headerName: "Description", minWidth: 240, width: 320 },
      { field: "scope", headerName: "Scope", minWidth: 170, width: 190 },
      {
        field: "device_count",
        headerName: "Devices",
        minWidth: 110,
        width: 120,
        valueFormatter: (params) => `${Number(params.value || 0)}`,
      },
      {
        field: "actions",
        headerName: "",
        minWidth: 120,
        width: 120,
        sortable: false,
        filter: false,
        cellRenderer: (params) => {
          const row = params?.data || {};
          const filterId = Number(row.id || 0);
          const alreadyAdded = selectedFilterTargetIds.has(filterId);
          return (
            <Box sx={{ display: "flex", justifyContent: "flex-end", width: "100%" }}>
              <Button
                size="small"
                variant={alreadyAdded ? "outlined" : "contained"}
                disabled={alreadyAdded}
                startIcon={!alreadyAdded ? <AddIcon fontSize="small" /> : null}
                onClick={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                  addFilterTarget(row);
                }}
                sx={{ minWidth: 92, textTransform: "none" }}
              >
                {alreadyAdded ? "Added" : "Add"}
              </Button>
            </Box>
          );
        },
      },
    ],
    [addFilterTarget, selectedFilterTargetIds]
  );

  const updateCriteria = useCallback((patch) => {
    setFormState((prev) => ({ ...prev, criteria: { ...(prev.criteria || {}), ...patch } }));
  }, []);

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

  const updateAssemblySelection = useCallback((actionId, assemblyGuid) => {
    const normalizedGuid = String(assemblyGuid || "").trim().toLowerCase();
    setFormState((prev) => ({
      ...prev,
      actions: {
        ...(prev.actions || {}),
        actions: normalizeActions(
          normalizeArray(prev.actions?.actions).map((action) => {
            if (action.id !== actionId) return action;
            return {
              ...action,
              assembly_guid: normalizedGuid,
              variables: [],
              variable_values: {},
              assembly_variables_source_guid: "",
              assembly_variable_error: "",
              assembly_variables_loading: Boolean(normalizedGuid),
            };
          })
        ),
      },
    }));
  }, []);

  const updateActionVariable = useCallback((actionId, variableName, value) => {
    if (!actionId || !variableName) return;
    setFormState((prev) => ({
      ...prev,
      actions: {
        ...(prev.actions || {}),
        actions: normalizeActions(
          normalizeArray(prev.actions?.actions).map((action) => {
            if (action.id !== actionId) return action;
            const nextVariables = normalizeArray(action.variables).map((variable) =>
              variable?.name === variableName
                ? { ...variable, value: coerceVariableValue(variable.type || "string", value) }
                : variable
            );
            return {
              ...action,
              variables: nextVariables,
              variable_values: buildVariableValuesMap(nextVariables),
            };
          })
        ),
      },
    }));
  }, []);

  useEffect(() => {
    normalizeArray(formState.actions?.actions).forEach((action) => {
      if (!action || String(action.type || "").trim().toLowerCase() !== "assembly") return;
      const assemblyGuid = String(action.assembly_guid || "").trim().toLowerCase();
      if (!assemblyGuid) return;
      const hydratedGuid = String(action.assembly_variables_source_guid || "").trim().toLowerCase();
      if (hydratedGuid === assemblyGuid && Array.isArray(action.variables)) return;

      const requestKey = `${action.id}:${assemblyGuid}`;
      if (pendingAssemblyHydrationRef.current.has(requestKey)) return;
      pendingAssemblyHydrationRef.current.add(requestKey);

      setFormState((prev) => ({
        ...prev,
        actions: {
          ...(prev.actions || {}),
          actions: normalizeActions(
            normalizeArray(prev.actions?.actions).map((candidate) => {
              if (candidate.id !== action.id) return candidate;
              if (String(candidate.assembly_guid || "").trim().toLowerCase() !== assemblyGuid) return candidate;
              return { ...candidate, assembly_variables_loading: true, assembly_variable_error: "" };
            })
          ),
        },
      }));

      hydrateAssemblyActionVariables(action)
        .then((hydratedAction) => {
          setFormState((prev) => ({
            ...prev,
            actions: {
              ...(prev.actions || {}),
              actions: normalizeActions(
                normalizeArray(prev.actions?.actions).map((candidate) => {
                  if (candidate.id !== action.id) return candidate;
                  if (String(candidate.assembly_guid || "").trim().toLowerCase() !== assemblyGuid) return candidate;
                  return { ...candidate, ...hydratedAction };
                })
              ),
            },
          }));
        })
        .finally(() => {
          pendingAssemblyHydrationRef.current.delete(requestKey);
        });
    });
  }, [formState.actions?.actions, hydrateAssemblyActionVariables]);

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
      navigate(APP_PATHS.watchdogs);
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
              <SectionCard
                title="Scope-wide Targeting"
                subtitle="Target every device currently inside the selected scope. This is the fastest way to monitor all devices in the scoped sites."
              >
                <FormControlLabel
                  control={
                    <Checkbox
                      checked={scopeWideTargetEnabled}
                      onChange={(event) => toggleAllDevicesTarget(event.target.checked)}
                    />
                  }
                  label="Include All Devices in Scope"
                />
                {scopeWideTargetEnabled ? (
                  <Chip
                    label="All Devices in Scope"
                    sx={{ alignSelf: "flex-start" }}
                    onDelete={() => toggleAllDevicesTarget(false)}
                  />
                ) : (
                  <Typography variant="body2" sx={{ color: "#94a3b8" }}>
                    Leave this off if you only want to target explicit devices or saved filters.
                  </Typography>
                )}
              </SectionCard>
              <SectionCard title="Filter Targets" subtitle="Use existing Borealis device filters to keep watchdog assignments dynamic.">
                <TextField
                  label="Search Device Filters"
                  value={filterTargetSearch}
                  onChange={(event) => setFilterTargetSearch(event.target.value)}
                  helperText={
                    String(filterTargetSearch || "").trim().length < TARGET_SEARCH_MIN_CHARS
                      ? `Type at least ${TARGET_SEARCH_MIN_CHARS} characters to search filters.`
                      : ""
                  }
                />
                <Box sx={{ ...GRID_WRAPPER_SX, minHeight: 260, height: 260 }}>
                  <AgGridReact
                    rowData={matchingFilterRows}
                    columnDefs={filterSearchColumns}
                    defaultColDef={targetSearchGridDefaultColDef}
                    animateRows
                    suppressCellFocus
                    suppressRowClickSelection
                    overlayNoRowsTemplate={`<span class='ag-overlay-no-rows-center'>${filterSearchOverlay}</span>`}
                    getRowId={(params) => String(params.data?.id || params.rowIndex)}
                    theme={gridTheme}
                  />
                </Box>
                {!selectedFilterTargets.length ? (
                  <Typography variant="body2" sx={{ color: "#94a3b8" }}>
                    No saved filters targeted yet.
                  </Typography>
                ) : (
                  <Stack direction="row" spacing={1} flexWrap="wrap" useFlexGap>
                    {selectedFilterTargets.map((option) => (
                      <Chip
                        key={`${option.id}-${option.name}`}
                        label={option.name || `Filter ${option.id}`}
                        onDelete={() =>
                          updateTopLevel({
                            targets: normalizeArray(formState.targets).filter(
                              (item) => !(item?.kind === "filter" && Number(item?.filter_id) === Number(option.id))
                            ),
                          })
                        }
                      />
                    ))}
                  </Stack>
                )}
              </SectionCard>
              <SectionCard title="Explicit Device Targets" subtitle="Add one-off device targets directly. This is especially useful from the per-device Watchdogs tab.">
                <TextField
                  label="Search Devices by Hostname"
                  value={deviceTargetSearch}
                  onChange={(event) => setDeviceTargetSearch(event.target.value)}
                  helperText={
                    String(deviceTargetSearch || "").trim().length < TARGET_SEARCH_MIN_CHARS
                      ? `Type at least ${TARGET_SEARCH_MIN_CHARS} characters to search devices.`
                      : ""
                  }
                />
                {deviceTargetSearchLoading ? <LinearProgress /> : null}
                <Box sx={{ ...GRID_WRAPPER_SX, minHeight: 260, height: 260 }}>
                  <AgGridReact
                    rowData={deviceTargetMatches}
                    columnDefs={deviceSearchColumns}
                    defaultColDef={targetSearchGridDefaultColDef}
                    animateRows
                    suppressCellFocus
                    suppressRowClickSelection
                    overlayNoRowsTemplate={`<span class='ag-overlay-no-rows-center'>${deviceSearchOverlay}</span>`}
                    getRowId={(params) => String(params.data?.id || params.rowIndex)}
                    theme={gridTheme}
                  />
                </Box>
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
                  onAssemblyChange={(assemblyGuid) => updateAssemblySelection(action.id, assemblyGuid)}
                  onVariableChange={(variableName, value) => updateActionVariable(action.id, variableName, value)}
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
