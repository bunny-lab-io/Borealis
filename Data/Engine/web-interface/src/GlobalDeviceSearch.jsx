import React, { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Box, CircularProgress, InputBase, Popper, Typography } from "@mui/material";
import ClickAwayListener from "@mui/material/ClickAwayListener";
import SearchIcon from "@mui/icons-material/Search";
import { AgGridReact } from "ag-grid-react";
import { ModuleRegistry, AllCommunityModule, themeQuartz } from "ag-grid-community";

ModuleRegistry.registerModules([AllCommunityModule]);

const MIN_SEARCH_LENGTH = 3;
const SEARCH_ROW_HEIGHT = 42;
const SEARCH_HEADER_HEIGHT = 34;
const SEARCH_PANEL_MAX_HEIGHT = 320;

const searchGridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  chromeBackgroundColor: {
    ref: "foregroundColor",
    mix: 0.08,
    onto: "backgroundColor",
  },
  fontFamily: {
    googleFont: "IBM Plex Sans",
  },
  foregroundColor: "#f4f7ff",
  headerFontSize: 12,
});

const themeClassName = searchGridTheme.themeName || "ag-theme-quartz";
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const iconFontFamily = '"Quartz Regular"';

const MAGIC_UI = {
  panelBg:
    "linear-gradient(135deg, rgba(10, 16, 31, 0.97) 0%, rgba(7, 11, 26, 0.95) 55%, rgba(20, 8, 33, 0.96) 100%)",
  panelBorder: "rgba(148, 163, 184, 0.28)",
  panelBorderStrong: "rgba(125, 183, 255, 0.34)",
  textMuted: "#94a3b8",
  textBright: "#e2e8f0",
  accentA: "#58a6ff",
  accentB: "#7dd3fc",
  searchShadow: "0 10px 28px rgba(4, 8, 24, 0.38)",
  glow: "0 24px 68px rgba(4, 8, 24, 0.72)",
};

function buildRowKey(device) {
  const guid = String(device?.agent_guid || "").trim().toLowerCase();
  const hostname = String(device?.hostname || "").trim().toLowerCase();
  const siteId = String(device?.site_id ?? "").trim().toLowerCase();
  const agentId = String(device?.agent_id || "").trim().toLowerCase();
  return guid || `${hostname}::${siteId}::${agentId}`;
}

function highlightHostname(hostname, query) {
  const text = String(hostname || "");
  const needle = String(query || "").trim();
  if (!needle) return text;

  const loweredText = text.toLowerCase();
  const loweredNeedle = needle.toLowerCase();
  const matchIndex = loweredText.indexOf(loweredNeedle);
  if (matchIndex < 0) return text;

  const before = text.slice(0, matchIndex);
  const match = text.slice(matchIndex, matchIndex + needle.length);
  const after = text.slice(matchIndex + needle.length);

  return (
    <>
      {before}
      <Box
        component="span"
        sx={{
          color: "#c9ebff",
          textShadow: "0 0 14px rgba(125, 211, 252, 0.28)",
        }}
      >
        {match}
      </Box>
      {after}
    </>
  );
}

