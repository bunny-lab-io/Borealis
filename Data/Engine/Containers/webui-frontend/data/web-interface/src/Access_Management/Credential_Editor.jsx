import React, { useEffect, useMemo, useState } from "react";
import {
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  FormControl,
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
  warning: "#f0c36d",
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

const STACKED_FIELD_SX = {
  ...INPUT_SX,
  "& .MuiInputLabel-root": {
    display: "none",
  },
};

const STACKED_SELECT_SX = {
  ...STACKED_FIELD_SX,
  "& .MuiSelect-select": {
    display: "flex",
    alignItems: "center",
    minHeight: 42,
    padding: "0 13px !important",
    boxSizing: "border-box",
  },
};

const SECTION_CARD_SX = {
  display: "flex",
  flexDirection: "column",
  gap: 1.8,
  borderRadius: 3,
  border: "1px solid rgba(148,163,184,0.16)",
  background:
    "linear-gradient(180deg, rgba(10,16,32,0.72) 0%, rgba(5,10,24,0.48) 100%)",
  px: 2.25,
  py: 2.1,
};

const SECTION_HEADER_SX = {
  display: "flex",
  alignItems: "flex-start",
  justifyContent: "space-between",
  gap: 1.5,
  flexWrap: "wrap",
};

const SECTION_TITLE_SX = {
  fontSize: "0.78rem",
  fontWeight: 700,
  letterSpacing: 0.9,
  textTransform: "uppercase",
  color: MAGIC_UI.textBright,
};

const SECTION_SUBTITLE_SX = {
  mt: 0.45,
  fontSize: "0.8rem",
  lineHeight: 1.45,
  color: MAGIC_UI.textMuted,
  maxWidth: "72ch",
};

const SECTION_GRID_SX = {
  display: "grid",
  gridTemplateColumns: {
    xs: "1fr",
    md: "repeat(12, minmax(0, 1fr))",
  },
  gap: 2,
  alignItems: "start",
};

const FIELD_STACK_SX = {
  display: "flex",
  flexDirection: "column",
  gap: 0.7,
  minWidth: 0,
};

const FIELD_LABEL_SX = {
  fontSize: "0.76rem",
  lineHeight: 1,
  fontWeight: 600,
  letterSpacing: 0.36,
  textTransform: "uppercase",
  color: "rgba(226,232,240,0.9)",
};

const RESET_FIELD_SX = {
  "& .MuiInputLabel-root": {
    color: MAGIC_UI.warning,
  },
  "& .MuiOutlinedInput-root, & .MuiInputBase-root": {
    backgroundColor: "rgba(240,195,109,0.06)",
    "& fieldset": {
      borderColor: "rgba(240,195,109,0.55)",
    },
    "&:hover fieldset": {
      borderColor: "rgba(240,195,109,0.82)",
    },
    "&.Mui-focused fieldset": {
      borderColor: MAGIC_UI.warning,
    },
  },
};

const RESET_FIELD_LABELS = {
  password: "password",
  private_key: "SSH private key",
  private_key_passphrase: "private key passphrase",
  become_password: "escalation password",
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

function normalizeLostSecretFields(credential, isGitHubToken) {
  if (isGitHubToken) {
    return Boolean(credential?.reset_required) ? ["password"] : [];
  }
  const explicit = Array.isArray(credential?.lost_secret_fields)
    ? credential.lost_secret_fields
    : credential?.metadata?.aegis_lost_secret_fields;
  if (!Array.isArray(explicit)) return [];
  return explicit
    .map((value) => String(value || "").trim().toLowerCase())
    .filter(Boolean);
}

function joinNaturalLanguage(parts) {
  if (!parts.length) return "";
  if (parts.length === 1) return parts[0];
  if (parts.length === 2) return `${parts[0]} and ${parts[1]}`;
  return `${parts.slice(0, -1).join(", ")}, and ${parts[parts.length - 1]}`;
}

function EditorSection({ title, subtitle, action = null, children }) {
  return (
    <Box sx={SECTION_CARD_SX}>
      <Box sx={SECTION_HEADER_SX}>
        <Box sx={{ minWidth: 0 }}>
          <Typography sx={SECTION_TITLE_SX}>{title}</Typography>
          {subtitle ? <Typography sx={SECTION_SUBTITLE_SX}>{subtitle}</Typography> : null}
        </Box>
        {action ? <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>{action}</Box> : null}
      </Box>
      {children}
    </Box>
  );
}

function EditorField({ label, sx, children }) {
  return (
    <Box sx={[FIELD_STACK_SX, sx]}>
      <Typography sx={FIELD_LABEL_SX}>{label}</Typography>
      {children}
    </Box>
  );
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
  const resetHelperStyle = { fontSize: 12, color: MAGIC_UI.warning, mt: 0.5 };
  const secretResetRequired = useMemo(() => {
    if (isGitHubToken) {
      return Boolean(credential?.reset_required);
    }
    if (Boolean(credential?.secret_reset_required)) {
      return true;
    }
    const state = String(credential?.metadata?.aegis_secret_state || "").trim().toLowerCase();
    return state === "reset_required" && normalizeLostSecretFields(credential, isGitHubToken).length > 0;
  }, [credential, isGitHubToken]);
  const lostSecretFields = useMemo(
    () => normalizeLostSecretFields(credential, isGitHubToken),
    [credential, isGitHubToken]
  );
  const resetSummary = useMemo(() => {
    if (!secretResetRequired) return "";
    if (isGitHubToken) {
      return "Aegis Cipher force reset removed the stored GitHub Personal Access Token. Re-enter the token below to restore Borealis GitHub access.";
    }
    const labels = lostSecretFields
      .map((field) => RESET_FIELD_LABELS[field])
      .filter(Boolean);
    if (!labels.length) {
      return "Aegis Cipher force reset removed one or more stored secret fields for this credential. Re-enter the highlighted values or clear them intentionally to resolve the warning.";
    }
    return `Aegis Cipher force reset removed the stored ${joinNaturalLanguage(labels)} for this credential. Re-enter the highlighted values or clear them intentionally to resolve the warning.`;
  }, [isGitHubToken, lostSecretFields, secretResetRequired]);

  useEffect(() => {
    if (!open || isGitHubToken) {
      setSites([]);
      return;
    }
    let canceled = false;
    (async () => {
      try {
        const resp = await fetch("/api/sites", { cache: "no-store", credentials: "include" });
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
    let canceled = false;
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
      if (!canceled) {
        setForm(next);
        setFetchingDetail(false);
      }
      return () => {
        canceled = true;
      };
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
        if (!canceled) {
          setForm(next);
        }
      };

      if (credential) {
        applyData(credential);
      }
      setFetchingDetail(true);
      (async () => {
        try {
          const resp = await fetch(`/api/credentials/${credentialId}`, {
            cache: "no-store",
            credentials: "include",
          });
          if (!resp.ok) return;
          const data = await resp.json();
          applyData(data?.credential || {});
        } catch {
          /* ignore */
        } finally {
          if (!canceled) {
            setFetchingDetail(false);
          }
        }
      })();
    } else {
      setForm(emptyForm());
      setFetchingDetail(false);
    }
    return () => {
      canceled = true;
    };
  }, [open, isEdit, credentialId, credential, isGitHubToken]);

  const currentCredentialFlags = useMemo(() => ({
    hasPassword: isGitHubToken ? Boolean(credential?.token) : Boolean(credential?.has_password),
    hasPrivateKey: isGitHubToken ? false : Boolean(credential?.has_private_key),
    hasPrivateKeyPassphrase: isGitHubToken ? false : Boolean(credential?.has_private_key_passphrase),
    hasBecomePassword: isGitHubToken ? false : Boolean(credential?.has_become_password)
  }), [credential, isGitHubToken]);

  const disableSave = loading || fetchingDetail;
  const fieldLostInReset = (fieldName) => lostSecretFields.includes(fieldName);
  const inputSxForField = (fieldName, extra = {}) => [INPUT_SX, extra, fieldLostInReset(fieldName) ? RESET_FIELD_SX : null];
  const stackedInputSxForField = (fieldName, extra = {}) => [
    STACKED_FIELD_SX,
    extra,
    fieldLostInReset(fieldName) ? RESET_FIELD_SX : null,
  ];
  const resetFieldHelper = "Lost during Aegis Cipher force reset. Re-enter or clear intentionally to resolve this warning.";

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
          credentials: "include",
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
      scroll="paper"
      PaperProps={{
        sx: {
          ...DIALOG_PAPER_SX,
          display: "flex",
          flexDirection: "column",
          maxHeight: "min(90vh, 1040px)",
        },
      }}
    >
      <DialogTitle sx={DIALOG_TITLE_SX}>
        <DialogHeaderBlock title={title} subtitle={subtitle} />
      </DialogTitle>
      <DialogContent
        sx={{
          ...DIALOG_CONTENT_SX,
          display: "flex",
          flexDirection: "column",
          gap: 2,
          flex: "1 1 auto",
          minHeight: 0,
          overflowY: "auto",
          pr: 2.5,
        }}
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
        {secretResetRequired && (
          <Box
            sx={{
              bgcolor: "rgba(240,195,109,0.08)",
              border: "1px solid rgba(240,195,109,0.32)",
              borderRadius: 2,
              p: 1.5,
            }}
          >
            <Typography variant="body2" sx={{ color: MAGIC_UI.warning, lineHeight: 1.55 }}>
              {resetSummary}
            </Typography>
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
              under <Box component="span" sx={{ color: MAGIC_UI.textBright }}>Personal Access Tokens {"->"} Tokens (Classic)</Box>.
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
              sx={inputSxForField("password")}
            />
            {fieldLostInReset("password") && (
              <Typography sx={resetHelperStyle}>{resetFieldHelper}</Typography>
            )}
          </>
        ) : (
          <>
            <EditorSection
              title="Identity"
              subtitle="Name the credential and choose where it can be reused across Borealis."
            >
              <Box sx={SECTION_GRID_SX}>
                <EditorField sx={{ gridColumn: { xs: "1 / -1", md: "span 8" } }} label="Credential Name *">
                  <TextField
                    value={form.name}
                    onChange={updateField("name")}
                    required
                    disabled={disableSave}
                    sx={STACKED_FIELD_SX}
                  />
                </EditorField>
                <EditorField sx={{ gridColumn: { xs: "1 / -1", md: "span 4" } }} label="Site">
                  <FormControl sx={[DIALOG_SELECT_SX, { minWidth: 0 }]} size="small" disabled={disableSave}>
                    <Select
                      value={form.site_id}
                      onChange={updateField("site_id")}
                      displayEmpty
                      sx={STACKED_SELECT_SX}
                    >
                      <MenuItem value="">Global</MenuItem>
                      {sites.map((site) => (
                        <MenuItem key={site.id} value={String(site.id)}>
                          {site.name}
                        </MenuItem>
                      ))}
                    </Select>
                  </FormControl>
                </EditorField>
                <EditorField sx={{ gridColumn: "1 / -1" }} label="Description">
                  <TextField
                    value={form.description}
                    onChange={updateField("description")}
                    disabled={disableSave}
                    multiline
                    minRows={2}
                    sx={STACKED_FIELD_SX}
                  />
                </EditorField>
              </Box>
            </EditorSection>

            <EditorSection
              title="Connection"
              subtitle="Set the transport and primary account Borealis will use to open the session."
            >
              <Box sx={SECTION_GRID_SX}>
                <EditorField sx={{ gridColumn: { xs: "1 / -1", sm: "span 3" } }} label="Credential Type">
                  <FormControl sx={[DIALOG_SELECT_SX, { minWidth: 0 }]} size="small" disabled={disableSave}>
                    <Select
                      value={form.credential_type}
                      onChange={updateField("credential_type")}
                      sx={STACKED_SELECT_SX}
                    >
                      {CREDENTIAL_TYPES.map((opt) => (
                        <MenuItem key={opt.value} value={opt.value}>{opt.label}</MenuItem>
                      ))}
                    </Select>
                  </FormControl>
                </EditorField>
                <EditorField sx={{ gridColumn: { xs: "1 / -1", sm: "span 3" } }} label="Connection">
                  <FormControl sx={[DIALOG_SELECT_SX, { minWidth: 0 }]} size="small" disabled={disableSave}>
                    <Select
                      value={form.connection_type}
                      onChange={updateField("connection_type")}
                      sx={STACKED_SELECT_SX}
                    >
                      {CONNECTION_TYPES.map((opt) => (
                        <MenuItem key={opt.value} value={opt.value}>{opt.label}</MenuItem>
                      ))}
                    </Select>
                  </FormControl>
                </EditorField>
                <EditorField sx={{ gridColumn: { xs: "1 / -1", md: "span 6" } }} label="Username">
                  <TextField
                    value={form.username}
                    onChange={updateField("username")}
                    disabled={disableSave}
                    sx={STACKED_FIELD_SX}
                  />
                </EditorField>
                <EditorField sx={{ gridColumn: "1 / -1" }} label={tokenFieldLabel}>
                  <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
                    <TextField
                      type="password"
                      value={form.password}
                      onChange={updateField("password")}
                      disabled={disableSave}
                      sx={stackedInputSxForField("password", { flex: 1 })}
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
                  {fieldLostInReset("password") && !clearPassword && (
                    <Typography sx={resetHelperStyle}>{resetFieldHelper}</Typography>
                  )}
                  {clearPassword && (
                    <Typography sx={{ ...helperStyle, color: MAGIC_UI.danger }}>Password will be removed when saving.</Typography>
                  )}
                </EditorField>
              </Box>
            </EditorSection>

            <EditorSection
              title="SSH Key Material"
              subtitle="Optional key-based authentication details for SSH credentials."
              action={
                <>
                  <Button
                    variant="outlined"
                    component="label"
                    startIcon={<UploadIcon />}
                    disabled={disableSave}
                    sx={OUTLINE_BUTTON_SX}
                  >
                    Upload Key
                    <input type="file" hidden accept=".pem,.key,.txt" onChange={handlePrivateKeyUpload} />
                  </Button>
                  {isEdit && currentCredentialFlags.hasPrivateKey && !privateKeyDirty && !clearPrivateKey ? (
                    <Tooltip title="Clear stored private key">
                      <IconButton size="small" onClick={() => setClearPrivateKey(true)} sx={{ color: MAGIC_UI.danger }}>
                        <ClearIcon fontSize="small" />
                      </IconButton>
                    </Tooltip>
                  ) : null}
                </>
              }
            >
              <Box sx={SECTION_GRID_SX}>
                <EditorField sx={{ gridColumn: "1 / -1" }} label="SSH Private Key">
                  <TextField
                    value={form.private_key}
                    onChange={updateField("private_key")}
                    disabled={disableSave}
                    multiline
                    minRows={4}
                    maxRows={12}
                    sx={stackedInputSxForField("private_key", {
                      "& .MuiOutlinedInput-input": { fontFamily: "monospace" },
                    })}
                  />
                  {isEdit && currentCredentialFlags.hasPrivateKey && !privateKeyDirty && !clearPrivateKey ? (
                    <Typography sx={helperStyle}>Private key is stored. Upload or paste a new one to replace, or clear it.</Typography>
                  ) : null}
                  {fieldLostInReset("private_key") && !clearPrivateKey ? (
                    <Typography sx={resetHelperStyle}>{resetFieldHelper}</Typography>
                  ) : null}
                  {clearPrivateKey && (
                    <Typography sx={{ ...helperStyle, color: MAGIC_UI.danger }}>Private key will be removed when saving.</Typography>
                  )}
                </EditorField>
                <EditorField sx={{ gridColumn: "1 / -1" }} label="Private Key Passphrase">
                  <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
                    <TextField
                      type="password"
                      value={form.private_key_passphrase}
                      onChange={updateField("private_key_passphrase")}
                      disabled={disableSave}
                      sx={stackedInputSxForField("private_key_passphrase", { flex: 1 })}
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
                  {fieldLostInReset("private_key_passphrase") && !clearPassphrase ? (
                    <Typography sx={resetHelperStyle}>{resetFieldHelper}</Typography>
                  ) : null}
                  {clearPassphrase && (
                    <Typography sx={{ ...helperStyle, color: MAGIC_UI.danger }}>Key passphrase will be removed when saving.</Typography>
                  )}
                </EditorField>
              </Box>
            </EditorSection>

            <EditorSection
              title="Privilege Escalation"
              subtitle="Optional secondary identity used after the initial connection is established."
            >
              <Box sx={SECTION_GRID_SX}>
                <EditorField sx={{ gridColumn: { xs: "1 / -1", md: "span 4" } }} label="Privilege Escalation Method">
                  <FormControl sx={[DIALOG_SELECT_SX, { minWidth: 0 }]} size="small" disabled={disableSave}>
                    <Select
                      value={form.become_method}
                      onChange={updateField("become_method")}
                      displayEmpty
                      sx={STACKED_SELECT_SX}
                    >
                      {BECOME_METHODS.map((opt) => (
                        <MenuItem key={opt.value || "none"} value={opt.value}>{opt.label}</MenuItem>
                      ))}
                    </Select>
                  </FormControl>
                </EditorField>
                <EditorField sx={{ gridColumn: { xs: "1 / -1", md: "span 8" } }} label="Escalation Username">
                  <TextField
                    value={form.become_username}
                    onChange={updateField("become_username")}
                    placeholder="Optional"
                    disabled={disableSave}
                    sx={STACKED_FIELD_SX}
                  />
                </EditorField>
                <EditorField sx={{ gridColumn: "1 / -1" }} label="Escalation Password">
                  <Box sx={{ display: "flex", alignItems: "center", gap: 1 }}>
                    <TextField
                      type="password"
                      value={form.become_password}
                      onChange={updateField("become_password")}
                      disabled={disableSave}
                      sx={stackedInputSxForField("become_password", { flex: 1 })}
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
                  {fieldLostInReset("become_password") && !clearBecomePassword ? (
                    <Typography sx={resetHelperStyle}>{resetFieldHelper}</Typography>
                  ) : null}
                  {clearBecomePassword && (
                    <Typography sx={{ ...helperStyle, color: MAGIC_UI.danger }}>Escalation password will be removed when saving.</Typography>
                  )}
                </EditorField>
              </Box>
            </EditorSection>
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
