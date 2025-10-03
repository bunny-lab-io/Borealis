import React, { useEffect, useState, useCallback } from "react";
import {
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  Button,
  Box,
  Typography,
  Paper,
  FormControlLabel,
  Checkbox
} from "@mui/material";
import { Folder as FolderIcon, Description as DescriptionIcon } from "@mui/icons-material";
import { SimpleTreeView, TreeItem } from "@mui/x-tree-view";

function buildTree(items, folders, rootLabel = "Scripts") {
  const map = {};
  const rootNode = {
    id: "root",
    label: rootLabel,
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

  (items || []).forEach((s) => {
    const parts = (s.rel_path || "").split("/");
    let children = rootNode.children;
    let parentPath = "";
    parts.forEach((part, idx) => {
      const path = parentPath ? `${parentPath}/${part}` : part;
      const isFile = idx === parts.length - 1;
      let node = children.find((n) => n.id === path);
      if (!node) {
        node = {
          id: path,
          label: isFile ? (s.name || s.file_name || part) : part,
          path,
          isFolder: !isFile,
          fileName: s.file_name,
          script: isFile ? s : null,
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

export default function QuickJob({ open, onClose, hostnames = [] }) {
  const [tree, setTree] = useState([]);
  const [nodeMap, setNodeMap] = useState({});
  const [selectedPath, setSelectedPath] = useState("");
  const [running, setRunning] = useState(false);
  const [error, setError] = useState("");
  const [runAsCurrentUser, setRunAsCurrentUser] = useState(false);
  const [mode, setMode] = useState("scripts"); // 'scripts' | 'ansible'

  const loadTree = useCallback(async () => {
    try {
      const island = mode === 'ansible' ? 'ansible' : 'scripts';
      const resp = await fetch(`/api/assembly/list?island=${island}`);
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const { root, map } = buildTree(data.items || [], data.folders || [], mode === 'ansible' ? 'Ansible Playbooks' : 'Scripts');
      setTree(root);
      setNodeMap(map);
    } catch (err) {
      console.error("Failed to load scripts:", err);
      setTree([]);
      setNodeMap({});
    }
  }, [mode]);

  useEffect(() => {
    if (open) {
      setSelectedPath("");
      setError("");
      loadTree();
    }
  }, [open, loadTree]);

  const renderNodes = (nodes = []) =>
    nodes.map((n) => (
      <TreeItem
        key={n.id}
        itemId={n.id}
        label={
          <Box sx={{ display: "flex", alignItems: "center" }}>
            {n.isFolder ? (
              <FolderIcon fontSize="small" sx={{ mr: 1, color: "#ccc" }} />
            ) : (
              <DescriptionIcon fontSize="small" sx={{ mr: 1, color: "#ccc" }} />
            )}
            <Typography variant="body2" sx={{ color: "#e6edf3" }}>{n.label}</Typography>
          </Box>
        }
      >
        {n.children && n.children.length ? renderNodes(n.children) : null}
      </TreeItem>
    ));

  const onItemSelect = (_e, itemId) => {
    const node = nodeMap[itemId];
    if (node && !node.isFolder) {
      setSelectedPath(node.path);
      setError("");
    }
  };

  const onRun = async () => {
    if (!selectedPath) {
      setError(mode === 'ansible' ? "Please choose a playbook to run." : "Please choose a script to run.");
      return;
    }
    setRunning(true);
    setError("");
    try {
      let resp;
      if (mode === 'ansible') {
        const playbook_path = selectedPath; // relative to ansible island
        resp = await fetch("/api/ansible/quick_run", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ playbook_path, hostnames })
        });
      } else {
        // quick_run expects a path relative to Assemblies root with 'Scripts/' prefix
        const script_path = selectedPath.startsWith('Scripts/') ? selectedPath : `Scripts/${selectedPath}`;
        resp = await fetch("/api/scripts/quick_run", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ script_path, hostnames, run_mode: runAsCurrentUser ? "current_user" : "system" })
        });
      }
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error || `HTTP ${resp.status}`);
      onClose && onClose();
    } catch (err) {
      setError(String(err.message || err));
    } finally {
      setRunning(false);
    }
  };

  return (
    <Dialog open={open} onClose={running ? undefined : onClose} fullWidth maxWidth="md"
      PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}
    >
      <DialogTitle>Quick Job</DialogTitle>
      <DialogContent>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1 }}>
          <Button size="small" variant={mode === 'scripts' ? 'outlined' : 'text'} onClick={() => setMode('scripts')} sx={{ textTransform: 'none', color: '#58a6ff', borderColor: '#58a6ff' }}>Scripts</Button>
          <Button size="small" variant={mode === 'ansible' ? 'outlined' : 'text'} onClick={() => setMode('ansible')} sx={{ textTransform: 'none', color: '#58a6ff', borderColor: '#58a6ff' }}>Ansible</Button>
        </Box>
        <Typography variant="body2" sx={{ color: "#aaa", mb: 1 }}>
          Select a {mode === 'ansible' ? 'playbook' : 'script'} to run on {hostnames.length} device{hostnames.length !== 1 ? "s" : ""}.
        </Typography>
        <Box sx={{ display: "flex", gap: 2 }}>
          <Paper sx={{ flex: 1, p: 1, bgcolor: "#1e1e1e", maxHeight: 400, overflow: "auto" }}>
            <SimpleTreeView sx={{ color: "#e6edf3" }} onItemSelectionToggle={onItemSelect}>
              {tree.length ? renderNodes(tree) : (
                <Typography variant="body2" sx={{ color: "#888", p: 1 }}>
                  {mode === 'ansible' ? 'No playbooks found.' : 'No scripts found.'}
                </Typography>
              )}
            </SimpleTreeView>
          </Paper>
          <Box sx={{ width: 320 }}>
            <Typography variant="subtitle2" sx={{ color: "#ccc", mb: 1 }}>Selection</Typography>
            <Typography variant="body2" sx={{ color: selectedPath ? "#e6edf3" : "#888" }}>
              {selectedPath || (mode === 'ansible' ? 'No playbook selected' : 'No script selected')}
            </Typography>
            <Box sx={{ mt: 2 }}>
              {mode !== 'ansible' && (
                <>
                  <FormControlLabel
                    control={<Checkbox size="small" checked={runAsCurrentUser} onChange={(e) => setRunAsCurrentUser(e.target.checked)} />}
                    label={<Typography variant="body2">Run as currently logged-in user</Typography>}
                  />
                  <Typography variant="caption" sx={{ color: "#888" }}>
                    Unchecked = Run-As BUILTIN\SYSTEM
                  </Typography>
                </>
              )}
            </Box>
            {error && (
              <Typography variant="body2" sx={{ color: "#ff4f4f", mt: 1 }}>{error}</Typography>
            )}
          </Box>
        </Box>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} disabled={running} sx={{ color: "#58a6ff" }}>Cancel</Button>
        <Button onClick={onRun} disabled={running || !selectedPath}
          sx={{ color: running || !selectedPath ? "#666" : "#58a6ff" }}
        >
          Run
        </Button>
      </DialogActions>
    </Dialog>
  );
}

