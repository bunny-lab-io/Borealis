import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  Checkbox,
  Divider,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  Icon,
  IconButton,
  InputBase,
  Menu,
  MenuItem,
  Stack,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import { AgGridReact } from "ag-grid-react";
import AccountTreeRoundedIcon from "@mui/icons-material/AccountTreeRounded";
import AddRoundedIcon from "@mui/icons-material/AddRounded";
import ArticleRoundedIcon from "@mui/icons-material/ArticleRounded";
import AutorenewRoundedIcon from "@mui/icons-material/AutorenewRounded";
import CheckCircleRoundedIcon from "@mui/icons-material/CheckCircleRounded";
import ChevronRightRoundedIcon from "@mui/icons-material/ChevronRightRounded";
import ContentCopyRoundedIcon from "@mui/icons-material/ContentCopyRounded";
import DeleteOutlineRoundedIcon from "@mui/icons-material/DeleteOutlineRounded";
import DriveFileRenameOutlineRoundedIcon from "@mui/icons-material/DriveFileRenameOutlineRounded";
import EditRoundedIcon from "@mui/icons-material/EditRounded";
import RefreshRoundedIcon from "@mui/icons-material/RefreshRounded";
import UnfoldLessRoundedIcon from "@mui/icons-material/UnfoldLessRounded";
import {
  DEFAULT_GRID_COL_DEF,
  DEVICE_DETAILS_GRID_THEME,
  GridShell,
  MAGIC_UI,
} from "./Shared.jsx";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_DANGER_BUTTON_SX,
  DIALOG_INPUT_SX,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../../DialogStyles.jsx";
import { useAppNotifications } from "../../app/hooks/useAppNotifications.js";

const VALUE_TYPES = ["REG_SZ", "REG_EXPAND_SZ", "REG_MULTI_SZ", "REG_DWORD", "REG_QWORD", "REG_BINARY"];
const ROOTS_SENTINEL = "__registry_roots__";

const ICON_BUTTON_SX = {
  width: 42,
  height: 42,
  borderRadius: "50%",
  flexShrink: 0,
  border: "1px solid rgba(148,163,184,0.36)",
  background: "rgba(5,10,24,0.82)",
  color: MAGIC_UI.textBright,
  transition: "background-color 0.18s ease, border-color 0.18s ease, transform 0.18s ease",
  "&:hover": {
    borderColor: "rgba(125,211,252,0.46)",
    background: "rgba(9,16,34,0.94)",
  },
  "&:active": {
    transform: "scale(0.98)",
  },
  "&.Mui-disabled": {
    color: "rgba(148,163,184,0.76)",
    borderColor: "rgba(148,163,184,0.22)",
    background: "rgba(15,23,42,0.42)",
  },
};

const ADDRESS_BAR_SHELL_SX = {
  display: "flex",
  alignItems: "center",
  width: "100%",
  minHeight: 42,
  borderRadius: 2.5,
  px: 1.15,
  gap: 0.45,
  background: "rgba(5,10,24,0.82)",
  border: "1px solid rgba(148,163,184,0.24)",
  transition: "border-color 0.18s ease, box-shadow 0.18s ease, background 0.18s ease",
  "&:hover": {
    borderColor: "rgba(125,211,252,0.4)",
  },
  "&:focus-within": {
    borderColor: "rgba(125,211,252,0.62)",
    boxShadow: "0 0 0 1px rgba(125,211,252,0.12)",
  },
};

const ADDRESS_BAR_INPUT_SX = {
  flex: 1,
  minWidth: 0,
  color: MAGIC_UI.textBright,
  "& input": {
    py: 1.05,
    fontSize: "0.92rem",
  },
  "& input::placeholder": {
    color: "rgba(148,163,184,0.84)",
    opacity: 1,
  },
};

const ADDRESS_BAR_SEGMENT_BUTTON_SX = {
  display: "inline-flex",
  alignItems: "center",
  minWidth: 0,
  border: 0,
  background: "transparent",
  color: MAGIC_UI.textBright,
  cursor: "pointer",
  px: 0.7,
  py: 0.35,
  m: 0,
  font: "inherit",
  borderRadius: 1.1,
  transition: "background-color 0.16s ease, color 0.16s ease",
  "&:hover": {
    background: "rgba(88,166,255,0.16)",
    color: "#9bd7ff",
  },
};

const ADDRESS_BAR_ROOT_BUTTON_SX = {
  ...ADDRESS_BAR_SEGMENT_BUTTON_SX,
  gap: 0.3,
  flexShrink: 0,
};

const ADDRESS_BAR_EMPTY_SPACE_BUTTON_SX = {
  flex: 1,
  minWidth: 28,
  alignSelf: "stretch",
  border: 0,
  background: "transparent",
  cursor: "text",
  p: 0,
  m: 0,
};

const ADDRESS_BAR_COPY_BUTTON_SX = {
  color: "rgba(148,163,184,0.92)",
  ml: 0.25,
  "&:hover": {
    color: "#d9ecff",
    background: "rgba(125,211,252,0.12)",
  },
  "&.Mui-disabled": {
    color: "rgba(148,163,184,0.38)",
  },
};

const GRID_CELL_TEXT_SX = {
  display: "block",
  width: "100%",
  color: "#cbd5e1",
  fontSize: "0.86rem",
  fontWeight: 400,
  lineHeight: 1.25,
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

const GRID_MUTED_CELL_SX = {
  ...GRID_CELL_TEXT_SX,
  color: "rgba(148,163,184,0.86)",
};

const LOADING_STATUS_PILL_SX = {
  display: "inline-flex",
  alignItems: "center",
  gap: 0.45,
  flexShrink: 0,
  px: 0.75,
  py: 0.25,
  ml: 0.25,
  borderRadius: 999,
  border: "1px solid rgba(125,211,252,0.26)",
  background: "rgba(14,116,144,0.18)",
  boxShadow: "inset 0 1px 0 rgba(255,255,255,0.05)",
};

const ACTION_MENU_PAPER_SX = {
  bgcolor: "rgba(8,12,24,0.96)",
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  backdropFilter: "blur(14px)",
  borderRadius: 2,
  minWidth: 288,
  px: 0.8,
  py: 0.8,
};

const ACTION_MENU_ITEM_SX = {
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
    transition: "background-color 0.16s ease",
  },
  "&:hover::before": {
    background: "#58a6ff",
  },
};

const ACTION_MENU_DANGER_ITEM_SX = {
  ...ACTION_MENU_ITEM_SX,
  "&:hover": {
    backgroundColor: "rgba(248,113,113,0.1)",
  },
  "&:hover::before": {
    background: "#58a6ff",
  },
};

const ACTION_MENU_SECTION_LABEL_SX = {
  px: 1.2,
  pt: 0.65,
  pb: 0.45,
  color: "rgba(148,163,184,0.72)",
  fontSize: "0.68rem",
  fontWeight: 700,
  letterSpacing: "0.08em",
  textTransform: "uppercase",
};

const ACTION_MENU_DIVIDER_SX = {
  my: 0.55,
  borderColor: "rgba(148,163,184,0.16)",
};

const ACTION_MENU_HEADER_SX = {
  display: "flex",
  alignItems: "center",
  gap: 1,
  px: 1.1,
  pt: 0.55,
  pb: 0.85,
};

