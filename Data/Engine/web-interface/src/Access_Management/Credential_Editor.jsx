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
  Link,
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
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_INPUT_SX,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_SELECT_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";

const MAGIC_UI = {
  panelBg: "rgba(8,12,24,0.96)",
  panelBorder: "rgba(148,163,184,0.35)",
  textBright: "#e2e8f0",
  textMuted: "#94a3b8",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
  danger: "#ff8a8a",
};

const OUTLINE_BUTTON_SX = {
  ...DIALOG_BUTTON_SX,
  borderColor: MAGIC_UI.panelBorder,
  color: MAGIC_UI.textBright,
  "&:hover": {
    ...DIALOG_BUTTON_SX["&:hover"],
    borderColor: MAGIC_UI.accentA,
  },
};

const INPUT_SX = {
  ...DIALOG_INPUT_SX,
  "& .MuiOutlinedInput-root, & .MuiInputBase-root": {
    ...DIALOG_INPUT_SX["& .MuiOutlinedInput-root, & .MuiInputBase-root"],
    borderRadius: 2,
    "& fieldset": { borderColor: MAGIC_UI.panelBorder },
    "&:hover fieldset": { borderColor: MAGIC_UI.accentA },
    "&.Mui-focused fieldset": { borderColor: MAGIC_UI.accentA },
  },
};

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
  const isGitHubToken = credential?.row_kind === "github_token";
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
  const helperStyle = { fontSize: 12, color: MAGIC_UI.textMuted, mt: 0.5 };

  useEffect(() => {
    if (!open || isGitHubToken) {
      setSites([]);
      return;
    }
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
  }, [open, isGitHubToken]);

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
    if (isGitHubToken) {
      const next = emptyForm();
      next.name = credential?.name || "GitHub API Token";
      next.description =
        credential?.description || "Significantly increases GitHub API rate limits for both the Borealis Repository and the Aurora Assembly Repository.";
      next.credential_type = "token";
      next.connection_type = "github";
      next.username = "";
      next.password = credential?.token || "";
      setForm(next);
      setFetchingDetail(false);
      return;
    }
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
  }, [open, isEdit, credentialId, credential, isGitHubToken]);

  const currentCredentialFlags = useMemo(() => ({
    hasPassword: isGitHubToken ? Boolean(credential?.token) : Boolean(credential?.has_password),
    hasPrivateKey: isGitHubToken ? false : Boolean(credential?.has_private_key),
    hasPrivateKeyPassphrase: isGitHubToken ? false : Boolean(credential?.has_private_key_passphrase),
    hasBecomePassword: isGitHubToken ? false : Boolean(credential?.has_become_password)
  }), [credential, isGitHubToken]);

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
    if (isGitHubToken) {
      setError("");
      return true;
    }
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
    try {
      const resp = await fetch(
        isGitHubToken ? "/api/github/token" : isEdit ? `/api/credentials/${credentialId}` : "/api/credentials",
        {
          method: isGitHubToken ? "POST" : isEdit ? "PUT" : "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(isGitHubToken ? { token: form.password } : buildPayload())
        }
      );
      const data = await resp.json();
      if (!resp.ok) {
        throw new Error(data?.message || data?.error || `Request failed (${resp.status})`);
      }
      onSaved && onSaved(isGitHubToken ? data || null : data?.credential || null);
    } catch (err) {
      setError(String(err.message || err));
    } finally {
      setLoading(false);
    }
  };

  const title = isGitHubToken ? "Edit GitHub API Token" : isEdit ? "Edit Credential" : "Create Credential";
  const subtitle = isGitHubToken
    ? "Update the GitHub Personal Access Token used for Borealis repository lookups and elevated API rate limits."
    : isEdit
    ? "Update stored authentication details and connection defaults."
    : "Create a reusable authentication record for Borealis connections.";
  const tokenFieldLabel = isGitHubToken ? "Token" : "Password";
  const saveLabel = isGitHubToken ? "Save Token" : "Save";
  const githubHintStyle = {
    bgcolor: "rgba(125,211,252,0.08)",
    border: `1px solid ${MAGIC_UI.panelBorder}`,
    borderRadius: 2,
    p: 1.5,
  };

  return (
    <Dialog
      open={open}
      onClose={handleCancel}
      maxWidth="md"
      fullWidth
      PaperProps={{ sx: DIALOG_PAPER_SX }}
    >
      <DialogTitle sx={DIALOG_TITLE_SX}>
        <DialogHeaderBlock title={title} subtitle={subtitle} />
      </DialogTitle>
      <DialogContent
        sx={{ ...DIALOG_CONTENT_SX, display: "flex", flexDirection: "column", gap: 2 }}
      >
        {fetchingDetail && (
          <Box sx={{ display: "flex", alignItems: "center", gap: 1, color: MAGIC_UI.textMuted }}>
            <CircularProgress size={18} sx={{ color: MAGIC_UI.accentA }} />
            <Typography variant="body2">Loading credential details…</Typography>
          </Box>
        )}
        {error && (
          <Box
            sx={{
              bgcolor: "rgba(255,124,124,0.1)",
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              borderRadius: 2,
              p: 1.5,
            }}
          >
            <Typography variant="body2" sx={{ color: MAGIC_UI.danger }}>{error}</Typography>
          </Box>
        )}
        {isGitHubToken && (
          <Box sx={githubHintStyle}>
            <Typography variant="body2" sx={{ color: MAGIC_UI.textBright, fontWeight: 600, mb: 0.6 }}>
              GitHub Personal Access Token
            </Typography>
            <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted, lineHeight: 1.55 }}>
              Using a GitHub Personal Access Token raises API rate limits from 60/hr to 5,000/hr. Generate one at{" "}
              <Link
                href="https://github.com/settings/tokens"
                target="_blank"
                rel="noopener noreferrer"
                sx={{ color: "#7db7ff" }}
              >
                github.com/settings/tokens
              </Link>{" "}
              under <Box component="span" sx={{ color: MAGIC_UI.textBright }}>Personal Access Tokens -> Tokens (Classic)</Box>.
            </Typography>
            <Typography variant="body2" sx={{ color: MAGIC_UI.textMuted, lineHeight: 1.55, mt: 1 }}>
              <Box component="span" sx={{ fontWeight: 600, color: MAGIC_UI.textBright }}>Note:</Box>{" "}
              <Box component="code" sx={{ fontSize: "0.84rem", mx: 0.35 }}>Borealis Automation Platform</Box>
              <Box component="span" sx={{ fontWeight: 600, color: MAGIC_UI.textBright, ml: 1.25 }}>Scope:</Box>{" "}
              <Box component="code" sx={{ fontSize: "0.84rem", mx: 0.35 }}>public_repo</Box>
              <Box component="span" sx={{ fontWeight: 600, color: MAGIC_UI.textBright, ml: 1.25 }}>Expiration:</Box>{" "}
              <Box component="code" sx={{ fontSize: "0.84rem", mx: 0.35 }}>No Expiration</Box>
            </Typography>
          </Box>
        )}
        {isGitHubToken ? (
          <>
            <TextField
              label="Name"
              value={form.name}
              required
              InputProps={{ readOnly: true }}
              sx={INPUT_SX}
            />
            <TextField
              label="Description"
              value={form.description}
              multiline
              minRows={2}
              InputProps={{ readOnly: true }}
              sx={INPUT_SX}
            />
            <TextField
              label="Personal Access Token"
              type="password"
              value={form.password}
              onChange={updateField("password")}
              disabled={disableSave}
              sx={INPUT_SX}
            />
          </>
        ) : (
          <>
            <TextField
              label="Name"
              value={form.name}
              onChange={updateField("name")}
              required
              disabled={disableSave}
              sx={INPUT_SX}
            />
            <TextField
              label="Description"
              value={form.description}
              onChange={updateField("description")}
              disabled={disableSave}
              multiline
              minRows={2}
              sx={INPUT_SX}
            />
            <Box sx={{ display: "flex", flexWrap: "wrap", gap: 2 }}>
              <FormControl sx={{ minWidth: 220, ...DIALOG_SELECT_SX }} size="small" disabled={disableSave}>
                <InputLabel sx={{ color: MAGIC_UI.textMuted }}>Site</InputLabel>
                <Select
                  value={form.site_id}
                  label="Site"
                  onChange={updateField("site_id")}
                  sx={INPUT_SX}
                >
                  <MenuItem value="">Global</MenuItem>
                  {sites.map((site) => (
                    <MenuItem key={site.id} value={String(site.id)}>
                      {site.name}
                    </MenuItem>
                  ))}
                </Select>
              </FormControl>
              <FormControl sx={{ minWidth: 180, ...DIALOG_SELECT_SX }} size="small" disabled={disableSave}>
                <InputLabel sx={{ color: MAGIC_UI.textMuted }}>Credential Type</InputLabel>
                <Select
                  value={form.credential_type}
                  label="Credential Type"
                  onChange={updateField("credential_type")}
                  sx={INPUT_SX}
                >
                  {CREDENTIAL_TYPES.map((opt) => (
                    <MenuItem key={opt.value} value={opt.value}>{opt.label}</MenuItem>
                  ))}
                </Select>
              </FormControl>
              <FormControl sx={{ minWidth: 180, ...DIALOG_SELECT_SX }} size="small" disabled={disableSave}>
                <InputLabel sx={{ color: MAGIC_UI.textMuted }}>Connection</InputLabel>
                <Select
                  value={form.connection_type}
                  label="Connection"
                  onChange={updateField("connection_type")}
                  sx={INPUT_SX}
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
              sx={INPUT_SX}
            />
            <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
              <TextField
                label={tokenFieldLabel}
                type="password"
                value={form.password}
                onChange={updateField("password")}
                disabled={disableSave}
                sx={{ flex: 1, ...INPUT_SX }}
              />
              {isEdit && currentCredentialFlags.hasPassword && !passwordDirty && !clearPassword && (
                <Tooltip title="Clear stored password">
                  <IconButton size="small" onClick={() => setClearPassword(true)} sx={{ color: MAGIC_UI.danger }}>
                    <ClearIcon fontSize="small" />
                  </IconButton>
                </Tooltip>
              )}
            </Box>
            {isEdit && currentCredentialFlags.hasPassword && !passwordDirty && !clearPassword && (
              <Typography sx={helperStyle}>Stored password will remain unless you change or clear it.</Typography>
            )}
            {clearPassword && (
              <Typography sx={{ ...helperStyle, color: MAGIC_UI.danger }}>Password will be removed when saving.</Typography>
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
                  ...INPUT_SX,
                  "& .MuiOutlinedInput-input": { fontFamily: "monospace" },
                }}
              />
              <Button
                variant="outlined"
                component="label"
                startIcon={<UploadIcon />}
                disabled={disableSave}
                sx={{ alignSelf: "center", ...OUTLINE_BUTTON_SX }}
              >
                Upload
                <input type="file" hidden accept=".pem,.key,.txt" onChange={handlePrivateKeyUpload} />
              </Button>
              {isEdit && currentCredentialFlags.hasPrivateKey && !privateKeyDirty && !clearPrivateKey && (
                <Tooltip title="Clear stored private key">
                  <IconButton size="small" onClick={() => setClearPrivateKey(true)} sx={{ color: MAGIC_UI.danger }}>
                    <ClearIcon fontSize="small" />
                  </IconButton>
                </Tooltip>
              )}
            </Box>
            {isEdit && currentCredentialFlags.hasPrivateKey && !privateKeyDirty && !clearPrivateKey ? (
              <Typography sx={helperStyle}>Private key is stored. Upload or paste a new one to replace, or clear it.</Typography>
            ) : null}
            {clearPrivateKey && (
              <Typography sx={{ ...helperStyle, color: MAGIC_UI.danger }}>Private key will be removed when saving.</Typography>
            )}

            <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
              <TextField
                label="Private Key Passphrase"
                type="password"
                value={form.private_key_passphrase}
                onChange={updateField("private_key_passphrase")}
                disabled={disableSave}
                sx={{ flex: 1, ...INPUT_SX }}
              />
              {isEdit && currentCredentialFlags.hasPrivateKeyPassphrase && !passphraseDirty && !clearPassphrase && (
                <Tooltip title="Clear stored passphrase">
                  <IconButton size="small" onClick={() => setClearPassphrase(true)} sx={{ color: MAGIC_UI.danger }}>
                    <ClearIcon fontSize="small" />
                  </IconButton>
                </Tooltip>
              )}
            </Box>
            {isEdit && currentCredentialFlags.hasPrivateKeyPassphrase && !passphraseDirty && !clearPassphrase ? (
              <Typography sx={helperStyle}>A passphrase is stored for this key.</Typography>
            ) : null}
            {clearPassphrase && (
              <Typography sx={{ ...helperStyle, color: MAGIC_UI.danger }}>Key passphrase will be removed when saving.</Typography>
            )}

            <Box sx={{ display: "flex", gap: 2, flexWrap: "wrap" }}>
              <FormControl sx={{ minWidth: 180, ...DIALOG_SELECT_SX }} size="small" disabled={disableSave}>
                <InputLabel sx={{ color: MAGIC_UI.textMuted }}>Privilege Escalation</InputLabel>
                <Select
                  value={form.become_method}
                  label="Privilege Escalation"
                  onChange={updateField("become_method")}
                  sx={INPUT_SX}
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
                sx={{ flex: 1, minWidth: 200, ...INPUT_SX }}
              />
            </Box>
            <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
              <TextField
                label="Escalation Password"
                type="password"
                value={form.become_password}
                onChange={updateField("become_password")}
                disabled={disableSave}
                sx={{ flex: 1, ...INPUT_SX }}
              />
              {isEdit && currentCredentialFlags.hasBecomePassword && !becomePasswordDirty && !clearBecomePassword && (
                <Tooltip title="Clear stored escalation password">
                  <IconButton size="small" onClick={() => setClearBecomePassword(true)} sx={{ color: MAGIC_UI.danger }}>
                    <ClearIcon fontSize="small" />
                  </IconButton>
                </Tooltip>
              )}
            </Box>
            {isEdit && currentCredentialFlags.hasBecomePassword && !becomePasswordDirty && !clearBecomePassword ? (
              <Typography sx={helperStyle}>Escalation password is stored.</Typography>
            ) : null}
            {clearBecomePassword && (
              <Typography sx={{ ...helperStyle, color: MAGIC_UI.danger }}>Escalation password will be removed when saving.</Typography>
            )}
          </>
        )}
      </DialogContent>
      <DialogActions sx={DIALOG_ACTIONS_SX}>
        <Button onClick={handleCancel} sx={OUTLINE_BUTTON_SX} disabled={loading}>
          Cancel
        </Button>
        <Button
          onClick={handleSave}
          variant="contained"
          sx={DIALOG_PRIMARY_BUTTON_SX}
          disabled={disableSave}
        >
          {loading ? <CircularProgress size={18} sx={{ color: "#041224" }} /> : saveLabel}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
