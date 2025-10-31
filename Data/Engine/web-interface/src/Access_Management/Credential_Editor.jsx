import React, { useEffect, useMemo, useState } from "react";
import {
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControl,
  InputLabel,
  MenuItem,
  Select,
  TextField,
  Typography,
  IconButton,
  Tooltip,
  CircularProgress
} from "@mui/material";
import UploadIcon from "@mui/icons-material/UploadFile";
import ClearIcon from "@mui/icons-material/Clear";

const CREDENTIAL_TYPES = [
  { value: "machine", label: "Machine" },
  { value: "domain", label: "Domain" },
  { value: "token", label: "Token" }
];

const CONNECTION_TYPES = [
  { value: "ssh", label: "SSH" },
  { value: "winrm", label: "WinRM" }
];

const BECOME_METHODS = [
  { value: "", label: "None" },
  { value: "sudo", label: "sudo" },
  { value: "su", label: "su" },
  { value: "runas", label: "runas" },
  { value: "enable", label: "enable" }
];

function emptyForm() {
  return {
    name: "",
    description: "",
    site_id: "",
    credential_type: "machine",
    connection_type: "ssh",
    username: "",
    password: "",
    private_key: "",
    private_key_passphrase: "",
    become_method: "",
    become_username: "",
    become_password: ""
  };
}

function normalizeSiteId(value) {
  if (value === null || typeof value === "undefined" || value === "") return "";
  const num = Number(value);
  if (Number.isNaN(num)) return "";
  return String(num);
}

