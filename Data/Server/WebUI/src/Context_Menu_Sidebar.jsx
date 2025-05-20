import React, { useState, useEffect } from "react";
import { Box, Typography, Tabs, Tab, TextField, MenuItem, Button, Slider, IconButton, Tooltip } from "@mui/material";
import ContentCopyIcon from "@mui/icons-material/ContentCopy";
import ContentPasteIcon from "@mui/icons-material/ContentPaste";
import RestoreIcon from "@mui/icons-material/Restore";
import { SketchPicker } from "react-color";

const SIDEBAR_WIDTH = 400;

const DEFAULT_EDGE_STYLE = {
  type: "bezier",
  animated: true,
  style: { strokeDasharray: "6 3", stroke: "#58a6ff", strokeWidth: 2 },
  label: "",
  labelStyle: { fill: "#fff", fontWeight: "bold" },
  labelBgStyle: { fill: "#2c2c2c", fillOpacity: 0.85, rx: 16, ry: 16 },
  labelBgPadding: [8, 4],
};

let globalEdgeClipboard = null;

function clone(obj) {
  return JSON.parse(JSON.stringify(obj));
}

export default function Context_Menu_Sidebar({
  open,
  onClose,
  edge,
  updateEdge,
}) {
  const [activeTab, setActiveTab] = useState(0);
  const [editState, setEditState] = useState(() => (edge ? clone(edge) : {}));
  const [colorPicker, setColorPicker] = useState({ field: null, anchor: null });

  useEffect(() => {
    if (edge && edge.id !== editState.id) setEditState(clone(edge));
    // eslint-disable-next-line
  }, [edge]);

  const handleChange = (field, value) => {
    setEditState((prev) => {
      const updated = { ...prev };
      if (field === "label") updated.label = value;
      else if (field === "labelStyle.fill") updated.labelStyle = { ...updated.labelStyle, fill: value };
      else if (field === "labelBgStyle.fill") updated.labelBgStyle = { ...updated.labelBgStyle, fill: value };
      else if (field === "labelBgStyle.rx") updated.labelBgStyle = { ...updated.labelBgStyle, rx: value, ry: value };
      else if (field === "labelBgPadding") updated.labelBgPadding = value;
      else if (field === "labelBgStyle.fillOpacity") updated.labelBgStyle = { ...updated.labelBgStyle, fillOpacity: value };
      else if (field === "type") updated.type = value;
      else if (field === "animated") updated.animated = value;
      else if (field === "style.stroke") updated.style = { ...updated.style, stroke: value };
      else if (field === "style.strokeDasharray") updated.style = { ...updated.style, strokeDasharray: value };
      else if (field === "style.strokeWidth") updated.style = { ...updated.style, strokeWidth: value };
      else if (field === "labelStyle.fontWeight") updated.labelStyle = { ...updated.labelStyle, fontWeight: value };
      else updated[field] = value;

      if (field === "style.strokeDasharray") {
        if (value === "") {
          updated.animated = false;
          updated.style = { ...updated.style, strokeDasharray: "" };
        } else {
          updated.animated = true;
          updated.style = { ...updated.style, strokeDasharray: value };
        }
      }
      updateEdge({ ...updated, id: prev.id });
      return updated;
    });
  };

  // Color Picker with right alignment
  const openColorPicker = (field, event) => {
    setColorPicker({ field, anchor: event.currentTarget });
  };

  const closeColorPicker = () => {
    setColorPicker({ field: null, anchor: null });
  };

  const handleColorChange = (color) => {
    handleChange(colorPicker.field, color.hex);
    closeColorPicker();
  };

  // Reset, Copy, Paste logic
  const handleReset = () => {
    setEditState(clone({ ...DEFAULT_EDGE_STYLE, id: edge.id }));
    updateEdge({ ...DEFAULT_EDGE_STYLE, id: edge.id });
  };
  const handleCopy = () => { globalEdgeClipboard = clone(editState); };
  const handlePaste = () => {
    if (globalEdgeClipboard) {
      setEditState(clone({ ...globalEdgeClipboard, id: edge.id }));
      updateEdge({ ...globalEdgeClipboard, id: edge.id });
    }
  };

  const renderColorButton = (label, field, value) => (
    <span style={{ display: "inline-block", verticalAlign: "middle", position: "relative" }}>
      <Button
        variant="outlined"
        size="small"
        onClick={(e) => openColorPicker(field, e)}
        sx={{
          ml: 1,
          borderColor: "#444",
          color: "#ccc",
          minWidth: 0,
          width: 32,
          height: 24,
          p: 0,
          bgcolor: "#232323",
        }}
      >
        <span style={{
          display: "inline-block",
          width: 20,
          height: 16,
          background: value,
          borderRadius: 3,
          border: "1px solid #888",
        }} />
      </Button>
      {colorPicker.field === field && (
        <Box sx={{
          position: "absolute",
          top: "32px",
          right: 0,
          zIndex: 1302,
          boxShadow: "0 2px 16px rgba(0,0,0,0.24)"
        }}>
          <SketchPicker
            color={value}
            onChange={handleColorChange}
            disableAlpha
            presetColors={[
              "#fff", "#000", "#58a6ff", "#ff4f4f", "#2c2c2c", "#00d18c",
              "#e3e3e3", "#0475c2", "#ff8c00", "#6b21a8", "#0e7490"
            ]}
          />
        </Box>
      )}
    </span>
  );

  // Label tab
  const renderLabelTab = () => (
    <Box sx={{ px: 2, pt: 1, pb: 2 }}>
      <Box sx={{ display: "flex", alignItems: "center", mb: 1 }}>
        <Typography variant="body2" sx={{ color: "#ccc", flex: 1 }}>Label</Typography>
      </Box>
      <TextField
        fullWidth
        size="small"
        variant="outlined"
        value={editState.label || ""}
        onChange={e => handleChange("label", e.target.value)}
        sx={{
          mb: 2,
          input: { color: "#fff", bgcolor: "#1e1e1e", fontSize: "0.95rem" },
          "& fieldset": { borderColor: "#444" },
        }}
      />

      <Box sx={{ display: "flex", alignItems: "center", mb: 1 }}>
        <Typography variant="body2" sx={{ color: "#ccc", flex: 1 }}>Text Color</Typography>
        {renderColorButton("Label Text Color", "labelStyle.fill", editState.labelStyle?.fill || "#fff")}
      </Box>

      <Box sx={{ display: "flex", alignItems: "center", mb: 1 }}>
        <Typography variant="body2" sx={{ color: "#ccc", flex: 1 }}>Background</Typography>
        {renderColorButton("Label Background Color", "labelBgStyle.fill", editState.labelBgStyle?.fill || "#2c2c2c")}
      </Box>

      <Box sx={{ display: "flex", alignItems: "center", mb: 2 }}>
        <Typography variant="body2" sx={{ color: "#ccc", flex: 1 }}>Padding</Typography>
        <TextField
          size="small"
          type="text"
          value={editState.labelBgPadding ? editState.labelBgPadding.join(",") : "8,4"}
          onChange={e => {
            const val = e.target.value.split(",").map(x => parseInt(x.trim())).filter(x => !isNaN(x));
            if (val.length === 2) handleChange("labelBgPadding", val);
          }}
          sx={{ width: 80, input: { color: "#fff", bgcolor: "#1e1e1e", fontSize: "0.95rem" } }}
        />
      </Box>

      <Box sx={{ display: "flex", alignItems: "center", mb: 2 }}>
        <Typography variant="body2" sx={{ color: "#ccc", flex: 1 }}>Background Style</Typography>
        <TextField
          select
          size="small"
          value={(editState.labelBgStyle?.rx ?? 11) >= 11 ? "rounded" : "square"}
          onChange={e => {
            handleChange("labelBgStyle.rx", e.target.value === "rounded" ? 11 : 0);
          }}
          sx={{
            width: 150,
            bgcolor: "#1e1e1e",
            "& .MuiSelect-select": { color: "#fff" }
          }}
        >
          <MenuItem value="rounded">Rounded</MenuItem>
          <MenuItem value="square">Square</MenuItem>
        </TextField>
      </Box>

      <Box sx={{ display: "flex", alignItems: "center", mb: 2 }}>
        <Typography variant="body2" sx={{ color: "#ccc", flex: 1 }}>Background Opacity</Typography>
        <Slider
          value={editState.labelBgStyle?.fillOpacity ?? 0.85}
          min={0}
          max={1}
          step={0.05}
          onChange={(_, v) => handleChange("labelBgStyle.fillOpacity", v)}
          sx={{ width: 100, ml: 2 }}
        />
        <TextField
          size="small"
          type="number"
          value={editState.labelBgStyle?.fillOpacity ?? 0.85}
          onChange={e => handleChange("labelBgStyle.fillOpacity", parseFloat(e.target.value) || 0)}
          sx={{ width: 60, ml: 2, input: { color: "#fff", bgcolor: "#1e1e1e", fontSize: "0.95rem" } }}
        />
      </Box>
    </Box>
  );

  const renderStyleTab = () => (
    <Box sx={{ px: 2, pt: 1, pb: 2 }}>
      <Box sx={{ display: "flex", alignItems: "center", mb: 2 }}>
        <Typography variant="body2" sx={{ color: "#ccc", flex: 1 }}>Edge Style</Typography>
        <TextField
          select
          size="small"
          value={editState.type || "bezier"}
          onChange={e => handleChange("type", e.target.value)}
          sx={{
            width: 200,
            bgcolor: "#1e1e1e",
            "& .MuiSelect-select": { color: "#fff" }
          }}
        >
          <MenuItem value="step">Step</MenuItem>
          <MenuItem value="bezier">Curved (Bezier)</MenuItem>
          <MenuItem value="straight">Straight</MenuItem>
          <MenuItem value="smoothstep">Smoothstep</MenuItem>
        </TextField>
      </Box>

      <Box sx={{ display: "flex", alignItems: "center", mb: 2 }}>
        <Typography variant="body2" sx={{ color: "#ccc", flex: 1 }}>Edge Animation</Typography>
        <TextField
          select
          size="small"
          value={
            editState.style?.strokeDasharray === "6 3" ? "dashes"
              : editState.style?.strokeDasharray === "2 4" ? "dots"
              : "solid"
          }
          onChange={e => {
            const val = e.target.value;
            handleChange("style.strokeDasharray",
              val === "dashes" ? "6 3" :
              val === "dots"   ? "2 4" : ""
            );
          }}
          sx={{
            width: 200,
            bgcolor: "#1e1e1e",
            "& .MuiSelect-select": { color: "#fff" }
          }}
        >
          <MenuItem value="dashes">Dashes</MenuItem>
          <MenuItem value="dots">Dots</MenuItem>
          <MenuItem value="solid">Solid</MenuItem>
        </TextField>
      </Box>

      <Box sx={{ display: "flex", alignItems: "center", mb: 2 }}>
        <Typography variant="body2" sx={{ color: "#ccc", flex: 1 }}>Color</Typography>
        {renderColorButton("Edge Color", "style.stroke", editState.style?.stroke || "#58a6ff")}
      </Box>
      <Box sx={{ display: "flex", alignItems: "center", mb: 2 }}>
        <Typography variant="body2" sx={{ color: "#ccc", flex: 1 }}>Edge Width</Typography>
        <Slider
          value={editState.style?.strokeWidth ?? 2}
          min={1}
          max={10}
          step={1}
          onChange={(_, v) => handleChange("style.strokeWidth", v)}
          sx={{ width: 100, ml: 2 }}
        />
        <TextField
          size="small"
          type="number"
          value={editState.style?.strokeWidth ?? 2}
          onChange={e => handleChange("style.strokeWidth", parseInt(e.target.value) || 1)}
          sx={{ width: 60, ml: 2, input: { color: "#fff", bgcolor: "#1e1e1e", fontSize: "0.95rem" } }}
        />
      </Box>
    </Box>
  );

  // Always render the sidebar for animation!
  if (!edge) return null;

  return (
    <>
      {/* Overlay */}
      <Box
        onClick={onClose}
        sx={{
          position: "absolute",
          top: 0,
          left: 0,
          right: 0,
          bottom: 0,
          backgroundColor: "rgba(0, 0, 0, 0.3)",
          opacity: open ? 1 : 0,
          pointerEvents: open ? "auto" : "none",
          transition: "opacity 0.6s ease",
          zIndex: 10
        }}
      />

      {/* Sidebar */}
      <Box
        sx={{
          position: "absolute",
          top: 0,
          right: 0,
          bottom: 0,
          width: SIDEBAR_WIDTH,
          bgcolor: "#2C2C2C",
          color: "#ccc",
          borderLeft: "1px solid #333",
          padding: 0,
          zIndex: 11,
          display: "flex",
          flexDirection: "column",
          height: "100%",
          transform: open ? "translateX(0)" : `translateX(${SIDEBAR_WIDTH}px)`,
          transition: "transform 0.3s cubic-bezier(.77,0,.18,1)"
        }}
        onClick={e => e.stopPropagation()}
      >
        <Box sx={{ backgroundColor: "#232323", borderBottom: "1px solid #333" }}>
          <Box sx={{ padding: "12px 16px", display: "flex", alignItems: "center" }}>
            <Typography variant="h7" sx={{ color: "#0475c2", fontWeight: "bold", flex: 1 }}>
              Edit Edge Properties
            </Typography>
          </Box>
          <Tabs
            value={activeTab}
            onChange={(_, v) => setActiveTab(v)}
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
            <Tab label="Label" sx={{
              color: "#ccc",
              "&.Mui-selected": { color: "#ccc" },
              minHeight: "36px",
              height: "36px",
              textTransform: "none"
            }} />
            <Tab label="Style" sx={{
              color: "#ccc",
              "&.Mui-selected": { color: "#ccc" },
              minHeight: "36px",
              height: "36px",
              textTransform: "none"
            }} />
          </Tabs>
        </Box>

        {/* Main fields scrollable */}
        <Box sx={{ flex: 1, overflowY: "auto" }}>
          {activeTab === 0 && renderLabelTab()}
          {activeTab === 1 && renderStyleTab()}
        </Box>

        {/* Sticky footer bar */}
        <Box sx={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          px: 2, py: 1,
          borderTop: "1px solid #333",
          backgroundColor: "#232323",
          flexShrink: 0
        }}>
          <Box>
            <Tooltip title="Copy Style"><IconButton onClick={handleCopy}><ContentCopyIcon /></IconButton></Tooltip>
            <Tooltip title="Paste Style"><IconButton onClick={handlePaste}><ContentPasteIcon /></IconButton></Tooltip>
          </Box>
          <Box>
            <Tooltip title="Reset to Default"><Button variant="outlined" size="small" startIcon={<RestoreIcon />} onClick={handleReset} sx={{
              color: "#58a6ff", borderColor: "#58a6ff", textTransform: "none"
            }}>Reset to Default</Button></Tooltip>
          </Box>
        </Box>
      </Box>
    </>
  );
}
