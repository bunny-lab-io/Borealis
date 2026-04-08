import React, { useState, useEffect, useCallback, useMemo, useRef } from "react";
import { useNavigate } from "react-router-dom";
import {
  Paper,
  Box,
  Typography,
  Button,
  Menu,
  MenuItem,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  TextField,
  CircularProgress,
  Link as MuiLink,
} from "@mui/material";
import AddIcon from "@mui/icons-material/Add";
import CachedIcon from "@mui/icons-material/Cached";
import SyncIcon from "@mui/icons-material/Sync";
import PolylineIcon from "@mui/icons-material/Polyline";
import CodeIcon from "@mui/icons-material/Code";
import MenuBookIcon from "@mui/icons-material/MenuBook";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { ConfirmDeleteDialog, NewWorkflowDialog } from "../Dialogs";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BODY_TEXT_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_INPUT_SX,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_SELECT_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";
import { DomainBadge, DirtyStatePill, resolveDomainMeta, DOMAIN_OPTIONS } from "./Assembly_Badges";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { useAuth } from "../app/providers/AuthContext.jsx";
import { APP_PATHS } from "../app/routes/paths.js";

import { Apps as AppsIcon } from "@mui/icons-material";

ModuleRegistry.registerModules([AllCommunityModule]);

/**
 * MagicUI Theme: Quartz base with Borealis aurora accents
 */
const myTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#1f2836",
  browserColorScheme: "dark",
  chromeBackgroundColor: {
    ref: "foregroundColor",
    mix: 0.07,
    onto: "backgroundColor",
  },
  fontFamily: {
    googleFont: "IBM Plex Sans",
  },
  foregroundColor: "#FFF",
  headerFontSize: 14,
});

const themeClassName = myTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';
const BOREALIS_BLUE = "#58a6ff";
const DARKER_GRAY = "#9aa3ad";
const AURORA_REPOSITORY_URL = "https://github.com/bunny-lab-io/Aurora";
const PAGE_SIZE = 25;
const ASSEMBLY_TYPE_FILTER_OPTIONS = [
  { key: "applications", label: "Applications", match: "applications" },
  { key: "playbooks", label: "Playbooks", match: "playbooks" },
  { key: "scripts", label: "Scripts", match: "scripts" },
  { key: "workflows", label: "Workflows", match: "workflows" },
];
const ASSEMBLY_OS_FILTER_OPTIONS = [
  { key: "windows", label: "Windows", match: "windows" },
  { key: "linux", label: "Linux", match: "linux" },
  { key: "macos", label: "MacOS", match: "macos" },
];

function normalizeAssemblyRow(row) {
  return {
    ...(row || {}),
    domain: (row?.domain || "user").toLowerCase(),
  };
}

function resolveAssemblyMode(row, fallbackMode = "script") {
  const rowKind = String(
    row?.assemblyKind || row?.assembly_type || row?.assemblyType || row?.typeKey || fallbackMode
  ).toLowerCase();
  return rowKind === "ansible" ? "ansible" : "script";
}

function buildAssemblyEditorState(row, fallbackMode = "script") {
  const normalizedRow = normalizeAssemblyRow(row);
  const mode = resolveAssemblyMode(normalizedRow, fallbackMode);
  return {
    mode,
    assemblyGuid: normalizedRow.assemblyGuid || null,
    path: normalizedRow.sourcePath || normalizedRow.relPath || normalizedRow.path || "",
    row: normalizedRow,
    createContext: normalizedRow.createContext || null,
    nonce: Date.now(),
  };
}

function CountSliderGroup({ options, activeKey, counts, onChange }) {
  return (
    <Box
      sx={{
        display: "inline-flex",
        alignItems: "center",
        gap: 0.75,
        background: "linear-gradient(120deg, rgba(8,12,24,0.92), rgba(4,7,17,0.85))",
        borderRadius: 999,
        border: "1px solid rgba(148,163,184,0.35)",
        boxShadow: "0 18px 48px rgba(2,8,23,0.45)",
        padding: "4px",
      }}
    >
      {options.map((option) => {
        const active = activeKey === option.key;
        return (
          <Box
            key={option.key}
            component="button"
            type="button"
            aria-pressed={active}
            onClick={() => onChange(active ? "" : option.key)}
            sx={{
              border: "none",
              outline: "none",
              background: active ? "linear-gradient(135deg,#7dd3fc,#c084fc)" : "transparent",
              color: active ? "#041224" : "#cbd5e1",
              fontWeight: 600,
              fontSize: 13,
              px: 2,
              py: 0.5,
              borderRadius: 999,
              cursor: "pointer",
              display: "inline-flex",
              alignItems: "center",
              gap: 0.6,
              boxShadow: active ? "0 0 18px rgba(125,211,252,0.35)" : "none",
              transition: "all 0.2s ease",
            }}
          >
            <Box component="span" sx={{ userSelect: "none" }}>
              {option.label}
            </Box>
            <Box
              component="span"
              sx={{
                minWidth: 28,
                textAlign: "center",
                borderRadius: 999,
                fontSize: 12,
                fontWeight: 600,
                px: 0.75,
                py: 0.1,
                color: active ? "#041224" : "#94a3b8",
                backgroundColor: active ? "rgba(4,18,36,0.2)" : "rgba(15,23,42,0.65)",
                border: active ? "1px solid rgba(4,18,36,0.3)" : "1px solid rgba(148,163,184,0.3)",
              }}
            >
              {counts?.[option.key] ?? 0}
            </Box>
          </Box>
        );
      })}
    </Box>
  );
}

const SELECT_BASE_SX = {
  "& .MuiOutlinedInput-root": {
    bgcolor: "#1C1C1C",
    color: "#e6edf3",
    borderRadius: 1,
    "& fieldset": { borderColor: "#2b3544" },
    "&:hover fieldset": { borderColor: "#3a4657" },
    "&.Mui-focused fieldset": { borderColor: "#58a6ff" },
  },
  "& .MuiOutlinedInput-input": {
    padding: "9px 12px",
    fontSize: "0.95rem",
    lineHeight: 1.4,
  },
  "& .MuiInputLabel-root": {
    color: "#9ba3b4",
  },
  "& .MuiInputLabel-root.Mui-focused": { color: "#58a6ff" },
};

const MENU_PROPS = {
  PaperProps: {
    sx: {
      background:
        "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), #040711",
      border: "none",
      boxShadow: "none",
      borderRadius: 0,
      color: "#fff",
      fontSize: "13px",
      "& .MuiMenuItem-root.Mui-selected": {
        bgcolor: "rgba(88,166,255,0.16)",
      },
      "& .MuiMenuItem-root.Mui-selected:hover": {
        bgcolor: "rgba(88,166,255,0.24)",
      },
    },
  },
};

const TYPE_METADATA = {
  workflow: {
    label: "Workflow",
    Icon: PolylineIcon,
  },
  script: {
    label: "Script",
    Icon: CodeIcon,
  },
  ansible: {
    label: "Playbook",
    Icon: MenuBookIcon,
  },
};

