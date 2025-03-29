import React from "react";
import FlowEditor from "./components/FlowEditor";
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import {
  AppBar,
  Toolbar,
  Typography,
  Box,
  Menu,
  MenuItem,
  Button,
  CssBaseline,
  ThemeProvider,
  createTheme
} from "@mui/material";

const darkTheme = createTheme({
  palette: {
    mode: "dark",
    background: {
      default: "#121212",
      paper: "#1e1e1e"
    },
    text: {
      primary: "#ffffff"
    }
  }
});

export default function App() {
  const [workflowsAnchorEl, setWorkflowsAnchorEl] = React.useState(null);
  const [aboutAnchorEl, setAboutAnchorEl] = React.useState(null);

  const handleWorkflowsMenuOpen = (event) => {
    setWorkflowsAnchorEl(event.currentTarget);
  };

  const handleAboutMenuOpen = (event) => {
    setAboutAnchorEl(event.currentTarget);
  };

  const handleWorkflowsMenuClose = () => {
    setWorkflowsAnchorEl(null);
  };

  const handleAboutMenuClose = () => {
    setAboutAnchorEl(null);
  };

  return (
    <ThemeProvider theme={darkTheme}>
      <CssBaseline />
      {/*
        Main container that:
          - fills 100% viewport height
          - organizes content with flexbox (vertical)
      */}
      <Box display="flex" flexDirection="column" height="100vh">
        {/* --- TOP BAR --- */}
        <AppBar position="static" sx={{ bgcolor: "#092c44" }}>
          <Toolbar>
            <Typography variant="h6" sx={{ flexGrow: 1 }}>
              Borealis - Workflow Automation Tool
            </Typography>

            {/* Workflows Menu */}
            <Button
              color="inherit"
              onClick={handleWorkflowsMenuOpen}
              endIcon={<KeyboardArrowDownIcon />}
            >
              Workflows
            </Button>
            <Menu
              anchorEl={workflowsAnchorEl}
              open={Boolean(workflowsAnchorEl)}
              onClose={handleWorkflowsMenuClose}
            >
              <MenuItem onClick={handleWorkflowsMenuClose}>Save Workflow</MenuItem>
              <MenuItem onClick={handleWorkflowsMenuClose}>Load Workflow</MenuItem>
              <MenuItem onClick={handleWorkflowsMenuClose}>Close Workflow</MenuItem>
            </Menu>

            {/* About Menu */}
            <Button
              color="inherit"
              onClick={handleAboutMenuOpen}
              endIcon={<KeyboardArrowDownIcon />}
            >
              About
            </Button>
            <Menu
              anchorEl={aboutAnchorEl}
              open={Boolean(aboutAnchorEl)}
              onClose={handleAboutMenuClose}
            >
              <MenuItem onClick={handleAboutMenuClose}>Gitea Project</MenuItem>
              <MenuItem onClick={handleAboutMenuClose}>Credits</MenuItem>
            </Menu>
          </Toolbar>
        </AppBar>

        {/* --- REACT FLOW EDITOR --- */}
        {/*
          flexGrow={1} ⇒ This box expands to fill remaining vertical space 
          overflow="hidden" ⇒ No scroll bars, so React Flow does internal panning
          mt: 1 ⇒ Add top margin so the gradient starts closer to the AppBar.
        */}
        <Box flexGrow={1} overflow="hidden" sx={{ mt: 0 }}>
          <FlowEditor
            updateNodeCount={(count) => {
              document.getElementById("nodeCount").innerText = count;
            }}
          />
        </Box>

        {/* --- STATUS BAR at BOTTOM --- */}
        <Box
          component="footer"
          sx={{
            bgcolor: "#1e1e1e",
            color: "white",
            px: 2,
            py: 1,
            textAlign: "left"
          }}
        >
          <b>Nodes</b>: <span id="nodeCount">0</span> | <b>Update Rate</b>: 500ms | <b>Flask API Server:</b>{" "}
          <a
            href="http://127.0.0.1:5000/api/nodes"
            style={{ color: "#3c78b4" }}
          >
            http://127.0.0.1:5000/data/api/nodes
          </a>
        </Box>
      </Box>
    </ThemeProvider>
  );
}
