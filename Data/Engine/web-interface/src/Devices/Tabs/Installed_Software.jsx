import React, { useCallback, useMemo, useState } from "react";
import { Box, TextField } from "@mui/material";
import { AgGridReact } from "ag-grid-react";
import {
  DEFAULT_GRID_COL_DEF,
  DEVICE_DETAILS_GRID_THEME,
  DEVICE_GRID_STYLE,
  GridShell,
  MAGIC_UI,
} from "./Shared.jsx";

export default function InstalledSoftwareTab({ softwareRows = [] }) {
  const [softwareSearch, setSoftwareSearch] = useState("");

  const softwareColumnDefs = useMemo(
    () => [
      {
        field: "name",
        headerName: "Software Name",
        flex: 1.2,
        minWidth: 240,
        filter: "agTextColumnFilter",
      },
      {
        field: "version",
        headerName: "Version",
        width: 180,
        minWidth: 160,
        filter: "agTextColumnFilter",
      },
      {
        field: "source",
        headerName: "Source",
        width: 180,
        minWidth: 160,
        filter: "agTextColumnFilter",
        valueFormatter: (params) => {
          const value = String(params.value || "").trim().toLowerCase();
          if (value === "local_installed") return "Locally Installed";
          if (value === "windows_store") return "Windows Store";
          if (value === "dpkg") return "DPKG";
          if (value === "rpm") return "RPM";
          return params.value || "—";
        },
      },
    ],
    []
  );

  const getSoftwareRowId = useCallback(
    (params) => `${params.data?.name || "software"}-${params.data?.version || ""}-${params.rowIndex}`,
    []
  );

  return (
    <Box
      sx={{
        display: "flex",
        flexDirection: "column",
        gap: 1.5,
        flexGrow: 1,
        minHeight: 0,
      }}
    >
      <TextField
        size="small"
        placeholder="Search software..."
        value={softwareSearch}
        onChange={(event) => setSoftwareSearch(event.target.value)}
        sx={{
          maxWidth: 320,
          input: { color: "#fff" },
          "& .MuiOutlinedInput-root": {
            backgroundColor: "rgba(4,7,17,0.65)",
            "& fieldset": { borderColor: "rgba(148,163,184,0.45)" },
            "&:hover fieldset": { borderColor: MAGIC_UI.accentA },
          },
          "& .MuiInputLabel-root": { color: MAGIC_UI.textMuted },
        }}
      />
      <GridShell sx={{ flexGrow: 1, minHeight: 360 }}>
        <AgGridReact
          rowData={softwareRows}
          columnDefs={softwareColumnDefs}
          defaultColDef={DEFAULT_GRID_COL_DEF}
          pagination
          paginationPageSize={20}
          paginationPageSizeSelector={[20, 50, 100]}
          animateRows
          quickFilterText={softwareSearch}
          getRowId={getSoftwareRowId}
          theme={DEVICE_DETAILS_GRID_THEME}
          style={DEVICE_GRID_STYLE}
        />
      </GridShell>
    </Box>
  );
}
