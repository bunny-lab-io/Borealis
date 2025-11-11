import React, { useEffect, useMemo, useState, useCallback, useRef } from "react";
import {
  Box,
  Paper,
  Typography,
  Button,
  IconButton,
  Tooltip,
} from "@mui/material";
import AddIcon from "@mui/icons-material/Add";
import LocationCityIcon from "@mui/icons-material/LocationCity";

import DeleteIcon from "@mui/icons-material/DeleteOutline";
import EditIcon from "@mui/icons-material/Edit";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { CreateSiteDialog, ConfirmDeleteDialog, RenameSiteDialog } from "../Dialogs.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const myTheme = themeQuartz.withParams({
  accentColor: "#8b5cf6",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 14,
});

const themeClassName = myTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';

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

const RAINBOW_BUTTON_SX = {
  borderRadius: 999,
  textTransform: "none",
  fontWeight: 600,
  px: 2.5,
  color: "#f8fafc",
  border: "1px solid transparent",
  backgroundImage:
    "linear-gradient(#05070f, #05070f), linear-gradient(120deg, #ff7c7c, #ffd36b, #7dffb7, #7dd3fc, #c084fc)",
  backgroundOrigin: "border-box",
  backgroundClip: "padding-box, border-box",
  boxShadow: "0 0 26px rgba(45, 212, 191, 0.45)",
  "&:hover": {
    boxShadow: "0 0 32px rgba(45, 212, 191, 0.55)",
  },
};

