import React, { useState, useEffect, useMemo } from "react";
import { Paper, Box, Typography, Button, Select, FormControl, InputLabel, TextField, MenuItem } from "@mui/material";
import Prism from "prismjs";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-powershell";
import "prismjs/components/prism-batch";
import "prismjs/themes/prism-okaidia.css";
import Editor from "react-simple-code-editor";
import { ConfirmDeleteDialog } from "../Dialogs";

const TYPE_OPTIONS_ALL = [
  { key: "ansible", label: "Ansible Playbook", ext: ".yml", prism: "yaml" },
  { key: "powershell", label: "Powershell Script", ext: ".ps1", prism: "powershell" },
  { key: "batch", label: "Batch Script", ext: ".bat", prism: "batch" },
  { key: "bash", label: "Bash Script", ext: ".sh", prism: "bash" }
];

const keyBy = (arr) => Object.fromEntries(arr.map((o) => [o.key, o]));

function typeFromFilename(name = "") {
  const n = name.toLowerCase();
  if (n.endsWith(".yml")) return "ansible";
  if (n.endsWith(".ps1")) return "powershell";
  if (n.endsWith(".bat")) return "batch";
  if (n.endsWith(".sh")) return "bash";
  return "powershell";
}

function ensureExt(baseName, t) {
  if (!baseName) return baseName;
  if (/\.[^./\\]+$/i.test(baseName)) return baseName;
  const TYPES = keyBy(TYPE_OPTIONS_ALL);
  const type = TYPES[t] || TYPES.powershell;
  return baseName + type.ext;
}

function highlightedHtml(code, prismLang) {
  try {
    const grammar = Prism.languages[prismLang] || Prism.languages.markup;
    return Prism.highlight(code ?? "", grammar, prismLang);
  } catch {
    return (code ?? "").replace(/[&<>]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;" }[c]));
  }
}

function RenameFileDialog({ open, value, onChange, onCancel, onSave }) {
  if (!open) return null;
  return (
    <div style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,0.4)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 9999 }}>
      <Paper sx={{ bgcolor: "#121212", color: "#fff", p: 2, minWidth: 360 }}>
        <Typography variant="h6" sx={{ mb: 1 }}>Rename</Typography>
        <TextField autoFocus margin="dense" label="Name" fullWidth variant="outlined" value={value} onChange={(e) => onChange(e.target.value)}
          sx={{ "& .MuiOutlinedInput-root": { backgroundColor: "#2a2a2a", color: "#ccc", "& fieldset": { borderColor: "#444" }, "&:hover fieldset": { borderColor: "#666" } }, label: { color: "#aaa" }, mt: 1 }} />
        <Box sx={{ display: "flex", justifyContent: "flex-end", mt: 2 }}>
          <Button onClick={onCancel} sx={{ color: "#58a6ff" }}>Cancel</Button>
          <Button onClick={onSave} sx={{ color: "#58a6ff" }}>Save</Button>
        </Box>
      </Paper>
    </div>
  );
}

