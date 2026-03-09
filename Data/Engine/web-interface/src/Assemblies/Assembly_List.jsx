import React, { useState, useEffect, useCallback, useMemo, useRef } from "react";
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
import PolylineIcon from "@mui/icons-material/Polyline";
import CodeIcon from "@mui/icons-material/Code";
import MenuBookIcon from "@mui/icons-material/MenuBook";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { ConfirmDeleteDialog, NewWorkflowDialog } from "../Dialogs";
import { DomainBadge, DirtyStatePill, resolveDomainMeta, DOMAIN_OPTIONS } from "./Assembly_Badges";

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
const PAGE_SIZE = 25;

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

const TypeCellRenderer = React.memo(function TypeCellRenderer(props) {
  const typeKey = props?.data?.typeKey;
  const meta = typeKey ? TYPE_METADATA[typeKey] : null;
  if (!meta) return null;
  const { Icon, label } = meta;
  return (
    <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
      <Icon sx={{ fontSize: 20, color: BOREALIS_BLUE }} />
      <Typography component="span" sx={{ fontSize: 14, color: "#f5f7fa" }}>
        {label}
      </Typography>
    </Box>
  );
});

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
    item.display_name ||
    fileName.replace(/\.[^.]+$/, "") ||
    fileName ||
    "Assembly";
  const summary = item.summary || "";
  const queueRecord = queueEntry || null;
  const isDirty = Boolean(item.is_dirty);

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
    domain,
    domainLabel: domainMeta.label,
    isDirty,
    dirtySince: item.dirty_since || queueRecord?.dirty_since || "",
    lastPersisted: item.last_persisted || queueRecord?.last_persisted || "",
    queueEntry: queueRecord,
    payloadGuid: item.payload_guid,
    updatedAt: item.updated_at,
    createdAt: item.created_at,
    raw: item,
  };
};

