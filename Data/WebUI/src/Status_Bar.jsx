////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Status_Bar.jsx

import React from "react";
import { Box, Button, Divider } from "@mui/material";

export default function StatusBar() {
  const applyRate = () => {
    const val = parseInt(
      document.getElementById("updateRateInput")?.value
    );
    if (!isNaN(val) && val >= 50) {
      window.BorealisUpdateRate = val;
      console.log("Global update rate set to", val + "ms");
    } else {
      alert("Please enter a valid number (min 50).");
    }
  };

  return (
    <Box
      component="footer"
      sx={{
        bgcolor: "#1e1e1e",
        color: "white",
        px: 2,
        py: 1,
        display: "flex",
        alignItems: "center",
        gap: 2
      }}
    >
      <b>Nodes</b>: <span id="nodeCount">0</span>
      <Divider
        orientation="vertical"
        flexItem
        sx={{ borderColor: "#444" }}
      />
      <b>Update Rate (ms):</b>
      <input
        id="updateRateInput"
        type="number"
        min="50"
        step="50"
        defaultValue={window.BorealisUpdateRate}
        style={{
          width: "80px",
          background: "#121212",
          color: "#fff",
          border: "1px solid #444",
          borderRadius: "3px",
          padding: "3px",
          fontSize: "0.8rem"
        }}
      />
      <Button
        variant="outlined"
        size="small"
        onClick={applyRate}
        sx={{
          color: "#58a6ff",
          borderColor: "#58a6ff",
          fontSize: "0.75rem",
          textTransform: "none",
          px: 1.5
        }}
      >
        Apply Rate
      </Button>
    </Box>
  );
}
