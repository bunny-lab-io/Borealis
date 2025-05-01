////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Node_Sidebar.jsx

import React, { useState } from "react";
import {
  Accordion,
  AccordionSummary,
  AccordionDetails,
  Button,
  Tooltip,
  Typography,
  IconButton,
  Box
} from "@mui/material";
import {
  ExpandMore as ExpandMoreIcon,
  Save as SaveIcon,
  FileOpen as FileOpenIcon,
  DeleteForever as DeleteForeverIcon,
  DragIndicator as DragIndicatorIcon,
  Polyline as PolylineIcon,
  ChevronLeft as ChevronLeftIcon,
  ChevronRight as ChevronRightIcon
} from "@mui/icons-material";

export default function NodeSidebar({
  categorizedNodes,
  handleExportFlow,
  handleImportFlow,
  handleOpenCloseAllDialog,
  fileInputRef,
  onFileInputChange
}) {
  const [expandedCategory, setExpandedCategory] = useState(null);
  const [collapsed, setCollapsed] = useState(false);

  const handleAccordionChange = (category) => (_, isExpanded) => {
    setExpandedCategory(isExpanded ? category : null);
  };

  return (
    <div
      style={{
        width: collapsed ? 40 : 300,
        backgroundColor: "#121212",
        borderRight: "1px solid #333",
        overflow: "hidden",
        display: "flex",
        flexDirection: "column",
        height: "100%"
      }}
    >
      <div style={{ flex: 1, overflowY: "auto" }}>
        {!collapsed && (
          <>
            {/* Workflows Section */}
            <Accordion
              defaultExpanded
              square
              disableGutters
              sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}
            >
              <AccordionSummary
                expandIcon={<ExpandMoreIcon />}
                sx={{
                  backgroundColor: "#2c2c2c",
                  minHeight: "36px",
                  "& .MuiAccordionSummary-content": { margin: 0 }
                }}
              >
                <Typography sx={{ fontSize: "0.9rem", color: "#0475c2" }}>
                  <b>Workflows</b>
                </Typography>
              </AccordionSummary>
              <AccordionDetails sx={{ p: 0, bgcolor: "#232323" }}>
                <Tooltip title="Export Current Tab to a JSON File" placement="right" arrow>
                  <Button fullWidth startIcon={<SaveIcon />} onClick={handleExportFlow} sx={buttonStyle}>
                    Export Current Flow
                  </Button>
                </Tooltip>
                <Tooltip title="Import JSON File into New Flow Tab" placement="right" arrow>
                  <Button fullWidth startIcon={<FileOpenIcon />} onClick={handleImportFlow} sx={buttonStyle}>
                    Import Flow
                  </Button>
                </Tooltip>
                <Tooltip title="Destroy all Flow Tabs Immediately" placement="right" arrow>
                  <Button fullWidth startIcon={<DeleteForeverIcon />} onClick={handleOpenCloseAllDialog} sx={buttonStyle}>
                    Close All Flows
                  </Button>
                </Tooltip>
              </AccordionDetails>
            </Accordion>

            {/* Nodes Section */}
            <Accordion
              defaultExpanded
              square
              disableGutters
              sx={{ "&:before": { display: "none" }, margin: 0, border: 0 }}
            >
              <AccordionSummary
                expandIcon={<ExpandMoreIcon />}
                sx={{
                  backgroundColor: "#2c2c2c",
                  minHeight: "36px",
                  "& .MuiAccordionSummary-content": { margin: 0 }
                }}
              >
                <Typography sx={{ fontSize: "0.9rem", color: "#0475c2" }}>
                  <b>Nodes</b>
                </Typography>
              </AccordionSummary>
              <AccordionDetails sx={{ p: 0 }}>
                {Object.entries(categorizedNodes).map(([category, items]) => (
                  <Accordion
                    key={category}
                    square
                    expanded={expandedCategory === category}
                    onChange={handleAccordionChange(category)}
                    disableGutters
                    sx={{
                      bgcolor: "#232323",
                      "&:before": { display: "none" },
                      margin: 0,
                      border: 0
                    }}
                  >
                    <AccordionSummary
                      expandIcon={<ExpandMoreIcon />}
                      sx={{
                        bgcolor: "#1e1e1e",
                        px: 2,
                        minHeight: "32px",
                        "& .MuiAccordionSummary-content": { margin: 0 }
                      }}
                    >
                      <Typography sx={{ color: "#888", fontSize: "0.75rem" }}>
                        {category}
                      </Typography>
                    </AccordionSummary>
                    <AccordionDetails sx={{ px: 1, py: 0 }}>
                      {items.map((nodeDef) => (
                        <Tooltip
                          key={`${category}-${nodeDef.type}`}
                          title={
                            <span style={{ whiteSpace: "pre-line", wordWrap: "break-word", maxWidth: 220 }}>
                              {nodeDef.description || "Drag & Drop into Editor"}
                            </span>
                          }
                          placement="right"
                          arrow
                        >
                          <Button
                            fullWidth
                            sx={nodeButtonStyle}
                            draggable
                            onDragStart={(event) => {
                              event.dataTransfer.setData("application/reactflow", nodeDef.type);
                              event.dataTransfer.effectAllowed = "move";
                            }}
                            startIcon={<DragIndicatorIcon sx={{ color: "#666", fontSize: 18 }} />}
                          >
                            <span style={{ flexGrow: 1, textAlign: "left" }}>{nodeDef.label}</span>
                            <PolylineIcon sx={{ color: "#58a6ff", fontSize: 18, ml: 1 }} />
                          </Button>
                        </Tooltip>
                      ))}
                    </AccordionDetails>
                  </Accordion>
                ))}
              </AccordionDetails>
            </Accordion>

            {/* Hidden file input */}
            <input
              type="file"
              accept=".json,application/json"
              style={{ display: "none" }}
              ref={fileInputRef}
              onChange={onFileInputChange}
            />
          </>
        )}
      </div>

      {/* Bottom toggle button */}
      <Tooltip title={collapsed ? "Expand Sidebar" : "Collapse Sidebar"} placement="left">
        <Box
          onClick={() => setCollapsed(!collapsed)}
          sx={{
            height: "36px",
            borderTop: "1px solid #333",
            cursor: "pointer",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            color: "#888",
            backgroundColor: "#121212",
            transition: "background-color 0.2s ease",
            "&:hover": {
              backgroundColor: "#1e1e1e"
            },
            "&:active": {
              backgroundColor: "#2a2a2a"
            }
          }}
        >
          {collapsed ? <ChevronRightIcon /> : <ChevronLeftIcon />}
        </Box>
      </Tooltip>
    </div>
  );
}

const buttonStyle = {
  color: "#ccc",
  backgroundColor: "#232323",
  justifyContent: "flex-start",
  pl: 2,
  fontSize: "0.9rem",
  textTransform: "none",
  "&:hover": {
    backgroundColor: "#2a2a2a"
  }
};

const nodeButtonStyle = {
  color: "#ccc",
  backgroundColor: "#232323",
  justifyContent: "space-between",
  pl: 2,
  pr: 1,
  fontSize: "0.9rem",
  textTransform: "none",
  "&:hover": {
    backgroundColor: "#2a2a2a"
  }
};
