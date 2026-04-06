import React, { useState } from "react";
import {
  Accordion,
  AccordionSummary,
  AccordionDetails,
  Tooltip,
  Typography,
  Box,
  Chip,
  Divider,
  ListItemButton,
  ListItemText,
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
  ChevronRight as ChevronRightIcon,
  PlayArrowRounded as PlayArrowRoundedIcon,
  WebhookRounded as WebhookRoundedIcon,
} from "@mui/icons-material";
import { ConfirmDeleteDialog, RenameWorkflowDialog, SaveWorkflowDialog } from "../Dialogs";
import WorkflowWebhookDialog from "./Workflow_Webhook_Dialog.jsx";
import { workflowCategorizedNodes } from "./nodeRegistry.js";

const COLORS = {
  cyan: "#7db7ff",
  violet: "#c084fc",
  text: "#cbd5e1",
  textActive: "#e6f2ff",
  matte: "#0f141c",
  line: "rgba(125,183,255,0.14)",
  hover: "rgba(255,255,255,0.05)",
  itemActiveBg:
    "linear-gradient(90deg, rgba(125,183,255,0.14) 0%, rgba(125,183,255,0.06) 55%, rgba(125,183,255,0.00) 100%)",
};

function Section({ title, expanded, onChange, collapsed, children }) {
  return (
    <Accordion
      expanded={collapsed ? true : expanded}
      onChange={(_, isExpanded) => {
        if (collapsed) return;
        onChange?.(isExpanded);
      }}
      square
      disableGutters
      sx={{
        "&:before": { display: "none" },
        m: 0,
        bgcolor: "transparent",
        border: 0,
      }}
    >
      <AccordionSummary
        expandIcon={<ExpandMoreIcon sx={{ color: COLORS.cyan }} />}
        sx={{
          minHeight: 38,
          "& .MuiAccordionSummary-content": {
            m: 0,
            py: 0.5,
            display: collapsed ? "none" : "flex",
          },
          display: collapsed ? "none" : "flex",
          backgroundColor: "rgba(255,255,255,0.02)",
          borderTopRightRadius: 8,
          borderBottomRightRadius: 8,
          px: collapsed ? 1 : 1.5,
        }}
        title={collapsed ? title : undefined}
      >
        {collapsed ? null : (
          <Typography
            sx={{
              fontSize: "0.85rem",
              color: COLORS.cyan,
              fontWeight: 700,
              letterSpacing: 0.3,
            }}
          >
            {title}
          </Typography>
        )}
      </AccordionSummary>
      <AccordionDetails sx={{ p: 0 }}>{children}</AccordionDetails>
    </Accordion>
  );
}

