////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Dialogs.jsx

import React from "react";
import {
  Alert,
  Box,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogContentText,
  DialogActions,
  Button,
  Menu,
  MenuItem,
  TextField,
  Typography,
} from "@mui/material";
import {
  DIALOG_ACTIONS_SX,
  DIALOG_BODY_TEXT_SX,
  DIALOG_BUTTON_SX,
  DIALOG_CONTENT_SX,
  DIALOG_DANGER_BUTTON_SX,
  DIALOG_INPUT_SX,
  DIALOG_PAPER_SX,
  DIALOG_PRIMARY_BUTTON_SX,
  DIALOG_TITLE_SX,
  DialogHeaderBlock,
} from "./DialogStyles.jsx";

function DialogFrame({ open, onClose, title, subtitle, maxWidth = "xs", fullWidth = true, children, actions, contentSx }) {
  return (
    <Dialog open={open} onClose={onClose} maxWidth={maxWidth} fullWidth={fullWidth} PaperProps={{ sx: DIALOG_PAPER_SX }}>
      <DialogTitle sx={DIALOG_TITLE_SX}>
        <DialogHeaderBlock title={title} subtitle={subtitle} />
      </DialogTitle>
      <DialogContent sx={{ ...DIALOG_CONTENT_SX, ...contentSx }}>
        {children}
      </DialogContent>
      {actions ? <DialogActions sx={DIALOG_ACTIONS_SX}>{actions}</DialogActions> : null}
    </Dialog>
  );
}

export function CloseAllDialog({ open, onClose, onConfirm }) {
  return (
    <DialogFrame
      open={open}
      onClose={onClose}
      title="Close All Flow Tabs?"
      actions={
        <>
          <Button onClick={onClose} sx={DIALOG_BUTTON_SX}>Cancel</Button>
          <Button onClick={onConfirm} sx={DIALOG_DANGER_BUTTON_SX}>Close All</Button>
        </>
      }
    >
      <DialogContentText sx={DIALOG_BODY_TEXT_SX}>
          This will remove all existing flow tabs and create a fresh tab named Flow 1.
      </DialogContentText>
    </DialogFrame>
  );
}

export function NotAuthorizedDialog({ open, onClose }) {
  return (
    <DialogFrame
      open={open}
      onClose={onClose}
      title="Not Authorized"
      actions={<Button onClick={onClose} sx={DIALOG_BUTTON_SX}>OK</Button>}
    >
      <DialogContentText sx={DIALOG_BODY_TEXT_SX}>
          You are not authorized to access this section.
      </DialogContentText>
    </DialogFrame>
  );
}

export function CreditsDialog({ open, onClose }) {
  return (
    <Dialog open={open} onClose={onClose} maxWidth="xs" fullWidth PaperProps={{ sx: DIALOG_PAPER_SX }}>
      <DialogContent sx={{ ...DIALOG_CONTENT_SX, textAlign: "center", pt: 3 }}>
        <img
          src="/Borealis_Logo.png"
          alt="Borealis Logo"
          style={{ width: "120px", marginBottom: "12px" }}
        />
        <DialogTitle sx={{ p: 0, mb: 1, color: "#e2e8f0" }}>Borealis - Automation Platform</DialogTitle>
        <DialogContentText sx={DIALOG_BODY_TEXT_SX}>
          Designed by Nicole Rappe @{" "}
          <a
            href="https://bunny-lab.io"
            target="_blank"
            rel="noopener noreferrer"
            style={{ color: "#58a6ff", textDecoration: "none" }}
          >
            Bunny Lab
          </a>
        </DialogContentText>
      </DialogContent>
      <DialogActions sx={DIALOG_ACTIONS_SX}>
        <Button onClick={onClose} sx={DIALOG_BUTTON_SX}>Close</Button>
      </DialogActions>
    </Dialog>
  );
}

