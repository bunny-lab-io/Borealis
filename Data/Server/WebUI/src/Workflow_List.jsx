import React, { useState, useEffect, useCallback } from "react";
import {
  Paper,
  Box,
  Typography,
  Button,
  IconButton,
  Menu,
  MenuItem
} from "@mui/material";
import {
  PlayCircle as PlayCircleIcon,
  MoreVert as MoreVertIcon,
  Folder as FolderIcon,
  Description as DescriptionIcon,
  CreateNewFolder as CreateNewFolderIcon
} from "@mui/icons-material";
import {
  SimpleTreeView,
  TreeItem,
  useTreeViewApiRef
} from "@mui/x-tree-view";
import { RenameWorkflowDialog, RenameFolderDialog } from "./Dialogs";

function buildTree(workflows) {
  const map = {};
  const root = [];
  (workflows || []).forEach((w) => {
    const parts = (w.rel_path || "").split("/");
    let children = root;
    let parentPath = "";
    parts.forEach((part, idx) => {
      const path = parentPath ? `${parentPath}/${part}` : part;
      const isFile = idx === parts.length - 1;
      let node = children.find((n) => n.id === path);
      if (!node) {
        node = {
          id: path,
          label: isFile
            ? w.tab_name && w.tab_name.trim().length > 0
              ? w.tab_name.trim()
              : w.file_name
            : part,
          path,
          isFolder: !isFile,
          fileName: w.file_name,
          workflow: isFile ? w : null,
          children: []
        };
        children.push(node);
        map[path] = node;
      }
      if (!isFile) {
        children = node.children;
        parentPath = path;
      }
    });
  });
  return { root, map };
}