export default function AssemblyList({ onOpenWorkflow, onOpenScript, userRole = "User", onPageMetaChange }) {
  const gridRef = useRef(null);
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

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
  const isAdmin = (userRole || "").toLowerCase() === "admin";
  const sendNotification = useCallback(async (message) => {
    if (!message) return;
    try {
      await fetch("/api/notifications/notify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          title: "Assemblies",
          message,
          icon: "apps",
          variant: "info",
        }),
      });
    } catch {
      /* notification transport is best-effort */
    }
  }, []);

  const fetchAssemblies = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const resp = await fetch("/api/assemblies");
      if (!resp.ok) {
        const problem = await resp.text();
        throw new Error(problem || `Failed to load assemblies (HTTP ${resp.status})`);
      }
      const payload = await resp.json();
      const items = Array.isArray(payload?.items) ? payload.items : [];
      const queue = Array.isArray(payload?.queue) ? payload.queue : [];
      const queueMap = queue.reduce((acc, entry) => {
        if (entry && entry.assembly_guid) {
          acc[entry.assembly_guid] = entry;
        }
        return acc;
      }, {});
      const processed = items
        .map((item) => normalizeRow(item, queueMap[item?.assembly_guid || item?.assembly_id || ""]))
        .filter(Boolean);
      setRows(processed);
      setTimeout(() => {
        const columnApi = gridRef.current?.columnApi;
        if (columnApi) {
          const ids = ["assemblyType", "source", "name"];
          columnApi.autoSizeColumns(ids, false);
        }
      }, 0);
    } catch (err) {
      console.error("Failed to load assemblies:", err);
      setRows([]);
      setError(err?.message || "Failed to load assemblies");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchAssemblies();
  }, [fetchAssemblies]);

  const openRow = useCallback(
    (row) => {
      if (!row) return;
      if (row.typeKey === "workflow") {
        onOpenWorkflow?.(row);
        return;
      }
      onOpenScript?.(row);
    },
    [onOpenWorkflow, onOpenScript],
  );

  const handleRowDoubleClicked = useCallback(
    (event) => {
      openRow(event?.data);
    },
    [openRow],
  );

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

  const columnDefs = useMemo(
    () => [
      {
        colId: "assemblyType",
        field: "assemblyType",
        headerName: "Type",
        valueGetter: (params) => TYPE_METADATA[params?.data?.typeKey]?.label || "",
        cellRenderer: TypeCellRenderer,
        minWidth: 160,
        flex: 0,
        sortable: true,
        filter: "agTextColumnFilter",
        resizable: true,
      },
      {
        colId: "source",
        field: "domain",
        headerName: "Source",
        valueGetter: (params) => params?.data?.domain || "",
        valueFormatter: (params) => resolveDomainMeta(params?.value).label,
        filter: "agTextColumnFilter",
        cellRenderer: SourceCellRenderer,
        minWidth: 170,
        flex: 0,
        sortable: true,
        resizable: true,
      },
      {
        colId: "path",
        field: "path",
        headerName: "Path",
        valueGetter: (params) => params?.data?.folder || "",
        cellStyle: { color: DARKER_GRAY, fontSize: 13 },
        minWidth: 200,
        flex: 0,
        sortable: true,
        filter: "agTextColumnFilter",
        resizable: true,
      },
      {
        colId: "name",
        field: "name",
        headerName: "Name",
        valueGetter: (params) => params?.data?.name || "",
        cellRenderer: NameCellRenderer,
        minWidth: 220,
        flex: 0,
        sortable: true,
        filter: "agTextColumnFilter",
        resizable: true,
      },
      {
        colId: "description",
        field: "description",
        headerName: "Description",
        valueGetter: (params) => params?.data?.description || "",
        flex: 1, // Only Description flexes to take remaining width
        minWidth: 300,
        sortable: true,
        filter: "agTextColumnFilter",
        resizable: true,
      },
    ],
    [],
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

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "assemblies-refresh",
        label: "Refresh",
        icon: <CachedIcon />,
        tone: "secondary",
        loading,
        onClick: fetchAssemblies,
      },
      {
        id: "assemblies-new",
        label: "New Assembly",
        icon: <AddIcon />,
        tone: "primary",
        onClick: (event) => setNewMenuAnchor(event.currentTarget),
      },
    ],
    [fetchAssemblies, loading]
  );

  useEffect(() => {
    onPageMetaChange?.({
      page_title: "Assemblies",
      page_subtitle: "Collections of scripts, workflows, and playbooks used to automate tasks across devices.",
      page_icon: AppsIcon,
      page_header_actions: pageHeaderActions,
    });
    return () => onPageMetaChange?.(null);
  }, [onPageMetaChange, pageHeaderActions]);

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
    const context = {
      folder: "",
      suggestedFileName: trimmed,
      name: trimmed,
      defaultType: isAnsible ? "ansible" : "powershell",
      type: isAnsible ? "ansible" : "powershell",
      category: isAnsible ? "application" : "script",
    };
    const newRow = {
      assemblyGuid: null,
      typeKey: isAnsible ? "ansible" : "script",
      assemblyKind: isAnsible ? "ansible" : "script",
      assemblyType: isAnsible ? "ansible" : context.type,
      name: trimmed,
      domain: "user",
      isDirty: false,
      isNew: true,
      createContext: context,
    };
    onOpenScript?.(newRow);
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
    onOpenWorkflow?.(newWorkflow);
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
      <Box sx={{ px: 2, mt: 1, minHeight: 28, display: "flex", alignItems: "center" }}>
        {error ? (
          <Typography variant="body2" sx={{ color: "#ff8a8a" }}>
            {error}
          </Typography>
        ) : null}
      </Box>
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
      <Box sx={{ mt: "10px", px: 2, pb: 2, flexGrow: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
        <Box
          className={gridWrapperClass}
          sx={{
            // Card chrome
            background: "transparent",
            borderRadius: 0,
            border: "none",
            boxShadow: "none",
            p: 0,

            // Layout
            width: "100%",
            flexGrow: 1,
            minHeight: 0,
            height: "100%",
            position: "relative",

            // Typography
            fontFamily: gridFontFamily,
            "--ag-font-family": gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,

            // AG Grid overrides
            "& .ag-root-wrapper": {
              borderRadius: 1,
              minHeight: 400,
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
            // Theme CSS variables for fine-grain color control
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
            rowData={rows}
            columnDefs={columnDefs}
            defaultColDef={defaultColDef}
            context={{ openRow }}
            rowSelection="single"
            suppressCellFocus
            pagination
            paginationPageSize={PAGE_SIZE}
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
            style={{
              width: "100%",
              height: "100%",
              fontFamily: gridFontFamily,
              "--ag-icon-font-family": iconFontFamily,
            }}
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
        <MenuItem onClick={startRename}>Rename</MenuItem>
        <MenuItem sx={{ color: "#ff8a8a" }} onClick={startDelete}>
          Delete
        </MenuItem>
      </Menu>

      <Dialog open={renameDialogOpen} onClose={() => setRenameDialogOpen(false)}>
        <DialogTitle>Rename Assembly</DialogTitle>
        <DialogContent>
          <TextField
            autoFocus
            fullWidth
            label="New Name"
            variant="outlined"
            value={renameValue}
            onChange={(event) => setRenameValue(event.target.value)}
            sx={{
              mt: 1,
              "& .MuiOutlinedInput-root": {
                "& fieldset": { borderColor: "#444" },
                "&:hover fieldset": { borderColor: "#666" },
              },
            }}
          />
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setRenameDialogOpen(false)} sx={{ textTransform: "none" }}>
            Cancel
          </Button>
          <Button onClick={handleRenameSave} disabled={!renameValue.trim()} sx={{ textTransform: "none", color: BOREALIS_BLUE }}>
            Save
          </Button>
        </DialogActions>
      </Dialog>

      <ConfirmDeleteDialog
        open={deleteDialogOpen}
        message="If you delete this assembly, there is no undo. Are you sure you want to proceed?"
        onCancel={() => setDeleteDialogOpen(false)}
        onConfirm={handleDeleteConfirm}
      />

      <Dialog open={cloneDialog.open} onClose={handleCloneClose}>
        <DialogTitle>Clone Assembly</DialogTitle>
        <DialogContent sx={{ minWidth: 280 }}>
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
            sx={{ ...SELECT_BASE_SX, mt: 1 }}
            SelectProps={{ MenuProps: MENU_PROPS }}
          >
            {DOMAIN_OPTIONS.filter((option) => option.value !== cloneDialog.row?.domain).map((option) => (
              <MenuItem key={option.value} value={option.value}>
                {option.label}
              </MenuItem>
            ))}
          </TextField>
          <Typography variant="body2" sx={{ mt: 1, color: "#9ba3b4" }}>
            Cloning creates a copy of the assembly in the selected domain.
          </Typography>
        </DialogContent>
        <DialogActions>
          <Button onClick={handleCloneClose} sx={{ textTransform: "none", color: "#58a6ff" }}>
            Cancel
          </Button>
          <Button onClick={handleCloneConfirm} sx={{ textTransform: "none", color: "#58a6ff" }}>
            Clone
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog
        open={scriptDialog.open}
        onClose={() => {
          setScriptDialog({ open: false, typeKey: null });
          setScriptName("");
        }}
      >
        <DialogTitle>{scriptDialog.typeKey === "ansible" ? "New Ansible Playbook" : "New Script"}</DialogTitle>
        <DialogContent>
          <TextField
            autoFocus
            fullWidth
            label="Name"
            variant="outlined"
            value={scriptName}
            onChange={(event) => setScriptName(event.target.value)}
            sx={{
              mt: 1,
              "& .MuiOutlinedInput-root": {
                "& fieldset": { borderColor: "#444" },
                "&:hover fieldset": { borderColor: "#666" },
              },
            }}
          />
        </DialogContent>
        <DialogActions>
          <Button
            onClick={() => {
              setScriptDialog({ open: false, typeKey: null });
              setScriptName("");
            }}
            sx={{ textTransform: "none" }}
          >
            Cancel
          </Button>
          <Button onClick={handleCreateScript} disabled={!scriptName.trim()} sx={{ textTransform: "none", color: BOREALIS_BLUE }}>
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
