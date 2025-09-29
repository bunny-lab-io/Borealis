import React, { useState, useEffect, useCallback } from "react";
import { Paper, Box, Typography, Menu, MenuItem, Button } from "@mui/material";
import { Folder as FolderIcon, Description as DescriptionIcon, Polyline as WorkflowsIcon, Code as ScriptIcon, MenuBook as BookIcon } from "@mui/icons-material";
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
} from "../Dialogs";

// Generic Island wrapper with large icon, stacked title/description, and actions on the right
const Island = ({ title, description, icon, actions, children, sx }) => (
  <Paper
    elevation={0}
    sx={{ p: 1.5, borderRadius: 2, bgcolor: '#1c1c1c', border: '1px solid #2a2a2a', mb: 1.5, ...(sx || {}) }}
  >
    <Box sx={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', mb: 1 }}>
      <Box sx={{ display: 'flex', alignItems: 'center' }}>
        {icon ? (
          <Box
            sx={{
              color: '#58a6ff',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              minWidth: 48,
              mr: 1.0,
            }}
          >
            {icon}
          </Box>
        ) : null}
        <Box>
          <Typography
            variant="caption"
            sx={{ color: '#58a6ff', fontWeight: 400, fontSize: '14px', letterSpacing: 0.2 }}
          >
            {title}
          </Typography>
          {description ? (
            <Typography variant="body2" sx={{ color: '#aaa' }}>
              {description}
            </Typography>
          ) : null}
        </Box>
      </Box>
      {actions ? (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          {actions}
        </Box>
      ) : null}
    </Box>
    {children}
  </Paper>
);

// ---------------- Workflows Island -----------------
function buildWorkflowTree(workflows, folders) {
  const map = {};
  const rootNode = { id: "root", label: "Workflows", path: "", isFolder: true, children: [] };
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
          label: isFile ? ((w.tab_name && w.tab_name.trim()) || w.file_name) : part,
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

function WorkflowsIsland({ onOpenWorkflow }) {
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
    if (dragNode.path === target.path || target.path.startsWith(`${dragNode.path}/`)) {
      setDragNode(null);
      return;
    }
    const newPath = target.path ? `${target.path}/${dragNode.fileName}` : dragNode.fileName;
    try {
      await fetch("/api/assembly/move", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ island: 'workflows', kind: 'file', path: dragNode.path, new_path: newPath })
      });
      loadTree();
    } catch (err) {
      console.error("Failed to move workflow:", err);
    }
    setDragNode(null);
  };

  const loadTree = useCallback(async () => {
    try {
      const resp = await fetch(`/api/assembly/list?island=workflows`);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const { root, map } = buildWorkflowTree(data.items || [], data.folders || []);
      setTree(root);
      setNodeMap(map);
    } catch (err) {
      console.error("Failed to load workflows:", err);
      setTree([]);
      setNodeMap({});
    }
  }, []);

  useEffect(() => { loadTree(); }, [loadTree]);

  const handleContextMenu = (e, node) => {
    e.preventDefault();
    setSelectedNode(node);
    setContextMenu(
      contextMenu === null ? { mouseX: e.clientX - 2, mouseY: e.clientY - 4 } : null
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
      await fetch("/api/assembly/rename", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ island: 'workflows', kind: 'file', path: selectedNode.path, new_name: renameValue })
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
        await fetch("/api/assembly/rename", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ island: 'workflows', kind: 'folder', path: selectedNode.path, new_name: renameValue })
        });
      } else {
        const basePath = selectedNode ? selectedNode.path : "";
        const newPath = basePath ? `${basePath}/${renameValue}` : renameValue;
        await fetch("/api/assembly/create", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ island: 'workflows', kind: 'folder', path: newPath })
        });
      }
      loadTree();
    } catch (err) {
      console.error("Folder operation failed:", err);
    }
    setRenameFolderOpen(false);
  };

  const handleNodeSelect = (_event, itemId) => {
    const node = nodeMap[itemId];
    if (node && !node.isFolder && onOpenWorkflow) {
      onOpenWorkflow(node.workflow);
    }
  };

  const confirmDelete = async () => {
    if (!selectedNode) return;
    try {
      if (selectedNode.isFolder) {
        await fetch("/api/assembly/delete", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ island: 'workflows', kind: 'folder', path: selectedNode.path })
        });
      } else {
        await fetch("/api/assembly/delete", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ island: 'workflows', kind: 'file', path: selectedNode.path })
        });
      }
      loadTree();
    } catch (err) {
      console.error("Failed to delete:", err);
    }
    setDeleteOpen(false);
  };

  const renderItems = (nodes) =>
    (nodes || []).map((n) => (
      <TreeItem
        key={n.id}
        itemId={n.id}
        label={
          <Box
            sx={{ display: "flex", alignItems: "center" }}
            draggable={!n.isFolder}
            onDragStart={() => !n.isFolder && setDragNode(n)}
            onDragOver={(e) => { if (dragNode && n.isFolder) e.preventDefault(); }}
            onDrop={(e) => { e.preventDefault(); handleDrop(n); }}
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

  const rootChildIds = tree[0]?.children?.map((c) => c.id) || [];

  return (
    <Island
      title="Workflows"
      description="Node-Based Automation Pipelines"
      icon={<WorkflowsIcon sx={{ fontSize: 40 }} />}
      actions={
        <Button
          size="small"
          variant="outlined"
          onClick={() => { setSelectedNode({ id: 'root', path: '', isFolder: true }); setNewWorkflowName(''); setNewWorkflowOpen(true); }}
          sx={{
            color: '#58a6ff',
            borderColor: '#2f81f7',
            textTransform: 'none',
            '&:hover': { borderColor: '#58a6ff' }
          }}
        >
          New Workflow
        </Button>
      }
    >
      <Box
        sx={{ p: 1 }}
        onDragOver={(e) => { if (dragNode) e.preventDefault(); }}
        onDrop={(e) => { e.preventDefault(); handleDrop({ path: "", isFolder: true }); }}
      >
        <SimpleTreeView
          key={rootChildIds.join(",")}
          sx={{ color: "#e6edf3" }}
          onNodeSelect={handleNodeSelect}
          apiRef={apiRef}
          defaultExpandedItems={["root", ...rootChildIds]}
        >
          {renderItems(tree)}
        </SimpleTreeView>
      </Box>
      <Menu
        open={contextMenu !== null}
        onClose={() => setContextMenu(null)}
        anchorReference="anchorPosition"
        anchorPosition={contextMenu ? { top: contextMenu.mouseY, left: contextMenu.mouseX } : undefined}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
      >
        {selectedNode?.isFolder && (
          <>
            <MenuItem onClick={handleNewWorkflow}>New Workflow</MenuItem>
            <MenuItem onClick={handleNewFolder}>New Subfolder</MenuItem>
            {selectedNode.id !== "root" && (<MenuItem onClick={handleRename}>Rename</MenuItem>)}
            {selectedNode.id !== "root" && (<MenuItem onClick={handleDelete}>Delete</MenuItem>)}
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
      <RenameWorkflowDialog open={renameOpen} value={renameValue} onChange={setRenameValue} onCancel={() => setRenameOpen(false)} onSave={saveRenameWorkflow} />
      <RenameFolderDialog open={renameFolderOpen} value={renameValue} onChange={setRenameValue} onCancel={() => setRenameFolderOpen(false)} onSave={saveRenameFolder} title={folderDialogMode === "rename" ? "Rename Folder" : "New Folder"} confirmText={folderDialogMode === "rename" ? "Save" : "Create"} />
      <NewWorkflowDialog open={newWorkflowOpen} value={newWorkflowName} onChange={setNewWorkflowName} onCancel={() => setNewWorkflowOpen(false)} onCreate={() => { setNewWorkflowOpen(false); onOpenWorkflow && onOpenWorkflow(null, selectedNode?.path || "", newWorkflowName); }} />
      <ConfirmDeleteDialog open={deleteOpen} message="If you delete this, there is no undo button, are you sure you want to proceed?" onCancel={() => setDeleteOpen(false)} onConfirm={confirmDelete} />
    </Island>
  );
}

// ---------------- Generic Scripts-like Islands (used for Scripts and Ansible) -----------------
function buildFileTree(rootLabel, items, folders) {
  // Some backends (e.g. /api/scripts) return paths relative to
  // the Assemblies root, which prefixes items with a top-level
  // folder like "Scripts". Others (e.g. /api/ansible) already
  // return paths relative to their specific root. Normalize by
  // stripping a matching top-level segment so the UI shows
  // "Scripts/<...>" rather than "Scripts/Scripts/<...>".
  const normalize = (p) => {
    const candidates = [
      String(rootLabel || "").trim(),
      String(rootLabel || "").replace(/\s+/g, "_")
    ].filter(Boolean);
    const parts = String(p || "").replace(/\\/g, "/").split("/").filter(Boolean);
    if (parts.length && candidates.includes(parts[0])) parts.shift();
    return parts;
  };

  const map = {};
  const rootNode = { id: "root", label: rootLabel, path: "", isFolder: true, children: [] };
  map[rootNode.id] = rootNode;

  (folders || []).forEach((f) => {
    const parts = normalize(f);
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

  (items || []).forEach((s) => {
    const parts = normalize(s?.rel_path);
    let children = rootNode.children;
    let parentPath = "";
    parts.forEach((part, idx) => {
      const path = parentPath ? `${parentPath}/${part}` : part;
      const isFile = idx === parts.length - 1;
      let node = children.find((n) => n.id === path);
      if (!node) {
        node = {
          id: path,
          label: isFile ? (s.file_name || part) : part,
          path,
          isFolder: !isFile,
          fileName: s.file_name,
          meta: isFile ? s : null,
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

function ScriptsLikeIsland({
  title,
  description,
  rootLabel,
  baseApi, // e.g. '/api/scripts' or '/api/ansible'
  newItemLabel = "New Script",
  onEdit // (rel_path) => void
}) {
  const [tree, setTree] = useState([]);
  const [nodeMap, setNodeMap] = useState({});
  const [contextMenu, setContextMenu] = useState(null);
  const [selectedNode, setSelectedNode] = useState(null);
  const [renameValue, setRenameValue] = useState("");
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameFolderOpen, setRenameFolderOpen] = useState(false);
  const [folderDialogMode, setFolderDialogMode] = useState("rename");
  const [newItemOpen, setNewItemOpen] = useState(false);
  const [newItemName, setNewItemName] = useState("");
  const [deleteOpen, setDeleteOpen] = useState(false);
  const apiRef = useTreeViewApiRef();
  const [dragNode, setDragNode] = useState(null);

  const island = React.useMemo(() => {
    const b = String(baseApi || '').toLowerCase();
    return b.endsWith('/api/ansible') ? 'ansible' : 'scripts';
  }, [baseApi]);

  const loadTree = useCallback(async () => {
    try {
      const resp = await fetch(`/api/assembly/list?island=${encodeURIComponent(island)}`);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const { root, map } = buildFileTree(rootLabel, data.items || [], data.folders || []);
      setTree(root);
      setNodeMap(map);
    } catch (err) {
      console.error(`Failed to load ${title}:`, err);
      setTree([]);
      setNodeMap({});
    }
  }, [island, title, rootLabel]);

  useEffect(() => { loadTree(); }, [loadTree]);

  const handleContextMenu = (e, node) => {
    e.preventDefault();
    setSelectedNode(node);
    setContextMenu(
      contextMenu === null ? { mouseX: e.clientX - 2, mouseY: e.clientY - 4 } : null
    );
  };

  const handleDrop = async (target) => {
    if (!dragNode || !target.isFolder) return;
    if (dragNode.path === target.path || target.path.startsWith(`${dragNode.path}/`)) {
      setDragNode(null);
      return;
    }
    const newPath = target.path ? `${target.path}/${dragNode.fileName}` : dragNode.fileName;
    try {
      await fetch(`/api/assembly/move`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ island, kind: 'file', path: dragNode.path, new_path: newPath })
      });
      loadTree();
    } catch (err) {
      console.error("Failed to move:", err);
    }
    setDragNode(null);
  };

  const handleNodeSelect = async (_e, itemId) => {
    const node = nodeMap[itemId];
    if (node && !node.isFolder) {
      setContextMenu(null);
      onEdit && onEdit(node.path);
    }
  };

  const saveRenameFile = async () => {
    try {
      const payload = { island, kind: 'file', path: selectedNode.path, new_name: renameValue };
      // preserve extension for scripts when no extension provided
      if (selectedNode?.meta?.type) payload.type = selectedNode.meta.type;
      const res = await fetch(`/api/assembly/rename`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data?.error || `HTTP ${res.status}`);
      setRenameOpen(false);
      loadTree();
    } catch (err) {
      console.error("Failed to rename file:", err);
      setRenameOpen(false);
    }
  };

  const saveRenameFolder = async () => {
    try {
      if (folderDialogMode === "rename" && selectedNode) {
        await fetch(`/api/assembly/rename`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ island, kind: 'folder', path: selectedNode.path, new_name: renameValue })
        });
      } else {
        const basePath = selectedNode ? selectedNode.path : "";
        const newPath = basePath ? `${basePath}/${renameValue}` : renameValue;
        await fetch(`/api/assembly/create`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ island, kind: 'folder', path: newPath })
        });
      }
      setRenameFolderOpen(false);
      loadTree();
    } catch (err) {
      console.error("Folder operation failed:", err);
      setRenameFolderOpen(false);
    }
  };

  const confirmDelete = async () => {
    if (!selectedNode) return;
    try {
      if (selectedNode.isFolder) {
        await fetch(`/api/assembly/delete`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ island, kind: 'folder', path: selectedNode.path })
        });
      } else {
        await fetch(`/api/assembly/delete`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ island, kind: 'file', path: selectedNode.path })
        });
      }
      setDeleteOpen(false);
      loadTree();
    } catch (err) {
      console.error("Failed to delete:", err);
      setDeleteOpen(false);
    }
  };

  const createNewItem = async () => {
    try {
      const folder = selectedNode?.isFolder ? selectedNode.path : (selectedNode?.path?.split("/").slice(0, -1).join("/") || "");
      let name = newItemName || "new";
      const hasExt = /\.[^./\\]+$/i.test(name);
      if (!hasExt) {
        if (String(baseApi || '').endsWith('/api/ansible')) name += '.yml';
        else name += '.ps1';
      }
      const newPath = folder ? `${folder}/${name}` : name;
      // create empty file via unified API
      const res = await fetch(`/api/assembly/create`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ island, kind: 'file', path: newPath, content: "", type: island === 'ansible' ? 'ansible' : 'powershell' })
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        throw new Error(data?.error || `HTTP ${res.status}`);
      }
      setNewItemOpen(false);
      setNewItemName("");
      loadTree();
    } catch (err) {
      console.error("Failed to create:", err);
    }
  };

  const renderItems = (nodes) =>
    (nodes || []).map((n) => (
      <TreeItem
        key={n.id}
        itemId={n.id}
        label={
          <Box
            sx={{ display: "flex", alignItems: "center" }}
            draggable={!n.isFolder}
            onDragStart={() => !n.isFolder && setDragNode(n)}
            onDragOver={(e) => { if (dragNode && n.isFolder) e.preventDefault(); }}
            onDrop={(e) => { e.preventDefault(); handleDrop(n); }}
            onContextMenu={(e) => handleContextMenu(e, n)}
            onDoubleClick={() => { if (!n.isFolder) onEdit && onEdit(n.path); }}
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

  const rootChildIds = tree[0]?.children?.map((c) => c.id) || [];

  return (
    <Island
      title={title}
      description={description}
      icon={title === 'Scripts' ? <ScriptIcon sx={{ fontSize: 40 }} /> : <BookIcon sx={{ fontSize: 40 }} />}
      actions={
        <Button
          size="small"
          variant="outlined"
          onClick={() => { setNewItemName(''); setNewItemOpen(true); }}
          sx={{ color: '#58a6ff', borderColor: '#2f81f7', textTransform: 'none', '&:hover': { borderColor: '#58a6ff' } }}
        >
          {newItemLabel}
        </Button>
      }
    >
      <Box
        sx={{ p: 1 }}
        onDragOver={(e) => { if (dragNode) e.preventDefault(); }}
        onDrop={(e) => { e.preventDefault(); handleDrop({ path: "", isFolder: true }); }}
      >
        <SimpleTreeView
          key={rootChildIds.join(",")}
          sx={{ color: "#e6edf3" }}
          onNodeSelect={handleNodeSelect}
          apiRef={apiRef}
          defaultExpandedItems={["root", ...rootChildIds]}
        >
          {renderItems(tree)}
        </SimpleTreeView>
      </Box>
      <Menu
        open={contextMenu !== null}
        onClose={() => setContextMenu(null)}
        anchorReference="anchorPosition"
        anchorPosition={contextMenu ? { top: contextMenu.mouseY, left: contextMenu.mouseX } : undefined}
        PaperProps={{ sx: { bgcolor: "#1e1e1e", color: "#fff", fontSize: "13px" } }}
      >
        {selectedNode?.isFolder && (
          <>
            <MenuItem onClick={() => { setContextMenu(null); setNewItemOpen(true); }}>{newItemLabel}</MenuItem>
            <MenuItem onClick={() => { setContextMenu(null); setFolderDialogMode("create"); setRenameValue(""); setRenameFolderOpen(true); }}>New Subfolder</MenuItem>
            {selectedNode.id !== "root" && (<MenuItem onClick={() => { setContextMenu(null); setRenameValue(selectedNode.label); setRenameOpen(true); }}>Rename</MenuItem>)}
            {selectedNode.id !== "root" && (<MenuItem onClick={() => { setContextMenu(null); setDeleteOpen(true); }}>Delete</MenuItem>)}
          </>
        )}
        {!selectedNode?.isFolder && (
          <>
            <MenuItem onClick={() => { setContextMenu(null); onEdit && onEdit(selectedNode.path); }}>Edit</MenuItem>
            <MenuItem onClick={() => { setContextMenu(null); setRenameValue(selectedNode.label); setRenameOpen(true); }}>Rename</MenuItem>
            <MenuItem onClick={() => { setContextMenu(null); setDeleteOpen(true); }}>Delete</MenuItem>
          </>
        )}
      </Menu>
      {/* Simple inline dialogs using shared components */}
      <RenameFolderDialog open={renameFolderOpen} value={renameValue} onChange={setRenameValue} onCancel={() => setRenameFolderOpen(false)} onSave={saveRenameFolder} title={folderDialogMode === "rename" ? "Rename Folder" : "New Folder"} confirmText={folderDialogMode === "rename" ? "Save" : "Create"} />
      {/* File rename */}
      <Paper component={(p) => <div {...p} />} sx={{ display: renameOpen ? 'block' : 'none' }}>
        <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.4)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 9999 }}>
          <Paper sx={{ bgcolor: '#121212', color: '#fff', p: 2, minWidth: 360 }}>
            <Typography variant="h6" sx={{ mb: 1 }}>Rename</Typography>
            <input autoFocus value={renameValue} onChange={(e) => setRenameValue(e.target.value)} style={{ width: '100%', padding: 8, background: '#2a2a2a', color: '#ccc', border: '1px solid #444', borderRadius: 4 }} />
            <Box sx={{ display: 'flex', justifyContent: 'flex-end', mt: 2 }}>
              <Button onClick={() => setRenameOpen(false)} sx={{ color: '#58a6ff' }}>Cancel</Button>
              <Button onClick={saveRenameFile} sx={{ color: '#58a6ff' }}>Save</Button>
            </Box>
          </Paper>
        </div>
      </Paper>
      <Paper component={(p) => <div {...p} />} sx={{ display: newItemOpen ? 'block' : 'none' }}>
        <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.4)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 9999 }}>
          <Paper sx={{ bgcolor: '#121212', color: '#fff', p: 2, minWidth: 360 }}>
            <Typography variant="h6" sx={{ mb: 1 }}>{newItemLabel}</Typography>
            <input autoFocus value={newItemName} onChange={(e) => setNewItemName(e.target.value)} placeholder="Name" style={{ width: '100%', padding: 8, background: '#2a2a2a', color: '#ccc', border: '1px solid #444', borderRadius: 4 }} />
            <Box sx={{ display: 'flex', justifyContent: 'flex-end', mt: 2 }}>
              <Button onClick={() => setNewItemOpen(false)} sx={{ color: '#58a6ff' }}>Cancel</Button>
              <Button onClick={createNewItem} sx={{ color: '#58a6ff' }}>Create</Button>
            </Box>
          </Paper>
        </div>
      </Paper>
      <ConfirmDeleteDialog open={deleteOpen} message="If you delete this, there is no undo button, are you sure you want to proceed?" onCancel={() => setDeleteOpen(false)} onConfirm={confirmDelete} />
    </Island>
  );
}

