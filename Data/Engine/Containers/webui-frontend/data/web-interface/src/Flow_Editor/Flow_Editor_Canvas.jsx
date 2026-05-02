////////// PROJECT FILE SEPARATION LINE ////////// CODE AFTER THIS LINE ARE FROM: <ProjectRoot>/Data/Engine/Containers/webui-frontend/data/web-interface/src/Flow_Editor/Flow_Editor_Canvas.jsx
import FlowEditorNodeConfig from "./Flow_Editor_Node_Config.jsx";
import NodeConfigurationSidebar from "./Node_Configuration_Sidebar.jsx";
import { workflowCategorizedNodes, workflowNodeTypes } from "./nodeRegistry.js";
import {
  decorateWorkflowEdge,
  getWorkflowEdgePortMetadata,
  getWorkflowRuntimeDisplayLabel,
  WORKFLOW_RUNTIME_EDGE_ROUTES,
} from "./runtimeV1";

import React, { useState, useEffect, useCallback, useRef } from "react";
import ReactFlow, {
  Background,
  addEdge,
  applyNodeChanges,
  applyEdgeChanges,
  useReactFlow
} from "reactflow";

import { Box, Divider, Menu, MenuItem, Tooltip, Typography } from "@mui/material";
import {
  Polyline as PolylineIcon,
  DeleteForever as DeleteForeverIcon,
  Edit as EditIcon,
  NavigateNext as NavigateNextIcon,
} from "@mui/icons-material";

import "reactflow/dist/style.css";

const EMPTY_EDGE_TYPES = {};
const DEFAULT_EDGE_OPTIONS = {
  type: "bezier",
  animated: true,
  style: { strokeDasharray: "6 3", stroke: "#58a6ff" },
};

const ACTION_MENU_PAPER_SX = {
  bgcolor: "rgba(8,12,24,0.96)",
  border: "1px solid rgba(148, 163, 184, 0.35)",
  backdropFilter: "blur(14px)",
  borderRadius: 2,
  minWidth: 288,
  px: 0.8,
  py: 0.8,
};

const ACTION_MENU_COMPACT_PAPER_SX = {
  ...ACTION_MENU_PAPER_SX,
  minWidth: 248,
};

const ACTION_MENU_ITEM_SX = {
  minHeight: 42,
  borderRadius: 1.6,
  color: "#e2e8f0",
  alignItems: "center",
  px: 1,
  py: 0.85,
  position: "relative",
  overflow: "hidden",
  "&:hover": {
    backgroundColor: "rgba(88,166,255,0.12)",
  },
  "&.Mui-selected": {
    backgroundColor: "rgba(88,166,255,0.16)",
  },
  "&.Mui-selected:hover": {
    backgroundColor: "rgba(88,166,255,0.22)",
  },
  "&::before": {
    content: '""',
    position: "absolute",
    left: 0,
    top: 8,
    bottom: 8,
    width: 3,
    borderRadius: 999,
    background: "transparent",
    transition: "background-color 0.16s ease",
  },
  "&:hover::before, &.Mui-selected::before": {
    background: "#58a6ff",
  },
};

const ACTION_MENU_DANGER_ITEM_SX = {
  ...ACTION_MENU_ITEM_SX,
  "&:hover": {
    backgroundColor: "rgba(248,113,113,0.1)",
  },
  "&.Mui-selected": {
    backgroundColor: "rgba(248,113,113,0.14)",
  },
  "&.Mui-selected:hover": {
    backgroundColor: "rgba(248,113,113,0.18)",
  },
  "&:hover::before, &.Mui-selected::before": {
    background: "#58a6ff",
  },
};

const ACTION_MENU_SECTION_LABEL_SX = {
  px: 1.2,
  pt: 0.65,
  pb: 0.45,
  color: "rgba(148,163,184,0.72)",
  fontSize: "0.68rem",
  fontWeight: 700,
  letterSpacing: "0.08em",
  textTransform: "uppercase",
};

const ACTION_MENU_DIVIDER_SX = {
  my: 0.55,
  borderColor: "rgba(148,163,184,0.16)",
};

