import React, { useEffect, useMemo, useState, useCallback } from "react";
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

function buildTree(scripts, folders) {
  const map = {};
  const rootNode = {
    id: "root",
    label: "Scripts",
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

  (scripts || []).forEach((s) => {
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
          label: isFile ? s.file_name : part,
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

  const loadTree = useCallback(async () => {
    try {
      const resp = await fetch("/api/scripts/list");
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const { root, map } = buildTree(data.scripts || [], data.folders || []);
      setTree(root);
      setNodeMap(map);
    } catch (err) {
      console.error("Failed to load scripts:", err);
      setTree([]);
      setNodeMap({});
    }
  }, []);

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
      setError("Please choose a script to run.");
      return;
    }
    setRunning(true);
    setError("");
    try {
      const resp = await fetch("/api/scripts/quick_run", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ script_path: selectedPath, hostnames, run_mode: runAsCurrentUser ? "current_user" : "system" })
      });
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
        <Typography variant="body2" sx={{ color: "#aaa", mb: 1 }}>
          Select a script to run on {hostnames.length} device{hostnames.length !== 1 ? "s" : ""}.
        </Typography>
        <Box sx={{ display: "flex", gap: 2 }}>
          <Paper sx={{ flex: 1, p: 1, bgcolor: "#1e1e1e", maxHeight: 400, overflow: "auto" }}>
            <SimpleTreeView sx={{ color: "#e6edf3" }} onItemSelectionToggle={onItemSelect}>
              {tree.length ? renderNodes(tree) : (
                <Typography variant="body2" sx={{ color: "#888", p: 1 }}>
                  No scripts found.
                </Typography>
              )}
            </SimpleTreeView>
          </Paper>
          <Box sx={{ width: 320 }}>
            <Typography variant="subtitle2" sx={{ color: "#ccc", mb: 1 }}>Selection</Typography>
            <Typography variant="body2" sx={{ color: selectedPath ? "#e6edf3" : "#888" }}>
              {selectedPath || "No script selected"}
            </Typography>
            <Box sx={{ mt: 2 }}>
              <FormControlLabel
                control={<Checkbox size="small" checked={runAsCurrentUser} onChange={(e) => setRunAsCurrentUser(e.target.checked)} />}
                label={<Typography variant="body2">Run as currently logged-in user</Typography>}
              />
              <Typography variant="caption" sx={{ color: "#888" }}>
                Unchecked = run as SYSTEM (requires agent service)
              </Typography>
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