const ACTION_MENU_HEADER_ICON_SX = {
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

const ACTION_MENU_ROW_ICON_SX = {
  mt: 0.18,
  mr: 1,
  fontSize: 18,
  flexShrink: 0,
};

const ACTION_MENU_LABEL_SX = {
  color: MAGIC_UI.textBright,
  fontSize: "0.84rem",
  fontWeight: 500,
  lineHeight: 1.2,
};

const ACTION_MENU_DESCRIPTION_SX = {
  color: "rgba(148,163,184,0.78)",
  fontSize: "0.73rem",
  lineHeight: 1.25,
  mt: 0.25,
};

const ACTION_MENU_TITLE_TRUNCATE_SX = {
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

const REGISTRY_GRID_SX = {
  flexGrow: 1,
  minHeight: 420,
  "--ag-row-hover-color": "rgba(73,156,196,0.2)",
  "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
  "& .ag-cell": {
    display: "flex",
    alignItems: "center",
    textAlign: "left",
  },
  "& .ag-row-hover": {
    backgroundColor: "rgba(73,156,196,0.2) !important",
  },
  "& .ag-row-selected": {
    backgroundColor: "rgba(125,211,252,0.2) !important",
    boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
  },
};

function normalizeText(value) {
  if (value == null) return "";
  return String(value).trim();
}

function getHostname(device) {
  return normalizeText(
    device?.hostname ||
      device?.Hostname ||
      device?.agent_hostname ||
      device?.agent?.hostname ||
      device?.name
  );
}

export function normalizeRegistryPath(value) {
  return normalizeText(value)
    .replace(/\//g, "\\")
    .replace(/^\\+|\\+$/g, "")
    .replace(/\\{2,}/g, "\\");
}

function formatTimestamp(value) {
  const numeric = Number(value || 0);
  if (!Number.isFinite(numeric) || numeric <= 0) return "";
  try {
    return new Date(numeric * 1000).toLocaleString();
  } catch {
    return "";
  }
}

export function normalizeKeyEntry(row) {
  const path = normalizeRegistryPath(row?.path);
  const name = normalizeText(row?.name || row?.short_name || path);
  return {
    ...row,
    id: `key:${path}`,
    row_type: "key",
    path,
    parent_path: normalizeRegistryPath(row?.parent_path),
    name,
    display_name: name,
    kind: normalizeText(row?.kind || "key"),
    type_label: normalizeText(row?.kind || "key").toUpperCase(),
    data_label: "",
    modified_label: formatTimestamp(row?.modified_at),
    editable: row?.editable !== false && normalizeText(row?.kind).toLowerCase() !== "hive",
  };
}

export function normalizeValueEntry(row, currentPath) {
  const name = normalizeText(row?.name);
  const type = normalizeText(row?.type || "REG_UNKNOWN").toUpperCase();
  const displayData = normalizeText(row?.display_data);
  const data = row?.data;
  return {
    ...row,
    id: `value:${normalizeRegistryPath(currentPath)}:${name}`,
    row_type: "value",
    path: normalizeRegistryPath(currentPath),
    parent_path: normalizeRegistryPath(currentPath),
    name,
    display_name: normalizeText(row?.display_name) || (name ? name : "(Default)"),
    kind: "value",
    type,
    type_label: type,
    data,
    data_label: displayData || (Array.isArray(data) ? data.join("; ") : normalizeText(data)),
    modified_label: "",
    editable: row?.editable !== false,
  };
}

function isRegistryKey(entry) {
  return normalizeText(entry?.row_type).toLowerCase() === "key";
}

function isGridInteractiveClick(event) {
  const target = event?.target;
  if (!target?.closest) return false;
  return Boolean(
    target.closest(
      "button, input, a, textarea, select, option, label, .ag-selection-checkbox, .ag-checkbox-input-wrapper, .ag-checkbox-input"
    )
  );
}

export function buildRegistryAddressSegments(pathValue) {
  const normalized = normalizeRegistryPath(pathValue);
  if (!normalized) return [];
  const segments = [];
  let currentPath = "";
  normalized
    .split("\\")
    .filter(Boolean)
    .forEach((segment) => {
      currentPath = currentPath ? `${currentPath}\\${segment}` : segment;
      segments.push({ label: segment, path: currentPath });
    });
  return segments;
}

export function buildRegistryPathChain(pathValue) {
  return buildRegistryAddressSegments(pathValue).map((segment) => segment.path);
}

function getRegistryPathLeafLabel(pathValue) {
  const normalized = normalizeRegistryPath(pathValue);
  if (!normalized) return "Registry Roots";
  const parts = normalized.split("\\").filter(Boolean);
  return parts[parts.length - 1] || normalized;
}

function compareRegistryRows(left, right, columnId) {
  const leftIsKey = isRegistryKey(left);
  const rightIsKey = isRegistryKey(right);
  if (leftIsKey !== rightIsKey) {
    return leftIsKey ? -1 : 1;
  }
  return normalizeText(left?.[columnId]).localeCompare(normalizeText(right?.[columnId]), undefined, {
    sensitivity: "base",
    numeric: true,
  });
}

function sortRegistryRows(entries, sortModel = []) {
  const activeSorts = Array.isArray(sortModel) && sortModel.length ? sortModel : [{ colId: "display_name", sort: "asc" }];
  return [...entries].sort((left, right) => {
    for (const descriptor of activeSorts) {
      const columnId = normalizeText(descriptor?.colId) || "display_name";
      const direction = normalizeText(descriptor?.sort).toLowerCase() === "desc" ? -1 : 1;
      const comparison = compareRegistryRows(left, right, columnId);
      if (comparison !== 0) return comparison * direction;
    }
    return compareRegistryRows(left, right, "display_name");
  });
}

export function buildVisibleRows(entriesByParent, expandedPaths, sortModel) {
  const rows = [];
  let sortIndex = 0;

  function visit(parentKey, depth) {
    const children = sortRegistryRows(entriesByParent[parentKey] || [], sortModel);
    children.forEach((entry) => {
      rows.push({ ...entry, depth, sortIndex });
      sortIndex += 1;
      if (isRegistryKey(entry) && expandedPaths.has(entry.path)) {
        visit(entry.path, depth + 1);
      }
    });
  }

  visit(ROOTS_SENTINEL, 0);
  return rows;
}

export function collapseExpandedBranch(expandedSet, entriesByParent, branchPath) {
  const next = new Set(expandedSet);
  const stack = [normalizeRegistryPath(branchPath)];
  while (stack.length) {
    const current = stack.pop();
    if (!current) continue;
    next.delete(current);
    (entriesByParent[current] || []).forEach((entry) => {
      if (isRegistryKey(entry) && normalizeRegistryPath(entry.path)) {
        stack.push(entry.path);
      }
    });
  }
  return next;
}

export function valueDataToEditor(row) {
  const data = row?.data;
  if (Array.isArray(data)) return data.join("\n");
  return data == null ? "" : String(data);
}

async function responseJson(response) {
  return response.json().catch(() => ({}));
}

export default function RemoteRegistryEditor({ device }) {
  const notifyOperator = useAppNotifications();
  const [searchParams, setSearchParams] = useSearchParams();
  const gridRef = useRef(null);
  const pathInputRef = useRef(null);
  const loadSuccessTimersRef = useRef({});
  const hydratedRegistryPathRef = useRef("");
  const pendingScrollPathRef = useRef("");
  const hostname = useMemo(() => getHostname(device), [device]);
  const requestedPath = useMemo(() => normalizeRegistryPath(searchParams.get("registry_path")), [searchParams]);

  const [currentPath, setCurrentPath] = useState("");
  const [pathInput, setPathInput] = useState("");
  const [isPathEditing, setIsPathEditing] = useState(false);
  const [entriesByParent, setEntriesByParent] = useState({});
  const [expandedPaths, setExpandedPaths] = useState(() => new Set());
  const [selectedRows, setSelectedRows] = useState([]);
  const [sortModel, setSortModel] = useState([]);
  const [loadingPaths, setLoadingPaths] = useState(() => new Set());
  const [initializing, setInitializing] = useState(true);
  const [rowLoadStateByPath, setRowLoadStateByPath] = useState({});
  const [error, setError] = useState("");
  const [contextMenuState, setContextMenuState] = useState(null);
  const [keyDialog, setKeyDialog] = useState({ open: false, mode: "create", row: null, parentPath: "", name: "" });
  const [valueDialog, setValueDialog] = useState({
    open: false,
    mode: "create",
    row: null,
    path: "",
    name: "",
    type: "REG_SZ",
    data: "",
  });
  const [deleteDialog, setDeleteDialog] = useState({
    open: false,
    row: null,
    confirmPath: "",
    recursive: false,
  });
  const [actionBusy, setActionBusy] = useState("");

  const selectedRow = selectedRows.length === 1 ? selectedRows[0] : null;
  const selectedKey = selectedRow?.row_type === "key" ? selectedRow : null;
  const selectedValue = selectedRow?.row_type === "value" ? selectedRow : null;
  const registryMutationTargetPath = useMemo(() => {
    if (selectedRows.length === 1) {
      if (isRegistryKey(selectedRows[0])) return normalizeRegistryPath(selectedRows[0].path);
      if (selectedRows[0]?.row_type === "value") return normalizeRegistryPath(selectedRows[0].path);
    }
    return normalizeRegistryPath(currentPath);
  }, [currentPath, selectedRows]);
  const canMutateCurrentKey = Boolean(registryMutationTargetPath);
  const canRenameKey = Boolean(selectedKey?.editable);
  const canDeleteKey = Boolean(selectedKey?.editable);
  const canEditValue = Boolean(selectedValue?.editable);
  const loading = initializing || loadingPaths.size > 0;
  const visibleRows = useMemo(
    () => buildVisibleRows(entriesByParent, expandedPaths, sortModel).map((row) => ({ ...row, id: row.id || `${row.row_type}:${row.path}:${row.name}` })),
    [entriesByParent, expandedPaths, sortModel]
  );
  const addressBarSegments = useMemo(() => buildRegistryAddressSegments(currentPath), [currentPath]);

  const fetchRootsView = useCallback(async () => {
    const response = await fetch(`/api/device/registry/${encodeURIComponent(hostname)}/roots`, {
      credentials: "include",
    });
    const data = await responseJson(response);
    if (!response.ok) {
      throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
    }
    return {
      entries: Array.isArray(data?.entries) ? data.entries.map((row) => normalizeKeyEntry(row)) : [],
    };
  }, [hostname]);

  const fetchChildrenView = useCallback(
    async (pathValue) => {
      const normalizedPath = normalizeRegistryPath(pathValue);
      const response = await fetch(
        `/api/device/registry/${encodeURIComponent(hostname)}/children?path=${encodeURIComponent(normalizedPath)}`,
        { credentials: "include" }
      );
      const data = await responseJson(response);
      if (!response.ok) {
        throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
      }
      const resolvedPath = normalizeRegistryPath(data?.current_path || normalizedPath);
      const keyRows = Array.isArray(data?.entries) ? data.entries.map((row) => normalizeKeyEntry(row)) : [];
      const valueRows = Array.isArray(data?.values) ? data.values.map((row) => normalizeValueEntry(row, resolvedPath)) : [];
      return {
        currentPath: resolvedPath,
        entries: [...keyRows, ...valueRows],
      };
    },
    [hostname]
  );

  const restoreRegistryView = useCallback(
    async ({ targetPath = "", resetSelection = true, scrollToPath = false } = {}) => {
      if (!hostname) return;
      const normalizedTarget = normalizeRegistryPath(targetPath);
      if (resetSelection) {
        setSelectedRows([]);
        gridRef.current?.api?.deselectAll?.();
      }
      setError("");
      setLoadingPaths((previous) => {
        const next = new Set(previous);
        next.add(ROOTS_SENTINEL);
        return next;
      });
      try {
        const rootPayload = await fetchRootsView();
        const nextEntriesByParent = {
          [ROOTS_SENTINEL]: Array.isArray(rootPayload?.entries) ? rootPayload.entries : [],
        };
        const nextExpandedPaths = new Set();
        let resolvedPath = "";

        setEntriesByParent({ ...nextEntriesByParent });
        setExpandedPaths(new Set());

        for (const expandedPath of buildRegistryPathChain(normalizedTarget)) {
          const childPayload = await fetchChildrenView(expandedPath);
          const childPath = normalizeRegistryPath(childPayload?.currentPath || expandedPath);
          nextEntriesByParent[childPath] = Array.isArray(childPayload?.entries) ? childPayload.entries : [];
          nextExpandedPaths.add(childPath);
          resolvedPath = childPath;
        }

        setEntriesByParent({ ...nextEntriesByParent });
        setExpandedPaths(nextExpandedPaths);
        setCurrentPath(resolvedPath);
        setPathInput(resolvedPath);
        setIsPathEditing(false);
        if (scrollToPath && resolvedPath) {
          pendingScrollPathRef.current = resolvedPath;
        }
        setError("");
      } catch (loadError) {
        setError(loadError?.message || "Registry request failed.");
      } finally {
        setLoadingPaths((previous) => {
          const next = new Set(previous);
          next.delete(ROOTS_SENTINEL);
          return next;
        });
      }
    },
    [fetchChildrenView, fetchRootsView, hostname]
  );

  useEffect(() => {
    if (!isPathEditing) return undefined;
    const timerId = window.setTimeout(() => {
      pathInputRef.current?.focus?.();
      pathInputRef.current?.select?.();
    }, 0);
    return () => window.clearTimeout(timerId);
  }, [isPathEditing]);

  useEffect(() => {
    return () => {
      Object.values(loadSuccessTimersRef.current || {}).forEach((timerId) => {
        window.clearTimeout(timerId);
      });
    };
  }, []);

  useEffect(() => {
    const targetPath = normalizeRegistryPath(pendingScrollPathRef.current);
    if (!targetPath || !visibleRows.length) {
      return undefined;
    }
    const rowIndex = visibleRows.findIndex((row) => isRegistryKey(row) && normalizeRegistryPath(row?.path) === targetPath);
    if (rowIndex < 0) {
      return undefined;
    }
    pendingScrollPathRef.current = "";
    const animationFrameId = window.requestAnimationFrame(() => {
      gridRef.current?.api?.ensureIndexVisible?.(rowIndex, "top");
    });
    return () => window.cancelAnimationFrame(animationFrameId);
  }, [visibleRows]);

  useEffect(() => {
    if (initializing) return;
    const serializedPath = normalizeRegistryPath(currentPath);
    const currentValue = normalizeRegistryPath(searchParams.get("registry_path"));
    if ((serializedPath && currentValue === serializedPath) || (!serializedPath && !currentValue)) {
      return;
    }
    const nextParams = new URLSearchParams(searchParams);
    nextParams.delete("working_directory");
    if (serializedPath) {
      nextParams.set("registry_path", serializedPath);
    } else {
      nextParams.delete("registry_path");
    }
    hydratedRegistryPathRef.current = `${hostname}::${serializedPath}`;
    setSearchParams(nextParams, { replace: true });
  }, [currentPath, hostname, initializing, searchParams, setSearchParams]);

  useEffect(() => {
    let canceled = false;

    async function loadInitial() {
      if (!hostname) {
        setInitializing(false);
        return;
      }
      setInitializing(true);
      try {
        const hydrateKey = `${hostname}::${requestedPath}`;
        if (hydratedRegistryPathRef.current === hydrateKey) {
          setInitializing(false);
          return;
        }
        await restoreRegistryView({ targetPath: requestedPath, resetSelection: false, scrollToPath: true });
        hydratedRegistryPathRef.current = hydrateKey;
      } finally {
        if (!canceled) {
          setInitializing(false);
        }
      }
    }

    void loadInitial();
    return () => {
      canceled = true;
    };
  }, [hostname, requestedPath, restoreRegistryView]);

  const navigateToPath = useCallback(
    async (pathValue) => {
      await restoreRegistryView({ targetPath: normalizeRegistryPath(pathValue), resetSelection: true, scrollToPath: true });
    },
    [restoreRegistryView]
  );

  const loadChildrenForPath = useCallback(
    async (pathValue) => {
      const normalizedPath = normalizeRegistryPath(pathValue);
      if (!hostname || !normalizedPath) return;
      if (loadSuccessTimersRef.current[normalizedPath]) {
        window.clearTimeout(loadSuccessTimersRef.current[normalizedPath]);
        delete loadSuccessTimersRef.current[normalizedPath];
      }
      setRowLoadStateByPath((previous) => ({ ...previous, [normalizedPath]: "loading" }));
      setLoadingPaths((previous) => {
        const next = new Set(previous);
        next.add(normalizedPath);
        return next;
      });
      try {
        const data = await fetchChildrenView(normalizedPath);
        const resolvedPath = normalizeRegistryPath(data?.currentPath || normalizedPath);
        setEntriesByParent((previous) => ({
          ...previous,
          [resolvedPath]: Array.isArray(data?.entries) ? data.entries : [],
        }));
        setRowLoadStateByPath((previous) => {
          const next = { ...previous, [resolvedPath]: "loaded" };
          if (resolvedPath !== normalizedPath) {
            delete next[normalizedPath];
          }
          return next;
        });
        loadSuccessTimersRef.current[resolvedPath] = window.setTimeout(() => {
          setRowLoadStateByPath((previous) => {
            const next = { ...previous };
            delete next[resolvedPath];
            return next;
          });
          delete loadSuccessTimersRef.current[resolvedPath];
        }, 1000);
      } catch (loadError) {
        setRowLoadStateByPath((previous) => {
          const next = { ...previous };
          delete next[normalizedPath];
          return next;
        });
        throw loadError;
      } finally {
        setLoadingPaths((previous) => {
          const next = new Set(previous);
          next.delete(normalizedPath);
          next.delete(normalizeRegistryPath(pathValue));
          return next;
        });
      }
    },
    [fetchChildrenView, hostname]
  );

  const toggleExpand = useCallback(
    async (row) => {
      if (!row || !isRegistryKey(row)) return;
      const pathValue = normalizeRegistryPath(row.path);
      if (!pathValue) return;
      if (expandedPaths.has(pathValue)) {
        setExpandedPaths((previous) => collapseExpandedBranch(previous, entriesByParent, pathValue));
        const parentPath = normalizeRegistryPath(row.parent_path);
        setCurrentPath(parentPath);
        setPathInput(parentPath);
        return;
      }
      if (!entriesByParent[pathValue]) {
        try {
          await loadChildrenForPath(pathValue);
        } catch (expandError) {
          setError(String(expandError?.message || expandError));
          return;
        }
      }
      setExpandedPaths((previous) => {
        let next = new Set(previous);
        const siblingParentKey = normalizeRegistryPath(row.parent_path) || ROOTS_SENTINEL;
        (entriesByParent[siblingParentKey] || []).forEach((sibling) => {
          const siblingPath = normalizeRegistryPath(sibling?.path);
          if (!siblingPath || siblingPath === pathValue || !isRegistryKey(sibling) || !next.has(siblingPath)) return;
          next = collapseExpandedBranch(next, entriesByParent, siblingPath);
        });
        next.add(pathValue);
        return next;
      });
      setCurrentPath(pathValue);
      setPathInput(pathValue);
      setIsPathEditing(false);
      setError("");
    },
    [entriesByParent, expandedPaths, loadChildrenForPath]
  );

  const handleEnablePathEditing = useCallback(() => {
    setPathInput(currentPath);
    setIsPathEditing(true);
  }, [currentPath]);

  const handleCancelPathEditing = useCallback(() => {
    setPathInput(currentPath);
    setIsPathEditing(false);
  }, [currentPath]);

  const handleAddressBarNavigate = useCallback(async () => {
    const targetPath = normalizeRegistryPath(pathInput);
    await navigateToPath(targetPath);
    setPathInput(targetPath);
    setIsPathEditing(false);
  }, [navigateToPath, pathInput]);

  const handleCloseContextMenu = useCallback(() => {
    setContextMenuState(null);
  }, []);

  const handleOpenContextMenuAtPointer = useCallback((event) => {
    event.preventDefault?.();
    event.stopPropagation?.();
    setContextMenuState({
      top: Number(event.clientY || 0),
      left: Number(event.clientX || 0),
    });
  }, []);

  const handleGridCellContextMenu = useCallback(
    (params) => {
      const mouseEvent = params?.event;
      if (!mouseEvent) return;
      const node = params?.node;
      if (node && !node.isSelected?.()) {
        gridRef.current?.api?.deselectAll?.();
        node.setSelected?.(true, true);
      }
      setSelectedRows(gridRef.current?.api?.getSelectedRows?.() || []);
      handleOpenContextMenuAtPointer(mouseEvent);
    },
    [handleOpenContextMenuAtPointer]
  );

  const handleGridShellContextMenu = useCallback(
    (event) => {
      if (event?.target?.closest?.(".ag-cell")) return;
      gridRef.current?.api?.deselectAll?.();
      setSelectedRows([]);
      handleOpenContextMenuAtPointer(event);
    },
    [handleOpenContextMenuAtPointer]
  );

  const requestRegistryMutation = useCallback(
    async (suffix, body) => {
      const response = await fetch(`/api/device/registry/${encodeURIComponent(hostname)}/${suffix}`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body || {}),
      });
      const data = await responseJson(response);
      if (!response.ok || data?.ok === false) {
        throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
      }
      return data;
    },
    [hostname]
  );

  const refreshCurrent = useCallback(() => {
    void restoreRegistryView({ targetPath: currentPath, resetSelection: true, scrollToPath: true });
  }, [currentPath, restoreRegistryView]);

  const handleCopyPath = useCallback(async () => {
    const pathValue = normalizeRegistryPath(currentPath);
    if (!pathValue) return;
    try {
      await navigator.clipboard?.writeText(pathValue);
      await notifyOperator({ title: "Registry", message: "Registry path copied.", variant: "success", icon: "content_copy" });
    } catch {
      await notifyOperator({ title: "Registry", message: "Clipboard copy failed.", variant: "warning", icon: "content_copy" });
    }
  }, [currentPath, notifyOperator]);

  const openCreateKeyDialog = useCallback(() => {
    setKeyDialog({ open: true, mode: "create", row: null, parentPath: registryMutationTargetPath, name: "" });
  }, [registryMutationTargetPath]);

  const openRenameKeyDialog = useCallback(() => {
    if (!selectedKey) return;
    setKeyDialog({ open: true, mode: "rename", row: selectedKey, parentPath: normalizeRegistryPath(selectedKey.parent_path), name: normalizeText(selectedKey.name) });
  }, [selectedKey]);

  const openCreateValueDialog = useCallback(() => {
    setValueDialog({ open: true, mode: "create", row: null, path: registryMutationTargetPath, name: "", type: "REG_SZ", data: "" });
  }, [registryMutationTargetPath]);

  const openEditValueDialog = useCallback(() => {
    if (!selectedValue) return;
    setValueDialog({
      open: true,
      mode: "update",
      row: selectedValue,
      path: normalizeRegistryPath(selectedValue.path),
      name: normalizeText(selectedValue.name),
      type: normalizeText(selectedValue.type || "REG_SZ").toUpperCase(),
      data: valueDataToEditor(selectedValue),
    });
  }, [selectedValue]);

  const openDeleteDialog = useCallback(() => {
    if (!selectedRow) return;
    setDeleteDialog({
      open: true,
      row: selectedRow,
      confirmPath: selectedRow.row_type === "key" ? "" : normalizeRegistryPath(selectedRow.path),
      recursive: false,
    });
  }, [selectedRow]);

  const handleSaveKey = useCallback(async () => {
    const name = normalizeText(keyDialog.name);
    if (!name) {
      setError("Registry key name is required.");
      return;
    }
    const isRename = keyDialog.mode === "rename";
    setActionBusy(isRename ? "rename-key" : "create-key");
    try {
      await requestRegistryMutation(isRename ? "key/rename" : "key/create", {
        path: normalizeRegistryPath(keyDialog.row?.path),
        parent_path: normalizeRegistryPath(keyDialog.parentPath || currentPath),
        new_name: name,
        name,
      });
      setKeyDialog({ open: false, mode: "create", row: null, parentPath: "", name: "" });
      await notifyOperator({
        title: "Registry",
        message: isRename ? "Registry key renamed." : "Registry key created.",
        variant: "success",
        icon: "account_tree",
      });
      await restoreRegistryView({
        targetPath: isRename ? normalizeRegistryPath(keyDialog.row?.parent_path) : normalizeRegistryPath(keyDialog.parentPath || currentPath),
        resetSelection: true,
        scrollToPath: true,
      });
    } catch (saveError) {
      setError(saveError?.message || "Registry key action failed.");
    } finally {
      setActionBusy("");
    }
  }, [currentPath, keyDialog, notifyOperator, requestRegistryMutation, restoreRegistryView]);

  const handleSaveValue = useCallback(async () => {
    const isUpdate = valueDialog.mode === "update";
    setActionBusy(isUpdate ? "update-value" : "create-value");
    try {
      await requestRegistryMutation(isUpdate ? "value/update" : "value/create", {
        path: normalizeRegistryPath(isUpdate ? valueDialog.row?.path : valueDialog.path || currentPath),
        name: valueDialog.name,
        type: valueDialog.type,
        data: valueDialog.data,
      });
      setValueDialog({ open: false, mode: "create", row: null, path: "", name: "", type: "REG_SZ", data: "" });
      await notifyOperator({
        title: "Registry",
        message: isUpdate ? "Registry value updated." : "Registry value created.",
        variant: "success",
        icon: "article",
      });
      await restoreRegistryView({
        targetPath: normalizeRegistryPath(isUpdate ? valueDialog.row?.path : valueDialog.path || currentPath),
        resetSelection: true,
        scrollToPath: true,
      });
    } catch (saveError) {
      setError(saveError?.message || "Registry value action failed.");
    } finally {
      setActionBusy("");
    }
  }, [currentPath, notifyOperator, requestRegistryMutation, restoreRegistryView, valueDialog]);

  const handleDelete = useCallback(async () => {
    const row = deleteDialog.row;
    if (!row) return;
    setActionBusy(row.row_type === "key" ? "delete-key" : "delete-value");
    try {
      if (row.row_type === "key") {
        await requestRegistryMutation("key/delete", {
          path: normalizeRegistryPath(row.path),
          confirm_path: normalizeRegistryPath(deleteDialog.confirmPath),
          recursive: Boolean(deleteDialog.recursive),
        });
        await notifyOperator({ title: "Registry", message: "Registry key deleted.", variant: "success", icon: "delete" });
      } else {
        await requestRegistryMutation("value/delete", {
          path: normalizeRegistryPath(row.path),
          name: row.name,
        });
        await notifyOperator({ title: "Registry", message: "Registry value deleted.", variant: "success", icon: "delete" });
      }
      setDeleteDialog({ open: false, row: null, confirmPath: "", recursive: false });
      await restoreRegistryView({
        targetPath: row.row_type === "key" ? normalizeRegistryPath(row.parent_path) : normalizeRegistryPath(row.path),
        resetSelection: true,
        scrollToPath: true,
      });
    } catch (deleteError) {
      setError(deleteError?.message || "Registry delete failed.");
    } finally {
      setActionBusy("");
    }
  }, [deleteDialog, notifyOperator, requestRegistryMutation, restoreRegistryView]);

  const columnDefs = useMemo(
    () => [
      {
        field: "display_name",
        headerName: "Name",
        minWidth: 260,
        flex: 1.4,
        cellStyle: {
          display: "flex",
          alignItems: "center",
        },
        cellRenderer: (params) => {
          const row = params?.data || {};
          const isKey = isRegistryKey(row);
          const loadState = params?.context?.rowLoadStateByPath?.[row.path] || "";
          return (
            <Box
              sx={{
                display: "flex",
                alignItems: "center",
                gap: 0.8,
                height: "100%",
                minWidth: 0,
                width: "100%",
                pl: `${Number(row?.depth || 0) * 18}px`,
              }}
            >
              {isKey ? (
                row.kind === "hive" ? (
                  <Icon
                    baseClassName="material-symbols-outlined"
                    sx={{
                      fontSize: 29,
                      color: "#8fd3ff",
                      flexShrink: 0,
                      lineHeight: 1,
                      fontVariationSettings: '"FILL" 0, "wght" 400, "GRAD" 0, "opsz" 24',
                    }}
                  >
                    account_tree
                  </Icon>
                ) : (
                  <AccountTreeRoundedIcon sx={{ fontSize: 29, color: "#8fd3ff", flexShrink: 0 }} />
                )
              ) : (
                <ArticleRoundedIcon sx={{ fontSize: 29, color: row.editable ? "#cbd5e1" : "rgba(148,163,184,0.78)", flexShrink: 0 }} />
              )}
              <Box
                component={isKey ? "button" : "span"}
                type={isKey ? "button" : undefined}
                onClick={
                  isKey
                    ? (event) => {
                        event.stopPropagation();
                        params?.node?.setSelected?.(true, true);
                        params?.context?.toggleExpand?.(row);
                      }
                    : undefined
                }
                sx={{
                  border: 0,
                  background: "transparent",
                  color: "inherit",
                  cursor: isKey ? "pointer" : "default",
                  p: 0,
                  m: 0,
                  minWidth: 0,
                  textAlign: "left",
                  display: "inline-flex",
                  alignItems: "center",
                }}
              >
                <Typography
                  component="span"
                  sx={{
                    color: "#58a6ff",
                    fontWeight: 400,
                    lineHeight: 1.25,
                    minWidth: 0,
                    whiteSpace: "nowrap",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                  }}
                >
                  {row.display_name}
                </Typography>
              </Box>
              {loadState ? (
                <Box sx={LOADING_STATUS_PILL_SX}>
                  {loadState === "loading" ? (
                    <AutorenewRoundedIcon
                      sx={{
                        fontSize: 15,
                        color: "#9bd7ff",
                        animation: "remoteRegistryManagementSpin 1.15s linear infinite",
                      }}
                    />
                  ) : (
                    <CheckCircleRoundedIcon sx={{ fontSize: 15, color: "#4ade80" }} />
                  )}
                  <Typography component="span" sx={{ color: loadState === "loading" ? "#d9ecff" : "#c7f9cc", fontSize: "0.72rem", fontWeight: 600 }}>
                    {loadState === "loading" ? "Loading..." : "Loaded Successfully"}
                  </Typography>
                </Box>
              ) : null}
            </Box>
          );
        },
      },
      {
        field: "type_label",
        headerName: "Type",
        minWidth: 130,
        flex: 0.55,
        cellRenderer: (params) => <Typography component="span" sx={GRID_MUTED_CELL_SX}>{params?.value || ""}</Typography>,
      },
      {
        field: "data_label",
        headerName: "Value",
        minWidth: 260,
        flex: 1.2,
        cellRenderer: (params) => <Typography component="span" sx={GRID_CELL_TEXT_SX}>{params?.value || ""}</Typography>,
      },
      {
        field: "modified_label",
        headerName: "Modified",
        minWidth: 190,
        flex: 0.75,
        resizable: false,
        cellRenderer: (params) => <Typography component="span" sx={GRID_MUTED_CELL_SX}>{params?.value || ""}</Typography>,
      },
    ],
    []
  );

  const rowSelection = useMemo(() => ({ mode: "singleRow", enableClickSelection: true }), []);
  const gridContext = useMemo(
    () => ({
      toggleExpand,
      expandedPaths,
      loadingPaths,
      rowLoadStateByPath,
    }),
    [expandedPaths, loadingPaths, rowLoadStateByPath, toggleExpand]
  );

  const contextMenuSubject = useMemo(() => {
    if (selectedRow) {
      return {
        title: selectedRow.display_name || selectedRow.name || "Selected registry item",
        subtitle: selectedRow.row_type === "value" ? normalizeRegistryPath(selectedRow.path) : normalizeRegistryPath(selectedRow.path),
        kind: selectedRow.row_type,
        entry: selectedRow,
      };
    }
    return {
      title: getRegistryPathLeafLabel(currentPath),
      subtitle: normalizeRegistryPath(currentPath) || "Registry roots",
      kind: "location",
      entry: null,
    };
  }, [currentPath, selectedRow]);

  const renderContextMenuSubjectIcon = useCallback((subject) => {
    if (subject?.kind === "value") {
      return <ArticleRoundedIcon sx={{ fontSize: 19, color: "currentColor" }} />;
    }
    return <AccountTreeRoundedIcon sx={{ fontSize: 19, color: "currentColor" }} />;
  }, []);

  const contextMenuActions = useMemo(
    () => [
      {
        id: "new-key",
        group: "primary",
        label: "New Key",
        icon: AddRoundedIcon,
        disabled: !canMutateCurrentKey || loading || Boolean(actionBusy),
        disabledReason: !canMutateCurrentKey ? "Open or select a registry key first." : "",
        description: registryMutationTargetPath ? `Create under ${registryMutationTargetPath}.` : "",
        onClick: openCreateKeyDialog,
      },
      {
        id: "new-value",
        group: "primary",
        label: "New Value",
        icon: AddRoundedIcon,
        disabled: !canMutateCurrentKey || loading || Boolean(actionBusy),
        disabledReason: !canMutateCurrentKey ? "Open or select a registry key first." : "",
        description: registryMutationTargetPath ? `Create under ${registryMutationTargetPath}.` : "",
        onClick: openCreateValueDialog,
      },
      {
        id: "edit-value",
        group: "organize",
        label: "Edit Value",
        icon: EditRoundedIcon,
        disabled: !canEditValue || loading || Boolean(actionBusy),
        disabledReason:
          selectedRow?.row_type !== "value"
            ? "Select one registry value."
            : !selectedValue?.editable
              ? "This value type is read-only."
              : "",
        onClick: openEditValueDialog,
      },
      {
        id: "rename-key",
        group: "organize",
        label: "Rename Key",
        icon: DriveFileRenameOutlineRoundedIcon,
        disabled: !canRenameKey || loading || Boolean(actionBusy),
        disabledReason:
          selectedRow?.row_type !== "key"
            ? "Select one registry key."
            : !selectedKey?.editable
              ? "Hive roots cannot be renamed."
              : "",
        onClick: openRenameKeyDialog,
      },
      {
        id: "delete",
        group: "danger",
        intent: "danger",
        label: "Delete",
        icon: DeleteOutlineRoundedIcon,
        disabled: !(canDeleteKey || selectedValue) || loading || Boolean(actionBusy),
        disabledReason: !selectedRow ? "Select one registry key or value." : "",
        description: selectedRow ? "Requires confirmation before removal." : "",
        onClick: openDeleteDialog,
      },
      {
        id: "collapse-all",
        group: "view",
        label: "Collapse All",
        icon: UnfoldLessRoundedIcon,
        disabled: !expandedPaths.size || loading || Boolean(actionBusy),
        disabledReason: !expandedPaths.size ? "No expanded registry keys in the current view." : "",
        onClick: () => {
          setExpandedPaths(new Set());
        },
      },
    ],
    [
      actionBusy,
      canDeleteKey,
      canEditValue,
      canMutateCurrentKey,
      canRenameKey,
      expandedPaths.size,
      loading,
      openCreateKeyDialog,
      openCreateValueDialog,
      openDeleteDialog,
      openEditValueDialog,
      openRenameKeyDialog,
      registryMutationTargetPath,
      selectedKey,
      selectedRow,
      selectedValue,
    ]
  );

  const renderActionMenuItems = useCallback(
    (closeMenu) => {
      const groupLabels = {
        primary: "Primary",
        organize: "Organize",
        danger: "Danger Zone",
        view: "View",
      };
      const groupOrder = ["primary", "organize", "danger", "view"];
      const groups = groupOrder
        .map((groupId) => ({
          id: groupId,
          label: groupLabels[groupId],
          actions: contextMenuActions.filter((action) => action.group === groupId),
        }))
        .filter((group) => group.actions.length);

      const nodes = [
        <Box key="context-header" component="li" role="presentation" sx={ACTION_MENU_HEADER_SX}>
          <Box sx={ACTION_MENU_HEADER_ICON_SX}>{renderContextMenuSubjectIcon(contextMenuSubject)}</Box>
          <Box sx={{ minWidth: 0 }}>
            <Tooltip title={contextMenuSubject.title || ""} placement="top-start">
              <Typography
                sx={{
                  ...ACTION_MENU_TITLE_TRUNCATE_SX,
                  color: MAGIC_UI.textBright,
                  fontSize: "0.88rem",
                  fontWeight: 600,
                  lineHeight: 1.2,
                  maxWidth: 240,
                }}
              >
                {contextMenuSubject.title}
              </Typography>
            </Tooltip>
            <Tooltip title={contextMenuSubject.subtitle || ""} placement="top-start">
              <Typography
                sx={{
                  ...ACTION_MENU_TITLE_TRUNCATE_SX,
                  color: "rgba(148,163,184,0.82)",
                  fontSize: "0.73rem",
                  lineHeight: 1.25,
                  mt: 0.22,
                  maxWidth: 240,
                }}
              >
                {contextMenuSubject.subtitle}
              </Typography>
            </Tooltip>
          </Box>
        </Box>,
      ];

      groups.forEach((group) => {
        nodes.push(<Divider key={`divider-before-${group.id}`} component="li" sx={ACTION_MENU_DIVIDER_SX} />);
        nodes.push(
          <Box key={`label-${group.id}`} component="li" role="presentation" sx={ACTION_MENU_SECTION_LABEL_SX}>
            {group.label}
          </Box>
        );
        group.actions.forEach((action) => {
          const IconComponent = action.icon;
          const helperText = action.disabledReason || action.description || "";
          nodes.push(
            <MenuItem
              key={action.id}
              disabled={Boolean(action.disabled)}
              onClick={() => {
                closeMenu();
                action.onClick?.();
              }}
              sx={action.intent === "danger" ? ACTION_MENU_DANGER_ITEM_SX : ACTION_MENU_ITEM_SX}
            >
              <IconComponent
                sx={{
                  ...ACTION_MENU_ROW_ICON_SX,
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
                <Typography sx={ACTION_MENU_LABEL_SX}>{action.label}</Typography>
                {helperText ? <Typography sx={ACTION_MENU_DESCRIPTION_SX}>{helperText}</Typography> : null}
              </Box>
            </MenuItem>
          );
        });
      });

      return nodes;
    },
    [contextMenuActions, contextMenuSubject, renderContextMenuSubjectIcon]
  );

  return (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        gap: 1.5,
        flexGrow: 1,
        minHeight: 0,
        "@keyframes remoteRegistryManagementSpin": {
          "0%": { transform: "rotate(0deg)" },
          "100%": { transform: "rotate(360deg)" },
        },
      }}
    >
      <Stack direction="row" spacing={0.9} alignItems="center" sx={{ minWidth: 0 }}>
        <Tooltip title="Refresh registry listing" arrow>
          <span>
            <IconButton onClick={refreshCurrent} disabled={loading} sx={ICON_BUTTON_SX}>
              <RefreshRoundedIcon sx={{ fontSize: 18 }} />
            </IconButton>
          </span>
        </Tooltip>
        <Box sx={ADDRESS_BAR_SHELL_SX}>
          <Box
            component="button"
            type="button"
            aria-label="Go to registry roots"
            onClick={() => {
              void navigateToPath("");
            }}
            sx={ADDRESS_BAR_ROOT_BUTTON_SX}
          >
            <AccountTreeRoundedIcon sx={{ color: "currentColor", fontSize: 19, flexShrink: 0 }} />
            <ChevronRightRoundedIcon sx={{ fontSize: 17, color: "currentColor", flexShrink: 0 }} />
          </Box>
          {isPathEditing ? (
            <InputBase
              inputRef={pathInputRef}
              value={pathInput}
              onChange={(event) => setPathInput(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  void handleAddressBarNavigate();
                } else if (event.key === "Escape") {
                  event.preventDefault();
                  handleCancelPathEditing();
                }
              }}
              placeholder="HKLM\\SOFTWARE"
              autoComplete="off"
              fullWidth
              sx={ADDRESS_BAR_INPUT_SX}
              inputProps={{ "aria-label": "Registry path" }}
            />
          ) : (
            <>
              <Box
                sx={{
                  display: "flex",
                  alignItems: "center",
                  gap: 0.35,
                  minWidth: 0,
                  overflow: "hidden",
                  flexShrink: 1,
                }}
              >
                {addressBarSegments.length ? (
                  addressBarSegments.map((segment, index) => (
                    <React.Fragment key={`${segment.path || "root"}-${index}`}>
                      <Box
                        component="button"
                        type="button"
                        onClick={() => {
                          void navigateToPath(segment.path);
                        }}
                        sx={ADDRESS_BAR_SEGMENT_BUTTON_SX}
                      >
                        <Typography
                          component="span"
                          sx={{
                            fontSize: "0.92rem",
                            whiteSpace: "nowrap",
                          }}
                        >
                          {segment.label}
                        </Typography>
                      </Box>
                      {index < addressBarSegments.length - 1 ? (
                        <ChevronRightRoundedIcon sx={{ fontSize: 17, color: "rgba(148,163,184,0.86)", flexShrink: 0 }} />
                      ) : null}
                    </React.Fragment>
                  ))
                ) : null}
              </Box>
              <Box
                component="button"
                type="button"
                aria-label="Edit registry path"
                onClick={handleEnablePathEditing}
                sx={ADDRESS_BAR_EMPTY_SPACE_BUTTON_SX}
              />
            </>
          )}
          <Tooltip title={normalizeRegistryPath(currentPath) ? "Copy path" : "Nothing to copy"} arrow>
            <span>
              <IconButton
                size="small"
                onClick={() => void handleCopyPath()}
                disabled={!normalizeRegistryPath(currentPath)}
                sx={ADDRESS_BAR_COPY_BUTTON_SX}
              >
                <ContentCopyRoundedIcon sx={{ fontSize: 18 }} />
              </IconButton>
            </span>
          </Tooltip>
        </Box>
      </Stack>

      {error ? (
        <Alert severity="warning" sx={{ background: "rgba(120,53,15,0.32)", color: "#fed7aa", border: "1px solid rgba(251,146,60,0.28)" }}>
          {error}
        </Alert>
      ) : null}

      <GridShell onContextMenu={handleGridShellContextMenu} sx={REGISTRY_GRID_SX}>
        <AgGridReact
          ref={gridRef}
          rowData={visibleRows}
          columnDefs={columnDefs}
          defaultColDef={DEFAULT_GRID_COL_DEF}
          rowSelection={rowSelection}
          animateRows
          suppressCellFocus
          loading={initializing || loadingPaths.has(ROOTS_SENTINEL)}
          theme={DEVICE_DETAILS_GRID_THEME}
          getRowId={(params) => params?.data?.id || ""}
          context={gridContext}
          suppressContextMenu
          preventDefaultOnContextMenu
          onSelectionChanged={(event) => setSelectedRows(event.api.getSelectedRows())}
          onSortChanged={() => {
            const nextSortModel = gridRef.current?.api?.getSortModel?.() || [];
            setSortModel(nextSortModel);
          }}
          postSortRows={(params) => {
            params.nodes.sort((leftNode, rightNode) => {
              const leftSort = Number(leftNode?.data?.sortIndex || 0);
              const rightSort = Number(rightNode?.data?.sortIndex || 0);
              return leftSort - rightSort;
            });
          }}
          onCellClicked={(event) => {
            if (isGridInteractiveClick(event?.event)) return;
            if (isRegistryKey(event?.data)) {
              void toggleExpand(event.data);
            } else if (event?.data?.row_type === "value") {
              const parentPath = normalizeRegistryPath(event.data.path);
              setCurrentPath(parentPath);
              setPathInput(parentPath);
              setIsPathEditing(false);
            }
          }}
          onCellContextMenu={handleGridCellContextMenu}
          onRowDoubleClicked={(event) => {
            if (event?.data?.row_type === "value" && event.data.editable) {
              setValueDialog({
                open: true,
                mode: "update",
                row: event.data,
                name: normalizeText(event.data.name),
                type: normalizeText(event.data.type || "REG_SZ").toUpperCase(),
                data: valueDataToEditor(event.data),
              });
            }
          }}
        />
      </GridShell>

      <Menu
        open={Boolean(contextMenuState)}
        onClose={handleCloseContextMenu}
        anchorReference="anchorPosition"
        anchorPosition={
          contextMenuState
            ? {
                top: Number(contextMenuState?.top || 0),
                left: Number(contextMenuState?.left || 0),
              }
            : undefined
        }
        PaperProps={{ sx: ACTION_MENU_PAPER_SX }}
      >
        {renderActionMenuItems(handleCloseContextMenu)}
      </Menu>

      <Dialog open={keyDialog.open} onClose={() => setKeyDialog({ open: false, mode: "create", row: null, parentPath: "", name: "" })} PaperProps={{ sx: DIALOG_PAPER_SX }} fullWidth maxWidth="sm">
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title={keyDialog.mode === "rename" ? "Rename Key" : "Create Key"}
            subtitle={keyDialog.mode === "rename" ? normalizeRegistryPath(keyDialog.row?.path) : normalizeRegistryPath(keyDialog.parentPath)}
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <TextField
            autoFocus
            fullWidth
            label="Name"
            value={keyDialog.name}
            onChange={(event) => setKeyDialog((previous) => ({ ...previous, name: event.target.value }))}
            sx={DIALOG_INPUT_SX}
          />
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button sx={DIALOG_BUTTON_SX} onClick={() => setKeyDialog({ open: false, mode: "create", row: null, parentPath: "", name: "" })}>
            Cancel
          </Button>
          <Button sx={DIALOG_PRIMARY_BUTTON_SX} disabled={Boolean(actionBusy)} onClick={() => void handleSaveKey()}>
            Save
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog open={valueDialog.open} onClose={() => setValueDialog({ open: false, mode: "create", row: null, path: "", name: "", type: "REG_SZ", data: "" })} PaperProps={{ sx: DIALOG_PAPER_SX }} fullWidth maxWidth="md">
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title={valueDialog.mode === "update" ? "Edit Value" : "Create Value"}
            subtitle={normalizeRegistryPath(valueDialog.path || currentPath)}
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Stack spacing={1.25}>
            <Stack direction={{ xs: "column", sm: "row" }} spacing={1}>
              <TextField
                fullWidth
                label="Name"
                value={valueDialog.name}
                disabled={valueDialog.mode === "update"}
                onChange={(event) => setValueDialog((previous) => ({ ...previous, name: event.target.value }))}
                sx={DIALOG_INPUT_SX}
              />
              <TextField
                select
                label="Type"
                value={valueDialog.type}
                onChange={(event) => setValueDialog((previous) => ({ ...previous, type: event.target.value }))}
                sx={{ ...DIALOG_INPUT_SX, minWidth: 190 }}
              >
                {VALUE_TYPES.map((type) => (
                  <MenuItem key={type} value={type}>
                    {type}
                  </MenuItem>
                ))}
              </TextField>
            </Stack>
            <TextField
              fullWidth
              multiline
              minRows={valueDialog.type === "REG_MULTI_SZ" || valueDialog.type === "REG_BINARY" ? 8 : 5}
              label="Data"
              value={valueDialog.data}
              onChange={(event) => setValueDialog((previous) => ({ ...previous, data: event.target.value }))}
              sx={DIALOG_INPUT_SX}
            />
          </Stack>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button sx={DIALOG_BUTTON_SX} onClick={() => setValueDialog({ open: false, mode: "create", row: null, path: "", name: "", type: "REG_SZ", data: "" })}>
            Cancel
          </Button>
          <Button sx={DIALOG_PRIMARY_BUTTON_SX} disabled={Boolean(actionBusy)} onClick={() => void handleSaveValue()}>
            Save
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog open={deleteDialog.open} onClose={() => setDeleteDialog({ open: false, row: null, confirmPath: "", recursive: false })} PaperProps={{ sx: DIALOG_PAPER_SX }} fullWidth maxWidth="sm">
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title={deleteDialog.row?.row_type === "key" ? "Delete Key" : "Delete Value"}
            subtitle={deleteDialog.row?.row_type === "key" ? normalizeRegistryPath(deleteDialog.row?.path) : deleteDialog.row?.display_name}
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          {deleteDialog.row?.row_type === "key" ? (
            <Stack spacing={1.1}>
              <TextField
                fullWidth
                label="Confirm Path"
                value={deleteDialog.confirmPath}
                onChange={(event) => setDeleteDialog((previous) => ({ ...previous, confirmPath: event.target.value }))}
                sx={DIALOG_INPUT_SX}
              />
              <FormControlLabel
                control={
                  <Checkbox
                    checked={Boolean(deleteDialog.recursive)}
                    onChange={(event) => setDeleteDialog((previous) => ({ ...previous, recursive: event.target.checked }))}
                    sx={{ color: "rgba(148,163,184,0.86)", "&.Mui-checked": { color: "#f87171" } }}
                  />
                }
                label="Delete subkeys"
                sx={{ color: MAGIC_UI.textMuted }}
              />
            </Stack>
          ) : (
            <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.92rem" }}>
              {normalizeRegistryPath(deleteDialog.row?.path)}
            </Typography>
          )}
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button sx={DIALOG_BUTTON_SX} onClick={() => setDeleteDialog({ open: false, row: null, confirmPath: "", recursive: false })}>
            Cancel
          </Button>
          <Button
            sx={DIALOG_DANGER_BUTTON_SX}
            disabled={
              Boolean(actionBusy) ||
              (deleteDialog.row?.row_type === "key" &&
                normalizeRegistryPath(deleteDialog.confirmPath).toLowerCase() !== normalizeRegistryPath(deleteDialog.row?.path).toLowerCase())
            }
            onClick={() => void handleDelete()}
          >
            Delete
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
}
