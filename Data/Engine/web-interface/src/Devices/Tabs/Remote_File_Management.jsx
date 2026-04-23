import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  LinearProgress,
  Menu,
  MenuItem,
  Stack,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import { AgGridReact } from "ag-grid-react";
import RefreshRoundedIcon from "@mui/icons-material/RefreshRounded";
import FileUploadRoundedIcon from "@mui/icons-material/FileUploadRounded";
import FileDownloadRoundedIcon from "@mui/icons-material/FileDownloadRounded";
import MoreHorizRoundedIcon from "@mui/icons-material/MoreHorizRounded";
import FolderRoundedIcon from "@mui/icons-material/FolderRounded";
import FolderOpenRoundedIcon from "@mui/icons-material/FolderOpenRounded";
import InsertDriveFileRoundedIcon from "@mui/icons-material/InsertDriveFileRounded";
import LinkRoundedIcon from "@mui/icons-material/LinkRounded";
import ChevronRightRoundedIcon from "@mui/icons-material/ChevronRightRounded";
import ExpandMoreRoundedIcon from "@mui/icons-material/ExpandMoreRounded";
import ArrowUpwardRoundedIcon from "@mui/icons-material/ArrowUpwardRounded";
import CreateNewFolderRoundedIcon from "@mui/icons-material/CreateNewFolderRounded";
import EditRoundedIcon from "@mui/icons-material/EditRounded";
import DriveFileMoveRoundedIcon from "@mui/icons-material/DriveFileMoveRounded";
import DeleteOutlineRoundedIcon from "@mui/icons-material/DeleteOutlineRounded";
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

const ROOTS_SENTINEL = "__borealis_roots__";
const TRANSFER_POLL_INTERVAL_MS = 2000;

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

const PRIMARY_ACTION_BUTTON_SX = {
  ...ACTION_BUTTON_SX,
  color: "#031525",
  borderColor: "transparent",
  background: "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)",
  boxShadow: "0 18px 38px rgba(76, 110, 245, 0.18)",
  "&:hover": {
    background: "linear-gradient(135deg, #93ddff 0%, #d0a0ff 100%)",
    borderColor: "transparent",
    boxShadow: "0 22px 42px rgba(76, 110, 245, 0.24)",
  },
  "&.Mui-disabled": {
    color: "rgba(2, 6, 23, 0.45)",
    background: "linear-gradient(135deg, rgba(125,211,252,0.4) 0%, rgba(192,132,252,0.4) 100%)",
    borderColor: "transparent",
    boxShadow: "none",
  },
};

function normalizeText(value) {
  if (value == null) return "";
  try {
    return String(value).trim();
  } catch {
    return "";
  }
}

function isGuidLike(value) {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(normalizeText(value));
}

function getHostname(device) {
  const candidates = [device?.summary?.hostname, device?.device_hostname, device?.hostname]
    .map((value) => normalizeText(value))
    .filter(Boolean);
  return candidates.find((value) => !isGuidLike(value)) || "";
}

function normalizeEntry(row = {}) {
  const path = normalizeText(row?.path);
  const parentPath = normalizeText(row?.parent_path);
  const name = normalizeText(row?.name) || path;
  const kind = normalizeText(row?.kind).toLowerCase() || "file";
  const attributesList = Array.isArray(row?.attributes)
    ? row.attributes.map((value) => normalizeText(value)).filter(Boolean)
    : normalizeText(row?.attributes)
      ? normalizeText(row.attributes)
          .split(",")
          .map((value) => normalizeText(value))
          .filter(Boolean)
      : [];
  const attributesText = attributesList
    .map((value) =>
      value
        .replace(/[_-]+/g, " ")
        .replace(/\b\w/g, (character) => character.toUpperCase())
    )
    .join(", ");
  const typeLabel =
    kind === "directory"
      ? "Folder"
      : kind === "symlink"
        ? "Link"
        : "File";
  return {
    ...row,
    id: path || name,
    path,
    parent_path: parentPath,
    name,
    kind,
    attributes: attributesList,
    attributes_text: attributesText,
    type_label: typeLabel,
    size_bytes: Number(row?.size_bytes || 0),
    modified_at: Number(row?.modified_at || 0),
    has_children: Boolean(row?.has_children),
    is_hidden: Boolean(row?.is_hidden),
  };
}

function formatBytes(sizeBytes) {
  const value = Number(sizeBytes || 0);
  if (!value) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = value;
  let index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }
  return `${size.toFixed(size >= 10 || index === 0 ? 0 : 1)} ${units[index]}`;
}

function formatModifiedAt(epochSeconds) {
  const value = Number(epochSeconds || 0);
  if (!value) return "—";
  const date = new Date(value * 1000);
  if (Number.isNaN(date.getTime())) return "—";
  const dateText = date.toLocaleDateString();
  const timeText = date.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
  return `${dateText} @ ${timeText}`;
}

function getRootKey(currentPath, platform) {
  return platform === "windows" && !normalizeText(currentPath) ? ROOTS_SENTINEL : normalizeText(currentPath || "/") || "/";
}

function isDirectory(entry) {
  return normalizeText(entry?.kind).toLowerCase() === "directory";
}

function isTerminalTransferStatus(value) {
  const normalized = normalizeText(value).toLowerCase();
  return normalized === "completed" || normalized === "failed";
}

