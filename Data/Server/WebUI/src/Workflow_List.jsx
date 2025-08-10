import React, { useState, useEffect, useCallback } from "react";
import { Paper, Box, Typography, Menu, MenuItem } from "@mui/material";
import { Folder as FolderIcon, Description as DescriptionIcon } from "@mui/icons-material";
import {
  SimpleTreeView,
  TreeItem,
  useTreeViewApiRef
} from "@mui/x-tree-view";
import {
  RenameWorkflowDialog,
  RenameFolderDialog,
  NewWorkflowDialog,
  ConfirmDeleteDialog
} from "./Dialogs";

function buildTree(workflows, folders) {
  const map = {};
  const rootNode = {
    id: "root",
    label: "Workflows",
    path: "",
    isFolder: true,
    children: []
  };
  map[rootNode.id] = rootNode;

  (folders || []).forEach((f) => {
    const parts = (f || "").split("/");
    let children = rootNode.children;
    let parentPath = "";
    parts.forEach((part) => {
      const path = parentPath ? `${parentPath}/${part}` : part;
      let node = children.find((n) => n.id === path);
      if (!node) {
        node = { id: path, label: part, path, isFolder: true, children: [] };
        children.push(node);
        map[path] = node;
      }
      children = node.children;
      parentPath = path;
    });
  });

  (workflows || []).forEach((w) => {
    const parts = (w.rel_path || "").split("/");
    let children = rootNode.children;
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

  return { root: [rootNode], map };
}

export default function WorkflowList({ onOpenWorkflow }) {
  const [tree, setTree] = useState([]);
  const [nodeMap, setNodeMap] = useState({});
  const [contextMenu, setContextMenu] = useState(null);
  const [selectedNode, setSelectedNode] = useState(null);
  const [renameValue, setRenameValue] = useState("");
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameFolderOpen, setRenameFolderOpen] = useState(false);
  const [folderDialogMode, setFolderDialogMode] = useState("rename");
  const [newWorkflowOpen, setNewWorkflowOpen] = useState(false);
  const [newWorkflowName, setNewWorkflowName] = useState("");
  const [deleteOpen, setDeleteOpen] = useState(false);
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
      const { root, map } = buildTree(data.workflows || [], data.folders || []);
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

  const handleContextMenu = (e, node) => {
    e.preventDefault();
    setSelectedNode(node);
    setContextMenu(
      contextMenu === null
        ? {
            mouseX: e.clientX - 2,
            mouseY: e.clientY - 4
          }
        : null
    );
  };

  const handleRename = () => {
    setContextMenu(null);
    if (!selectedNode) return;
    setRenameValue(selectedNode.label);
    if (selectedNode.isFolder) {
      setFolderDialogMode("rename");
      setRenameFolderOpen(true);
    } else setRenameOpen(true);
  };

  const handleEdit = () => {
    setContextMenu(null);
    if (selectedNode && !selectedNode.isFolder && onOpenWorkflow) {
      onOpenWorkflow(selectedNode.workflow);
    }
  };

  const handleDelete = () => {
    setContextMenu(null);
    if (!selectedNode) return;
    setDeleteOpen(true);
  };

  const handleNewFolder = () => {
    if (!selectedNode) return;
    setContextMenu(null);
    setFolderDialogMode("create");
    setRenameValue("");
    setRenameFolderOpen(true);
  };

  const handleNewWorkflow = () => {
    if (!selectedNode) return;
    setContextMenu(null);
    setNewWorkflowName("");
    setNewWorkflowOpen(true);
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
      if (folderDialogMode === "rename" && selectedNode) {
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

  const confirmDelete = async () => {
    if (!selectedNode) return;
    try {
      if (selectedNode.isFolder) {
        await fetch("/api/storage/delete_folder", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path: selectedNode.path })
        });
      } else {
        await fetch("/api/storage/delete_workflow", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path: selectedNode.path })
        });
      }
      loadTree();
    } catch (err) {
      console.error("Failed to delete:", err);
    }
    setDeleteOpen(false);
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
            onContextMenu={(e) => handleContextMenu(e, n)}
          >
            {n.isFolder ? (
              <FolderIcon sx={{ mr: 1, color: "#0475c2" }} />
            ) : (
              <DescriptionIcon sx={{ mr: 1, color: "#0475c2" }} />
            )}
            <Typography sx={{ flexGrow: 1, color: "#e6edf3" }}>{n.label}</Typography>
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
          <Typography variant="h6" sx={{ color: "#0475c2", mb: 0 }}>
            Workflows
          </Typography>
          <Typography variant="body2" sx={{ color: "#aaa" }}>
            Create, edit, and rearrange workflows within an organized folder structure.
          </Typography>
        </Box>
        <Box />
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
          defaultExpanded={["root"]}
        >
          {renderItems(tree)}
        </SimpleTreeView>
      </Box>
      <Menu
        open={contextMenu !== null}
        onClose={() => setContextMenu(null)}
        anchorReference="anchorPosition"
        anchorPosition=
          {contextMenu ? { top: contextMenu.mouseY, left: contextMenu.mouseX } : undefined}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
      >
        {selectedNode?.isFolder && (
          <>
            <MenuItem onClick={handleNewWorkflow}>New Workflow</MenuItem>
            <MenuItem onClick={handleNewFolder}>New Subfolder</MenuItem>
            {selectedNode.id !== "root" && (
              <MenuItem onClick={handleRename}>Rename</MenuItem>
            )}
            {selectedNode.id !== "root" && (
              <MenuItem onClick={handleDelete}>Delete</MenuItem>
            )}
          </>
        )}
        {!selectedNode?.isFolder && (
          <>
            <MenuItem onClick={handleEdit}>Edit</MenuItem>
            <MenuItem onClick={handleRename}>Rename</MenuItem>
            <MenuItem onClick={handleDelete}>Delete</MenuItem>
          </>
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
        title={folderDialogMode === "rename" ? "Rename Folder" : "New Folder"}
        confirmText={folderDialogMode === "rename" ? "Save" : "Create"}
      />
      <NewWorkflowDialog
        open={newWorkflowOpen}
        value={newWorkflowName}
        onChange={setNewWorkflowName}
        onCancel={() => setNewWorkflowOpen(false)}
        onCreate={() => {
          setNewWorkflowOpen(false);
          onOpenWorkflow && onOpenWorkflow(null, selectedNode.path, newWorkflowName);
        }}
      />
      <ConfirmDeleteDialog
        open={deleteOpen}
        message="If you delete this, there is no undo button, are you sure you want to proceed?"
        onCancel={() => setDeleteOpen(false)}
        onConfirm={confirmDelete}
      />
    </Paper>
  );
}

