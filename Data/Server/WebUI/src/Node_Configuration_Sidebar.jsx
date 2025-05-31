////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Node_Configuration_Sidebar.jsx
import { Box, Typography, Tabs, Tab, TextField, MenuItem, IconButton, Dialog, DialogTitle, DialogContent, DialogActions, Button, Tooltip } from "@mui/material";
import React, { useState } from "react";
import { useReactFlow } from "reactflow";
import ReactMarkdown from "react-markdown"; // Used for Node Usage Documentation
import EditIcon from "@mui/icons-material/Edit";
import PaletteIcon from "@mui/icons-material/Palette";
import { SketchPicker } from "react-color";

// ---- NEW: Brightness utility for gradient ----
function darkenColor(hex, percent = 0.7) {
  if (!/^#[0-9A-Fa-f]{6}$/.test(hex)) return hex;
  let r = parseInt(hex.slice(1, 3), 16);
  let g = parseInt(hex.slice(3, 5), 16);
  let b = parseInt(hex.slice(5, 7), 16);
  r = Math.round(r * percent);
  g = Math.round(g * percent);
  b = Math.round(b * percent);
  return `#${r.toString(16).padStart(2,"0")}${g.toString(16).padStart(2,"0")}${b.toString(16).padStart(2,"0")}`;
}
// --------------------------------------------

export default function NodeConfigurationSidebar({ drawerOpen, setDrawerOpen, title, nodeData, setNodes, selectedNode }) {
  const [activeTab, setActiveTab] = useState(0);
  const contextSetNodes = useReactFlow().setNodes;
  // Use setNodes from props if provided, else fallback to context (for backward compatibility)
  const effectiveSetNodes = setNodes || contextSetNodes;
  const handleTabChange = (_, newValue) => setActiveTab(newValue);

  // Rename dialog state
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameValue, setRenameValue] = useState(title || "");

  // ---- NEW: Accent Color Picker ----
  const [colorDialogOpen, setColorDialogOpen] = useState(false);
  const accentColor = selectedNode?.data?.accentColor || "#58a6ff";
  // ----------------------------------

  const renderConfigFields = () => {
    const config = nodeData?.config || [];
    const nodeId = nodeData?.nodeId;

    return config.map((field, index) => {
      const value = nodeData?.[field.key] || "";

      return (
        <Box key={index} sx={{ mb: 2 }}>
          <Typography variant="body2" sx={{ color: "#ccc", mb: 0.5 }}>
            {field.label || field.key}
          </Typography>

          {field.type === "select" ? (
            <TextField
              select
              fullWidth
              size="small"
              value={value}
              onChange={(e) => {
                const newValue = e.target.value;
                if (!nodeId) return;
                effectiveSetNodes((nds) =>
                  nds.map((n) => {
                    if (n.id !== nodeId) return n;
                    const accentColor = color.hex;
                    const accentColorDark = darkenColor(accentColor, 0.7);
                    return {
                      ...n,
                      data: { ...n.data, accentColor },
                      style: {
                        ...n.style,
                        "--borealis-accent": accentColor,
                        "--borealis-accent-dark": accentColorDark,
                        "--borealis-title": accentColor,
                      },
                    };
                  })
                );
                window.BorealisValueBus[nodeId] = newValue;
              }}
              SelectProps={{
                MenuProps: {
                  PaperProps: {
                    sx: {
                      bgcolor: "#1e1e1e",
                      color: "#ccc",
                      border: "1px solid #58a6ff",
                      "& .MuiMenuItem-root": {
                        color: "#ccc",
                        fontSize: "0.85rem",
                        "&:hover": {
                          backgroundColor: "#2a2a2a"
                        },
                        "&.Mui-selected": {
                          backgroundColor: "#2c2c2c !important",
                          color: "#58a6ff"
                        },
                        "&.Mui-selected:hover": {
                          backgroundColor: "#2a2a2a !important"
                        }
                      }
                    }
                  }
                }
              }}
              sx={{
                "& .MuiOutlinedInput-root": {
                  backgroundColor: "#1e1e1e",
                  color: "#ccc",
                  fontSize: "0.85rem",
                  "& fieldset": {
                    borderColor: "#444"
                  },
                  "&:hover fieldset": {
                    borderColor: "#58a6ff"
                  },
                  "&.Mui-focused fieldset": {
                    borderColor: "#58a6ff"
                  }
                },
                "& .MuiSelect-select": {
                  backgroundColor: "#1e1e1e"
                }
              }}
            >
              {(field.options || []).map((opt, idx) => (
                <MenuItem key={idx} value={opt}>
                  {opt}
                </MenuItem>
              ))}
            </TextField>

          ) : (
            <TextField
              variant="outlined"
              size="small"
              fullWidth
              value={value}
              onChange={(e) => {
                const newValue = e.target.value;
                if (!nodeId) return;
                effectiveSetNodes((nds) =>
                  nds.map((n) =>
                    n.id === nodeId
                      ? { ...n, data: { ...n.data, [field.key]: newValue } }
                      : n
                  )
                );
                window.BorealisValueBus[nodeId] = newValue;
              }}
              InputProps={{
                sx: {
                  backgroundColor: "#1e1e1e",
                  color: "#ccc",
                  "& fieldset": { borderColor: "#444" },
                  "&:hover fieldset": { borderColor: "#666" },
                  "&.Mui-focused fieldset": { borderColor: "#58a6ff" }
                }
              }}
            />
          )}
        </Box>
      );
    });
  };

  // ---- NEW: Accent Color Button ----
  const renderAccentColorButton = () => (
    <Tooltip title="Override Node Header/Accent Color">
      <IconButton
        size="small"
        aria-label="Override Node Color"
        onClick={() => setColorDialogOpen(true)}
        sx={{
          ml: 1,
          border: "1px solid #58a6ff",
          background: accentColor,
          color: "#222",
          width: 28, height: 28, p: 0
        }}
      >
        <PaletteIcon fontSize="small" />
      </IconButton>
    </Tooltip>
  );
  // ----------------------------------

  return (
    <>
      <Box
        onClick={() => setDrawerOpen(false)}
        sx={{
          position: "absolute",
          top: 0,
          left: 0,
          right: 0,
          bottom: 0,
          backgroundColor: "rgba(0, 0, 0, 0.3)",
          opacity: drawerOpen ? 1 : 0,
          pointerEvents: drawerOpen ? "auto" : "none",
          transition: "opacity 0.6s ease",
          zIndex: 10
        }}
      />

      <Box
        sx={{
          position: "absolute",
          top: 0,
          right: 0,
          bottom: 0,
          width: 400,
          bgcolor: "#2C2C2C",
          color: "#ccc",
          borderLeft: "1px solid #333",
          padding: 0,
          zIndex: 11,
          overflowY: "auto",
          transform: drawerOpen ? "translateX(0)" : "translateX(100%)",
          transition: "transform 0.3s ease"
        }}
        onClick={(e) => e.stopPropagation()}
      >
        <Box sx={{ backgroundColor: "#232323", borderBottom: "1px solid #333" }}>
          <Box sx={{ padding: "12px 16px" }}>
            <Box sx={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
              <Typography variant="h7" sx={{ color: "#0475c2", fontWeight: "bold" }}>
                {"Edit " + (title || "Node")}
              </Typography>
              <Box sx={{ display: "flex", alignItems: "center" }}>
                <IconButton
                  size="small"
                  aria-label="Rename Node"
                  onClick={() => {
                    setRenameValue(title || "");
                    setRenameOpen(true);
                  }}
                  sx={{ ml: 1, color: "#58a6ff" }}
                >
                  <EditIcon fontSize="small" />
                </IconButton>
                {/* ---- NEW: Accent Color Picker button next to pencil ---- */}
                {renderAccentColorButton()}
                {/* ------------------------------------------------------ */}
              </Box>
            </Box>
          </Box>
          <Tabs
            value={activeTab}
            onChange={handleTabChange}
            variant="fullWidth"
            textColor="inherit"
            TabIndicatorProps={{ style: { backgroundColor: "#ccc" } }}
            sx={{
              borderTop: "1px solid #333",
              borderBottom: "1px solid #333",
              minHeight: "36px",
              height: "36px"
            }}
          >
            <Tab
              label="Config"
              sx={{
                color: "#ccc",
                "&.Mui-selected": { color: "#ccc" },
                minHeight: "36px",
                height: "36px",
                textTransform: "none"
              }}
            />
            <Tab
              label="Usage Docs"
              sx={{
                color: "#ccc",
                "&.Mui-selected": { color: "#ccc" },
                minHeight: "36px",
                height: "36px",
                textTransform: "none"
              }}
            />

          </Tabs>
        </Box>

        <Box sx={{ padding: 2 }}>
          {activeTab === 0 && renderConfigFields()}
          {activeTab === 1 && (
            <Box sx={{ fontSize: "0.85rem", color: "#aaa" }}>
              <ReactMarkdown
                children={nodeData?.usage_documentation || "No usage documentation provided for this node."}
                components={{
                  h3: ({ node, ...props }) => (
                    <Typography variant="h6" sx={{ color: "#58a6ff", mb: 1 }} {...props} />
                  ),
                  p: ({ node, ...props }) => (
                    <Typography paragraph sx={{ mb: 1.5 }} {...props} />
                  ),
                  ul: ({ node, ...props }) => (
                    <ul style={{ marginBottom: "1em", paddingLeft: "1.2em" }} {...props} />
                  ),
                  li: ({ node, ...props }) => (
                    <li style={{ marginBottom: "0.5em" }} {...props} />
                  )
                }}
              />
            </Box>
          )}
        </Box>
      </Box>

      {/* Rename Node Dialog */}
      <Dialog
        open={renameOpen}
        onClose={() => setRenameOpen(false)}
        PaperProps={{ sx: { bgcolor: "#232323" } }}
      >
        <DialogTitle>Rename Node</DialogTitle>
        <DialogContent>
          <TextField
            autoFocus
            fullWidth
            variant="outlined"
            label="Node Title"
            value={renameValue}
            onChange={(e) => setRenameValue(e.target.value)}
            sx={{
              mt: 1,
              bgcolor: "#1e1e1e",
              "& .MuiOutlinedInput-root": {
                color: "#ccc",
                backgroundColor: "#1e1e1e",
                "& fieldset": { borderColor: "#444" }
              },
              label: { color: "#aaa" }
            }}
          />
        </DialogContent>
        <DialogActions>
          <Button sx={{ color: "#aaa" }} onClick={() => setRenameOpen(false)}>
            Cancel
          </Button>
          <Button
            sx={{ color: "#58a6ff" }}
            onClick={() => {
              // Use selectedNode (passed as prop) or nodeData?.nodeId as fallback
              const nodeId = selectedNode?.id || nodeData?.nodeId;
              if (!nodeId) {
                setRenameOpen(false);
                return;
              }
              effectiveSetNodes((nds) =>
                nds.map((n) =>
                  n.id === nodeId
                    ? { ...n, data: { ...n.data, label: renameValue } }
                    : n
                )
              );
              setRenameOpen(false);
            }}
          >
            Save
          </Button>
        </DialogActions>
      </Dialog>

      {/* ---- NEW: Accent Color Picker Dialog ---- */}
      <Dialog
        open={colorDialogOpen}
        onClose={() => setColorDialogOpen(false)}
        PaperProps={{ sx: { bgcolor: "#232323" } }}
      >
        <DialogTitle>Pick Node Header/Accent Color</DialogTitle>
        <DialogContent>
          <SketchPicker
            color={accentColor}
            onChangeComplete={(color) => {
              const nodeId = selectedNode?.id || nodeData?.nodeId;
              if (!nodeId) return;
              const accent = color.hex;
              const accentDark = darkenColor(accent, 0.7);
              effectiveSetNodes((nds) =>
                nds.map((n) =>
                  n.id === nodeId
                    ? {
                        ...n,
                        data: { ...n.data, accentColor: accent },
                        style: {
                          ...n.style,
                          "--borealis-accent": accent,
                          "--borealis-accent-dark": accentDark,
                          "--borealis-title": accent,
                        },
                      }
                    : n
                )
              );
            }}
            disableAlpha
            presetColors={[
              "#58a6ff", "#0475c2", "#00d18c", "#ff4f4f", "#ff8c00",
              "#6b21a8", "#0e7490", "#888", "#fff", "#000"
            ]}
          />
          <Box sx={{ mt: 2 }}>
            <Typography variant="body2">
              The node's header text and accent gradient will use your selected color.<br />
              The accent gradient fades to a slightly darker version.
            </Typography>
            <Box sx={{ mt: 2, display: "flex", alignItems: "center" }}>
              <span style={{
                display: "inline-block",
                width: 48,
                height: 22,
                borderRadius: 4,
                border: "1px solid #888",
                background: `linear-gradient(to bottom, ${accentColor} 0%, ${darkenColor(accentColor, 0.7)} 100%)`
              }} />
              <span style={{ marginLeft: 10, color: accentColor, fontWeight: "bold" }}>
                {accentColor}
              </span>
            </Box>
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setColorDialogOpen(false)} sx={{ color: "#aaa" }}>Close</Button>
        </DialogActions>
      </Dialog>
      {/* ---- END ACCENT COLOR PICKER DIALOG ---- */}
    </>
  );
}