export default function GlobalDeviceSearch({ onSelectDevice }) {
  const anchorRef = useRef(null);
  const [query, setQuery] = useState("");
  const [expanded, setExpanded] = useState(false);
  const [results, setResults] = useState([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [dropdownWidth, setDropdownWidth] = useState(0);

  const trimmedQuery = query.trim();
  const queryReady = trimmedQuery.length >= MIN_SEARCH_LENGTH;
  const showDropdown = expanded && queryReady;

  useLayoutEffect(() => {
    const anchor = anchorRef.current;
    if (!anchor) return undefined;

    const updateWidth = () => {
      const nextWidth = Math.round(anchor.getBoundingClientRect().width || 0);
      setDropdownWidth((prev) => (prev === nextWidth ? prev : nextWidth));
    };

    updateWidth();
    window.addEventListener("resize", updateWidth);

    if (typeof ResizeObserver === "undefined") {
      return () => {
        window.removeEventListener("resize", updateWidth);
      };
    }

    const observer = new ResizeObserver(() => updateWidth());
    observer.observe(anchor);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", updateWidth);
    };
  }, []);

  useEffect(() => {
    if (!showDropdown) {
      setLoading(false);
      setError("");
      setResults([]);
      return undefined;
    }

    const controller = new AbortController();
    setLoading(true);
    setError("");
    setResults([]);
    const timer = window.setTimeout(async () => {
      try {
        const resp = await fetch(
          `/api/devices/search?hostname=${encodeURIComponent(trimmedQuery)}`,
          {
            cache: "no-store",
            credentials: "include",
            signal: controller.signal,
          }
        );
        if (!resp.ok) {
          throw new Error(`device search failed (${resp.status})`);
        }
        const payload = await resp.json();
        if (controller.signal.aborted) return;
        setResults(Array.isArray(payload?.devices) ? payload.devices : []);
      } catch (err) {
        if (controller.signal.aborted) return;
        setResults([]);
        setError("Search is temporarily unavailable.");
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    }, 180);

    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [showDropdown, trimmedQuery]);

  const rowData = useMemo(
    () =>
      results.map((device) => ({
        ...device,
        rowKey: buildRowKey(device),
        siteLabel: String(device?.site_name || "").trim() || "Not Configured",
      })),
    [results]
  );

  const columnDefs = useMemo(
    () => [
      {
        field: "hostname",
        headerName: "Hostname",
        minWidth: 220,
        flex: 1.35,
        resizable: true,
        sortable: false,
        suppressMenu: true,
        cellClass: "auto-col-tight",
        cellRenderer: (params) => (
          <Typography
            component="span"
            sx={{
              color: MAGIC_UI.accentA,
              fontWeight: 600,
              fontSize: "0.9rem",
              letterSpacing: 0.1,
              whiteSpace: "nowrap",
              overflow: "hidden",
              textOverflow: "ellipsis",
            }}
          >
            {highlightHostname(params.value, trimmedQuery)}
          </Typography>
        ),
      },
      {
        field: "siteLabel",
        headerName: "Site",
        minWidth: 180,
        flex: 1,
        resizable: true,
        sortable: false,
        suppressMenu: true,
        cellClass: "auto-col-tight",
        cellRenderer: (params) => (
          <Typography
            component="span"
            sx={{
              color: MAGIC_UI.textMuted,
              fontSize: "0.85rem",
              fontWeight: 500,
              whiteSpace: "nowrap",
              overflow: "hidden",
              textOverflow: "ellipsis",
            }}
          >
            {params.value}
          </Typography>
        ),
      },
    ],
    [trimmedQuery]
  );

  const defaultColDef = useMemo(
    () => ({
      filter: false,
      editable: false,
      suppressMovable: true,
      resizable: true,
      sortable: false,
    }),
    []
  );

  const statusLabel = loading
    ? "Searching"
    : queryReady
    ? `${rowData.length} result${rowData.length === 1 ? "" : "s"}`
    : `Min ${MIN_SEARCH_LENGTH} chars`;

  const handleClose = () => {
    setExpanded(false);
  };

  const handleSelect = (device) => {
    if (!device) return;
    onSelectDevice?.({
      agent_guid: device.agent_guid || "",
      agent_id: device.agent_id || "",
      hostname: device.hostname || "",
      site_id: device.site_id ?? null,
      site_name: device.site_name || "",
      connection_type: device.connection_type || "",
    });
    setQuery("");
    setExpanded(false);
    setResults([]);
    setError("");
  };

  const dropdownHeight = Math.min(
    SEARCH_HEADER_HEIGHT + rowData.length * SEARCH_ROW_HEIGHT + 2,
    SEARCH_PANEL_MAX_HEIGHT
  );

  return (
    <ClickAwayListener onClickAway={handleClose}>
      <Box sx={{ position: "relative", width: "100%", minWidth: 0 }}>
        <Box
          ref={anchorRef}
          sx={{
            position: "relative",
            display: "flex",
            alignItems: "center",
            gap: 1.2,
            width: "100%",
            minWidth: 0,
            height: 42,
            px: 1.6,
            borderRadius: "999px",
            border: `1px solid ${expanded ? MAGIC_UI.panelBorderStrong : MAGIC_UI.panelBorder}`,
            background:
              "linear-gradient(135deg, rgba(10, 16, 31, 0.94) 0%, rgba(8, 13, 28, 0.92) 60%, rgba(20, 8, 33, 0.92) 100%)",
            boxShadow: MAGIC_UI.searchShadow,
            backdropFilter: "blur(16px) saturate(145%)",
            transition: "border-color 160ms ease, box-shadow 180ms ease, transform 120ms ease",
            "&:hover": {
              borderColor: MAGIC_UI.panelBorderStrong,
            },
            "&:focus-within": {
              borderColor: MAGIC_UI.panelBorderStrong,
              transform: "translateY(-0.5px)",
            },
          }}
        >
          <SearchIcon sx={{ color: MAGIC_UI.accentB, fontSize: 19, flexShrink: 0 }} />
          <InputBase
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              setExpanded(true);
            }}
            onFocus={() => setExpanded(true)}
            onKeyDown={(event) => {
              if (event.key === "Escape") {
                event.preventDefault();
                setExpanded(false);
                return;
              }
              if (event.key === "Enter" && rowData.length) {
                event.preventDefault();
                handleSelect(rowData[0]);
              }
            }}
            placeholder="Search devices by hostname..."
            inputProps={{ "aria-label": "Global device hostname search" }}
            sx={{
              flex: 1,
              minWidth: 0,
              color: MAGIC_UI.textBright,
              fontSize: "0.95rem",
              fontWeight: 500,
              "& input::placeholder": {
                color: "rgba(148, 163, 184, 0.92)",
                opacity: 1,
              },
            }}
          />
          <Box sx={{ minWidth: 64, display: "flex", justifyContent: "flex-end", alignItems: "center", gap: 0.7 }}>
            {loading ? <CircularProgress size={14} sx={{ color: MAGIC_UI.accentB }} /> : null}
            <Typography
              variant="caption"
              sx={{
                color: queryReady ? MAGIC_UI.textBright : MAGIC_UI.textMuted,
                fontWeight: 600,
                letterSpacing: 0.18,
                whiteSpace: "nowrap",
              }}
            >
              {statusLabel}
            </Typography>
          </Box>
        </Box>

        <Popper
          open={showDropdown}
          anchorEl={anchorRef.current}
          placement="bottom-start"
          keepMounted={false}
          modifiers={[
            {
              name: "offset",
              options: {
                offset: [0, 10],
              },
            },
            {
              name: "preventOverflow",
              options: {
                padding: 16,
              },
            },
          ]}
          sx={{
            zIndex: 2200,
            width: dropdownWidth ? `${dropdownWidth}px` : undefined,
            maxWidth: "calc(100vw - 32px)",
          }}
        >
          <Box
            sx={{
              width: "100%",
              overflow: "hidden",
              borderRadius: "18px",
              border: `1px solid ${MAGIC_UI.panelBorder}`,
              background: MAGIC_UI.panelBg,
              boxShadow: MAGIC_UI.glow,
              backdropFilter: "blur(18px) saturate(150%)",
            }}
          >
            {error ? (
              <Box sx={{ px: 2, py: 1.6 }}>
                <Typography sx={{ color: "#fda4af", fontSize: "0.88rem", fontWeight: 600 }}>
                  {error}
                </Typography>
              </Box>
            ) : loading ? (
              <Box sx={{ px: 2, py: 1.6, display: "flex", alignItems: "center", gap: 1.2 }}>
                <CircularProgress size={16} sx={{ color: MAGIC_UI.accentB }} />
                <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.86rem", fontWeight: 600 }}>
                  Searching accessible devices...
                </Typography>
              </Box>
            ) : !loading && !rowData.length ? (
              <Box sx={{ px: 2, py: 1.75 }}>
                <Typography sx={{ color: MAGIC_UI.textBright, fontSize: "0.88rem", fontWeight: 600 }}>
                  No accessible devices matched "{trimmedQuery}".
                </Typography>
                <Typography sx={{ mt: 0.35, color: MAGIC_UI.textMuted, fontSize: "0.8rem" }}>
                  Search spans every site available to your Borealis role.
                </Typography>
              </Box>
            ) : (
              <Box
                className={themeClassName}
                sx={{
                  height: dropdownHeight,
                  width: "100%",
                  fontFamily: gridFontFamily,
                  "--ag-font-family": gridFontFamily,
                  "--ag-icon-font-family": iconFontFamily,
                  "& .ag-root-wrapper": {
                    border: "none",
                    borderRadius: 0,
                    background: "transparent",
                  },
                  "& .ag-header": {
                    backgroundColor: "rgba(15,23,42,0.88)",
                    borderBottom: "1px solid rgba(148,163,184,0.18)",
                  },
                  "& .ag-header-cell-label": {
                    color: "#d7e9ff",
                    fontWeight: 600,
                    letterSpacing: 0.22,
                  },
                  "& .ag-center-cols-container .ag-cell, & .ag-pinned-left-cols-container .ag-cell, & .ag-pinned-right-cols-container .ag-cell": {
                    display: "flex",
                    alignItems: "center",
                    textAlign: "left",
                    padding: "8px 12px 8px 18px",
                    borderColor: "rgba(255,255,255,0.04)",
                  },
                  "& .ag-center-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-left-cols-container .ag-cell .ag-cell-wrapper, & .ag-pinned-right-cols-container .ag-cell .ag-cell-wrapper": {
                    width: "100%",
                    display: "flex",
                    alignItems: "center",
                    padding: 0,
                  },
                  "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
                    paddingLeft: "12px",
                    paddingRight: "9px",
                  },
                  "& .ag-row:nth-of-type(even)": {
                    backgroundColor: "rgba(15,23,42,0.34)",
                  },
                  "& .ag-row-hover": {
                    backgroundColor: "rgba(73,156,196,0.16) !important",
                    cursor: "pointer",
                  },
                  "& .ag-icon": {
                    fontFamily: iconFontFamily,
                  },
                }}
                style={{
                  "--ag-background-color": "transparent",
                  "--ag-foreground-color": MAGIC_UI.textBright,
                  "--ag-header-background-color": "rgba(15,23,42,0.88)",
                  "--ag-header-foreground-color": "#d7e9ff",
                  "--ag-row-hover-color": "rgba(73,156,196,0.16)",
                  "--ag-selected-row-background-color": "rgba(64,164,255,0.18)",
                  "--ag-odd-row-background-color": "rgba(255,255,255,0.015)",
                  "--ag-border-color": "rgba(125,183,255,0.18)",
                  "--ag-row-border-color": "rgba(255,255,255,0.04)",
                  "--ag-border-radius": "0px",
                }}
              >
                <AgGridReact
                  rowData={rowData}
                  columnDefs={columnDefs}
                  defaultColDef={defaultColDef}
                  theme={searchGridTheme}
                  animateRows
                  headerHeight={SEARCH_HEADER_HEIGHT}
                  rowHeight={SEARCH_ROW_HEIGHT}
                  suppressCellFocus
                  suppressMovableColumns
                  suppressContextMenu
                  suppressColumnVirtualisation
                  getRowId={(params) => params.data?.rowKey || ""}
                  onRowClicked={(event) => handleSelect(event.data)}
                  style={{
                    width: "100%",
                    height: "100%",
                    fontFamily: gridFontFamily,
                    "--ag-icon-font-family": iconFontFamily,
                  }}
                />
              </Box>
            )}
          </Box>
        </Popper>
      </Box>
    </ClickAwayListener>
  );
}
