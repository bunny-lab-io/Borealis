import React, { useEffect, useMemo, useState, useCallback, useRef } from "react";
import {
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Paper,
  Typography,
  IconButton,
  Tooltip,
} from "@mui/material";
import AddIcon from "@mui/icons-material/Add";
import LocationCityIcon from "@mui/icons-material/LocationCity";

import DeleteIcon from "@mui/icons-material/DeleteOutline";
import EditIcon from "@mui/icons-material/Edit";
import ContentCopyIcon from "@mui/icons-material/ContentCopy";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { CreateSiteDialog, RenameSiteDialog } from "../Dialogs.jsx";
import PageBodyFrame from "../PageBodyFrame.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const myTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 14,
});

const themeClassName = myTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';

const AUTO_SIZE_COLUMNS = ["device_count", "enrollment_code"];

const MAGIC_UI = {
  shellBg:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), #040711",
  panelBg:
    "linear-gradient(135deg, rgba(10, 16, 31, 0.98) 0%, rgba(6, 10, 24, 0.94) 60%, rgba(15, 6, 26, 0.96) 100%)",
  panelBorder: "rgba(148, 163, 184, 0.35)",
  textBright: "#e2e8f0",
  textMuted: "#94a3b8",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
  success: "#34d399",
};

const SITE_DIALOG_PAPER_SX = {
  borderRadius: 3,
  background:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), rgba(8,12,24,0.96)",
  backdropFilter: "blur(18px)",
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  boxShadow: "0 24px 60px rgba(2,8,23,0.72)",
  color: MAGIC_UI.textBright,
  overflow: "hidden",
};

const SITE_DIALOG_TITLE_SX = {
  px: 3,
  pt: 3,
  pb: 0.75,
  background: "transparent",
};

const SITE_DIALOG_CONTENT_SX = {
  px: 3,
  pt: 1,
  pb: 2.5,
  background: "transparent",
};

const SITE_DIALOG_ACTIONS_SX = {
  px: 3,
  pt: 0.5,
  pb: 2.5,
  gap: 1,
  background: "transparent",
};

const SITE_DIALOG_BUTTON_SX = {
  borderRadius: 999,
  px: 2,
  minHeight: 38,
  textTransform: "none",
  fontWeight: 600,
  fontSize: "0.92rem",
  color: MAGIC_UI.textBright,
  border: `1px solid ${MAGIC_UI.panelBorder}`,
  background: "rgba(5,10,24,0.84)",
  transition: "background 160ms ease, border-color 160ms ease, color 160ms ease, transform 120ms ease",
  "&:hover": {
    background: "rgba(8,14,30,0.92)",
    borderColor: "rgba(125,211,252,0.46)",
  },
  "&:active": {
    transform: "translateY(0.5px)",
  },
};

const SITE_DIALOG_DANGER_BUTTON_SX = {
  ...SITE_DIALOG_BUTTON_SX,
  color: "#ff9aa5",
  borderColor: "rgba(244,63,94,0.38)",
  background: "rgba(44,8,22,0.6)",
  "&:hover": {
    background: "rgba(58,10,28,0.76)",
    borderColor: "rgba(251,113,133,0.58)",
  },
  "&.Mui-disabled": {
    color: "rgba(255,154,165,0.48)",
    borderColor: "rgba(244,63,94,0.16)",
    background: "rgba(44,8,22,0.24)",
  },
};