export default function CredentialEditor({
  open,
  mode = "create",
  credential,
  onClose,
  onSaved
}) {
  const isEdit = mode === "edit" && credential && credential.id;
  const [form, setForm] = useState(emptyForm);
  const [sites, setSites] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [passwordDirty, setPasswordDirty] = useState(false);
  const [privateKeyDirty, setPrivateKeyDirty] = useState(false);
  const [passphraseDirty, setPassphraseDirty] = useState(false);
  const [becomePasswordDirty, setBecomePasswordDirty] = useState(false);
  const [clearPassword, setClearPassword] = useState(false);
  const [clearPrivateKey, setClearPrivateKey] = useState(false);
  const [clearPassphrase, setClearPassphrase] = useState(false);
  const [clearBecomePassword, setClearBecomePassword] = useState(false);
  const [fetchingDetail, setFetchingDetail] = useState(false);

  const credentialId = credential?.id;

  useEffect(() => {
    if (!open) return;
    let canceled = false;
    (async () => {
      try {
        const resp = await fetch("/api/sites");
        if (!resp.ok) return;
        const data = await resp.json();
        if (canceled) return;
        const parsed = Array.isArray(data?.sites)
          ? data.sites
              .filter((s) => s && s.id)
              .map((s) => ({
                id: s.id,
                name: s.name || `Site ${s.id}`
              }))
          : [];
        parsed.sort((a, b) => String(a.name || "").localeCompare(String(b.name || "")));
        setSites(parsed);
      } catch {
        if (!canceled) setSites([]);
      }
    })();
    return () => {
      canceled = true;
    };
  }, [open]);

  useEffect(() => {
    if (!open) return;
    setError("");
    setPasswordDirty(false);
    setPrivateKeyDirty(false);
    setPassphraseDirty(false);
    setBecomePasswordDirty(false);
    setClearPassword(false);
    setClearPrivateKey(false);
    setClearPassphrase(false);
    setClearBecomePassword(false);
    if (isEdit && credentialId) {
      const applyData = (detail) => {
        const next = emptyForm();
        next.name = detail?.name || "";
        next.description = detail?.description || "";
        next.site_id = normalizeSiteId(detail?.site_id);
        next.credential_type = (detail?.credential_type || "machine").toLowerCase();
        next.connection_type = (detail?.connection_type || "ssh").toLowerCase();
        next.username = detail?.username || "";
        next.become_method = (detail?.become_method || "").toLowerCase();
        next.become_username = detail?.become_username || "";
        setForm(next);
      };

      if (credential?.name) {
        applyData(credential);
      } else {
        setFetchingDetail(true);
        (async () => {
          try {
            const resp = await fetch(`/api/credentials/${credentialId}`);
            if (resp.ok) {
              const data = await resp.json();
              applyData(data?.credential || {});
            }
          } catch {
            /* ignore */
          } finally {
            setFetchingDetail(false);
          }
        })();
      }
    } else {
      setForm(emptyForm());
    }
  }, [open, isEdit, credentialId, credential]);

  const currentCredentialFlags = useMemo(() => ({
    hasPassword: Boolean(credential?.has_password),
    hasPrivateKey: Boolean(credential?.has_private_key),
    hasPrivateKeyPassphrase: Boolean(credential?.has_private_key_passphrase),
    hasBecomePassword: Boolean(credential?.has_become_password)
  }), [credential]);

  const disableSave = loading || fetchingDetail;

  const updateField = (key) => (event) => {
    const value = event?.target?.value ?? "";
    setForm((prev) => ({ ...prev, [key]: value }));
    if (key === "password") {
      setPasswordDirty(true);
      setClearPassword(false);
    } else if (key === "private_key") {
      setPrivateKeyDirty(true);
      setClearPrivateKey(false);
    } else if (key === "private_key_passphrase") {
      setPassphraseDirty(true);
      setClearPassphrase(false);
    } else if (key === "become_password") {
      setBecomePasswordDirty(true);
      setClearBecomePassword(false);
    }
  };

  const handlePrivateKeyUpload = async (event) => {
    const file = event.target.files?.[0];
    if (!file) return;
    try {
      const text = await file.text();
      setForm((prev) => ({ ...prev, private_key: text }));
      setPrivateKeyDirty(true);
      setClearPrivateKey(false);
    } catch {
      setError("Unable to read private key file.");
    } finally {
      event.target.value = "";
    }
  };

  const handleCancel = () => {
    if (loading) return;
    onClose && onClose();
  };

  const validate = () => {
    if (!form.name.trim()) {
      setError("Credential name is required.");
      return false;
    }
    setError("");
    return true;
  };

  const buildPayload = () => {
    const payload = {
      name: form.name.trim(),
      description: form.description.trim(),
      credential_type: (form.credential_type || "machine").toLowerCase(),
      connection_type: (form.connection_type || "ssh").toLowerCase(),
      username: form.username.trim(),
      become_method: form.become_method.trim(),
      become_username: form.become_username.trim()
    };
    const siteId = normalizeSiteId(form.site_id);
    if (siteId) {
      payload.site_id = Number(siteId);
    } else {
      payload.site_id = null;
    }
    if (passwordDirty) {
      payload.password = form.password;
    }
    if (privateKeyDirty) {
      payload.private_key = form.private_key;
    }
    if (passphraseDirty) {
      payload.private_key_passphrase = form.private_key_passphrase;
    }
    if (becomePasswordDirty) {
      payload.become_password = form.become_password;
    }
    if (clearPassword) payload.clear_password = true;
    if (clearPrivateKey) payload.clear_private_key = true;
    if (clearPassphrase) payload.clear_private_key_passphrase = true;
    if (clearBecomePassword) payload.clear_become_password = true;
    return payload;
  };

  const handleSave = async () => {
    if (!validate()) return;
    setLoading(true);
    setError("");
    const payload = buildPayload();
    try {
      const resp = await fetch(
        isEdit ? `/api/credentials/${credentialId}` : "/api/credentials",
        {
          method: isEdit ? "PUT" : "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(payload)
        }
      );
      const data = await resp.json();
      if (!resp.ok) {
        throw new Error(data?.error || `Request failed (${resp.status})`);
      }
      onSaved && onSaved(data?.credential || null);
    } catch (err) {
      setError(String(err.message || err));
    } finally {
      setLoading(false);
    }
  };

  const title = isEdit ? "Edit Credential" : "Create Credential";
  const helperStyle = { fontSize: 12, color: "#8a8a8a", mt: 0.5 };

  return (
    <Dialog
      open={open}
      onClose={handleCancel}
      maxWidth="md"
      fullWidth
      PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}
    >
      <DialogTitle sx={{ pb: 1 }}>{title}</DialogTitle>
      <DialogContent dividers sx={{ display: "flex", flexDirection: "column", gap: 2 }}>
        {fetchingDetail && (
          <Box sx={{ display: "flex", alignItems: "center", gap: 1, color: "#aaa" }}>
            <CircularProgress size={18} sx={{ color: "#58a6ff" }} />
            <Typography variant="body2">Loading credential details…</Typography>
          </Box>
        )}
        {error && (
          <Box sx={{ bgcolor: "#2c1c1c", border: "1px solid #663939", borderRadius: 1, p: 1 }}>
            <Typography variant="body2" sx={{ color: "#ff8080" }}>{error}</Typography>
          </Box>
        )}
        <TextField
          label="Name"
          value={form.name}
          onChange={updateField("name")}
          required
          disabled={disableSave}
          sx={{
            "& .MuiInputBase-root": { bgcolor: "#1f1f1f", color: "#fff" },
            "& label": { color: "#888" }
          }}
        />
        <TextField
          label="Description"
          value={form.description}
          onChange={updateField("description")}
          disabled={disableSave}
          multiline
          minRows={2}
          sx={{
            "& .MuiInputBase-root": { bgcolor: "#1f1f1f", color: "#fff" },
            "& label": { color: "#888" }
          }}
        />
        <Box sx={{ display: "flex", flexWrap: "wrap", gap: 2 }}>
          <FormControl sx={{ minWidth: 220 }} size="small" disabled={disableSave}>
            <InputLabel sx={{ color: "#aaa" }}>Site</InputLabel>
            <Select
              value={form.site_id}
              label="Site"
              onChange={updateField("site_id")}
              sx={{ bgcolor: "#1f1f1f", color: "#fff" }}
            >
              <MenuItem value="">(None)</MenuItem>
              {sites.map((site) => (
                <MenuItem key={site.id} value={String(site.id)}>
                  {site.name}
                </MenuItem>
              ))}
            </Select>
          </FormControl>
          <FormControl sx={{ minWidth: 180 }} size="small" disabled={disableSave}>
            <InputLabel sx={{ color: "#aaa" }}>Credential Type</InputLabel>
            <Select
              value={form.credential_type}
              label="Credential Type"
              onChange={updateField("credential_type")}
              sx={{ bgcolor: "#1f1f1f", color: "#fff" }}
            >
              {CREDENTIAL_TYPES.map((opt) => (
                <MenuItem key={opt.value} value={opt.value}>{opt.label}</MenuItem>
              ))}
            </Select>
          </FormControl>
          <FormControl sx={{ minWidth: 180 }} size="small" disabled={disableSave}>
            <InputLabel sx={{ color: "#aaa" }}>Connection</InputLabel>
            <Select
              value={form.connection_type}
              label="Connection"
              onChange={updateField("connection_type")}
              sx={{ bgcolor: "#1f1f1f", color: "#fff" }}
            >
              {CONNECTION_TYPES.map((opt) => (
                <MenuItem key={opt.value} value={opt.value}>{opt.label}</MenuItem>
              ))}
            </Select>
          </FormControl>
        </Box>
        <TextField
          label="Username"
          value={form.username}
          onChange={updateField("username")}
          disabled={disableSave}
          sx={{
            "& .MuiInputBase-root": { bgcolor: "#1f1f1f", color: "#fff" },
            "& label": { color: "#888" }
          }}
        />
        <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
          <TextField
            label="Password"
            type="password"
            value={form.password}
            onChange={updateField("password")}
            disabled={disableSave}
            sx={{
              flex: 1,
              "& .MuiInputBase-root": { bgcolor: "#1f1f1f", color: "#fff" },
              "& label": { color: "#888" }
            }}
          />
          {isEdit && currentCredentialFlags.hasPassword && !passwordDirty && !clearPassword && (
            <Tooltip title="Clear stored password">
              <IconButton size="small" onClick={() => setClearPassword(true)} sx={{ color: "#ff8080" }}>
                <ClearIcon fontSize="small" />
              </IconButton>
            </Tooltip>
          )}
        </Box>
        {isEdit && currentCredentialFlags.hasPassword && !passwordDirty && !clearPassword && (
          <Typography sx={helperStyle}>Stored password will remain unless you change or clear it.</Typography>
        )}
        {clearPassword && (
          <Typography sx={{ ...helperStyle, color: "#ffaaaa" }}>Password will be removed when saving.</Typography>
        )}

        <Box sx={{ display: "flex", gap: 1, alignItems: "flex-start" }}>
          <TextField
            label="SSH Private Key"
            value={form.private_key}
            onChange={updateField("private_key")}
            disabled={disableSave}
            multiline
            minRows={4}
            maxRows={12}
            sx={{
              flex: 1,
              "& .MuiInputBase-root": { bgcolor: "#1f1f1f", color: "#fff", fontFamily: "monospace" },
              "& label": { color: "#888" }
            }}
          />
          <Button
            variant="outlined"
            component="label"
            startIcon={<UploadIcon />}
            disabled={disableSave}
            sx={{ alignSelf: "center", borderColor: "#58a6ff", color: "#58a6ff" }}
          >
            Upload
            <input type="file" hidden accept=".pem,.key,.txt" onChange={handlePrivateKeyUpload} />
          </Button>
          {isEdit && currentCredentialFlags.hasPrivateKey && !privateKeyDirty && !clearPrivateKey && (
            <Tooltip title="Clear stored private key">
              <IconButton size="small" onClick={() => setClearPrivateKey(true)} sx={{ color: "#ff8080" }}>
                <ClearIcon fontSize="small" />
              </IconButton>
            </Tooltip>
          )}
        </Box>
        {isEdit && currentCredentialFlags.hasPrivateKey && !privateKeyDirty && !clearPrivateKey && (
          <Typography sx={helperStyle}>Private key is stored. Upload or paste a new one to replace, or clear it.</Typography>
        )}
        {clearPrivateKey && (
          <Typography sx={{ ...helperStyle, color: "#ffaaaa" }}>Private key will be removed when saving.</Typography>
        )}

        <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
          <TextField
            label="Private Key Passphrase"
            type="password"
            value={form.private_key_passphrase}
            onChange={updateField("private_key_passphrase")}
            disabled={disableSave}
            sx={{
              flex: 1,
              "& .MuiInputBase-root": { bgcolor: "#1f1f1f", color: "#fff" },
              "& label": { color: "#888" }
            }}
          />
          {isEdit && currentCredentialFlags.hasPrivateKeyPassphrase && !passphraseDirty && !clearPassphrase && (
            <Tooltip title="Clear stored passphrase">
              <IconButton size="small" onClick={() => setClearPassphrase(true)} sx={{ color: "#ff8080" }}>
                <ClearIcon fontSize="small" />
              </IconButton>
            </Tooltip>
          )}
        </Box>
        {isEdit && currentCredentialFlags.hasPrivateKeyPassphrase && !passphraseDirty && !clearPassphrase && (
          <Typography sx={helperStyle}>A passphrase is stored for this key.</Typography>
        )}
        {clearPassphrase && (
          <Typography sx={{ ...helperStyle, color: "#ffaaaa" }}>Key passphrase will be removed when saving.</Typography>
        )}

        <Box sx={{ display: "flex", gap: 2, flexWrap: "wrap" }}>
          <FormControl sx={{ minWidth: 180 }} size="small" disabled={disableSave}>
            <InputLabel sx={{ color: "#aaa" }}>Privilege Escalation</InputLabel>
            <Select
              value={form.become_method}
              label="Privilege Escalation"
              onChange={updateField("become_method")}
              sx={{ bgcolor: "#1f1f1f", color: "#fff" }}
            >
              {BECOME_METHODS.map((opt) => (
                <MenuItem key={opt.value || "none"} value={opt.value}>{opt.label}</MenuItem>
              ))}
            </Select>
          </FormControl>
          <TextField
            label="Escalation Username"
            value={form.become_username}
            onChange={updateField("become_username")}
            disabled={disableSave}
            sx={{
              flex: 1,
              minWidth: 200,
              "& .MuiInputBase-root": { bgcolor: "#1f1f1f", color: "#fff" },
              "& label": { color: "#888" }
            }}
          />
        </Box>
        <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
          <TextField
            label="Escalation Password"
            type="password"
            value={form.become_password}
            onChange={updateField("become_password")}
            disabled={disableSave}
            sx={{
              flex: 1,
              "& .MuiInputBase-root": { bgcolor: "#1f1f1f", color: "#fff" },
              "& label": { color: "#888" }
            }}
          />
          {isEdit && currentCredentialFlags.hasBecomePassword && !becomePasswordDirty && !clearBecomePassword && (
            <Tooltip title="Clear stored escalation password">
              <IconButton size="small" onClick={() => setClearBecomePassword(true)} sx={{ color: "#ff8080" }}>
                <ClearIcon fontSize="small" />
              </IconButton>
            </Tooltip>
          )}
        </Box>
        {isEdit && currentCredentialFlags.hasBecomePassword && !becomePasswordDirty && !clearBecomePassword && (
          <Typography sx={helperStyle}>Escalation password is stored.</Typography>
        )}
        {clearBecomePassword && (
          <Typography sx={{ ...helperStyle, color: "#ffaaaa" }}>Escalation password will be removed when saving.</Typography>
        )}
      </DialogContent>
      <DialogActions sx={{ px: 3, py: 2 }}>
        <Button onClick={handleCancel} sx={{ color: "#58a6ff" }} disabled={loading}>
          Cancel
        </Button>
        <Button
          onClick={handleSave}
          variant="outlined"
          sx={{ color: "#58a6ff", borderColor: "#58a6ff" }}
          disabled={disableSave}
        >
          {loading ? <CircularProgress size={18} sx={{ color: "#58a6ff" }} /> : "Save"}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
