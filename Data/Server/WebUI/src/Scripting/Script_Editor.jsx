////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Scripting/Script_Editor.jsx

import React, { useState, useEffect, useCallback, useMemo } from "react";
import {
  Paper,
  Box,
  Typography,
  Button,
  Menu,
  MenuItem,
  Select,
  FormControl,
  InputLabel,
  TextField
} from "@mui/material";
import { Folder as FolderIcon, Description as DescriptionIcon } from "@mui/icons-material";
import {
  SimpleTreeView,
  TreeItem,
  useTreeViewApiRef
} from "@mui/x-tree-view";

// Reuse shared dialogs for consistency
import { ConfirmDeleteDialog, RenameFolderDialog } from "../Dialogs";

// Prism for syntax highlighting (same theme as JSON node)
import Prism from "prismjs";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-powershell";
import "prismjs/components/prism-batch";
import "prismjs/themes/prism-okaidia.css";
import Editor from "react-simple-code-editor";

// ---------- helpers ----------
const TYPE_OPTIONS = [
  { key: "ansible", label: "Ansible Playbook", ext: ".yml", prism: "yaml" },
  { key: "powershell", label: "Powershell Script", ext: ".ps1", prism: "powershell" },
  { key: "batch", label: "Batch Script", ext: ".bat", prism: "batch" },
  { key: "bash", label: "Bash Script", ext: ".sh", prism: "bash" }
];

const keyBy = (arr) => Object.fromEntries(arr.map((o) => [o.key, o]));
const TYPES = keyBy(TYPE_OPTIONS);

function typeFromFilename(name = "") {
  const n = name.toLowerCase();
  if (n.endsWith(".yml")) return "ansible";
  if (n.endsWith(".ps1")) return "powershell";
  if (n.endsWith(".bat")) return "batch";
  if (n.endsWith(".sh")) return "bash";
  return "ansible"; // default
}

function ensureExt(baseName, typeKey) {
  if (!baseName) return baseName;
  // If user already provided any extension, keep it.
  if (/\.[^./\\]+$/i.test(baseName)) return baseName;
  const t = TYPES[typeKey] || TYPES.ansible;
  return baseName + t.ext;
}

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

function highlightedHtml(code, prismLang) {
  try {
    const grammar = Prism.languages[prismLang] || Prism.languages.markup;
    return Prism.highlight(code ?? "", grammar, prismLang);
  } catch {
    return (code ?? "").replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]));
  }
}

// Local dialog: rename script file
function RenameScriptDialog({ open, value, onChange, onCancel, onSave, onBlur }) {
  return (
    <Paper
      component={(props) => (
        <div {...props} />
      )}
      sx={{ display: open ? "block" : "none" }}
    >
      <div
        style={{
          position: "fixed",
          inset: 0,
          background: "rgba(0,0,0,0.4)",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          zIndex: 9999
        }}
      >
        <Paper sx={{ bgcolor: "#121212", color: "#fff", p: 2, minWidth: 360 }}>
          <Typography variant="h6" sx={{ mb: 1 }}>Rename Script</Typography>
          <TextField
            autoFocus
            margin="dense"
            label="File Name"
            fullWidth
            variant="outlined"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            onBlur={() => onBlur && onBlur()}
            sx={{
              "& .MuiOutlinedInput-root": {
                backgroundColor: "#2a2a2a",
                color: "#ccc",
                "& fieldset": { borderColor: "#444" },
                "&:hover fieldset": { borderColor: "#666" }
              },
              label: { color: "#aaa" },
              mt: 1
            }}
          />
          <Box sx={{ display: "flex", justifyContent: "flex-end", mt: 2 }}>
            <Button onClick={onCancel} sx={{ color: "#58a6ff" }}>Cancel</Button>
            <Button onClick={onSave} sx={{ color: "#58a6ff" }}>Save</Button>
          </Box>
        </Paper>
      </div>
    </Paper>
  );
}

