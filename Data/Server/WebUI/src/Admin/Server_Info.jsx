import React from "react";
import { Paper, Box, Typography } from "@mui/material";

export default function ServerInfo() {
  return (
    <Paper sx={{ m: 2, p: 0, bgcolor: "#1e1e1e" }} elevation={2}>
      <Box sx={{ p: 2 }}>
        <Typography variant="h6" sx={{ color: "#58a6ff", mb: 1 }}>Server Info</Typography>
        <Typography sx={{ color: '#aaa' }}>Basic server information will appear here.</Typography>
      </Box>
    </Paper>
  );
}

