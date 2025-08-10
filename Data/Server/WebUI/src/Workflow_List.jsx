import React, { useState, useMemo, useEffect, useCallback } from "react";
import {
  Paper,
  Box,
  Typography,
  Button,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TableSortLabel,
  IconButton,
  Menu,
  MenuItem
} from "@mui/material";
import { PlayCircle as PlayCircleIcon, MoreVert as MoreVertIcon, CreateNewFolder as CreateNewFolderIcon } from "@mui/icons-material";
import { RenameWorkflowDialog, CreateFolderDialog, MoveWorkflowDialog } from "./Dialogs";

function formatDateTime(dateString) {
  if (!dateString) return "";
  const date = new Date(dateString);
  if (isNaN(date)) return "";
  const day = String(date.getDate()).padStart(2, "0");
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const year = date.getFullYear();
  let hours = date.getHours();
  const minutes = String(date.getMinutes()).padStart(2, "0");
  const ampm = hours >= 12 ? "PM" : "AM";
  hours = hours % 12 || 12;
  return `${day}/${month}/${year} ${hours}:${minutes}${ampm}`;
}

export default function WorkflowList({ onOpenWorkflow }) {
  const [rows, setRows] = useState([]);
  const [orderBy, setOrderBy] = useState("name");
  const [order, setOrder] = useState("asc");
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [selected, setSelected] = useState(null);
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [createValue, setCreateValue] = useState("");
  const [moveOpen, setMoveOpen] = useState(false);
  const [folders, setFolders] = useState([]);
  const [selectedFolder, setSelectedFolder] = useState("");

  const loadRows = useCallback(async () => {
    try {
      const resp = await fetch("/api/storage/load_workflows");
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const mapped = (data.workflows || []).map((w) => ({
        ...w,
        name: w.tab_name && w.tab_name.trim() ? w.tab_name.trim() : w.file_name,
        description: "",
        category: "",
        lastEdited: w.last_edited,
        lastEditedEpoch: w.last_edited_epoch
      }));
      setRows(mapped);
    } catch (err) {
      console.error("Failed to load workflows:", err);
      setRows([]);
    }
  }, []);

  const loadFolders = useCallback(async () => {
    try {
      const resp = await fetch("/api/storage/list_folders");
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      setFolders(data.folders || []);
    } catch (err) {
      console.error("Failed to load folders:", err);
      setFolders([]);
    }
  }, []);

  useEffect(() => {
    loadRows();
    loadFolders();
  }, [loadRows, loadFolders]);

  const handleSort = (col) => {
    if (orderBy === col) setOrder(order === "asc" ? "desc" : "asc");
    else {
      setOrderBy(col);
      setOrder("asc");
    }
  };

  const sorted = useMemo(() => {
    const dir = order === "asc" ? 1 : -1;
    return [...rows].sort((a, b) => {
      if (orderBy === "lastEdited" || orderBy === "lastEditedEpoch") {
        const A = Number(a.lastEditedEpoch || 0);
        const B = Number(b.lastEditedEpoch || 0);
        return (A - B) * dir;
      }
      const A = a[orderBy] || "";
      const B = b[orderBy] || "";
      return String(A).localeCompare(String(B)) * dir;
    });
  }, [rows, orderBy, order]);

  const handleNewWorkflow = () => {
    if (onOpenWorkflow) {
      onOpenWorkflow();
    }
  };

  const handleCreateFolder = () => {
    setCreateValue("");
    setCreateOpen(true);
  };

  const handleCreateSave = async () => {
    try {
      await fetch("/api/storage/create_folder", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: createValue })
      });
      await loadFolders();
    } catch (err) {
      console.error("Failed to create folder:", err);
    }
    setCreateOpen(false);
    setCreateValue("");
  };

  const handleRowClick = (workflow) => {
    if (onOpenWorkflow) {
      onOpenWorkflow(workflow);
    }
  };

  const openMenu = (e, row) => {
    e.stopPropagation();
    setMenuAnchor(e.currentTarget);
    setSelected(row);
  };

  const closeMenu = () => setMenuAnchor(null);

  const startRename = () => {
    closeMenu();
    if (selected) {
      const initial = selected.tab_name && selected.tab_name.trim().length > 0
        ? selected.tab_name.trim()
        : selected.file_name.replace(/\.json$/i, "");
      setRenameValue(initial);
      setRenameOpen(true);
    }
  };

  const handleRenameSave = async () => {
    if (!selected) return;
    try {
      await fetch("/api/storage/rename_workflow", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: selected.rel_path, new_name: renameValue })
      });
      await loadRows();
    } catch (err) {
      console.error("Failed to rename workflow:", err);
    }
    setRenameOpen(false);
    setSelected(null);
  };

  const startMove = () => {
    closeMenu();
    setSelectedFolder("");
    setMoveOpen(true);
  };

  const handleMove = async () => {
    if (!selected) return;
    try {
      await fetch("/api/storage/move_workflow", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: selected.rel_path, dest: selectedFolder })
      });
      await loadRows();
      await loadFolders();
    } catch (err) {
      console.error("Failed to move workflow:", err);
    }
    setMoveOpen(false);
    setSelected(null);
  };

  const renderNameCell = (r) => {
    const hasPrefix = r.breadcrumb_prefix && r.breadcrumb_prefix.length > 0;
    const primary = r.tab_name && r.tab_name.trim().length > 0 ? r.tab_name.trim() : r.file_name;
    return (
      <Box component="span">
        {hasPrefix && (
          <Typography
            component="span"
            sx={{ color: "#6b6b6b", mr: 0.5 }}
          >
            {r.breadcrumb_prefix} {">"}{" "}
          </Typography>
        )}
        <Typography component="span" sx={{ color: "#e6edf3" }}>
          {primary}
        </Typography>
      </Box>
    );
  };

  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box sx={{ p: 2, pb: 1, display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <Box>
          <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>
            Workflows
          </Typography>
          <Typography variant="body2" sx={{ color: "#aaa" }}>
            List of available workflows.
          </Typography>
        </Box>
        <Box sx={{ display: "flex", gap: 1 }}>
          <Button
            startIcon={<CreateNewFolderIcon />}
            sx={{
              color: "#58a6ff",
              borderColor: "#58a6ff",
              textTransform: "none",
              border: "1px solid #58a6ff",
              backgroundColor: "#1e1e1e",
              "&:hover": { backgroundColor: "#1b1b1b" }
            }}
            onClick={handleCreateFolder}
          >
            Create Folder
          </Button>
          <Button
            startIcon={<PlayCircleIcon />}
            sx={{
              color: "#58a6ff",
              borderColor: "#58a6ff",
              textTransform: "none",
              border: "1px solid #58a6ff",
              backgroundColor: "#1e1e1e",
              "&:hover": { backgroundColor: "#1b1b1b" }
            }}
            onClick={handleNewWorkflow}
          >
            New Workflow
          </Button>
        </Box>
      </Box>
      <Table size="small" sx={{ minWidth: 680 }}>
        <TableHead>
          <TableRow>
            <TableCell sortDirection={orderBy === "name" ? order : false}>
              <TableSortLabel
                active={orderBy === "name"}
                direction={orderBy === "name" ? order : "asc"}
                onClick={() => handleSort("name")}
              >
                Name
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={orderBy === "description" ? order : false}>
              <TableSortLabel
                active={orderBy === "description"}
                direction={orderBy === "description" ? order : "asc"}
                onClick={() => handleSort("description")}
              >
                Description
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={orderBy === "category" ? order : false}>
              <TableSortLabel
                active={orderBy === "category"}
                direction={orderBy === "category" ? order : "asc"}
                onClick={() => handleSort("category")}
              >
                Category
              </TableSortLabel>
            </TableCell>
            <TableCell sortDirection={orderBy === "lastEdited" ? order : false}>
              <TableSortLabel
                active={orderBy === "lastEdited"}
                direction={orderBy === "lastEdited" ? order : "asc"}
                onClick={() => handleSort("lastEdited")}
              >
                Last Edited
              </TableSortLabel>
            </TableCell>
            <TableCell />
          </TableRow>
        </TableHead>
        <TableBody>
          {sorted.map((r, i) => (
            <TableRow
              key={i}
              hover
              sx={{ cursor: "pointer" }}
              onClick={() => handleRowClick(r)}
            >
              <TableCell>{renderNameCell(r)}</TableCell>
              <TableCell>{r.description}</TableCell>
              <TableCell>{r.category}</TableCell>
              <TableCell>{formatDateTime(r.lastEdited)}</TableCell>
              <TableCell align="right" onClick={(e) => e.stopPropagation()}>
                <IconButton
                  size="small"
                  onClick={(e) => openMenu(e, r)}
                  sx={{ color: "#ccc" }}
                >
                  <MoreVertIcon fontSize="small" />
                </IconButton>
              </TableCell>
            </TableRow>
          ))}
          {sorted.length === 0 && (
            <TableRow>
              <TableCell colSpan={5} sx={{ color: "#888" }}>
                No workflows found.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
      <Menu
        anchorEl={menuAnchor}
        open={Boolean(menuAnchor)}
        onClose={closeMenu}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
      >
        <MenuItem onClick={startMove}>Move Flow</MenuItem>
        <MenuItem onClick={startRename}>Rename</MenuItem>
      </Menu>
      <RenameWorkflowDialog
        open={renameOpen}
        value={renameValue}
        onChange={setRenameValue}
        onCancel={() => setRenameOpen(false)}
        onSave={handleRenameSave}
      />
      <CreateFolderDialog
        open={createOpen}
        value={createValue}
        onChange={setCreateValue}
        onCancel={() => setCreateOpen(false)}
        onCreate={handleCreateSave}
      />
      <MoveWorkflowDialog
        open={moveOpen}
        folders={folders}
        value={selectedFolder}
        onSelect={setSelectedFolder}
        onCancel={() => setMoveOpen(false)}
        onMove={handleMove}
      />
    </Paper>
  );
}