// Clickable name that opens the corresponding editor, styled in Borealis blue
const NameCellRenderer = React.memo(function NameCellRenderer(props) {
  const { data, context } = props;
  const openRow = context?.openRow;
  if (!data) return null;
  const handleClick = (e) => {
    e.preventDefault();
    openRow?.(data);
  };
  const handleKeyDown = (e) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      openRow?.(data);
    }
  };
  return (
    <MuiLink
      component="button"
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      sx={{
        color: BOREALIS_BLUE,
        textAlign: "left",
        cursor: "pointer",
        p: 0,
        m: 0,
        fontSize: 14,
        textDecoration: "none",
        "&:hover": { textDecoration: "underline" },
      }}
    >
      {data?.name || ""}
    </MuiLink>
  );
});

const SourceCellRenderer = React.memo(function SourceCellRenderer(props) {
  const { data } = props;
  if (!data) return null;
  return (
    <Box sx={{ display: "flex", alignItems: "center", gap: 1.25 }}>
      <DomainBadge domain={data.domain} size="small" />
      {data.isDirty ? <DirtyStatePill compact /> : null}
    </Box>
  );
});

const OfficialUpdateCellRenderer = React.memo(function OfficialUpdateCellRenderer(props) {
  const { data, context } = props;
  if (!data?.officialUpdateAvailable || !context?.canOfficiallyUpdate || !context?.hasCheckedForUpdates) {
    return null;
  }

  const handleClick = (event) => {
    event.preventDefault();
    event.stopPropagation();
    context?.requestOfficialUpdate?.(data);
  };

  return (
    <Box sx={{ display: "flex", alignItems: "center", justifyContent: "center", width: "100%" }}>
      <Button
        size="small"
        variant="outlined"
        onClick={handleClick}
        sx={{
          minWidth: 84,
          px: 1.5,
          borderRadius: 999,
          textTransform: "none",
          fontWeight: 600,
          color: "#7dd3fc",
          borderColor: "rgba(125, 211, 252, 0.42)",
          background: "rgba(10, 23, 45, 0.74)",
          "&:hover": {
            borderColor: "rgba(125, 211, 252, 0.62)",
            background: "rgba(10, 27, 55, 0.92)",
          },
        }}
      >
        Update
      </Button>
    </Box>
  );
});

const PathCellRenderer = React.memo(function PathCellRenderer(props) {
  const { data } = props;
  if (!data) return null;
  const meta = data.typeKey ? TYPE_METADATA[data.typeKey] : null;
  const Icon = meta?.Icon;
  return (
    <Box sx={{ display: "flex", alignItems: "center", gap: 1.1, minWidth: 0 }}>
      {Icon ? <Icon sx={{ fontSize: 19, color: BOREALIS_BLUE, flexShrink: 0 }} /> : null}
      <Typography
        component="span"
        sx={{
          fontSize: 13,
          color: DARKER_GRAY,
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
        }}
      >
        {data.pathDisplay || ""}
      </Typography>
    </Box>
  );
});

const formatPathSegment = (segment) => {
  const value = String(segment || "").trim();
  if (!value) return "";
  return value.charAt(0).toUpperCase() + value.slice(1);
};

const formatPathDisplay = (folderPath) => {
  const segments = String(folderPath || "")
    .split("/")
    .map((segment) => formatPathSegment(segment))
    .filter(Boolean);
  return segments.join(" \u203A ");
};

const buildPathSearchValue = (folderPath) => {
  const rawPath = String(folderPath || "").replace(/\\/g, "/");
  const formattedPath = formatPathDisplay(rawPath);
  return [rawPath, formattedPath]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
};

const buildFilterFlags = (pathSearchValue, options) =>
  options.reduce((acc, option) => {
    acc[option.key] = pathSearchValue.includes(String(option.match || "").toLowerCase());
    return acc;
  }, {});

const toCount = (value) => {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed < 0) return 0;
  return parsed;
};

const getOfficialRevocationLabels = (deletedItems = []) => [...new Set(
  (Array.isArray(deletedItems) ? deletedItems : [])
    .map((item) => String(item?.display_name || item?.name || item?.assembly_guid || "").trim())
    .filter(Boolean)
)];

const buildOfficialRevocationNotifications = (deletedItems = [], deletedCount = 0) => {
  const labels = getOfficialRevocationLabels(deletedItems);
  if (labels.length) {
    return labels.map((label) => `Official Assembly "${label}" removed from Aurora repository. Removing from local database.`);
  }

  const count = toCount(deletedCount || deletedItems?.length);
  if (count <= 0) return [];
  return [`Official Assemblies removed from Aurora repository. Removing ${count} assemblies from local database.`];
};

const normalizeRow = (item, queueEntry) => {
  if (!item) return null;
  const assemblyGuid = item.assembly_guid || item.assembly_id || "";
  const assemblyKind = String(item.assembly_type || "").toLowerCase();
  const assemblyType = String(item.assembly_subtype || "").toLowerCase();
  let typeKey = "script";
  if (assemblyKind === "workflow") {
    typeKey = "workflow";
  } else if (assemblyKind === "ansible") {
    typeKey = "ansible";
  }

  const sourcePath = String(item.virtual_path || item.path || "").replace(/\\/g, "/");
  const pathParts = sourcePath ? sourcePath.split("/") : [];
  const fileName = pathParts.length ? pathParts[pathParts.length - 1] : "";
  const folder = pathParts.length > 1 ? pathParts.slice(0, -1).join("/") : "";

  const domain = String(item.source || "user").toLowerCase();
  const domainMeta = resolveDomainMeta(domain);
  const displayName =
    item.name ||
    item.display_name ||
    fileName.replace(/\.[^.]+$/, "") ||
    fileName ||
    "Assembly";
  const payloadDocument =
    item?.payload_json && typeof item.payload_json === "object"
      ? item.payload_json
      : item?.payload && typeof item.payload === "object"
      ? item.payload
      : null;
  const summary =
    payloadDocument?.description ||
    payloadDocument?.summary ||
    item.description ||
    item.summary ||
    "";
  const queueRecord = queueEntry || null;
  const isDirty = Boolean(item.is_dirty);
  const pathDisplay = formatPathDisplay(folder);
  const pathSearchValue = buildPathSearchValue(folder);
  const assemblyTypeFlags = buildFilterFlags(pathSearchValue, ASSEMBLY_TYPE_FILTER_OPTIONS);
  const targetOsFlags = buildFilterFlags(pathSearchValue, ASSEMBLY_OS_FILTER_OPTIONS);

  return {
    id: assemblyGuid || `${typeKey}:${displayName}`,
    assemblyGuid,
    typeKey,
    assemblyKind,
    assemblyType,
    name: displayName,
    description: summary,
    relPath: sourcePath,
    sourcePath,
    fileName,
    folder,
    pathDisplay,
    pathSearchValue,
    assemblyTypeFlags,
    targetOsFlags,
    domain,
    domainLabel: domainMeta.label,
    isDirty,
    dirtySince: item.dirty_since || queueRecord?.dirty_since || "",
    lastPersisted: item.last_persisted || queueRecord?.last_persisted || "",
    queueEntry: queueRecord,
    payloadGuid: item.payload_guid,
    updatedAt: item.updated_at,
    createdAt: item.created_at,
    officialManaged: Boolean(item.official_managed),
    officialUpdateAvailable: Boolean(item.official_update_available),
    officialRepoUrl: item.official_repo_url || AURORA_REPOSITORY_URL,
    officialSourceUrl: item.official_source_url || item.official_repo_url || AURORA_REPOSITORY_URL,
    officialCatalogSource: item.official_catalog_source || "",
    officialSourceVersion: item.official_source_version || "",
    officialLastAppliedSource: item.official_last_applied_source || "",
    officialLastSyncedAt: item.official_last_synced_at || "",
    raw: item,
  };
};

