import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  Button,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  FormControlLabel,
  Menu,
  MenuItem,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import AppsRoundedIcon from "@mui/icons-material/AppsRounded";
import BlockRoundedIcon from "@mui/icons-material/BlockRounded";
import FileDownloadRoundedIcon from "@mui/icons-material/FileDownloadRounded";
import ImageRoundedIcon from "@mui/icons-material/ImageRounded";
import Inventory2RoundedIcon from "@mui/icons-material/Inventory2Rounded";
import LockOpenRoundedIcon from "@mui/icons-material/LockOpenRounded";
import TerminalRoundedIcon from "@mui/icons-material/TerminalRounded";
import { AgGridReact } from "ag-grid-react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { ConfirmDeleteDialog } from "../Dialogs.jsx";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_PAPER_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { BOREALIS_BLUE, CountSliderGroup } from "../Automation/Watchdogs/shared.jsx";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { APP_PATHS } from "../app/routes/paths.js";
import { DEFAULT_GRID_COL_DEF, DEVICE_DETAILS_GRID_THEME, GridShell, MAGIC_UI } from "../Devices/Tabs/Shared.jsx";
import {
  buildIconOverrideDialogState,
  buildUninstallOverrideDialogState,
} from "../Devices/Tabs/Installed_Software.jsx";

const PAGE_TITLE = "Software Audit";
const PAGE_SUBTITLE = "Detailed list of all installed software spanning all deployed agents.";

const PLATFORM_FILTER_OPTIONS = [
  { key: "windows", label: "Windows" },
  { key: "linux", label: "Linux" },
  { key: "macos", label: "MacOS" },
];

const SOURCE_FILTER_OPTIONS = [
  { key: "locally_installed", label: "Locally Installed" },
  { key: "windows_store", label: "Windows Store" },
  { key: "dpkg", label: "DPKG" },
  { key: "rpm", label: "RPM" },
];

const FILTER_LABEL_SX = {
  color: BOREALIS_BLUE,
  fontSize: 11,
  fontWeight: 600,
  lineHeight: 1.1,
  pl: 1,
};

const ACTION_BUTTON_SX = {
  minWidth: 118,
  minHeight: 34,
  borderRadius: 999,
  px: 1.8,
  textTransform: "none",
  fontWeight: 600,
  color: MAGIC_UI.textBright,
  border: "1px solid rgba(148,163,184,0.36)",
  background: "rgba(5,10,24,0.82)",
  "&:hover": {
    background: "rgba(9,16,34,0.94)",
    borderColor: "rgba(125,211,252,0.46)",
  },
  "&.Mui-disabled": {
    color: "rgba(148,163,184,0.76)",
    borderColor: "rgba(148,163,184,0.22)",
    background: "rgba(15,23,42,0.42)",
  },
};

const MENU_PAPER_SX = {
  bgcolor: "rgba(8,12,24,0.96)",
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  backdropFilter: "blur(14px)",
  borderRadius: 2,
  minWidth: 288,
  px: 0.8,
  py: 0.8,
};

const MENU_ITEM_SX = {
  minHeight: 42,
  borderRadius: 1.6,
  color: MAGIC_UI.textBright,
  alignItems: "center",
  px: 1,
  py: 0.85,
  position: "relative",
  overflow: "hidden",
  "&:hover": {
    backgroundColor: "rgba(88,166,255,0.12)",
  },
  "&::before": {
    content: '""',
    position: "absolute",
    left: 0,
    top: 8,
    bottom: 8,
    width: 3,
    borderRadius: 999,
    background: "transparent",
  },
  "&:hover::before": {
    background: "#58a6ff",
  },
};

const MENU_DANGER_ITEM_SX = {
  ...MENU_ITEM_SX,
  "&:hover": {
    backgroundColor: "rgba(248,113,113,0.1)",
  },
  "&:hover::before": {
    background: "#58a6ff",
  },
};

const MENU_SECTION_LABEL_SX = {
  px: 1.2,
  pt: 0.65,
  pb: 0.45,
  color: "rgba(148,163,184,0.72)",
  fontSize: "0.68rem",
  fontWeight: 700,
  letterSpacing: "0.08em",
  textTransform: "uppercase",
};

const MENU_DIVIDER_SX = {
  my: 0.55,
  borderColor: "rgba(148,163,184,0.16)",
};

const MENU_HEADER_SX = {
  display: "flex",
  alignItems: "center",
  gap: 1,
  px: 1.1,
  pt: 0.55,
  pb: 0.85,
};

const MENU_HEADER_ICON_SX = {
  width: 32,
  height: 32,
  borderRadius: 1.35,
  flexShrink: 0,
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  border: "1px solid rgba(148,163,184,0.14)",
  background: "rgba(255,255,255,0.04)",
  color: "#8fd3ff",
};

const MENU_ROW_ICON_SX = {
  mt: 0.18,
  mr: 1,
  fontSize: 18,
  flexShrink: 0,
};