export default function WorkflowList({ onOpenWorkflow }) {
  const [tree, setTree] = useState([]);
  const [nodeMap, setNodeMap] = useState({});
  const [menuAnchor, setMenuAnchor] = useState(null);
  const [selectedNode, setSelectedNode] = useState(null);
  const [renameValue, setRenameValue] = useState("");
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameFolderOpen, setRenameFolderOpen] = useState(false);
  const apiRef = useTreeViewApiRef();
  const [dragNode, setDragNode] = useState(null);

  const handleDrop = async (target) => {
    if (!dragNode || !target.isFolder) return;
    // Prevent dropping into itself or its descendants
    if (dragNode.path === target.path || target.path.startsWith(`${dragNode.path}/`)) {
      setDragNode(null);
      return;
    }
    const newPath = target.path ? `${target.path}/${dragNode.fileName}` : dragNode.fileName;
    try {
      await fetch("/api/storage/move_workflow", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: dragNode.path, new_path: newPath })
      });
      loadTree();
    } catch (err) {
      console.error("Failed to move workflow:", err);
    }
    setDragNode(null);
  };

  const loadTree = useCallback(async () => {
    try {
      const resp = await fetch("/api/storage/load_workflows");
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const { root, map } = buildTree(data.workflows || []);
      setTree(root);
      setNodeMap(map);
    } catch (err) {
      console.error("Failed to load workflows:", err);
      setTree([]);
      setNodeMap({});
    }
  }, []);

  useEffect(() => {
    loadTree();
  }, [loadTree]);

  const openMenu = (e, node) => {
    e.stopPropagation();
    setMenuAnchor(e.currentTarget);
    setSelectedNode(node);
  };

  const closeMenu = () => setMenuAnchor(null);

  const handleRename = () => {
    closeMenu();
    if (!selectedNode) return;
    setRenameValue(selectedNode.label);
    if (selectedNode.isFolder) setRenameFolderOpen(true);
    else setRenameOpen(true);
  };

  const handleDeleteWorkflow = async () => {
    closeMenu();
    if (!selectedNode) return;
    try {
      await fetch("/api/storage/delete_workflow", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: selectedNode.path })
      });
      loadTree();
    } catch (err) {
      console.error("Failed to delete workflow:", err);
    }
  };

  const handleCreateFolder = () => {
    closeMenu();
    setRenameValue("");
    setRenameFolderOpen(true);
  };

  const saveRenameWorkflow = async () => {
    if (!selectedNode) return;
    try {
      await fetch("/api/storage/rename_workflow", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: selectedNode.path, new_name: renameValue })
      });
      loadTree();
    } catch (err) {
      console.error("Failed to rename workflow:", err);
    }
    setRenameOpen(false);
  };

  const saveRenameFolder = async () => {
    try {
      if (selectedNode && selectedNode.isFolder) {
        await fetch("/api/storage/rename_folder", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path: selectedNode.path, new_name: renameValue })
        });
      } else {
        const basePath = selectedNode ? selectedNode.path : "";
        const newPath = basePath ? `${basePath}/${renameValue}` : renameValue;
        await fetch("/api/storage/create_folder", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path: newPath })
        });
      }
      loadTree();
    } catch (err) {
      console.error("Folder operation failed:", err);
    }
    setRenameFolderOpen(false);
  };

  const handleNodeSelect = (event, itemId) => {
    const node = nodeMap[itemId];
    if (node && !node.isFolder && onOpenWorkflow) {
      onOpenWorkflow(node.workflow);
    }
  };

  const renderItems = (nodes) =>
    nodes.map((n) => (
      <TreeItem
        key={n.id}
        itemId={n.id}
        label={
          <Box
            sx={{ display: "flex", alignItems: "center" }}
            draggable={!n.isFolder}
            onDragStart={() => !n.isFolder && setDragNode(n)}
            onDragOver={(e) => {
              if (dragNode && n.isFolder) e.preventDefault();
            }}
            onDrop={(e) => {
              e.preventDefault();
              handleDrop(n);
            }}
          >
            {n.isFolder ? (
              <FolderIcon sx={{ mr: 1, color: "#ffd54f" }} />
            ) : (
              <DescriptionIcon sx={{ mr: 1, color: "#90caf9" }} />
            )}
            <Typography sx={{ flexGrow: 1, color: "#e6edf3" }}>{n.label}</Typography>
            <IconButton
              size="small"
              onClick={(e) => openMenu(e, n)}
              sx={{ color: "#ccc" }}
            >
              <MoreVertIcon fontSize="small" />
            </IconButton>
          </Box>
        }
      >
        {n.children && n.children.length > 0 ? renderItems(n.children) : null}
      </TreeItem>
    ));

  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box
        sx={{
          p: 2,
          pb: 1,
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center"
        }}
      >
        <Box>
          <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>
            Workflows
          </Typography>
          <Typography variant="body2" sx={{ color: "#aaa" }}>
            Manage workflow folders and files.
          </Typography>
        </Box>
        <Box>
          <Button
            startIcon={<CreateNewFolderIcon />}
            sx={{
              mr: 1,
              color: "#58a6ff",
              borderColor: "#58a6ff",
              textTransform: "none",
              border: "1px solid #58a6ff",
              backgroundColor: "#1e1e1e",
              "&:hover": { backgroundColor: "#1b1b1b" }
            }}
            onClick={() => {
              setSelectedNode(null);
              setRenameValue("");
              setRenameFolderOpen(true);
            }}
          >
            New Folder
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
            onClick={() => onOpenWorkflow && onOpenWorkflow()}
          >
            New Workflow
          </Button>
        </Box>
      </Box>
      <Box
        sx={{ p: 2 }}
        onDragOver={(e) => {
          if (dragNode) e.preventDefault();
        }}
        onDrop={(e) => {
          e.preventDefault();
          handleDrop({ path: "", isFolder: true });
        }}
      >
        <SimpleTreeView
          sx={{ color: "#e6edf3" }}
          onNodeSelect={handleNodeSelect}
          apiRef={apiRef}
        >
          {renderItems(tree)}
        </SimpleTreeView>
      </Box>
      <Menu
        anchorEl={menuAnchor}
        open={Boolean(menuAnchor)}
        onClose={closeMenu}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
      >
        {selectedNode?.isFolder && (
          <MenuItem onClick={handleCreateFolder}>New Folder</MenuItem>
        )}
        <MenuItem onClick={handleRename}>Rename</MenuItem>
        {!selectedNode?.isFolder && (
          <MenuItem onClick={handleDeleteWorkflow}>Delete</MenuItem>
        )}
      </Menu>
      <RenameWorkflowDialog
        open={renameOpen}
        value={renameValue}
        onChange={setRenameValue}
        onCancel={() => setRenameOpen(false)}
        onSave={saveRenameWorkflow}
      />
      <RenameFolderDialog
        open={renameFolderOpen}
        value={renameValue}
        onChange={setRenameValue}
        onCancel={() => setRenameFolderOpen(false)}
        onSave={saveRenameFolder}
      />
    </Paper>
  );
}

