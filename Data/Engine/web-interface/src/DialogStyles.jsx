import React from "react";
import { Box, Typography } from "@mui/material";

export const DIALOG_MAGIC_UI = {
  panelBg:
    "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
    "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), rgba(8,12,24,0.96)",
  inputBg: "rgba(5,10,24,0.88)",
  panelBorder: "rgba(148,163,184,0.3)",
  panelBorderStrong: "rgba(125,211,252,0.26)",
  textBright: "#e2e8f0",
  textMuted: "#94a3b8",
  accentA: "#7dd3fc",
  accentB: "#c084fc",
  danger: "#ff9aa5",
  glow: "0 24px 60px rgba(2,8,23,0.72)",
};

export const DIALOG_PAPER_SX = {
  borderRadius: 3,
  background: DIALOG_MAGIC_UI.panelBg,
  backdropFilter: "blur(18px)",
  border: `1px solid ${DIALOG_MAGIC_UI.panelBorder}`,
  boxShadow: DIALOG_MAGIC_UI.glow,
  color: DIALOG_MAGIC_UI.textBright,
  overflow: "hidden",
};

export const DIALOG_TITLE_SX = {
  px: 3,
  pt: 3,
  pb: 0.75,
  background: "transparent",
};

export const DIALOG_CONTENT_SX = {
  px: 3,
  pt: 2.25,
  pb: 2.5,
  background: "transparent",
  overflow: "visible",
};

export const DIALOG_ACTIONS_SX = {
  px: 3,
  pt: 0.5,
  pb: 2.5,
  gap: 1,
  background: "transparent",
};

export const DIALOG_BODY_TEXT_SX = {
  color: DIALOG_MAGIC_UI.textMuted,
  lineHeight: 1.6,
};

export const DIALOG_INPUT_SX = {
  "& .MuiOutlinedInput-root, & .MuiInputBase-root": {
    background: DIALOG_MAGIC_UI.inputBg,
    color: DIALOG_MAGIC_UI.textBright,
    borderRadius: 2.5,
    minHeight: 42,
    transition: "border-color 160ms ease, box-shadow 160ms ease, background 160ms ease",
    "& fieldset": {
      borderColor: "rgba(148,163,184,0.28)",
    },
    "&:hover fieldset": {
      borderColor: "rgba(125,211,252,0.48)",
    },
    "&.Mui-focused fieldset": {
      borderColor: DIALOG_MAGIC_UI.accentB,
      boxShadow: "0 0 0 1px rgba(192,132,252,0.28)",
    },
  },
  "& .MuiOutlinedInput-input, & .MuiInputBase-input": {
    padding: "11px 13px",
    fontSize: "0.95rem",
    lineHeight: 1.4,
  },
  "& .MuiOutlinedInput-inputMultiline": {
    padding: "11px 13px",
  },
  "& .MuiInputLabel-root": {
    color: DIALOG_MAGIC_UI.textMuted,
    top: "50%",
    transform: "translate(14px, -50%) scale(1)",
    transformOrigin: "top left",
    zIndex: 2,
  },
  "& .MuiInputLabel-root.Mui-focused": {
    color: DIALOG_MAGIC_UI.accentA,
  },
  "& .MuiInputLabel-root.MuiInputLabel-shrink": {
    top: 0,
    transform: "translate(14px, -6px) scale(0.75)",
  },
  "& .MuiFormHelperText-root": {
    color: DIALOG_MAGIC_UI.textMuted,
    mx: 0.5,
    mt: 0.8,
  },
};

export const DIALOG_SELECT_SX = {
  ...DIALOG_INPUT_SX,
  "& .MuiSelect-select": {
    minHeight: "42px",
    padding: "10px 13px !important",
    display: "flex",
    alignItems: "center",
  },
};

export const DIALOG_BUTTON_SX = {
  borderRadius: 999,
  px: 2,
  minHeight: 38,
  textTransform: "none",
  fontWeight: 600,
  fontSize: "0.92rem",
  color: DIALOG_MAGIC_UI.textBright,
  border: `1px solid ${DIALOG_MAGIC_UI.panelBorder}`,
  background: "rgba(5,10,24,0.84)",
  transition: "background 160ms ease, border-color 160ms ease, color 160ms ease, transform 120ms ease",
  "&:hover": {
    background: "rgba(8,14,30,0.92)",
    borderColor: "rgba(125,211,252,0.46)",
  },
  "&:active": {
    transform: "translateY(0.5px)",
  },
  "&.Mui-disabled": {
    color: "rgba(148,163,184,0.72)",
    borderColor: "rgba(148,163,184,0.18)",
    background: "rgba(15,23,42,0.52)",
  },
};

export const DIALOG_PRIMARY_BUTTON_SX = {
  ...DIALOG_BUTTON_SX,
  color: "#06101d",
  borderColor: "transparent",
  backgroundImage: "linear-gradient(135deg, #7dd3fc 0%, #c084fc 100%)",
  "&:hover": {
    backgroundImage: "linear-gradient(135deg, #91dcff 0%, #cfa0ff 100%)",
    borderColor: "transparent",
  },
};

export const DIALOG_DANGER_BUTTON_SX = {
  ...DIALOG_BUTTON_SX,
  color: DIALOG_MAGIC_UI.danger,
  borderColor: "rgba(244,63,94,0.38)",
  background: "rgba(44,8,22,0.6)",
  "&:hover": {
    background: "rgba(58,10,28,0.76)",
    borderColor: "rgba(251,113,133,0.58)",
  },
  "&.Mui-disabled": {
    color: "rgba(255,154,165,0.48)",
    borderColor: "rgba(244,63,94,0.16)",
    background: "rgba(44,8,22,0.24)",
  },
};

export function DialogHeaderBlock({ title, subtitle }) {
  return (
    <Box sx={{ minWidth: 0 }}>
      <Typography sx={{ fontWeight: 700, fontSize: "1rem", lineHeight: 1.2, color: DIALOG_MAGIC_UI.textBright }}>
        {title}
      </Typography>
      {subtitle ? (
        <Typography sx={{ mt: 0.55, fontSize: "0.84rem", lineHeight: 1.45, color: DIALOG_MAGIC_UI.textMuted }}>
          {subtitle}
        </Typography>
      ) : null}
    </Box>
  );
}