export function RenameTabDialog({ open, value, onChange, onCancel, onSave }) {
  return (
    <DialogFrame
      open={open}
      onClose={onCancel}
      title="Rename Tab"
      actions={
        <>
          <Button onClick={onCancel} sx={DIALOG_BUTTON_SX}>Cancel</Button>
          <Button onClick={onSave} sx={DIALOG_PRIMARY_BUTTON_SX}>Save</Button>
        </>
      }
    >
        <TextField
          autoFocus
          label="Tab Name"
          fullWidth
          variant="outlined"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
        />
    </DialogFrame>
  );
}

export function RenameWorkflowDialog({ open, value, onChange, onCancel, onSave }) {
  return (
    <DialogFrame
      open={open}
      onClose={onCancel}
      title="Rename Workflow"
      actions={
        <>
          <Button onClick={onCancel} sx={DIALOG_BUTTON_SX}>Cancel</Button>
          <Button onClick={onSave} sx={DIALOG_PRIMARY_BUTTON_SX}>Save</Button>
        </>
      }
    >
        <TextField
          autoFocus
          label="Workflow Name"
          fullWidth
          variant="outlined"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
        />
    </DialogFrame>
  );
}

export function RenameFolderDialog({
  open,
  value,
  onChange,
  onCancel,
  onSave,
  title = "Folder Name",
  confirmText = "Save"
}) {
  return (
    <DialogFrame
      open={open}
      onClose={onCancel}
      title={title}
      actions={
        <>
          <Button onClick={onCancel} sx={DIALOG_BUTTON_SX}>Cancel</Button>
          <Button onClick={onSave} sx={DIALOG_PRIMARY_BUTTON_SX}>{confirmText}</Button>
        </>
      }
    >
        <TextField
          autoFocus
          label="Folder Name"
          fullWidth
          variant="outlined"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
        />
    </DialogFrame>
  );
}

export function NewWorkflowDialog({ open, value, onChange, onCancel, onCreate }) {
  return (
    <DialogFrame
      open={open}
      onClose={onCancel}
      title="New Workflow"
      actions={
        <>
          <Button onClick={onCancel} sx={DIALOG_BUTTON_SX}>Cancel</Button>
          <Button onClick={onCreate} sx={DIALOG_PRIMARY_BUTTON_SX}>Create</Button>
        </>
      }
    >
        <TextField
          autoFocus
          label="Workflow Name"
          fullWidth
          variant="outlined"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
        />
    </DialogFrame>
  );
}

export function ClearDeviceActivityDialog({ open, onCancel, onConfirm }) {
  return (
    <DialogFrame
      open={open}
      onClose={onCancel}
      title="Clear Device Activity"
      actions={
        <>
          <Button onClick={onCancel} sx={DIALOG_BUTTON_SX}>Cancel</Button>
          <Button onClick={onConfirm} sx={DIALOG_DANGER_BUTTON_SX}>Clear</Button>
        </>
      }
    >
      <DialogContentText sx={DIALOG_BODY_TEXT_SX}>
          All device activity history will be cleared, are you sure?
      </DialogContentText>
    </DialogFrame>
  );
}

export function SaveWorkflowDialog({ open, value, onChange, onCancel, onSave }) {
  return (
    <DialogFrame
      open={open}
      onClose={onCancel}
      title="Save Workflow"
      actions={
        <>
          <Button onClick={onCancel} sx={DIALOG_BUTTON_SX}>Cancel</Button>
          <Button onClick={onSave} sx={DIALOG_PRIMARY_BUTTON_SX}>Save</Button>
        </>
      }
    >
        <TextField
          autoFocus
          label="Workflow Name"
          fullWidth
          variant="outlined"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
        />
    </DialogFrame>
  );
}

export function ConfirmDeleteDialog({
  open,
  message,
  onCancel,
  onConfirm,
  title = "Confirm Delete",
  confirmLabel = "Confirm",
  confirmDisabled = false,
  confirmTone = "danger",
}) {
  return (
    <DialogFrame
      open={open}
      onClose={onCancel}
      title={title}
      actions={
        <>
          <Button onClick={onCancel} sx={DIALOG_BUTTON_SX}>Cancel</Button>
          <Button
            onClick={onConfirm}
            disabled={confirmDisabled}
            sx={confirmTone === "danger" ? DIALOG_DANGER_BUTTON_SX : DIALOG_PRIMARY_BUTTON_SX}
          >
            {confirmLabel}
          </Button>
        </>
      }
    >
      <DialogContentText sx={DIALOG_BODY_TEXT_SX}>{message}</DialogContentText>
    </DialogFrame>
  );
}

