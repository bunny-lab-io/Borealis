import React, { useEffect, useMemo, useState } from "react";
import { Paper, Box, Typography } from "@mui/material";
import { GitHub as GitHubIcon, InfoOutlined as InfoIcon } from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";
import { CreditsDialog } from "../Dialogs.jsx";

ModuleRegistry.registerModules([AllCommunityModule]);

const gridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#050915",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#e2e8f0",
  headerFontSize: 13,
});

const themeClassName = gridTheme.themeName || "ag-theme-quartz";
const gridFontFamily = "\"IBM Plex Sans\", \"Helvetica Neue\", Arial, sans-serif";
const iconFontFamily = "\"Quartz Regular\"";

export default function ServerInfo({ isAdmin = false, onPageMetaChange }) {
  const [serverTime, setServerTime] = useState(null);
  const [error, setError] = useState(null);
  const [aboutOpen, setAboutOpen] = useState(false);

  useEffect(() => {
    if (!isAdmin) return;
    let isMounted = true;
    const fetchTime = async () => {
      try {
        const resp = await fetch('/api/server/time');
        if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
        const data = await resp.json();
        if (isMounted) {
          setServerTime(data?.display || data?.iso || null);
          setError(null);
        }
      } catch (e) {
        if (isMounted) setError(String(e));
      }
    };
    fetchTime();
    const id = setInterval(fetchTime, 60000); // update once per minute
    return () => { isMounted = false; clearInterval(id); };
  }, [isAdmin]);

  const infoRows = useMemo(
    () => [
      {
        id: "server-time",
        field: "Server Time",
        value: error ? `Error: ${error}` : (serverTime || "Loading..."),
        description: "Internal server clock used for troubleshooting the job scheduling system.",
      },
    ],
    [error, serverTime]
  );

  const pageHeaderActions = useMemo(
    () => [
      {
        id: "server-github",
        label: "GitHub Project",
        icon: <GitHubIcon />,
        tone: "secondary",
        onClick: () => window.open("https://github.com/bunny-lab-io/Borealis", "_blank"),
      },
      {
        id: "server-about",
        label: "About Borealis",
        icon: <InfoIcon />,
        tone: "primary",
        onClick: () => setAboutOpen(true),
      },
    ],
    []
  );

  useEffect(() => {
    onPageMetaChange?.({
      page_title: "Server Info",
      page_subtitle: "Basic server information and project links for debugging and support.",
      page_icon: InfoIcon,
      page_header_actions: pageHeaderActions,
    });
    return () => onPageMetaChange?.(null);
  }, [onPageMetaChange, pageHeaderActions]);

  const columnDefs = useMemo(
    () => [
      { headerName: "Field", field: "field", minWidth: 180, flex: 0.35 },
      { headerName: "Value", field: "value", minWidth: 240, flex: 0.4 },
      { headerName: "Description", field: "description", minWidth: 260, flex: 0.6 },
    ],
    []
  );

  if (!isAdmin) return null;

  return (
    <Paper sx={{ m: 0, p: 0, bgcolor: "transparent", border: "none", boxShadow: "none" }} elevation={0}>
      <Box
        sx={{
          p: 2,
          pb: 3,
          display: "flex",
          flexDirection: "column",
          minHeight: "calc(100vh - 110px)",
          gap: 2,
        }}
      >
        <Typography sx={{ color: '#aaa', mb: 1 }}>
          Basic server information for debug and support. Server time updates automatically every minute.
        </Typography>
        <Box
          className={themeClassName}
          sx={{
            mt: 3,
            width: "100%",
            maxWidth: "100%",
            minHeight: "75vh",
            display: "flex",
            flexDirection: "column",
            background: "rgba(8,12,24,0.72)",
            borderRadius: 2,
            border: "1px solid rgba(148,163,184,0.35)",
            boxShadow: "0 12px 36px rgba(2,6,23,0.55)",
            overflow: "hidden",
            fontFamily: gridFontFamily,
            "--ag-font-family": gridFontFamily,
            "--ag-icon-font-family": iconFontFamily,
            "& .ag-root-wrapper": {
              border: "none",
              borderRadius: 2,
            },
            "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-start",
              textAlign: "left",
              padding: "8px 12px 8px 18px",
            },
            "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
              width: "100%",
              display: "flex",
              alignItems: "center",
              justifyContent: "flex-start",
              padding: 0,
            },
            "& .ag-header": {
              borderBottom: "1px solid rgba(148,163,184,0.35)",
            },
          }}
          style={{
            "--ag-background-color": "rgba(5,9,21,0.92)",
            "--ag-foreground-color": "#e2e8f0",
            "--ag-header-background-color": "#0b1226",
            "--ag-header-foreground-color": "#cfe0ff",
            "--ag-row-hover-color": "rgba(125,211,252,0.08)",
            "--ag-selected-row-background-color": "rgba(64,164,255,0.16)",
            "--ag-border-color": "rgba(148,163,184,0.28)",
            "--ag-row-border-color": "rgba(148,163,184,0.16)",
            "--ag-odd-row-background-color": "rgba(255,255,255,0.02)",
            "--ag-border-radius": "12px",
          }}
        >
          <AgGridReact
            rowData={infoRows}
            columnDefs={columnDefs}
            defaultColDef={{
              sortable: false,
              filter: false,
              resizable: true,
              flex: 1,
              minWidth: 160,
              cellStyle: {
                color: "#e2e8f0",
                fontFamily: gridFontFamily,
                fontSize: 14,
              },
              headerClass: "server-info-grid-header",
            }}
            getRowId={(params) => params.data?.id || String(params.rowIndex ?? "")}
            suppressCellFocus
            animateRows
            rowHeight={48}
            headerHeight={42}
            pagination={false}
            theme={gridTheme}
            style={{
              width: "100%",
              flex: 1,
              fontFamily: gridFontFamily,
              "--ag-icon-font-family": iconFontFamily,
            }}
          />
        </Box>

      </Box>
      <CreditsDialog open={aboutOpen} onClose={() => setAboutOpen(false)} />
    </Paper>
  );
}
