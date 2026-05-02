import React, { useEffect, useState } from "react";
import {
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
  TextField,
  Button,
  MenuItem,
  Typography
} from "@mui/material";
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

const TYPE_OPTIONS = [
  { value: "ssh", label: "SSH" },
  { value: "winrm", label: "WinRM" }
];

const initialForm = {
  hostname: "",
  address: "",
  description: "",
  operating_system: ""
};

export default function AddDevice({
  open,
  onClose,
  defaultType = null,
  onCreated
}) {
  const [type, setType] = useState(defaultType || "ssh");
  const [form, setForm] = useState(initialForm);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (open) {
      setType(defaultType || "ssh");
      setForm(initialForm);
      setError("");
    }
  }, [open, defaultType]);

  const handleClose = () => {
    if (submitting) return;
    onClose && onClose();
  };

  const handleChange = (field) => (event) => {
    const value = event.target.value;
    setForm((prev) => ({ ...prev, [field]: value }));
  };

  const handleSubmit = async () => {
    if (submitting) return;
    const trimmedHostname = form.hostname.trim();
    const trimmedAddress = form.address.trim();
    if (!trimmedHostname) {
      setError("Hostname is required.");
      return;
    }
    if (!type) {
      setError("Select a device type.");
      return;
    }
    if (!trimmedAddress) {
      setError("Address is required.");
      return;
    }
    setSubmitting(true);
    setError("");
    const payload = {
      hostname: trimmedHostname,
      address: trimmedAddress,
      description: form.description.trim(),
      operating_system: form.operating_system.trim()
    };
    const apiBase = type === "winrm" ? "/api/winrm_devices" : "/api/ssh_devices";
    try {
      const resp = await fetch(apiBase, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok) throw new Error(data?.error || `HTTP ${resp.status}`);
      onCreated && onCreated(data.device || null);
      onClose && onClose();
    } catch (err) {
      setError(String(err.message || err));
    } finally {
      setSubmitting(false);
    }
  };

  const dialogTitle = defaultType
    ? `Add ${defaultType.toUpperCase()} Device`
    : "Add Device";

  const typeLabel = (TYPE_OPTIONS.find((opt) => opt.value === type) || TYPE_OPTIONS[0]).label;

  return (
    <Dialog
      open={open}
      onClose={handleClose}
      fullWidth
      maxWidth="sm"
      PaperProps={{ sx: DIALOG_PAPER_SX }}
    >
      <DialogTitle sx={DIALOG_TITLE_SX}>
        <DialogHeaderBlock
          title={dialogTitle}
          subtitle="Add a device record Borealis can target and manage."
        />
      </DialogTitle>
      <DialogContent sx={{ ...DIALOG_CONTENT_SX, display: "flex", flexDirection: "column" }}>
        {!defaultType && (
          <TextField
            select
            label="Device Type"
            value={type}
            onChange={(e) => setType(e.target.value)}
            sx={{ ...DIALOG_SELECT_SX, mt: 1.25 }}
          >
            {TYPE_OPTIONS.map((opt) => (
              <MenuItem key={opt.value} value={opt.value}>
                {opt.label}
              </MenuItem>
            ))}
          </TextField>
        )}
        <TextField
          label="Hostname"
          value={form.hostname}
          onChange={handleChange("hostname")}
          sx={{ ...DIALOG_INPUT_SX, mt: defaultType ? 1.25 : 2.1 }}
          helperText="Name used inside Borealis."
        />
        <TextField
          label={`${typeLabel} Address`}
          value={form.address}
          onChange={handleChange("address")}
          sx={{ ...DIALOG_INPUT_SX, mt: 2.1 }}
          helperText="IP or FQDN reachable from the Borealis server."
        />
        <TextField
          label="Description"
          value={form.description}
          onChange={handleChange("description")}
          sx={{ ...DIALOG_INPUT_SX, mt: 2.1 }}
        />
        <TextField
          label="Operating System"
          value={form.operating_system}
          onChange={handleChange("operating_system")}
          sx={{ ...DIALOG_INPUT_SX, mt: 2.1 }}
        />
        {error && (
          <Typography variant="body2" sx={{ color: "#ff8080", mt: 1.5 }}>
            {error}
          </Typography>
        )}
      </DialogContent>
      <DialogActions sx={DIALOG_ACTIONS_SX}>
        <Button onClick={handleClose} sx={DIALOG_BUTTON_SX} disabled={submitting}>
          Cancel
        </Button>
        <Button
          onClick={handleSubmit}
          sx={DIALOG_PRIMARY_BUTTON_SX}
          disabled={submitting}
        >
          {submitting ? "Saving..." : "Save"}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
