import React from "react";
import { CssBaseline, ThemeProvider } from "@mui/material";
import { darkTheme } from "../utils/theme.js";
import { AuthProvider } from "./AuthContext.jsx";
import { PageChromeProvider } from "./PageChromeContext.jsx";

export function AppProviders({ children }) {
  return (
    <ThemeProvider theme={darkTheme}>
      <CssBaseline />
      <AuthProvider>
        <PageChromeProvider>{children}</PageChromeProvider>
      </AuthProvider>
    </ThemeProvider>
  );
}
