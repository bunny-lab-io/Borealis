import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Paper,
  Box,
  Typography,
  Button,
  IconButton,
  Stack,
  TextField,
  ToggleButton,
  ToggleButtonGroup,
  Chip,
  Tooltip,
  Autocomplete,
  Tabs,
  Tab,
} from "@mui/material";
import {
  FilterAlt as HeaderIcon,
  Save as SaveIcon,
  Close as CloseIcon,
  Add as AddIcon,
  Remove as RemoveIcon,
  Cached as CachedIcon,
  CheckCircle as CheckCircleIcon,
  HighlightOff as HighlightOffIcon,
  DriveFileRenameOutline as DriveFileRenameOutlineIcon,
  PublicRounded as PublicRoundedIcon,
  TuneRounded as TuneRoundedIcon,
  TableViewRounded as TableViewRoundedIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";

ModuleRegistry.registerModules([AllCommunityModule]);

const AURORA_SHELL = {
  background:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), " +
    "linear-gradient(180deg, #040711 0%, #050816 45%, #050816 100%)",
  text: "#e2e8f0",
  subtext: "#94a3b8",
  border: "rgba(148,163,184,0.35)",
  glass: "rgba(15,23,42,0.72)",
};

const gradientButtonSx = {
  backgroundImage: "linear-gradient(135deg,#7dd3fc,#c084fc)",
  color: "#0b1220",
  borderRadius: 999,
  textTransform: "none",
  boxShadow: "0 10px 26px rgba(124,58,237,0.28)",
  px: 2.4,
  minWidth: 126,
  "&:hover": {
    backgroundImage: "linear-gradient(135deg,#86e1ff,#d1a6ff)",
    boxShadow: "0 12px 34px rgba(124,58,237,0.38)",
  },
};

const DEVICE_FIELDS = [
  { value: "hostname", label: "Hostname" },
  { value: "description", label: "Description" },
  { value: "site", label: "Site" },
  { value: "os", label: "Operating System" },
  { value: "type", label: "Device Type" },
  { value: "status", label: "Status" },
  { value: "agentVersion", label: "Agent Version" },
  { value: "lastUser", label: "Last User" },
  { value: "internalIp", label: "Internal IP" },
  { value: "externalIp", label: "External IP" },
  { value: "lastReboot", label: "Last Reboot" },
  { value: "lastSeen", label: "Last Seen" },
  { value: "domain", label: "Domain" },
  { value: "memory", label: "Memory" },
  { value: "network", label: "Network" },
  { value: "software", label: "Software" },
  { value: "storage", label: "Storage" },
  { value: "cpu", label: "CPU" },
  { value: "agentId", label: "Agent ID" },
  { value: "agentGuid", label: "Agent GUID" },
];

const OPERATORS = [
  { value: "contains", label: "contains" },
  { value: "not_contains", label: "does not contain" },
  { value: "empty", label: "is empty" },
  { value: "not_empty", label: "is not empty" },
  { value: "begins_with", label: "begins with" },
  { value: "not_begins_with", label: "does not begin with" },
  { value: "ends_with", label: "ends with" },
  { value: "not_ends_with", label: "does not end with" },
  { value: "equals", label: "equals" },
  { value: "not_equals", label: "does not equal" },
];

const operatorNeedsValue = (op) => !["empty", "not_empty"].includes(op);
const genId = (prefix) =>
  `${prefix}-${typeof crypto !== "undefined" && crypto.randomUUID ? crypto.randomUUID() : Math.random().toString(36).slice(2, 10)}`;

const buildEmptyCondition = () => ({
  id: genId("condition"),
  field: DEVICE_FIELDS[0].value,
  operator: "contains",
  value: "",
  joinWith: "AND",
});

const buildEmptyGroup = (joinWith = null) => ({
  id: genId("group"),
  joinWith,
  conditions: [buildEmptyCondition()],
});

const statusTokenTheme = {
  Online: { background: "rgba(74,222,128,0.18)", color: "#bbf7d0", icon: CheckCircleIcon },
  Offline: { background: "rgba(239,68,68,0.18)", color: "#fecdd3", icon: HighlightOffIcon },
  default: { background: "rgba(148,163,184,0.2)", color: "#e2e8f0", icon: HighlightOffIcon },
};

const OS_ICON_MAP = {
  windows: "fab fa-windows",
  linux: "fab fa-linux",
  mac: "fab fa-apple",
};

const NAV_TAB_HEIGHT = 32;
const NAV_TAB_COLORS = {
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  icon: "#8fbfff",
  iconActive: "#7db7ff",
  hover: "rgba(255,255,255,0.05)",
  activeBg:
    "linear-gradient(to top, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
};
const TAB_HOVER_GRADIENT = "linear-gradient(120deg, rgba(125,211,252,0.18), rgba(192,132,252,0.22))";

const TABS = [
  { value: "name", label: "Name", icon: DriveFileRenameOutlineIcon },
  { value: "scope", label: "Scope", icon: PublicRoundedIcon },
  { value: "criteria", label: "Criteria", icon: TuneRoundedIcon },
  { value: "results", label: "Results", icon: TableViewRoundedIcon },
];

const TabPanel = ({ value, active, children }) => {
  if (value !== active) return null;
  return (
    <Box
      sx={{
        mt: 2,
        display: "flex",
        flexDirection: "column",
        gap: 2.75,
        flex: 1,
        minHeight: 0,
        overflow: "auto",
      }}
    >
      {children}
    </Box>
  );
};

const GRID_STYLE_BASE = {
  "& .ag-root-wrapper": { borderRadius: 1.5 },
  "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    textAlign: "left",
    padding: "8px 12px 8px 18px",
  },
  "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
    width: "100%",
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    padding: 0,
  },
  "& .ag-center-cols-container .ag-cell .ag-cell-value, & .ag-pinned-left-cols-container .ag-cell .ag-cell-value, & .ag-pinned-right-cols-container .ag-cell .ag-cell-value": {
    width: "100%",
    display: "flex",
    alignItems: "center",
    justifyContent: "flex-start",
    textAlign: "left",
  },
  "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
    paddingLeft: "12px",
    paddingRight: "9px",
  },
};

const PREVIEW_AUTO_SIZE_COLUMNS = ["status", "site", "hostname", "description", "type"];
const SITE_AUTO_SIZE_COLUMNS = ["site"];

const resolveLastEdited = (filter) =>
  filter?.lastEdited || filter?.last_edited || filter?.updated_at || filter?.updated || null;