function SiteDeleteDialog({ open, onCancel, onConfirm, sites }) {
  const siteNames = Array.isArray(sites) ? sites.map((site) => site?.name).filter(Boolean) : [];
  const previewNames = siteNames.slice(0, 4);
  const remainingCount = Math.max(siteNames.length - previewNames.length, 0);
  const deleteLabel = "Delete Site(s)";

  return (
    <Dialog open={open} onClose={onCancel} maxWidth="xs" fullWidth PaperProps={{ sx: SITE_DIALOG_PAPER_SX }}>
      <DialogTitle sx={SITE_DIALOG_TITLE_SX}>
        <Box sx={{ minWidth: 0 }}>
          <Typography sx={{ fontWeight: 700, fontSize: "1rem", lineHeight: 1.2, color: MAGIC_UI.textBright }}>
            {deleteLabel}
          </Typography>
          <Typography sx={{ mt: 0.55, fontSize: "0.84rem", lineHeight: 1.45, color: MAGIC_UI.textMuted }}>
            Permanently remove the selected site records from Borealis.
          </Typography>
        </Box>
      </DialogTitle>
      <DialogContent sx={SITE_DIALOG_CONTENT_SX}>
        {previewNames.length ? (
          <Box
            sx={{
              mt: 0.5,
              borderRadius: 2.5,
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              background: "rgba(7,12,24,0.82)",
              px: 1.5,
              py: 1.35,
            }}
          >
            <Typography sx={{ color: MAGIC_UI.textMuted, fontSize: "0.78rem", fontWeight: 700, letterSpacing: 0.75, textTransform: "uppercase" }}>
              Selected Sites
            </Typography>
            <Box sx={{ mt: 1.2, display: "flex", flexWrap: "wrap", gap: 0.9 }}>
              {previewNames.map((name) => (
                <Box
                  key={name}
                  sx={{
                    borderRadius: 999,
                    border: "1px solid rgba(148,163,184,0.26)",
                    background: "rgba(15,23,42,0.76)",
                    px: 1.2,
                    py: 0.55,
                  }}
                >
                  <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.84rem", fontWeight: 500 }}>
                    {name}
                  </Typography>
                </Box>
              ))}
              {remainingCount > 0 ? (
                <Box
                  sx={{
                    borderRadius: 999,
                    border: "1px solid rgba(244,63,94,0.26)",
                    background: "rgba(44,8,22,0.38)",
                    px: 1.2,
                    py: 0.55,
                  }}
                >
                  <Typography sx={{ color: "#ffb1b9", fontSize: "0.84rem", fontWeight: 600 }}>
                    +{remainingCount} more
                  </Typography>
                </Box>
              ) : null}
            </Box>
          </Box>
        ) : null}
      </DialogContent>
      <DialogActions sx={SITE_DIALOG_ACTIONS_SX}>
        <Button onClick={onCancel} sx={SITE_DIALOG_BUTTON_SX}>Cancel</Button>
        <Button onClick={onConfirm} disabled={!siteNames.length} sx={SITE_DIALOG_DANGER_BUTTON_SX}>
          {deleteLabel}
        </Button>
      </DialogActions>
    </Dialog>
  );
}

const PAGE_TITLE = "Sites";
const PAGE_SUBTITLE = "Manage site enrollment codes and open device inventories by site.";
const PAGE_ICON = LocationCityIcon;