function SidebarItem({
  icon,
  label,
  onClick,
  tooltip,
  disabled = false,
  collapsed = false,
  tone = "default",
  draggable = false,
  onDragStart = null,
  trailing = null,
}) {
  const destructive = tone === "danger";
  const iconColor = destructive ? "#fca5a5" : "#8fbfff";
  const labelColor = destructive ? "#f0b3b3" : COLORS.text;
  const disabledColor = destructive ? "rgba(252,165,165,0.45)" : "rgba(203,213,225,0.45)";
  const hoverBackground = destructive
    ? "linear-gradient(90deg, rgba(248,113,113,0.14) 0%, rgba(127,29,29,0.14) 55%, rgba(127,29,29,0.00) 100%)"
    : COLORS.hover;

  const item = (
    <ListItemButton
      onClick={disabled ? undefined : onClick}
      disabled={disabled}
      draggable={draggable}
      onDragStart={onDragStart}
      sx={{
        pl: collapsed ? 1.5 : 2,
        pr: collapsed ? 1.5 : 2,
        py: 1,
        color: disabled ? disabledColor : labelColor,
        position: "relative",
        background: "transparent",
        borderTopRightRadius: 0,
        borderBottomRightRadius: 0,
        justifyContent: collapsed ? "center" : "flex-start",
        transition:
          "background 160ms ease, box-shadow 160ms ease, color 160ms ease, transform 120ms ease",
        "&:hover": {
          background: disabled ? "transparent" : hoverBackground,
        },
        "&:active": disabled ? undefined : { transform: "translateY(0.5px)" },
        "&.Mui-disabled": {
          opacity: 1,
          color: disabledColor,
        },
      }}
      title={collapsed ? label : undefined}
    >
      {icon ? (
        <Box
          sx={{
            mr: collapsed ? 0 : 1,
            display: "flex",
            alignItems: "center",
            color: disabled ? disabledColor : iconColor,
            transition: "color 160ms ease",
            minWidth: collapsed ? "auto" : 18,
          }}
        >
          {icon}
        </Box>
      ) : null}
      <ListItemText
        primary={label}
        sx={{ display: collapsed ? "none" : "block", minWidth: 0 }}
        primaryTypographyProps={{
          fontSize: "0.8rem",
          fontWeight: 400,
          letterSpacing: 0.2,
          color: disabled ? disabledColor : labelColor,
          whiteSpace: "nowrap",
          overflow: "hidden",
          textOverflow: "ellipsis",
        }}
      />
      {!collapsed && trailing ? (
        <Box
          sx={{
            display: "flex",
            alignItems: "center",
            color: disabled ? disabledColor : "rgba(143,191,255,0.7)",
            ml: 1,
          }}
        >
          {trailing}
        </Box>
      ) : null}
    </ListItemButton>
  );

  if (!tooltip) {
    return item;
  }

  return (
    <Tooltip
      title={
        <span style={{ whiteSpace: "pre-line", wordWrap: "break-word", maxWidth: 240 }}>
          {tooltip}
        </span>
      }
      placement="right"
      arrow
    >
      <span style={{ display: "block", width: "100%" }}>{item}</span>
    </Tooltip>
  );
}

function NodeCategory({
  category,
  items,
  expanded,
  onChange,
  collapsed,
}) {
  return (
    <Accordion
      square
      expanded={collapsed ? true : expanded}
      onChange={(_, isExpanded) => {
        if (collapsed) return;
        onChange?.(isExpanded);
      }}
      disableGutters
      sx={{
        "&:before": { display: "none" },
        m: 0,
        bgcolor: "transparent",
        border: 0,
      }}
    >
      <AccordionSummary
        expandIcon={<ExpandMoreIcon sx={{ color: COLORS.cyan }} />}
        sx={{
          minHeight: 34,
          "& .MuiAccordionSummary-content": {
            m: 0,
            py: 0.35,
            display: collapsed ? "none" : "flex",
          },
          display: collapsed ? "none" : "flex",
          backgroundColor: "rgba(255,255,255,0.02)",
          borderTopRightRadius: 8,
          borderBottomRightRadius: 8,
          px: collapsed ? 1 : 1.5,
        }}
        title={collapsed ? category : undefined}
      >
        {collapsed ? null : (
          <Typography
            sx={{
              fontSize: "0.8rem",
              color: COLORS.text,
              fontWeight: 500,
              letterSpacing: 0.2,
            }}
          >
            {category}
          </Typography>
        )}
      </AccordionSummary>
      <AccordionDetails sx={{ p: 0 }}>
        {items.map((nodeDef) => (
          <SidebarItem
            key={`${category}-${nodeDef.type}`}
            collapsed={collapsed}
            icon={<PolylineIcon fontSize="small" />}
            trailing={<DragIndicatorIcon sx={{ fontSize: 16 }} />}
            label={nodeDef.label}
            tooltip={nodeDef.description || "Drag and drop this node into the workflow editor."}
            draggable
            onDragStart={(event) => {
              event.dataTransfer.setData("application/reactflow", nodeDef.type);
              event.dataTransfer.effectAllowed = "move";
            }}
          />
        ))}
      </AccordionDetails>
    </Accordion>
  );
}

