import React, { useState, useEffect, useCallback, useMemo, useRef } from "react";
import {
  Paper,
  Box,
  Typography,
  Button,
  Menu,
  MenuItem,
  IconButton,
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

ModuleRegistry.registerModules([AllCommunityModule]);

const myTheme = themeQuartz.withParams({
  accentColor: "#FFA6FF",
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

const TYPE_METADATA = {
  workflows: {
    label: "Workflow",
    Icon: PolylineIcon,
  },
  scripts: {
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

const normalizeRow = (island, item) => {
  const relPath = String(item?.rel_path || "").replace(/\\/g, "/");
  const fileName = String(item?.file_name || relPath.split("/").pop() || "");
  const folder = relPath ? relPath.split("/").slice(0, -1).join("/") : "";
  const idSeed = relPath || fileName || `${Date.now()}_${Math.random().toString(36).slice(2)}`;
  const name =
    island === "workflows"
      ? item?.tab_name || fileName.replace(/\.[^.]+$/, "") || fileName || "Workflow"
      : item?.name || fileName.replace(/\.[^.]+$/, "") || fileName || "Assembly";
  // For workflows, always show 'workflow' in Category per request
  const category =
    island === "workflows"
      ? "workflow"
      : item?.category || "";
  const description = island === "workflows" ? "" : item?.description || "";
  return {
    id: `${island}:${idSeed}`,
    typeKey: island,
    name,
    category,
    description,
    relPath,
    fileName,
    folder,
    raw: item || {},
  };
};

export default function AssemblyList({ onOpenWorkflow, onOpenScript }) {
  const gridRef = useRef(null);
  const [rows, setRows] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const [newMenuAnchor, setNewMenuAnchor] = useState(null);
  const [scriptDialog, setScriptDialog] = useState({ open: false, island: null });
  const [scriptName, setScriptName] = useState("");
  const [workflowDialogOpen, setWorkflowDialogOpen] = useState(false);
  const [workflowName, setWorkflowName] = useState("");

  const [contextMenu, setContextMenu] = useState(null);
  const [activeRow, setActiveRow] = useState(null);

  const [renameDialogOpen, setRenameDialogOpen] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);

  const fetchAssemblies = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const islands = ["workflows", "scripts", "ansible"];
      const results = await Promise.all(
        islands.map(async (island) => {
          const resp = await fetch(`/api/assembly/list?island=${encodeURIComponent(island)}`);
          if (!resp.ok) {
            const problem = await resp.text();
            throw new Error(problem || `Failed to load ${island} assemblies (HTTP ${resp.status})`);
          }
          const data = await resp.json();
          const items = Array.isArray(data?.items) ? data.items : [];
          return items.map((item) => normalizeRow(island, item));
        }),
      );
      setRows(results.flat());
      // After data load, auto-size specific columns
      setTimeout(() => {
        const columnApi = gridRef.current?.columnApi;
        if (columnApi) {
          const ids = ["assemblyType", "location", "category", "name"];
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
      if (row.typeKey === "workflows") {
        const payload = {
          ...row.raw,
          rel_path: row.relPath,
          file_name: row.fileName,
        };
        if (!payload.name) payload.name = row.name;
        if (!payload.tab_name) payload.tab_name = row.name;
        onOpenWorkflow?.(payload);
        return;
      }
      const mode = row.typeKey === "ansible" ? "ansible" : "scripts";
      onOpenScript?.(row.relPath, mode, null);
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

  const startDelete = () => {
    if (!activeRow) return;
    setDeleteDialogOpen(true);
    closeContextMenu();
  };

  const handleRenameSave = async () => {
    const target = activeRow;
    const trimmed = renameValue.trim();
    if (!target || !trimmed) {
      setRenameDialogOpen(false);
      return;
    }
    try {
      const payload = {
        island: target.typeKey,
        kind: "file",
        path: target.relPath,
        new_name: trimmed,
      };
      if (target.typeKey !== "workflows" && target.raw?.type) {
        payload.type = target.raw.type;
      }
      const resp = await fetch(`/api/assembly/rename`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const data = await resp.json();
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
    if (!target) {
      setDeleteDialogOpen(false);
      return;
    }
    try {
      const resp = await fetch(`/api/assembly/delete`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          island: target.typeKey,
          kind: "file",
          path: target.relPath,
        }),
      });
      const data = await resp.json();
      if (!resp.ok) throw new Error(data?.error || `HTTP ${resp.status}`);
      setDeleteDialogOpen(false);
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
        colId: "location",
        field: "location",
        headerName: "Location",
        valueGetter: (params) => params?.data?.folder || "",
        cellStyle: { color: DARKER_GRAY, fontSize: 13 },
        minWidth: 180,
        flex: 0,
        sortable: true,
        filter: "agTextColumnFilter",
        resizable: true,
      },
      {
        colId: "category",
        field: "category",
        headerName: "Category",
        valueGetter: (params) => params?.data?.category || "",
        minWidth: 160,
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
      // Remove floating textboxes at the top (use column menu filters instead)
      floatingFilter: false,
      resizable: true,
      flex: 0,
      minWidth: 140,
    }),
    [],
  );

  const gridWrapperClass = themeClassName;

  const handleRefresh = () => fetchAssemblies();

  const handleNewAssemblyOption = (island) => {
    setNewMenuAnchor(null);
    if (island === "workflows") {
      setWorkflowName("");
      setWorkflowDialogOpen(true);
      return;
    }
    setScriptName("");
    setScriptDialog({ open: true, island });
  };

  const handleCreateScript = () => {
    const trimmed = scriptName.trim();
    if (!trimmed || !scriptDialog.island) return;
    const isAnsible = scriptDialog.island === "ansible";
    const context = {
      folder: "",
      suggestedFileName: trimmed,
      name: trimmed,
      defaultType: isAnsible ? "ansible" : "powershell",
      type: isAnsible ? "ansible" : "powershell",
      category: isAnsible ? "application" : "script",
    };
    onOpenScript?.(null, isAnsible ? "ansible" : "scripts", context);
    setScriptDialog({ open: false, island: null });
    setScriptName("");
  };

  const handleCreateWorkflow = () => {
    const trimmed = workflowName.trim();
    if (!trimmed) return;
    setWorkflowDialogOpen(false);
    onOpenWorkflow?.(null, "", trimmed);
    setWorkflowName("");
  };

  return (
    <Paper
      sx={{
        m: 2,
        p: 0,
        bgcolor: "#1e1e1e",
        fontFamily: gridFontFamily,
        color: "#f5f7fa",
        display: "flex",
        flexDirection: "column",
        flexGrow: 1,
        minWidth: 0,
        height: "100%",
      }}
      elevation={2}
    >
      <Box sx={{ px: 2, pt: 2, pb: 1 }}>
        <Typography variant="h6" sx={{ color: BOREALIS_BLUE, mb: 0.5 }}>
          Assemblies
        </Typography>
        <Typography variant="body2" sx={{ color: "#aaa" }}>
          Collections of scripts, workflows, and playbooks used to automate tasks across devices.
        </Typography>
      </Box>
      <Box sx={{ px: 2, display: "flex", alignItems: "center", justifyContent: "space-between", gap: 1 }}>
        <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
          <IconButton
            size="small"
            onClick={handleRefresh}
            sx={{
              color: "#ccc",
              border: "1px solid #333",
              borderRadius: 1,
              "&:hover": { color: "#fff", borderColor: "#555" },
            }}
          >
            <CachedIcon fontSize="small" />
          </IconButton>
          {error ? (
            <Typography variant="body2" sx={{ color: "#ff8a8a" }}>
              {error}
            </Typography>
          ) : null}
        </Box>
        <Button
          variant="contained"
          size="small"
          startIcon={<AddIcon />}
          onClick={(event) => setNewMenuAnchor(event.currentTarget)}
          sx={{
            bgcolor: BOREALIS_BLUE,
            "&:hover": { bgcolor: "#3975c7" },
            textTransform: "none",
          }}
        >
          New Assembly
        </Button>
        <Menu
          anchorEl={newMenuAnchor}
          open={Boolean(newMenuAnchor)}
          onClose={() => setNewMenuAnchor(null)}
          PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
        >
          <MenuItem onClick={() => handleNewAssemblyOption("scripts")}>Script</MenuItem>
          <MenuItem onClick={() => handleNewAssemblyOption("workflows")}>Workflow</MenuItem>
          <MenuItem onClick={() => handleNewAssemblyOption("ansible")}>Ansible Playbook</MenuItem>
        </Menu>
      </Box>
      <Box sx={{ mt: "10px", px: 2, pb: 2, flexGrow: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
        <Box
          className={gridWrapperClass}
          sx={{
            width: "100%",
            flexGrow: 1,
            minHeight: 0,
            height: "100%",
            fontFamily: gridFontFamily,
            "--ag-font-family": gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,
            position: "relative",
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
            // Vertically center cell content across the board
            "& .ag-cell": {
              display: "flex",
              alignItems: "center",
              paddingTop: "8px",
              paddingBottom: "8px",
            },
          }}
        >
          <AgGridReact
            ref={gridRef}
            rowData={rows}
            columnDefs={columnDefs}
            defaultColDef={defaultColDef}
            context={{ openRow }}
            rowSelection="single"
            pagination
            paginationPageSize={PAGE_SIZE}
            animateRows
            onRowDoubleClicked={handleRowDoubleClicked}
            onCellContextMenu={handleCellContextMenu}
            getRowId={(params) =>
              params?.data?.id || params?.data?.relPath || params?.data?.fileName || String(params?.rowIndex ?? "")
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
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
      >
        <MenuItem
          onClick={() => {
            closeContextMenu();
            openRow(activeRow);
          }}
        >
          Open
        </MenuItem>
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

      <Dialog
        open={scriptDialog.open}
        onClose={() => {
          setScriptDialog({ open: false, island: null });
          setScriptName("");
        }}
      >
        <DialogTitle>{scriptDialog.island === "ansible" ? "New Ansible Playbook" : "New Script"}</DialogTitle>
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
              setScriptDialog({ open: false, island: null });
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
