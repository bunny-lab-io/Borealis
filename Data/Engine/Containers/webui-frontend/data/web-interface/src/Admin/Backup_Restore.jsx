import React, { useMemo, useRef, useState } from "react";
import {
  Alert,
  Box,
  Button,
  CircularProgress,
  Divider,
  LinearProgress,
  Stack,
  TextField,
  Typography,
} from "@mui/material";
import {
  BackupRounded as BackupIcon,
  DownloadRounded as DownloadIcon,
  ManageSearchRounded as AnalyzeIcon,
  RestoreRounded as RestoreIcon,
  UploadFileRounded as UploadFileIcon,
} from "@mui/icons-material";
import { AgGridReact } from "ag-grid-react";
import { AllCommunityModule, ModuleRegistry, themeQuartz } from "ag-grid-community";
import { Navigate, useNavigate } from "react-router-dom";
import PageBodyFrame from "../PageBodyFrame.jsx";
import { useAppNotifications } from "../app/hooks/useAppNotifications.js";
import { useRoutePageChrome } from "../app/hooks/useRoutePageChrome.js";
import { useAuth } from "../app/providers/AuthContext.jsx";
import { APP_PATHS } from "../app/routes/paths.js";

const PAGE_TITLE = "Backup or Restore Engine Configuration";
const PAGE_SUBTITLE =
  "Export or import an encrypted JSON file of all Engine settings excluding logs, device activity history, and scheduled job history.";
const RESTORE_CONFIRMATION = "RESTORE ENGINE CONFIG BACKUP";
const gridFontFamily = '"IBM Plex Sans", "Helvetica Neue", Arial, sans-serif';
const backupAnalysisGridTheme = themeQuartz.withParams({
  accentColor: "#7dd3fc",
  backgroundColor: "#070b1a",
  browserColorScheme: "dark",
  fontFamily: { googleFont: "IBM Plex Sans" },
  foregroundColor: "#f4f7ff",
  headerFontSize: 13,
});
const backupAnalysisGridThemeClass = backupAnalysisGridTheme.themeName || "ag-theme-quartz";
const iconFontFamily = '"Quartz Regular"';

ModuleRegistry.registerModules([AllCommunityModule]);

function filenameFromDisposition(header) {
  const raw = String(header || "");
  const match = raw.match(/filename\*?=(?:UTF-8''|")?([^";]+)/i);
  if (!match) return "";
  return decodeURIComponent(match[1].replace(/^"|"$/g, ""));
}

function restoreSuccessMessage(payload) {
  const tableCount = Number(payload?.tables_restored || 0);
  const rowCount = Number(payload?.rows_restored || 0);
  const fileCount = Number(payload?.files_restored || 0);
  const logCount = Number(payload?.logs_cleared || 0);
  return `Restored ${tableCount} tables, ${rowCount} rows, ${fileCount} files, and cleared ${logCount} log entries. API restart and Aegis unlock required.`;
}

