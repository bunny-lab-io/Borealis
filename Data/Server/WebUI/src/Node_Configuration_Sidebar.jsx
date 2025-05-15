////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Node_Configuration_Sidebar.jsx
import { Box, Typography, Tabs, Tab, TextField } from "@mui/material";
import React, { useState } from "react";

export default function NodeConfigurationSidebar({ drawerOpen, setDrawerOpen, title, nodeData }) {
  const [activeTab, setActiveTab] = useState(0);
  const handleTabChange = (_, newValue) => setActiveTab(newValue);

  const renderConfigFields = () => {
    const config = nodeData?.config || [];
    return config.map((field, index) => (
      <Box key={index} sx={{ mb: 2 }}>
        <Typography variant="body2" sx={{ color: "#ccc", mb: 0.5 }}>
          {field.label || field.key}
        </Typography>
        <TextField
          variant="outlined"
          size="small"
          fullWidth
          value={nodeData?.[field.key] || ""}
          InputProps={{
            sx: {
              backgroundColor: "#1e1e1e",
              color: "#ccc",
              "& fieldset": { borderColor: "#444" },
              "&:hover fieldset": { borderColor: "#666" }
            }
          }}
        />
      </Box>
    ));
  };

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
            <Typography variant="h7" sx={{ color: "#0475c2", fontWeight: "bold" }}>
              {"Edit " + (title || "Node")}
            </Typography>
          </Box>
          <Tabs
            value={activeTab}
            onChange={handleTabChange}
            variant="fullWidth"
            textColor="inherit"
            TabIndicatorProps={{ style: { backgroundColor: "#58a6ff" } }}
            sx={{ borderTop: "1px solid #333", borderBottom: "1px solid #333", minHeight: "36px", height: "36px" }}
          >
            <Tab label="Config" sx={{ color: "#ccc", minHeight: "36px", height: "36px", textTransform: "none" }} />
            <Tab label="Usage Docs" sx={{ color: "#ccc", minHeight: "36px", height: "36px", textTransform: "none" }} />
          </Tabs>
        </Box>

        <Box sx={{ padding: 2 }}>
          {activeTab === 0 && renderConfigFields()}
          {activeTab === 1 && (
            <Typography sx={{ whiteSpace: "pre-wrap", fontSize: "0.85rem", color: "#aaa" }}>
              {nodeData?.usage_documentation || "No documentation provided for this node."}
            </Typography>
          )}
        </Box>
      </Box>
    </>
  );
}