function NewItemDialog({ open, name, type, typeOptions, onChangeName, onChangeType, onCancel, onCreate }) {
  if (!open) return null;
  return (
    <div style={{ position: "fixed", inset: 0, background: "rgba(0,0,0,0.4)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 9999 }}>
      <Paper sx={{ bgcolor: "#121212", color: "#fff", p: 2, minWidth: 360 }}>
        <Typography variant="h6" sx={{ mb: 1 }}>New</Typography>
        <TextField autoFocus margin="dense" label="Name" fullWidth variant="outlined" value={name} onChange={(e) => onChangeName(e.target.value)}
          sx={{ "& .MuiOutlinedInput-root": { backgroundColor: "#2a2a2a", color: "#ccc", "& fieldset": { borderColor: "#444" }, "&:hover fieldset": { borderColor: "#666" } }, label: { color: "#aaa" }, mt: 1 }} />
        <FormControl fullWidth sx={{ mt: 2 }}>
          <InputLabel sx={{ color: "#aaa" }}>Type</InputLabel>
          <Select value={type} label="Type" onChange={(e) => onChangeType(e.target.value)}
            sx={{ color: "#e6edf3", bgcolor: "#1e1e1e", "& .MuiOutlinedInput-notchedOutline": { borderColor: "#444" }, "&:hover .MuiOutlinedInput-notchedOutline": { borderColor: "#666" } }}>
            {typeOptions.map((o) => (<MenuItem key={o.key} value={o.key}>{o.label}</MenuItem>))}
          </Select>
        </FormControl>
        <Box sx={{ display: "flex", justifyContent: "flex-end", mt: 2 }}>
          <Button onClick={onCancel} sx={{ color: "#58a6ff" }}>Cancel</Button>
          <Button onClick={onCreate} sx={{ color: "#58a6ff" }}>Create</Button>
        </Box>
      </Paper>
    </div>
  );
}

export default function ScriptEditor({ mode = "scripts", initialPath = "", onConsumedInitialPath, onSaved }) {
  const isAnsible = mode === "ansible";
  const TYPE_OPTIONS = useMemo(() => (isAnsible ? TYPE_OPTIONS_ALL.filter(o => o.key === 'ansible') : TYPE_OPTIONS_ALL.filter(o => o.key !== 'ansible')), [isAnsible]);

  const [currentPath, setCurrentPath] = useState("");
  const [fileName, setFileName] = useState("");
  const [type, setType] = useState(isAnsible ? "ansible" : "powershell");
  const [code, setCode] = useState("");

  const [renameOpen, setRenameOpen] = useState(false);
  const [renameValue, setRenameValue] = useState("");
  const [newOpen, setNewOpen] = useState(false);
  const [newName, setNewName] = useState("");
  const [newType, setNewType] = useState(isAnsible ? "ansible" : "powershell");
  const [deleteOpen, setDeleteOpen] = useState(false);

  const island = useMemo(() => (isAnsible ? 'ansible' : 'scripts'), [isAnsible]);

  useEffect(() => {
    (async () => {
      if (!initialPath) return;
      try {
        const resp = await fetch(`/api/assembly/load?island=${encodeURIComponent(island)}&path=${encodeURIComponent(initialPath)}`);
        if (resp.ok) {
          const data = await resp.json();
          setCurrentPath(data.rel_path || initialPath);
          const fname = data.file_name || initialPath.split('/').pop() || '';
          setFileName(fname);
          setType(typeFromFilename(fname));
          setCode(data.content || "");
        }
      } catch {}
      if (onConsumedInitialPath) onConsumedInitialPath();
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialPath, island]);

  const saveFile = async () => {
    if (!currentPath && !fileName) {
      setNewName("");
      setNewType(isAnsible ? "ansible" : type);
      setNewOpen(true);
      return;
    }
    const island = isAnsible ? 'ansible' : 'scripts';
    const normalizedName = currentPath ? currentPath : ensureExt(fileName, type);
    try {
      // If we already have a path, edit; otherwise create
      if (currentPath) {
        const resp = await fetch(`/api/assembly/edit`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ island, path: currentPath, content: code })
        });
        if (!resp.ok) {
          const data = await resp.json().catch(() => ({}));
          throw new Error(data?.error || `HTTP ${resp.status}`);
        }
        onSaved && onSaved();
      } else {
        const resp = await fetch(`/api/assembly/create`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ island, kind: 'file', path: normalizedName, content: code, type })
        });
        const data = await resp.json();
        if (!resp.ok) throw new Error(data?.error || `HTTP ${resp.status}`);
        if (data.rel_path) {
          setCurrentPath(data.rel_path);
          const fname = data.rel_path.split('/').pop();
          setFileName(fname);
          setType(typeFromFilename(fname));
          onSaved && onSaved();
        }
      }
    } catch (err) {
      console.error("Failed to save:", err);
    }
  };

  const saveRenameFile = async () => {
    try {
      const island = isAnsible ? 'ansible' : 'scripts';
      const finalName = ensureExt(renameValue, type);
      const res = await fetch(`/api/assembly/rename`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ island, kind: 'file', path: currentPath, new_name: finalName, type }) });
      const data = await res.json();
      if (!res.ok) throw new Error(data?.error || `HTTP ${res.status}`);
      setCurrentPath(data.rel_path || currentPath);
      const fname = (data.rel_path || currentPath).split('/').pop();
      setFileName(fname);
      setType(typeFromFilename(fname));
      setRenameOpen(false);
    } catch (err) {
      console.error("Failed to rename file:", err);
      setRenameOpen(false);
    }
  };

  const createNew = () => {
    const finalName = ensureExt(newName || (isAnsible ? "playbook" : "script"), newType);
    setCurrentPath(finalName);
    setFileName(finalName);
    setType(newType);
    setCode("");
    setNewOpen(false);
  };

  return (
    <Box sx={{ display: "flex", flex: 1, height: "100%", overflow: "hidden" }}>
      <Paper sx={{ my: 2, mx: 2, p: 1.5, bgcolor: "#1e1e1e", display: "flex", flexDirection: "column", flex: 1 }} elevation={2}>
        <Box sx={{ display: "flex", alignItems: "center", gap: 2, mb: 2 }}>
          <FormControl size="small" sx={{ minWidth: 220 }}>
            <InputLabel sx={{ color: "#aaa" }}>Type</InputLabel>
            <Select value={type} label="Type" onChange={(e) => setType(e.target.value)} sx={{ color: "#e6edf3", bgcolor: "#1e1e1e", "& .MuiOutlinedInput-notchedOutline": { borderColor: "#444" }, "&:hover .MuiOutlinedInput-notchedOutline": { borderColor: "#666" } }}>
              {TYPE_OPTIONS.map((o) => (<MenuItem key={o.key} value={o.key}>{o.label}</MenuItem>))}
            </Select>
          </FormControl>
          <Box sx={{ flex: 1 }} />
          {fileName && (
            <Button onClick={() => { setRenameValue(fileName); setRenameOpen(true); }} sx={{ color: "#58a6ff", textTransform: "none" }}>Rename: {fileName}</Button>
          )}
          <Button onClick={saveFile} sx={{ color: "#58a6ff", borderColor: "#58a6ff", textTransform: "none", border: "1px solid #58a6ff", backgroundColor: "#1e1e1e", "&:hover": { backgroundColor: "#1b1b1b" } }}>Save</Button>
        </Box>
        <Box sx={{ flex: 1, minHeight: 300, border: "1px solid #444", borderRadius: 1, background: "#121212", overflow: "auto" }}>
          <Editor value={code} onValueChange={setCode} highlight={(src) => highlightedHtml(src, (keyBy(TYPE_OPTIONS_ALL)[type]?.prism || 'yaml'))} padding={12} placeholder={currentPath ? `Editing: ${currentPath}` : (isAnsible ? "New Playbook..." : "New Script...")}
            style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace', fontSize: 14, color: "#e6edf3", background: "#121212", outline: "none", minHeight: 300, lineHeight: 1.4, caretColor: "#58a6ff" }} />
        </Box>
      </Paper>

      {/* Dialogs */}
      <RenameFileDialog open={renameOpen} value={renameValue} onChange={setRenameValue} onCancel={() => setRenameOpen(false)} onSave={saveRenameFile} />
      <NewItemDialog open={newOpen} name={newName} type={newType} typeOptions={TYPE_OPTIONS} onChangeName={setNewName} onChangeType={setNewType} onCancel={() => setNewOpen(false)} onCreate={createNew} />
      <ConfirmDeleteDialog open={deleteOpen} message="If you delete this, there is no undo button, are you sure you want to proceed?" onCancel={() => setDeleteOpen(false)} onConfirm={() => { setDeleteOpen(false); onSaved && onSaved(); }} />
    </Box>
  );
}