function BackupRestoreTool({ mode }) {
  const navigate = useNavigate();
  const { refreshBootstrapState } = useAuth();
  const notifyOperator = useAppNotifications({ title: "Backup/Restore", icon: "settings", variant: "success" });
  const fileInputRef = useRef(null);
  const [backupFileName, setBackupFileName] = useState("");
  const [backupDocument, setBackupDocument] = useState(null);
  const [cipher, setCipher] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [analysisRows, setAnalysisRows] = useState([]);
  const [analysisSignature, setAnalysisSignature] = useState("");
  const [isAnalyzing, setIsAnalyzing] = useState(false);
  const [isExporting, setIsExporting] = useState(false);
  const [isRestoring, setIsRestoring] = useState(false);
  const [notice, setNotice] = useState(null);

  const isBootstrap = mode === "bootstrap";
  const backupSignature = useMemo(
    () => `${backupFileName}|${backupDocument?.ciphertext_b64 || ""}|${cipher.trim()}`,
    [backupDocument, backupFileName, cipher]
  );
  const analysisReady = Boolean(analysisRows.length > 0 && analysisSignature === backupSignature);
  const canAnalyze = useMemo(
    () => Boolean(backupDocument) && cipher.trim().length > 0 && !isAnalyzing && !isRestoring,
    [backupDocument, cipher, isAnalyzing, isRestoring]
  );
  const canRestore = useMemo(
    () =>
      Boolean(backupDocument) &&
      cipher.trim().length > 0 &&
      analysisReady &&
      confirmation.trim() === RESTORE_CONFIRMATION &&
      !isAnalyzing &&
      !isRestoring,
    [analysisReady, backupDocument, cipher, confirmation, isAnalyzing, isRestoring]
  );
  const analysisColumnDefs = useMemo(
    () => [
      { field: "name", headerName: "Content", flex: 1, minWidth: 220 },
      {
        field: "count",
        headerName: "Count",
        width: 120,
        type: "numericColumn",
        headerClass: "analysis-count-header",
        cellClass: "auto-col-tight analysis-count-cell",
        cellRenderer: (params) => (
          <Box
            component="span"
            sx={{
              minWidth: 44,
              px: 1,
              py: 0.25,
              borderRadius: 999,
              border: "1px solid rgba(125, 211, 252, 0.28)",
              background: "rgba(14, 165, 233, 0.12)",
              color: "#e0f2fe",
              fontWeight: 800,
              lineHeight: 1.4,
              textAlign: "center",
            }}
          >
            {Number(params.value || 0).toLocaleString()}
          </Box>
        ),
        valueFormatter: (params) => Number(params.value || 0).toLocaleString(),
      },
    ],
    []
  );
  const analysisDefaultColDef = useMemo(
    () => ({
      sortable: true,
      resizable: true,
      filter: false,
    }),
    []
  );

  const clearAnalysis = () => {
    setAnalysisRows([]);
    setAnalysisSignature("");
  };

  const handleFileSelect = async (event) => {
    const file = event.target.files?.[0];
    setNotice(null);
    setBackupDocument(null);
    setBackupFileName("");
    clearAnalysis();
    if (!file) return;
    try {
      const text = await file.text();
      const parsed = JSON.parse(text);
      if (!parsed || typeof parsed !== "object" || !parsed.ciphertext_b64 || !parsed.nonce_b64) {
        throw new Error("Selected file is not an encrypted Borealis Engine backup.");
      }
      setBackupDocument(parsed);
      setBackupFileName(file.name);
    } catch (error) {
      setNotice({
        severity: "error",
        message: error instanceof Error ? error.message : "Unable to read backup file.",
      });
      if (fileInputRef.current) {
        fileInputRef.current.value = "";
      }
    }
  };

  const handleCipherChange = (event) => {
    setCipher(event.target.value);
    clearAnalysis();
  };

  const handleExport = async () => {
    setIsExporting(true);
    setNotice(null);
    try {
      const response = await fetch("/api/server/backup/export", {
        method: "GET",
        credentials: "include",
      });
      if (!response.ok) {
        const payload = await response.json().catch(() => ({}));
        throw new Error(payload?.message || payload?.error || "Backup export failed.");
      }
      const blob = await response.blob();
      const filename =
        filenameFromDisposition(response.headers.get("Content-Disposition")) ||
        `borealis-engine-backup-${new Date().toISOString().replace(/[:.]/g, "-")}.json`;
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);
      await notifyOperator({
        message: "Encrypted Engine configuration backup exported.",
        variant: "success",
      });
    } catch (error) {
      setNotice({
        severity: "error",
        message: error instanceof Error ? error.message : "Backup export failed.",
      });
    } finally {
      setIsExporting(false);
    }
  };

  const handleRestore = async () => {
    if (!canRestore) return;
    setIsRestoring(true);
    setNotice(null);
    try {
      const endpoint = isBootstrap
        ? "/api/bootstrap/backup/restore"
        : "/api/server/backup/restore";
      const response = await fetch(endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          cipher,
          confirmation: confirmation.trim(),
          backup: backupDocument,
        }),
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.message || payload?.error || "Backup restore failed.");
      }
      const message = restoreSuccessMessage(payload);
      setNotice({ severity: "success", message });
      await refreshBootstrapState();
      setTimeout(() => navigate(APP_PATHS.login, { replace: true }), 1200);
    } catch (error) {
      setNotice({
        severity: "error",
        message: error instanceof Error ? error.message : "Backup restore failed.",
      });
    } finally {
      setIsRestoring(false);
    }
  };

  const handleAnalyze = async () => {
    if (!canAnalyze) return;
    setIsAnalyzing(true);
    setNotice(null);
    clearAnalysis();
    const signature = backupSignature;
    try {
      const endpoint = isBootstrap
        ? "/api/bootstrap/backup/analyze"
        : "/api/server/backup/analyze";
      const response = await fetch(endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          cipher,
          backup: backupDocument,
        }),
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.message || payload?.error || "Backup analysis failed.");
      }
      const rows = Array.isArray(payload?.analysis?.summary)
        ? payload.analysis.summary.map((row) => ({
            id: String(row?.id || row?.name || ""),
            name: String(row?.name || row?.id || ""),
            count: Number(row?.count || 0),
          }))
        : [];
      if (!rows.length) {
        throw new Error("Backup analysis returned no import summary.");
      }
      setAnalysisRows(rows);
      setAnalysisSignature(signature);
    } catch (error) {
      clearAnalysis();
      setNotice({
        severity: "error",
        message: error instanceof Error ? error.message : "Backup analysis failed.",
      });
    } finally {
      setIsAnalyzing(false);
    }
  };

  return (
    <Stack
      spacing={2.5}
      sx={{
        minWidth: 0,
        minHeight: 0,
        height: isBootstrap ? "auto" : "100%",
        display: "flex",
      }}
    >
      {!isBootstrap ? (
        <Box
          sx={{
            border: "1px solid rgba(148, 163, 184, 0.22)",
            borderRadius: 2,
            p: 2.5,
            display: "flex",
            flexDirection: { xs: "column", sm: "row" },
            gap: 2,
            alignItems: { xs: "stretch", sm: "center" },
            justifyContent: "space-between",
            background: "rgba(15, 23, 42, 0.5)",
          }}
        >
          <Box sx={{ minWidth: 0 }}>
            <Typography variant="subtitle1" sx={{ fontWeight: 700, color: "#e5f2ff" }}>
              Export Encrypted Backup
            </Typography>
            <Typography variant="body2" sx={{ color: "rgba(226, 232, 240, 0.72)", mt: 0.5 }}>
              Current Engine configuration, trust, user content, and protected secrets.  Backup files are encrypted using the existing Aegis Cipher.
            </Typography>
          </Box>
          <Button
            variant="contained"
            startIcon={<DownloadIcon />}
            onClick={handleExport}
            disabled={isExporting || isRestoring}
            sx={{ minWidth: 150, borderRadius: 2, textTransform: "none", fontWeight: 700 }}
          >
            Export
          </Button>
        </Box>
      ) : null}

      <Box
        sx={{
          border: "1px solid rgba(248, 113, 113, 0.3)",
          borderRadius: 2,
          p: 2.5,
          background: "rgba(69, 10, 10, 0.18)",
          display: "flex",
          flexDirection: "column",
          flex: isBootstrap ? "0 0 auto" : "1 1 auto",
          minHeight: 0,
        }}
      >
        <Stack spacing={2} sx={{ minHeight: 0, height: "100%" }}>
          <Box>
            <Typography variant="subtitle1" sx={{ fontWeight: 700, color: "#fff1f2" }}>
              Restore Encrypted Backup
            </Typography>
            <Typography variant="body2" sx={{ color: "rgba(254, 226, 226, 0.78)", mt: 0.5 }}>
              Restore sterilizes existing Engine configuration, secrets, and trust before importing the backup.
            </Typography>
          </Box>

          <Stack direction={{ xs: "column", sm: "row" }} spacing={1.5} alignItems={{ xs: "stretch", sm: "center" }}>
            <input
              ref={fileInputRef}
              type="file"
              accept="application/json,.json"
              hidden
              onChange={handleFileSelect}
            />
            <Button
              variant="outlined"
              startIcon={<UploadFileIcon />}
              onClick={() => fileInputRef.current?.click()}
              disabled={isRestoring}
              sx={{
                borderRadius: 2,
                textTransform: "none",
                fontWeight: 700,
                color: "#e0f2fe",
                borderColor: "rgba(125, 211, 252, 0.42)",
              }}
            >
              Select File
            </Button>
            <Typography
              variant="body2"
              sx={{
                color: backupFileName ? "#e5f2ff" : "rgba(226, 232, 240, 0.58)",
                overflowWrap: "anywhere",
              }}
            >
              {backupFileName || "No encrypted JSON backup selected"}
            </Typography>
          </Stack>

          <Box
            sx={{
              display: "grid",
              gridTemplateColumns: { xs: "1fr", md: "repeat(2, minmax(0, 1fr))" },
              gap: 2,
            }}
          >
            <TextField
              label="Aegis Cipher"
              type="password"
              value={cipher}
              onChange={handleCipherChange}
              disabled={isAnalyzing || isRestoring}
              fullWidth
              InputProps={{ sx: { borderRadius: 2 } }}
            />
            <TextField
              label={`Type ${RESTORE_CONFIRMATION}`}
              value={confirmation}
              onChange={(event) => setConfirmation(event.target.value)}
              disabled={isAnalyzing || isRestoring}
              fullWidth
              InputProps={{ sx: { borderRadius: 2 } }}
            />
          </Box>

          {analysisRows.length ? (
            <Box
              className={backupAnalysisGridThemeClass}
              sx={{
                border: "1px solid rgba(148, 163, 184, 0.2)",
                borderRadius: 2,
                overflow: "hidden",
                background: "rgba(15, 23, 42, 0.72)",
                boxShadow: "inset 0 1px 0 rgba(255,255,255,0.05), 0 18px 48px rgba(0, 0, 0, 0.26)",
                flex: isBootstrap ? "0 0 auto" : "1 1 0",
                minHeight: isBootstrap ? 240 : 0,
                "--ag-font-family": gridFontFamily,
                "--ag-icon-font-family": iconFontFamily,
                "& .ag-root-wrapper": {
                  border: "none",
                  borderRadius: 0,
                  background: "transparent",
                },
                "& .ag-header": {
                  backgroundColor: "rgba(15,23,42,0.92)",
                  borderBottom: "1px solid rgba(148,163,184,0.25)",
                },
                "& .ag-header-cell-label": {
                  color: "#e2e8f0",
                  fontWeight: 700,
                  letterSpacing: 0.3,
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
                "& .ag-center-cols-container .ag-cell.auto-col-tight, & .ag-pinned-left-cols-container .ag-cell.auto-col-tight, & .ag-pinned-right-cols-container .ag-cell.auto-col-tight": {
                  paddingLeft: "12px",
                  paddingRight: "9px",
                },
                "& .ag-header-cell.analysis-count-header .ag-header-cell-label": {
                  justifyContent: "center",
                },
                "& .ag-center-cols-container .ag-cell.analysis-count-cell, & .ag-pinned-left-cols-container .ag-cell.analysis-count-cell, & .ag-pinned-right-cols-container .ag-cell.analysis-count-cell": {
                  justifyContent: "center",
                  textAlign: "center",
                },
                "& .ag-row": {
                  borderColor: "rgba(255,255,255,0.04)",
                  transition: "background 0.2s ease",
                },
                "& .ag-row:nth-of-type(even)": {
                  backgroundColor: "rgba(15,23,42,0.42)",
                },
                "& .ag-row-hover": {
                  backgroundColor: "rgba(73,156,196,0.18) !important",
                },
              }}
            >
              <Box sx={{ height: isBootstrap ? 320 : "100%", minHeight: isBootstrap ? 240 : 0 }}>
                <AgGridReact
                  theme={backupAnalysisGridTheme}
                  rowData={analysisRows}
                  columnDefs={analysisColumnDefs}
                  defaultColDef={analysisDefaultColDef}
                  domLayout="normal"
                  suppressCellFocus
                  getRowId={(params) => params.data?.id || params.data?.name}
                />
              </Box>
            </Box>
          ) : null}

          <Divider sx={{ borderColor: "rgba(248, 113, 113, 0.24)" }} />

          <Stack direction={{ xs: "column", sm: "row" }} spacing={1.5} alignItems={{ xs: "stretch", sm: "center" }}>
            <Button
              variant="outlined"
              startIcon={<AnalyzeIcon />}
              onClick={handleAnalyze}
              disabled={!canAnalyze}
              sx={{
                borderRadius: 2,
                textTransform: "none",
                fontWeight: 800,
                color: "#e0f2fe",
                borderColor: "rgba(125, 211, 252, 0.42)",
              }}
            >
              Analyze
            </Button>
            <Button
              variant="contained"
              color="error"
              startIcon={<RestoreIcon />}
              onClick={handleRestore}
              disabled={!canRestore}
              sx={{ borderRadius: 2, textTransform: "none", fontWeight: 800 }}
            >
              Import / Restore
            </Button>
          </Stack>
        </Stack>
      </Box>

      {isExporting || isAnalyzing || isRestoring ? <LinearProgress sx={{ borderRadius: 999 }} /> : null}
      {notice ? <Alert severity={notice.severity}>{notice.message}</Alert> : null}
    </Stack>
  );
}

