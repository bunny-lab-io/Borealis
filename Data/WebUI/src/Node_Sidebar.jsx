// Node_Sidebar.jsx

import React from "react";
import {
  Accordion,
  AccordionSummary,
  AccordionDetails,
  Button,
  Divider,
  Tooltip,
  Typography
} from "@mui/material";
import {
  ExpandMore as ExpandMoreIcon,
  Save as SaveIcon,
  FileOpen as FileOpenIcon,
  DeleteForever as DeleteForeverIcon,
  DragIndicator as DragIndicatorIcon,
  Polyline as PolylineIcon
} from "@mui/icons-material";

/**
 * Left sidebar for managing workflows and node categories.
 * 
 * Props:
 * - categorizedNodes (object of arrays, e.g. { "Category": [{...}, ...], ... })
 * - handleExportFlow() => void
 * - handleImportFlow() => void
 * - handleOpenCloseAllDialog() => void
 * - fileInputRef (ref to hidden file input)
 * - onFileInputChange(event) => void
 */
export default function NodeSidebar({
  categorizedNodes,
  handleExportFlow,
  handleImportFlow,
  handleOpenCloseAllDialog,
  fileInputRef,
  onFileInputChange
}) {
  return (
    <div
      style={{
        width: 320,
        backgroundColor: "#121212",
        borderRight: "1px solid #333",
        overflowY: "auto"
      }}
    >
      <Accordion
        defaultExpanded
        square
        disableGutters
        sx={{
          "&:before": { display: "none" },
          margin: 0,
          border: 0
        }}
      >
        <AccordionSummary
          expandIcon={<ExpandMoreIcon />}
          sx={{
            backgroundColor: "#2c2c2c",
            minHeight: "36px",
            "& .MuiAccordionSummary-content": {
              margin: 0
            }
          }}
        >
          <Typography
            align="left"
            sx={{
              fontSize: "0.9rem",
              color: "#0475c2"
            }}
          >
            <b>Workflows</b>
          </Typography>
        </AccordionSummary>
        <AccordionDetails sx={{ p: 0, bgcolor: "#232323" }}>
          <Button
            fullWidth
            startIcon={<SaveIcon />}
            sx={{
              color: "#ccc",
              backgroundColor: "#232323",
              justifyContent: "flex-start",
              pl: 2,
              fontSize: "0.9rem",
              textTransform: "none",
              "&:hover": {
                backgroundColor: "#2a2a2a"
              }
            }}
            onClick={handleExportFlow}
          >
            Export Current Flow
          </Button>
          <Button
            fullWidth
            startIcon={<FileOpenIcon />}
            sx={{
              color: "#ccc",
              backgroundColor: "#232323",
              justifyContent: "flex-start",
              pl: 2,
              fontSize: "0.9rem",
              textTransform: "none",
              "&:hover": {
                backgroundColor: "#2a2a2a"
              }
            }}
            onClick={handleImportFlow}
          >
            Import Flow
          </Button>
          <Button
            fullWidth
            startIcon={<DeleteForeverIcon />}
            sx={{
              color: "#ccc",
              backgroundColor: "#232323",
              justifyContent: "flex-start",
              pl: 2,
              fontSize: "0.9rem",
              textTransform: "none",
              "&:hover": {
                backgroundColor: "#2a2a2a"
              }
            }}
            onClick={handleOpenCloseAllDialog}
          >
            Close All Flows
          </Button>
        </AccordionDetails>
      </Accordion>

      <Accordion
        defaultExpanded
        square
        disableGutters
        sx={{
          "&:before": { display: "none" },
          margin: 0,
          border: 0
        }}
      >
        <AccordionSummary
          expandIcon={<ExpandMoreIcon />}
          sx={{
            backgroundColor: "#2c2c2c",
            minHeight: "36px",
            "& .MuiAccordionSummary-content": {
              margin: 0
            }
          }}
        >
          <Typography
            align="left"
            sx={{
              fontSize: "0.9rem",
              color: "#0475c2"
            }}
          >
            <b>Nodes</b>
          </Typography>
        </AccordionSummary>
        <AccordionDetails sx={{ p: 0 }}>
          {Object.entries(categorizedNodes).map(([category, items]) => (
            <div key={category} style={{ marginBottom: 0, backgroundColor: "#232323" }}>
              <Divider
                sx={{
                  bgcolor: "transparent",
                  px: 2,
                  py: 0.75,
                  display: "flex",
                  justifyContent: "center",
                  borderColor: "#333"
                }}
                variant="fullWidth"
              >
                <Typography
                  variant="caption"
                  sx={{
                    color: "#888",
                    fontSize: "0.75rem"
                  }}
                >
                  {category}
                </Typography>
              </Divider>
              {items.map((nodeDef) => (
                <Tooltip
                  key={`${category}-${nodeDef.type}`}
                  title={
                    <span
                      style={{
                        whiteSpace: "pre-line",
                        wordWrap: "break-word",
                        maxWidth: 220
                      }}
                    >
                      {nodeDef.description || "Drag & Drop into Editor"}
                    </span>
                  }
                  placement="right"
                  arrow
                >
                  <Button
                    fullWidth
                    sx={{
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
                    }}
                    draggable
                    onDragStart={(event) => {
                      event.dataTransfer.setData("application/reactflow", nodeDef.type);
                      event.dataTransfer.effectAllowed = "move";
                    }}
                    startIcon={
                      <DragIndicatorIcon
                        sx={{
                          color: "#666",
                          fontSize: 18
                        }}
                      />
                    }
                  >
                    <span style={{ flexGrow: 1, textAlign: "left" }}>
                      {nodeDef.label}
                    </span>
                    <PolylineIcon
                      sx={{
                        color: "#58a6ff",
                        fontSize: 18,
                        ml: 1
                      }}
                    />
                  </Button>
                </Tooltip>
              ))}
            </div>
          ))}
        </AccordionDetails>
      </Accordion>

      {/* Hidden file input fallback for older browsers */}
      <input
        type="file"
        accept=".json,application/json"
        style={{ display: "none" }}
        ref={fileInputRef}
        onChange={onFileInputChange}
      />
    </div>
  );
}