const formatOfficialUpdateAllMessage = (payload) => {
  const updatedItems = Array.isArray(payload?.updated_items) ? payload.updated_items : [];
  const installedCount = toCount(payload?.installed_count || payload?.installed?.length);
  const updatedExistingCount = toCount(payload?.updated_existing_count);
  const failedCount = Array.isArray(payload?.failed) ? payload.failed.length : 0;

  const fragments = [];
  if (installedCount > 0) {
    fragments.push(`installed ${installedCount} new Aurora ${installedCount === 1 ? "assembly" : "assemblies"}`);
  }
  if (updatedExistingCount > 0) {
    fragments.push(`updated ${updatedExistingCount} Aurora ${updatedExistingCount === 1 ? "assembly" : "assemblies"}`);
  }
  if (!fragments.length && updatedItems.length > 0) {
    fragments.push(`updated ${updatedItems.length} Aurora ${updatedItems.length === 1 ? "assembly" : "assemblies"}`);
  }
  const sentences = [];
  if (fragments.length) {
    const message = fragments.join(" and ");
    sentences.push(`${message.charAt(0).toUpperCase()}${message.slice(1)}.`);
  }
  if (!sentences.length) {
    return failedCount > 0
      ? `Aurora sync completed with ${failedCount} ${failedCount === 1 ? "failure" : "failures"}.`
      : "No Aurora assemblies needed installation or updating.";
  }
  if (failedCount > 0) {
    sentences.push(`${failedCount} ${failedCount === 1 ? "item failed" : "items failed"}.`);
  }
  return sentences.join(" ");
};