export function DeleteDeviceDialog({ open, onCancel, onConfirm, busy = false, errorText = "", devices = [] }) {
  const deviceRows = Array.isArray(devices) ? devices : [];
  const previewNames = deviceRows
    .map((device) => String(device?.hostname || device?.summary?.hostname || device?.agentGuid || device?.guid || "").trim())
    .filter(Boolean)
    .slice(0, 5);
  const remainingCount = Math.max(deviceRows.length - previewNames.length, 0);
  const count = Math.max(deviceRows.length, 1);
  const plural = count !== 1;
  const title = plural ? `Purge ${count} Devices` : "Purge Device";
  const confirmLabel = busy ? "Purging..." : plural ? `Purge ${count} Devices` : "Purge Device";

  return (
    <DialogFrame
      open={open}
      onClose={onCancel}
      title={title}
      actions={
        <>
          <Button onClick={onCancel} sx={DIALOG_BUTTON_SX} disabled={busy}>Cancel</Button>
          <Button onClick={onConfirm} sx={DIALOG_DANGER_BUTTON_SX} disabled={busy}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      {errorText ? <Alert severity="error" sx={{ mb: 1.5 }}>{errorText}</Alert> : null}
      {previewNames.length ? (
        <Box
          sx={{
            mb: 1.5,
            border: "1px solid rgba(148,163,184,0.28)",
            borderRadius: 2,
            background: "rgba(7,12,24,0.68)",
            px: 1.4,
            py: 1.2,
          }}
        >
          <Typography sx={{ color: "#94a3b8", fontSize: "0.76rem", fontWeight: 800, letterSpacing: 0.7, textTransform: "uppercase" }}>
            Selected Devices
          </Typography>
          <Box sx={{ mt: 0.9, display: "flex", flexWrap: "wrap", gap: 0.75 }}>
            {previewNames.map((name, index) => (
              <Box
                key={`${name}-${index}`}
                sx={{
                  borderRadius: 999,
                  border: "1px solid rgba(148,163,184,0.24)",
                  background: "rgba(15,23,42,0.72)",
                  px: 1,
                  py: 0.45,
                }}
              >
                <Typography sx={{ color: "#e2e8f0", fontSize: "0.8rem", fontWeight: 600 }}>{name}</Typography>
              </Box>
            ))}
            {remainingCount > 0 ? (
              <Box
                sx={{
                  borderRadius: 999,
                  border: "1px solid rgba(248,113,113,0.28)",
                  background: "rgba(76,5,25,0.38)",
                  px: 1,
                  py: 0.45,
                }}
              >
                <Typography sx={{ color: "#fecdd3", fontSize: "0.8rem", fontWeight: 700 }}>+{remainingCount} more</Typography>
              </Box>
            ) : null}
          </Box>
        </Box>
      ) : null}
      <DialogContentText sx={DIALOG_BODY_TEXT_SX}>
          Borealis will permanently purge {plural ? "these devices'" : "this device's"} identity, history, site assignment, keys and tokens, and scheduled-job references. If {plural ? "these machines come" : "the same machine comes"} back, {plural ? "they" : "it"} will need fresh enrollment and approval.
      </DialogContentText>
    </DialogFrame>
  );
}

export function TabContextMenu({ anchor, onClose, onRename, onCloseTab }) {
  return (
    <Menu
      open={Boolean(anchor)}
      onClose={onClose}
      anchorReference="anchorPosition"
      anchorPosition={anchor ? { top: anchor.y, left: anchor.x } : undefined}
      PaperProps={{
        sx: {
          bgcolor: "#1e1e1e",
          color: "#fff",
          fontSize: "13px"
        }
      }}
    >
      <MenuItem onClick={onRename}>Rename</MenuItem>
      <MenuItem onClick={onCloseTab}>Close Workflow</MenuItem>
    </Menu>
  );
}

export function CreateCustomViewDialog({ open, value, onChange, onCancel, onSave }) {
  return (
    <DialogFrame
      open={open}
      onClose={onCancel}
      title="Create a New Custom View"
      actions={
        <>
          <Button onClick={onCancel} sx={DIALOG_BUTTON_SX}>Cancel</Button>
          <Button onClick={onSave} sx={DIALOG_PRIMARY_BUTTON_SX}>Save</Button>
        </>
      }
    >
        <DialogContentText sx={{ ...DIALOG_BODY_TEXT_SX, mb: 1 }}>
          Saving a view will save column order, visibility, and filters.
        </DialogContentText>
        <TextField
          autoFocus
          fullWidth
          label="View Name"
          variant="outlined"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="Add a name for this custom view"
          sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
        />
    </DialogFrame>
  );
}

export function RenameCustomViewDialog({ open, value, onChange, onCancel, onSave }) {
  return (
    <DialogFrame
      open={open}
      onClose={onCancel}
      title="Rename Custom View"
      actions={
        <>
          <Button onClick={onCancel} sx={DIALOG_BUTTON_SX}>Cancel</Button>
          <Button onClick={onSave} sx={DIALOG_PRIMARY_BUTTON_SX}>Save</Button>
        </>
      }
    >
        <TextField
          autoFocus
          fullWidth
          label="View Name"
          variant="outlined"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
        />
    </DialogFrame>
  );
}

export function CreateSiteDialog({ open, onCancel, onCreate }) {
  const [name, setName] = React.useState("");
  const [description, setDescription] = React.useState("");
  const trimmedName = (name || "").trim();

  React.useEffect(() => {
    if (open) {
      setName("");
      setDescription("");
    }
  }, [open]);

  return (
    <Dialog open={open} onClose={onCancel} maxWidth="sm" fullWidth PaperProps={{ sx: DIALOG_PAPER_SX }}>
      <DialogTitle sx={DIALOG_TITLE_SX}>
        <DialogHeaderBlock
          title="Create Site"
          subtitle="Provision a new site profile for device enrollment, filtering, and operator workflows."
        />
      </DialogTitle>
      <DialogContent sx={DIALOG_CONTENT_SX}>
        <TextField
          autoFocus
          fullWidth
          label="Site Name"
          variant="outlined"
          value={name}
          onChange={(e) => setName(e.target.value)}
          sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
        />
        <TextField
          fullWidth
          multiline
          minRows={3}
          label="Description"
          variant="outlined"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          sx={{ ...DIALOG_INPUT_SX, mt: 2.75 }}
        />
      </DialogContent>
      <DialogActions sx={DIALOG_ACTIONS_SX}>
        <Button onClick={onCancel} sx={DIALOG_BUTTON_SX}>Cancel</Button>
        <Button
          onClick={() => {
            if (!trimmedName) return;
            onCreate && onCreate(trimmedName, description || "");
          }}
          disabled={!trimmedName}
          sx={DIALOG_PRIMARY_BUTTON_SX}
        >
          Create
        </Button>
      </DialogActions>
    </Dialog>
  );
}

export function RenameSiteDialog({ open, value, onChange, onCancel, onSave }) {
  const trimmedValue = (value || "").trim();

  return (
    <Dialog open={open} onClose={onCancel} maxWidth="xs" fullWidth PaperProps={{ sx: DIALOG_PAPER_SX }}>
      <DialogTitle sx={DIALOG_TITLE_SX}>
        <DialogHeaderBlock
          title="Rename Site"
        />
      </DialogTitle>
      <DialogContent sx={DIALOG_CONTENT_SX}>
        <TextField
          autoFocus
          fullWidth
          label="Site Name"
          variant="outlined"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          sx={{ ...DIALOG_INPUT_SX, mt: 1.25 }}
        />
      </DialogContent>
      <DialogActions sx={DIALOG_ACTIONS_SX}>
        <Button onClick={onCancel} sx={DIALOG_BUTTON_SX}>Cancel</Button>
        <Button onClick={onSave} disabled={!trimmedValue} sx={DIALOG_PRIMARY_BUTTON_SX}>Save</Button>
      </DialogActions>
    </Dialog>
  );
}
