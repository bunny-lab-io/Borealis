////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/WebUI/src/Node_Sidebar.jsx

import React, { useState } from "react";
import {
  Accordion,
  AccordionSummary,
  AccordionDetails,
  Button,
  Tooltip,
  Typography,
  Box
} from "@mui/material";
import {
  ExpandMore as ExpandMoreIcon,
  SaveAlt as SaveAltIcon,
  Save as SaveIcon,
  FileOpen as FileOpenIcon,
  DeleteForever as DeleteForeverIcon,
  DriveFileRenameOutline as DriveFileRenameOutlineIcon,
  DragIndicator as DragIndicatorIcon,
  Polyline as PolylineIcon,
  ChevronLeft as ChevronLeftIcon,
  ChevronRight as ChevronRightIcon
} from "@mui/icons-material";
import { ConfirmDeleteDialog, RenameWorkflowDialog, SaveWorkflowDialog } from "../Dialogs";

export default function NodeSidebar({
  categorizedNodes,
  handleExportFlow,
  handleImportFlow,
  handleSaveFlow,
  handleRenameFlow,
  handleDeleteFlow,
  handleOpenCloseAllDialog,
  fileInputRef,
  onFileInputChange,
  currentTabName,
  currentAssemblyGuid,
  currentDomain
}) {
  const [expandedCategory, setExpandedCategory] = useState(null);
  const [collapsed, setCollapsed] = useState(false);
  const [saveOpen, setSaveOpen] = useState(false);
  const [saveName, setSaveName] = useState("");
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameName, setRenameName] = useState("");
  const [deleteOpen, setDeleteOpen] = useState(false);

  const normalizedGuid = typeof currentAssemblyGuid === "string" ? currentAssemblyGuid.trim() : "";
  const normalizedDomain = typeof currentDomain === "string" ? currentDomain.trim().toLowerCase() : "";
  const canManageWorkflow = Boolean(normalizedGuid) && (!normalizedDomain || normalizedDomain === "user");
  const workflowManagementHint = !normalizedGuid
    ? "Save this workflow to the User domain before renaming or deleting it."
    : "Only User-domain workflows can be renamed or deleted from the flow editor.";

  const handleAccordionChange = (category) => (_, isExpanded) => {
    setExpandedCategory(isExpanded ? category : null);
  };

  const handleSaveConfirm = () => {
    const trimmedName = (saveName || "").trim();
    setSaveOpen(false);
    if (!trimmedName) return;
    handleSaveFlow(trimmedName);
  };

  const handleRenameConfirm = () => {
    const trimmedName = (renameName || "").trim();
    setRenameOpen(false);
    if (!trimmedName) return;
    handleRenameFlow(trimmedName);
  };

  const handleDeleteConfirm = () => {
    setDeleteOpen(false);
    handleDeleteFlow();
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
                <Tooltip title="Save the current flow as a Borealis workflow assembly document." placement="right" arrow>
                  <Button
                    fullWidth
                    startIcon={<SaveIcon />}
                    onClick={() => {
                      setSaveName(currentTabName || "workflow");
                      setSaveOpen(true);
                    }}
                    sx={buttonStyle}
                  >
                    Save Workflow
                  </Button>
                </Tooltip>
                <Tooltip
                  title={
                    canManageWorkflow
                      ? "Rename the saved User-domain workflow and update its assembly record."
                      : workflowManagementHint
                  }
                  placement="right"
                  arrow
                >
                  <span style={{ display: "block", width: "100%" }}>
                    <Button
                      fullWidth
                      disabled={!canManageWorkflow}
                      startIcon={<DriveFileRenameOutlineIcon />}
                      onClick={() => {
                        setRenameName(currentTabName || "workflow");
                        setRenameOpen(true);
                      }}
                      sx={buttonStyle}
                    >
                      Rename Workflow
                    </Button>
                  </span>
                </Tooltip>
                <Tooltip
                  title={
                    canManageWorkflow
                      ? "Delete the saved User-domain workflow from assemblies and remove it from the editor."
                      : workflowManagementHint
                  }
                  placement="right"
                  arrow
                >
                  <span style={{ display: "block", width: "100%" }}>
                    <Button
                      fullWidth
                      disabled={!canManageWorkflow}
                      startIcon={<DeleteForeverIcon />}
                      onClick={() => setDeleteOpen(true)}
                      sx={deleteButtonStyle}
                    >
                      Delete Workflow
                    </Button>
                  </span>
                </Tooltip>
                <Tooltip
                  title="Import a workflow assembly document or legacy flat canvas JSON into a new flow tab."
                  placement="right"
                  arrow
                >
                  <Button fullWidth startIcon={<FileOpenIcon />} onClick={handleImportFlow} sx={buttonStyle}>
                    Import Workflow (JSON)
                  </Button>
                </Tooltip>
                <Tooltip
                  title="Export the current tab as a canonical workflow assembly document with encoded workflow data."
                  placement="right"
                  arrow
                >
                  <Button fullWidth startIcon={<SaveAltIcon />} onClick={handleExportFlow} sx={buttonStyle}>
                    Export Workflow (JSON)
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
                {Object.keys(categorizedNodes).length ? (
                  Object.entries(categorizedNodes).map(([category, items]) => (
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
                  ))
                ) : (
                  <Box sx={{ px: 2, py: 1.5 }}>
                    <Typography sx={{ color: "#8a94a6", fontSize: "0.78rem", lineHeight: 1.45 }}>
                      No nodes were loaded into the workflow editor. Check the node registry in{" "}
                      <code>src/App.jsx</code>.
                    </Typography>
                  </Box>
                )}
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
      <SaveWorkflowDialog
        open={saveOpen}
        value={saveName}
        onChange={setSaveName}
        onCancel={() => setSaveOpen(false)}
        onSave={handleSaveConfirm}
      />
      <RenameWorkflowDialog
        open={renameOpen}
        value={renameName}
        onChange={setRenameName}
        onCancel={() => setRenameOpen(false)}
        onSave={handleRenameConfirm}
      />
      <ConfirmDeleteDialog
        open={deleteOpen}
        title="Delete Workflow"
        message={`Delete "${currentTabName || "workflow"}" from the User domain? This cannot be undone.`}
        confirmLabel="Delete"
        onCancel={() => setDeleteOpen(false)}
        onConfirm={handleDeleteConfirm}
      />
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

const deleteButtonStyle = {
  ...buttonStyle,
  color: "#f0b3b3",
  "&:hover": {
    backgroundColor: "rgba(127, 29, 29, 0.28)"
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
