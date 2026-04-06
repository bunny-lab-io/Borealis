import { createTheme } from "@mui/material";

export const darkTheme = createTheme({
  palette: {
    mode: "dark",
    background: { default: "#121212", paper: "#1e1e1e" },
    text: { primary: "#ffffff" },
  },
  components: {
    MuiTooltip: {
      styleOverrides: {
        tooltip: {
          backgroundColor: "#2a2a2a",
          color: "#ccc",
          fontSize: "0.75rem",
          border: "1px solid #444",
        },
        arrow: { color: "#2a2a2a" },
      },
    },
  },
});

export const APP_AURORA_BACKGROUND =
  "radial-gradient(120% 120% at 0% 0%, rgba(76, 186, 255, 0.16), transparent 55%), " +
  "radial-gradient(120% 120% at 100% 0%, rgba(214, 130, 255, 0.18), transparent 60%), " +
  "linear-gradient(180deg, #040711 0%, #050816 45%, #050816 100%)";
