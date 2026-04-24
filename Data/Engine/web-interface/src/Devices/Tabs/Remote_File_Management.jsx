import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import Prism from "prismjs";
import Editor from "react-simple-code-editor";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-batch";
import "prismjs/components/prism-css";
import "prismjs/components/prism-ini";
import "prismjs/components/prism-javascript";
import "prismjs/components/prism-json";
import "prismjs/components/prism-jsx";
import "prismjs/components/prism-markdown";
import "prismjs/components/prism-markup";
import "prismjs/components/prism-powershell";
import "prismjs/components/prism-python";
import "prismjs/components/prism-sql";
import "prismjs/components/prism-tsx";
import "prismjs/components/prism-typescript";
import "prismjs/components/prism-yaml";
import "prismjs/themes/prism-okaidia.css";
import {
  Alert,
  Box,
  Checkbox,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  Icon,
  IconButton,
  InputBase,
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
import FolderRoundedIcon from "@mui/icons-material/FolderRounded";
import InsertDriveFileRoundedIcon from "@mui/icons-material/InsertDriveFileRounded";
import LinkRoundedIcon from "@mui/icons-material/LinkRounded";
import ChevronRightRoundedIcon from "@mui/icons-material/ChevronRightRounded";
import ComputerRoundedIcon from "@mui/icons-material/ComputerRounded";
import ContentCopyRoundedIcon from "@mui/icons-material/ContentCopyRounded";
import AutorenewRoundedIcon from "@mui/icons-material/AutorenewRounded";
import CheckCircleRoundedIcon from "@mui/icons-material/CheckCircleRounded";
import CreateNewFolderRoundedIcon from "@mui/icons-material/CreateNewFolderRounded";
import EditRoundedIcon from "@mui/icons-material/EditRounded";
import DriveFileRenameOutlineRoundedIcon from "@mui/icons-material/DriveFileRenameOutlineRounded";
import DriveFileMoveRoundedIcon from "@mui/icons-material/DriveFileMoveRounded";
import DeleteOutlineRoundedIcon from "@mui/icons-material/DeleteOutlineRounded";
import UnfoldLessRoundedIcon from "@mui/icons-material/UnfoldLessRounded";
import CheckCircleOutlineRoundedIcon from "@mui/icons-material/CheckCircleOutlineRounded";
import SkipNextRoundedIcon from "@mui/icons-material/SkipNextRounded";
import CompareArrowsRoundedIcon from "@mui/icons-material/CompareArrowsRounded";
import CloseRoundedIcon from "@mui/icons-material/CloseRounded";
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

const REFRESH_ICON_BUTTON_SX = {
  width: 42,
  height: 42,
  borderRadius: "50%",
  flexShrink: 0,
  border: "1px solid rgba(148,163,184,0.36)",
  background: "rgba(5,10,24,0.82)",
  color: MAGIC_UI.textBright,
  transition: "background-color 0.18s ease, border-color 0.18s ease, transform 0.18s ease",
  "&:hover": {
    background: "rgba(9,16,34,0.94)",
    borderColor: "rgba(125,211,252,0.46)",
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

const LOADING_STATUS_PILL_SX = {
  display: "inline-flex",
  alignItems: "center",
  gap: 0.45,
  px: 0.7,
  py: 0.24,
  ml: 0.25,
  borderRadius: 999,
  border: "1px solid rgba(74, 222, 128, 0.22)",
  background: "rgba(15, 23, 42, 0.72)",
  color: "#c7f9cc",
  whiteSpace: "nowrap",
};

const ACTION_MENU_PAPER_SX = {
  bgcolor: "rgba(8,12,24,0.96)",
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  backdropFilter: "blur(14px)",
  borderRadius: 2,
  minWidth: 240,
};

const ACTION_MENU_ITEM_SX = {
  fontSize: 13,
  color: MAGIC_UI.textBright,
  minHeight: 38,
};

const UPLOAD_CONFLICT_DIALOG_PAPER_SX = {
  ...DIALOG_PAPER_SX,
  width: "min(560px, 92vw)",
  maxWidth: "min(560px, 92vw)",
  borderRadius: 2.5,
  background: "rgba(20,24,32,0.98)",
};

const UPLOAD_CONFLICT_OPTION_SX = {
  width: "100%",
  display: "flex",
  alignItems: "center",
  gap: 1.2,
  px: 1.5,
  py: 1.2,
  borderRadius: 2,
  border: "1px solid rgba(148,163,184,0.18)",
  background: "rgba(255,255,255,0.02)",
  color: "#f8fafc",
  textAlign: "left",
  cursor: "pointer",
  transition: "background-color 0.16s ease, border-color 0.16s ease, transform 0.16s ease",
  "&:hover": {
    background: "rgba(255,255,255,0.05)",
    borderColor: "rgba(148,163,184,0.32)",
  },
  "&:active": {
    transform: "scale(0.995)",
  },
  "&:disabled": {
    cursor: "not-allowed",
    opacity: 0.48,
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

function formatBrowserModifiedAt(epochMilliseconds) {
  const value = Number(epochMilliseconds || 0);
  if (!value) return "—";
  return formatModifiedAt(value / 1000);
}

function getRootKey(currentPath, platform) {
  return platform === "windows" && !normalizeText(currentPath) ? ROOTS_SENTINEL : normalizeText(currentPath || "/") || "/";
}

function getDisplayedPath(pathValue, platform) {
  if (platform === "windows") {
    return normalizeText(pathValue);
  }
  if (!normalizeText(platform)) {
    return normalizeText(pathValue);
  }
  return normalizeText(pathValue || "/") || "/";
}

function isDirectory(entry) {
  return normalizeText(entry?.kind).toLowerCase() === "directory";
}

function isEditableFileEntry(entry) {
  return Boolean(entry) && !isDirectory(entry);
}

function isWindowsDriveRootEntry(entry, currentPath, platform) {
  if (platform !== "windows" || normalizeText(currentPath) || !isDirectory(entry)) {
    return false;
  }
  const normalizedPath = normalizeText(entry?.path).replace(/\//g, "\\");
  return /^[A-Za-z]:\\?$/.test(normalizedPath);
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

function isTerminalTransferStatus(value) {
  const normalized = normalizeText(value).toLowerCase();
  return normalized === "completed" || normalized === "failed";
}

function normalizeNavigablePath(pathValue, platform) {
  const normalized = normalizeText(pathValue);
  if (platform === "windows") {
    const windowsPath = normalized.replace(/\//g, "\\");
    if (/^[A-Za-z]:$/.test(windowsPath)) {
      return `${windowsPath}\\`;
    }
    return windowsPath;
  }
  return normalized || "/";
}

function buildAddressBarSegments(pathValue, platform) {
  if (platform === "windows") {
    const normalized = normalizeText(pathValue).replace(/\//g, "\\");
    if (!normalized) return [];
    const driveMatch = normalized.match(/^[A-Za-z]:\\/);
    if (driveMatch) {
      let currentPath = driveMatch[0];
      const segments = [{ label: currentPath, path: currentPath }];
      normalized
        .slice(currentPath.length)
        .split("\\")
        .filter(Boolean)
        .forEach((segment) => {
          currentPath = `${currentPath.replace(/[\\]+$/, "")}\\${segment}`;
          segments.push({ label: segment, path: currentPath });
        });
      return segments;
    }
    return [{ label: normalized, path: normalized }];
  }

  const normalized = normalizeText(pathValue || "/") || "/";
  const segments = [];
  if (normalized === "/") return segments;
  let currentPath = "";
  normalized
    .split("/")
    .filter(Boolean)
    .forEach((segment) => {
      currentPath += `/${segment}`;
      segments.push({ label: segment, path: currentPath });
    });
  return segments;
}

function normalizeWorkingDirectoryPath(pathValue, platform) {
  const normalizedPlatform = normalizeText(platform).toLowerCase();
  if (normalizedPlatform === "windows") {
    return normalizeNavigablePath(pathValue, normalizedPlatform);
  }
  return normalizeNavigablePath(pathValue || "/", normalizedPlatform || "linux");
}

function getWorkingDirectoryPath(currentPath, addressPath, platform) {
  const normalizedPlatform = normalizeText(platform).toLowerCase();
  if (normalizedPlatform === "windows") {
    return normalizeWorkingDirectoryPath(addressPath || currentPath, normalizedPlatform);
  }
  return normalizeWorkingDirectoryPath(addressPath || currentPath || "/", normalizedPlatform || "linux");
}

function buildExpandedPathChain(targetPath, basePath, platform) {
  const normalizedPlatform = normalizeText(platform).toLowerCase();
  const normalizedBase = normalizeWorkingDirectoryPath(basePath, normalizedPlatform);
  const normalizedTarget = normalizeWorkingDirectoryPath(targetPath, normalizedPlatform);
  if (!normalizedTarget || normalizedTarget === normalizedBase) {
    return [];
  }

  if (normalizedPlatform === "windows") {
    const driveMatch = normalizedTarget.match(/^[A-Za-z]:\\/);
    if (!driveMatch) {
      return normalizedTarget ? [normalizedTarget] : [];
    }
    let current = driveMatch[0];
    const chain = [current];
    normalizedTarget
      .slice(current.length)
      .split("\\")
      .filter(Boolean)
      .forEach((segment) => {
        current = `${current.replace(/[\\]+$/, "")}\\${segment}`;
        chain.push(current);
      });
    if (!normalizedBase) {
      return chain;
    }
    const basePrefix = `${normalizedBase.replace(/[\\]+$/, "").toLowerCase()}\\`;
    const normalizedBaseKey = normalizedBase.replace(/[\\]+$/, "").toLowerCase();
    return chain.filter((candidate) => {
      const normalizedCandidate = candidate.replace(/[\\]+$/, "").toLowerCase();
      return normalizedCandidate !== normalizedBaseKey && normalizedCandidate.startsWith(basePrefix);
    });
  }

  if (normalizedTarget === "/") {
    return [];
  }
  const chain = [];
  let current = "";
  normalizedTarget
    .split("/")
    .filter(Boolean)
    .forEach((segment) => {
      current += `/${segment}`;
      chain.push(current);
    });
  if (!normalizedBase || normalizedBase === "/") {
    return chain;
  }
  const basePrefix = `${normalizedBase}/`;
  return chain.filter((candidate) => candidate !== normalizedBase && candidate.startsWith(basePrefix));
}

function isPathMissingFetchError(error) {
  const normalizedCode = normalizeText(error?.code).toLowerCase();
  return (
    normalizedCode === "path_not_found" ||
    normalizedCode === "not_found" ||
    normalizedCode === "file_not_found" ||
    normalizedCode === "directory_not_found" ||
    normalizedCode === "invalid_path" ||
    normalizedCode === "not_a_directory"
  );
}

function detectEditorLanguage(pathValue) {
  const normalizedPath = normalizeText(pathValue).toLowerCase();
  if (!normalizedPath) return "markup";
  if (normalizedPath.endsWith(".ps1") || normalizedPath.endsWith(".psm1") || normalizedPath.endsWith(".psd1")) {
    return "powershell";
  }
  if (normalizedPath.endsWith(".bat") || normalizedPath.endsWith(".cmd")) {
    return "batch";
  }
  if (normalizedPath.endsWith(".sh") || normalizedPath.endsWith(".bash") || normalizedPath.endsWith(".zsh")) {
    return "bash";
  }
  if (normalizedPath.endsWith(".yml") || normalizedPath.endsWith(".yaml")) {
    return "yaml";
  }
  if (normalizedPath.endsWith(".json")) {
    return "json";
  }
  if (normalizedPath.endsWith(".py")) {
    return "python";
  }
  if (normalizedPath.endsWith(".tsx")) {
    return "tsx";
  }
  if (normalizedPath.endsWith(".ts")) {
    return "typescript";
  }
  if (normalizedPath.endsWith(".jsx")) {
    return "jsx";
  }
  if (normalizedPath.endsWith(".js") || normalizedPath.endsWith(".mjs") || normalizedPath.endsWith(".cjs")) {
    return "javascript";
  }
  if (normalizedPath.endsWith(".css") || normalizedPath.endsWith(".scss")) {
    return "css";
  }
  if (
    normalizedPath.endsWith(".html") ||
    normalizedPath.endsWith(".htm") ||
    normalizedPath.endsWith(".xml") ||
    normalizedPath.endsWith(".svg")
  ) {
    return "markup";
  }
  if (
    normalizedPath.endsWith(".ini") ||
    normalizedPath.endsWith(".cfg") ||
    normalizedPath.endsWith(".conf") ||
    normalizedPath.endsWith(".service") ||
    normalizedPath.endsWith(".toml") ||
    normalizedPath.endsWith(".properties")
  ) {
    return "ini";
  }
  if (normalizedPath.endsWith(".sql")) {
    return "sql";
  }
  if (normalizedPath.endsWith(".md")) {
    return "markdown";
  }
  return "markup";
}

function formatEditorLanguage(language) {
  const normalized = normalizeText(language).toLowerCase();
  if (!normalized) return "Plain Text";
  return normalized
    .replace(/[_-]+/g, " ")
    .replace(/\b\w/g, (character) => character.toUpperCase());
}

function formatLineEndingLabel(value) {
  const normalized = normalizeText(value).toLowerCase();
  if (normalized === "crlf") return "CRLF";
  if (normalized === "cr") return "CR";
  return "LF";
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

function collapseExpandedBranch(expandedSet, entriesByParent, branchPath) {
  const next = new Set(expandedSet);
  const stack = [normalizeText(branchPath)];
  while (stack.length) {
    const current = stack.pop();
    if (!current) continue;
    next.delete(current);
    (entriesByParent[current] || []).forEach((entry) => {
      if (isDirectory(entry) && normalizeText(entry.path)) {
        stack.push(entry.path);
      }
    });
  }
  return next;
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
  const [searchParams, setSearchParams] = useSearchParams();
  const gridRef = useRef(null);
  const fileInputRef = useRef(null);
  const pathInputRef = useRef(null);
  const handledTransfersRef = useRef({});
  const loadSuccessTimersRef = useRef({});
  const hydratedWorkingDirectoryRef = useRef("");
  const pendingScrollPathRef = useRef("");

  const hostname = useMemo(() => getHostname(device), [device]);
  const [platform, setPlatform] = useState("");
  const [currentPath, setCurrentPath] = useState("");
  const [addressPath, setAddressPath] = useState("");
  const [pathInput, setPathInput] = useState("");
  const [isPathEditing, setIsPathEditing] = useState(false);
  const [entriesByParent, setEntriesByParent] = useState({});
  const [expandedPaths, setExpandedPaths] = useState(() => new Set());
  const [selectedRows, setSelectedRows] = useState([]);
  const [sortModel, setSortModel] = useState([]);
  const [loadingPaths, setLoadingPaths] = useState(() => new Set());
  const [initializing, setInitializing] = useState(true);
  const [error, setError] = useState("");
  const [contextMenuState, setContextMenuState] = useState(null);
  const [newFolderOpen, setNewFolderOpen] = useState(false);
  const [renameOpen, setRenameOpen] = useState(false);
  const [moveOpen, setMoveOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [newFolderName, setNewFolderName] = useState("");
  const [renameValue, setRenameValue] = useState("");
  const [moveDestination, setMoveDestination] = useState("");
  const [actionBusy, setActionBusy] = useState("");
  const [editorOpen, setEditorOpen] = useState(false);
  const [editorLoading, setEditorLoading] = useState(false);
  const [editorSaving, setEditorSaving] = useState(false);
  const [editorError, setEditorError] = useState("");
  const [editorPath, setEditorPath] = useState("");
  const [editorContent, setEditorContent] = useState("");
  const [editorOriginalContent, setEditorOriginalContent] = useState("");
  const [editorLanguage, setEditorLanguage] = useState("markup");
  const [editorEncoding, setEditorEncoding] = useState("utf-8");
  const [editorLineEnding, setEditorLineEnding] = useState("lf");
  const [inlineEditingUnsupported, setInlineEditingUnsupported] = useState(false);
  const [pendingUploadFiles, setPendingUploadFiles] = useState([]);
  const [pendingUploadTarget, setPendingUploadTarget] = useState("");
  const [uploadConflicts, setUploadConflicts] = useState([]);
  const [uploadConflictIndex, setUploadConflictIndex] = useState(0);
  const [uploadConflictSelections, setUploadConflictSelections] = useState({});
  const [uploadConflictCompareOpen, setUploadConflictCompareOpen] = useState(false);
  const [uploadConflictApplyToAll, setUploadConflictApplyToAll] = useState(false);
  const [activeTransfers, setActiveTransfers] = useState({});
  const [rowLoadStateByPath, setRowLoadStateByPath] = useState({});
  const [showHiddenItems, setShowHiddenItems] = useState(false);

  const requestedWorkingDirectory = useMemo(
    () => normalizeText(searchParams.get("working_directory")),
    [searchParams]
  );

  const rootKey = useMemo(() => getRootKey(currentPath, platform), [currentPath, platform]);
  const visibleRows = useMemo(
    () => buildVisibleRows(entriesByParent, rootKey, expandedPaths, sortModel).map((row) => ({ ...row, id: row.path })),
    [entriesByParent, expandedPaths, rootKey, sortModel]
  );
  const renderedRows = useMemo(
    () =>
      showHiddenItems
        ? visibleRows
        : visibleRows.filter((row) => !row?.is_hidden || isWindowsDriveRootEntry(row, currentPath, platform)),
    [currentPath, platform, showHiddenItems, visibleRows]
  );

  const selectedEntry = selectedRows.length === 1 ? selectedRows[0] : null;
  const displayedPath = useMemo(() => getDisplayedPath(addressPath, platform), [addressPath, platform]);
  const addressBarSegments = useMemo(() => buildAddressBarSegments(addressPath, platform), [addressPath, platform]);
  const addressBarRootPath = useMemo(() => (platform === "windows" ? "" : "/"), [platform]);
  const workingDirectoryPath = useMemo(
    () => getWorkingDirectoryPath(currentPath, addressPath, platform),
    [addressPath, currentPath, platform]
  );
  const editorHasChanges = useMemo(() => editorContent !== editorOriginalContent, [editorContent, editorOriginalContent]);
  const currentUploadConflict = uploadConflicts[uploadConflictIndex] || null;
  const currentUploadConflictFile = useMemo(
    () => pendingUploadFiles.find((file) => normalizeText(file?.name) === normalizeText(currentUploadConflict?.name)) || null,
    [currentUploadConflict, pendingUploadFiles]
  );

  const resolvedUploadTarget = useMemo(() => {
    if (selectedRows.length === 1) {
      return isDirectory(selectedRows[0]) ? selectedRows[0].path : normalizeText(selectedRows[0].parent_path);
    }
    if (platform !== "windows" || normalizeText(addressPath)) {
      return normalizeText(addressPath || "/");
    }
    const firstDirectory = selectedRows.find((row) => isDirectory(row));
    return firstDirectory?.path || "";
  }, [addressPath, platform, selectedRows]);

  useEffect(() => {
    setPathInput(displayedPath);
    setIsPathEditing(false);
  }, [displayedPath]);

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
    const targetPath = normalizeText(pendingScrollPathRef.current);
    if (!targetPath || !renderedRows.length) {
      return undefined;
    }
    const rowIndex = renderedRows.findIndex((row) => normalizeText(row?.path) === targetPath);
    if (rowIndex < 0) {
      return undefined;
    }
    pendingScrollPathRef.current = "";
    const animationFrameId = window.requestAnimationFrame(() => {
      gridRef.current?.api?.ensureIndexVisible?.(rowIndex, "top");
    });
    return () => window.cancelAnimationFrame(animationFrameId);
  }, [renderedRows]);

  useEffect(() => {
    const normalizedPlatform = normalizeText(platform).toLowerCase();
    if (!normalizedPlatform) return;
    const serializedWorkingDirectory =
      normalizedPlatform === "windows"
        ? normalizeText(workingDirectoryPath)
        : normalizeText(workingDirectoryPath || "/") || "/";
    const shouldClear =
      (normalizedPlatform === "windows" && !serializedWorkingDirectory) ||
      (normalizedPlatform !== "windows" && serializedWorkingDirectory === "/");
    const currentValue = normalizeText(searchParams.get("working_directory"));
    if ((shouldClear && !currentValue) || (!shouldClear && currentValue === serializedWorkingDirectory)) {
      return;
    }
    const nextParams = new URLSearchParams(searchParams);
    if (shouldClear) {
      nextParams.delete("working_directory");
    } else {
      nextParams.set("working_directory", serializedWorkingDirectory);
    }
    hydratedWorkingDirectoryRef.current = `${hostname}::${shouldClear ? "" : serializedWorkingDirectory}`;
    setSearchParams(nextParams, { replace: true });
  }, [hostname, platform, searchParams, setSearchParams, workingDirectoryPath]);

  const fetchRootsView = useCallback(async () => {
    const response = await fetch(`/api/device/files/${encodeURIComponent(hostname)}/roots`, {
      credentials: "include",
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) {
      throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
    }
    const normalizedPlatform = normalizeText(data?.platform).toLowerCase() || "linux";
    return {
      platform: normalizedPlatform,
      currentPath: normalizedPlatform === "windows" ? "" : normalizeText(data?.current_path || "/") || "/",
      entries: Array.isArray(data?.entries) ? data.entries.map((row) => normalizeEntry(row)) : [],
    };
  }, [hostname]);

  const fetchChildrenView = useCallback(
    async (pathValue) => {
      const normalizedPath = normalizeText(pathValue);
      const response = await fetch(
        `/api/device/files/${encodeURIComponent(hostname)}/children?path=${encodeURIComponent(normalizedPath)}`,
        { credentials: "include" }
      );
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        const fetchError = new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
        fetchError.code = normalizeText(data?.error).toLowerCase();
        fetchError.status = Number(response.status || 0);
        throw fetchError;
      }
      return {
        entries: Array.isArray(data?.entries) ? data.entries.map((row) => normalizeEntry(row)) : [],
      };
    },
    [hostname]
  );

  const restoreWorkingDirectoryView = useCallback(
    async ({
      basePath,
      workingPath,
      platformOverride,
      resetSelection = true,
      scrollToWorkingPath = false,
    }) => {
      if (!hostname) return;
      const normalizedPlatform = normalizeText(platformOverride || platform).toLowerCase() || "linux";
      const normalizedBasePath =
        normalizedPlatform === "windows"
          ? normalizeWorkingDirectoryPath(basePath, normalizedPlatform)
          : normalizeWorkingDirectoryPath(basePath || "/", normalizedPlatform);
      const normalizedWorkingPath = getWorkingDirectoryPath(
        normalizedBasePath,
        workingPath,
        normalizedPlatform
      );
      const nextRootKey = getRootKey(normalizedBasePath, normalizedPlatform);

      if (resetSelection) {
        setSelectedRows([]);
        gridRef.current?.api?.deselectAll?.();
      }

      setError("");
      setLoadingPaths((previous) => {
        const next = new Set(previous);
        next.add(nextRootKey);
        return next;
      });

      try {
        const rootPayload =
          normalizedPlatform === "windows" && !normalizeText(normalizedBasePath)
            ? await fetchRootsView()
            : await fetchChildrenView(normalizedBasePath);
        const nextEntriesByParent = {
          [nextRootKey]: Array.isArray(rootPayload?.entries) ? rootPayload.entries : [],
        };
        const nextExpandedPaths = new Set();
        const expansionChain = buildExpandedPathChain(normalizedWorkingPath, normalizedBasePath, normalizedPlatform);
        let resolvedWorkingPath = normalizedBasePath;

        setPlatform(normalizedPlatform);
        setCurrentPath(normalizedBasePath);
        setAddressPath(normalizedWorkingPath || normalizedBasePath);
        setEntriesByParent({ ...nextEntriesByParent });
        setExpandedPaths(new Set());

        for (const expandedPath of expansionChain) {
          try {
            const childPayload = await fetchChildrenView(expandedPath);
            nextEntriesByParent[expandedPath] = Array.isArray(childPayload?.entries) ? childPayload.entries : [];
            nextExpandedPaths.add(expandedPath);
            resolvedWorkingPath = expandedPath;
          } catch (fetchError) {
            if (isPathMissingFetchError(fetchError)) {
              break;
            }
            throw fetchError;
          }
        }

        setAddressPath(resolvedWorkingPath || normalizedBasePath);
        setEntriesByParent({ ...nextEntriesByParent });
        setExpandedPaths(nextExpandedPaths);
        if (scrollToWorkingPath && normalizeText(resolvedWorkingPath) && resolvedWorkingPath !== normalizedBasePath) {
          pendingScrollPathRef.current = resolvedWorkingPath;
        }
        setError("");
      } catch (fetchError) {
        setError(String(fetchError?.message || fetchError));
      } finally {
        setLoadingPaths((previous) => {
          const next = new Set(previous);
          next.delete(nextRootKey);
          return next;
        });
      }
    },
    [fetchChildrenView, fetchRootsView, hostname, platform]
  );

  const refreshBaseView = useCallback(async () => {
    if (!hostname) return;
    await restoreWorkingDirectoryView({
      basePath: currentPath,
      workingPath: workingDirectoryPath,
      platformOverride: platform,
      resetSelection: true,
      scrollToWorkingPath: true,
    });
  }, [currentPath, hostname, platform, restoreWorkingDirectoryView, workingDirectoryPath]);

  useEffect(() => {
    let canceled = false;

    async function loadInitial() {
      if (!hostname) {
        setInitializing(false);
        return;
      }
      setInitializing(true);
      try {
        const hydrateKey = `${hostname}::${requestedWorkingDirectory}`;
        if (hydratedWorkingDirectoryRef.current === hydrateKey) {
          setInitializing(false);
          return;
        }
        const rootPayload = await fetchRootsView();
        if (canceled) return;
        const normalizedPlatform = rootPayload.platform;
        const basePath = rootPayload.currentPath;
        const nextWorkingDirectory = normalizeWorkingDirectoryPath(requestedWorkingDirectory, normalizedPlatform);
        const shouldRestoreWorkingDirectory =
          (normalizedPlatform === "windows" && Boolean(nextWorkingDirectory)) ||
          (normalizedPlatform !== "windows" && nextWorkingDirectory !== "/");
        if (shouldRestoreWorkingDirectory) {
          await restoreWorkingDirectoryView({
            basePath: normalizedPlatform === "windows" ? "" : "/",
            workingPath: nextWorkingDirectory,
            platformOverride: normalizedPlatform,
            resetSelection: false,
            scrollToWorkingPath: true,
          });
        } else {
          setPlatform(normalizedPlatform);
          setCurrentPath(basePath);
          setAddressPath(basePath);
          setEntriesByParent({
            [getRootKey(basePath, normalizedPlatform)]: rootPayload.entries,
          });
          setExpandedPaths(new Set());
          setError("");
        }
        hydratedWorkingDirectoryRef.current = hydrateKey;
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
  }, [fetchRootsView, hostname, requestedWorkingDirectory, restoreWorkingDirectoryView]);

  const loadChildrenForPath = useCallback(
    async (pathValue) => {
      const normalizedPath = normalizeText(pathValue);
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
        setEntriesByParent((previous) => ({
          ...previous,
          [normalizedPath]: Array.isArray(data?.entries) ? data.entries : [],
        }));
        setRowLoadStateByPath((previous) => ({ ...previous, [normalizedPath]: "loaded" }));
        loadSuccessTimersRef.current[normalizedPath] = window.setTimeout(() => {
          setRowLoadStateByPath((previous) => {
            const next = { ...previous };
            delete next[normalizedPath];
            return next;
          });
          delete loadSuccessTimersRef.current[normalizedPath];
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
          return next;
        });
      }
    },
    [fetchChildrenView, hostname]
  );

  const navigateToPath = useCallback(
    async (pathValue) => {
      const nextPath = platform === "windows" ? normalizeText(pathValue) : normalizeText(pathValue || "/") || "/";
      setCurrentPath(nextPath);
      setAddressPath(nextPath);
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
          return collapseExpandedBranch(previous, entriesByParent, pathValue);
        });
        setAddressPath(normalizeText(row.parent_path) || currentPath || (platform === "windows" ? "" : "/"));
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
        const siblingParentKey = normalizeText(row.parent_path) || getRootKey(currentPath, platform);
        (entriesByParent[siblingParentKey] || []).forEach((sibling) => {
          const siblingPath = normalizeText(sibling?.path);
          if (!siblingPath || siblingPath === pathValue || !isDirectory(sibling) || !next.has(siblingPath)) return;
          next = collapseExpandedBranch(next, entriesByParent, siblingPath);
        });
        next.add(pathValue);
        return next;
      });
      setAddressPath(pathValue);
    },
    [currentPath, entriesByParent, expandedPaths, loadChildrenForPath, platform]
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

  const highlightCode = useCallback((code, language) => {
    try {
      return Prism.highlight(code ?? "", Prism.languages[language] || Prism.languages.markup, language || "markup");
    } catch {
      return String(code || "");
    }
  }, []);

  const handleCloseEditor = useCallback(() => {
    setEditorOpen(false);
    setEditorLoading(false);
    setEditorSaving(false);
    setEditorError("");
  }, []);

  const handleOpenEditor = useCallback(
    async (entryOverride = null) => {
      const entry = entryOverride || selectedEntry;
      if (!hostname || !isEditableFileEntry(entry)) return;
      const targetPath = normalizeText(entry?.path);
      if (!targetPath) return;
      setEditorOpen(true);
      setEditorLoading(true);
      setEditorSaving(false);
      setEditorError("");
      setEditorPath(targetPath);
      setEditorContent("");
      setEditorOriginalContent("");
      setEditorLanguage(detectEditorLanguage(targetPath));
      setEditorEncoding("utf-8");
      setEditorLineEnding("lf");
      try {
        const response = await fetch(
          `/api/device/files/${encodeURIComponent(hostname)}/text?path=${encodeURIComponent(targetPath)}`,
          { credentials: "include" }
        );
        const data = await response.json().catch(() => ({}));
        if (!response.ok) {
          const apiMessage = normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`;
          if (
            normalizeText(data?.error).toLowerCase() === "agent_update_required" ||
            apiMessage.includes("Unsupported file-management action 'read_text'")
          ) {
            setInlineEditingUnsupported(true);
            throw new Error("This device agent needs to be updated before inline text editing is available.");
          }
          throw new Error(apiMessage);
        }
        setInlineEditingUnsupported(false);
        const nextPath = normalizeText(data?.path) || targetPath;
        const nextContent = typeof data?.content === "string" ? data.content : "";
        setEditorPath(nextPath);
        setEditorContent(nextContent);
        setEditorOriginalContent(nextContent);
        setEditorEncoding(normalizeText(data?.encoding) || "utf-8");
        setEditorLineEnding(normalizeText(data?.line_ending) || "lf");
        setEditorLanguage(detectEditorLanguage(nextPath));
      } catch (openError) {
        const message = String(openError?.message || openError);
        setEditorError(message);
        await notifyOperator({
          title: "Edit Failed",
          message,
          icon: "error",
          variant: "error",
        });
      } finally {
        setEditorLoading(false);
      }
    },
    [hostname, notifyOperator, selectedEntry]
  );

  const handleSaveEditor = useCallback(async () => {
    const targetPath = normalizeText(editorPath);
    if (!hostname || !targetPath || editorLoading || editorSaving) return;
    setEditorSaving(true);
    setEditorError("");
    try {
      const response = await fetch(`/api/device/files/${encodeURIComponent(hostname)}/text`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          path: targetPath,
          content: editorContent,
          encoding: editorEncoding,
          line_ending: editorLineEnding,
        }),
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        const apiMessage = normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`;
        if (
          normalizeText(data?.error).toLowerCase() === "agent_update_required" ||
          apiMessage.includes("Unsupported file-management action 'write_text'")
        ) {
          setInlineEditingUnsupported(true);
          throw new Error("This device agent needs to be updated before inline text editing is available.");
        }
        throw new Error(apiMessage);
      }
      setInlineEditingUnsupported(false);
      setEditorOriginalContent(editorContent);
      setEditorEncoding(normalizeText(data?.encoding) || editorEncoding);
      setEditorLineEnding(normalizeText(data?.line_ending) || editorLineEnding);
      await notifyOperator({
        title: "File Saved",
        message: `Saved ${targetPath}.`,
        icon: "success",
        variant: "success",
      });
      await refreshBaseView();
    } catch (saveError) {
      const message = String(saveError?.message || saveError);
      setEditorError(message);
      await notifyOperator({
        title: "Save Failed",
        message,
        icon: "error",
        variant: "error",
      });
    } finally {
      setEditorSaving(false);
    }
  }, [
    editorContent,
    editorEncoding,
    editorLineEnding,
    editorLoading,
    editorPath,
    editorSaving,
    hostname,
    notifyOperator,
    refreshBaseView,
  ]);

  const clearUploadConflictState = useCallback(() => {
    setPendingUploadFiles([]);
    setPendingUploadTarget("");
    setUploadConflicts([]);
    setUploadConflictIndex(0);
    setUploadConflictSelections({});
    setUploadConflictCompareOpen(false);
    setUploadConflictApplyToAll(false);
  }, []);

  const openUploadConflictDialog = useCallback((files, targetPath, conflicts, existingSelections = {}) => {
    const normalizedConflicts = Array.isArray(conflicts) ? conflicts : [];
    const firstPendingIndex = Math.max(
      0,
      normalizedConflicts.findIndex((conflict) => !["replace", "skip"].includes(normalizeText(existingSelections?.[conflict?.name]).toLowerCase()))
    );
    setPendingUploadFiles(Array.isArray(files) ? files : []);
    setPendingUploadTarget(normalizeText(targetPath));
    setUploadConflicts(normalizedConflicts);
    setUploadConflictSelections(existingSelections || {});
    setUploadConflictIndex(firstPendingIndex >= 0 ? firstPendingIndex : 0);
    setUploadConflictCompareOpen(false);
    setUploadConflictApplyToAll(false);
  }, []);

  const startUploadRequest = useCallback(
    async (files, targetPath, conflictSelections = {}) => {
      const uploadFiles = Array.isArray(files) ? files : [];
      const normalizedTarget = normalizeText(targetPath);
      if (!uploadFiles.length || !hostname || !normalizedTarget) return;
      setActionBusy("upload");
      try {
        const formData = new FormData();
        formData.append("target_path", normalizedTarget);
        if (Object.keys(conflictSelections || {}).length) {
          formData.append("conflict_resolutions", JSON.stringify(conflictSelections));
        }
        uploadFiles.forEach((file) => {
          formData.append("files", file, file.name);
        });
        const response = await fetch(`/api/device/files/${encodeURIComponent(hostname)}/upload`, {
          method: "POST",
          credentials: "include",
          body: formData,
        });
        const data = await response.json().catch(() => ({}));
        if (response.status === 409 && normalizeText(data?.error).toLowerCase() === "upload_conflicts") {
          openUploadConflictDialog(uploadFiles, normalizedTarget, data?.conflicts || [], conflictSelections);
          return;
        }
        if (response.status === 409 && normalizeText(data?.error).toLowerCase() === "agent_update_required") {
          throw new Error("This device agent needs to be updated before duplicate upload resolution is available.");
        }
        if (!response.ok) {
          throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
        }
        if (normalizeText(data?.status).toLowerCase() === "skipped") {
          await notifyOperator({
            title: "Upload Skipped",
            message: `Skipped ${Number(data?.skipped_count || 0)} duplicate item${Number(data?.skipped_count || 0) === 1 ? "" : "s"} in ${normalizedTarget}.`,
            icon: "notification",
            variant: "info",
          });
          await refreshBaseView();
          clearUploadConflictState();
          return;
        }
        setActiveTransfers((previous) => ({ ...previous, [data.transfer_id]: data }));
        await notifyOperator({
          title: "Upload Started",
          message: `Uploading ${Number(data?.item_count || uploadFiles.length)} item${Number(data?.item_count || uploadFiles.length) === 1 ? "" : "s"} to ${normalizedTarget}.`,
          icon: "upload",
          variant: "info",
        });
        clearUploadConflictState();
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
    [clearUploadConflictState, hostname, notifyOperator, openUploadConflictDialog, refreshBaseView]
  );

  const handleUploadConflictChoice = useCallback(
    async (choice) => {
      const normalizedChoice = normalizeText(choice).toLowerCase();
      const currentConflict = uploadConflicts[uploadConflictIndex];
      if (!currentConflict || !["replace", "skip"].includes(normalizedChoice)) return;
      const nextSelections = { ...uploadConflictSelections };
      if (uploadConflictApplyToAll) {
        uploadConflicts.slice(uploadConflictIndex).forEach((conflict) => {
          nextSelections[conflict.name] = normalizedChoice;
        });
        setUploadConflictSelections(nextSelections);
        await startUploadRequest(pendingUploadFiles, pendingUploadTarget, nextSelections);
        return;
      }
      nextSelections[currentConflict.name] = normalizedChoice;
      const nextIndex = uploadConflictIndex + 1;
      if (nextIndex >= uploadConflicts.length) {
        setUploadConflictSelections(nextSelections);
        await startUploadRequest(pendingUploadFiles, pendingUploadTarget, nextSelections);
        return;
      }
      setUploadConflictSelections(nextSelections);
      setUploadConflictIndex(nextIndex);
      setUploadConflictCompareOpen(false);
    },
    [
      pendingUploadFiles,
      pendingUploadTarget,
      startUploadRequest,
      uploadConflictApplyToAll,
      uploadConflictIndex,
      uploadConflictSelections,
      uploadConflicts,
    ]
  );

  const handleSelectionChanged = useCallback(() => {
    const rows = gridRef.current?.api?.getSelectedRows?.() || [];
    setSelectedRows(rows);
  }, []);

  const handleGridSortChanged = useCallback(() => {
    const nextSortModel = gridRef.current?.api?.getSortModel?.() || [];
    setSortModel(nextSortModel);
  }, []);

  const handleGridCellClicked = useCallback(
    (params) => {
      if (
        !isDirectory(params?.data) ||
        isGridInteractiveClick(params?.event) ||
        Number(params?.event?.detail || 1) > 1
      ) {
        return;
      }
      void toggleExpand(params.data);
    },
    [toggleExpand]
  );

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
        const response = await fetch(`/api/device/files/${encodeURIComponent(hostname)}/upload/conflicts`, {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            target_path: resolvedUploadTarget,
            items: files.map((file) => ({
              name: file.name,
              size_bytes: Number(file.size || 0),
              modified_at: Math.floor(Number(file.lastModified || 0) / 1000),
            })),
          }),
        });
        const data = await response.json().catch(() => ({}));
        if (!response.ok) {
          throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
        }
        if (data?.capability_supported === false) {
          await notifyOperator({
            title: "Legacy Upload Mode",
            message: "This device agent does not support duplicate upload previews yet. Uploading with legacy behavior.",
            icon: "notification",
            variant: "info",
          });
        }
        const conflicts = Array.isArray(data?.conflicts) ? data.conflicts : [];
        if (conflicts.length) {
          openUploadConflictDialog(files, resolvedUploadTarget, conflicts, {});
          return;
        }
        await startUploadRequest(files, resolvedUploadTarget, {});
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
    [hostname, notifyOperator, openUploadConflictDialog, resolvedUploadTarget, startUploadRequest]
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
      setContextMenuState(null);
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
      setContextMenuState(null);
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
      setContextMenuState(null);
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
      setContextMenuState(null);
    }
  }, [executeJsonAction, selectedRows]);

  const handleAddressBarNavigate = useCallback(async () => {
    const nextPath = normalizeNavigablePath(pathInput, platform);
    if (!hostname) return;
    setIsPathEditing(false);
    await navigateToPath(nextPath);
  }, [hostname, navigateToPath, pathInput, platform]);

  const handleEnablePathEditing = useCallback(() => {
    setPathInput(displayedPath);
    setIsPathEditing(true);
  }, [displayedPath]);

  const handleCancelPathEditing = useCallback(() => {
    setPathInput(displayedPath);
    setIsPathEditing(false);
  }, [displayedPath]);

  const handleCopyPath = useCallback(async () => {
    const valueToCopy = normalizeText(isPathEditing ? pathInput : displayedPath);
    if (!valueToCopy) return;
    try {
      await navigator?.clipboard?.writeText?.(valueToCopy);
    } catch {
      // Ignore clipboard errors silently so the bar remains lightweight.
    }
  }, [displayedPath, isPathEditing, pathInput]);

  const handleCloseContextMenu = useCallback(() => {
    setContextMenuState(null);
  }, []);

  const handleOpenContextMenuAtPointer = useCallback((event) => {
    if (!event) return;
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
      handleOpenContextMenuAtPointer(event);
    },
    [handleOpenContextMenuAtPointer]
  );

  const rowSelection = useMemo(
    () => ({
      mode: "multiRow",
      checkboxes: true,
      headerCheckbox: true,
      enableClickSelection: false,
    }),
    []
  );

  const selectionColumnDef = useMemo(
    () => ({
      headerName: "",
      width: 52,
      maxWidth: 52,
      minWidth: 52,
      pinned: "left",
      resizable: false,
      sortable: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      suppressMovable: true,
      lockPinned: true,
      lockPosition: true,
    }),
    []
  );

  const renderActionMenuItems = (closeMenu) => [
    <MenuItem
      key="upload"
      disabled={!resolvedUploadTarget || !!actionBusy}
      onClick={() => {
        closeMenu();
        handleOpenUploadPicker();
      }}
      sx={ACTION_MENU_ITEM_SX}
    >
      <FileUploadRoundedIcon sx={{ mr: 1, fontSize: 18 }} />
      Upload
    </MenuItem>,
    <MenuItem
      key="download"
      disabled={!selectedRows.length || !!actionBusy}
      onClick={() => {
        closeMenu();
        void handleStartDownload();
      }}
      sx={ACTION_MENU_ITEM_SX}
    >
      <FileDownloadRoundedIcon sx={{ mr: 1, fontSize: 18 }} />
      Download
    </MenuItem>,
    <MenuItem
      key="edit"
      disabled={!isEditableFileEntry(selectedEntry) || !!actionBusy || editorLoading || editorSaving || inlineEditingUnsupported}
      onClick={() => {
        closeMenu();
        void handleOpenEditor();
      }}
      sx={ACTION_MENU_ITEM_SX}
    >
      <EditRoundedIcon sx={{ mr: 1, fontSize: 18 }} />
      Edit
    </MenuItem>,
    <MenuItem
      key="rename"
      disabled={!selectedEntry || !!actionBusy}
      onClick={() => {
        closeMenu();
        setRenameValue(normalizeText(selectedEntry?.name));
        setRenameOpen(true);
      }}
      sx={ACTION_MENU_ITEM_SX}
    >
      <DriveFileRenameOutlineRoundedIcon sx={{ mr: 1, fontSize: 18 }} />
      Rename
    </MenuItem>,
    <MenuItem
      key="delete"
      disabled={!selectedRows.length || !!actionBusy}
      onClick={() => {
        closeMenu();
        setDeleteOpen(true);
      }}
      sx={ACTION_MENU_ITEM_SX}
    >
      <DeleteOutlineRoundedIcon sx={{ mr: 1, fontSize: 18 }} />
      Delete
    </MenuItem>,
    <MenuItem
      key="move"
      disabled={!selectedRows.length || !!actionBusy}
      onClick={() => {
        closeMenu();
        setMoveDestination(normalizeText(currentPath || resolvedUploadTarget || (platform === "windows" ? "" : "/")));
        setMoveOpen(true);
      }}
      sx={ACTION_MENU_ITEM_SX}
    >
      <DriveFileMoveRoundedIcon sx={{ mr: 1, fontSize: 18 }} />
      Move
    </MenuItem>,
    <MenuItem
      key="new-folder"
      disabled={!resolvedUploadTarget || !!actionBusy}
      onClick={() => {
        closeMenu();
        setNewFolderName("");
        setNewFolderOpen(true);
      }}
      sx={ACTION_MENU_ITEM_SX}
    >
      <CreateNewFolderRoundedIcon sx={{ mr: 1, fontSize: 18 }} />
      New Folder
    </MenuItem>,
    <MenuItem
      key="collapse-all"
      disabled={!expandedPaths.size || !!actionBusy}
      onClick={() => {
        closeMenu();
        setExpandedPaths(new Set());
      }}
      sx={ACTION_MENU_ITEM_SX}
    >
      <UnfoldLessRoundedIcon sx={{ mr: 1, fontSize: 18 }} />
      Collapse All
    </MenuItem>,
  ];

  const columnDefs = useMemo(
    () => [
      {
        headerName: "Name",
        field: "name",
        flex: 1.6,
        minWidth: 260,
        cellStyle: {
          display: "flex",
          alignItems: "center",
        },
        cellRenderer: (params) => {
          const row = params?.data || {};
          const loadState = params?.context?.rowLoadStateByPath?.[row.path] || "";
          const canExpand = isDirectory(row);
          const isDriveRoot = isWindowsDriveRootEntry(row, currentPath, platform) && Number(row?.depth || 0) === 0;
          const IconComponent =
            row?.kind === "directory"
                ? FolderRoundedIcon
              : row?.kind === "symlink"
                ? LinkRoundedIcon
                : InsertDriveFileRoundedIcon;
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
              {isDriveRoot ? (
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
                  hard_drive
                </Icon>
              ) : (
                <IconComponent
                  sx={{
                    fontSize: 29,
                    color: row?.kind === "directory" ? "#8fd3ff" : "#cbd5e1",
                    flexShrink: 0,
                  }}
                />
              )}
              <Box
                component={canExpand ? "button" : "span"}
                type={canExpand ? "button" : undefined}
                onClick={
                  canExpand
                    ? (event) => {
                        event.stopPropagation();
                        params?.context?.toggleExpand?.(row);
                      }
                    : undefined
                }
                sx={{
                  border: 0,
                  background: "transparent",
                  color: "inherit",
                  cursor: canExpand ? "pointer" : "default",
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
                    whiteSpace: "nowrap",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                  }}
                >
                  {row?.name || row?.path || "Unnamed"}
                </Typography>
              </Box>
              {loadState ? (
                <Box sx={LOADING_STATUS_PILL_SX}>
                  {loadState === "loading" ? (
                    <AutorenewRoundedIcon
                      sx={{
                        fontSize: 15,
                        color: "#9bd7ff",
                        animation: "remoteFileManagementSpin 1.15s linear infinite",
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
        headerName: "Size",
        field: "size_bytes",
        minWidth: 120,
        valueFormatter: (params) => formatBytes(params?.value),
      },
      {
        headerName: "Last Modified",
        field: "modified_at",
        minWidth: 170,
        valueFormatter: (params) => formatModifiedAt(params?.value),
      },
    ],
    [currentPath, platform]
  );

  const gridContext = useMemo(
    () => ({
      toggleExpand,
      expandedPaths,
      loadingPaths,
      rowLoadStateByPath,
    }),
    [expandedPaths, loadingPaths, rowLoadStateByPath, toggleExpand]
  );

  return (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        gap: 1.5,
        minHeight: 0,
        flexGrow: 1,
        "@keyframes remoteFileManagementSpin": {
          "0%": { transform: "rotate(0deg)" },
          "100%": { transform: "rotate(360deg)" },
        },
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

      <Stack spacing={0.8} sx={{ minWidth: 0 }}>
        <Stack direction="row" spacing={1} alignItems="center" sx={{ minWidth: 0 }}>
          <IconButton
            aria-label="Refresh file listing"
            onClick={() => {
              void refreshBaseView();
            }}
            disabled={!!actionBusy}
            sx={REFRESH_ICON_BUTTON_SX}
          >
            <RefreshRoundedIcon sx={{ fontSize: 20 }} />
          </IconButton>
          <Box sx={ADDRESS_BAR_SHELL_SX}>
            <Box
              component="button"
              type="button"
              aria-label={platform === "windows" ? "Go to drive roots" : "Go to root directory"}
              onClick={() => {
                void navigateToPath(addressBarRootPath);
              }}
              sx={ADDRESS_BAR_ROOT_BUTTON_SX}
            >
              <ComputerRoundedIcon sx={{ color: "currentColor", fontSize: 19, flexShrink: 0 }} />
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
                placeholder={platform === "windows" ? "This PC" : "/"}
                autoComplete="off"
                fullWidth
                sx={ADDRESS_BAR_INPUT_SX}
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
                  ) : (
                    platform === "windows" ? null : (
                      <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.92rem" }}>
                        Root
                      </Typography>
                    )
                  )}
                </Box>
                <Box
                  component="button"
                  type="button"
                  aria-label="Edit path"
                  onClick={handleEnablePathEditing}
                  sx={ADDRESS_BAR_EMPTY_SPACE_BUTTON_SX}
                />
              </>
            )}
            <Tooltip title={normalizeText(isPathEditing ? pathInput : displayedPath) ? "Copy path" : "Nothing to copy"} arrow>
              <span>
                <IconButton
                  size="small"
                  onClick={() => {
                    void handleCopyPath();
                  }}
                  disabled={!normalizeText(isPathEditing ? pathInput : displayedPath)}
                  sx={ADDRESS_BAR_COPY_BUTTON_SX}
                >
                  <ContentCopyRoundedIcon sx={{ fontSize: 18 }} />
                </IconButton>
              </span>
            </Tooltip>
          </Box>
        </Stack>
        <FormControlLabel
          control={
            <Checkbox
              checked={showHiddenItems}
              onChange={(event) => setShowHiddenItems(event.target.checked)}
              size="small"
              sx={{
                color: "rgba(148,163,184,0.86)",
                "&.Mui-checked": {
                  color: "#7dd3fc",
                },
                "& .MuiSvgIcon-root": {
                  fontSize: 18,
                },
              }}
            />
          }
          label="Show Hidden Items"
          sx={{
            mt: -0.1,
            ml: 0.15,
            userSelect: "none",
            "& .MuiFormControlLabel-label": {
              color: MAGIC_UI.textMuted,
              fontSize: "0.84rem",
              fontWeight: 500,
            },
          }}
        />
      </Stack>

      <GridShell
        onContextMenu={handleGridShellContextMenu}
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
          rowData={renderedRows}
          columnDefs={columnDefs}
          defaultColDef={DEFAULT_GRID_COL_DEF}
          rowSelection={rowSelection}
          selectionColumnDef={selectionColumnDef}
          animateRows
          suppressCellFocus
          suppressContextMenu
          preventDefaultOnContextMenu
          context={gridContext}
          getRowId={(params) => params?.data?.id || params?.data?.path || ""}
          onCellContextMenu={handleGridCellContextMenu}
          onCellClicked={handleGridCellClicked}
          onSelectionChanged={handleSelectionChanged}
          onSortChanged={handleGridSortChanged}
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

      <Dialog
        open={Boolean(uploadConflicts.length && currentUploadConflict)}
        onClose={() => {
          if (!actionBusy) {
            clearUploadConflictState();
          }
        }}
        fullWidth
        maxWidth="sm"
        PaperProps={{ sx: UPLOAD_CONFLICT_DIALOG_PAPER_SX }}
      >
        <DialogTitle
          sx={{
            ...DIALOG_TITLE_SX,
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: 2,
            pb: 1.2,
          }}
        >
          <Typography sx={{ color: "#f8fafc", fontSize: "1rem", fontWeight: 600 }}>Replace or Skip Files</Typography>
          <IconButton
            size="small"
            onClick={clearUploadConflictState}
            disabled={Boolean(actionBusy)}
            sx={{ color: "rgba(226,232,240,0.82)" }}
          >
            <CloseRoundedIcon sx={{ fontSize: 18 }} />
          </IconButton>
        </DialogTitle>
        <DialogContent sx={{ ...DIALOG_CONTENT_SX, pt: 0.25 }}>
          {currentUploadConflict ? (
            <Stack spacing={1.45}>
              <Typography sx={{ color: "rgba(226,232,240,0.72)", fontSize: "0.82rem" }}>
                Copying {pendingUploadFiles.length} item{pendingUploadFiles.length === 1 ? "" : "s"} to{" "}
                {normalizeText(pendingUploadTarget).split(/[\\/]/).filter(Boolean).pop() || pendingUploadTarget || "the destination"}
              </Typography>
              <Box>
                <Typography sx={{ color: "#f8fafc", fontSize: "1.28rem", fontWeight: 500, lineHeight: 1.32 }}>
                  The destination already has a file named "{currentUploadConflict.name}"
                </Typography>
                <Typography sx={{ color: "rgba(226,232,240,0.74)", fontSize: "0.84rem", mt: 0.7 }}>
                  Incoming file last modified{" "}
                  {currentUploadConflictFile
                    ? formatBrowserModifiedAt(currentUploadConflictFile?.lastModified)
                    : formatModifiedAt(currentUploadConflict?.upload_modified_at)}
                  . Existing file last modified {formatModifiedAt(currentUploadConflict?.destination?.modified_at)}.
                </Typography>
              </Box>

              <Stack spacing={1}>
                <Box
                  component="button"
                  type="button"
                  onClick={() => {
                    void handleUploadConflictChoice("replace");
                  }}
                  disabled={
                    Boolean(actionBusy) ||
                    currentUploadConflict?.replace_supported === false ||
                    (uploadConflictApplyToAll &&
                      uploadConflicts.slice(uploadConflictIndex).some((conflict) => conflict?.replace_supported === false))
                  }
                  sx={UPLOAD_CONFLICT_OPTION_SX}
                >
                  <CheckCircleOutlineRoundedIcon sx={{ color: "#4ade80", fontSize: 21, flexShrink: 0 }} />
                  <Box sx={{ minWidth: 0 }}>
                    <Typography sx={{ color: "inherit", fontSize: "1rem", fontWeight: 500 }}>
                      Replace the file in the destination
                    </Typography>
                    {currentUploadConflict?.replace_supported === false ? (
                      <Typography sx={{ color: "rgba(226,232,240,0.6)", fontSize: "0.76rem", mt: 0.25 }}>
                        The existing destination item is a folder, so it cannot be replaced by this upload.
                      </Typography>
                    ) : null}
                  </Box>
                </Box>

                <Box
                  component="button"
                  type="button"
                  onClick={() => {
                    void handleUploadConflictChoice("skip");
                  }}
                  disabled={Boolean(actionBusy)}
                  sx={UPLOAD_CONFLICT_OPTION_SX}
                >
                  <SkipNextRoundedIcon sx={{ color: "#7dd3fc", fontSize: 21, flexShrink: 0 }} />
                  <Typography sx={{ color: "inherit", fontSize: "1rem", fontWeight: 500 }}>Skip this file</Typography>
                </Box>

                <Box
                  component="button"
                  type="button"
                  onClick={() => setUploadConflictCompareOpen((previous) => !previous)}
                  disabled={Boolean(actionBusy)}
                  sx={UPLOAD_CONFLICT_OPTION_SX}
                >
                  <CompareArrowsRoundedIcon sx={{ color: "#7dd3fc", fontSize: 21, flexShrink: 0 }} />
                  <Typography sx={{ color: "inherit", fontSize: "1rem", fontWeight: 500 }}>
                    Compare info for both files
                  </Typography>
                </Box>
              </Stack>

              {uploadConflictCompareOpen ? (
                <Stack
                  direction={{ xs: "column", sm: "row" }}
                  spacing={1.2}
                  sx={{
                    pt: 0.35,
                    borderTop: "1px solid rgba(148,163,184,0.18)",
                  }}
                >
                  <Box
                    sx={{
                      flex: 1,
                      borderRadius: 2,
                      border: "1px solid rgba(148,163,184,0.16)",
                      background: "rgba(255,255,255,0.025)",
                      px: 1.35,
                      py: 1.1,
                    }}
                  >
                    <Typography sx={{ color: "#7dd3fc", fontSize: "0.8rem", fontWeight: 600, mb: 0.9 }}>Incoming file</Typography>
                    <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.82rem" }}>{currentUploadConflictFile?.name || currentUploadConflict.name}</Typography>
                    <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem", mt: 0.55 }}>
                      Size: {formatBytes(currentUploadConflictFile?.size || currentUploadConflict?.upload_size_bytes)}
                    </Typography>
                    <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem", mt: 0.3 }}>
                      Last Modified:{" "}
                      {currentUploadConflictFile
                        ? formatBrowserModifiedAt(currentUploadConflictFile?.lastModified)
                        : formatModifiedAt(currentUploadConflict?.upload_modified_at)}
                    </Typography>
                  </Box>
                  <Box
                    sx={{
                      flex: 1,
                      borderRadius: 2,
                      border: "1px solid rgba(148,163,184,0.16)",
                      background: "rgba(255,255,255,0.025)",
                      px: 1.35,
                      py: 1.1,
                    }}
                  >
                    <Typography sx={{ color: "#c084fc", fontSize: "0.8rem", fontWeight: 600, mb: 0.9 }}>Existing file</Typography>
                    <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.82rem" }}>
                      {normalizeText(currentUploadConflict?.destination?.name) || currentUploadConflict.name}
                    </Typography>
                    <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem", mt: 0.55 }}>
                      Size: {formatBytes(currentUploadConflict?.destination?.size_bytes)}
                    </Typography>
                    <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem", mt: 0.3 }}>
                      Last Modified: {formatModifiedAt(currentUploadConflict?.destination?.modified_at)}
                    </Typography>
                  </Box>
                </Stack>
              ) : null}

              {uploadConflicts.length > 1 ? (
                <FormControlLabel
                  control={
                    <Checkbox
                      checked={uploadConflictApplyToAll}
                      onChange={(event) => setUploadConflictApplyToAll(event.target.checked)}
                      disabled={Boolean(actionBusy)}
                      size="small"
                      sx={{
                        color: "rgba(148,163,184,0.86)",
                        "&.Mui-checked": {
                          color: "#7dd3fc",
                        },
                      }}
                    />
                  }
                  label="Do this for all items"
                  sx={{
                    mt: 0.35,
                    ml: 0.1,
                    "& .MuiFormControlLabel-label": {
                      color: MAGIC_UI.textMuted,
                      fontSize: "0.84rem",
                    },
                  }}
                />
              ) : null}

              {uploadConflicts.length > 1 ? (
                <Typography sx={{ color: "rgba(226,232,240,0.52)", fontSize: "0.76rem" }}>
                  Duplicate {Math.min(uploadConflictIndex + 1, uploadConflicts.length)} of {uploadConflicts.length}
                </Typography>
              ) : null}
            </Stack>
          ) : null}
        </DialogContent>
      </Dialog>

      <Dialog
        open={editorOpen}
        onClose={handleCloseEditor}
        fullWidth
        maxWidth={false}
        PaperProps={{
          sx: {
            ...DIALOG_PAPER_SX,
            display: "flex",
            flexDirection: "column",
            width: "92vw",
            maxWidth: "92vw",
            height: "88vh",
            maxHeight: "88vh",
          },
        }}
      >
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <Box
            sx={{
              display: "flex",
              alignItems: "flex-start",
              justifyContent: "space-between",
              gap: 2,
              flexWrap: "wrap",
            }}
          >
            <DialogHeaderBlock
              title={editorPath ? `Edit ${editorPath.split(/[/\\]/).filter(Boolean).pop() || editorPath}` : "Edit File"}
              subtitle={editorPath || "Open a remote text file for quick inline editing."}
            />
            <Stack direction="row" spacing={1} alignItems="center">
              <Button
                onClick={() => {
                  void handleSaveEditor();
                }}
                disabled={!editorHasChanges || editorLoading || editorSaving || !normalizeText(editorPath)}
                sx={DIALOG_PRIMARY_BUTTON_SX}
              >
                {editorSaving ? "Saving..." : "Save"}
              </Button>
              <Button onClick={handleCloseEditor} sx={DIALOG_BUTTON_SX}>
                Close
              </Button>
            </Stack>
          </Box>
        </DialogTitle>
        <DialogContent
          sx={{
            ...DIALOG_CONTENT_SX,
            display: "flex",
            flexDirection: "column",
            gap: 1.5,
            flex: 1,
            minHeight: 0,
            overflow: "hidden",
          }}
        >
          <Stack direction="row" spacing={1.5} flexWrap="wrap" sx={{ color: MAGIC_UI.textMuted, fontSize: "0.8rem" }}>
            <Typography variant="caption" sx={{ color: "inherit" }}>
              Syntax: {formatEditorLanguage(editorLanguage)}
            </Typography>
            <Typography variant="caption" sx={{ color: "inherit" }}>
              Encoding: {normalizeText(editorEncoding) || "utf-8"}
            </Typography>
            <Typography variant="caption" sx={{ color: "inherit" }}>
              Line Endings: {formatLineEndingLabel(editorLineEnding)}
            </Typography>
          </Stack>
          {editorError ? (
            <Alert severity="error" sx={{ borderRadius: 2 }}>
              {editorError}
            </Alert>
          ) : null}
          {editorLoading ? (
            <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted }}>
              Loading file...
            </Typography>
          ) : null}
          <Box
            sx={{
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              borderRadius: 2,
              bgcolor: "rgba(4,7,17,0.72)",
              position: "relative",
              display: "flex",
              flexDirection: "column",
              flex: 1,
              minHeight: 0,
              overflow: "auto",
              overscrollBehavior: "contain",
              scrollbarGutter: "stable both-edges",
              "&::-webkit-scrollbar": {
                width: 10,
                height: 10,
              },
              "&::-webkit-scrollbar-track": {
                background: "rgba(15,23,42,0.45)",
                borderRadius: 999,
              },
              "&::-webkit-scrollbar-thumb": {
                background: "rgba(125,183,255,0.35)",
                borderRadius: 999,
                border: "2px solid rgba(15,23,42,0.45)",
              },
              "& textarea, & pre": {
                minHeight: "100% !important",
                whiteSpace: "pre !important",
                overflowWrap: "normal !important",
                wordBreak: "normal !important",
              },
            }}
          >
            {!editorLoading ? (
              <Editor
                value={editorContent}
                onValueChange={setEditorContent}
                highlight={(code) => highlightCode(code, editorLanguage)}
                padding={12}
                spellCheck={false}
                wrap="off"
                autoCapitalize="off"
                autoCorrect="off"
                autoComplete="off"
                style={{
                  fontFamily:
                    'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
                  fontSize: 12,
                  color: "#e6edf3",
                  minHeight: "100%",
                  whiteSpace: "pre",
                  overflowWrap: "normal",
                  wordBreak: "normal",
                  backgroundColor: "transparent",
                }}
              />
            ) : null}
          </Box>
        </DialogContent>
      </Dialog>

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