function getParentPath(pathValue, platform) {
  const path = normalizeText(pathValue);
  if (!path) return "";
  if (platform === "windows") {
    const normalized = path.replace(/\//g, "\\");
    if (/^[A-Za-z]:\\?$/.test(normalized)) {
      return "";
    }
    const trimmed = normalized.replace(/[\\]+$/, "");
    const separatorIndex = trimmed.lastIndexOf("\\");
    if (separatorIndex < 0) return "";
    const parent = trimmed.slice(0, separatorIndex);
    return /^[A-Za-z]:$/.test(parent) ? `${parent}\\` : parent;
  }
  if (path === "/") return "/";
  const trimmed = path.endsWith("/") && path !== "/" ? path.slice(0, -1) : path;
  const separatorIndex = trimmed.lastIndexOf("/");
  if (separatorIndex <= 0) return "/";
  return trimmed.slice(0, separatorIndex);
}

function buildBreadcrumbs(pathValue, platform) {
  if (platform === "windows") {
    const normalized = normalizeText(pathValue).replace(/\//g, "\\");
    const breadcrumbs = [{ label: "Computer", path: "" }];
    if (!normalized) return breadcrumbs;
    const driveMatch = normalized.match(/^[A-Za-z]:\\/);
    if (driveMatch) {
      let currentPath = driveMatch[0];
      breadcrumbs.push({ label: currentPath, path: currentPath });
      const remainder = normalized.slice(currentPath.length).split("\\").filter(Boolean);
      remainder.forEach((segment) => {
        currentPath = `${currentPath.replace(/[\\]+$/, "")}\\${segment}`;
        breadcrumbs.push({ label: segment, path: currentPath });
      });
      return breadcrumbs;
    }
    return [...breadcrumbs, { label: normalized, path: normalized }];
  }
  const normalized = normalizeText(pathValue) || "/";
  const breadcrumbs = [{ label: "Root", path: "/" }];
  if (normalized === "/") return breadcrumbs;
  let currentPath = "";
  normalized
    .split("/")
    .filter(Boolean)
    .forEach((segment) => {
      currentPath += `/${segment}`;
      breadcrumbs.push({ label: segment, path: currentPath });
    });
  return breadcrumbs;
}

function compareEntryValues(left, right, columnId) {
  if (columnId === "size_bytes" || columnId === "modified_at") {
    return Number(left?.[columnId] || 0) - Number(right?.[columnId] || 0);
  }
  if (columnId === "type_label") {
    return normalizeText(left?.type_label).localeCompare(normalizeText(right?.type_label), undefined, {
      sensitivity: "base",
      numeric: true,
    });
  }
  if (columnId === "attributes_text") {
    return normalizeText(left?.attributes_text).localeCompare(normalizeText(right?.attributes_text), undefined, {
      sensitivity: "base",
      numeric: true,
    });
  }
  return normalizeText(left?.[columnId]).localeCompare(normalizeText(right?.[columnId]), undefined, {
    sensitivity: "base",
    numeric: true,
  });
}

function sortEntries(entries, sortModel = []) {
  const activeSorts = Array.isArray(sortModel) && sortModel.length ? sortModel : [{ colId: "name", sort: "asc" }];
  return [...entries].sort((left, right) => {
    const leftIsDirectory = isDirectory(left);
    const rightIsDirectory = isDirectory(right);
    if (leftIsDirectory !== rightIsDirectory) {
      return leftIsDirectory ? -1 : 1;
    }
    for (const descriptor of activeSorts) {
      const columnId = normalizeText(descriptor?.colId) || "name";
      const direction = normalizeText(descriptor?.sort).toLowerCase() === "desc" ? -1 : 1;
      const comparison = compareEntryValues(left, right, columnId);
      if (comparison !== 0) return comparison * direction;
    }
    return normalizeText(left?.name).localeCompare(normalizeText(right?.name), undefined, {
      sensitivity: "base",
      numeric: true,
    });
  });
}

function buildVisibleRows(entriesByParent, rootKey, expandedPaths, sortModel) {
  const rows = [];
  let sortIndex = 0;

  function visit(parentKey, depth) {
    const children = sortEntries(entriesByParent[parentKey] || [], sortModel);
    children.forEach((entry) => {
      rows.push({ ...entry, depth, sortIndex });
      sortIndex += 1;
      if (isDirectory(entry) && expandedPaths.has(entry.path)) {
        visit(entry.path, depth + 1);
      }
    });
  }

  visit(rootKey, 0);
  return rows;
}

function TransferBanner({ transfers = {} }) {
  const rows = Object.values(transfers || {}).filter((transfer) => !isTerminalTransferStatus(transfer?.status));
  if (!rows.length) return null;
  return (
    <Stack spacing={0.75} sx={{ mb: 1.5 }}>
      {rows.map((transfer) => {
        const bytesTotal = Number(transfer?.bytes_total || 0);
        const bytesComplete = Math.min(bytesTotal || Number(transfer?.bytes_complete || 0), Number(transfer?.bytes_complete || 0));
        const progress = bytesTotal > 0 ? Math.min(100, Math.round((bytesComplete / bytesTotal) * 100)) : 0;
        const directionLabel = normalizeText(transfer?.direction).toLowerCase() === "upload" ? "Upload" : "Download";
        const targetLabel = normalizeText(transfer?.target_path) || normalizeText(transfer?.result_name) || normalizeText(transfer?.archive_name);
        return (
          <Box
            key={transfer.transfer_id || directionLabel}
            sx={{
              px: 1.5,
              py: 1.15,
              borderRadius: 2,
              border: "1px solid rgba(148,163,184,0.22)",
              background: "rgba(5,10,24,0.72)",
            }}
          >
            <Stack direction="row" spacing={1} alignItems="center" justifyContent="space-between" sx={{ mb: 0.75 }}>
              <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.88rem", fontWeight: 600 }}>
                {directionLabel} in progress
              </Typography>
              <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem" }}>
                {progress ? `${progress}%` : normalizeText(transfer?.status) || "Pending"}
              </Typography>
            </Stack>
            {targetLabel ? (
              <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.79rem", mb: 0.75 }}>
                {targetLabel}
              </Typography>
            ) : null}
            <LinearProgress
              variant={bytesTotal > 0 ? "determinate" : "indeterminate"}
              value={progress}
              sx={{
                height: 7,
                borderRadius: 999,
                backgroundColor: "rgba(30,41,59,0.8)",
                "& .MuiLinearProgress-bar": {
                  borderRadius: 999,
                  background: "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)",
                },
              }}
            />
          </Box>
        );
      })}
    </Stack>
  );
}

export default function RemoteFileManagement({ device }) {
  const notifyOperator = useAppNotifications();
  const gridRef = useRef(null);
  const fileInputRef = useRef(null);
  const handledTransfersRef = useRef({});

  const hostname = useMemo(() => getHostname(device), [device]);
  const [platform, setPlatform] = useState("");
  const [contextLabel, setContextLabel] = useState("SYSTEM");
  const [currentPath, setCurrentPath] = useState("");
  const [entriesByParent, setEntriesByParent] = useState({});
  const [expandedPaths, setExpandedPaths] = useState(() => new Set());
  const [selectedRows, setSelectedRows] = useState([]);
  const [sortModel, setSortModel] = useState([]);
  const [loadingPaths, setLoadingPaths] = useState(() => new Set());
  const [initializing, setInitializing] = useState(true);
  const [error, setError] = useState("");
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [newFolderOpen, setNewFolderOpen] = useState(false);
  const [renameOpen, setRenameOpen] = useState(false);
  const [moveOpen, setMoveOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [newFolderName, setNewFolderName] = useState("");
  const [renameValue, setRenameValue] = useState("");
  const [moveDestination, setMoveDestination] = useState("");
  const [actionBusy, setActionBusy] = useState("");
  const [activeTransfers, setActiveTransfers] = useState({});

  const rootKey = useMemo(() => getRootKey(currentPath, platform), [currentPath, platform]);
  const visibleRows = useMemo(
    () => buildVisibleRows(entriesByParent, rootKey, expandedPaths, sortModel).map((row) => ({ ...row, id: row.path })),
    [entriesByParent, expandedPaths, rootKey, sortModel]
  );

  const selectedEntry = selectedRows.length === 1 ? selectedRows[0] : null;
  const currentEntries = entriesByParent[rootKey] || [];
  const breadcrumbs = useMemo(() => buildBreadcrumbs(currentPath || (platform === "windows" ? "" : "/"), platform), [currentPath, platform]);
  const canGoUp = useMemo(() => {
    if (platform === "windows") return Boolean(normalizeText(currentPath));
    return normalizeText(currentPath) !== "/";
  }, [currentPath, platform]);

  const resolvedUploadTarget = useMemo(() => {
    if (selectedRows.length === 1) {
      return isDirectory(selectedRows[0]) ? selectedRows[0].path : normalizeText(selectedRows[0].parent_path);
    }
    if (platform !== "windows" || normalizeText(currentPath)) {
      return normalizeText(currentPath || "/");
    }
    const firstDirectory = selectedRows.find((row) => isDirectory(row));
    return firstDirectory?.path || "";
  }, [currentPath, platform, selectedRows]);

  const refreshBaseView = useCallback(async () => {
    if (!hostname) return;
    setError("");
    setSelectedRows([]);
    gridRef.current?.api?.deselectAll?.();
    if (platform === "windows" && !normalizeText(currentPath)) {
      setLoadingPaths((previous) => {
        const next = new Set(previous);
        next.add(ROOTS_SENTINEL);
        return next;
      });
      try {
        const response = await fetch(`/api/device/files/${encodeURIComponent(hostname)}/roots`, {
          credentials: "include",
        });
        const data = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
        }
        const normalizedPlatform = normalizeText(data?.platform).toLowerCase() || "windows";
        const basePath = normalizedPlatform === "windows" ? "" : normalizeText(data?.current_path || "/") || "/";
        setPlatform(normalizedPlatform);
        setContextLabel(normalizeText(data?.context_label) || (normalizedPlatform === "windows" ? "SYSTEM" : "root"));
        setCurrentPath(basePath);
        setEntriesByParent({
          [getRootKey(basePath, normalizedPlatform)]: Array.isArray(data?.entries)
            ? data.entries.map((row) => normalizeEntry(row))
            : [],
        });
        setExpandedPaths(new Set());
      } catch (fetchError) {
        setError(String(fetchError?.message || fetchError));
      } finally {
        setLoadingPaths((previous) => {
          const next = new Set(previous);
          next.delete(ROOTS_SENTINEL);
          return next;
        });
      }
      return;
    }

    const normalizedPath = normalizeText(currentPath || "/") || "/";
    setLoadingPaths((previous) => {
      const next = new Set(previous);
      next.add(normalizedPath);
      return next;
    });
    try {
      const response = await fetch(
        `/api/device/files/${encodeURIComponent(hostname)}/children?path=${encodeURIComponent(normalizedPath)}`,
        { credentials: "include" }
      );
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
      }
      setEntriesByParent({
        [getRootKey(normalizedPath, platform || "linux")]: Array.isArray(data?.entries)
          ? data.entries.map((row) => normalizeEntry(row))
          : [],
      });
      setExpandedPaths(new Set());
    } catch (fetchError) {
      setError(String(fetchError?.message || fetchError));
    } finally {
      setLoadingPaths((previous) => {
        const next = new Set(previous);
        next.delete(normalizedPath);
        return next;
      });
    }
  }, [currentPath, hostname, platform]);

  useEffect(() => {
    let canceled = false;

    async function loadInitial() {
      if (!hostname) {
        setInitializing(false);
        return;
      }
      setInitializing(true);
      try {
        const response = await fetch(`/api/device/files/${encodeURIComponent(hostname)}/roots`, {
          credentials: "include",
        });
        const data = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
        }
        if (canceled) return;
        const normalizedPlatform = normalizeText(data?.platform).toLowerCase() || "linux";
        const basePath = normalizedPlatform === "windows" ? "" : normalizeText(data?.current_path || "/") || "/";
        setPlatform(normalizedPlatform);
        setContextLabel(normalizeText(data?.context_label) || (normalizedPlatform === "windows" ? "SYSTEM" : "root"));
        setCurrentPath(basePath);
        setEntriesByParent({
          [getRootKey(basePath, normalizedPlatform)]: Array.isArray(data?.entries)
            ? data.entries.map((row) => normalizeEntry(row))
            : [],
        });
        setExpandedPaths(new Set());
        setError("");
      } catch (fetchError) {
        if (!canceled) {
          setError(String(fetchError?.message || fetchError));
        }
      } finally {
        if (!canceled) {
          setInitializing(false);
        }
      }
    }

    loadInitial();
    return () => {
      canceled = true;
    };
  }, [hostname]);

  const loadChildrenForPath = useCallback(
    async (pathValue) => {
      const normalizedPath = normalizeText(pathValue);
      if (!hostname || !normalizedPath) return;
      setLoadingPaths((previous) => {
        const next = new Set(previous);
        next.add(normalizedPath);
        return next;
      });
      try {
        const response = await fetch(
          `/api/device/files/${encodeURIComponent(hostname)}/children?path=${encodeURIComponent(normalizedPath)}`,
          { credentials: "include" }
        );
        const data = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
        }
        setEntriesByParent((previous) => ({
          ...previous,
          [normalizedPath]: Array.isArray(data?.entries) ? data.entries.map((row) => normalizeEntry(row)) : [],
        }));
      } finally {
        setLoadingPaths((previous) => {
          const next = new Set(previous);
          next.delete(normalizedPath);
          return next;
        });
      }
    },
    [hostname]
  );

  const navigateToPath = useCallback(
    async (pathValue) => {
      const nextPath = platform === "windows" ? normalizeText(pathValue) : normalizeText(pathValue || "/") || "/";
      setCurrentPath(nextPath);
      setExpandedPaths(new Set());
      setSelectedRows([]);
      gridRef.current?.api?.deselectAll?.();
      setEntriesByParent({});
      if (!hostname) return;
      setLoadingPaths((previous) => {
        const next = new Set(previous);
        next.add(getRootKey(nextPath, platform));
        return next;
      });
      try {
        if (platform === "windows" && !nextPath) {
          const response = await fetch(`/api/device/files/${encodeURIComponent(hostname)}/roots`, {
            credentials: "include",
          });
          const data = await response.json().catch(() => ({}));
          if (!response.ok) {
            throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
          }
          setEntriesByParent({
            [ROOTS_SENTINEL]: Array.isArray(data?.entries) ? data.entries.map((row) => normalizeEntry(row)) : [],
          });
          return;
        }
        const response = await fetch(
          `/api/device/files/${encodeURIComponent(hostname)}/children?path=${encodeURIComponent(nextPath)}`,
          { credentials: "include" }
        );
        const data = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
        }
        setEntriesByParent({
          [getRootKey(nextPath, platform)]: Array.isArray(data?.entries) ? data.entries.map((row) => normalizeEntry(row)) : [],
        });
        setError("");
      } catch (fetchError) {
        setError(String(fetchError?.message || fetchError));
      } finally {
        setLoadingPaths((previous) => {
          const next = new Set(previous);
          next.delete(getRootKey(nextPath, platform));
          return next;
        });
      }
    },
    [hostname, platform]
  );

  const toggleExpand = useCallback(
    async (row) => {
      if (!row || !isDirectory(row)) return;
      const pathValue = normalizeText(row.path);
      if (!pathValue) return;
      if (expandedPaths.has(pathValue)) {
        setExpandedPaths((previous) => {
          const next = new Set(previous);
          next.delete(pathValue);
          return next;
        });
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
        const next = new Set(previous);
        next.add(pathValue);
        return next;
      });
    },
    [entriesByParent, expandedPaths, loadChildrenForPath]
  );

  const triggerDownload = useCallback((transferId, fileName) => {
    const anchor = document.createElement("a");
    anchor.href = `/api/device/files/${encodeURIComponent(hostname)}/transfer/${encodeURIComponent(transferId)}/content`;
    if (normalizeText(fileName)) {
      anchor.download = fileName;
    }
    document.body.appendChild(anchor);
    anchor.click();
    document.body.removeChild(anchor);
  }, [hostname]);

  useEffect(() => {
    const pendingTransfers = Object.values(activeTransfers || {}).filter(
      (transfer) => transfer?.transfer_id && !isTerminalTransferStatus(transfer?.status)
    );
    if (!pendingTransfers.length || !hostname) return undefined;
    let canceled = false;

    async function pollOnce() {
      for (const transfer of pendingTransfers) {
        try {
          const response = await fetch(
            `/api/device/files/${encodeURIComponent(hostname)}/transfer/${encodeURIComponent(transfer.transfer_id)}/status`,
            { credentials: "include" }
          );
          const data = await response.json().catch(() => ({}));
          if (!response.ok) {
            throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
          }
          if (canceled) return;
          setActiveTransfers((previous) => ({ ...previous, [transfer.transfer_id]: data }));
          const handledKey = `${transfer.transfer_id}:${normalizeText(data?.status).toLowerCase()}`;
          if (normalizeText(data?.status).toLowerCase() === "completed" && !handledTransfersRef.current[handledKey]) {
            handledTransfersRef.current[handledKey] = true;
            if (normalizeText(data?.direction).toLowerCase() === "download" && data?.download_ready) {
              triggerDownload(data.transfer_id, data.result_name || data.archive_name);
              void notifyOperator({
                title: "Download Ready",
                message: `Prepared ${normalizeText(data?.result_name) || normalizeText(data?.archive_name) || "the requested download"}.`,
                icon: "download",
                variant: "success",
              });
            } else {
              void notifyOperator({
                title: "Upload Complete",
                message: `Uploaded ${Number(data?.item_count || 0)} item${Number(data?.item_count || 0) === 1 ? "" : "s"} to ${normalizeText(data?.target_path) || "the selected directory"}.`,
                icon: "upload",
                variant: "success",
              });
              void refreshBaseView();
            }
            setActiveTransfers((previous) => {
              const next = { ...previous };
              delete next[transfer.transfer_id];
              return next;
            });
          }
          if (normalizeText(data?.status).toLowerCase() === "failed" && !handledTransfersRef.current[handledKey]) {
            handledTransfersRef.current[handledKey] = true;
            void notifyOperator({
              title: "Transfer Failed",
              message: normalizeText(data?.error) || "The file transfer failed.",
              icon: "error",
              variant: "error",
            });
            setActiveTransfers((previous) => {
              const next = { ...previous };
              delete next[transfer.transfer_id];
              return next;
            });
          }
        } catch (pollError) {
          if (!canceled) {
            setError(String(pollError?.message || pollError));
          }
        }
      }
    }

    void pollOnce();
    const timerId = window.setInterval(() => {
      void pollOnce();
    }, TRANSFER_POLL_INTERVAL_MS);
    return () => {
      canceled = true;
      window.clearInterval(timerId);
    };
  }, [activeTransfers, hostname, notifyOperator, refreshBaseView, triggerDownload]);

  const handleSelectionChanged = useCallback(() => {
    const rows = gridRef.current?.api?.getSelectedRows?.() || [];
    setSelectedRows(rows);
  }, []);

  const handleGridSortChanged = useCallback(() => {
    const nextSortModel = gridRef.current?.api?.getSortModel?.() || [];
    setSortModel(nextSortModel);
  }, []);

  const handleOpenUploadPicker = useCallback(() => {
    fileInputRef.current?.click?.();
  }, []);

  const handleUploadSelection = useCallback(
    async (event) => {
      const files = Array.from(event?.target?.files || []);
      event.target.value = "";
      if (!files.length || !hostname || !resolvedUploadTarget) return;
      setActionBusy("upload");
      try {
        const formData = new FormData();
        formData.append("target_path", resolvedUploadTarget);
        files.forEach((file) => {
          formData.append("files", file, file.name);
        });
        const response = await fetch(`/api/device/files/${encodeURIComponent(hostname)}/upload`, {
          method: "POST",
          credentials: "include",
          body: formData,
        });
        const data = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
        }
        setActiveTransfers((previous) => ({ ...previous, [data.transfer_id]: data }));
        await notifyOperator({
          title: "Upload Started",
          message: `Uploading ${files.length} item${files.length === 1 ? "" : "s"} to ${resolvedUploadTarget}.`,
          icon: "upload",
          variant: "info",
        });
      } catch (uploadError) {
        await notifyOperator({
          title: "Upload Failed",
          message: String(uploadError?.message || uploadError),
          icon: "error",
          variant: "error",
        });
      } finally {
        setActionBusy("");
      }
    },
    [hostname, notifyOperator, resolvedUploadTarget]
  );

  const handleStartDownload = useCallback(async () => {
    if (!hostname || !selectedRows.length) return;
    setActionBusy("download");
    try {
      const response = await fetch(`/api/device/files/${encodeURIComponent(hostname)}/download`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          items: selectedRows.map((row) => ({
            path: row.path,
            name: row.name,
            kind: row.kind,
          })),
        }),
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
      }
      setActiveTransfers((previous) => ({ ...previous, [data.transfer_id]: data }));
      await notifyOperator({
        title: "Download Started",
        message: `Preparing ${selectedRows.length} item${selectedRows.length === 1 ? "" : "s"} for download.`,
        icon: "download",
        variant: "info",
      });
    } catch (downloadError) {
      await notifyOperator({
        title: "Download Failed",
        message: String(downloadError?.message || downloadError),
        icon: "error",
        variant: "error",
      });
    } finally {
      setActionBusy("");
    }
  }, [hostname, notifyOperator, selectedRows]);

  const executeJsonAction = useCallback(
    async (pathSuffix, payload, successMessage, busyKey) => {
      if (!hostname) return false;
      setActionBusy(busyKey);
      try {
        const response = await fetch(`/api/device/files/${encodeURIComponent(hostname)}/${pathSuffix}`, {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload),
        });
        const data = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
        }
        await notifyOperator({
          title: "File Management",
          message: successMessage,
          icon: "notification",
          variant: "success",
        });
        await refreshBaseView();
        return true;
      } catch (actionError) {
        await notifyOperator({
          title: "Action Failed",
          message: String(actionError?.message || actionError),
          icon: "error",
          variant: "error",
        });
        return false;
      } finally {
        setActionBusy("");
      }
    },
    [hostname, notifyOperator, refreshBaseView]
  );

  const handleCreateFolder = useCallback(async () => {
    const destination = normalizeText(resolvedUploadTarget || currentPath || (platform === "windows" ? "" : "/"));
    if (!destination || !newFolderName.trim()) return;
    const didCreate = await executeJsonAction(
      "mkdir",
      { path: destination, name: newFolderName.trim() },
      `Created ${newFolderName.trim()} in ${destination}.`,
      "mkdir"
    );
    if (didCreate) {
      setNewFolderOpen(false);
      setNewFolderName("");
      setMenuAnchor(null);
    }
  }, [currentPath, executeJsonAction, newFolderName, platform, resolvedUploadTarget]);

  const handleRename = useCallback(async () => {
    if (!selectedEntry || !renameValue.trim()) return;
    const didRename = await executeJsonAction(
      "rename",
      { path: selectedEntry.path, new_name: renameValue.trim() },
      `Renamed ${selectedEntry.name} to ${renameValue.trim()}.`,
      "rename"
    );
    if (didRename) {
      setRenameOpen(false);
      setRenameValue("");
      setMenuAnchor(null);
    }
  }, [executeJsonAction, renameValue, selectedEntry]);

  const handleMove = useCallback(async () => {
    if (!selectedRows.length || !moveDestination.trim()) return;
    const didMove = await executeJsonAction(
      "move",
      {
        destination_path: moveDestination.trim(),
        paths: selectedRows.map((row) => ({ path: row.path, name: row.name, kind: row.kind })),
      },
      `Moved ${selectedRows.length} item${selectedRows.length === 1 ? "" : "s"} to ${moveDestination.trim()}.`,
      "move"
    );
    if (didMove) {
      setMoveOpen(false);
      setMoveDestination("");
      setMenuAnchor(null);
    }
  }, [executeJsonAction, moveDestination, selectedRows]);

  const handleDelete = useCallback(async () => {
    if (!selectedRows.length) return;
    const didDelete = await executeJsonAction(
      "delete",
      { paths: selectedRows.map((row) => ({ path: row.path, name: row.name, kind: row.kind })) },
      `Deleted ${selectedRows.length} item${selectedRows.length === 1 ? "" : "s"}.`,
      "delete"
    );
    if (didDelete) {
      setDeleteOpen(false);
      setMenuAnchor(null);
    }
  }, [executeJsonAction, selectedRows]);

  const columnDefs = useMemo(
    () => [
      {
        headerName: "",
        field: "__select__",
        width: 52,
        maxWidth: 52,
        checkboxSelection: true,
        headerCheckboxSelection: true,
        resizable: false,
        sortable: false,
        suppressMenu: true,
        filter: false,
        pinned: "left",
        lockPosition: true,
      },
      {
        headerName: "Name",
        field: "name",
        flex: 1.6,
        minWidth: 260,
        cellRenderer: (params) => {
          const row = params?.data || {};
          const expanded = params?.context?.expandedPaths?.has(row.path);
          const isLoading = params?.context?.loadingPaths?.has(row.path);
          const canExpand = isDirectory(row) && Boolean(row?.has_children);
          const IconComponent =
            row?.kind === "directory"
              ? expanded
                ? FolderOpenRoundedIcon
                : FolderRoundedIcon
              : row?.kind === "symlink"
                ? LinkRoundedIcon
                : InsertDriveFileRoundedIcon;
          return (
            <Box
              sx={{
                display: "flex",
                alignItems: "center",
                gap: 0.8,
                minWidth: 0,
                width: "100%",
                pl: `${Number(row?.depth || 0) * 18}px`,
              }}
            >
              <Box
                component="button"
                type="button"
                onClick={(event) => {
                  event.stopPropagation();
                  if (canExpand) {
                    params?.context?.toggleExpand?.(row);
                  }
                }}
                sx={{
                  width: 22,
                  height: 22,
                  minWidth: 22,
                  border: 0,
                  background: "transparent",
                  color: canExpand ? "#9bd7ff" : "rgba(148,163,184,0.32)",
                  display: "inline-flex",
                  alignItems: "center",
                  justifyContent: "center",
                  cursor: canExpand ? "pointer" : "default",
                  p: 0,
                }}
              >
                {canExpand ? (
                  expanded ? <ExpandMoreRoundedIcon sx={{ fontSize: 18 }} /> : <ChevronRightRoundedIcon sx={{ fontSize: 18 }} />
                ) : null}
              </Box>
              <IconComponent sx={{ fontSize: 19, color: row?.kind === "directory" ? "#8fd3ff" : "#cbd5e1", flexShrink: 0 }} />
              <Tooltip title={row?.path || row?.name || ""} arrow>
                <Typography
                  component="span"
                  sx={{
                    color: MAGIC_UI.textBright,
                    fontWeight: row?.kind === "directory" ? 600 : 500,
                    whiteSpace: "nowrap",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                  }}
                >
                  {row?.name || row?.path || "Unnamed"}
                </Typography>
              </Tooltip>
              {isLoading ? (
                <Typography component="span" sx={{ color: MAGIC_UI.textMuted, fontSize: "0.72rem" }}>
                  Loading...
                </Typography>
              ) : null}
            </Box>
          );
        },
      },
      {
        headerName: "Type",
        field: "type_label",
        minWidth: 140,
      },
      {
        headerName: "Size",
        field: "size_bytes",
        minWidth: 120,
        valueFormatter: (params) => formatBytes(params?.value),
      },
      {
        headerName: "Modified",
        field: "modified_at",
        minWidth: 170,
        valueFormatter: (params) => formatModifiedAt(params?.value),
      },
      {
        headerName: "Attributes",
        field: "attributes_text",
        minWidth: 170,
        valueFormatter: (params) => normalizeText(params?.value) || "—",
      },
    ],
    []
  );

  const gridContext = useMemo(
    () => ({
      toggleExpand,
      expandedPaths,
      loadingPaths,
    }),
    [expandedPaths, loadingPaths, toggleExpand]
  );

  const selectionSummary = selectedRows.length
    ? `${selectedRows.length} selected`
    : `Showing ${currentEntries.length} item${currentEntries.length === 1 ? "" : "s"}`;

  const directorySummary = normalizeText(resolvedUploadTarget)
    ? `Target: ${resolvedUploadTarget}`
    : platform === "windows" && !normalizeText(currentPath)
      ? "Select a folder or drive to upload."
      : `Target: ${normalizeText(currentPath || "/") || "/"}`;

  return (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        gap: 1.5,
        minHeight: 0,
        flexGrow: 1,
      }}
    >
      <input
        ref={fileInputRef}
        type="file"
        multiple
        hidden
        onChange={(event) => {
          void handleUploadSelection(event);
        }}
      />

      {error ? (
        <Alert severity="error" sx={{ borderRadius: 2 }}>
          {error}
        </Alert>
      ) : null}

      <TransferBanner transfers={activeTransfers} />

      <Stack
        direction={{ xs: "column", lg: "row" }}
        spacing={1.25}
        alignItems={{ xs: "stretch", lg: "center" }}
        justifyContent="space-between"
      >
        <Stack spacing={0.8} sx={{ minWidth: 0 }}>
          <Stack direction="row" spacing={0.8} alignItems="center" flexWrap="wrap" useFlexGap>
            <Button
              startIcon={<ArrowUpwardRoundedIcon />}
              onClick={() => {
                void navigateToPath(getParentPath(currentPath || "/", platform));
              }}
              disabled={!canGoUp || !!actionBusy}
              sx={ACTION_BUTTON_SX}
            >
              Up
            </Button>
            <Button
              startIcon={<RefreshRoundedIcon />}
              onClick={() => {
                void refreshBaseView();
              }}
              disabled={!!actionBusy}
              sx={ACTION_BUTTON_SX}
            >
              Refresh
            </Button>
            <Typography
              sx={{
                px: 1.2,
                py: 0.55,
                borderRadius: 999,
                border: "1px solid rgba(148,163,184,0.24)",
                color: "#9bd7ff",
                background: "rgba(5,10,24,0.72)",
                fontSize: "0.78rem",
                fontWeight: 600,
              }}
            >
              {contextLabel || "SYSTEM"} context
            </Typography>
          </Stack>

          <Stack direction="row" spacing={0.5} alignItems="center" flexWrap="wrap" useFlexGap sx={{ minWidth: 0 }}>
            {breadcrumbs.map((crumb, index) => (
              <React.Fragment key={`${crumb.path || "root"}-${index}`}>
                {index ? (
                  <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.8rem" }}>/</Typography>
                ) : null}
                <Button
                  onClick={() => {
                    void navigateToPath(crumb.path);
                  }}
                  sx={{
                    px: 1.2,
                    py: 0.4,
                    minWidth: 0,
                    borderRadius: 999,
                    textTransform: "none",
                    color: normalizeText(crumb.path) === normalizeText(currentPath || (platform === "windows" ? "" : "/"))
                      ? MAGIC_UI.textBright
                      : MAGIC_UI.textMuted,
                    background:
                      normalizeText(crumb.path) === normalizeText(currentPath || (platform === "windows" ? "" : "/"))
                        ? "rgba(56,189,248,0.16)"
                        : "rgba(5,10,24,0.72)",
                    border: "1px solid rgba(148,163,184,0.2)",
                    "&:hover": {
                      background: "rgba(12,20,42,0.9)",
                    },
                  }}
                >
                  {crumb.label}
                </Button>
              </React.Fragment>
            ))}
          </Stack>

          <Stack direction={{ xs: "column", sm: "row" }} spacing={1.2} alignItems={{ xs: "flex-start", sm: "center" }} flexWrap="wrap" useFlexGap>
            <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.82rem" }}>{selectionSummary}</Typography>
            <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.82rem" }}>{directorySummary}</Typography>
          </Stack>
        </Stack>

        <Stack direction="row" spacing={1} alignItems="center" flexWrap="wrap" useFlexGap justifyContent={{ xs: "flex-start", lg: "flex-end" }}>
          <Button
            startIcon={<FileUploadRoundedIcon />}
            onClick={handleOpenUploadPicker}
            disabled={!resolvedUploadTarget || !!actionBusy}
            sx={PRIMARY_ACTION_BUTTON_SX}
          >
            Upload
          </Button>
          <Button
            startIcon={<FileDownloadRoundedIcon />}
            onClick={() => {
              void handleStartDownload();
            }}
            disabled={!selectedRows.length || !!actionBusy}
            sx={PRIMARY_ACTION_BUTTON_SX}
          >
            Download
          </Button>
          <Button
            startIcon={<MoreHorizRoundedIcon />}
            onClick={(event) => setMenuAnchor(event.currentTarget)}
            sx={ACTION_BUTTON_SX}
          >
            Actions
          </Button>
        </Stack>
      </Stack>

      <GridShell
        sx={{
          flexGrow: 1,
          minHeight: 420,
          "--ag-row-hover-color": "rgba(73,156,196,0.2)",
          "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
          "& .ag-row-hover": {
            backgroundColor: "rgba(73,156,196,0.2) !important",
          },
          "& .ag-row-selected": {
            backgroundColor: "rgba(125,211,252,0.2) !important",
            boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
          },
        }}
      >
        <AgGridReact
          ref={gridRef}
          rowData={visibleRows}
          columnDefs={columnDefs}
          defaultColDef={DEFAULT_GRID_COL_DEF}
          rowSelection={{
            mode: "multiRow",
            checkboxes: true,
            headerCheckbox: true,
            enableClickSelection: true,
          }}
          animateRows
          suppressCellFocus
          context={gridContext}
          getRowId={(params) => params?.data?.id || params?.data?.path || ""}
          onSelectionChanged={handleSelectionChanged}
          onSortChanged={handleGridSortChanged}
          onRowDoubleClicked={(params) => {
            if (isDirectory(params?.data)) {
              void navigateToPath(params.data.path);
            }
          }}
          postSortRows={(params) => {
            params.nodes.sort((leftNode, rightNode) => {
              const leftSort = Number(leftNode?.data?.sortIndex || 0);
              const rightSort = Number(rightNode?.data?.sortIndex || 0);
              return leftSort - rightSort;
            });
          }}
          loading={initializing || loadingPaths.has(rootKey)}
          theme={DEVICE_DETAILS_GRID_THEME}
        />
      </GridShell>

      <Menu
        anchorEl={menuAnchor}
        open={Boolean(menuAnchor)}
        onClose={() => setMenuAnchor(null)}
        PaperProps={{
          sx: {
            bgcolor: "rgba(8,12,24,0.96)",
            color: "#fff",
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            minWidth: 220,
          },
        }}
      >
        <MenuItem
          disabled={!resolvedUploadTarget || !!actionBusy}
          onClick={() => {
            setMenuAnchor(null);
            setNewFolderName("");
            setNewFolderOpen(true);
          }}
        >
          <CreateNewFolderRoundedIcon sx={{ mr: 1, fontSize: 18 }} />
          New Folder
        </MenuItem>
        <MenuItem
          disabled={!selectedEntry || !!actionBusy}
          onClick={() => {
            setMenuAnchor(null);
            setRenameValue(normalizeText(selectedEntry?.name));
            setRenameOpen(true);
          }}
        >
          <EditRoundedIcon sx={{ mr: 1, fontSize: 18 }} />
          Rename
        </MenuItem>
        <MenuItem
          disabled={!selectedRows.length || !!actionBusy}
          onClick={() => {
            setMenuAnchor(null);
            setMoveDestination(normalizeText(currentPath || resolvedUploadTarget || (platform === "windows" ? "" : "/")));
            setMoveOpen(true);
          }}
        >
          <DriveFileMoveRoundedIcon sx={{ mr: 1, fontSize: 18 }} />
          Move
        </MenuItem>
        <MenuItem
          disabled={!selectedRows.length || !!actionBusy}
          onClick={() => {
            setMenuAnchor(null);
            setDeleteOpen(true);
          }}
        >
          <DeleteOutlineRoundedIcon sx={{ mr: 1, fontSize: 18 }} />
          Delete
        </MenuItem>
        <MenuItem
          disabled={!expandedPaths.size || !!actionBusy}
          onClick={() => {
            setMenuAnchor(null);
            setExpandedPaths(new Set());
          }}
        >
          <UnfoldLessRoundedIcon sx={{ mr: 1, fontSize: 18 }} />
          Collapse All
        </MenuItem>
      </Menu>

      <Dialog open={newFolderOpen} onClose={() => setNewFolderOpen(false)} fullWidth maxWidth="xs" PaperProps={{ sx: DIALOG_PAPER_SX }}>
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="New Folder"
            subtitle={resolvedUploadTarget ? `Create a new folder in ${resolvedUploadTarget}` : "Select a destination folder first."}
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <TextField
            autoFocus
            fullWidth
            label="Folder Name"
            value={newFolderName}
            onChange={(event) => setNewFolderName(event.target.value)}
            sx={{ ...DIALOG_INPUT_SX, mt: 1.2 }}
          />
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={() => setNewFolderOpen(false)} sx={DIALOG_BUTTON_SX}>
            Cancel
          </Button>
          <Button onClick={() => void handleCreateFolder()} disabled={!newFolderName.trim() || !resolvedUploadTarget || !!actionBusy} sx={DIALOG_PRIMARY_BUTTON_SX}>
            Create
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog open={renameOpen} onClose={() => setRenameOpen(false)} fullWidth maxWidth="xs" PaperProps={{ sx: DIALOG_PAPER_SX }}>
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock title="Rename Item" subtitle={selectedEntry ? `Rename ${selectedEntry.name}` : "Select one item to rename."} />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <TextField
            autoFocus
            fullWidth
            label="New Name"
            value={renameValue}
            onChange={(event) => setRenameValue(event.target.value)}
            sx={{ ...DIALOG_INPUT_SX, mt: 1.2 }}
          />
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={() => setRenameOpen(false)} sx={DIALOG_BUTTON_SX}>
            Cancel
          </Button>
          <Button onClick={() => void handleRename()} disabled={!renameValue.trim() || !selectedEntry || !!actionBusy} sx={DIALOG_PRIMARY_BUTTON_SX}>
            Save
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog open={moveOpen} onClose={() => setMoveOpen(false)} fullWidth maxWidth="sm" PaperProps={{ sx: DIALOG_PAPER_SX }}>
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Move Items"
            subtitle={`Move ${selectedRows.length} item${selectedRows.length === 1 ? "" : "s"} to another absolute path.`}
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <TextField
            autoFocus
            fullWidth
            label="Destination Directory"
            value={moveDestination}
            onChange={(event) => setMoveDestination(event.target.value)}
            sx={{ ...DIALOG_INPUT_SX, mt: 1.2 }}
            helperText={platform === "windows" ? "Use an absolute path such as C:\\Temp" : "Use an absolute path such as /var/tmp"}
          />
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={() => setMoveOpen(false)} sx={DIALOG_BUTTON_SX}>
            Cancel
          </Button>
          <Button onClick={() => void handleMove()} disabled={!moveDestination.trim() || !selectedRows.length || !!actionBusy} sx={DIALOG_PRIMARY_BUTTON_SX}>
            Move
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog open={deleteOpen} onClose={() => setDeleteOpen(false)} fullWidth maxWidth="xs" PaperProps={{ sx: DIALOG_PAPER_SX }}>
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title="Delete Items"
            subtitle={`Delete ${selectedRows.length} item${selectedRows.length === 1 ? "" : "s"} from the remote file system?`}
          />
        </DialogTitle>
        <DialogContent sx={DIALOG_CONTENT_SX}>
          <Typography sx={{ color: MAGIC_UI.textMuted, lineHeight: 1.6 }}>
            This action is recursive for folders and cannot be undone.
          </Typography>
        </DialogContent>
        <DialogActions sx={DIALOG_ACTIONS_SX}>
          <Button onClick={() => setDeleteOpen(false)} sx={DIALOG_BUTTON_SX}>
            Cancel
          </Button>
          <Button onClick={() => void handleDelete()} disabled={!selectedRows.length || !!actionBusy} sx={DIALOG_DANGER_BUTTON_SX}>
            Delete
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
}
