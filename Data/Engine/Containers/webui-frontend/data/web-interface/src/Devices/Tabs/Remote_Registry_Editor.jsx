import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import {
  Alert,
  Box,
  Button,
  Checkbox,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControlLabel,
  IconButton,
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
import ChevronRightRoundedIcon from "@mui/icons-material/ChevronRightRounded";
import ContentCopyRoundedIcon from "@mui/icons-material/ContentCopyRounded";
import DeleteOutlineRoundedIcon from "@mui/icons-material/DeleteOutlineRounded";
import DriveFileRenameOutlineRoundedIcon from "@mui/icons-material/DriveFileRenameOutlineRounded";
import EditRoundedIcon from "@mui/icons-material/EditRounded";
import RefreshRoundedIcon from "@mui/icons-material/RefreshRounded";
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

const TOOLBAR_BUTTON_SX = {
  minHeight: 38,
  borderRadius: 2,
  textTransform: "none",
  fontWeight: 700,
  borderColor: "rgba(125,211,252,0.28)",
  color: MAGIC_UI.textBright,
  background: "rgba(5,10,24,0.72)",
  "&:hover": {
    borderColor: "rgba(125,211,252,0.52)",
    background: "rgba(8,17,35,0.9)",
  },
  "&.Mui-disabled": {
    color: "rgba(148,163,184,0.58)",
    borderColor: "rgba(148,163,184,0.18)",
  },
};

const ICON_BUTTON_SX = {
  width: 38,
  height: 38,
  borderRadius: "50%",
  border: "1px solid rgba(148,163,184,0.28)",
  background: "rgba(5,10,24,0.72)",
  color: MAGIC_UI.textBright,
  "&:hover": {
    borderColor: "rgba(125,211,252,0.52)",
    background: "rgba(8,17,35,0.9)",
  },
  "&.Mui-disabled": {
    color: "rgba(148,163,184,0.58)",
    borderColor: "rgba(148,163,184,0.18)",
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
  const hostname = useMemo(() => getHostname(device), [device]);
  const requestedPath = useMemo(() => normalizeRegistryPath(searchParams.get("registry_path")), [searchParams]);

  const [currentPath, setCurrentPath] = useState("");
  const [addressInput, setAddressInput] = useState("");
  const [rows, setRows] = useState([]);
  const [selectedRows, setSelectedRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [keyDialog, setKeyDialog] = useState({ open: false, mode: "create", row: null, name: "" });
  const [valueDialog, setValueDialog] = useState({
    open: false,
    mode: "create",
    row: null,
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
  const canMutateCurrentKey = Boolean(currentPath);
  const canRenameKey = Boolean(selectedKey?.editable);
  const canDeleteKey = Boolean(selectedKey?.editable);
  const canEditValue = Boolean(selectedValue?.editable);

  const setUrlPath = useCallback(
    (pathValue) => {
      const normalizedPath = normalizeRegistryPath(pathValue);
      const nextParams = new URLSearchParams(searchParams);
      if (normalizedPath) {
        nextParams.set("registry_path", normalizedPath);
      } else {
        nextParams.delete("registry_path");
      }
      setSearchParams(nextParams, { replace: true });
    },
    [searchParams, setSearchParams]
  );

  const buildRows = useCallback((data, pathValue) => {
    const keyRows = Array.isArray(data?.entries) ? data.entries.map((row) => normalizeKeyEntry(row)) : [];
    const valueRows = Array.isArray(data?.values) ? data.values.map((row) => normalizeValueEntry(row, pathValue)) : [];
    return [...keyRows, ...valueRows];
  }, []);

  const loadRegistryPath = useCallback(
    async (pathValue = "") => {
      if (!hostname) return;
      const normalizedPath = normalizeRegistryPath(pathValue);
      setLoading(true);
      setError("");
      try {
        const url = normalizedPath
          ? `/api/device/registry/${encodeURIComponent(hostname)}/children?path=${encodeURIComponent(normalizedPath)}`
          : `/api/device/registry/${encodeURIComponent(hostname)}/roots`;
        const response = await fetch(url, { credentials: "include" });
        const data = await responseJson(response);
        if (!response.ok) {
          throw new Error(normalizeText(data?.message) || normalizeText(data?.error) || `HTTP ${response.status}`);
        }
        const nextPath = normalizedPath ? normalizeRegistryPath(data?.current_path || normalizedPath) : "";
        setCurrentPath(nextPath);
        setAddressInput(nextPath);
        setRows(buildRows(data, nextPath));
        setSelectedRows([]);
        gridRef.current?.api?.deselectAll?.();
      } catch (loadError) {
        setError(loadError?.message || "Registry request failed.");
        setRows([]);
      } finally {
        setLoading(false);
      }
    },
    [buildRows, hostname]
  );

  useEffect(() => {
    void loadRegistryPath(requestedPath);
  }, [loadRegistryPath, requestedPath]);

  const navigateToPath = useCallback(
    (pathValue) => {
      const normalizedPath = normalizeRegistryPath(pathValue);
      setUrlPath(normalizedPath);
      if (normalizedPath === requestedPath) {
        void loadRegistryPath(normalizedPath);
      }
    },
    [loadRegistryPath, requestedPath, setUrlPath]
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
    void loadRegistryPath(currentPath);
  }, [currentPath, loadRegistryPath]);

  const handleOpenSelected = useCallback(() => {
    if (selectedKey?.path) {
      navigateToPath(selectedKey.path);
    }
  }, [navigateToPath, selectedKey]);

  const handleCopyPath = useCallback(async () => {
    const pathValue = normalizeRegistryPath(selectedRow?.path || currentPath);
    if (!pathValue) return;
    try {
      await navigator.clipboard?.writeText(pathValue);
      await notifyOperator({ title: "Registry", message: "Registry path copied.", variant: "success", icon: "content_copy" });
    } catch {
      await notifyOperator({ title: "Registry", message: "Clipboard copy failed.", variant: "warning", icon: "content_copy" });
    }
  }, [currentPath, notifyOperator, selectedRow]);

  const openCreateKeyDialog = useCallback(() => {
    setKeyDialog({ open: true, mode: "create", row: null, name: "" });
  }, []);

  const openRenameKeyDialog = useCallback(() => {
    if (!selectedKey) return;
    setKeyDialog({ open: true, mode: "rename", row: selectedKey, name: normalizeText(selectedKey.name) });
  }, [selectedKey]);

  const openCreateValueDialog = useCallback(() => {
    setValueDialog({ open: true, mode: "create", row: null, name: "", type: "REG_SZ", data: "" });
  }, []);

  const openEditValueDialog = useCallback(() => {
    if (!selectedValue) return;
    setValueDialog({
      open: true,
      mode: "update",
      row: selectedValue,
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
        parent_path: normalizeRegistryPath(currentPath),
        new_name: name,
        name,
      });
      setKeyDialog({ open: false, mode: "create", row: null, name: "" });
      await notifyOperator({
        title: "Registry",
        message: isRename ? "Registry key renamed." : "Registry key created.",
        variant: "success",
        icon: "account_tree",
      });
      refreshCurrent();
    } catch (saveError) {
      setError(saveError?.message || "Registry key action failed.");
    } finally {
      setActionBusy("");
    }
  }, [currentPath, keyDialog, notifyOperator, refreshCurrent, requestRegistryMutation]);

  const handleSaveValue = useCallback(async () => {
    const isUpdate = valueDialog.mode === "update";
    setActionBusy(isUpdate ? "update-value" : "create-value");
    try {
      await requestRegistryMutation(isUpdate ? "value/update" : "value/create", {
        path: normalizeRegistryPath(currentPath),
        name: valueDialog.name,
        type: valueDialog.type,
        data: valueDialog.data,
      });
      setValueDialog({ open: false, mode: "create", row: null, name: "", type: "REG_SZ", data: "" });
      await notifyOperator({
        title: "Registry",
        message: isUpdate ? "Registry value updated." : "Registry value created.",
        variant: "success",
        icon: "article",
      });
      refreshCurrent();
    } catch (saveError) {
      setError(saveError?.message || "Registry value action failed.");
    } finally {
      setActionBusy("");
    }
  }, [currentPath, notifyOperator, refreshCurrent, requestRegistryMutation, valueDialog]);

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
      refreshCurrent();
    } catch (deleteError) {
      setError(deleteError?.message || "Registry delete failed.");
    } finally {
      setActionBusy("");
    }
  }, [deleteDialog, notifyOperator, refreshCurrent, requestRegistryMutation]);

  const pathSegments = useMemo(() => {
    if (!currentPath) return [];
    const parts = currentPath.split("\\").filter(Boolean);
    return parts.map((_part, index) => parts.slice(0, index + 1).join("\\"));
  }, [currentPath]);

  const columnDefs = useMemo(
    () => [
      {
        field: "display_name",
        headerName: "Name",
        minWidth: 260,
        flex: 1.4,
        cellRenderer: (params) => {
          const row = params?.data || {};
          const isKey = row.row_type === "key";
          return (
            <Box sx={{ display: "flex", alignItems: "center", gap: 0.9, minWidth: 0 }}>
              {isKey ? (
                <AccountTreeRoundedIcon sx={{ fontSize: 19, color: row.kind === "hive" ? "#7dd3fc" : "#34d399" }} />
              ) : (
                <ArticleRoundedIcon sx={{ fontSize: 19, color: row.editable ? "#c084fc" : "rgba(148,163,184,0.82)" }} />
              )}
              <Typography
                component="span"
                sx={{
                  minWidth: 0,
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                  color: MAGIC_UI.textBright,
                  fontSize: "0.9rem",
                  fontWeight: isKey ? 700 : 600,
                }}
              >
                {row.display_name}
              </Typography>
            </Box>
          );
        },
      },
      { field: "type_label", headerName: "Type", minWidth: 130, flex: 0.55 },
      { field: "data_label", headerName: "Data", minWidth: 240, flex: 1.2 },
      { field: "subkey_count", headerName: "Subkeys", width: 110, flex: 0 },
      { field: "value_count", headerName: "Values", width: 100, flex: 0 },
      { field: "modified_label", headerName: "Modified", minWidth: 190, flex: 0.75 },
    ],
    []
  );

  const rowSelection = useMemo(() => ({ mode: "singleRow", enableClickSelection: true }), []);

  return (
    <Box sx={{ display: "flex", flexDirection: "column", gap: 1.35, flexGrow: 1, minHeight: 0 }}>
      <Stack direction={{ xs: "column", lg: "row" }} spacing={1} alignItems={{ xs: "stretch", lg: "center" }}>
        <Stack direction="row" spacing={0.75} alignItems="center" sx={{ flex: 1, minWidth: 0 }}>
          <Tooltip title="Registry roots" arrow>
            <IconButton onClick={() => navigateToPath("")} sx={ICON_BUTTON_SX}>
              <AccountTreeRoundedIcon sx={{ fontSize: 19 }} />
            </IconButton>
          </Tooltip>
          <TextField
            value={addressInput}
            onChange={(event) => setAddressInput(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                navigateToPath(addressInput);
              }
            }}
            placeholder="HKLM\\SOFTWARE"
            size="small"
            fullWidth
            sx={DIALOG_INPUT_SX}
          />
          <Tooltip title="Open path" arrow>
            <span>
              <IconButton onClick={() => navigateToPath(addressInput)} disabled={!normalizeRegistryPath(addressInput)} sx={ICON_BUTTON_SX}>
                <ChevronRightRoundedIcon sx={{ fontSize: 21 }} />
              </IconButton>
            </span>
          </Tooltip>
          <Tooltip title="Copy path" arrow>
            <span>
              <IconButton onClick={() => void handleCopyPath()} disabled={!normalizeRegistryPath(selectedRow?.path || currentPath)} sx={ICON_BUTTON_SX}>
                <ContentCopyRoundedIcon sx={{ fontSize: 18 }} />
              </IconButton>
            </span>
          </Tooltip>
          <Tooltip title="Refresh" arrow>
            <IconButton onClick={refreshCurrent} disabled={loading} sx={ICON_BUTTON_SX}>
              <RefreshRoundedIcon sx={{ fontSize: 18 }} />
            </IconButton>
          </Tooltip>
        </Stack>
        <Stack direction="row" spacing={0.75} flexWrap="wrap" useFlexGap>
          <Button variant="outlined" startIcon={<AddRoundedIcon />} onClick={openCreateKeyDialog} disabled={!canMutateCurrentKey || loading} sx={TOOLBAR_BUTTON_SX}>
            Key
          </Button>
          <Button variant="outlined" startIcon={<AddRoundedIcon />} onClick={openCreateValueDialog} disabled={!canMutateCurrentKey || loading} sx={TOOLBAR_BUTTON_SX}>
            Value
          </Button>
          <Button variant="outlined" startIcon={<EditRoundedIcon />} onClick={openEditValueDialog} disabled={!canEditValue || loading} sx={TOOLBAR_BUTTON_SX}>
            Edit
          </Button>
          <Button variant="outlined" startIcon={<DriveFileRenameOutlineRoundedIcon />} onClick={openRenameKeyDialog} disabled={!canRenameKey || loading} sx={TOOLBAR_BUTTON_SX}>
            Rename
          </Button>
          <Button variant="outlined" startIcon={<DeleteOutlineRoundedIcon />} onClick={openDeleteDialog} disabled={!(canDeleteKey || selectedValue) || loading} sx={TOOLBAR_BUTTON_SX}>
            Delete
          </Button>
        </Stack>
      </Stack>

      {currentPath ? (
        <Stack direction="row" spacing={0.4} alignItems="center" sx={{ minHeight: 24, overflow: "hidden" }}>
          {pathSegments.map((segment, index) => (
            <React.Fragment key={segment}>
              <Button
                variant="text"
                onClick={() => navigateToPath(segment)}
                sx={{
                  minWidth: 0,
                  px: 0.7,
                  py: 0.2,
                  color: "#9bd7ff",
                  textTransform: "none",
                  fontWeight: 700,
                  fontSize: "0.82rem",
                }}
              >
                {segment.split("\\").pop()}
              </Button>
              {index < pathSegments.length - 1 ? <ChevronRightRoundedIcon sx={{ fontSize: 16, color: MAGIC_UI.textMuted }} /> : null}
            </React.Fragment>
          ))}
        </Stack>
      ) : null}

      {error ? (
        <Alert severity="warning" sx={{ background: "rgba(120,53,15,0.32)", color: "#fed7aa", border: "1px solid rgba(251,146,60,0.28)" }}>
          {error}
        </Alert>
      ) : null}

      <GridShell
        sx={{
          flexGrow: 1,
          minHeight: 460,
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
          rowData={rows}
          columnDefs={columnDefs}
          defaultColDef={DEFAULT_GRID_COL_DEF}
          rowSelection={rowSelection}
          animateRows
          suppressCellFocus
          loading={loading}
          theme={DEVICE_DETAILS_GRID_THEME}
          getRowId={(params) => params?.data?.id || ""}
          onSelectionChanged={(event) => setSelectedRows(event.api.getSelectedRows())}
          onRowDoubleClicked={(event) => {
            if (event?.data?.row_type === "key") {
              navigateToPath(event.data.path);
            } else if (event?.data?.row_type === "value" && event.data.editable) {
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

      <Dialog open={keyDialog.open} onClose={() => setKeyDialog({ open: false, mode: "create", row: null, name: "" })} PaperProps={{ sx: DIALOG_PAPER_SX }} fullWidth maxWidth="sm">
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title={keyDialog.mode === "rename" ? "Rename Key" : "Create Key"}
            subtitle={keyDialog.mode === "rename" ? normalizeRegistryPath(keyDialog.row?.path) : currentPath}
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
          <Button sx={DIALOG_BUTTON_SX} onClick={() => setKeyDialog({ open: false, mode: "create", row: null, name: "" })}>
            Cancel
          </Button>
          <Button sx={DIALOG_PRIMARY_BUTTON_SX} disabled={Boolean(actionBusy)} onClick={() => void handleSaveKey()}>
            Save
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog open={valueDialog.open} onClose={() => setValueDialog({ open: false, mode: "create", row: null, name: "", type: "REG_SZ", data: "" })} PaperProps={{ sx: DIALOG_PAPER_SX }} fullWidth maxWidth="md">
        <DialogTitle sx={DIALOG_TITLE_SX}>
          <DialogHeaderBlock
            title={valueDialog.mode === "update" ? "Edit Value" : "Create Value"}
            subtitle={currentPath}
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
          <Button sx={DIALOG_BUTTON_SX} onClick={() => setValueDialog({ open: false, mode: "create", row: null, name: "", type: "REG_SZ", data: "" })}>
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
