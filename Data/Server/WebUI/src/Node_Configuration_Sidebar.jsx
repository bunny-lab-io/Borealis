////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Node_Configuration_Sidebar.jsx
import { Box, Typography } from "@mui/material";

export default function NodeConfigurationSidebar({ drawerOpen, setDrawerOpen, title }) {
  return (
    <>
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
        {/* Title bar section */}
        <Box
          sx={{
            backgroundColor: "#232323",
            padding: "16px",
            borderBottom: "1px solid #333"
          }}
        >
          <Typography variant="h6" sx={{ color: "#0475c2" }}>
            {title || "Node Configuration Panel"}
          </Typography>
        </Box>

        {/* Content */}
        <Box sx={{ padding: 2 }}>
          <p style={{ fontSize: "0.85rem", color: "#aaa" }}>
            This sidebar will be used to configure nodes in the future.
          </p>
          <p style={{ fontSize: "0.85rem", color: "#aaa" }}>
            The idea is that this area will allow for more node configuration controls to be dynamically-populated by the nodes to allow more complex node documentation and configuration.
          </p>
        </Box>
      </Box>
    </>
  );
}