// Local dialog: new script (name + type)
function NewScriptDialog({ open, name, type, onChangeName, onChangeType, onCancel, onCreate, onBlurName }) {
  return (
    <Paper component={(p) => <div {...p} />} sx={{ display: open ? "block" : "none" }}>
      <div
        style={{
          position: "fixed",
          inset: 0,
          background: "rgba(0,0,0,0.4)",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          zIndex: 9999
        }}
      >
        <Paper sx={{ bgcolor: "#121212", color: "#fff", p: 2, minWidth: 360 }}>
          <Typography variant="h6" sx={{ mb: 1 }}>New Script</Typography>
          <TextField
            autoFocus
            margin="dense"
            label="File Name"
            fullWidth
            variant="outlined"
            value={name}
            onChange={(e) => onChangeName(e.target.value)}
            onBlur={() => onBlurName && onBlurName()}
            sx={{
              "& .MuiOutlinedInput-root": {
                backgroundColor: "#2a2a2a",
                color: "#ccc",
                "& fieldset": { borderColor: "#444" },
                "&:hover fieldset": { borderColor: "#666" }
              },
              label: { color: "#aaa" },
              mt: 1
            }}
          />
          <FormControl fullWidth sx={{ mt: 2 }}>
            <InputLabel sx={{ color: "#aaa" }}>Type</InputLabel>
            <Select
              value={type}
              label="Type"
              onChange={(e) => onChangeType(e.target.value)}
              sx={{
                color: "#e6edf3",
                bgcolor: "#1e1e1e",
                "& .MuiOutlinedInput-notchedOutline": { borderColor: "#444" },
                "&:hover .MuiOutlinedInput-notchedOutline": { borderColor: "#666" }
              }}
            >
              {TYPE_OPTIONS.map((o) => (
                <MenuItem key={o.key} value={o.key}>{o.label}</MenuItem>
              ))}
            </Select>
          </FormControl>
          <Box sx={{ display: "flex", justifyContent: "flex-end", mt: 2 }}>
            <Button onClick={onCancel} sx={{ color: "#58a6ff" }}>Cancel</Button>
            <Button onClick={onCreate} sx={{ color: "#58a6ff" }}>Create</Button>
          </Box>
        </Paper>
      </div>
    </Paper>
  );
}