export default function BackupRestore({ mode = "admin" }) {
  const isBootstrap = mode === "bootstrap";
  const { bootstrapState, ready, isAuthenticated, isAdmin } = useAuth();
  useRoutePageChrome(
    isBootstrap
      ? null
      : {
          title: PAGE_TITLE,
          subtitle: PAGE_SUBTITLE,
          breadcrumbLabel: "Backup/Restore",
          Icon: BackupIcon,
        }
  );

  if (isBootstrap) {
    if (!ready) {
      return (
        <Box sx={{ width: "100vw", height: "100vh", display: "grid", placeItems: "center", background: "#040711" }}>
          <CircularProgress />
        </Box>
      );
    }
    if (String(bootstrapState?.phase || "") === "login_required") {
      return <Navigate to={isAuthenticated && isAdmin ? APP_PATHS.backupRestore : APP_PATHS.login} replace />;
    }
    return (
      <Box
        sx={{
          minHeight: "100vh",
          px: { xs: 2, sm: 3 },
          py: { xs: 3, md: 5 },
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          background:
            "linear-gradient(145deg, #06111f 0%, #111827 45%, #1f2933 100%)",
        }}
      >
        <Box
          sx={{
            width: "min(960px, 100%)",
            border: "1px solid rgba(148, 163, 184, 0.26)",
            borderRadius: 2,
            background: "rgba(2, 6, 23, 0.86)",
            boxShadow: "0 28px 70px rgba(0, 0, 0, 0.45)",
            p: { xs: 2.5, sm: 4 },
          }}
        >
          <Stack spacing={1} sx={{ mb: 3 }}>
            <Typography variant="h4" sx={{ color: "#e5f2ff", fontWeight: 800, letterSpacing: 0 }}>
              {PAGE_TITLE}
            </Typography>
            <Typography variant="body1" sx={{ color: "rgba(226, 232, 240, 0.76)", maxWidth: 780 }}>
              {PAGE_SUBTITLE}
            </Typography>
          </Stack>
          <BackupRestoreTool mode="bootstrap" />
        </Box>
      </Box>
    );
  }

  return (
    <PageBodyFrame variant="content_panel" fillHeight contentSx={{ height: "100%", minHeight: 0 }}>
      <BackupRestoreTool mode="admin" />
    </PageBodyFrame>
  );
}
