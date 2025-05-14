////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Node_Configuration_Sidebar.jsx
import React from "react";
import { Box, Typography, IconButton } from "@mui/material";
import { Settings as SettingsIcon } from "@mui/icons-material";

export default function NodeConfigurationSidebar({ drawerOpen, setDrawerOpen, title }) {
  return (
    <>
      {/* Top-right gear icon */}
      <IconButton
        onClick={() => setDrawerOpen(true)}
        size="small"
        sx={{
          position: "absolute",
          top: 8,
          right: 8,
          zIndex: 1500,
          bgcolor: "#2a2a2a",
          color: "#58a6ff",
          border: "1px solid #444",
          "&:hover": { bgcolor: "#3a3a3a" }
        }}
      >
        <SettingsIcon fontSize="small" />
      </IconButton>

      {/* Dim overlay when drawer is open */}
      {drawerOpen && (
        <Box
          onClick={() => setDrawerOpen(false)}
          sx={{
            position: "absolute",
            top: 0,
            left: 0,
            right: 0,
            bottom: 0,
            backgroundColor: "rgba(0,0,0,0.4)",
            zIndex: 10
          }}
        />
      )}

      {/* Right-Side Node Configuration Panel */}
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

      {/* Animated right drawer panel */}
      <Box
        sx={{
          position: "absolute",
          top: 0,
          right: 0,
          bottom: 0,
          width: 400,
          bgcolor: "#121212",
          color: "#ccc",
          borderLeft: "1px solid #333",
          padding: 2,
          zIndex: 11,
          overflowY: "auto",
          transform: drawerOpen ? "translateX(0)" : "translateX(100%)",
          transition: "transform 0.3s ease"
        }}
        onClick={(e) => e.stopPropagation()}
      >
        <Typography variant="h6" sx={{ mb: 2, color: "#0475c2" }}>
          {title || "Node Configuration Panel"}
        </Typography>
        <p style={{ fontSize: "0.85rem", color: "#aaa" }}>
          This sidebar will be used to configure nodes in the future.
        </p>
        <p style={{ fontSize: "0.85rem", color: "#aaa" }}>
          The idea is that this area will allow for more node configuration controls to be dynamically-populated by the nodes to allow more complex node documentation and configuration.
        </p>
      </Box>
    </>
  );
}
