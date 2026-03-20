import React, { useEffect, useMemo, useState } from "react";
import {
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  InputAdornment,
  TextField,
  Typography,
} from "@mui/material";
import VpnKeyIcon from "@mui/icons-material/VpnKeyRounded";
import VisibilityIcon from "@mui/icons-material/Visibility";
import VisibilityOffIcon from "@mui/icons-material/VisibilityOff";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BODY_TEXT_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_INPUT_SX,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "../DialogStyles.jsx";

function dialogCopy(mode, source) {
  if (mode === "setup") {
    return {
      title: "Setup Aegis Cipher",
      subtitle: "Configure the Aegis Cipher to encrypt database stored credentials and the GitHub API token at rest.",
      body:
        "The Aegis Cipher encrypts all existing protected secrets and decrypts secrets on-the-fly in-memory for the current Engine's lifetime.",
      confirmLabel: "Setup Aegis Cipher",
    };
  }
  if (mode === "rotate") {
    return {
      title: "Rotate Aegis Cipher",
      subtitle: "Provide the current cipher, then choose a new one to re-encrypt stored credentials and the GitHub token.",
      body:
        "Rotation re-encrypts all protected secrets in one step and keeps the Engine unlocked with the new cipher for the rest of this process lifetime.",
      confirmLabel: "Rotate Aegis Cipher",
    };
  }
  if (source === "login") {
    return {
      title: "Enter Aegis Cipher",
      subtitle:
        "Please enter the Aegis Cipher to unlock stored credentials and other protected secrets such as the GitHub token. Protected secrets are encrypted at rest, and after a restart, re-entering the Aegis Cipher is required.",
      body:
        "Canceling keeps you signed in, but credential-backed jobs and other protected-secret workflows stay disabled until the Aegis Cipher is entered.",
      confirmLabel: "Unlock Protected Secrets",
    };
  }
  return {
    title: "Enter Aegis Cipher",
    subtitle: "Enter the Aegis Cipher to unlock stored credentials and the GitHub token for this running Engine process.",
    body:
      "After a restart, the Engine relocks and the Aegis Cipher must be entered again before protected secrets can be used.",
    confirmLabel: "Enter Aegis Cipher",
  };
}