export default function ScriptEditor() {
  // Tree state
  const [tree, setTree] = useState([]);
  const [nodeMap, setNodeMap] = useState({});
  const [contextMenu, setContextMenu] = useState(null);
  const [selectedNode, setSelectedNode] = useState(null);
  const apiRef = useTreeViewApiRef();
  const [dragNode, setDragNode] = useState(null);

  // Editor state
  const [currentPath, setCurrentPath] = useState("");
  const [currentFolder, setCurrentFolder] = useState("");
  const [fileName, setFileName] = useState("");
  const [type, setType] = useState("ansible");
  const [code, setCode] = useState("");

  // Dialog state
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const [renameFolderOpen, setRenameFolderOpen] = useState(false);
  const [folderDialogMode, setFolderDialogMode] = useState("rename");
  const [newScriptOpen, setNewScriptOpen] = useState(false);
  const [newScriptName, setNewScriptName] = useState("");
  const [newScriptType, setNewScriptType] = useState("ansible");
  const [deleteOpen, setDeleteOpen] = useState(false);

  const prismLang = useMemo(() => (TYPES[type]?.prism || "yaml"), [type]);

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

  useEffect(() => { loadTree(); }, [loadTree]);

  // Context menu
  const handleContextMenu = (e, node) => {
    e.preventDefault();
    setSelectedNode(node);
    setContextMenu(
      contextMenu === null
        ? { mouseX: e.clientX - 2, mouseY: e.clientY - 4 }
        : null
    );
  };

  const handleDrop = async (target) => {
    if (!dragNode || !target.isFolder) return;
    // Prevent dropping into itself or its descendants
    if (dragNode.path === target.path || target.path.startsWith(`${dragNode.path}/`)) {
      setDragNode(null);
      return;
    }
    const newPath = target.path ? `${target.path}/${dragNode.fileName}` : dragNode.fileName;
    try {
      await fetch("/api/scripts/move_file", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: dragNode.path, new_path: newPath })
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
      try {
        const resp = await fetch(`/api/scripts/load?path=${encodeURIComponent(node.path)}`);
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        const data = await resp.json();
        const folder = node.path.split("/").slice(0, -1).join("/");
        setCurrentFolder(folder);
        setCurrentPath(node.path);
        setFileName(data.file_name || node.fileName || node.label);
        setType(typeFromFilename(data.file_name || node.fileName || node.label));
        setCode(data.content || "");
      } catch (err) {
        console.error("Failed to load script:", err);
      }
    }
  };

  const saveScript = async () => {
    if (!currentPath && !fileName) {
      // prompt for name/type first if user started typing without creating
      setNewScriptName("");
      setNewScriptType(type);
      setNewScriptOpen(true);
      return;
    }
    // Ensure filename extension aligns with selected type when saving new file by name
    const normalizedName = currentPath ? undefined : ensureExt(fileName, type);
    const payload = {
      path: currentPath || undefined,
      name: normalizedName,
      content: code,
      type
    };
    try {
      const resp = await fetch("/api/scripts/save", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      });
      const data = await resp.json();
      if (!resp.ok) throw new Error(data?.error || `HTTP ${resp.status}`);
      // Update state with normalized rel_path (may include ext changes)
      if (data.rel_path) {
        setCurrentPath(data.rel_path);
        const fname = data.rel_path.split("/").pop();
        setFileName(fname);
        setType(typeFromFilename(fname));
        // ensure tree refresh
        loadTree();
      }
    } catch (err) {
      console.error("Failed to save script:", err);
    }
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

  const handleNewFolder = () => {
    if (!selectedNode) return;
    setContextMenu(null);
    setFolderDialogMode("create");
    setRenameValue("");
    setRenameFolderOpen(true);
  };

  const handleNewScript = () => {
    if (!selectedNode) return;
    setContextMenu(null);
    setNewScriptName("");
    setNewScriptType("ansible");
    setNewScriptOpen(true);
  };

  const saveRenameFile = async () => {
    try {
      const finalName = ensureExt(renameValue, type);
      const res = await fetch("/api/scripts/rename_file", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: selectedNode.path, new_name: finalName, type })
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data?.error || `HTTP ${res.status}`);
      if (currentPath === selectedNode.path) {
        setCurrentPath(data.rel_path || selectedNode.path);
        const fname = (data.rel_path || selectedNode.path).split("/").pop();
        setFileName(fname);
        setType(typeFromFilename(fname));
      }
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
        await fetch("/api/scripts/rename_folder", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path: selectedNode.path, new_name: renameValue })
        });
      } else {
        const basePath = selectedNode ? selectedNode.path : "";
        const newPath = basePath ? `${basePath}/${renameValue}` : renameValue;
        await fetch("/api/scripts/create_folder", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path: newPath })
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
        await fetch("/api/scripts/delete_folder", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path: selectedNode.path })
        });
      } else {
        await fetch("/api/scripts/delete_file", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ path: selectedNode.path })
        });
        if (currentPath === selectedNode.path) {
          setCurrentPath("");
          setFileName("");
          setCode("");
        }
      }
      setDeleteOpen(false);
      loadTree();
    } catch (err) {
      console.error("Failed to delete:", err);
      setDeleteOpen(false);
    }
  };

  const createNewScript = () => {
    const folder = selectedNode?.isFolder
      ? selectedNode.path
      : (selectedNode?.path?.split("/").slice(0, -1).join("/") || currentFolder || "");
    const finalName = ensureExt(newScriptName || "script", newScriptType);
    const newPath = folder ? `${folder}/${finalName}` : finalName;
    setCurrentFolder(folder);
    setCurrentPath(newPath);
    setFileName(finalName);
    setType(newScriptType);
    setCode("");
    setNewScriptOpen(false);
    // Note: not saved until user clicks Save
  };

  const rootChildIds = tree[0]?.children?.map((c) => c.id) || [];
  // live highlighting handled by react-simple-code-editor

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

  return (
    <Box sx={{ display: "flex", flex: 1, height: "100%", overflow: "hidden" }}>
      {/* Left: Tree */}
      <Paper sx={{ my: 2, ml: 2, mr: 1, p: 0, bgcolor: "#1e1e1e", width: 320, flexShrink: 0 }} elevation={2}>
        <Box sx={{ p: 2, pb: 1 }}>
          <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0 }}>Scripts</Typography>
          <Typography variant="body2" sx={{ color: "#aaa" }}>
            Create, edit, and rearrange scripts in folders.
          </Typography>
        </Box>
        <Box
          sx={{ p: 2 }}
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
              <MenuItem onClick={handleNewScript}>New Script</MenuItem>
              <MenuItem onClick={handleNewFolder}>New Subfolder</MenuItem>
              {selectedNode.id !== "root" && (
                <MenuItem onClick={handleRename}>Rename</MenuItem>
              )}
              {selectedNode.id !== "root" && (
                <MenuItem onClick={() => { setContextMenu(null); setDeleteOpen(true); }}>Delete</MenuItem>
              )}
            </>
          )}
          {!selectedNode?.isFolder && (
            <>
              <MenuItem onClick={() => { setContextMenu(null); handleNodeSelect(null, selectedNode.id); }}>Edit</MenuItem>
              <MenuItem onClick={handleRename}>Rename</MenuItem>
              <MenuItem onClick={() => { setContextMenu(null); setDeleteOpen(true); }}>Delete</MenuItem>
            </>
          )}
        </Menu>
      </Paper>

      {/* Right: Editor */}
      <Paper sx={{ my: 2, mr: 2, ml: 1, p: 1.5, bgcolor: "#1e1e1e", display: "flex", flexDirection: "column", flex: 1 }} elevation={2}>
        <Box sx={{ display: "flex", alignItems: "center", gap: 2, mb: 2 }}>
          <FormControl size="small" sx={{ minWidth: 220 }}>
            <InputLabel sx={{ color: "#aaa" }}>Type</InputLabel>
            <Select
              value={type}
              label="Type"
              onChange={(e) => setType(e.target.value)}
              sx={{
                color: "#e6edf3",
                bgcolor: "#1e1e1e",
                "& .MuiOutlinedInput-notchedOutline": { borderColor: "#444" },
                "&:hover .MuiOutlinedInput-notchedOutline": { borderColor: "#666" }
              }}
            >
              {TYPE_OPTIONS.map((o) => (
                <MenuItem key={o.key} value={o.key}>{o.label}</MenuItem>
              ))}
            </Select>
          </FormControl>

          <Box sx={{ flex: 1 }} />

          {fileName && (
            <Button
              onClick={() => { setRenameValue(fileName); setRenameOpen(true); }}
              sx={{ color: "#58a6ff", textTransform: "none" }}
            >
              Rename: {fileName}
            </Button>
          )}

          <Button
            onClick={saveScript}
            sx={{
              color: "#58a6ff",
              borderColor: "#58a6ff",
              textTransform: "none",
              border: "1px solid #58a6ff",
              backgroundColor: "#1e1e1e",
              "&:hover": { backgroundColor: "#1b1b1b" }
            }}
          >
            Save
          </Button>
        </Box>

        {/* Single-pane live-highlight editor */}
        <Box sx={{ flex: 1, minHeight: 300, border: "1px solid #444", borderRadius: 1, background: "#121212", overflow: "auto" }}>
          <Editor
            value={code}
            onValueChange={setCode}
            highlight={(src) => highlightedHtml(src, prismLang)}
            padding={12}
            placeholder={currentPath ? `Editing: ${currentPath}` : "New Script..."}
            style={{
              fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
              fontSize: 14,
              color: "#e6edf3",
              background: "#121212",
              outline: "none",
              minHeight: 300,
              lineHeight: 1.4,
              caretColor: "#58a6ff"
            }}
          />
        </Box>
      </Paper>

      {/* Dialogs */}
      <RenameScriptDialog
        open={renameOpen}
        value={renameValue}
        onChange={setRenameValue}
        onCancel={() => setRenameOpen(false)}
        onSave={saveRenameFile}
        onBlur={() => setRenameValue((v) => ensureExt(v, type))}
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

      <NewScriptDialog
        open={newScriptOpen}
        name={newScriptName}
        type={newScriptType}
        onChangeName={setNewScriptName}
        onChangeType={setNewScriptType}
        onCancel={() => setNewScriptOpen(false)}
        onCreate={createNewScript}
        onBlurName={() => setNewScriptName((v) => ensureExt(v, newScriptType))}
      />

      <ConfirmDeleteDialog
        open={deleteOpen}
        message="If you delete this, there is no undo button, are you sure you want to proceed?"
        onCancel={() => setDeleteOpen(false)}
        onConfirm={confirmDelete}
      />
    </Box>
  );
}
