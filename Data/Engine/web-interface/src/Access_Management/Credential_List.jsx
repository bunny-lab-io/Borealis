import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  IconButton,
  Menu,
  MenuItem,
  Paper,
  Typography,
  CircularProgress,
} from "@mui/material";
import MoreVertIcon from "@mui/icons-material/MoreVert";
import AddIcon from "@mui/icons-material/Add";
import RefreshIcon from "@mui/icons-material/Refresh";
import LockIcon from "@mui/icons-material/Lock";
import WifiIcon from "@mui/icons-material/Wifi";
import ComputerIcon from "@mui/icons-material/Computer";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import CredentialEditor from "./Credential_Editor.jsx";
import { ConfirmDeleteDialog } from "../Dialogs.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const myTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#1f2836",
  browserColorScheme: "dark",
  chromeBackgroundColor: {
    ref: "foregroundColor",
    mix: 0.07,
    onto: "backgroundColor"
  },
  fontFamily: {
    googleFont: "IBM Plex Sans"
  },
  foregroundColor: "#FFF",
  headerFontSize: 14
});

const themeClassName = myTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';
function formatTs(ts) {
  if (!ts) return "-";
  const date = new Date(Number(ts) * 1000);
  if (Number.isNaN(date?.getTime())) return "-";
  return `${date.toLocaleDateString()} ${date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
}

function titleCase(value) {
  if (!value) return "-";
  const lower = String(value).toLowerCase();
  return lower.replace(/(^|\s)\w/g, (c) => c.toUpperCase());
}

function connectionIcon(connection) {
  const val = (connection || "").toLowerCase();
  if (val === "ssh") return <LockIcon fontSize="small" sx={{ mr: 0.6, color: "#58a6ff" }} />;
  if (val === "winrm") return <WifiIcon fontSize="small" sx={{ mr: 0.6, color: "#58a6ff" }} />;
  return <ComputerIcon fontSize="small" sx={{ mr: 0.6, color: "#58a6ff" }} />;
}

export default function CredentialList({ isAdmin = false, onPageMetaChange }) {
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [menuRow, setMenuRow] = useState(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [editorMode, setEditorMode] = useState("create");
  const [editingCredential, setEditingCredential] = useState(null);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const gridApiRef = useRef(null);

  const openMenu = useCallback((event, row) => {
    setMenuAnchor(event.currentTarget);
    setMenuRow(row);
  }, []);

  const closeMenu = useCallback(() => {
    setMenuAnchor(null);
    setMenuRow(null);
  }, []);

  const connectionCellRenderer = useCallback((params) => {
    const row = params.data || {};
    const label = titleCase(row.connection_type);
    return (
      <Box sx={{ display: "flex", alignItems: "center", fontFamily: gridFontFamily }}>
        {connectionIcon(row.connection_type)}
        <Box component="span" sx={{ color: "#f5f7fa" }}>
          {label}
        </Box>
      </Box>
    );
  }, []);

  const actionCellRenderer = useCallback(
    (params) => {
      const row = params.data;
      if (!row) return null;
      const handleClick = (event) => {
        event.preventDefault();
        event.stopPropagation();
        openMenu(event, row);
      };
      return (
        <IconButton size="small" onClick={handleClick} sx={{ color: "#7db7ff" }}>
          <MoreVertIcon fontSize="small" />
        </IconButton>
      );
    },
    [openMenu]
  );

  const columnDefs = useMemo(
    () => [
      {
        headerName: "Name",
        field: "name",
        sort: "asc",
        cellRenderer: (params) => params.value || "-"
      },
      {
        headerName: "Credential Type",
        field: "credential_type",
        valueGetter: (params) => titleCase(params.data?.credential_type)
      },
      {
        headerName: "Connection",
        field: "connection_type",
        cellRenderer: connectionCellRenderer
      },
      {
        headerName: "Site",
        field: "site_name",
        cellRenderer: (params) => params.value || "-"
      },
      {
        headerName: "Username",
        field: "username",
        cellRenderer: (params) => params.value || "-"
      },
      {
        headerName: "Updated",
        field: "updated_at",
        valueGetter: (params) =>
          formatTs(params.data?.updated_at || params.data?.created_at)
      },
      {
        headerName: "",
        field: "__actions__",
        minWidth: 70,
        maxWidth: 80,
        sortable: false,
        filter: false,
        resizable: false,
        suppressMenu: true,
        cellRenderer: actionCellRenderer,
        pinned: "right"
      }
    ],
    [actionCellRenderer, connectionCellRenderer]
  );

  const defaultColDef = useMemo(
    () => ({
      sortable: true,
      filter: "agTextColumnFilter",
      resizable: true,
      flex: 1,
      minWidth: 140,
      cellStyle: {
        display: "flex",
        alignItems: "center",
        color: "#f5f7fa",
        fontFamily: gridFontFamily,
        fontSize: "13px"
      },
      headerClass: "credential-grid-header"
    }),
    []
  );

  const getRowId = useCallback(
    (params) =>
      params.data?.id ||
      params.data?.name ||
      params.data?.username ||
      String(params.rowIndex ?? ""),
    []
  );

  const fetchCredentials = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const resp = await fetch("/api/credentials");
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const list = Array.isArray(data?.credentials) ? data.credentials : [];
      list.sort((a, b) => String(a?.name || "").localeCompare(String(b?.name || "")));
      setRows(list);
    } catch (err) {
      setRows([]);
      setError(String(err.message || err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchCredentials();
  }, [fetchCredentials]);

  const handleCreate = useCallback(() => {
    setEditorMode("create");
    setEditingCredential(null);
    setEditorOpen(true);
  }, []);

  const handleEdit = (row) => {
    closeMenu();
    setEditorMode("edit");
    setEditingCredential(row);
    setEditorOpen(true);
  };

  const handleDelete = (row) => {
    closeMenu();
    setDeleteTarget(row);
  };

  const doDelete = async () => {
    if (!deleteTarget?.id) return;
    setDeleteBusy(true);
    try {
      const resp = await fetch(`/api/credentials/${deleteTarget.id}`, { method: "DELETE" });
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}));
        throw new Error(data?.error || `HTTP ${resp.status}`);
      }
      setDeleteTarget(null);
      await fetchCredentials();
    } catch (err) {
      setError(String(err.message || err));
    } finally {
      setDeleteBusy(false);
    }
  };

  const handleEditorSaved = async () => {
    setEditorOpen(false);
    setEditingCredential(null);
    await fetchCredentials();
  };

  const handleGridReady = useCallback((params) => {
    gridApiRef.current = params.api;
  }, []);

  useEffect(() => {
    const api = gridApiRef.current;
    if (!api) return;
    if (loading) {
      api.showLoadingOverlay();
    } else if (!rows.length) {
      api.showNoRowsOverlay();
    } else {
      api.hideOverlay();
    }
  }, [loading, rows]);

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "credentials-refresh",
        label: "Refresh",
        icon: <RefreshIcon />,
        tone: "secondary",
        loading,
        onClick: fetchCredentials,
      },
      {
        id: "credentials-create",
        label: "New Credential",
        icon: <AddIcon />,
        tone: "primary",
        onClick: handleCreate,
      },
    ],
    [fetchCredentials, handleCreate, loading]
  );

  useEffect(() => {
    onPageMetaChange?.({
      page_title: "Credentials",
      page_subtitle: "Stored credentials for remote automation tasks and Ansible playbook runs.",
      page_icon: LockIcon,
      page_header_actions: pageHeaderActions,
    });
    return () => onPageMetaChange?.(null);
  }, [onPageMetaChange, pageHeaderActions]);

  if (!isAdmin) {
    return (
      <Paper sx={{ m: 2, p: 3, bgcolor: "transparent" }}>
        <Typography variant="h6" sx={{ color: "#ff8080" }}>
          Access denied
        </Typography>
        <Typography variant="body2" sx={{ color: "#bbb" }}>
          You do not have permission to manage credentials.
        </Typography>
      </Paper>
    );
  }

  return (
    <>
      <Paper
        sx={{
          m: 0,
          p: 0,
          bgcolor: "transparent",
          border: "none",
          boxShadow: "none",
          fontFamily: gridFontFamily,
          color: "#f5f7fa",
          display: "flex",
          flexDirection: "column",
          flexGrow: 1,
          minWidth: 0,
          minHeight: 420
        }}
        elevation={0}
      >
        {loading && (
          <Box
            sx={{
              display: "flex",
              alignItems: "center",
              gap: 1,
              color: "#7db7ff",
              px: 2,
              py: 1.5,
            }}
          >
            <CircularProgress size={18} sx={{ color: "#58a6ff" }} />
            <Typography variant="body2">Loading credentials…</Typography>
          </Box>
        )}
        {error && (
          <Box sx={{ px: 2, py: 1.5, color: "#ff8080" }}>
            <Typography variant="body2">{error}</Typography>
          </Box>
        )}

        <Box
          sx={{
            flexGrow: 1,
            minHeight: 0,
            display: "flex",
            flexDirection: "column",
            mt: "10px",
            px: 2,
            pb: 2
          }}
        >
          <Box
            className={themeClassName}
            sx={{
              width: "100%",
              height: "100%",
              flexGrow: 1,
              fontFamily: gridFontFamily,
              "--ag-font-family": gridFontFamily,
              "--ag-icon-font-family": iconFontFamily,
              "--ag-row-border-style": "solid",
              "--ag-row-border-color": "#2a2a2a",
              "--ag-row-border-width": "1px",
              "& .ag-root-wrapper": {
                borderRadius: 1,
                minHeight: 320
              },
              "& .ag-root, & .ag-header, & .ag-center-cols-container, & .ag-paging-panel": {
                fontFamily: gridFontFamily
              },
              "& .ag-icon": {
                fontFamily: iconFontFamily
              },
              "& .ag-row-selected": {
                backgroundColor: "rgba(125,211,252,0.2) !important",
                boxShadow: "inset 0 0 0 1px rgba(125,211,252,0.45)",
              }
            }}
            style={{
              color: "#f5f7fa",
              "--ag-row-hover-color": "rgba(73,156,196,0.2)",
              "--ag-selected-row-background-color": "rgba(125,211,252,0.2)",
              "--ag-checkbox-checked-color": "#7dd3fc",
            }}
          >
            <AgGridReact
              rowData={rows}
              columnDefs={columnDefs}
              defaultColDef={defaultColDef}
              animateRows
              rowHeight={46}
              headerHeight={44}
              getRowId={getRowId}
              overlayNoRowsTemplate="<span class='ag-overlay-no-rows-center'>No credentials have been created yet.</span>"
              onGridReady={handleGridReady}
              suppressCellFocus
              theme={myTheme}
              style={{
                width: "100%",
                height: "100%",
                fontFamily: gridFontFamily,
                "--ag-icon-font-family": iconFontFamily
              }}
            />
          </Box>
        </Box>
      </Paper>

      <Menu
        anchorEl={menuAnchor}
        open={Boolean(menuAnchor)}
        onClose={closeMenu}
        elevation={2}
        PaperProps={{ sx: { bgcolor: "#1f1f1f", color: "#f5f5f5" } }}
      >
        <MenuItem onClick={() => handleEdit(menuRow)}>Edit</MenuItem>
        <MenuItem onClick={() => handleDelete(menuRow)} sx={{ color: "#ff8080" }}>
          Delete
        </MenuItem>
      </Menu>

      <CredentialEditor
        open={editorOpen}
        mode={editorMode}
        credential={editingCredential}
        onClose={() => {
          setEditorOpen(false);
          setEditingCredential(null);
        }}
        onSaved={handleEditorSaved}
      />

      <ConfirmDeleteDialog
        open={Boolean(deleteTarget)}
        onCancel={() => setDeleteTarget(null)}
        onConfirm={doDelete}
        confirmDisabled={deleteBusy}
        message={
          deleteTarget
            ? `Delete credential '${deleteTarget.name || ""}'? Any jobs referencing it will require an update.`
            : ""
        }
      />
    </>
  );
}