export default function AssemblyList({ onOpenWorkflow, onOpenScript }) {
  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: '#1e1e1e' }} elevation={2}>
      <Box sx={{ p: 2, pb: 1 }}>
        <Typography variant="h6" sx={{ color: '#58a6ff', mb: 0 }}>Assemblies</Typography>
        <Typography variant="body2" sx={{ color: '#aaa' }}>Collections of various types of components used to perform various automations upon targeted devices.</Typography>
      </Box>
      <Box sx={{ px: 2, pb: 2 }}>
        <Box sx={{ display: 'grid', gridTemplateColumns: { xs: '1fr', md: '1.2fr 1fr 1fr' }, gap: 2 }}>
          {/* Left: Workflows */}
          <WorkflowsIsland onOpenWorkflow={onOpenWorkflow} />

        {/* Middle: Scripts */}
        <ScriptsLikeIsland
          title="Scripts"
          description="Powershell, Batch, and Bash Scripts"
          rootLabel="Scripts"
          baseApi="/api/scripts"
          newItemLabel="New Script"
          onEdit={(rel) => onOpenScript && onOpenScript(rel, 'scripts')}
        />

        {/* Right: Ansible Playbooks */}
        <ScriptsLikeIsland
          title="Ansible Playbooks"
          description="Declarative Instructions for Consistent Automation"
          rootLabel="Ansible Playbooks"
          baseApi="/api/ansible"
          newItemLabel="New Playbook"
          onEdit={(rel) => onOpenScript && onOpenScript(rel, 'ansible')}
        />
        </Box>
      </Box>
    </Paper>
  );
}