const MENU_LABEL_SX = {
  color: MAGIC_UI.textBright,
  fontSize: "0.84rem",
  fontWeight: 500,
  lineHeight: 1.2,
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

const MENU_DESCRIPTION_SX = {
  color: "rgba(148,163,184,0.78)",
  fontSize: "0.73rem",
  lineHeight: 1.25,
  mt: 0.25,
};

const MENU_TRUNCATE_SX = {
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

const MENU_GROUP_LABELS = {
  primary: "Primary",
  organize: "Organize",
  danger: "Danger Zone",
  view: "View",
};

const MENU_GROUP_ORDER = ["primary", "organize", "danger", "view"];

const TEXT_FIELD_SX = {
  "& .MuiOutlinedInput-root": {
    borderRadius: 2,
    background: "rgba(7, 12, 26, 0.82)",
    color: MAGIC_UI.textBright,
    "& fieldset": { borderColor: "rgba(148,163,184,0.28)" },
    "&:hover fieldset": { borderColor: "rgba(125,211,252,0.4)" },
    "&.Mui-focused fieldset": {
      borderColor: "rgba(125,211,252,0.62)",
      boxShadow: "0 0 0 1px rgba(125,211,252,0.12)",
    },
  },
  "& .MuiInputLabel-root": { color: "rgba(191,219,254,0.88)" },
  "& .MuiInputLabel-root.Mui-focused": { color: "#9bd7ff" },
  "& .MuiFormHelperText-root": {
    color: "rgba(148,163,184,0.86)",
    marginLeft: 0,
  },
  "& .MuiSelect-icon": { color: "#9bd7ff" },
};

const ICON_IMAGE_SX = {
  width: 27,
  height: 27,
  objectFit: "contain",
  flexShrink: 0,
};

const STEAM_BADGE_SX = {
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  minWidth: 34,
  minHeight: 34,
  borderRadius: 999,
  border: "1px solid rgba(148,163,184,0.28)",
  background: "rgba(5,10,24,0.74)",
  color: "#8fbfff",
  fontSize: "1.3rem",
  cursor: "help",
};

const NAME_NORMALIZER_DROP_WORDS = new Set([
  "the",
  "app",
  "application",
  "software",
  "desktop",
  "client",
  "x64",
  "x86",
  "64bit",
  "32bit",
  "windows",
  "linux",
  "mac",
  "macos",
  "stable",
  "beta",
  "preview",
  "lts",
]);

function text(value) {
  return String(value || "").trim();
}

const SOFTWARE_NAME_COLLATOR = new Intl.Collator(undefined, {
  numeric: true,
  sensitivity: "base",
});

function compareSoftwareNames(left = "", right = "") {
  return SOFTWARE_NAME_COLLATOR.compare(text(left), text(right));
}

function compareSoftwareRowsByName(left = {}, right = {}) {
  return (
    compareSoftwareNames(left?.name, right?.name) ||
    compareSoftwareNames(left?.version, right?.version) ||
    compareSoftwareNames(left?.source, right?.source)
  );
}

function sourceFilterCategory(source = "") {
  const normalized = text(source).toLowerCase();
  if (["windows_store", "appx", "ms_store", "store"].includes(normalized)) {
    return "windows_store";
  }
  if (normalized === "dpkg") return "dpkg";
  if (normalized === "rpm") return "rpm";
  return "locally_installed";
}

function metadata(row = {}) {
  return row?.metadata && typeof row.metadata === "object" && !Array.isArray(row.metadata) ? row.metadata : {};
}

function iconHash(row = {}) {
  return text(metadata(row).icon_hash).toLowerCase();
}

function SoftwareIcon({ row = {}, size = 27 }) {
  const hash = iconHash(row);
  const [failed, setFailed] = useState(false);
  useEffect(() => setFailed(false), [hash]);
  if (!hash || failed) return <AppsRoundedIcon sx={{ fontSize: size - 3, color: "#8fd3ff" }} />;
  return (
    <Box
      component="img"
      src={`/api/device/software/icon/${encodeURIComponent(hash)}`}
      alt=""
      loading="lazy"
      onError={() => setFailed(true)}
      sx={{ ...ICON_IMAGE_SX, width: size, height: size }}
    />
  );
}

function isSteam(row = {}) {
  const platform = text(row?.distribution_platform).toLowerCase();
  if (platform === "steam") return true;
  const meta = metadata(row);
  const uninstallString = text(meta.uninstall_string);
  const installLocation = text(meta.install_location);
  return /\bsteam:\/\/uninstall\/\d+\b/i.test(uninstallString) || /(^|[\\/])steamapps[\\/]+common([\\/]|$)/i.test(installLocation);
}

function isUninstallSupported(row = {}) {
  if (isSteam(row)) return false;
  return Boolean(row?.uninstall?.supported);
}

function actionKey(row = {}) {
  return `${text(row.name).toLowerCase()}::${text(row.version).toLowerCase()}::${text(row.source).toLowerCase()}`;
}

function normalizeNameForDedupe(value = "") {
  const cleaned = text(value)
    .toLowerCase()
    .replace(/\([^)]*\)/g, " ")
    .replace(/\b(v?\d+(?:\.\d+){1,4}|lts|stable|beta|preview)\b/g, " ")
    .replace(/[^a-z0-9]+/g, " ");
  const tokens = cleaned
    .split(/\s+/)
    .map((token) => token.trim())
    .filter((token) => token && !NAME_NORMALIZER_DROP_WORDS.has(token));
  return tokens.join(" ");
}

function dedupeNameKey(value = "") {
  const normalized = normalizeNameForDedupe(value);
  if (!normalized) return "";
  const tokens = normalized.split(/\s+/).filter(Boolean);
  return [...new Set(tokens)].sort().join(" ");
}

function canonicalName(rows = []) {
  const counts = new Map();
  rows.forEach((row) => {
    const name = text(row.name);
    if (!name) return;
    counts.set(name, (counts.get(name) || 0) + 1);
  });
  return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))[0]?.[0] || "Software";
}

function versionLabel(rows = []) {
  const versions = [...new Set(rows.map((row) => text(row.version)).filter(Boolean))];
  if (!versions.length) return "";
  if (versions.length === 1) return versions[0];
  return `Mixed (${versions.length})`;
}

function uniqueDevices(rows = []) {
  const devices = new Map();
  rows.forEach((row) => {
    const hostname = text(row.hostname);
    if (!hostname || devices.has(hostname.toLowerCase())) return;
    devices.set(hostname.toLowerCase(), {
      hostname,
      device_guid: text(row.device_guid),
      site_name: text(row.site_name),
    });
  });
  return [...devices.values()];
}