const resolveLastEditedBy = (filter) => {
  const candidate =
    filter?.last_edited_by_username ||
    filter?.last_edited_by_name ||
    filter?.last_edited_by ||
    filter?.lastEditedBy ||
    filter?.last_editor ||
    filter?.lastEditor ||
    filter?.updated_by ||
    filter?.updatedBy ||
    filter?.owner ||
    filter?.user ||
    filter?.modified_by;
  if (candidate && typeof candidate === "object") {
    if (candidate.name) return candidate.name;
    if (candidate.username) return candidate.username;
    if (candidate.user) return candidate.user;
  }
  if (typeof candidate === "string" && candidate.trim()) return candidate.trim();
  return "Unknown";
};

const formatLastEditedLabel = (ts, user) => {
  if (!ts) return "";
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return "";
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  const year = date.getFullYear();
  let hours = date.getHours();
  const minutes = String(date.getMinutes()).padStart(2, "0");
  const suffix = hours >= 12 ? "PM" : "AM";
  hours = hours % 12 || 12;
  const datePart = `${month}/${day}/${year}`;
  const timePart = `${hours}:${minutes}${suffix}`;
  const editor = user && typeof user === "string" && user.trim() ? user : "Unknown";
  return `Last edited by ${editor} @ ${datePart} @ ${timePart}`;
};

const normalizeSiteValue = (site) => {
  if (site == null) return "";
  if (typeof site === "object") {
    const candidate =
      site.id ??
      site.site_id ??
      site.siteId ??
      site.value ??
      site.site_value ??
      site.name ??
      site.site_name;
    if (candidate != null && candidate !== "") return String(candidate);
  }
  if (typeof site === "string" || typeof site === "number") {
    const value = String(site).trim();
    return value;
  }
  return "";
};

const resolveSiteSelection = (filter) => {
  if (!filter) return [];
  const candidates =
    filter?.sites || filter?.site_list || filter?.site_scope_values || filter?.site_scope_value || filter?.siteScopeValues;
  if (Array.isArray(candidates)) {
    return candidates
      .map((c) => normalizeSiteValue(c))
      .filter((v, idx, arr) => v && arr.indexOf(v) === idx);
  }
  const single =
    filter?.site ||
    filter?.site_id ||
    filter?.siteId ||
    filter?.site_scope_value ||
    filter?.siteScopeValue ||
    filter?.siteName ||
    filter?.site_name ||
    filter?.target_site ||
    (typeof candidates === "string" || typeof candidates === "number" ? candidates : null);
  const normalized = normalizeSiteValue(single);
  return normalized ? [normalized] : [];
};

const resolveSiteScope = (filter) => {
  const raw = filter?.site_scope || filter?.siteScope || filter?.scope || filter?.type;
  const normalized = String(raw || "").toLowerCase();
  if (normalized === "scoped" || normalized === "site") return "scoped";
  const sites = resolveSiteSelection(filter);
  return sites.length ? "scoped" : "global";
};

const normalizeGroupsForUI = (rawGroups) => {
  if (!Array.isArray(rawGroups) || !rawGroups.length) {
    return [buildEmptyGroup()];
  }
  return rawGroups.map((g, gIdx) => {
    const groupId = g.id || genId("group");
    const conditions = Array.isArray(g.conditions) ? g.conditions : [];
    const normalizedConditions = conditions.length
      ? conditions.map((c, cIdx) => ({
          id: c.id || genId("condition"),
          field: c.field || DEVICE_FIELDS[0].value,
          operator: c.operator || "contains",
          value: c.value ?? "",
          joinWith: cIdx === 0 ? null : c.joinWith || "AND",
        }))
      : [buildEmptyCondition()];
    return {
      id: groupId,
      joinWith: gIdx === 0 ? null : g.joinWith || "OR",
      conditions: normalizedConditions,
    };
  });
};

