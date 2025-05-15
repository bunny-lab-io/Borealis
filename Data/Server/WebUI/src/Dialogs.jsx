////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Dialogs.jsx

import {
  Dialog,
  DialogTitle,
  DialogContent,
  DialogContentText,
  DialogActions,
  Button,
  Menu,
  MenuItem,
  TextField
} from "@mui/material";

export function CloseAllDialog({ open, onClose, onConfirm }) {
  return (
    <Dialog open={open} onClose={onClose} PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}>
      <DialogTitle>Close All Flow Tabs?</DialogTitle>
      <DialogContent>
        <DialogContentText sx={{ color: "#ccc" }}>
          This will remove all existing flow tabs and create a fresh tab named Flow 1.
        </DialogContentText>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose} sx={{ color: "#58a6ff" }}>Cancel</Button>
        <Button onClick={onConfirm} sx={{ color: "#ff4f4f" }}>Close All</Button>
      </DialogActions>
    </Dialog>
  );
}

export function CreditsDialog({ open, onClose }) {
  return (
    <Dialog open={open} onClose={onClose} PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}>
      <DialogContent sx={{ textAlign: "center", pt: 3 }}>
        <img
          src="/Borealis_Logo.png"
          alt="Borealis Logo"
          style={{ width: "120px", marginBottom: "12px" }}
        />
        <DialogTitle sx={{ p: 0, mb: 1 }}>Borealis Workflow Automation Tool</DialogTitle>
        <DialogContentText sx={{ color: "#ccc" }}>
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
      <DialogActions>
        <Button onClick={onClose} sx={{ color: "#58a6ff" }}>Close</Button>
      </DialogActions>
    </Dialog>
  );
}

export function RenameTabDialog({ open, value, onChange, onCancel, onSave }) {
  return (
    <Dialog open={open} onClose={onCancel} PaperProps={{ sx: { bgcolor: "#121212", color: "#fff" } }}>
      <DialogTitle>Rename Tab</DialogTitle>
      <DialogContent>
        <TextField
          autoFocus
          margin="dense"
          label="Tab Name"
          fullWidth
          variant="outlined"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          sx={{
            "& .MuiOutlinedInput-root": {
              backgroundColor: "#2a2a2a",
              color: "#ccc",
              "& fieldset": {
                borderColor: "#444"
              },
              "&:hover fieldset": {
                borderColor: "#666"
              }
            },
            label: { color: "#aaa" },
            mt: 1
          }}
        />
      </DialogContent>
      <DialogActions>
        <Button onClick={onCancel} sx={{ color: "#58a6ff" }}>Cancel</Button>
        <Button onClick={onSave} sx={{ color: "#58a6ff" }}>Save</Button>
      </DialogActions>
    </Dialog>
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
      <MenuItem onClick={onCloseTab}>Close</MenuItem>
    </Menu>
  );
}