function buildExactDisplayRows(rows = []) {
  const groups = new Map();
  (Array.isArray(rows) ? rows : []).forEach((row) => {
    const key = `${actionKey(row)}::${text(row.platform)}`;
    const group = groups.get(key) || [];
    group.push(row);
    groups.set(key, group);
  });
  return [...groups.values()].map((group, index) => {
    const devices = uniqueDevices(group);
    const representative = group.find((row) => iconHash(row)) || group[0];
    return {
      ...representative,
      id: `exact-${index}-${actionKey(representative)}`,
      childRows: group,
      devices,
      installedOn: devices.length,
      deduped: false,
    };
  });
}

function buildDisplayRows(rows = [], dedupeEnabled = false) {
  if (!dedupeEnabled) {
    return buildExactDisplayRows(rows);
  }

  const groups = new Map();
  (Array.isArray(rows) ? rows : []).forEach((row) => {
    const key = dedupeNameKey(row.name) || text(row.name).toLowerCase() || `software-${row.id || groups.size}`;
    const group = groups.get(key) || [];
    group.push(row);
    groups.set(key, group);
  });

  return [...groups.values()].map((rowsInGroup, index) => {
    const devices = uniqueDevices(rowsInGroup);
    const name = canonicalName(rowsInGroup);
    const canonicalRows = rowsInGroup.filter((row) => text(row.name) === name);
    const representative =
      canonicalRows.find((row) => iconHash(row)) ||
      rowsInGroup.find((row) => iconHash(row)) ||
      canonicalRows[0] ||
      rowsInGroup[0] ||
      {};
    return {
      ...representative,
      id: `dedupe-${index}-${dedupeNameKey(name) || index}`,
      name,
      version: versionLabel(rowsInGroup),
      source: "",
      childRows: rowsInGroup,
      devices,
      installedOn: devices.length,
      deduped: true,
      uninstall: {
        supported: rowsInGroup.some((row) => isUninstallSupported(row)),
        reason: "",
        summary: "Dedupe group contains uninstall-capable rows.",
      },
    };
  });
}

function softwareEntriesFor(row = {}) {
  return (Array.isArray(row.childRows) && row.childRows.length ? row.childRows : [row]).map((item) => ({
    hostname: text(item.hostname),
    name: text(item.name),
    version: text(item.version),
    source: text(item.source),
  }));
}

function suggestedIconCandidates(row = {}) {
  const rows = Array.isArray(row.childRows) && row.childRows.length ? row.childRows : [row];
  const candidates = [];
  rows.forEach((item) => {
    const state = buildIconOverrideDialogState(item);
    [...(state.candidates || []), state.manualPath, state.selectedCandidate]
      .map(text)
      .filter(Boolean)
      .forEach((candidate) => {
        if (!candidates.includes(candidate)) candidates.push(candidate);
      });
  });
  return candidates.slice(0, 10);
}