export default function FlowEditorSidebar({
  handleExportFlow,
  handleImportFlow,
  handleSaveFlow,
  handleRenameFlow,
  handleDeleteFlow,
  handleTriggerWorkflow,
  fileInputRef,
  onFileInputChange,
  currentTabName,
  currentAssemblyGuid,
  currentDomain,
  readOnly = false,
  workflowRun = null,
}) {
  const [expandedCategory, setExpandedCategory] = useState(null);
  const [collapsed, setCollapsed] = useState(false);
  const [workflowSectionExpanded, setWorkflowSectionExpanded] = useState(true);
  const [nodeSectionExpanded, setNodeSectionExpanded] = useState(true);
  const [saveOpen, setSaveOpen] = useState(false);
  const [saveName, setSaveName] = useState("");
  const [renameOpen, setRenameOpen] = useState(false);
  const [renameName, setRenameName] = useState("");
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [webhookDialogOpen, setWebhookDialogOpen] = useState(false);

  const normalizedGuid = typeof currentAssemblyGuid === "string" ? currentAssemblyGuid.trim() : "";
  const normalizedDomain = typeof currentDomain === "string" ? currentDomain.trim().toLowerCase() : "";
  const canManageWorkflow = Boolean(normalizedGuid) && (!normalizedDomain || normalizedDomain === "user");
  const workflowManagementHint = !normalizedGuid
    ? "Save this workflow to the User domain before renaming or deleting it."
    : "Only User-domain workflows can be renamed or deleted from the flow editor.";

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

  const handleCategoryChange = (category, isExpanded) => {
    setExpandedCategory(isExpanded ? category : null);
  };

  return (
    <Box
      sx={{
        width: collapsed ? 45 : 260,
        flexShrink: 0,
        position: "relative",
        zIndex: 2,
        display: "flex",
        flexDirection: "column",
        overflow: "hidden",
        background:
          "linear-gradient(180deg, rgba(64,164,255,0.05) 0%, rgba(192,132,252,0.04) 100%), " +
          COLORS.matte,
        borderRight: `1px solid ${COLORS.line}`,
        backdropFilter: "blur(8px) saturate(130%)",
        height: "100%",
      }}
    >
      <Divider sx={{ borderColor: COLORS.line }} />

      <Box sx={{ flex: 1, overflowY: "auto", p: 0.25 }}>
        <Section
          title={readOnly ? "Run Snapshot" : "Workflows"}
          expanded={workflowSectionExpanded}
          onChange={setWorkflowSectionExpanded}
          collapsed={collapsed}
        >
          {readOnly ? (
            <>
              {!collapsed ? (
                <Box sx={{ px: 2, py: 1.5, display: "flex", flexDirection: "column", gap: 0.9 }}>
                  <Typography sx={{ color: "#e2e8f0", fontSize: "0.85rem", fontWeight: 700 }}>
                    {workflowRun?.workflow_name || currentTabName || "Workflow Run"}
                  </Typography>
                  <Typography sx={{ color: "#94a3b8", fontSize: "0.76rem", lineHeight: 1.45 }}>
                    This canvas is read-only and reflects the immutable server-side workflow snapshot for the selected run.
                  </Typography>
                  {workflowRun?.status ? (
                    <Chip
                      size="small"
                      label={workflowRun.status}
                      sx={{
                        width: "fit-content",
                        color: "#7dd3fc",
                        border: "1px solid rgba(125,211,252,0.28)",
                        bgcolor: "rgba(14,116,144,0.16)",
                        borderRadius: 999,
                      }}
                    />
                  ) : null}
                </Box>
              ) : null}
              <SidebarItem
                collapsed={collapsed}
                icon={<WebhookRoundedIcon fontSize="small" />}
                label="Webhook Management"
                tooltip="Define webhooks to trigger this saved workflow."
                disabled={!normalizedGuid}
                onClick={() => setWebhookDialogOpen(true)}
              />
            </>
          ) : (
            <>
              <SidebarItem
                collapsed={collapsed}
                icon={<SaveIcon fontSize="small" />}
                label="Save Workflow"
                tooltip="Save the current flow as a Borealis workflow assembly document."
                onClick={() => {
                  setSaveName(currentTabName || "workflow");
                  setSaveOpen(true);
                }}
              />
              <SidebarItem
                collapsed={collapsed}
                icon={<PlayArrowRoundedIcon fontSize="small" />}
                label="Trigger Workflow"
                tooltip="Save the current workflow, launch it, and open a read-only run snapshot."
                onClick={() => handleTriggerWorkflow?.()}
              />
              <SidebarItem
                collapsed={collapsed}
                icon={<WebhookRoundedIcon fontSize="small" />}
                label="Webhook Management"
                tooltip={
                  normalizedGuid
                    ? "Define webhooks to trigger this workflow."
                    : "Save this workflow first so Borealis can create opaque webhook URLs for it."
                }
                disabled={!normalizedGuid}
                onClick={() => setWebhookDialogOpen(true)}
              />
              {!collapsed ? (
                <SidebarItem
                  collapsed={collapsed}
                  icon={<DriveFileRenameOutlineIcon fontSize="small" />}
                  label="Rename Workflow"
                  tooltip={
                    canManageWorkflow
                      ? "Rename the saved User-domain workflow and update its assembly record."
                      : workflowManagementHint
                  }
                  disabled={!canManageWorkflow}
                  onClick={() => {
                    setRenameName(currentTabName || "workflow");
                    setRenameOpen(true);
                  }}
                />
              ) : null}
              <SidebarItem
                collapsed={collapsed}
                icon={<DeleteForeverIcon fontSize="small" />}
                label="Delete Workflow"
                tooltip={
                  canManageWorkflow
                    ? "Delete the saved User-domain workflow from assemblies and remove it from the editor."
                    : workflowManagementHint
                }
                disabled={!canManageWorkflow}
                onClick={() => setDeleteOpen(true)}
                tone="danger"
              />
              <SidebarItem
                collapsed={collapsed}
                icon={<FileOpenIcon fontSize="small" />}
                label="Import Workflow (JSON)"
                tooltip="Import a workflow assembly document or legacy flat canvas JSON into a new flow tab."
                onClick={handleImportFlow}
              />
              <SidebarItem
                collapsed={collapsed}
                icon={<SaveAltIcon fontSize="small" />}
                label="Export Workflow (JSON)"
                tooltip="Export the current tab as a canonical workflow assembly document with encoded workflow data."
                onClick={handleExportFlow}
              />
            </>
          )}
        </Section>

        {!readOnly && !collapsed ? (
          <Section
            title="Nodes"
            expanded={nodeSectionExpanded}
            onChange={setNodeSectionExpanded}
            collapsed={collapsed}
          >
            {Object.keys(workflowCategorizedNodes).length ? (
              Object.entries(workflowCategorizedNodes).map(([category, items]) => (
                <NodeCategory
                  key={category}
                  category={category}
                  items={items}
                  expanded={expandedCategory === category}
                  onChange={(isExpanded) => handleCategoryChange(category, isExpanded)}
                  collapsed={collapsed}
                />
              ))
            ) : (
              <Box sx={{ px: 2, py: 1.5 }}>
                <Typography sx={{ color: "#8a94a6", fontSize: "0.78rem", lineHeight: 1.45 }}>
                  No nodes were loaded into the workflow editor. Check <code>Flow_Editor/nodeRegistry.js</code>.
                </Typography>
              </Box>
            )}
          </Section>
        ) : null}

        <input
          type="file"
          accept=".json,application/json"
          style={{ display: "none" }}
          ref={fileInputRef}
          onChange={onFileInputChange}
        />
      </Box>

      <Box sx={{ px: 1, pb: 1 }}>
        <Box
          component="button"
          type="button"
          onClick={() => setCollapsed((prev) => !prev)}
          aria-label={collapsed ? "Expand node sidebar" : "Collapse node sidebar"}
          sx={{
            width: "100%",
            height: 28,
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            background: "rgba(255,255,255,0.04)",
            border: `1px solid ${COLORS.line}`,
            borderRadius: 6,
            color: COLORS.cyan,
            cursor: "pointer",
            transition: "background 160ms ease, transform 120ms ease",
            "&:hover": {
              background: "rgba(255,255,255,0.08)",
            },
            "&:active": {
              transform: "translateY(1px)",
            },
          }}
        >
          {collapsed ? <ChevronRightIcon fontSize="small" /> : <ChevronLeftIcon fontSize="small" />}
        </Box>
      </Box>

      <Divider sx={{ borderColor: COLORS.line }} />

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
      <WorkflowWebhookDialog
        open={webhookDialogOpen}
        onClose={() => setWebhookDialogOpen(false)}
        workflowGuid={normalizedGuid}
        workflowName={currentTabName || "workflow"}
      />
    </Box>
  );
}