export default function DeviceFilterEditor({ initialFilter, onCancel, onSaved, onPageMetaChange }) {
  const [name, setName] = useState(initialFilter?.name || "");
  const initialScope = resolveSiteScope(initialFilter);
  const initialSelectedSites = resolveSiteSelection(initialFilter);
  const [scope, setScope] = useState(initialScope === "scoped" ? "site" : "global");
  const [selectedSites, setSelectedSites] = useState(initialSelectedSites);
  const [groups, setGroups] = useState(normalizeGroupsForUI(initialFilter?.groups || initialFilter?.raw?.groups));
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState(null);
  const [sites, setSites] = useState([]);
  const [loadingSites, setLoadingSites] = useState(false);
  const [lastEditedTs, setLastEditedTs] = useState(resolveLastEdited(initialFilter));
  const [lastEditedBy, setLastEditedBy] = useState(resolveLastEditedBy(initialFilter));
  const [loadingFilter, setLoadingFilter] = useState(false);
  const [loadError, setLoadError] = useState(null);
  const [previewRows, setPreviewRows] = useState([]);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [previewError, setPreviewError] = useState(null);
  const [previewAppliedAt, setPreviewAppliedAt] = useState(null);
  const [tab, setTab] = useState(TABS[0].value);
  const isEditing = Boolean(initialFilter);
  const previewGridRef = useRef(null);
  const siteGridRef = useRef(null);
  const sendNotification = useCallback(async (message) => {
    if (!message) return;
    try {
      await fetch("/api/notifications/notify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          title: "Device Filters",
          message,
          icon: "filter",
          variant: "info",
        }),
      });
    } catch {
      /* ignore notification transport errors */
    }
  }, []);
  const gridTheme = useMemo(
    () =>
      themeQuartz.withParams({
        accentColor: "#8b5cf6",
        backgroundColor: "#070b1a",
        browserColorScheme: "dark",
        fontFamily: { googleFont: "IBM Plex Sans" },
        foregroundColor: "#f4f7ff",
        headerFontSize: 13,
      }),
    []
  );
  const gridFontFamily = "'IBM Plex Sans','Helvetica Neue',Arial,sans-serif";
  const iconFontFamily = "'Quartz Regular'";
  const pageTitle = isEditing ? "Edit Device Filter" : "Create Device Filter";
  const pageSubtitle =
    "Combine grouped criteria with AND/OR logic to build reusable device scopes for automation and reporting.";

  const applyFilterData = useCallback((filter) => {
    if (!filter) return;
    setName(filter?.name || "");
    const resolvedScope = resolveSiteScope(filter);
    const resolvedSites = resolveSiteSelection(filter);
    setScope(resolvedScope === "scoped" ? "site" : "global");
    setSelectedSites(resolvedSites);
    setGroups(normalizeGroupsForUI(filter?.groups || filter?.raw?.groups));
    setLastEditedTs(resolveLastEdited(filter));
    setLastEditedBy(resolveLastEditedBy(filter));
  }, []);

  useEffect(() => {
    applyFilterData(initialFilter);
  }, [applyFilterData, initialFilter]);

  useEffect(() => {
    onPageMetaChange?.({
      page_title: pageTitle,
      page_subtitle: pageSubtitle,
      page_icon: HeaderIcon,
    });
    return () => onPageMetaChange?.(null);
  }, [onPageMetaChange, pageSubtitle, pageTitle]);

  const handlePreviewGridReady = useCallback((params) => {
    previewGridRef.current = params.api;
    requestAnimationFrame(() => {
      try {
        params.api.autoSizeColumns(PREVIEW_AUTO_SIZE_COLUMNS, true);
      } catch {}
    });
  }, []);

  const autoSizePreviewGrid = useCallback(() => {
    if (!previewGridRef.current || !previewRows.length) return;
    requestAnimationFrame(() => {
      try {
        previewGridRef.current.autoSizeColumns(PREVIEW_AUTO_SIZE_COLUMNS, true);
      } catch {}
    });
  }, [previewRows.length]);

  useEffect(() => {
    autoSizePreviewGrid();
  }, [previewRows, autoSizePreviewGrid]);

  const getDeviceField = (device, field) => {
    const summary = device && typeof device.summary === "object" ? device.summary : {};
    switch (field) {
      case "status":
        return device.status || summary.status || "";
      case "site":
        return device.site || device.site_name || summary.site || "";
      case "hostname":
        return device.hostname || summary.hostname || "";
      case "description":
        return device.description || summary.description || "";
      case "os":
        return device.os || summary.os || summary.operating_system || "";
      case "type":
        return device.type || summary.type || summary.device_type || device.device_type || "";
      default:
        return device[field] || summary[field] || "";
    }
  };

  const evaluateCondition = (device, condition) => {
    const operator = (condition.operator || "contains").toLowerCase();
    const value = String(condition.value ?? "").trim();
    const fieldValueRaw = getDeviceField(device, condition.field);
    const fieldValue = fieldValueRaw == null ? "" : String(fieldValueRaw);
    const lcField = fieldValue.toLowerCase();
    const lcValue = value.toLowerCase();

    switch (operator) {
      case "contains":
        return lcField.includes(lcValue);
      case "not_contains":
        return !lcField.includes(lcValue);
      case "empty":
        return lcField.length === 0;
      case "not_empty":
        return lcField.length > 0;
      case "begins_with":
        return lcField.startsWith(lcValue);
      case "not_begins_with":
        return !lcField.startsWith(lcValue);
      case "ends_with":
        return lcField.endsWith(lcValue);
      case "not_ends_with":
        return !lcField.endsWith(lcValue);
      case "equals":
        return lcField === lcValue;
      case "not_equals":
        return lcField !== lcValue;
      default:
        return false;
    }
  };

  const evaluateGroup = (device, group) => {
    const conditions = group?.conditions || [];
    if (!conditions.length) return true;
    let result = evaluateCondition(device, conditions[0]);
    for (let i = 1; i < conditions.length; i++) {
      const cond = conditions[i];
      const joiner = (cond.joinWith || "AND").toUpperCase();
      const res = evaluateCondition(device, cond);
      result = joiner === "OR" ? result || res : result && res;
    }
    return result;
  };

  const evaluateCriteria = useCallback(
    (device) => {
      if (!groups.length) return true;
      let result = evaluateGroup(device, groups[0]);
      for (let i = 1; i < groups.length; i++) {
        const group = groups[i];
        const joiner = (group.joinWith || "OR").toUpperCase();
        const res = evaluateGroup(device, group);
        result = joiner === "AND" ? result && res : result || res;
      }
      return result;
    },
    [groups]
  );

  const siteRows = useMemo(
    () =>
      sites.map((s, idx) => {
        const value = String(s.value ?? s.id ?? idx);
        const label = s.label || `Site ${idx + 1}`;
        return {
          id: s.id ? String(s.id) : value,
          value,
          label,
          labelLower: String(label).toLowerCase(),
          valueLower: value.toLowerCase(),
        };
      }),
    [sites]
  );

  const selectedSiteMatchers = useMemo(() => {
    const tokens = [];
    selectedSites.forEach((id) => {
      const value = String(id || "").trim();
      if (value) tokens.push(value.toLowerCase());
      const match = sites.find((s) => String(s.value) === String(id) || String(s.id) === String(id));
      if (match?.label) tokens.push(String(match.label).toLowerCase());
      if (match?.value) tokens.push(String(match.value).toLowerCase());
    });
    return Array.from(new Set(tokens.filter(Boolean)));
  }, [selectedSites, sites]);

  const selectedSiteLabels = useMemo(
    () =>
      selectedSites
        .map((id) => {
          const match = sites.find((s) => String(s.value) === String(id) || String(s.id) === String(id));
          const label = match?.label || match?.name;
          const fallback = String(id || "").trim();
          return label || fallback;
        })
        .filter((v, idx, arr) => v && arr.indexOf(v) === idx),
    [selectedSites, sites]
  );

  const syncSiteGridSelection = useCallback(
    (apiOverride = null) => {
      const api = apiOverride || siteGridRef.current;
      if (!api) return;
      const selectedSet = new Set(selectedSites.map((s) => String(s)));
      api.forEachNode((node) => {
        const nodeId = String(node?.data?.id ?? node?.data?.value ?? "");
        const shouldSelect = selectedSet.has(nodeId);
        if (node.isSelected() !== shouldSelect) {
          node.setSelected(shouldSelect);
        }
      });
    },
    [selectedSites]
  );

  const autoSizeSiteGrid = useCallback(() => {
    if (!siteGridRef.current || !siteRows.length) return;
    requestAnimationFrame(() => {
      try {
        siteGridRef.current.autoSizeColumns(SITE_AUTO_SIZE_COLUMNS, true);
      } catch {}
    });
  }, [siteRows.length]);

  const handleSiteGridReady = useCallback(
    (params) => {
      siteGridRef.current = params.api;
      autoSizeSiteGrid();
      syncSiteGridSelection(params.api);
    },
    [autoSizeSiteGrid, syncSiteGridSelection]
  );

  useEffect(() => {
    autoSizeSiteGrid();
    syncSiteGridSelection();
  }, [autoSizeSiteGrid, siteRows, syncSiteGridSelection]);

  useEffect(() => {
    const api = siteGridRef.current;
    if (!api) return;
    if (loadingSites) {
      api.showLoadingOverlay();
    } else if (!siteRows.length) {
      api.showNoRowsOverlay();
    } else {
      api.hideOverlay();
    }
  }, [loadingSites, siteRows]);

  const handleSiteSelectionChanged = useCallback(() => {
    const api = siteGridRef.current;
    if (!api) return;
    const selected = api
      .getSelectedNodes()
      .map((node) => String(node?.data?.id ?? node?.data?.value ?? ""))
      .filter(Boolean);
    setSelectedSites(selected);
  }, []);

  const applyCriteria = useCallback(async () => {
    setPreviewLoading(true);
    setPreviewError(null);
    if (scope === "site" && !selectedSiteMatchers.length) {
      setPreviewRows([]);
      setPreviewAppliedAt(null);
      setPreviewLoading(false);
      setPreviewError("Select at least one site to preview scoped results.");
      return;
    }
    try {
      const resp = await fetch("/api/devices");
      if (!resp.ok) {
        throw new Error(`Failed to load devices (${resp.status})`);
      }
      const payload = await resp.json();
      const list = Array.isArray(payload?.devices) ? payload.devices : [];
      const siteMatchSet = new Set(selectedSiteMatchers);
      const scopedMode = scope === "site";
      const filtered = list.filter((d) => {
        if (!evaluateCriteria(d)) return false;
        if (!scopedMode) return true;
        if (!siteMatchSet.size) return false;
        const deviceSite = getDeviceField(d, "site");
        const normalizedSite = String(deviceSite || "").toLowerCase();
        return siteMatchSet.has(normalizedSite);
      });
      const rows = filtered.map((d, idx) => ({
        id: d.agent_guid || d.agent_id || d.hostname || `device-${idx}`,
        status: getDeviceField(d, "status"),
        site: getDeviceField(d, "site"),
        hostname: getDeviceField(d, "hostname"),
        description: getDeviceField(d, "description"),
        type: getDeviceField(d, "type"),
        os: getDeviceField(d, "os"),
        raw: d,
      }));
      setPreviewRows(rows);
      setPreviewAppliedAt(new Date());
    } catch (err) {
      setPreviewError(err?.message || "Unable to apply criteria");
      setPreviewRows([]);
    } finally {
      setPreviewLoading(false);
      autoSizePreviewGrid();
    }
  }, [autoSizePreviewGrid, evaluateCriteria, scope, selectedSiteMatchers]);

  const applyCriteriaRef = useRef(applyCriteria);
  useEffect(() => {
    applyCriteriaRef.current = applyCriteria;
  }, [applyCriteria]);

  useEffect(() => {
    if (tab === "results") {
      applyCriteriaRef.current?.();
    }
  }, [tab]);

  const previewColumns = useMemo(
    () => [
      {
        field: "status",
        headerName: "Status",
        minWidth: 100,
        cellRenderer: (params) => {
          const status = params.value || "";
          const theme = statusTokenTheme[status] || statusTokenTheme.default;
          const Icon = theme.icon || CheckCircleIcon;
          return (
            <Box
              component="span"
              sx={{
                display: "inline-flex",
                alignItems: "center",
                gap: 0.75,
                color: theme.color,
                fontWeight: 700,
                fontSize: "0.95rem",
              }}
            >
              <Icon sx={{ fontSize: 18 }} />
              {status || "Unknown"}
            </Box>
          );
        },
        cellClass: "auto-col-tight",
      },
      { field: "site", headerName: "Site", minWidth: 180, cellClass: "auto-col-tight" },
      {
        field: "hostname",
        headerName: "Hostname",
        minWidth: 180,
        cellClass: "auto-col-tight",
        cellRenderer: (params) => {
          const name = params.value || "";
          const raw = params.data?.raw;
          const device = raw || {};
          const deviceId =
            device?.agent_guid ||
            device?.agent_id ||
            device?.id ||
            device?.hostname ||
            device?.summary?.agent_guid ||
            device?.summary?.hostname;
          const href = deviceId ? `/device/${encodeURIComponent(deviceId)}` : "#";
          return (
            <a
              href={href}
              onClick={(e) => {
                if (href === "#") return;
                e.preventDefault();
                window.history.pushState({}, "", href);
                window.dispatchEvent(new PopStateEvent("popstate"));
              }}
              style={{ color: "#7dd3fc", textDecoration: "none", fontWeight: 600 }}
            >
              {name}
            </a>
          );
        },
      },
      { field: "description", headerName: "Description", minWidth: 260, cellClass: "auto-col-tight" },
      { field: "type", headerName: "Device Type", minWidth: 160, cellClass: "auto-col-tight" },
      {
        field: "os",
        headerName: "OS",
        minWidth: 200,
        flex: 1,
        cellClass: "auto-col-tight",
        cellRenderer: (params) => {
          const value = params.value || "";
          const key = String(value || "").toLowerCase();
          const iconClass =
            OS_ICON_MAP[key] ||
            (key.includes("mac") || key.includes("os x") ? OS_ICON_MAP.mac : key.includes("win") ? OS_ICON_MAP.windows : key.includes("linux") ? OS_ICON_MAP.linux : null);
          return (
            <Box sx={{ display: "inline-flex", alignItems: "center", gap: 0.75 }}>
              {iconClass ? <i className={iconClass} style={{ color: "#a5e0ff" }} /> : null}
              <span>{value || "Unknown"}</span>
            </Box>
          );
        },
      },
    ],
    []
  );

  const defaultPreviewColDef = useMemo(
    () => ({
      sortable: true,
      filter: "agTextColumnFilter",
      resizable: true,
      cellClass: "auto-col-tight",
      suppressMenu: true,
    }),
    []
  );

  const siteColumnDefs = useMemo(
    () => [
      {
        headerName: "",
        field: "select",
        width: 52,
        maxWidth: 52,
        minWidth: 52,
        checkboxSelection: true,
        headerCheckboxSelection: true,
        pinned: "left",
        suppressMenu: true,
        suppressHeaderMenuButton: true,
        menuTabs: [],
        filter: false,
        sortable: false,
        resizable: false,
        lockPosition: true,
        cellClass: "auto-col-tight",
      },
      {
        headerName: "Site",
        field: "label",
        flex: 1,
        minWidth: 220,
        cellClass: "auto-col-tight",
      },
    ],
    []
  );

  const siteDefaultColDef = useMemo(
    () => ({
      sortable: true,
      filter: "agTextColumnFilter",
      resizable: true,
      cellClass: "auto-col-tight",
      suppressMenu: true,
    }),
    []
  );

  useEffect(() => {
    if (!initialFilter?.id) return;
    const missingGroups = !initialFilter.groups || initialFilter.groups.length === 0;
    if (!missingGroups) return;
    let canceled = false;
    setLoadingFilter(true);
    setLoadError(null);
    fetch(`/api/device_filters/${encodeURIComponent(initialFilter.id)}`)
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`Failed to load filter (${r.status})`))))
      .then((data) => {
        if (canceled) return;
        if (data?.filter) {
          applyFilterData(data.filter);
        } else if (data) {
          applyFilterData(data);
        }
      })
      .catch((err) => {
        if (canceled) return;
        setLoadError(err?.message || "Unable to load filter");
      })
      .finally(() => {
        if (!canceled) setLoadingFilter(false);
      });
    return () => {
      canceled = true;
    };
  }, [applyFilterData, initialFilter]);

  const loadSites = useCallback(async () => {
    setLoadingSites(true);
    try {
      const resp = await fetch("/api/sites");
      const json = await resp.json().catch(() => []);
      const siteList = Array.isArray(json?.sites) ? json.sites : Array.isArray(json) ? json : [];
      const normalized = siteList.map((s, idx) => {
        const normalizedValue = normalizeSiteValue(s);
        const value = normalizedValue || String(idx);
        const label = s.name || s.site_name || s.label || `Site ${idx + 1}`;
        return {
          label,
          value,
          id: value,
          labelLower: String(label).toLowerCase(),
          valueLower: String(value).toLowerCase(),
        };
      });
      setSites(normalized);
    } catch {
      setSites([]);
    } finally {
      setLoadingSites(false);
    }
  }, []);

  useEffect(() => {
    loadSites();
  }, [loadSites]);

  const updateGroup = useCallback((groupId, updater) => {
    setGroups((prev) =>
      prev.map((g) => {
        if (g.id !== groupId) return g;
        const next = typeof updater === "function" ? updater(g) : updater;
        return { ...next };
      })
    );
  }, []);

  const updateCondition = useCallback((groupId, conditionId, updater) => {
    updateGroup(groupId, (group) => ({
      ...group,
      conditions: group.conditions.map((c, idx) => {
        if (c.id !== conditionId) return c;
        const updated = typeof updater === "function" ? updater(c, idx) : updater;
        return { ...updated };
      }),
    }));
  }, [updateGroup]);

  const addCondition = useCallback((groupId) => {
    updateGroup(groupId, (group) => ({
      ...group,
      conditions: [
        ...group.conditions,
        { ...buildEmptyCondition(), joinWith: group.conditions.length === 0 ? null : "AND" },
      ],
    }));
  }, [updateGroup]);

  const removeCondition = useCallback((groupId, conditionId) => {
    updateGroup(groupId, (group) => {
      const filtered = group.conditions.filter((c) => c.id !== conditionId);
      return { ...group, conditions: filtered.length ? filtered : [buildEmptyCondition()] };
    });
  }, [updateGroup]);

  const addGroup = useCallback((joinWith = "OR") => {
    setGroups((prev) => [...prev, buildEmptyGroup(prev.length === 0 ? null : joinWith)]);
  }, []);

  const removeGroup = useCallback((groupId) => {
    setGroups((prev) => {
      const filtered = prev.filter((g) => g.id !== groupId);
      if (!filtered.length) return [buildEmptyGroup()];
      const next = filtered.map((g, idx) => ({ ...g, joinWith: idx === 0 ? null : g.joinWith || "OR" }));
      return next;
    });
  }, []);

  const handleSave = useCallback(async () => {
    setSaving(true);
    setSaveError(null);
    const siteScope = scope === "site" ? "scoped" : "global";
    const scopedSites =
      siteScope === "scoped"
        ? Array.from(new Set(selectedSites.map((s) => String(s || "").trim()).filter(Boolean)))
        : [];
    const primarySite = siteScope === "scoped" ? scopedSites[0] || null : null;
    if (siteScope === "scoped" && !scopedSites.length) {
      setSaveError("Select at least one site when scoping a filter to sites.");
      setSaving(false);
      return;
    }
    const payload = {
      id: initialFilter?.id || initialFilter?.filter_id,
      name: name.trim() || "Unnamed Filter",
      site_scope: siteScope,
      site_scope_values: scopedSites,
      sites: scopedSites,
      site_ids: scopedSites,
      site_names: siteScope === "scoped" ? selectedSiteLabels : [],
      site_scope_value: primarySite,
      scope: siteScope,
      type: siteScope,
      site: primarySite,
      groups: groups.map((g, gIdx) => ({
        join_with: gIdx === 0 ? null : g.joinWith || "OR",
        conditions: (g.conditions || []).map((c, cIdx) => ({
          join_with: cIdx === 0 ? null : c.joinWith || "AND",
          field: c.field,
          operator: c.operator,
          value: operatorNeedsValue(c.operator) ? c.value : "",
        })),
      })),
    };

    try {
      const method = payload.id ? "PUT" : "POST";
      const url = payload.id ? `/api/device_filters/${encodeURIComponent(payload.id)}` : "/api/device_filters";
      const resp = await fetch(url, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      if (!resp.ok) {
        throw new Error(`Failed to save filter (${resp.status})`);
      }
      const json = await resp.json().catch(() => ({}));
      const saved = json?.filter || json || payload;
      onSaved?.(saved);
      if (method === "POST") {
        const createdName = saved?.name || payload.name || "Filter";
        sendNotification(`File ${createdName} Created Successfully`);
      }
    } catch (err) {
      setSaveError(err?.message || "Unable to save filter");
    } finally {
      setSaving(false);
    }
  }, [groups, initialFilter, name, onSaved, scope, selectedSiteLabels, selectedSites, sendNotification]);

  const renderConditionRow = (groupId, condition, isFirst) => {
    const label = DEVICE_FIELDS.find((f) => f.value === condition.field)?.label || condition.field;
    const needsValue = operatorNeedsValue(condition.operator);
    return (
      <Box
        key={condition.id}
        sx={{
          display: "grid",
          gridTemplateColumns: "94px 220px 220px 1fr auto",
          gap: 0.5,
          alignItems: "center",
          background: "rgba(12,18,35,0.7)",
          border: `1px solid ${AURORA_SHELL.border}`,
          borderRadius: 2,
          px: 1.25,
          py: 1,
        }}
      >
        <Box sx={{ display: "flex", alignItems: "center", gap: 0.5 }}>
          {!isFirst && (
            <ToggleButtonGroup
              exclusive
              size="small"
              value={condition.joinWith || "AND"}
              onChange={(_, val) => {
                if (!val) return;
                updateCondition(groupId, condition.id, (c) => ({ ...c, joinWith: val }));
              }}
              color="info"
              sx={{
                "& .MuiToggleButton-root": {
                  px: 1.5,
                  textTransform: "uppercase",
                  fontSize: "0.7rem",
                },
              }}
            >
              <ToggleButton value="AND">AND</ToggleButton>
              <ToggleButton value="OR">OR</ToggleButton>
            </ToggleButtonGroup>
          )}
        </Box>

        <Autocomplete
          disablePortal
          options={DEVICE_FIELDS}
          value={DEVICE_FIELDS.find((f) => f.value === condition.field) || DEVICE_FIELDS[0]}
          getOptionLabel={(option) => option?.label || ""}
          isOptionEqualToValue={(option, value) => option?.value === value?.value}
          onChange={(_, val) =>
            updateCondition(groupId, condition.id, (c) => ({ ...c, field: val?.value || DEVICE_FIELDS[0].value }))
          }
          renderInput={(params) => (
            <TextField
              {...params}
              label="Field"
              size="small"
              sx={{
                "& .MuiInputBase-root": { backgroundColor: "rgba(4,7,17,0.65)" },
                "& .MuiOutlinedInput-notchedOutline": { borderColor: AURORA_SHELL.border },
              }}
            />
          )}
        />

        <TextField
          select
          size="small"
          label="Operator"
          value={condition.operator}
          onChange={(e) =>
            updateCondition(groupId, condition.id, (c) => ({ ...c, operator: e.target.value }))
          }
          SelectProps={{ native: true }}
          sx={{
            "& .MuiInputBase-root": { backgroundColor: "rgba(4,7,17,0.65)" },
            "& .MuiOutlinedInput-notchedOutline": { borderColor: AURORA_SHELL.border },
          }}
        >
          {OPERATORS.map((op) => (
            <option key={op.value} value={op.value}>
              {op.label}
            </option>
          ))}
        </TextField>

        <TextField
          size="small"
          label={`Value${needsValue ? "" : " (ignored)"}`}
          value={condition.value}
          onChange={(e) =>
            updateCondition(groupId, condition.id, (c) => ({ ...c, value: e.target.value }))
          }
          disabled={!needsValue}
          placeholder={needsValue ? `Enter value for ${label}` : "Not needed for this operator"}
          sx={{
            "& .MuiInputBase-root": { backgroundColor: "rgba(4,7,17,0.65)" },
            "& .MuiOutlinedInput-notchedOutline": { borderColor: AURORA_SHELL.border },
          }}
        />

        <Stack direction="row" spacing={0.5} justifyContent="flex-end">
          <Tooltip title="Add condition">
            <IconButton size="small" onClick={() => addCondition(groupId)} sx={{ color: "#7dd3fc" }}>
              <AddIcon fontSize="inherit" />
            </IconButton>
          </Tooltip>
          <Tooltip title="Remove condition">
            <IconButton
              size="small"
              onClick={() => removeCondition(groupId, condition.id)}
              sx={{ color: "#ffb4b4" }}
            >
              <RemoveIcon fontSize="inherit" />
            </IconButton>
          </Tooltip>
        </Stack>
      </Box>
    );
  };

  return (
    <Paper
      elevation={0}
      sx={{
        height: "100%",
        minHeight: 0,
        flex: 1,
        position: "relative",
        backgroundColor: "transparent",
        color: AURORA_SHELL.text,
        p: 3,
        borderRadius: 0,
        display: "flex",
        flexDirection: "column",
        gap: 3,
        pb: 3,
        overflow: "hidden",
      }}
    >
      {loadingFilter ? (
        <Box sx={{ mb: 2, color: "#7dd3fc" }}>Loading filter...</Box>
      ) : null}
      {loadError ? (
        <Box
          sx={{
            mb: 2,
            background: "rgba(255,179,179,0.08)",
            color: "#ffb4b4",
            border: "1px solid rgba(255,179,179,0.35)",
            borderRadius: 1.5,
            p: 1.5,
          }}
        >
          {loadError}
        </Box>
      ) : null}

      <Box
        sx={{
          position: "fixed",
          top: { xs: 72, md: 88 }, // align with page title padding beneath the menu bar
          right: { xs: 12, md: 24 },
          display: "flex",
          justifyContent: "flex-end",
          zIndex: 1400,
          pointerEvents: "none",
        }}
      >
        <Stack direction="row" spacing={1.25} sx={{ pointerEvents: "auto" }}>
          <Tooltip title="Cancel and return">
            <Button
              variant="outlined"
              startIcon={<CloseIcon />}
              onClick={() => onCancel?.()}
              sx={{
                textTransform: "none",
                borderColor: AURORA_SHELL.border,
                color: AURORA_SHELL.text,
                borderRadius: 999,
              }}
            >
              Cancel
            </Button>
          </Tooltip>
          <Tooltip title="Save filter">
            <Button
              variant="contained"
              startIcon={saving ? <CachedIcon /> : <SaveIcon />}
              onClick={handleSave}
              disabled={saving}
              sx={gradientButtonSx}
            >
              {saving ? "Saving..." : "Save Filter"}
            </Button>
          </Tooltip>
        </Stack>
      </Box>

      <Box
        sx={{
          display: "flex",
          flexDirection: "column",
          gap: 2,
          flex: 1,
          minHeight: 0,
          position: "relative",
        }}
      >
        <Tabs
          value={tab}
          onChange={(_, val) => setTab(val)}
          variant="scrollable"
          scrollButtons="auto"
          TabIndicatorProps={{
            style: {
              height: 3,
              borderRadius: 3,
              background: NAV_TAB_COLORS.iconActive,
            },
          }}
          sx={{
            mt: 0,
            borderBottom: `1px solid ${AURORA_SHELL.border}`,
            minHeight: NAV_TAB_HEIGHT,
            height: NAV_TAB_HEIGHT,
            "& .MuiTabs-flexContainer": {
              minHeight: NAV_TAB_HEIGHT,
              height: NAV_TAB_HEIGHT,
              alignItems: "stretch",
            },
            "& .MuiTab-root": {
              color: NAV_TAB_COLORS.text,
              fontFamily: "inherit",
              fontSize: "0.8rem",
              textTransform: "none",
              fontWeight: 400,
              minHeight: NAV_TAB_HEIGHT,
              height: NAV_TAB_HEIGHT,
              opacity: 1,
              borderRadius: 1,
              py: 0.35,
              transition:
                "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
              "& .MuiTab-iconWrapper": {
                color: NAV_TAB_COLORS.icon,
              },
              "&:hover": {
                background: NAV_TAB_COLORS.hover,
              },
              "&:active": {
                transform: "translateY(0.5px)",
              },
            },
            "& .MuiTab-root.Mui-selected": {
              color: NAV_TAB_COLORS.textActive,
              fontWeight: 600,
              background: NAV_TAB_COLORS.activeBg,
              "& .MuiTab-iconWrapper": {
                color: NAV_TAB_COLORS.iconActive,
              },
              "&:hover": {
                background: NAV_TAB_COLORS.activeBg,
              },
            },
          }}
        >
          {TABS.map((tabDef) => (
            <Tab
              key={tabDef.value}
              label={tabDef.label}
              value={tabDef.value}
              icon={tabDef.icon ? <tabDef.icon sx={{ fontSize: 18 }} /> : undefined}
              iconPosition="start"
            />
          ))}
        </Tabs>

        <TabPanel value="name" active={tab}>
          <Box sx={{ display: "flex", flexDirection: "column", gap: 1 }}>
            <Typography sx={{ fontWeight: 700 }}>Name</Typography>
            <TextField
              size="small"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Filter name or convention (e.g., RMM targeting)"
              sx={{
                width: { xs: "100%", md: "65%" },
                maxWidth: 546,
                "& .MuiInputBase-root": { backgroundColor: "rgba(4,7,17,0.65)" },
                "& .MuiOutlinedInput-notchedOutline": { borderColor: AURORA_SHELL.border },
              }}
            />
          </Box>
        </TabPanel>

        <TabPanel value="scope" active={tab}>
          <Box sx={{ display: "flex", flexDirection: "column", gap: 1.25 }}>
            <Typography sx={{ fontWeight: 700 }}>Scope</Typography>
            <Typography sx={{ color: AURORA_SHELL.subtext, fontSize: "0.95rem" }}>
              Choose whether this filter is global or pinned to a specific site.
            </Typography>
            <ToggleButtonGroup
              exclusive
              value={scope}
              onChange={(_, val) => {
                if (!val) return;
                setScope(val);
              }}
              color="info"
              sx={{
                alignSelf: "flex-start",
                background: "rgba(7,12,26,0.7)",
                borderRadius: 2,
                "& .MuiToggleButton-root": {
                  textTransform: "none",
                  color: AURORA_SHELL.text,
                  borderColor: "rgba(148,163,184,0.35)",
                  minHeight: 32,
                  paddingTop: 0.25,
                  paddingBottom: 0.25,
                  paddingLeft: 1.6,
                  paddingRight: 1.6,
                  fontWeight: 700,
                },
                "& .Mui-selected": {
                  background: TAB_HOVER_GRADIENT,
                  color: "#0b1220",
                  boxShadow: "0 0 0 1px rgba(148,163,184,0.35) inset",
                },
              }}
            >
              <ToggleButton value="global">Global</ToggleButton>
              <ToggleButton value="site">Site(s)</ToggleButton>
            </ToggleButtonGroup>
<br></br>
            {scope === "site" && (
              <Box sx={{ display: "flex", flexDirection: "column", gap: 1.25, flex: 1, minHeight: 0, pb: 2 }}>
                <Typography sx={{ fontWeight: 600 }}>Select Sites</Typography>
                <Typography sx={{ color: AURORA_SHELL.subtext, fontSize: "0.95rem" }}>
                  Use the table below to choose which site(s) you want to search for devices within.
                </Typography>
                <Box
                  className={gridTheme.themeName}
                  sx={{
                    ...GRID_STYLE_BASE,
                    flex: 1,
                    minHeight: 420,
                    height: "calc(100vh - 360px)",
                    maxHeight: "calc(100vh - 240px)",
                    width: "100%",
                    maxWidth: 760,
                    "& .ag-root-wrapper": { borderRadius: "8px", overflow: "hidden" },
                  }}
                  style={{
                    "--ag-icon-font-family": iconFontFamily,
                    "--ag-background-color": "#070b1a",
                    "--ag-foreground-color": "#f4f7ff",
                    "--ag-header-background-color": "#0f172a",
                    "--ag-header-foreground-color": "#cfe0ff",
                    "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
                    "--ag-row-hover-color": "rgba(125,183,255,0.08)",
                    "--ag-selected-row-background-color": "rgba(64,164,255,0.18)",
                    "--ag-border-color": "rgba(125,183,255,0.18)",
                    "--ag-row-border-color": "rgba(125,183,255,0.14)",
                    "--ag-border-radius": "8px",
                    "--ag-checkbox-border-radius": "3px",
                    "--ag-checkbox-background-color": "rgba(255,255,255,0.06)",
                    "--ag-checkbox-border-color": "rgba(180,200,220,0.6)",
                    "--ag-checkbox-checked-color": "#7dd3fc",
                    "--ag-cell-horizontal-padding": "18px",
                  }}
                >
                  <AgGridReact
                    rowData={siteRows}
                    columnDefs={siteColumnDefs}
                    defaultColDef={siteDefaultColDef}
                    rowSelection="multiple"
                    rowMultiSelectWithClick
                    suppressCellFocus
                    animateRows
                    pagination
                    paginationPageSize={20}
                    paginationPageSizeSelector={[20, 50, 100]}
                    rowHeight={46}
                    headerHeight={44}
                    getRowId={(params) => params?.data?.id}
                    onGridReady={handleSiteGridReady}
                    onSelectionChanged={handleSiteSelectionChanged}
                    overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>No sites available.</span>"
                    theme={gridTheme}
                    style={{ width: "100%", height: "100%", minHeight: "100%", fontFamily: gridFontFamily }}
                  />
                </Box>
              </Box>
            )}
          </Box>
        </TabPanel>

        <TabPanel value="criteria" active={tab}>
          <Box sx={{ display: "flex", flexDirection: "column", gap: 2.75 }}>
            <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
              <Typography sx={{ fontWeight: 700 }}>Criteria</Typography>
              <Chip label="Grouped AND / OR" size="small" sx={{ backgroundColor: "rgba(125,211,252,0.12)", color: "#7dd3fc" }} />
            </Box>
            <Typography sx={{ color: AURORA_SHELL.subtext, fontSize: "0.95rem", mb: 1 }}>
              Add conditions inside each group, mixing AND/OR as needed. Groups themselves can be chained with AND or OR to
              mirror complex targeting logic (e.g., (A AND B) OR (C AND D)).
            </Typography>

            {groups.map((group, idx) => (
              <Box key={group.id} sx={{ display: "flex", flexDirection: "column", gap: 1.5 }}>
                {idx > 0 && (
                  <ToggleButtonGroup
                    exclusive
                    size="small"
                    value={group.joinWith || "OR"}
                    onChange={(_, val) => {
                      if (!val) return;
                      updateGroup(group.id, { ...group, joinWith: val });
                    }}
                    color="info"
                    sx={{
                alignSelf: "flex-start",
                "& .MuiToggleButton-root": { px: 2, textTransform: "uppercase", fontSize: "0.8rem" },
              }}
            >
              <ToggleButton value="AND">AND</ToggleButton>
              <ToggleButton value="OR">OR</ToggleButton>
                  </ToggleButtonGroup>
                )}

                <Box
                  sx={{
                    border: `1px solid ${AURORA_SHELL.border}`,
                    borderRadius: 2,
                    background: "linear-gradient(135deg, rgba(7,10,22,0.85), rgba(9,11,24,0.92))",
                    p: 1.5,
                    boxShadow: "0 12px 28px rgba(3,7,18,0.5)",
                    display: "flex",
                    flexDirection: "column",
                    gap: 1,
                  }}
                >
                  <Stack direction="row" alignItems="center" justifyContent="space-between" sx={{ pr: 0.5 }}>
                    <Typography sx={{ fontWeight: 600 }}>Criteria Group {idx + 1}</Typography>
                    <Stack direction="row" spacing={1}>
                      <Button
                        size="small"
                        variant="outlined"
                        startIcon={<AddIcon />}
                        onClick={() => addCondition(group.id)}
                        sx={{
                          textTransform: "none",
                          color: "#7dd3fc",
                          borderColor: "rgba(125,211,252,0.5)",
                          borderRadius: 1.5,
                        }}
                      >
                        Add Condition
                      </Button>
                      <Button
                        size="small"
                        variant="outlined"
                        startIcon={<RemoveIcon />}
                        disabled={groups.length === 1}
                        onClick={() => removeGroup(group.id)}
                        sx={{
                          textTransform: "none",
                          color: "#ffb4b4",
                          borderColor: "rgba(255,180,180,0.5)",
                          borderRadius: 1.5,
                        }}
                      >
                        Remove Group
                      </Button>
                    </Stack>
                  </Stack>

                  <Stack spacing={1}>
                    {group.conditions.map((condition, cIdx) =>
                      renderConditionRow(group.id, condition, cIdx === 0)
                    )}
                  </Stack>
                </Box>
              </Box>
            ))}

            <Button
              startIcon={<AddIcon />}
              variant="outlined"
              onClick={() => addGroup("OR")}
              sx={{
                textTransform: "none",
                alignSelf: "flex-start",
                color: "#a5e0ff",
                borderColor: "rgba(125,183,255,0.5)",
                borderRadius: 1.5,
              }}
            >
              Add Group
            </Button>
          </Box>
        </TabPanel>

        <TabPanel value="results" active={tab}>
          <Box
            sx={{
              display: "flex",
              flexDirection: "column",
              gap: 1.5,
              flex: 1,
              minHeight: 0,
              overflow: "hidden",
            }}
          >
            <Stack direction="row" alignItems="center" justifyContent="space-between" sx={{ flexShrink: 0 }}>
              <Box>
                <Typography sx={{ fontWeight: 700 }}>Results</Typography>
                <Typography sx={{ color: AURORA_SHELL.subtext, fontSize: "0.95rem" }}>
                  Opening this tab auto-applies the current criteria to preview matching devices.
                </Typography>
                {previewAppliedAt && (
                  <Typography sx={{ color: AURORA_SHELL.subtext, fontSize: "0.85rem" }}>
                    Last applied: {previewAppliedAt.toLocaleString()}
                  </Typography>
                )}
                {previewError ? (
                  <Typography sx={{ color: "#ffb4b4", fontSize: "0.9rem", mt: 0.5 }}>{previewError}</Typography>
                ) : null}
              </Box>
            </Stack>

            <Box
              sx={{
                flex: 1,
                minHeight: 0,
                display: "flex",
                flexDirection: "column",
                overflow: "auto",
              }}
            >
              <Box
              className={gridTheme.themeName}
              sx={{
                ...GRID_STYLE_BASE,
                flex: 1,
                minHeight: 0,
                height: "100%",
                overflow: "hidden",
              }}
              style={{
                "--ag-icon-font-family": iconFontFamily,
                "--ag-background-color": "#070b1a",
                "--ag-foreground-color": "#f4f7ff",
                  "--ag-header-background-color": "#0f172a",
                  "--ag-header-foreground-color": "#cfe0ff",
                  "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
                  "--ag-row-hover-color": "rgba(125,183,255,0.08)",
                  "--ag-selected-row-background-color": "rgba(64,164,255,0.18)",
                  "--ag-border-color": "rgba(125,183,255,0.18)",
                  "--ag-row-border-color": "rgba(125,183,255,0.14)",
                  "--ag-border-radius": "8px",
                  "--ag-checkbox-border-radius": "3px",
                "--ag-checkbox-background-color": "rgba(255,255,255,0.06)",
                "--ag-checkbox-border-color": "rgba(180,200,220,0.6)",
                "--ag-checkbox-checked-color": "#7dd3fc",
                "--ag-cell-horizontal-padding": "18px",
              }}
            >
                <AgGridReact
                  rowData={previewRows}
                  columnDefs={previewColumns}
                  defaultColDef={defaultPreviewColDef}
                  animateRows
                  rowHeight={46}
                  headerHeight={44}
              suppressCellFocus
                  overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>Apply criteria to preview devices.</span>"
                  onGridReady={handlePreviewGridReady}
                  theme={gridTheme}
                  pagination
                  paginationPageSize={20}
                  paginationPageSizeSelector={[20, 50, 100]}
                  style={{ width: "100%", height: "100%", minHeight: "100%", fontFamily: gridFontFamily }}
                />
              </Box>
            </Box>
          </Box>
        </TabPanel>

        {saveError ? (
          <Box
            sx={{
              background: "rgba(255,179,179,0.08)",
              color: "#ffb4b4",
              border: "1px solid rgba(255,179,179,0.35)",
              borderRadius: 1.5,
              p: 1.5,
            }}
          >
            {saveError}
          </Box>
        ) : null}
      </Box>
    </Paper>
  );
}