function sanitizeCsvFilename(value = "") {
  const sanitized = text(value)
    .replace(/[<>:"/\\|?*\x00-\x1F]/g, "_")
    .replace(/\s+/g, "_")
    .replace(/_+/g, "_")
    .replace(/^_+|_+$/g, "");
  return sanitized || "Software";
}

function csvCell(value = "") {
  const normalized = text(value);
  if (/[",\r\n]/.test(normalized)) {
    return `"${normalized.replace(/"/g, '""')}"`;
  }
  return normalized;
}

function buildSoftwareDeviceCsv(row = {}) {
  const childRows = Array.isArray(row.childRows) && row.childRows.length ? row.childRows : [row];
  const headers = ["Software Name", "Version", "Hostname", "Site", "Operating System", "Platform", "Source"];
  const lines = [
    headers.map(csvCell).join(","),
    ...childRows.map((item) =>
      [
        text(item.name),
        text(item.version),
        text(item.hostname),
        text(item.site_name),
        text(item.operating_system),
        text(item.platform),
        text(item.source),
      ]
        .map(csvCell)
        .join(",")
    ),
  ];
  return `${lines.join("\r\n")}\r\n`;
}

function SoftwareNameCell({ row = {}, onContextMenu }) {
  return (
    <Box
      onContextMenu={(event) => onContextMenu?.(event, row)}
      sx={{ display: "inline-flex", alignItems: "center", gap: 1, minWidth: 0, cursor: "context-menu" }}
    >
      <SoftwareIcon row={row} />
      <Box component="span" sx={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
        {text(row.name) || "Software"}
      </Box>
    </Box>
  );
}

function ActionCell({ row = {}, busyKey = "", onUninstall }) {
  if (isSteam(row)) {
    return (
      <Tooltip title="Steam software cannot be uninstalled through Borealis. Uninstall it via Steam directly." placement="top">
        <Box component="span" sx={STEAM_BADGE_SX}>
          <i className="fa-brands fa-steam" aria-hidden="true" />
        </Box>
      </Tooltip>
    );
  }
  if (!isUninstallSupported(row)) return null;
  const busy = busyKey === String(row.id || "");
  return (
    <Button
      size="small"
      disabled={busy}
      onClick={() => onUninstall?.(row)}
      sx={ACTION_BUTTON_SX}
    >
      {busy ? "Queueing..." : "Uninstall"}
    </Button>
  );
}

function FilterSliderBlock({ label = "", children }) {
  return (
    <Box sx={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: "8px" }}>
      <Typography component="span" sx={FILTER_LABEL_SX}>
        {label}
      </Typography>
      {children}
    </Box>
  );
}

export default function SoftwareList() {
  const gridRef = useRef(null);
  const softwareRefreshTimersRef = useRef([]);
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const notifyOperator = useAppNotifications();
  const [softwareRows, setSoftwareRows] = useState([]);
  const [loadError, setLoadError] = useState("");
  const [platformFilter, setPlatformFilter] = useState("windows");
  const [sourceFilter, setSourceFilter] = useState("");
  const [siteFilter, setSiteFilter] = useState("");
  const [dedupeEnabled, setDedupeEnabled] = useState(false);
  const [busyKey, setBusyKey] = useState("");
  const [confirmRow, setConfirmRow] = useState(null);
  const [contextMenu, setContextMenu] = useState({ open: false, top: 0, left: 0, row: null });
  const [dialog, setDialog] = useState({
    open: false,
    kind: "",
    row: null,
    selectedCandidate: "",
    manualPath: "",
    clearIcon: false,
    applicationPath: "",
    arguments: "",
    reason: "",
    submitting: false,
  });
  const selectedSiteId = useMemo(
    () => String(searchParams.get("site") || "").trim(),
    [searchParams]
  );

  useRoutePageChrome({
    title: PAGE_TITLE,
    subtitle: PAGE_SUBTITLE,
    Icon: Inventory2RoundedIcon,
  });

  const loadSoftwareRows = useCallback(async () => {
    try {
      const response = await fetch("/api/software/audit", { credentials: "include" });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      setSoftwareRows(Array.isArray(payload?.software) ? payload.software : []);
      setLoadError("");
    } catch (error) {
      setLoadError(String(error?.message || error));
      setSoftwareRows([]);
    }
  }, []);

  useEffect(() => {
    void loadSoftwareRows();
  }, [loadSoftwareRows]);

  const clearSoftwareRefreshTimers = useCallback(() => {
    if (typeof window === "undefined") return;
    softwareRefreshTimersRef.current.forEach((timerId) => window.clearTimeout(timerId));
    softwareRefreshTimersRef.current = [];
  }, []);

  const requestSoftwareAuditRefresh = useCallback(
    ({ burst = false } = {}) => {
      clearSoftwareRefreshTimers();
      void loadSoftwareRows();
      if (!burst || typeof window === "undefined") return;
      [1200, 2500, 5000, 10000, 20000].forEach((delayMs) => {
        const timerId = window.setTimeout(() => {
          void loadSoftwareRows();
        }, delayMs);
        softwareRefreshTimersRef.current.push(timerId);
      });
    },
    [clearSoftwareRefreshTimers, loadSoftwareRows]
  );

  useEffect(() => () => clearSoftwareRefreshTimers(), [clearSoftwareRefreshTimers]);

  useEffect(() => {
    try {
      const nextSiteFilter = window.localStorage.getItem("software_audit_initial_site_filter");
      if (nextSiteFilter && nextSiteFilter.trim()) {
        setSiteFilter(nextSiteFilter.trim());
      }
      window.localStorage.removeItem("software_audit_initial_site_filter");
    } catch {
      /* noop */
    }
  }, []);

  useEffect(() => {
    if (!selectedSiteId) return;
    const rowSiteName = softwareRows.find((row) => String(row?.site_id ?? "") === selectedSiteId)?.site_name;
    setSiteFilter(text(rowSiteName) || `Site ${selectedSiteId}`);
  }, [selectedSiteId, softwareRows]);

  const siteScopedRows = useMemo(
    () =>
      softwareRows.filter((row) => {
        if (selectedSiteId && String(row?.site_id ?? "") !== selectedSiteId) {
          return false;
        }
        if (!selectedSiteId && siteFilter && text(row.site_name).toLowerCase() !== siteFilter.toLowerCase()) {
          return false;
        }
        return true;
      }),
    [selectedSiteId, siteFilter, softwareRows]
  );

  const platformCountRows = useMemo(
    () =>
      sourceFilter
        ? siteScopedRows.filter((row) => sourceFilterCategory(row.source) === sourceFilter)
        : siteScopedRows,
    [siteScopedRows, sourceFilter]
  );

  const sourceCountRows = useMemo(
    () =>
      platformFilter
        ? siteScopedRows.filter((row) => text(row.platform) === platformFilter)
        : siteScopedRows,
    [platformFilter, siteScopedRows]
  );

  const platformCounts = useMemo(
    () =>
      platformCountRows.reduce(
        (counts, row) => {
          const platform = text(row.platform) || "unknown";
          if (platform in counts) counts[platform] += 1;
          return counts;
        },
        { windows: 0, linux: 0, macos: 0 }
      ),
    [platformCountRows]
  );

  const sourceCounts = useMemo(
    () =>
      sourceCountRows.reduce(
        (counts, row) => {
          const source = sourceFilterCategory(row.source);
          if (source in counts) counts[source] += 1;
          return counts;
        },
        SOURCE_FILTER_OPTIONS.reduce((counts, option) => ({ ...counts, [option.key]: 0 }), {})
      ),
    [sourceCountRows]
  );

  const visibleSourceRows = useMemo(
    () =>
      siteScopedRows.filter((row) => {
        if (platformFilter && text(row.platform) !== platformFilter) {
          return false;
        }
        if (sourceFilter && sourceFilterCategory(row.source) !== sourceFilter) {
          return false;
        }
        return true;
      }),
    [platformFilter, siteScopedRows, sourceFilter]
  );

  const displayRows = useMemo(
    () => buildDisplayRows(visibleSourceRows, dedupeEnabled),
    [dedupeEnabled, visibleSourceRows]
  );
  const sortedDisplayRows = useMemo(
    () => [...displayRows].sort(compareSoftwareRowsByName),
    [displayRows]
  );
  const softwareGridDefaultColDef = useMemo(
    () => ({ ...DEFAULT_GRID_COL_DEF, sortable: false }),
    []
  );

  const openContextMenu = useCallback((event, row, node = null) => {
    event?.preventDefault?.();
    event?.stopPropagation?.();
    if (node && !node.isSelected?.()) node.setSelected?.(true, true);
    setContextMenu({
      open: true,
      top: Number(event?.clientY || 0),
      left: Number(event?.clientX || 0),
      row,
    });
  }, []);

  const closeContextMenu = useCallback(() => {
    setContextMenu({ open: false, top: 0, left: 0, row: null });
  }, []);

  const openDialog = useCallback((kind, rowOverride = null) => {
    const row = rowOverride || contextMenu.row;
    closeContextMenu();
    if (!row) return;
    const candidates = suggestedIconCandidates(row);
    const uninstallState = buildUninstallOverrideDialogState(
      (Array.isArray(row.childRows) && row.childRows.length ? row.childRows : [row])[0] || row
    );
    setDialog({
      open: true,
      kind,
      row,
      selectedCandidate: "",
      manualPath: candidates[0] || "",
      candidatePaths: candidates,
      clearIcon: false,
      applicationPath: uninstallState.applicationPath || "",
      arguments: uninstallState.arguments || "",
      reason: "",
      submitting: false,
    });
  }, [closeContextMenu, contextMenu.row]);

  const closeDialog = useCallback(() => {
    setDialog({
      open: false,
      kind: "",
      row: null,
      selectedCandidate: "",
      manualPath: "",
      candidatePaths: [],
      clearIcon: false,
      applicationPath: "",
      arguments: "",
      reason: "",
      submitting: false,
    });
  }, []);

  const submitDialog = useCallback(async () => {
    const row = dialog.row;
    const kind = text(dialog.kind);
    if (!row || !kind) return;
    const entries = softwareEntriesFor(row);
    let endpoint = "";
    let body = { entries };
    if (kind === "icon_override") {
      const selectedPath = text(dialog.manualPath) || text(dialog.selectedCandidate);
      if (!dialog.clearIcon && !selectedPath) {
        await notifyOperator({ title: "Icon Override Required", message: "Choose an icon path before saving.", icon: "warning", variant: "warning" });
        return;
      }
      endpoint = "/api/software/action/icon-override";
      body = { ...body, ...(dialog.clearIcon ? { clear_icon: true } : { display_icon: selectedPath }) };
    } else if (kind === "uninstall_override") {
      if (!text(dialog.applicationPath)) {
        await notifyOperator({ title: "Application Path Required", message: "Enter application path before saving.", icon: "warning", variant: "warning" });
        return;
      }
      endpoint = "/api/software/action/uninstall-override";
      body = { ...body, application_path: text(dialog.applicationPath), arguments: text(dialog.arguments) };
    } else if (kind === "uninstall_block") {
      if (!text(dialog.reason)) {
        await notifyOperator({ title: "Reason Required", message: "Enter block reason before saving.", icon: "warning", variant: "warning" });
        return;
      }
      endpoint = "/api/software/action/uninstall-block";
      body = { ...body, reason: text(dialog.reason) };
    } else if (kind === "uninstall_unblock") {
      endpoint = "/api/software/action/uninstall-unblock";
    } else {
      return;
    }

    setDialog((current) => ({ ...current, submitting: true }));
    try {
      const response = await fetch(endpoint, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      closeDialog();
      if (kind === "icon_override") {
        requestSoftwareAuditRefresh({ burst: true });
      } else {
        await loadSoftwareRows();
      }
      await notifyOperator({
        title: "Software Management Updated",
        message: `<b>${text(row.name)}</b> updated across ${entries.length} software row${entries.length === 1 ? "" : "s"}.`,
        icon: "success",
        variant: "success",
      });
    } catch (error) {
      await notifyOperator({
        title: "Software Action Failed",
        message: `Borealis could not update <b>${text(row.name)}</b>: ${String(error?.message || error)}`,
        icon: "error",
        variant: "error",
      });
    } finally {
      setDialog((current) => ({ ...current, submitting: false }));
    }
  }, [closeDialog, dialog, loadSoftwareRows, notifyOperator, requestSoftwareAuditRefresh]);

  const confirmUninstall = useCallback(async () => {
    const row = confirmRow;
    if (!row) return;
    setBusyKey(String(row.id || ""));
    try {
      const response = await fetch("/api/software/uninstall", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ entries: softwareEntriesFor(row) }),
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload?.message || payload?.error || `HTTP ${response.status}`);
      const queuedCount = Number(payload?.count || payload?.queued?.length || 0);
      const errorCount = Array.isArray(payload?.errors) ? payload.errors.length : 0;
      await notifyOperator({
        title: "Software Uninstall Queued",
        message: `Queued uninstall for <b>${text(row.name)}</b> on ${queuedCount} device${queuedCount === 1 ? "" : "s"}${errorCount ? `; ${errorCount} device${errorCount === 1 ? "" : "s"} skipped.` : "."}`,
        icon: errorCount ? "warning" : "success",
        variant: errorCount ? "warning" : "success",
      });
      setConfirmRow(null);
    } catch (error) {
      await notifyOperator({
        title: "Software Uninstall Failed",
        message: `Borealis could not queue uninstall for <b>${text(row.name)}</b>: ${String(error?.message || error)}`,
        icon: "error",
        variant: "error",
      });
    } finally {
      setBusyKey("");
    }
  }, [confirmRow, notifyOperator]);

  const openInstalledDevices = useCallback((row = {}) => {
    const childRows = Array.isArray(row.childRows) && row.childRows.length ? row.childRows : [row];
    const names = [...new Set(childRows.map((item) => text(item.name)).filter(Boolean))];
    const hostnames = uniqueDevices(childRows).map((device) => device.hostname).filter(Boolean);
    const filters = {};
    if (names.length === 1) {
      filters.software = names[0];
    } else if (text(row.name)) {
      filters.software = text(row.name);
    }
    try {
      window.localStorage.setItem("device_list_initial_filters", JSON.stringify(filters));
      window.localStorage.setItem("device_list_initial_hostnames_filter", JSON.stringify(hostnames));
      navigate(APP_PATHS.devices);
    } catch {
      navigate(APP_PATHS.devices);
    }
  }, [navigate]);

  const exportSoftwareDeviceList = useCallback(async (row = {}) => {
    const childRows = Array.isArray(row.childRows) && row.childRows.length ? row.childRows : [row];
    if (!childRows.length || !text(row.name)) {
      await notifyOperator({
        title: "CSV Export Unavailable",
        message: "Borealis could not determine which software row to export.",
        icon: "warning",
        variant: "warning",
      });
      return;
    }

    const blob = new Blob([buildSoftwareDeviceCsv(row)], { type: "text/csv;charset=utf-8" });
    const objectUrl = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = objectUrl;
    link.download = `${sanitizeCsvFilename(row.name)}_DeviceList.csv`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(objectUrl);
    closeContextMenu();
  }, [closeContextMenu, notifyOperator]);

  const columnDefs = useMemo(
    () => [
      {
        field: "name",
        headerName: "Name",
        flex: 1.3,
        minWidth: 260,
        sortable: true,
        sort: "asc",
        sortingOrder: ["asc"],
        comparator: compareSoftwareNames,
        cellRenderer: (params) => <SoftwareNameCell row={params.data} onContextMenu={openContextMenu} />,
      },
      {
        field: "version",
        headerName: "Version",
        width: 180,
        minWidth: 160,
      },
      {
        field: "installedOn",
        headerName: "Installed On",
        width: 175,
        minWidth: 165,
        filter: "agNumberColumnFilter",
        cellRenderer: (params) => (
          <Box
            component="a"
            href="#"
            title={`${Number(params.value || 0).toLocaleString()} Device${Number(params.value || 0) === 1 ? "" : "s"}`}
            onClick={(event) => {
              event.preventDefault();
              event.stopPropagation();
              openInstalledDevices(params.data);
            }}
            sx={{
              color: "#58a6ff",
              textDecoration: "none",
              fontWeight: 500,
              cursor: "pointer",
              "&:hover": { textDecoration: "none" },
            }}
          >
            {`${Number(params.value || 0).toLocaleString()} Device${Number(params.value || 0) === 1 ? "" : "s"}`}
          </Box>
        ),
      },
      {
        field: "action",
        headerName: "Action",
        width: 170,
        minWidth: 150,
        sortable: false,
        filter: false,
        suppressHeaderMenuButton: true,
        cellStyle: { display: "flex", alignItems: "center", justifyContent: "center" },
        cellRenderer: (params) => (
          <ActionCell row={params.data} busyKey={busyKey} onUninstall={(row) => setConfirmRow(row)} />
        ),
      },
    ],
    [busyKey, openContextMenu, openInstalledDevices]
  );

  const menuRow = contextMenu.row || {};
  const dialogKind = text(dialog.kind);
  const dialogTitle = {
    icon_override: `Create Global Icon Override - ${text(dialog.row?.name) || "Software"}`,
    uninstall_override: `Create Global Uninstall Override - ${text(dialog.row?.name) || "Software"}`,
    uninstall_block: `Block Uninstallation - ${text(dialog.row?.name) || "Software"}`,
    uninstall_unblock: `Unblock Uninstallation - ${text(dialog.row?.name) || "Software"}`,
  }[dialogKind] || "Software Action";
  const dialogSubtitle = {
    icon_override: "Set or clear icon override for every software row represented by this selection.",
    uninstall_override: "Set trusted uninstall command for every software row represented by this selection.",
    uninstall_block: "Block uninstall actions for every software row represented by this selection.",
    uninstall_unblock: "Remove matching uninstall block rules for every software row represented by this selection.",
  }[dialogKind] || "";
  const menuActions = useMemo(() => {
    const unavailableReason = contextMenu.row ? "" : "Select a software row first.";
    return [
      {
        id: "export-device-list",
        group: "primary",
        label: "Export Software Device List to CSV",
        icon: FileDownloadRoundedIcon,
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description: "Download a CSV of devices represented by this software row.",
        onClick: () => {
          void exportSoftwareDeviceList(contextMenu.row);
        },
      },
      {
        id: "uninstall-override",
        group: "primary",
        label: "Create Global Uninstall Override",
        icon: TerminalRoundedIcon,
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description: "Define the trusted uninstall command Borealis should use for this software.",
        onClick: () => openDialog("uninstall_override", contextMenu.row),
      },
      {
        id: "icon-override",
        group: "primary",
        label: "Create Global Icon Override",
        icon: ImageRoundedIcon,
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description: "Set or clear the display icon Borealis uses for this software.",
        onClick: () => openDialog("icon_override", contextMenu.row),
      },
      {
        id: "uninstall-block",
        group: "danger",
        label: "Block Uninstallation",
        icon: BlockRoundedIcon,
        intent: "danger",
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description: "Prevent Borealis from queueing uninstall actions for this software.",
        onClick: () => openDialog("uninstall_block", contextMenu.row),
      },
      {
        id: "uninstall-unblock",
        group: "danger",
        label: "Unblock Uninstallation",
        icon: LockOpenRoundedIcon,
        disabled: Boolean(unavailableReason),
        disabledReason: unavailableReason,
        description: "Remove matching global uninstall block rules for this software.",
        onClick: () => openDialog("uninstall_unblock", contextMenu.row),
      },
    ];
  }, [contextMenu.row, exportSoftwareDeviceList, openDialog]);
  const groupedMenuActions = useMemo(
    () =>
      MENU_GROUP_ORDER.map((groupId) => ({
        id: groupId,
        label: MENU_GROUP_LABELS[groupId],
        actions: menuActions.filter((action) => action.group === groupId),
      })).filter((group) => group.actions.length),
    [menuActions]
  );

  return (
    <PageBodyFrame
      variant="grid_with_stack"
      stack={
        <Box sx={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 1.5, flexWrap: "wrap" }}>
          <Box sx={{ display: "flex", alignItems: "flex-start", columnGap: 1, rowGap: 1, flexWrap: "wrap" }}>
            <FilterSliderBlock label="Operating System">
              <CountSliderGroup
                options={PLATFORM_FILTER_OPTIONS}
                activeKey={platformFilter}
                counts={platformCounts}
                onChange={setPlatformFilter}
              />
            </FilterSliderBlock>
            <FilterSliderBlock label="Source">
              <CountSliderGroup
                options={SOURCE_FILTER_OPTIONS}
                activeKey={sourceFilter}
                counts={sourceCounts}
                onChange={setSourceFilter}
              />
            </FilterSliderBlock>
            {siteFilter ? (
              <Box
                sx={{
                  display: "inline-flex",
                  alignItems: "center",
                  alignSelf: "flex-end",
                  gap: 0.8,
                  borderRadius: 999,
                  border: "1px solid rgba(148,163,184,0.35)",
                  background: "rgba(8,12,24,0.78)",
                  px: 1.35,
                  py: 0.55,
                }}
              >
                <Typography sx={{ color: "rgba(191,219,254,0.92)", fontSize: "0.82rem", fontWeight: 600 }}>
                  {`Site: ${siteFilter}`}
                </Typography>
                <Button
                  size="small"
                  onClick={() => {
                    setSiteFilter("");
                    if (selectedSiteId) {
                      const nextParams = new URLSearchParams(searchParams);
                      nextParams.delete("site");
                      setSearchParams(nextParams, { replace: true });
                    }
                  }}
                  sx={{
                    minWidth: 0,
                    px: 1,
                    py: 0.15,
                    borderRadius: 999,
                    color: MAGIC_UI.textBright,
                    textTransform: "none",
                    fontSize: "0.76rem",
                    border: "1px solid rgba(148,163,184,0.28)",
                    "&:hover": { borderColor: "rgba(125,211,252,0.5)", background: "rgba(125,211,252,0.1)" },
                  }}
                >
                  Clear
                </Button>
              </Box>
            ) : null}
          </Box>
          <FormControlLabel
            sx={{
              color: "rgba(191,219,254,0.9)",
              "& .MuiFormControlLabel-label": { fontSize: "0.88rem" },
            }}
            control={
              <Checkbox
                checked={dedupeEnabled}
                onChange={(event) => setDedupeEnabled(Boolean(event.target.checked))}
                sx={{
                  color: "rgba(148,163,184,0.72)",
                  "&.Mui-checked": { color: "#7dd3fc" },
                }}
              />
            }
            label="Intelligently Deduplicate"
            labelPlacement="end"
          />
        </Box>
      }
    >
      {loadError ? (
        <Box sx={{ p: 2, color: "rgba(248,113,113,0.95)" }}>Failed to load software audit: {loadError}</Box>
      ) : null}
      <GridShell
        sx={{
          flexGrow: 1,
          minHeight: 520,
          borderRadius: 0,
          border: "none",
          "--ag-row-hover-color": "rgba(73,156,196,0.2)",
          "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
          "& .ag-row-hover": { backgroundColor: "rgba(73,156,196,0.2) !important" },
          "& .ag-row-selected": {
            backgroundColor: "rgba(125,211,252,0.2) !important",
            boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
          },
        }}
      >
        <AgGridReact
          ref={gridRef}
          rowData={sortedDisplayRows}
          columnDefs={columnDefs}
          defaultColDef={softwareGridDefaultColDef}
          rowSelection={{ mode: "singleRow", checkboxes: false, headerCheckbox: false, enableClickSelection: true }}
          suppressCellFocus
          suppressContextMenu
          preventDefaultOnContextMenu
          pagination
          paginationPageSize={100}
          paginationPageSizeSelector={[20, 50, 100]}
          animateRows
          getRowId={(params) => String(params.data?.id || params.rowIndex)}
          onCellContextMenu={(params) => openContextMenu(params.event, params.data, params.node)}
          theme={DEVICE_DETAILS_GRID_THEME}
        />
      </GridShell>
      <Menu
        open={Boolean(contextMenu.open)}
        onClose={closeContextMenu}
        anchorReference="anchorPosition"
        anchorPosition={contextMenu.open ? { top: contextMenu.top, left: contextMenu.left } : undefined}
        PaperProps={{ sx: MENU_PAPER_SX }}
      >
        <Box component="li" role="presentation" sx={MENU_HEADER_SX}>
          <Box sx={MENU_HEADER_ICON_SX}>
            <SoftwareIcon row={menuRow} size={22} />
          </Box>
          <Box sx={{ minWidth: 0 }}>
            <Tooltip title={text(menuRow.name) || "Software"} placement="top-start">
              <Typography
                sx={{
                  ...MENU_TRUNCATE_SX,
                  color: MAGIC_UI.textBright,
                  fontSize: "0.88rem",
                  fontWeight: 600,
                  lineHeight: 1.2,
                  maxWidth: 240,
                }}
              >
                {text(menuRow.name) || "Software"}
              </Typography>
            </Tooltip>
            <Tooltip
              title={`${Number(menuRow.installedOn || 0).toLocaleString()} Device${Number(menuRow.installedOn || 0) === 1 ? "" : "s"}`}
              placement="top-start"
            >
              <Typography
                sx={{
                  ...MENU_TRUNCATE_SX,
                  color: "rgba(148,163,184,0.82)",
                  fontSize: "0.73rem",
                  lineHeight: 1.25,
                  mt: 0.22,
                  maxWidth: 240,
                }}
              >
                {`${Number(menuRow.installedOn || 0).toLocaleString()} Device${Number(menuRow.installedOn || 0) === 1 ? "" : "s"}`}
              </Typography>
            </Tooltip>
          </Box>
        </Box>
        {groupedMenuActions.map((group) => (
          <React.Fragment key={group.id}>
            <Divider component="li" sx={MENU_DIVIDER_SX} />
            <Box component="li" role="presentation" sx={MENU_SECTION_LABEL_SX}>{group.label}</Box>
            {group.actions.map((action) => {
              const Icon = action.icon;
              const helperText = action.disabledReason || action.description || "";
              return (
                <MenuItem
                  key={action.id}
                  disabled={Boolean(action.disabled)}
                  onClick={() => action.onClick?.()}
                  sx={action.intent === "danger" ? MENU_DANGER_ITEM_SX : MENU_ITEM_SX}
                >
                  <Icon
                    sx={{
                      ...MENU_ROW_ICON_SX,
                      color: action.intent === "danger" ? "rgba(248,113,113,0.92)" : "rgba(226,232,240,0.92)",
                    }}
                  />
                  <Box
                    sx={{
                      flex: 1,
                      minWidth: 0,
                      display: "flex",
                      flexDirection: "column",
                      justifyContent: helperText ? "flex-start" : "center",
                    }}
                  >
                    <Typography sx={MENU_LABEL_SX}>{action.label}</Typography>
                    {helperText ? <Typography sx={MENU_DESCRIPTION_SX}>{helperText}</Typography> : null}
                  </Box>
                </MenuItem>
              );
            })}
          </React.Fragment>
        ))}
      </Menu>
      <Dialog open={Boolean(dialog.open)} onClose={dialog.submitting ? undefined : closeDialog} fullWidth maxWidth="sm" PaperProps={{ sx: DIALOG_PAPER_SX }}>
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock title={dialogTitle} subtitle={dialogSubtitle} />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Box sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
            {dialogKind === "icon_override" ? (
              <>
                <TextField
                  select
                  fullWidth
                  label="Suggested Icon Candidates"
                  value={dialog.selectedCandidate || ""}
                  disabled={Boolean(dialog.clearIcon)}
                  onChange={(event) => {
                    const nextValue = text(event.target.value);
                    setDialog((current) => ({ ...current, selectedCandidate: nextValue, manualPath: nextValue || current.manualPath || "" }));
                  }}
                  helperText="Choose a suggested candidate or enter a manual path below."
                  sx={TEXT_FIELD_SX}
                >
                  <MenuItem value="">Choose a suggested candidate</MenuItem>
                  {(dialog.candidatePaths || []).map((candidate) => (
                    <MenuItem key={candidate} value={candidate}>{candidate}</MenuItem>
                  ))}
                </TextField>
                <TextField
                  fullWidth
                  label="Manual Icon Resource Path"
                  value={dialog.manualPath || ""}
                  disabled={Boolean(dialog.clearIcon)}
                  onChange={(event) => setDialog((current) => ({ ...current, manualPath: text(event.target.value) }))}
                  helperText="Use a verified EXE, DLL, or ICO path. If you omit ,0, Borealis defaults to icon index 0."
                  sx={TEXT_FIELD_SX}
                />
              </>
            ) : null}
            {dialogKind === "uninstall_override" ? (
              <>
                <TextField
                  fullWidth
                  label="Application Path"
                  value={dialog.applicationPath || ""}
                  onChange={(event) => setDialog((current) => ({ ...current, applicationPath: text(event.target.value) }))}
                  helperText="Example: C:\\Program Files\\Vendor\\App\\uninstall.exe"
                  sx={TEXT_FIELD_SX}
                />
                <TextField
                  fullWidth
                  label="Arguments"
                  value={dialog.arguments || ""}
                  onChange={(event) => setDialog((current) => ({ ...current, arguments: text(event.target.value) }))}
                  helperText="Optional arguments passed to the application path above."
                  sx={TEXT_FIELD_SX}
                />
              </>
            ) : null}
            {dialogKind === "uninstall_block" ? (
              <TextField
                fullWidth
                multiline
                minRows={4}
                label="Reason"
                value={dialog.reason || ""}
                onChange={(event) => setDialog((current) => ({ ...current, reason: text(event.target.value) }))}
                helperText="This reason is shown to other operators when Borealis blocks uninstall."
                sx={TEXT_FIELD_SX}
              />
            ) : null}
            {dialogKind === "uninstall_unblock" ? (
              <Box sx={{ border: `1px solid ${MAGIC_UI.panelBorder}`, borderRadius: 2, px: 1.6, py: 1.5, background: "rgba(7,12,26,0.78)" }}>
                <Typography sx={{ fontSize: 13, color: MAGIC_UI.textBright, lineHeight: 1.55 }}>
                  Borealis will remove matching uninstall block rules for every software row represented by this selection.
                </Typography>
              </Box>
            ) : null}
          </Box>
        </DialogContent>
        <DialogActions sx={{ ...DIALOG_ACTIONS_SX, justifyContent: "space-between", gap: 1 }}>
          {dialogKind === "icon_override" ? (
            <FormControlLabel
              sx={{ mr: "auto", color: "rgba(191,219,254,0.9)", "& .MuiFormControlLabel-label": { fontSize: "0.88rem" } }}
              control={
                <Checkbox
                  checked={Boolean(dialog.clearIcon)}
                  disabled={Boolean(dialog.submitting)}
                  onChange={(event) => {
                    const checked = Boolean(event.target.checked);
                    setDialog((current) => ({ ...current, clearIcon: checked, ...(checked ? { selectedCandidate: "", manualPath: "" } : {}) }));
                  }}
                  sx={{ color: "rgba(148,163,184,0.72)", "&.Mui-checked": { color: "#7dd3fc" } }}
                />
              }
              label="Remove the icon entirely"
            />
          ) : <Box sx={{ mr: "auto" }} />}
          <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
            <Button onClick={closeDialog} disabled={Boolean(dialog.submitting)} sx={DIALOG_BUTTON_SX}>Cancel</Button>
            <Button onClick={() => void submitDialog()} disabled={Boolean(dialog.submitting)} sx={DIALOG_BUTTON_SX}>
              {dialog.submitting ? "Saving..." : "Save"}
            </Button>
          </Box>
        </DialogActions>
      </Dialog>
      <ConfirmDeleteDialog
        open={Boolean(confirmRow)}
        onCancel={() => {
          if (busyKey) return;
          setConfirmRow(null);
        }}
        onConfirm={confirmUninstall}
        title="Uninstall Software"
        confirmLabel={busyKey ? "Queueing..." : "Uninstall"}
        confirmDisabled={Boolean(busyKey)}
        message={
          confirmRow
            ? `Borealis will ask ${Number(confirmRow.installedOn || 0).toLocaleString()} device${Number(confirmRow.installedOn || 0) === 1 ? "" : "s"} to silently uninstall ${text(confirmRow.name)}${text(confirmRow.version) ? ` ${text(confirmRow.version)}` : ""}. Output will be recorded in Activity History.`
            : ""
        }
      />
    </PageBodyFrame>
  );
}
