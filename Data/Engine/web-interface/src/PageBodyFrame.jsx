import React from "react";
import { Box } from "@mui/material";

export const PAGE_BODY_FRAME_BORDER_COLOR = "rgba(148, 163, 184, 0.35)";
export const PAGE_BODY_FRAME_DIVIDER_COLOR = "rgba(148, 163, 184, 0.16)";
export const PAGE_BODY_FRAME_BACKGROUND =
  "linear-gradient(165deg, rgba(2,6,23,0.9), rgba(8,12,32,0.85))";
export const PAGE_BODY_FRAME_SHADOW = "0 20px 60px rgba(2,8,23,0.85)";

export const PAGE_BODY_FRAME_OUTER_SX = {
  px: 2,
  pb: 2,
  display: "flex",
  flexDirection: "column",
  minWidth: 0,
};

export const PAGE_BODY_FRAME_FILL_SX = {
  flexGrow: 1,
  minHeight: 0,
};

export const PAGE_BODY_FRAME_SHELL_SX = {
  borderRadius: 3,
  border: `1px solid ${PAGE_BODY_FRAME_BORDER_COLOR}`,
  background: PAGE_BODY_FRAME_BACKGROUND,
  boxShadow: PAGE_BODY_FRAME_SHADOW,
  overflow: "hidden",
  position: "relative",
  display: "flex",
  flexDirection: "column",
  minWidth: 0,
};

export const PAGE_BODY_FRAME_MAIN_SX = {
  display: "flex",
  flexDirection: "column",
  flexGrow: 1,
  minHeight: 0,
  minWidth: 0,
};

export const PAGE_BODY_FRAME_GRID_MAIN_SX = {
  ...PAGE_BODY_FRAME_MAIN_SX,
  p: 2,
};

export const PAGE_BODY_FRAME_STACK_SX = {
  px: 2,
  pt: 2,
  pb: 1.5,
  display: "flex",
  flexDirection: "column",
  gap: 1.5,
  flexShrink: 0,
  borderBottom: `1px solid ${PAGE_BODY_FRAME_DIVIDER_COLOR}`,
};

export const PAGE_BODY_FRAME_SPLIT_LAYOUT_SX = {
  ...PAGE_BODY_FRAME_MAIN_SX,
  flexDirection: { xs: "column", lg: "row" },
};

export const PAGE_BODY_FRAME_SPLIT_SIDEBAR_SX = {
  width: { xs: "100%", lg: 360 },
  maxWidth: "100%",
  flexShrink: 0,
  p: 3,
  display: "flex",
  flexDirection: "column",
  gap: 2,
  minWidth: 0,
  borderBottom: {
    xs: `1px solid ${PAGE_BODY_FRAME_DIVIDER_COLOR}`,
    lg: "none",
  },
  borderRight: {
    xs: "none",
    lg: `1px solid ${PAGE_BODY_FRAME_DIVIDER_COLOR}`,
  },
};

export const PAGE_BODY_FRAME_SPLIT_MAIN_SX = {
  ...PAGE_BODY_FRAME_MAIN_SX,
  p: 3,
  gap: 2,
};

export const PAGE_BODY_FRAME_CONTENT_PANEL_SX = {
  ...PAGE_BODY_FRAME_MAIN_SX,
  p: 3,
};

export default function PageBodyFrame({
  variant = "grid",
  children,
  stack,
  sidebar,
  main,
  fillHeight = true,
}) {
  const content = main ?? children;
  const fillStyles = fillHeight ? PAGE_BODY_FRAME_FILL_SX : null;
  const shellSx = fillStyles ? [PAGE_BODY_FRAME_SHELL_SX, fillStyles] : PAGE_BODY_FRAME_SHELL_SX;
  const outerSx = fillStyles ? [PAGE_BODY_FRAME_OUTER_SX, fillStyles] : PAGE_BODY_FRAME_OUTER_SX;

  if (variant === "split_tool") {
    return (
      <Box sx={outerSx}>
        <Box sx={shellSx}>
          <Box sx={PAGE_BODY_FRAME_SPLIT_LAYOUT_SX}>
            {sidebar ? <Box sx={PAGE_BODY_FRAME_SPLIT_SIDEBAR_SX}>{sidebar}</Box> : null}
            <Box sx={PAGE_BODY_FRAME_SPLIT_MAIN_SX}>{content}</Box>
          </Box>
        </Box>
      </Box>
    );
  }

  if (variant === "content_panel") {
    return (
      <Box sx={outerSx}>
        <Box sx={shellSx}>
          <Box sx={PAGE_BODY_FRAME_CONTENT_PANEL_SX}>{content}</Box>
        </Box>
      </Box>
    );
  }

  if (variant === "grid_with_stack") {
    return (
      <Box sx={outerSx}>
        <Box sx={shellSx}>
          {stack ? <Box sx={PAGE_BODY_FRAME_STACK_SX}>{stack}</Box> : null}
          <Box
            sx={[
              PAGE_BODY_FRAME_MAIN_SX,
              {
                px: 2,
                pb: 2,
                pt: stack ? 0 : 2,
              },
            ]}
          >
            {content}
          </Box>
        </Box>
      </Box>
    );
  }

  return (
    <Box sx={outerSx}>
      <Box sx={shellSx}>
        <Box sx={PAGE_BODY_FRAME_GRID_MAIN_SX}>{content}</Box>
      </Box>
    </Box>
  );
}