export default function AssemblyList() {
  const navigate = useNavigate();
  const { role } = useAuth();
  const gridRef = useRef(null);
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [catalogStatus, setCatalogStatus] = useState({
    repoUrl: AURORA_REPOSITORY_URL,
    source: "bundled",
    available: false,
    updateCount: 0,
    newAssemblyCount: 0,
    metadataRefreshCount: 0,
    actionableCount: 0,
    deletedAssemblyCount: 0,
    error: "",
    warning: "",
    hasChecked: false,
  });

  const [newMenuAnchor, setNewMenuAnchor] = useState(null);
  const [scriptDialog, setScriptDialog] = useState({ open: false, typeKey: null });
  const [scriptName, setScriptName] = useState("");
  const [workflowDialogOpen, setWorkflowDialogOpen] = useState(false);
  const [workflowName, setWorkflowName] = useState("");

  const [contextMenu, setContextMenu] = useState(null);
  const [activeRow, setActiveRow] = useState(null);

  const [renameDialogOpen, setRenameDialogOpen] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [cloneDialog, setCloneDialog] = useState({ open: false, row: null, targetDomain: "user" });
  const [officialUpdateDialog, setOfficialUpdateDialog] = useState({ open: false, row: null, mode: "single" });
  const [updatingOfficialGuid, setUpdatingOfficialGuid] = useState("");
  const [updatingAllOfficial, setUpdatingAllOfficial] = useState(false);
  const [checkingForAuroraUpdates, setCheckingForAuroraUpdates] = useState(false);
  const [assemblyTypeFilterMode, setAssemblyTypeFilterMode] = useState("");
  const [assemblyOsFilterMode, setAssemblyOsFilterMode] = useState("");
  const isAdmin = (role || "User").toLowerCase() === "admin";
  const sendNotification = useAppNotifications({
    title: "Assemblies",
    icon: "apps",
    variant: "info",
  });

  const fetchAssemblies = useCallback(async ({ forceCatalogRefresh = false } = {}) => {
    setLoading(true);
    setError("");
    if (forceCatalogRefresh) {
      setCheckingForAuroraUpdates(true);
    }
    try {
      const url = forceCatalogRefresh ? "/api/assemblies?refresh_catalog=1" : "/api/assemblies";
      const resp = await fetch(url);
      if (!resp.ok) {
        const problem = await resp.text();
        throw new Error(problem || `Failed to load assemblies (HTTP ${resp.status})`);
      }
      const payload = await resp.json();
      const items = Array.isArray(payload?.items) ? payload.items : [];
      const queue = Array.isArray(payload?.queue) ? payload.queue : [];
      const officialCatalog = payload?.official_catalog && typeof payload.official_catalog === "object" ? payload.official_catalog : {};
      const queueMap = queue.reduce((acc, entry) => {
        if (entry && entry.assembly_guid) {
          acc[entry.assembly_guid] = entry;
        }
        return acc;
      }, {});
      const processed = items
        .map((item) => normalizeRow(item, queueMap[item?.assembly_guid || item?.assembly_id || ""]))
        .filter(Boolean);
      const updateCount = toCount(officialCatalog?.update_count);
      const newAssemblyCount = toCount(officialCatalog?.new_assembly_count);
      const metadataRefreshCount = toCount(officialCatalog?.metadata_refresh_count);
      const deletedItems = Array.isArray(officialCatalog?.deleted_items) ? officialCatalog.deleted_items : [];
      const deletedAssemblyCount = toCount(officialCatalog?.deleted_assembly_count);
      const fallbackExistingUpdates = processed.filter((row) => row.officialUpdateAvailable).length;
      const effectiveUpdateCount = updateCount || fallbackExistingUpdates;
      const actionableCount =
        toCount(officialCatalog?.actionable_count) || effectiveUpdateCount + newAssemblyCount + metadataRefreshCount;
      setRows(processed);
      setCatalogStatus((prev) => {
        const hasChecked = forceCatalogRefresh || Boolean(prev?.hasChecked);
        return {
          repoUrl: officialCatalog?.repo_url || AURORA_REPOSITORY_URL,
          source: officialCatalog?.source || "bundled",
          available: hasChecked ? Boolean(officialCatalog?.available) : false,
          updateCount: hasChecked ? effectiveUpdateCount : 0,
          newAssemblyCount: hasChecked ? newAssemblyCount : 0,
          metadataRefreshCount: hasChecked ? metadataRefreshCount : 0,
          actionableCount: hasChecked ? actionableCount : 0,
          deletedAssemblyCount: hasChecked ? deletedAssemblyCount : 0,
          error: hasChecked ? officialCatalog?.error || "" : "",
          warning: hasChecked ? officialCatalog?.warning || "" : "",
          hasChecked,
        };
      });
      if (forceCatalogRefresh && deletedAssemblyCount > 0) {
        for (const message of buildOfficialRevocationNotifications(deletedItems, deletedAssemblyCount)) {
          await sendNotification(message);
        }
      }
    } catch (err) {
      console.error("Failed to load assemblies:", err);
      setRows([]);
      setCatalogStatus((prev) => {
        const hasChecked = forceCatalogRefresh || Boolean(prev?.hasChecked);
        return {
          repoUrl: prev?.repoUrl || AURORA_REPOSITORY_URL,
          source: prev?.source || "bundled",
          available: false,
          updateCount: 0,
          newAssemblyCount: 0,
          metadataRefreshCount: 0,
          actionableCount: 0,
          deletedAssemblyCount: 0,
          error: hasChecked ? err?.message || "Failed to load assemblies" : "",
          warning: "",
          hasChecked,
        };
      });
      setError(err?.message || "Failed to load assemblies");
    } finally {
      if (forceCatalogRefresh) {
        setCheckingForAuroraUpdates(false);
      }
      setLoading(false);
    }
  }, [sendNotification]);

  useEffect(() => {
    fetchAssemblies();
  }, [fetchAssemblies]);

  const openRow = useCallback(
    (row) => {
      if (!row) return;
      if (row.typeKey === "workflow") {
        const workflowGuid = row?.assemblyGuid || null;
        if (workflowGuid) {
          navigate(APP_PATHS.assemblyWorkflow(workflowGuid));
          return;
        }
        const initialName = String(row?.name || "").trim();
        navigate(APP_PATHS.assemblyNewWorkflow, {
          state: initialName ? { workflowSeed: { name: initialName } } : undefined,
        });
        return;
      }
      const assemblyState = buildAssemblyEditorState(row);
      const assemblyGuid = assemblyState.assemblyGuid;
      if (assemblyState.mode === "ansible") {
        if (assemblyGuid) {
          navigate(APP_PATHS.assemblyAnsible(assemblyGuid), {
            state: { initialAssembly: assemblyState },
          });
          return;
        }
        navigate(APP_PATHS.assemblyNewAnsible, { state: { initialAssembly: assemblyState } });
        return;
      }
      if (assemblyGuid) {
        navigate(APP_PATHS.assemblyScript(assemblyGuid), {
          state: { initialAssembly: assemblyState },
        });
        return;
      }
      navigate(APP_PATHS.assemblyNewScript, { state: { initialAssembly: assemblyState } });
    },
    [navigate],
  );

  const handleRowDoubleClicked = useCallback(
    (event) => {
      openRow(event?.data);
    },
    [openRow],
  );

  useEffect(() => {
    const api = gridRef.current?.api;
    if (!api) return undefined;
    const handle = requestAnimationFrame(() => {
      try {
        api.refreshCells({ columns: ["officialUpdate"], force: true });
      } catch {
        /* grid may not be ready during initial mount/unmount */
      }
    });
    return () => cancelAnimationFrame(handle);
  }, [catalogStatus.hasChecked, isAdmin, rows]);

  const handleCellContextMenu = useCallback((params) => {
    params.event?.preventDefault();
    setActiveRow(params?.data || null);
    setContextMenu(
      params?.event
        ? {
            mouseX: params.event.clientX + 2,
            mouseY: params.event.clientY - 6,
          }
        : null,
    );
  }, []);

  const closeContextMenu = () => setContextMenu(null);

  const startRename = () => {
    if (!activeRow) return;
    setRenameValue(activeRow.name || activeRow.fileName || "");
    setRenameDialogOpen(true);
    closeContextMenu();
  };

  const startClone = () => {
    if (!activeRow || !activeRow.assemblyGuid) return;
    const defaultTarget = activeRow.domain === "user" ? "community" : "user";
    setCloneDialog({ open: true, row: activeRow, targetDomain: defaultTarget });
    closeContextMenu();
  };

  const startDelete = () => {
    if (!activeRow) return;
    setDeleteDialogOpen(true);
    closeContextMenu();
  };

  const handleCloneClose = () => setCloneDialog({ open: false, row: null, targetDomain: "user" });

  const handleCloneConfirm = async () => {
    const target = cloneDialog.row;
    if (!target?.assemblyGuid) {
      handleCloneClose();
      return;
    }
    try {
      const resp = await fetch(`/api/assemblies/${encodeURIComponent(target.assemblyGuid)}/clone`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          target_domain: cloneDialog.targetDomain,
        }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) throw new Error(data?.error || data?.message || `HTTP ${resp.status}`);
      handleCloneClose();
      await fetchAssemblies();
    } catch (err) {
      console.error("Failed to clone assembly:", err);
      alert(err?.message || "Failed to clone assembly");
      handleCloneClose();
    }
  };

  const handleRenameSave = async () => {
    const target = activeRow;
    const trimmed = renameValue.trim();
    if (!target || !trimmed || !target.assemblyGuid) {
      setRenameDialogOpen(false);
      return;
    }
    try {
      const resp = await fetch(`/api/assemblies/${encodeURIComponent(target.assemblyGuid)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          display_name: trimmed,
        }),
      });
      let data = null;
      try {
        data = await resp.json();
      } catch {
        data = null;
      }
      if (!resp.ok) throw new Error(data?.error || `HTTP ${resp.status}`);
      setRenameDialogOpen(false);
      await fetchAssemblies();
    } catch (err) {
      console.error("Failed to rename assembly:", err);
      setRenameDialogOpen(false);
    }
  };

  const handleDeleteConfirm = async () => {
    const target = activeRow;
    if (!target || !target.assemblyGuid) {
      setDeleteDialogOpen(false);
      return;
    }
    try {
      const resp = await fetch(`/api/assemblies/${encodeURIComponent(target.assemblyGuid)}`, {
        method: "DELETE",
      });
      let data = null;
      try {
        data = await resp.json();
      } catch {
        data = null;
      }
      if (!resp.ok) throw new Error(data?.error || `HTTP ${resp.status}`);
      setDeleteDialogOpen(false);
      const label = target.name || target.fileName || target.assemblyGuid;
      if (label) {
        sendNotification(`Assembly "${label}" Deleted Successfully`);
      }
      await fetchAssemblies();
    } catch (err) {
      console.error("Failed to delete assembly:", err);
      setDeleteDialogOpen(false);
    }
  };

  const requestOfficialUpdate = useCallback((row) => {
    if (!row?.assemblyGuid) return;
    setOfficialUpdateDialog({ open: true, row, mode: "single" });
  }, []);

  const requestOfficialUpdateAll = useCallback(() => {
    setOfficialUpdateDialog({ open: true, row: null, mode: "all" });
  }, []);

  const handleOfficialUpdateClose = useCallback(() => {
    if (updatingOfficialGuid || updatingAllOfficial) return;
    setOfficialUpdateDialog({ open: false, row: null, mode: "single" });
  }, [updatingAllOfficial, updatingOfficialGuid]);

  const handleOfficialUpdateConfirm = useCallback(async () => {
    if (officialUpdateDialog.mode === "all") {
      try {
        setUpdatingAllOfficial(true);
        const resp = await fetch("/api/assemblies/official/update-all", { method: "POST" });
        const data = await resp.json().catch(() => ({}));
        if (!resp.ok) throw new Error(data?.error || data?.message || `HTTP ${resp.status}`);
        const summaryMessage = formatOfficialUpdateAllMessage(data);
        if (summaryMessage !== "No Aurora assemblies needed installation or updating.") {
          await sendNotification(summaryMessage);
        }
        const deletedItems = Array.isArray(data?.deleted_items) ? data.deleted_items : [];
        const deletedCount = toCount(data?.deleted_count || data?.deleted?.length);
        for (const message of buildOfficialRevocationNotifications(deletedItems, deletedCount)) {
          await sendNotification(message);
        }
        setOfficialUpdateDialog({ open: false, row: null, mode: "single" });
        await fetchAssemblies({ forceCatalogRefresh: true });
      } catch (err) {
        console.error("Failed to update all Aurora assemblies:", err);
        alert(err?.message || "Failed to update Aurora assemblies");
      } finally {
        setUpdatingAllOfficial(false);
      }
      return;
    }

    const target = officialUpdateDialog.row;
    if (!target?.assemblyGuid) {
      setOfficialUpdateDialog({ open: false, row: null, mode: "single" });
      return;
    }

    try {
      setUpdatingOfficialGuid(target.assemblyGuid);
      const resp = await fetch(`/api/assemblies/${encodeURIComponent(target.assemblyGuid)}/official-update`, {
        method: "POST",
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) throw new Error(data?.error || data?.message || `HTTP ${resp.status}`);
      await sendNotification(`Aurora assembly "${target.name || target.assemblyGuid}" updated successfully.`);
      setOfficialUpdateDialog({ open: false, row: null, mode: "single" });
      await fetchAssemblies({ forceCatalogRefresh: true });
    } catch (err) {
      console.error("Failed to update Aurora assembly:", err);
      alert(err?.message || "Failed to update Aurora assembly");
    } finally {
      setUpdatingOfficialGuid("");
    }
  }, [catalogStatus.repoUrl, fetchAssemblies, officialUpdateDialog, sendNotification]);

  const typeScopedRows = useMemo(() => {
    if (!assemblyOsFilterMode) return rows;
    return rows.filter((row) => row?.targetOsFlags?.[assemblyOsFilterMode]);
  }, [assemblyOsFilterMode, rows]);

  const osScopedRows = useMemo(() => {
    if (!assemblyTypeFilterMode) return rows;
    return rows.filter((row) => row?.assemblyTypeFlags?.[assemblyTypeFilterMode]);
  }, [assemblyTypeFilterMode, rows]);

  const assemblyTypeCounts = useMemo(() => {
    const totals = ASSEMBLY_TYPE_FILTER_OPTIONS.reduce((acc, option) => {
      acc[option.key] = 0;
      return acc;
    }, {});
    typeScopedRows.forEach((row) => {
      ASSEMBLY_TYPE_FILTER_OPTIONS.forEach((option) => {
        if (row?.assemblyTypeFlags?.[option.key]) {
          totals[option.key] += 1;
        }
      });
    });
    return totals;
  }, [typeScopedRows]);

  const assemblyOsCounts = useMemo(() => {
    const totals = ASSEMBLY_OS_FILTER_OPTIONS.reduce((acc, option) => {
      acc[option.key] = 0;
      return acc;
    }, {});
    osScopedRows.forEach((row) => {
      ASSEMBLY_OS_FILTER_OPTIONS.forEach((option) => {
        if (row?.targetOsFlags?.[option.key]) {
          totals[option.key] += 1;
        }
      });
    });
    return totals;
  }, [osScopedRows]);

  const filteredRows = useMemo(
    () =>
      rows.filter((row) => {
        if (assemblyTypeFilterMode && !row?.assemblyTypeFlags?.[assemblyTypeFilterMode]) {
          return false;
        }
        if (assemblyOsFilterMode && !row?.targetOsFlags?.[assemblyOsFilterMode]) {
          return false;
        }
        return true;
      }),
    [assemblyOsFilterMode, assemblyTypeFilterMode, rows],
  );

  const activeAssemblyTypeLabel = useMemo(() => {
    const match = ASSEMBLY_TYPE_FILTER_OPTIONS.find((option) => option.key === assemblyTypeFilterMode);
    return match ? match.label : "";
  }, [assemblyTypeFilterMode]);

  const activeAssemblyOsLabel = useMemo(() => {
    const match = ASSEMBLY_OS_FILTER_OPTIONS.find((option) => option.key === assemblyOsFilterMode);
    return match ? match.label : "";
  }, [assemblyOsFilterMode]);

  const filterSummary = useMemo(() => {
    const count = filteredRows.length;
    const assemblyLabel = count === 1 ? "assembly" : "assemblies";
    const activeFilters = [activeAssemblyTypeLabel, activeAssemblyOsLabel].filter(Boolean);
    if (!activeFilters.length) {
      return `Showing ${count} ${assemblyLabel}`;
    }
    return `Showing ${count} ${assemblyLabel} for ${activeFilters.join(" / ")}`;
  }, [activeAssemblyOsLabel, activeAssemblyTypeLabel, filteredRows.length]);

  useEffect(() => {
    const api = gridRef.current?.api;
    if (!api) return;
    api.paginationGoToFirstPage();
  }, [assemblyOsFilterMode, assemblyTypeFilterMode]);

  const gridContext = useMemo(
    () => ({
      openRow,
      requestOfficialUpdate,
      canOfficiallyUpdate: isAdmin,
      hasCheckedForUpdates: catalogStatus.hasChecked,
    }),
    [catalogStatus.hasChecked, isAdmin, openRow, requestOfficialUpdate],
  );
  const compareAssemblyNames = useCallback(
    (valueA, valueB, nodeA, nodeB) => {
      if (catalogStatus.hasChecked) {
        const updateA = Boolean(nodeA?.data?.officialUpdateAvailable);
        const updateB = Boolean(nodeB?.data?.officialUpdateAvailable);
        if (updateA !== updateB) {
          return updateA ? -1 : 1;
        }
      }
      return String(valueA || "").localeCompare(String(valueB || ""), undefined, {
        sensitivity: "base",
        numeric: true,
      });
    },
    [catalogStatus.hasChecked],
  );

  const columnDefs = useMemo(
    () => [
      {
        colId: "source",
        field: "domain",
        headerName: "Source",
        valueGetter: (params) => params?.data?.domain || "",
        valueFormatter: (params) => resolveDomainMeta(params?.value).label,
        filter: "agTextColumnFilter",
        cellRenderer: SourceCellRenderer,
        minWidth: 110,
        width: 110,
        flex: 0,
        sortable: true,
        resizable: true,
      },
      {
        colId: "name",
        field: "name",
        headerName: "Name",
        valueGetter: (params) => params?.data?.name || "",
        cellRenderer: NameCellRenderer,
        minWidth: 450,
        flex: 1,
        sort: "asc",
        sortable: true,
        comparator: compareAssemblyNames,
        filter: "agTextColumnFilter",
        resizable: true,
      },
      {
        colId: "path",
        field: "path",
        headerName: "Path",
        valueGetter: (params) => params?.data?.pathDisplay || "",
        cellRenderer: PathCellRenderer,
        minWidth: 300,
        width: 300,
        flex: 0,
        sortable: true,
        filter: "agTextColumnFilter",
        resizable: true,
      },
      {
        colId: "officialUpdate",
        field: "officialUpdateAvailable",
        headerName: "Update Available",
        minWidth: 160,
        width: 160,
        sortable: false,
        filter: false,
        resizable: true,
        cellRenderer: OfficialUpdateCellRenderer,
      },
    ],
    [compareAssemblyNames],
  );

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      filter: "agTextColumnFilter",
      floatingFilter: false,
      resizable: true,
      flex: 0,
      minWidth: 140,
    }),
    [],
  );

  const gridWrapperClass = themeClassName;
  const newAuroraAssemblies = toCount(catalogStatus.newAssemblyCount);
  const existingAuroraUpdates = toCount(catalogStatus.updateCount);
  const auroraMetadataRefreshes = toCount(catalogStatus.metadataRefreshCount);
  const auroraActionableCount = toCount(catalogStatus.actionableCount);
  const revokedAuroraAssemblies = toCount(catalogStatus.deletedAssemblyCount);
  const hasAuroraActions = catalogStatus.hasChecked && auroraActionableCount > 0;
  const shouldInstallNewAuroraAssemblies = hasAuroraActions && newAuroraAssemblies > 0;
  const handleCheckForAuroraUpdates = useCallback(() => {
    fetchAssemblies({ forceCatalogRefresh: true });
  }, [fetchAssemblies]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "assemblies-aurora-sync",
        label: shouldInstallNewAuroraAssemblies
          ? "Install New Assemblies"
          : hasAuroraActions
          ? "Update All"
          : "Check for Updates",
        icon: hasAuroraActions ? <SyncIcon /> : <CachedIcon />,
        tone: "secondary",
        disabled: hasAuroraActions ? !isAdmin || updatingAllOfficial : checkingForAuroraUpdates,
        tooltip: hasAuroraActions
          ? !isAdmin
            ? shouldInstallNewAuroraAssemblies
              ? "Administrator access is required to install new Aurora assemblies."
              : "Administrator access is required to apply Aurora assembly updates."
            : shouldInstallNewAuroraAssemblies
            ? existingAuroraUpdates > 0 || auroraMetadataRefreshes > 0
              ? "Install new Aurora assemblies and sync existing official assemblies to the latest catalog."
              : "Install new Aurora assemblies from Aurora without restarting the Engine."
            : "Apply all available Aurora assembly updates."
          : catalogStatus.hasChecked
          ? catalogStatus.error
            || catalogStatus.warning
            || (revokedAuroraAssemblies > 0
              ? `Removed ${revokedAuroraAssemblies} revoked Aurora ${revokedAuroraAssemblies === 1 ? "assembly" : "assemblies"} during the last catalog check.`
              : "No new Aurora assemblies or updates are available.")
          : "Check Aurora for available assembly updates.",
        loading: hasAuroraActions ? updatingAllOfficial : checkingForAuroraUpdates,
        onClick: hasAuroraActions ? requestOfficialUpdateAll : handleCheckForAuroraUpdates,
      },
      {
        id: "assemblies-new",
        label: "New Assembly",
        icon: <AddIcon />,
        tone: "primary",
        onClick: (event) => setNewMenuAnchor(event.currentTarget),
      },
    ],
    [
      catalogStatus.error,
      catalogStatus.hasChecked,
      catalogStatus.warning,
      handleCheckForAuroraUpdates,
      hasAuroraActions,
      isAdmin,
      auroraMetadataRefreshes,
      existingAuroraUpdates,
      revokedAuroraAssemblies,
      shouldInstallNewAuroraAssemblies,
      checkingForAuroraUpdates,
      requestOfficialUpdateAll,
      updatingAllOfficial,
    ],
  );

  useRoutePageChrome({
    title: "Assemblies",
    subtitle:
      "Collections of scripts, workflows, and ansible playbooks used to automate tasks across devices. Officially-published assemblies can be found at the [Aurora Assembly Repository](https://github.com/bunny-lab-io/Aurora).",
    Icon: AppsIcon,
    actions: pageHeaderActions,
  });

  const handleNewAssemblyOption = (typeKey) => {
    setNewMenuAnchor(null);
    if (typeKey === "workflow") {
      setWorkflowName("");
      setWorkflowDialogOpen(true);
      return;
    }
    setScriptName("");
    setScriptDialog({ open: true, typeKey });
  };

  const handleCreateScript = () => {
    const trimmed = scriptName.trim();
    if (!trimmed || !scriptDialog.typeKey) return;
    const isAnsible = scriptDialog.typeKey === "ansible";
    const scriptType = isAnsible ? "ansible" : scriptDialog.typeKey || "powershell";
    const context = {
      folder: "",
      suggestedFileName: trimmed,
      name: trimmed,
      defaultType: scriptType,
      type: scriptType,
    };
    const newRow = {
      assemblyGuid: null,
      typeKey: isAnsible ? "ansible" : "script",
      assemblyKind: isAnsible ? "ansible" : "script",
      assemblyType: context.type,
      name: trimmed,
      domain: "user",
      isDirty: false,
      isNew: true,
      createContext: context,
    };
    const assemblyState = buildAssemblyEditorState(newRow);
    if (scriptDialog.typeKey === "ansible") {
      navigate(APP_PATHS.assemblyNewAnsible, { state: { initialAssembly: assemblyState } });
    } else {
      navigate(APP_PATHS.assemblyNewScript, { state: { initialAssembly: assemblyState } });
    }
    setScriptDialog({ open: false, typeKey: null });
    setScriptName("");
  };

  const handleCreateWorkflow = () => {
    const trimmed = workflowName.trim();
    if (!trimmed) return;
    setWorkflowDialogOpen(false);
    const newWorkflow = {
      assemblyGuid: null,
      typeKey: "workflow",
      assemblyKind: "workflow",
      assemblyType: "workflow",
      name: trimmed,
      domain: "user",
      isNew: true,
    };
    navigate(APP_PATHS.assemblyNewWorkflow, {
      state: { workflowSeed: { name: trimmed } },
    });
    setWorkflowName("");
  };

  return (
    <Paper
      sx={{
        m: 0, // Full-bleed to parent container
        p: 0,
        background: "transparent",
        border: "none",
        boxShadow: "none",
        borderRadius: 0,
        fontFamily: gridFontFamily,
        color: "#f5f7fa",
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minWidth: 0,
        height: "100%",
      }}
      elevation={0}
    >
      <Menu
        anchorEl={newMenuAnchor}
        open={Boolean(newMenuAnchor)}
        onClose={() => setNewMenuAnchor(null)}
        PaperProps={MENU_PROPS.PaperProps}
      >
        <MenuItem onClick={() => handleNewAssemblyOption("script")}>Script</MenuItem>
        <MenuItem onClick={() => handleNewAssemblyOption("workflow")}>Workflow</MenuItem>
        <MenuItem onClick={() => handleNewAssemblyOption("ansible")}>Ansible Playbook</MenuItem>
      </Menu>
      <PageBodyFrame
        variant="grid_with_stack"
        stack={(
          <>
            {error ? (
              <Typography variant="body2" sx={{ color: "#ff8a8a" }}>
                {error}
              </Typography>
            ) : null}
            <Box
              sx={{
                display: "flex",
                flexWrap: "wrap",
                alignItems: "center",
                justifyContent: "space-between",
                gap: 1.5,
              }}
            >
              <Box
                sx={{
                  display: "flex",
                  flexWrap: "wrap",
                  alignItems: "flex-start",
                  columnGap: 1,
                  rowGap: 1,
                }}
              >
                <Box sx={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: "8px" }}>
                  <Typography
                    component="span"
                    sx={{
                      color: BOREALIS_BLUE,
                      fontSize: 11,
                      fontWeight: 600,
                      lineHeight: 1.1,
                      pl: 1,
                    }}
                  >
                    Assembly Type
                  </Typography>
                  <CountSliderGroup
                    options={ASSEMBLY_TYPE_FILTER_OPTIONS}
                    activeKey={assemblyTypeFilterMode}
                    counts={assemblyTypeCounts}
                    onChange={setAssemblyTypeFilterMode}
                  />
                </Box>
                <Box sx={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: "8px" }}>
                  <Typography
                    component="span"
                    sx={{
                      color: BOREALIS_BLUE,
                      fontSize: 11,
                      fontWeight: 600,
                      lineHeight: 1.1,
                      pl: 1,
                    }}
                  >
                    Operating System
                  </Typography>
                  <CountSliderGroup
                    options={ASSEMBLY_OS_FILTER_OPTIONS}
                    activeKey={assemblyOsFilterMode}
                    counts={assemblyOsCounts}
                    onChange={setAssemblyOsFilterMode}
                  />
                </Box>
              </Box>
              <Typography variant="body2" sx={{ color: DARKER_GRAY, ml: "auto" }}>
                {filterSummary}
              </Typography>
            </Box>
          </>
        )}
      >
        <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, minHeight: 0 }}>
          <Box
            className={gridWrapperClass}
            sx={{
              width: "100%",
              flexGrow: 1,
              minHeight: 0,
              height: "100%",
              position: "relative",
              fontFamily: gridFontFamily,
              "--ag-font-family": gridFontFamily,
              "--ag-icon-font-family": iconFontFamily,
              "& .ag-root-wrapper": {
                minHeight: "100%",
                border: "none",
                borderRadius: 0,
                background: "transparent",
              },
              "& .ag-root, & .ag-header, & .ag-center-cols-container, & .ag-paging-panel": {
                fontFamily: gridFontFamily,
              },
              "& .ag-icon": {
                fontFamily: iconFontFamily,
              },
              "& .ag-cell": {
                display: "flex",
                alignItems: "center",
                paddingTop: "8px",
                paddingBottom: "8px",
              },
              "& .ag-row-selected": {
                backgroundColor: "rgba(125,211,252,0.2) !important",
                boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
              },
            }}
            style={{
              "--ag-background-color": "#070b1a",
              "--ag-foreground-color": "#f4f7ff",
              "--ag-header-background-color": "#0f172a",
              "--ag-header-foreground-color": "#cfe0ff",
              "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
              "--ag-row-hover-color": "rgba(73,156,196,0.2)",
              "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
              "--ag-border-color": "rgba(125,183,255,0.18)",
              "--ag-row-border-color": "rgba(125,183,255,0.14)",
              "--ag-border-radius": "8px",
            }}
          >
            <AgGridReact
              ref={gridRef}
              rowData={filteredRows}
              columnDefs={columnDefs}
              defaultColDef={defaultColDef}
              context={gridContext}
              rowSelection={{
                mode: "singleRow",
                checkboxes: false,
                headerCheckbox: false,
                enableClickSelection: true,
              }}
              suppressCellFocus
              pagination
              paginationPageSize={PAGE_SIZE}
              paginationPageSizeSelector={[25, 50, 100]}
              animateRows
              onRowDoubleClicked={handleRowDoubleClicked}
              onCellContextMenu={handleCellContextMenu}
              getRowId={(params) =>
                params?.data?.assemblyGuid ||
                params?.data?.id ||
                params?.data?.relPath ||
                params?.data?.fileName ||
                String(params?.rowIndex ?? "")
              }
              theme={myTheme}
              rowHeight={44}
            />
            {loading ? (
              <Box
                sx={{
                  position: "absolute",
                  inset: 0,
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                  bgcolor: "rgba(30, 30, 30, 0.6)",
                  zIndex: 2,
                }}
              >
                <CircularProgress size={32} sx={{ color: BOREALIS_BLUE }} />
              </Box>
            ) : null}
          </Box>
        </Box>
      </PageBodyFrame>

      <Menu
        open={contextMenu !== null}
        onClose={closeContextMenu}
        anchorReference="anchorPosition"
        anchorPosition={contextMenu ? { top: contextMenu.mouseY, left: contextMenu.mouseX } : undefined}
        PaperProps={MENU_PROPS.PaperProps}
      >
        <MenuItem
          onClick={() => {
            closeContextMenu();
            openRow(activeRow);
          }}
        >
          Open
        </MenuItem>
        {activeRow?.assemblyGuid && (isAdmin || activeRow.domain === "user") ? (
          <MenuItem onClick={startClone}>Clone</MenuItem>
        ) : null}
        {catalogStatus.hasChecked && activeRow?.officialUpdateAvailable && isAdmin ? (
          <MenuItem
            onClick={() => {
              closeContextMenu();
              requestOfficialUpdate(activeRow);
            }}
          >
            Update from Aurora
          </MenuItem>
        ) : null}
        <MenuItem onClick={startRename}>Rename</MenuItem>
        <MenuItem sx={{ color: "#ff8a8a" }} onClick={startDelete}>
          Delete
        </MenuItem>
      </Menu>
      <Dialog open={renameDialogOpen} onClose={() => setRenameDialogOpen(false)} maxWidth="xs" fullWidth PaperProps={{ sx: DIALOG_PAPER_SX }}>
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock title="Rename Assembly" />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <TextField
            autoFocus
            fullWidth
            label="New Name"
            variant="outlined"
            value={renameValue}
            onChange={(event) => setRenameValue(event.target.value)}
            sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
          />
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={() => setRenameDialogOpen(false)} sx={DIALOG_BUTTON_SX}>
            Cancel
          </Button>
          <Button onClick={handleRenameSave} disabled={!renameValue.trim()} sx={DIALOG_PRIMARY_BUTTON_SX}>
            Save
          </Button>
        </DialogActions>
      </Dialog>

      <ConfirmDeleteDialog
        open={deleteDialogOpen}
        title="Delete Assembly"
        message="If you delete this assembly, there is no undo. Are you sure you want to proceed?"
        onCancel={() => setDeleteDialogOpen(false)}
        onConfirm={handleDeleteConfirm}
        confirmLabel="Delete Assembly"
      />

      <Dialog open={cloneDialog.open} onClose={handleCloneClose} maxWidth="xs" fullWidth PaperProps={{ sx: DIALOG_PAPER_SX }}>
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock title="Clone Assembly" subtitle="Create a copy of this assembly in another domain." />
        </DialogTitle>
        <DialogContent sx={{ ...DIALOG_CONTENT_SX, minWidth: 280 }}>
          <TextField
            select
            fullWidth
            label="Target Domain"
            value={cloneDialog.targetDomain}
            onChange={(e) =>
              setCloneDialog((prev) => ({
                ...prev,
                targetDomain: String(e.target.value || "").toLowerCase(),
              }))
            }
            sx={{ ...DIALOG_SELECT_SX, mt: 1.25 }}
            SelectProps={{ MenuProps: MENU_PROPS }}
          >
            {DOMAIN_OPTIONS.filter((option) => option.value !== cloneDialog.row?.domain).map((option) => (
              <MenuItem key={option.value} value={option.value}>
                {option.label}
              </MenuItem>
            ))}
          </TextField>
          <Typography variant="body2" sx={{ mt: 1.2, ...DIALOG_BODY_TEXT_SX }}>
            Cloning creates a copy of the assembly in the selected domain.
          </Typography>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={handleCloneClose} sx={DIALOG_BUTTON_SX}>
            Cancel
          </Button>
          <Button onClick={handleCloneConfirm} sx={DIALOG_PRIMARY_BUTTON_SX}>
            Clone
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog open={officialUpdateDialog.open} onClose={handleOfficialUpdateClose} maxWidth="sm" fullWidth PaperProps={{ sx: DIALOG_PAPER_SX }}>
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title={
              officialUpdateDialog.mode === "all"
                ? shouldInstallNewAuroraAssemblies
                  ? "Install New Aurora Assemblies"
                  : "Update All Aurora Assemblies"
                : "Update Aurora Assembly"
            }
            subtitle="Pull the latest published Aurora assembly definitions from GitHub."
          />
        </DialogTitle>
        <DialogContent sx={{ ...DIALOG_CONTENT_SX, minWidth: 420 }}>
          <Typography variant="body2" sx={{ mt: 0.5, color: "#c6d0dd", lineHeight: 1.6 }}>
            {officialUpdateDialog.mode === "all"
              ? shouldInstallNewAuroraAssemblies
                ? existingAuroraUpdates > 0 || auroraMetadataRefreshes > 0
                  ? `This will download and ingest ${newAuroraAssemblies} new Aurora ${
                      newAuroraAssemblies === 1 ? "assembly" : "assemblies"
                    } and sync ${existingAuroraUpdates + auroraMetadataRefreshes} existing official ${
                      existingAuroraUpdates + auroraMetadataRefreshes === 1 ? "assembly" : "assemblies"
                    } in Borealis. Proceed?`
                  : `This will download and ingest ${newAuroraAssemblies} new Aurora ${
                      newAuroraAssemblies === 1 ? "assembly" : "assemblies"
                    } so ${newAuroraAssemblies === 1 ? "it appears" : "they appear"} in the assembly list without restarting the Engine. Proceed?`
                : "This will pull down the most recent version of each Aurora assembly that has an available update from GitHub and overwrite the current Aurora versions in Borealis. Proceed?"
              : "This will pull down the most recent version of this Aurora assembly from GitHub and overwrite the current Aurora version in Borealis. Proceed?"}
          </Typography>
          <Typography variant="body2" sx={{ mt: 1.5, color: "#9ba3b4" }}>
            Repository:
          </Typography>
          <MuiLink
            href={officialUpdateDialog.row?.officialRepoUrl || catalogStatus.repoUrl || AURORA_REPOSITORY_URL}
            target="_blank"
            rel="noopener noreferrer"
            sx={{
              mt: 0.5,
              display: "inline-block",
              color: BOREALIS_BLUE,
              wordBreak: "break-all",
            }}
          >
            {officialUpdateDialog.row?.officialRepoUrl || catalogStatus.repoUrl || AURORA_REPOSITORY_URL}
          </MuiLink>
          {officialUpdateDialog.mode === "single" && officialUpdateDialog.row?.name ? (
            <Typography variant="body2" sx={{ mt: 1.5, color: "#9ba3b4" }}>
              Target: <Box component="span" sx={{ color: "#f5f7fa" }}>{officialUpdateDialog.row.name}</Box>
            </Typography>
          ) : null}
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={handleOfficialUpdateClose} disabled={Boolean(updatingOfficialGuid || updatingAllOfficial)} sx={DIALOG_BUTTON_SX}>
            Cancel
          </Button>
          <Button
            onClick={handleOfficialUpdateConfirm}
            disabled={Boolean(updatingOfficialGuid || updatingAllOfficial)}
            sx={DIALOG_PRIMARY_BUTTON_SX}
          >
            {officialUpdateDialog.mode === "all"
              ? shouldInstallNewAuroraAssemblies
                ? "Install Assemblies"
                : "Update All"
              : "Update"}
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog
        open={scriptDialog.open}
        onClose={() => {
          setScriptDialog({ open: false, typeKey: null });
          setScriptName("");
        }}
        maxWidth="xs"
        fullWidth
        PaperProps={{ sx: DIALOG_PAPER_SX }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title={scriptDialog.typeKey === "ansible" ? "New Ansible Playbook" : "New Script"}
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <TextField
            autoFocus
            fullWidth
            label="Name"
            variant="outlined"
            value={scriptName}
            onChange={(event) => setScriptName(event.target.value)}
            sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
          />
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button
            onClick={() => {
              setScriptDialog({ open: false, typeKey: null });
              setScriptName("");
            }}
            sx={DIALOG_BUTTON_SX}
          >
            Cancel
          </Button>
          <Button onClick={handleCreateScript} disabled={!scriptName.trim()} sx={DIALOG_PRIMARY_BUTTON_SX}>
            Create
          </Button>
        </DialogActions>
      </Dialog>

      <NewWorkflowDialog
        open={workflowDialogOpen}
        value={workflowName}
        onChange={setWorkflowName}
        onCancel={() => {
          setWorkflowDialogOpen(false);
          setWorkflowName("");
        }}
        onCreate={handleCreateWorkflow}
      />
    </Paper>
  );
}