const ACTION_MENU_HEADER_SX = {
  display: "flex",
  alignItems: "center",
  gap: 1,
  px: 1.1,
  pt: 0.55,
  pb: 0.85,
};

const ACTION_MENU_HEADER_ICON_SX = {
  width: 32,
  height: 32,
  borderRadius: 1.35,
  flexShrink: 0,
  display: "inline-flex",
  alignItems: "center",
  justifyContent: "center",
  border: "1px solid rgba(148,163,184,0.14)",
  background: "rgba(255,255,255,0.04)",
  color: "#8fd3ff",
};

const ACTION_MENU_ROW_ICON_SX = {
  mt: 0.18,
  mr: 1,
  fontSize: 18,
  flexShrink: 0,
};

const ACTION_MENU_LABEL_SX = {
  color: "#e2e8f0",
  fontSize: "0.84rem",
  fontWeight: 500,
  lineHeight: 1.2,
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

const ACTION_MENU_DESCRIPTION_SX = {
  color: "rgba(148,163,184,0.78)",
  fontSize: "0.73rem",
  lineHeight: 1.25,
  mt: 0.25,
};

const ACTION_MENU_TITLE_TRUNCATE_SX = {
  whiteSpace: "nowrap",
  overflow: "hidden",
  textOverflow: "ellipsis",
};

const ACTION_MENU_GROUP_LABELS = {
  primary: "Primary",
  organize: "Organize",
  danger: "Danger Zone",
  view: "View",
};

const ACTION_MENU_GROUP_ORDER = ["primary", "organize", "danger", "view"];

function routeDescriptor(route) {
  const normalized = String(route || WORKFLOW_RUNTIME_EDGE_ROUTES[0]?.value || "").trim().toLowerCase();
  return WORKFLOW_RUNTIME_EDGE_ROUTES.find((entry) => entry.value === normalized) || WORKFLOW_RUNTIME_EDGE_ROUTES[0];
}

function workflowNodeLabel(node) {
  if (!node) return "Workflow Node";
  return getWorkflowRuntimeDisplayLabel(node.type, node.data?.label || node.id);
}

function workflowEdgeNodeLabel(node) {
  return node ? workflowNodeLabel(node) : "Unknown node";
}

function WorkflowContextMenuContent({ subject, actions, onClose }) {
  const groups = ACTION_MENU_GROUP_ORDER
    .map((groupId) => ({
      id: groupId,
      label: ACTION_MENU_GROUP_LABELS[groupId],
      actions: actions.filter((action) => !action.hidden && action.group === groupId),
    }))
    .filter((group) => group.actions.length);
  const SubjectIcon = subject?.Icon || PolylineIcon;

  return (
    <>
      <Box key="context-header" component="li" role="presentation" sx={ACTION_MENU_HEADER_SX}>
        <Box sx={ACTION_MENU_HEADER_ICON_SX}>
          <SubjectIcon sx={{ fontSize: 19, color: "currentColor" }} />
        </Box>
        <Box sx={{ minWidth: 0 }}>
          <Tooltip title={subject?.title || ""} placement="top-start">
            <Typography
              sx={{
                ...ACTION_MENU_TITLE_TRUNCATE_SX,
                color: "#e2e8f0",
                fontSize: "0.88rem",
                fontWeight: 600,
                lineHeight: 1.2,
                maxWidth: 240,
              }}
            >
              {subject?.title || "Workflow"}
            </Typography>
          </Tooltip>
          <Tooltip title={subject?.subtitle || ""} placement="top-start">
            <Typography
              sx={{
                ...ACTION_MENU_TITLE_TRUNCATE_SX,
                color: "rgba(148,163,184,0.82)",
                fontSize: "0.73rem",
                lineHeight: 1.25,
                mt: 0.22,
                maxWidth: 240,
              }}
            >
              {subject?.subtitle || "Context actions"}
            </Typography>
          </Tooltip>
        </Box>
      </Box>
      {groups.map((group) => (
        <React.Fragment key={group.id}>
          <Divider component="li" sx={ACTION_MENU_DIVIDER_SX} />
          <Box component="li" role="presentation" sx={ACTION_MENU_SECTION_LABEL_SX}>
            {group.label}
          </Box>
          {group.actions.map((action) => {
            const IconComponent = action.icon;
            const helperText = action.disabledReason || action.description || "";
            return (
              <MenuItem
                key={action.id}
                selected={Boolean(action.selected)}
                disabled={Boolean(action.disabled)}
                onClick={(event) => {
                  if (action.keepOpen) {
                    action.onClick?.(event);
                    return;
                  }
                  onClose?.();
                  action.onClick?.(event);
                }}
                sx={action.intent === "danger" ? ACTION_MENU_DANGER_ITEM_SX : ACTION_MENU_ITEM_SX}
              >
                {action.dotColor ? (
                  <Box
                    sx={{
                      width: 10,
                      height: 10,
                      borderRadius: "50%",
                      bgcolor: action.dotColor,
                      mr: 1.15,
                      flexShrink: 0,
                      boxShadow: `0 0 0 1px ${action.dotColor}55`,
                    }}
                  />
                ) : (
                  <IconComponent
                    sx={{
                      ...ACTION_MENU_ROW_ICON_SX,
                      color: action.intent === "danger" ? "rgba(248,113,113,0.92)" : "rgba(226,232,240,0.92)",
                    }}
                  />
                )}
                <Box
                  sx={{
                    flex: 1,
                    minWidth: 0,
                    display: "flex",
                    flexDirection: "column",
                    justifyContent: helperText ? "flex-start" : "center",
                  }}
                >
                  <Typography sx={ACTION_MENU_LABEL_SX}>{action.label}</Typography>
                  {helperText ? <Typography sx={ACTION_MENU_DESCRIPTION_SX}>{helperText}</Typography> : null}
                </Box>
                {action.trailingIcon ? action.trailingIcon : null}
              </MenuItem>
            );
          })}
        </React.Fragment>
      ))}
    </>
  );
}

export default function FlowEditorCanvas({
  flowId,
  nodes,
  edges,
  setNodes,
  setEdges,
  readOnly = false,
  nodeRunLookup = {},
  onSelectedNodeChange = null,
}) {
  // Node Configuration Sidebar State
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [selectedNodeId, setSelectedNodeId] = useState(null);

  // Edge Properties Sidebar State
  const [edgeSidebarOpen, setEdgeSidebarOpen] = useState(false);
  const [edgeSidebarEdgeId, setEdgeSidebarEdgeId] = useState(null);

  // Context Menus
  const [nodeContextMenu, setNodeContextMenu] = useState(null); // { mouseX, mouseY, nodeId }
  const [edgeContextMenu, setEdgeContextMenu] = useState(null); // { mouseX, mouseY, edgeId }
  const [edgeRouteMenu, setEdgeRouteMenu] = useState(null); // { anchorEl, edgeId }

  // Drag/snap helpers (untouched)
  const wrapperRef = useRef(null);
  const { screenToFlowPosition } = useReactFlow();
  const [guides, setGuides] = useState([]);
  const [activeGuides, setActiveGuides] = useState([]);
  const movingFlowSize = useRef({ width: 0, height: 0 });
  const stableNodeTypesRef = useRef(workflowNodeTypes);
  const stableEdgeTypesRef = useRef(EMPTY_EDGE_TYPES);

  // ----- Node/Edge Definitions -----
  const selectedNode = nodes.find((n) => n.id === selectedNodeId);
  const selectedEdge = edges.find((e) => e.id === edgeSidebarEdgeId);
  const selectedNodeRun = selectedNodeId ? nodeRunLookup?.[selectedNodeId] || null : null;
  const selectedNodeTitle = selectedNode
    ? getWorkflowRuntimeDisplayLabel(selectedNode.type, selectedNode.data?.label || selectedNode.id)
    : "";
  const contextMenuNode = nodeContextMenu?.nodeId
    ? nodes.find((node) => node.id === nodeContextMenu.nodeId) || null
    : null;
  const contextMenuNodeDef = contextMenuNode
    ? Object.values(workflowCategorizedNodes).flat().find((def) => def.type === contextMenuNode.type) || null
    : null;
  const nodesById = React.useMemo(
    () =>
      (Array.isArray(nodes) ? nodes : []).reduce((acc, node) => {
        if (node?.id) acc[String(node.id)] = node;
        return acc;
      }, {}),
    [nodes]
  );
  const contextMenuEdge = edgeContextMenu?.edgeId
    ? edges.find((edge) => edge.id === edgeContextMenu.edgeId) || null
    : null;
  const contextMenuEdgePorts = contextMenuEdge
    ? getWorkflowEdgePortMetadata(contextMenuEdge, nodesById)
    : {};
  const contextMenuRoute = routeDescriptor(contextMenuEdge?.data?.route_on);

  // --------- Context Menu Handlers ----------
  const handleRightClick = (e, node) => {
    e.preventDefault();
    e.stopPropagation();
    setEdgeContextMenu(null);
    setEdgeRouteMenu(null);
    setNodeContextMenu({ mouseX: e.clientX + 2, mouseY: e.clientY - 6, nodeId: node.id });
  };

  const handleEdgeRightClick = (e, edge) => {
    e.preventDefault();
    e.stopPropagation();
    setNodeContextMenu(null);
    setEdgeRouteMenu(null);
    setEdgeContextMenu({ mouseX: e.clientX + 2, mouseY: e.clientY - 6, edgeId: edge.id });
  };

  // --------- Node Context Menu Actions ---------
  const handleDisconnectAllEdges = (nodeId) => {
    if (readOnly) return;
    setEdges((eds) => eds.filter((e) => e.source !== nodeId && e.target !== nodeId));
    setNodeContextMenu(null);
  };

  const handleRemoveNode = (nodeId) => {
    if (readOnly) return;
    setNodes((nds) => nds.filter((n) => n.id !== nodeId));
    setEdges((eds) => eds.filter((e) => e.source !== nodeId && e.target !== nodeId));
    setNodeContextMenu(null);
  };

  const handleEditNodeProps = (nodeId) => {
    setSelectedNodeId(nodeId);
    setDrawerOpen(true);
    setNodeContextMenu(null);
  };

  // --------- Edge Context Menu Actions ---------
  const handleUnlinkEdge = (edgeId) => {
    if (readOnly) return;
    setEdges((eds) => eds.filter((e) => e.id !== edgeId));
    setEdgeContextMenu(null);
  };

  const handleEditEdgeProps = (edgeId) => {
    setEdgeSidebarEdgeId(edgeId);
    setEdgeSidebarOpen(true);
    setEdgeContextMenu(null);
  };

  const handleOpenEdgeRouteMenu = (event, edgeId) => {
    if (readOnly) return;
    event.stopPropagation();
    setEdgeRouteMenu({ anchorEl: event.currentTarget, edgeId });
  };

  const handleCloseEdgeRouteMenu = () => {
    setEdgeRouteMenu(null);
  };

  const handleQuickSetEdgeRoute = (edgeId, route) => {
    if (readOnly || !edgeId) return;
    setEdges((eds) =>
      eds.map((edge) =>
        edge.id === edgeId
          ? decorateWorkflowEdge(
              {
                ...edge,
                data: {
                  ...(edge.data || {}),
                  route_on: route,
                },
              },
              { nodesById }
            )
          : edge
      )
    );
    setEdgeRouteMenu(null);
    setEdgeContextMenu(null);
  };

  // ----- Sidebar Closing -----
  const handleCloseNodeSidebar = () => {
    setDrawerOpen(false);
    setSelectedNodeId(null);
  };

  const handleCloseEdgeSidebar = () => {
    setEdgeSidebarOpen(false);
    setEdgeSidebarEdgeId(null);
  };

  // ----- Update Edge Callback for Sidebar -----
  const updateEdge = (updatedEdgeObj) => {
    setEdges((eds) =>
      eds.map((e) =>
        e.id === updatedEdgeObj.id
          ? decorateWorkflowEdge({ ...e, ...updatedEdgeObj }, { nodesById })
          : e
      )
    );
  };

  // ----- Drag/Drop, Guides, Node Snap Logic (unchanged) -----
  const computeGuides = useCallback((dragNode) => {
    if (!wrapperRef.current) return;
    const parentRect = wrapperRef.current.getBoundingClientRect();
    const dragEl = wrapperRef.current.querySelector(
      `.react-flow__node[data-id="${dragNode.id}"]`
    );
    if (dragEl) {
      const dr = dragEl.getBoundingClientRect();
      const relLeft   = dr.left   - parentRect.left;
      const relTop    = dr.top    - parentRect.top;
      const relRight  = relLeft   + dr.width;
      const relBottom = relTop    + dr.height;
      const pTL = screenToFlowPosition({ x: dr.left, y: dr.top });
      const pTR = screenToFlowPosition({ x: dr.right, y: dr.top });
      const pBL = screenToFlowPosition({ x: dr.left, y: dr.bottom });
      movingFlowSize.current = { width:  pTR.x - pTL.x, height: pBL.y - pTL.y };
    }
    const lines = [];
    nodes.forEach((n) => {
      if (n.id === dragNode.id) return;
      const el = wrapperRef.current.querySelector(
        `.react-flow__node[data-id="${n.id}"]`
      );
      if (!el) return;
      const r = el.getBoundingClientRect();
      const relLeft   = r.left   - parentRect.left;
      const relTop    = r.top    - parentRect.top;
      const relRight  = relLeft + r.width;
      const relBottom = relTop  + r.height;
      const pTL = screenToFlowPosition({ x: r.left, y: r.top });
      const pTR = screenToFlowPosition({ x: r.right, y: r.top });
      const pBL = screenToFlowPosition({ x: r.left, y: r.bottom });
      lines.push({ xFlow: pTL.x, xPx: relLeft });
      lines.push({ xFlow: pTR.x, xPx: relRight });
      lines.push({ yFlow: pTL.y, yPx: relTop });
      lines.push({ yFlow: pBL.y, yPx: relBottom });
    });
    setGuides(lines);
  }, [nodes, screenToFlowPosition]);

  const onNodeDrag = useCallback((_, node) => {
    const threshold = 5;
    let snapX = null, snapY = null;
    const show = [];
    const { width: fw, height: fh } = movingFlowSize.current;
    guides.forEach((ln) => {
      if (ln.xFlow != null) {
        if (Math.abs(node.position.x - ln.xFlow) < threshold) { snapX = ln.xFlow; show.push({ xPx: ln.xPx }); }
        else if (Math.abs(node.position.x + fw - ln.xFlow) < threshold) { snapX = ln.xFlow - fw; show.push({ xPx: ln.xPx }); }
      }
      if (ln.yFlow != null) {
        if (Math.abs(node.position.y - ln.yFlow) < threshold) { snapY = ln.yFlow; show.push({ yPx: ln.yPx }); }
        else if (Math.abs(node.position.y + fh - ln.yFlow) < threshold) { snapY = ln.yFlow - fh; show.push({ yPx: ln.yPx }); }
      }
    });
    if (snapX !== null || snapY !== null) {
      setNodes((nds) =>
        applyNodeChanges(
          [{
            id: node.id,
            type: "position",
            position: {
              x: snapX !== null ? snapX : node.position.x,
              y: snapY !== null ? snapY : node.position.y
            }
          }],
          nds
        )
      );
      setActiveGuides(show);
    } else {
      setActiveGuides([]);
    }
  }, [guides, setNodes]);

  const onDrop = useCallback((event) => {
    if (readOnly) return;
    event.preventDefault();
    const type = event.dataTransfer.getData("application/reactflow");
    if (!type) return;
    const position = screenToFlowPosition({
      x: event.clientX,
      y: event.clientY,
    });
    const id = "node-" + Date.now();
    const nodeMeta = Object.values(workflowCategorizedNodes).flat().find((n) => n.type === type);
    // Seed config defaults:
    const configDefaults = {};
    (nodeMeta?.config || []).forEach(cfg => {
      if (cfg.defaultValue !== undefined) {
        configDefaults[cfg.key] = cfg.defaultValue;
      }
    });
    const newNode = {
      id,
      type,
      position,
      data: {
        label: nodeMeta?.label || type,
        content: nodeMeta?.content,
        ...configDefaults
      },
      dragHandle: ".borealis-node-header"
    };
    setNodes((nds) => [...nds, newNode]);

  }, [screenToFlowPosition, setNodes, readOnly]);

  const onDragOver = useCallback((event) => {
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
  }, []);

  const onConnect = useCallback((params) => {
    if (readOnly) return;
    setEdges((eds) =>
      addEdge(
        decorateWorkflowEdge(
          {
            ...params,
            type: "bezier",
            data: { route_on: "always" },
          },
          { nodesById }
        ),
        eds
      )
    );
  }, [setEdges, readOnly, nodesById]);

  const onNodesChange = useCallback((changes) => {
    if (readOnly) return;
    setNodes((nds) => applyNodeChanges(changes, nds));
  }, [setNodes, readOnly]);

  const onEdgesChange = useCallback((changes) => {
    if (readOnly) return;
    setEdges((eds) => applyEdgeChanges(changes, eds));
  }, [setEdges, readOnly]);

  useEffect(() => {
    const nodeCountEl = document.getElementById("nodeCount");
    if (nodeCountEl) nodeCountEl.innerText = nodes.length;
  }, [nodes]);

  const nodeDef = selectedNode
    ? Object.values(workflowCategorizedNodes).flat().find((def) => def.type === selectedNode.type)
    : null;

  useEffect(() => {
    if (typeof onSelectedNodeChange === "function") {
      onSelectedNodeChange(selectedNodeId, selectedNodeRun);
    }
  }, [onSelectedNodeChange, selectedNodeId, selectedNodeRun]);

  const nodeMenuSubject = React.useMemo(
    () => ({
      title: workflowNodeLabel(contextMenuNode),
      subtitle: [
        contextMenuNodeDef?.label || contextMenuNode?.type || "Workflow node",
        readOnly ? "Read-only" : "Editable",
      ].filter(Boolean).join(" / "),
      Icon: EditIcon,
    }),
    [contextMenuNode, contextMenuNodeDef, readOnly]
  );

  const nodeMenuActions = React.useMemo(
    () => {
      const nodeId = contextMenuNode?.id || "";
      const readOnlyReason = readOnly ? "Workflow run snapshots are read-only." : "";
      return [
        {
          id: "node-edit",
          group: "primary",
          label: readOnly ? "View Details" : "Edit Properties",
          icon: EditIcon,
          disabled: !nodeId,
          disabledReason: !nodeId ? "Select a workflow node first." : "",
          onClick: () => handleEditNodeProps(nodeId),
        },
        {
          id: "node-disconnect",
          group: "organize",
          label: "Disconnect All Edges",
          icon: PolylineIcon,
          disabled: !nodeId || readOnly,
          disabledReason: !nodeId ? "Select a workflow node first." : readOnlyReason,
          description: "Remove every connection to and from this node.",
          onClick: () => handleDisconnectAllEdges(nodeId),
        },
        {
          id: "node-remove",
          group: "danger",
          label: "Remove Node",
          icon: DeleteForeverIcon,
          intent: "danger",
          disabled: !nodeId || readOnly,
          disabledReason: !nodeId ? "Select a workflow node first." : readOnlyReason,
          description: "Deletes this node and any attached edges.",
          onClick: () => handleRemoveNode(nodeId),
        },
      ];
    },
    [contextMenuNode, handleDisconnectAllEdges, handleEditNodeProps, handleRemoveNode, readOnly]
  );

  const edgeMenuSubject = React.useMemo(
    () => {
      const sourceLabel = workflowEdgeNodeLabel(contextMenuEdgePorts?.sourceNode);
      const targetLabel = workflowEdgeNodeLabel(contextMenuEdgePorts?.targetNode);
      const portLabel = [
        contextMenuEdgePorts?.sourcePort?.label || contextMenuEdge?.sourceHandle || "",
        contextMenuEdgePorts?.targetPort?.label || contextMenuEdge?.targetHandle || "",
      ].filter(Boolean).join(" -> ");
      return {
        title: contextMenuEdge ? `${sourceLabel} -> ${targetLabel}` : "Workflow Edge",
        subtitle: [
          portLabel || "Workflow connection",
          contextMenuEdgePorts?.supportsRouteSelection ? contextMenuRoute?.label : "",
          readOnly ? "Read-only" : "Editable",
        ].filter(Boolean).join(" / "),
        Icon: PolylineIcon,
      };
    },
    [
      contextMenuEdge,
      contextMenuEdgePorts,
      contextMenuRoute,
      readOnly,
    ]
  );

  const edgeMenuActions = React.useMemo(
    () => {
      const edgeId = contextMenuEdge?.id || "";
      const readOnlyReason = readOnly ? "Workflow run snapshots are read-only." : "";
      return [
        {
          id: "edge-edit",
          group: "primary",
          label: "Edit Properties",
          icon: EditIcon,
          disabled: !edgeId || readOnly,
          disabledReason: !edgeId ? "Select a workflow edge first." : readOnlyReason,
          onClick: () => handleEditEdgeProps(edgeId),
        },
        {
          id: "edge-route",
          group: "primary",
          label: "Flow Control",
          icon: PolylineIcon,
          hidden: !contextMenuEdgePorts?.supportsRouteSelection,
          disabled: !edgeId || readOnly,
          disabledReason: !edgeId ? "Select a workflow edge first." : readOnlyReason,
          description: contextMenuRoute?.label ? `Current route: ${contextMenuRoute.label}` : "",
          keepOpen: true,
          trailingIcon: <NavigateNextIcon sx={{ fontSize: 18, color: "#7dd3fc", ml: 1 }} />,
          onClick: (event) => handleOpenEdgeRouteMenu(event, edgeId),
        },
        {
          id: "edge-unlink",
          group: "danger",
          label: "Unlink Edge",
          icon: DeleteForeverIcon,
          intent: "danger",
          disabled: !edgeId || readOnly,
          disabledReason: !edgeId ? "Select a workflow edge first." : readOnlyReason,
          description: "Removes this connection from the workflow.",
          onClick: () => handleUnlinkEdge(edgeId),
        },
      ];
    },
    [
      contextMenuEdge,
      contextMenuEdgePorts,
      contextMenuRoute,
      handleEditEdgeProps,
      handleOpenEdgeRouteMenu,
      handleUnlinkEdge,
      readOnly,
    ]
  );

  const edgeRouteMenuSubject = React.useMemo(
    () => ({
      title: "Flow Control",
      subtitle: contextMenuRoute?.label ? `Current route: ${contextMenuRoute.label}` : "Choose route condition",
      Icon: PolylineIcon,
    }),
    [contextMenuRoute]
  );

  const edgeRouteMenuActions = React.useMemo(
    () =>
      WORKFLOW_RUNTIME_EDGE_ROUTES.map((route) => ({
        id: `edge-route-${route.value}`,
        group: "primary",
        label: route.label,
        icon: PolylineIcon,
        dotColor: route.color,
        selected: route.value === contextMenuRoute?.value,
        onClick: () => handleQuickSetEdgeRoute(edgeRouteMenu?.edgeId, route.value),
      })),
    [contextMenuRoute, edgeRouteMenu, handleQuickSetEdgeRoute]
  );

  // --------- MAIN RENDER ----------
  return (
    <div
      className="flow-editor-container"
      ref={wrapperRef}
      style={{ position: "relative" }}
    >
      {/* Node Config Sidebar */}
      <NodeConfigurationSidebar
        drawerOpen={drawerOpen}
        setDrawerOpen={setDrawerOpen}
        title={selectedNodeTitle}
        nodeData={
          selectedNode && nodeDef
            ? {
                config: nodeDef.config,
                usage_documentation: nodeDef.usage_documentation,
                ...selectedNode.data,
                nodeId: selectedNode.id
              }
            : null
        }
        setNodes={setNodes}
        selectedNode={selectedNode}
        readOnly={readOnly}
        runNodeRecord={selectedNodeRun}
      />

      {/* Edge Properties Sidebar */}
      <FlowEditorNodeConfig
        open={edgeSidebarOpen}
        onClose={handleCloseEdgeSidebar}
        edge={selectedEdge ? { ...selectedEdge } : null}
        edgePortMetadata={selectedEdge ? getWorkflowEdgePortMetadata(selectedEdge, nodesById) : null}
        updateEdge={edge => {
          // Provide id if missing
          if (!edge.id && edgeSidebarEdgeId) edge.id = edgeSidebarEdgeId;
          updateEdge(edge);
        }}
      />

      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={stableNodeTypesRef.current}
        edgeTypes={stableEdgeTypesRef.current}
        onNodesChange={readOnly ? undefined : onNodesChange}
        onEdgesChange={readOnly ? undefined : onEdgesChange}
        onConnect={readOnly ? undefined : onConnect}
        onDrop={readOnly ? undefined : onDrop}
        onDragOver={onDragOver}
        onNodeContextMenu={handleRightClick}
        onEdgeContextMenu={handleEdgeRightClick}
        onNodeClick={(_, node) => {
          setSelectedNodeId(node.id);
          setDrawerOpen(true);
        }}
        defaultViewport={{ x: 0, y: 0, zoom: 1.5 }}
        defaultEdgeOptions={DEFAULT_EDGE_OPTIONS}
        onNodeDragStart={readOnly ? undefined : (_, node) => computeGuides(node)}
        onNodeDrag={readOnly ? undefined : onNodeDrag}
        onNodeDragStop={readOnly ? undefined : () => { setGuides([]); setActiveGuides([]); }}
        nodesDraggable={!readOnly}
        nodesConnectable={!readOnly}
        elementsSelectable
        zoomOnDoubleClick={!readOnly}
        panOnDrag
        deleteKeyCode={readOnly ? null : ["Backspace", "Delete"]}
      >
        <Background id={flowId} variant="lines" gap={65} size={1} color="rgba(255,255,255,0.2)" />
      </ReactFlow>

      {/* Helper lines for snapping */}
      {activeGuides.map((ln, i) =>
        ln.xPx != null ? (
          <div
            key={i}
            className="helper-line helper-line-vertical"
            style={{ left: ln.xPx + "px", top: 0 }}
          />
        ) : (
          <div
            key={i}
            className="helper-line helper-line-horizontal"
            style={{ top: ln.yPx + "px", left: 0 }}
          />
        )
      )}

      {/* Node Context Menu */}
      <Menu
        open={Boolean(nodeContextMenu)}
        onClose={() => setNodeContextMenu(null)}
        anchorReference="anchorPosition"
        anchorPosition={nodeContextMenu ? { top: nodeContextMenu.mouseY, left: nodeContextMenu.mouseX } : undefined}
        PaperProps={{ sx: ACTION_MENU_PAPER_SX }}
      >
        <WorkflowContextMenuContent
          subject={nodeMenuSubject}
          actions={nodeMenuActions}
          onClose={() => setNodeContextMenu(null)}
        />
      </Menu>

      {/* Edge Context Menu */}
      <Menu
        open={Boolean(edgeContextMenu)}
        onClose={() => {
          setEdgeContextMenu(null);
          setEdgeRouteMenu(null);
        }}
        anchorReference="anchorPosition"
        anchorPosition={edgeContextMenu ? { top: edgeContextMenu.mouseY, left: edgeContextMenu.mouseX } : undefined}
        PaperProps={{ sx: ACTION_MENU_PAPER_SX }}
      >
        <WorkflowContextMenuContent
          subject={edgeMenuSubject}
          actions={edgeMenuActions}
          onClose={() => {
            setEdgeContextMenu(null);
            setEdgeRouteMenu(null);
          }}
        />
      </Menu>

      <Menu
        open={Boolean(edgeRouteMenu)}
        anchorEl={edgeRouteMenu?.anchorEl || null}
        onClose={handleCloseEdgeRouteMenu}
        PaperProps={{ sx: ACTION_MENU_COMPACT_PAPER_SX }}
        anchorOrigin={{ vertical: "top", horizontal: "right" }}
        transformOrigin={{ vertical: "top", horizontal: "left" }}
      >
        <WorkflowContextMenuContent
          subject={edgeRouteMenuSubject}
          actions={edgeRouteMenuActions}
          onClose={handleCloseEdgeRouteMenu}
        />
      </Menu>
    </div>
  );
}