export default function AegisCipherDialog({
  open,
  mode = "unlock",
  source = "credentials",
  onClose,
  onCompleted,
}) {
  const [cipher, setCipher] = useState("");
  const [currentCipher, setCurrentCipher] = useState("");
  const [newCipher, setNewCipher] = useState("");
  const [confirmCipher, setConfirmCipher] = useState("");
  const [showCipherValues, setShowCipherValues] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const copy = useMemo(() => dialogCopy(mode, source), [mode, source]);
  const requiresConfirmation = mode === "setup" || mode === "rotate";
  const primaryCipherValue = mode === "rotate" ? newCipher : cipher;
  const confirmationMissing = requiresConfirmation && !confirmCipher;
  const confirmationMismatch = requiresConfirmation && confirmCipher !== "" && primaryCipherValue !== confirmCipher;
  const confirmationHint = confirmationMismatch
    ? "The new Aegis Cipher does not match."
    : "Re-enter the new Aegis Cipher exactly as typed above.";

  useEffect(() => {
    if (!open) return;
    setCipher("");
    setCurrentCipher("");
    setNewCipher("");
    setConfirmCipher("");
    setShowCipherValues(false);
    setLoading(false);
    setError("");
  }, [mode, open, source]);

  const endpoint = mode === "setup" ? "/api/aegis/setup" : mode === "rotate" ? "/api/aegis/rotate" : "/api/aegis/unlock";

  const handleSubmit = async (event) => {
    event?.preventDefault?.();
    if (requiresConfirmation && confirmationMissing) {
      setError("Please confirm the new Aegis Cipher before continuing.");
      return;
    }
    if (confirmationMismatch) {
      setError("The Aegis Cipher confirmation does not match.");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const payload = mode === "rotate"
        ? { current_cipher: currentCipher, new_cipher: newCipher }
        : { cipher };
      const resp = await fetch(endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify(payload),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) {
        throw new Error(data?.message || data?.error || `Request failed (${resp.status})`);
      }
      onCompleted && onCompleted(data);
    } catch (err) {
      setError(String(err?.message || err));
    } finally {
      setLoading(false);
    }
  };

  const toggleCipherVisibility = () => {
    setShowCipherValues((prev) => !prev);
  };

  const cipherInputType = showCipherValues ? "text" : "password";
  const cipherAdornment = (
    <InputAdornment position="end">
      <IconButton
        edge="end"
        onClick={toggleCipherVisibility}
        onMouseDown={(event) => event.preventDefault()}
        aria-label={showCipherValues ? "Hide Aegis Cipher" : "Show Aegis Cipher"}
        sx={{ color: "rgba(148,163,184,0.82)" }}
      >
        {showCipherValues ? <VisibilityOffIcon /> : <VisibilityIcon />}
      </IconButton>
    </InputAdornment>
  );

  const submitDisabled = loading || (
    mode === "rotate"
      ? !currentCipher || !newCipher || confirmationMissing || confirmationMismatch
      : !cipher || (requiresConfirmation && (confirmationMissing || confirmationMismatch))
  );

  return (
    <Dialog
      open={open}
      onClose={() => !loading && onClose && onClose("cancel")}
      maxWidth="sm"
      fullWidth
      PaperProps={{ sx: DIALOG_PAPER_SX }}
    >
      <DialogTitle sx={DIALOG_TITLE_SX}>
        <DialogHeaderBlock title={copy.title} subtitle={copy.subtitle} />
      </DialogTitle>
      <DialogContent sx={{ ...DIALOG_CONTENT_SX, display: "flex", flexDirection: "column", gap: 2 }}>
        <Box
          component="form"
          onSubmit={handleSubmit}
          sx={{ display: "flex", flexDirection: "column", gap: 2 }}
        >
          <Typography variant="body2" sx={DIALOG_BODY_TEXT_SX}>
            {copy.body}
          </Typography>

          {mode === "rotate" ? (
            <>
              <TextField
                autoFocus
                type={cipherInputType}
                label="Current Aegis Cipher"
                value={currentCipher}
                onChange={(event) => {
                  setCurrentCipher(event.target.value);
                  setError("");
                }}
                fullWidth
                sx={DIALOG_INPUT_SX}
                InputProps={{ endAdornment: cipherAdornment }}
              />
              <TextField
                type={cipherInputType}
                label="New Aegis Cipher"
                value={newCipher}
                onChange={(event) => {
                  setNewCipher(event.target.value);
                  setError("");
                }}
                fullWidth
                sx={DIALOG_INPUT_SX}
                InputProps={{ endAdornment: cipherAdornment }}
              />
              <TextField
                type={cipherInputType}
                label="Confirm New Aegis Cipher"
                value={confirmCipher}
                onChange={(event) => {
                  setConfirmCipher(event.target.value);
                  setError("");
                }}
                fullWidth
                sx={DIALOG_INPUT_SX}
                error={confirmationMismatch}
                InputProps={{ endAdornment: cipherAdornment }}
              />
              <Typography
                variant="body2"
                sx={confirmationMismatch ? { color: "#ff9aa5", mt: -1.1 } : { ...DIALOG_BODY_TEXT_SX, mt: -1.1 }}
              >
                {confirmationHint}
              </Typography>
            </>
          ) : (
            <>
              <TextField
                autoFocus
                type={cipherInputType}
                label={mode === "setup" ? "New Aegis Cipher" : "Aegis Cipher"}
                value={cipher}
                onChange={(event) => {
                  setCipher(event.target.value);
                  setError("");
                }}
                fullWidth
                sx={DIALOG_INPUT_SX}
                InputProps={{ endAdornment: cipherAdornment }}
              />
              {requiresConfirmation ? (
                <>
                  <TextField
                    type={cipherInputType}
                    label="Confirm New Aegis Cipher"
                    value={confirmCipher}
                    onChange={(event) => {
                      setConfirmCipher(event.target.value);
                      setError("");
                    }}
                    fullWidth
                    sx={DIALOG_INPUT_SX}
                    error={confirmationMismatch}
                    InputProps={{ endAdornment: cipherAdornment }}
                  />
                  <Typography
                    variant="body2"
                    sx={confirmationMismatch ? { color: "#ff9aa5", mt: -1.1 } : { ...DIALOG_BODY_TEXT_SX, mt: -1.1 }}
                  >
                    {confirmationHint}
                  </Typography>
                </>
              ) : null}
            </>
          )}

          {error ? (
            <Box
              sx={{
                borderRadius: 2,
                border: "1px solid rgba(244, 63, 94, 0.26)",
                background: "rgba(44, 8, 22, 0.38)",
                px: 1.5,
                py: 1.2,
              }}
            >
              <Typography variant="body2" sx={{ color: "#ff9aa5" }}>
                {error}
              </Typography>
            </Box>
          ) : null}

          <Box sx={{ display: "none" }}>
            <button type="submit" />
          </Box>
        </Box>
      </DialogContent>
      <DialogActions sx={DIALOG_ACTIONS_SX}>
        <Button onClick={() => onClose && onClose("cancel")} disabled={loading} sx={DIALOG_BUTTON_SX}>
          Cancel
        </Button>
        <Button
          onClick={handleSubmit}
          disabled={submitDisabled}
          startIcon={!loading ? <VpnKeyIcon /> : null}
          sx={DIALOG_PRIMARY_BUTTON_SX}
        >
          {copy.confirmLabel}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