export default function SiteList({ onOpenDevicesForSite }) {
  const [rows, setRows] = useState([]);
  const [selectedIds, setSelectedIds] = useState(() => new Set());
  const [createOpen, setCreateOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const gridRef = useRef(null);

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

  const columnDefs = useMemo(() => [
    {
      headerName: "",
      field: "__select__",
      checkboxSelection: true,
      headerCheckboxSelection: true,
      width: 52,
      pinned: "left",
    },
    {
      headerName: "Name",
      field: "name",
      minWidth: 180,
      cellRenderer: (params) => (
        <span
          style={{ color: "#7dd3fc", cursor: "pointer", fontWeight: 500 }}
          onClick={() => onOpenDevicesForSite && onOpenDevicesForSite(params.value)}
        >
          {params.value}
        </span>
      ),
    },
    { headerName: "Description", field: "description", minWidth: 220 },
    { headerName: "Devices", field: "device_count", minWidth: 120 },
  ], [onOpenDevicesForSite]);

  const defaultColDef = useMemo(() => ({
    sortable: true,
    filter: "agTextColumnFilter",
    resizable: true,
    flex: 1,
    minWidth: 160,
  }), []);

  const heroStats = useMemo(() => ({
    totalSites: rows.length,
    totalDevices: rows.reduce((acc, r) => acc + (r.device_count || 0), 0),
    selected: selectedIds.size,
  }), [rows, selectedIds]);

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
        border: `1px solid ${MAGIC_UI.panelBorder}`,
        background: MAGIC_UI.shellBg,
        boxShadow: "0 25px 80px rgba(6, 12, 30, 0.8)",
        overflow: "hidden",
      }}
      elevation={0}
    >
      {/* Hero Section Removed — integrated header and buttons */}
      <Box sx={{ p: { xs: 2, md: 3 }, pb: 1, display: "flex", alignItems: "center", justifyContent: "space-between", flexWrap: "wrap" }}>
        <Box sx={{ display: "flex", flexDirection: "column", gap: 1 }}>
          <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
            <LocationCityIcon sx={{ color: MAGIC_UI.accentA }} />
            <Typography variant="h6" sx={{ color: MAGIC_UI.textBright, fontWeight: 700, fontSize: "1.3rem" }}>
            Managed Sites
          </Typography>
          </Box>
          <Typography sx={{ color: MAGIC_UI.textMuted }}>
            {`Monitoring ${heroStats.totalDevices} devices across ${heroStats.totalSites} site(s)`}
          </Typography>
          {heroStats.selected > 0 && (
            <Typography sx={{ color: MAGIC_UI.accentA, fontSize: "0.85rem", fontWeight: 600 }}>
              {heroStats.selected} selected
            </Typography>
          )}
        </Box>
        <Box sx={{ display: "flex", alignItems: "center", gap: 1, flexWrap: "wrap", justifyContent: "flex-end" }}>
          <Button variant="contained" size="small" startIcon={<AddIcon />} sx={RAINBOW_BUTTON_SX} onClick={() => setCreateOpen(true)}>
            Create Site
          </Button>
          <Button
            variant="outlined"
            size="small"
            startIcon={<EditIcon />}
            disabled={selectedIds.size !== 1}
            onClick={() => {
              const selId = selectedIds.size === 1 ? Array.from(selectedIds)[0] : null;
              if (selId != null) {
                const site = rows.find((r) => r.id === selId);
                setRenameValue(site?.name || "");
                setRenameOpen(true);
              }
            }}
            sx={{
              borderColor: "rgba(148,163,184,0.4)",
              color: MAGIC_UI.textBright,
              "&:hover": { borderColor: MAGIC_UI.accentA },
            }}
          >
            Rename
          </Button>
          <Button
            variant="outlined"
            size="small"
            startIcon={<DeleteIcon />}
            disabled={selectedIds.size === 0}
            onClick={() => setDeleteOpen(true)}
            sx={{
              borderColor: selectedIds.size ? "#f87171" : "rgba(148,163,184,0.3)",
              color: selectedIds.size ? "#f87171" : MAGIC_UI.textMuted,
              "&:hover": { borderColor: "#fb7185" },
            }}
          >
            Delete
          </Button>
        </Box>
      </Box>

      {/* AG Grid */}
      <Box sx={{ px: { xs: 2, md: 3 }, pb: 3, flexGrow: 1, display: "flex", flexDirection: "column" }}>
        <Box
          className={themeClassName}
          sx={{
            flexGrow: 1,
            borderRadius: 3,
            border: `1px solid ${MAGIC_UI.panelBorder}`,
            background: "linear-gradient(165deg, rgba(2,6,23,0.9), rgba(8,12,32,0.85))",
            boxShadow: "0 20px 60px rgba(2,8,23,0.85)",
            "--ag-font-family": gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,
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
              backgroundColor: "rgba(124, 58, 237, 0.15) !important",
            },
            "& .ag-row-selected": {
              backgroundColor: "rgba(56,189,248,0.18) !important",
              boxShadow: "inset 0 0 0 1px rgba(56,189,248,0.35)",
            },
          }}
        >
          <AgGridReact
            ref={gridRef}
            rowData={rows}
            columnDefs={columnDefs}
            defaultColDef={defaultColDef}
            rowSelection="multiple"
            rowMultiSelectWithClick
            pagination
            paginationPageSize={20}
            paginationPageSizeSelector={[20, 50, 100]}
            animateRows
            onSelectionChanged={() => {
              const api = gridRef.current?.api;
              if (!api) return;
              const selected = api.getSelectedNodes().map((n) => n.data?.id).filter(Boolean);
              setSelectedIds(new Set(selected));
            }}
            theme={myTheme}
          />
        </Box>
      </Box>

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
              fetchSites();
            }
          } catch {}
        }}
      />

      <ConfirmDeleteDialog
        open={deleteOpen}
        message={`Delete ${selectedIds.size} selected site(s)? This cannot be undone.`}
        onCancel={() => setDeleteOpen(false)}
        onConfirm={async () => {
          try {
            const ids = Array.from(selectedIds);
            await fetch("/api/sites/delete", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ ids }),
            });
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
          try {
            const res = await fetch("/api/sites/rename", {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ id: selId, new_name: newName }),
            });
            if (res.ok) {
              setRenameOpen(false);
              fetchSites();
            }
          } catch {}
        }}
      />
    </Paper>
  );
}