export default function SiteList({ onOpenDevicesForSite, onPageMetaChange }) {
  const [rows, setRows] = useState([]);
  const [selectedIds, setSelectedIds] = useState(() => new Set());
  const [createOpen, setCreateOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const gridRef = useRef(null);
  const gridApiRef = useRef(null);
  const autoSizeHandleRef = useRef(null);
  const sendNotification = useCallback(async (message) => {
    if (!message) return;
    try {
      await fetch("/api/notifications/notify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          title: PAGE_TITLE,
          message,
          icon: "locationcity",
          variant: "info",
        }),
      });
    } catch {
      /* notification transport errors are non-blocking */
    }
  }, []);

  const fetchSites = useCallback(async () => {
    try {
      const res = await fetch("/api/sites");
      const data = await res.json();
      setRows(Array.isArray(data?.sites) ? data.sites : []);
    } catch {
      setRows([]);
    }
  }, []);

  useEffect(() => { fetchSites(); }, [fetchSites]);

  const autoSizeColumns = useCallback(() => {
    const api = gridApiRef.current || gridRef.current?.api;
    if (!api || !rows.length) return;
    const doSize = () => {
      autoSizeHandleRef.current = null;
      const liveApi = gridApiRef.current || gridRef.current?.api || api;
      if (!liveApi) return;
      if (typeof liveApi.isDestroyed === "function" && liveApi.isDestroyed()) return;
      try {
        liveApi.autoSizeColumns(AUTO_SIZE_COLUMNS, true);
      } catch {}
    };
    if (autoSizeHandleRef.current != null) {
      if (typeof cancelAnimationFrame === "function") {
        cancelAnimationFrame(autoSizeHandleRef.current);
      } else {
        clearTimeout(autoSizeHandleRef.current);
      }
      autoSizeHandleRef.current = null;
    }
    if (typeof requestAnimationFrame === "function") {
      autoSizeHandleRef.current = requestAnimationFrame(doSize);
    } else {
      autoSizeHandleRef.current = setTimeout(doSize, 0);
    }
  }, [rows.length]);

  useEffect(() => {
    autoSizeColumns();
  }, [rows, autoSizeColumns]);

  useEffect(() => {
    return () => {
      if (autoSizeHandleRef.current != null) {
        if (typeof cancelAnimationFrame === "function") {
          cancelAnimationFrame(autoSizeHandleRef.current);
        } else {
          clearTimeout(autoSizeHandleRef.current);
        }
        autoSizeHandleRef.current = null;
      }
      gridApiRef.current = null;
    };
  }, []);

  const handleCopy = useCallback(async (code) => {
    const value = (code || "").trim();
    if (!value) return;
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      window.prompt("Copy enrollment code", value);
    }
  }, []);

  const openRenameDialog = useCallback(() => {
    const selId = selectedIds.size === 1 ? Array.from(selectedIds)[0] : null;
    if (selId == null) return;
    const site = rows.find((row) => row.id === selId);
    setRenameValue(site?.name || "");
    setRenameOpen(true);
  }, [rows, selectedIds]);

  const getRowId = useCallback((params) => String(params.data?.id ?? ""), []);

  const rowSelection = useMemo(
    () => ({
      mode: "multiRow",
      checkboxes: true,
      headerCheckbox: true,
      enableClickSelection: true,
      enableSelectionWithoutKeys: true,
    }),
    []
  );

  const selectionColumnDef = useMemo(
    () => ({
      headerName: "",
      minWidth: 52,
      width: 52,
      maxWidth: 52,
      pinned: "left",
      sortable: false,
      resizable: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      suppressMovable: true,
      lockPinned: true,
      lockPosition: true,
    }),
    []
  );

  const columnDefs = useMemo(() => [
    {
      headerName: "Name",
      field: "name",
      minWidth: 220,
      flex: 1,
      cellRenderer: (params) => (
        <span
          style={{ color: "#7dd3fc", cursor: "pointer", fontWeight: 500 }}
          onClick={() => onOpenDevicesForSite && onOpenDevicesForSite(params.value)}
        >
          {params.value}
        </span>
      ),
    },
    {
      headerName: "Description",
      field: "description",
      minWidth: 220,
      flex: 1,
    },
    {
      headerName: "Devices",
      field: "device_count",
      minWidth: 140,
    },
    {
      headerName: "Agent Enrollment Code",
      field: "enrollment_code",
      minWidth: 260,
      filter: false,
      suppressHeaderMenuButton: true,
      suppressHeaderContextMenu: true,
      cellRenderer: (params) => {
        const code = params.value || "—";
        return (
          <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
            <Typography variant="body2" sx={{ fontFamily: "monospace", color: MAGIC_UI.textBright }}>
              {code}
            </Typography>
            <Tooltip title="Copy">
              <span>
                <IconButton
                  size="small"
                  onClick={() => handleCopy(code)}
                  disabled={!code || code === "—"}
                  sx={{ color: MAGIC_UI.textMuted }}
                >
                  <ContentCopyIcon fontSize="small" />
                </IconButton>
              </span>
            </Tooltip>
          </Box>
        );
      },
    },
  ], [onOpenDevicesForSite, handleCopy]);

  const defaultColDef = useMemo(() => ({
    sortable: true,
    filter: "agTextColumnFilter",
    resizable: true,
    minWidth: 160,
  }), []);

  const selectedSiteRows = useMemo(
    () => rows.filter((row) => row?.id != null && selectedIds.has(row.id)),
    [rows, selectedIds]
  );

  const selectedCount = selectedIds.size;

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "delete-site",
        label: "Delete",
        icon: <DeleteIcon />,
        tone: "danger",
        disabled: selectedIds.size === 0,
        onClick: () => setDeleteOpen(true),
      },
      {
        id: "rename-site",
        label: "Rename",
        icon: <EditIcon />,
        tone: "secondary",
        disabled: selectedIds.size !== 1,
        onClick: openRenameDialog,
      },
      {
        id: "create-site",
        label: "Create Site",
        icon: <AddIcon />,
        tone: "primary",
        onClick: () => setCreateOpen(true),
      },
    ],
    [openRenameDialog, selectedIds.size]
  );

  useEffect(() => {
    onPageMetaChange?.({
      page_title: PAGE_TITLE,
      page_subtitle: PAGE_SUBTITLE,
      page_icon: PAGE_ICON,
      page_header_actions: pageHeaderActions,
    });
    return () => onPageMetaChange?.(null);
  }, [onPageMetaChange, pageHeaderActions]);

  return (
    <Paper
      sx={{
        m: 0,
        p: 0,
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minWidth: 0,
        height: "100%",
        borderRadius: 0,
        border: "none",
        background: "transparent",
        boxShadow: "none",
        overflow: "hidden",
      }}
      elevation={0}
    >
      <PageBodyFrame variant="grid">
        <Box sx={{ display: "flex", flexDirection: "column", flexGrow: 1, minHeight: 0 }}>
          {selectedCount > 0 ? (
            <Box sx={{ display: "flex", justifyContent: "flex-end", mb: 1.25 }}>
              <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted, fontWeight: 600 }}>
                {selectedCount} selected
              </Typography>
            </Box>
          ) : null}
          <Box
            className={themeClassName}
            sx={{
              flexGrow: 1,
              minHeight: 0,
              "--ag-font-family": gridFontFamily,
              "--ag-icon-font-family": iconFontFamily,
              "& .ag-root-wrapper": {
                minHeight: "100%",
                border: "none",
                borderRadius: 0,
                background: "transparent",
              },
              "& .ag-header": {
                backgroundColor: "rgba(15,23,42,0.9)",
                borderBottom: "1px solid rgba(148,163,184,0.25)",
              },
              "& .ag-header-cell-label": {
                color: "#e2e8f0",
                fontWeight: 600,
                letterSpacing: 0.3,
              },
              "& .ag-row": {
                borderColor: "rgba(255,255,255,0.04)",
                transition: "background 0.2s ease",
              },
              "& .ag-row:nth-of-type(even)": {
                backgroundColor: "rgba(15,23,42,0.45)",
              },
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
              defaultColDef={defaultColDef}
              rowSelection={rowSelection}
              selectionColumnDef={selectionColumnDef}
              suppressCellFocus
              pagination
              paginationPageSize={20}
              paginationPageSizeSelector={[20, 50, 100]}
              animateRows
              getRowId={getRowId}
              onGridReady={(params) => {
                gridApiRef.current = params.api;
                autoSizeColumns();
              }}
              onGridPreDestroyed={() => {
                gridApiRef.current = null;
                if (autoSizeHandleRef.current != null) {
                  if (typeof cancelAnimationFrame === "function") {
                    cancelAnimationFrame(autoSizeHandleRef.current);
                  } else {
                    clearTimeout(autoSizeHandleRef.current);
                  }
                  autoSizeHandleRef.current = null;
                }
              }}
              onSelectionChanged={() => {
                const api = gridApiRef.current || gridRef.current?.api;
                if (!api) return;
                const selected = api.getSelectedNodes().map((n) => n.data?.id).filter((id) => id != null);
                setSelectedIds(new Set(selected));
              }}
              theme={myTheme}
            />
          </Box>
        </Box>
      </PageBodyFrame>

      <CreateSiteDialog
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        onCreate={async (name, description) => {
          try {
            const res = await fetch("/api/sites", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ name, description }),
            });
            if (res.ok) {
              setCreateOpen(false);
              if (name) {
                sendNotification(`Site ${name} Created Successfully`);
              }
              fetchSites();
            }
          } catch {}
        }}
      />

      <SiteDeleteDialog
        open={deleteOpen}
        onCancel={() => setDeleteOpen(false)}
        sites={selectedSiteRows}
        onConfirm={async () => {
          try {
            const ids = selectedSiteRows.map((row) => row.id);
            const selectedNames = selectedSiteRows.map((row) => row?.name).filter(Boolean);
            const resp = await fetch("/api/sites/delete", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ ids }),
            });
            if (resp.ok) {
              selectedNames.forEach((name) => sendNotification(`Site ${name} Deleted Successfully`));
            }
          } catch {}
          setDeleteOpen(false);
          setSelectedIds(new Set());
          fetchSites();
        }}
      />

      <RenameSiteDialog
        open={renameOpen}
        value={renameValue}
        onChange={setRenameValue}
        onCancel={() => setRenameOpen(false)}
        onSave={async () => {
          const newName = (renameValue || "").trim();
          if (!newName) return;
          const selId = selectedIds.size === 1 ? Array.from(selectedIds)[0] : null;
          if (!selId) return;
          const oldName = rows.find((r) => r.id === selId)?.name || "Site";
          try {
            const res = await fetch("/api/sites/rename", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ id: selId, new_name: newName }),
            });
            if (res.ok) {
              setRenameOpen(false);
              sendNotification(`Site ${oldName} Renamed as ${newName} Successfully`);
              fetchSites();
            }
          } catch {}
        }}
      />
    </Paper>
  );
}
