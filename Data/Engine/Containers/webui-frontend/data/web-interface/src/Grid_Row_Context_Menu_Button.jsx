import React, { useCallback } from "react";
import { Box, IconButton, Tooltip } from "@mui/material";
import MoreVertIcon from "@mui/icons-material/MoreVert";

export const ROW_CONTEXT_MENU_COL_ID = "__row_context_menu";
export const ROW_CONTEXT_MENU_COLUMN_WIDTH = 52;

const ROW_CONTEXT_MENU_CELL_SX = {
  width: "100%",
  height: "100%",
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
};

export const ROW_CONTEXT_MENU_BUTTON_SX = {
  width: 30,
  height: 30,
  borderRadius: "50%",
  color: "rgba(226,232,240,0.86)",
  border: "1px solid rgba(148,163,184,0.2)",
  background: "rgba(8,12,24,0.52)",
  transition: "background-color 0.16s ease, border-color 0.16s ease, color 0.16s ease",
  "&:hover": {
    color: "#f8fbff",
    borderColor: "rgba(125,211,252,0.55)",
    background: "rgba(14,22,42,0.92)",
  },
  "&:focus-visible": {
    color: "#f8fbff",
    borderColor: "rgba(125,211,252,0.72)",
    boxShadow: "0 0 0 2px rgba(125,211,252,0.18)",
  },
};

function eventWithFallbackCoordinates(event) {
  const clientX = Number(event?.clientX || 0);
  const clientY = Number(event?.clientY || 0);
  if (clientX || clientY || !event?.currentTarget?.getBoundingClientRect) {
    return event;
  }
  const rect = event.currentTarget.getBoundingClientRect();
  return {
    clientX: rect.left + rect.width / 2,
    clientY: rect.top + rect.height / 2,
    currentTarget: event.currentTarget,
    target: event.target,
    preventDefault: () => event.preventDefault?.(),
    stopPropagation: () => event.stopPropagation?.(),
  };
}

export function GridRowContextMenuButtonCell({
  params = {},
  onOpenContextMenu,
  tooltip = "Row Actions",
  disabled = false,
}) {
  const row = params?.data || null;
  const label = typeof tooltip === "function" ? tooltip(row, params) : tooltip;
  const resolvedLabel = String(label || "Row Actions");

  const handlePointerDown = useCallback((event) => {
    event.preventDefault();
    event.stopPropagation();
  }, []);

  const handleClick = useCallback(
    (event) => {
      event.preventDefault();
      event.stopPropagation();
      if (!row || disabled) return;
      onOpenContextMenu?.(eventWithFallbackCoordinates(event), row, params?.node || null, params);
    },
    [disabled, onOpenContextMenu, params, row]
  );

  if (!row) return null;

  return (
    <Box sx={ROW_CONTEXT_MENU_CELL_SX}>
      <Tooltip title={resolvedLabel} placement="left" arrow>
        <span>
          <IconButton
            aria-label={resolvedLabel}
            size="small"
            disabled={disabled}
            onMouseDown={handlePointerDown}
            onClick={handleClick}
            sx={ROW_CONTEXT_MENU_BUTTON_SX}
          >
            <MoreVertIcon fontSize="small" />
          </IconButton>
        </span>
      </Tooltip>
    </Box>
  );
}

export function buildRowContextMenuColumnDef(onOpenContextMenu, options = {}) {
  const width = Number(options.width || ROW_CONTEXT_MENU_COLUMN_WIDTH);
  const tooltip = options.tooltip || "Row Actions";
  return {
    colId: options.colId || ROW_CONTEXT_MENU_COL_ID,
    headerName: options.headerName || "",
    width,
    minWidth: width,
    maxWidth: width,
    flex: 0,
    pinned: "right",
    lockPinned: true,
    lockPosition: true,
    sortable: false,
    filter: false,
    resizable: false,
    suppressMovable: true,
    suppressHeaderMenuButton: true,
    suppressHeaderContextMenu: true,
    cellClass: "row-context-menu-cell",
    cellStyle: {
      padding: 0,
      display: "flex",
      alignItems: "center",
      justifyContent: "center",
    },
    cellRendererParams: {
      suppressMouseEventHandling: () => true,
    },
    cellRenderer: (params) => (
      <GridRowContextMenuButtonCell
        params={params}
        onOpenContextMenu={onOpenContextMenu}
        tooltip={tooltip}
        disabled={Boolean(options.disabled)}
      />
    ),
  };
}
