import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  Box,
  Button,
  CircularProgress,
  InputAdornment,
  Link,
  Paper,
  TextField,
  Typography
} from "@mui/material";
import RefreshIcon from "@mui/icons-material/Refresh";
import SaveIcon from "@mui/icons-material/Save";
import VisibilityIcon from "@mui/icons-material/Visibility";
import VisibilityOffIcon from "@mui/icons-material/VisibilityOff";
import GitHubIcon from "@mui/icons-material/GitHub";

const paperSx = {
  m: 0,
  p: 0,
  bgcolor: "transparent",
  border: "none",
  boxShadow: "none",
  color: "#f5f7fa",
  display: "flex",
  flexDirection: "column",
  flexGrow: 1,
  minWidth: 0,
  minHeight: 320
};

const fieldSx = {
  mt: 2,
  "& .MuiOutlinedInput-root": {
    bgcolor: "rgba(255,255,255,0.04)",
    color: "#f5f7fa",
    borderRadius: 1,
    "& fieldset": { borderColor: "rgba(148,163,184,0.35)" },
    "&:hover fieldset": { borderColor: "rgba(148,163,184,0.55)" },
    "&.Mui-focused fieldset": { borderColor: "#7dd3fc" }
  },
  "& .MuiInputLabel-root": { color: "#bbb" },
  "& .MuiInputLabel-root.Mui-focused": { color: "#7db7ff" }
};

const gradientButtonSx = {
  backgroundImage: "linear-gradient(135deg,#7dd3fc,#c084fc)",
  color: "#0b1220",
  borderRadius: 999,
  textTransform: "none",
  boxShadow: "0 10px 26px rgba(124,58,237,0.28)",
  "&:hover": {
    backgroundImage: "linear-gradient(135deg,#86e1ff,#d1a6ff)",
    boxShadow: "0 12px 34px rgba(124,58,237,0.38)",
    filter: "none",
  },
};

export default function GithubAPIToken({ isAdmin = false, onPageMetaChange }) {
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [token, setToken] = useState("");
  const [inputValue, setInputValue] = useState("");
  const [fetchError, setFetchError] = useState("");
  const [showToken, setShowToken] = useState(false);
  const [verification, setVerification] = useState({
    message: "",
    valid: null,
    status: "",
    rateLimit: null,
    error: ""
  });

  const sendNotification = useCallback(async ({ message, variant = "info" }) => {
    if (!message) return;
    try {
      await fetch("/api/notifications/notify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          title: "GitHub API Token",
          message,
          icon: "github",
          variant,
        }),
      });
    } catch {
      /* Swallow notification transport errors */
    }
  }, []);

  const broadcastVerificationResult = useCallback(
    (payload) => {
      if (!payload) return;
      const isValid = payload.valid === true;
      const status = (payload.status || "").toLowerCase();
      const hasToken = Boolean((payload.token || "").trim());
      if (isValid) {
        sendNotification({
          variant: "info",
          message: "Github Personal Access Token Successfully Validated and Working",
        });
      } else if ((hasToken || status) && status !== "missing") {
        sendNotification({
          variant: "error",
          message: "Github Personal Access Token is either Invalid or Expired",
        });
      }
    },
    [sendNotification]
  );

  const hydrate = useCallback(async (shouldNotify = false) => {
    setLoading(true);
    setFetchError("");
    try {
      const resp = await fetch("/api/github/token");
      const data = await resp.json();
      if (!resp.ok) {
        throw new Error(data?.error || `HTTP ${resp.status}`);
      }
      const storedToken = typeof data?.token === "string" ? data.token : "";
      setToken(storedToken);
      setInputValue(storedToken);
      setShowToken(false);
      setVerification({
        message: typeof data?.message === "string" ? data.message : "",
        valid: data?.valid === true,
        status: typeof data?.status === "string" ? data.status : "",
        rateLimit: typeof data?.rate_limit === "number" ? data.rate_limit : null,
        error: typeof data?.error === "string" ? data.error : ""
      });
      if (shouldNotify) {
        broadcastVerificationResult(data);
      }
    } catch (err) {
      const message = err && typeof err.message === "string" ? err.message : String(err);
      setFetchError(message);
      setToken("");
      setInputValue("");
      setVerification({ message: "", valid: null, status: "", rateLimit: null, error: "" });
    } finally {
      setLoading(false);
    }
  }, [broadcastVerificationResult]);

  useEffect(() => {
    if (!isAdmin) return;
    hydrate();
  }, [hydrate, isAdmin]);

  const handleSave = useCallback(async () => {
    setSaving(true);
    setFetchError("");
    try {
      const resp = await fetch("/api/github/token", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token: inputValue })
      });
      const data = await resp.json();
      if (!resp.ok) {
        throw new Error(data?.error || `HTTP ${resp.status}`);
      }
      const storedToken = typeof data?.token === "string" ? data.token : "";
      setToken(storedToken);
      setInputValue(storedToken);
      setShowToken(false);
      setVerification({
        message: typeof data?.message === "string" ? data.message : "",
        valid: data?.valid === true,
        status: typeof data?.status === "string" ? data.status : "",
        rateLimit: typeof data?.rate_limit === "number" ? data.rate_limit : null,
        error: typeof data?.error === "string" ? data.error : ""
      });
      broadcastVerificationResult(data);
    } catch (err) {
      const message = err && typeof err.message === "string" ? err.message : String(err);
      setFetchError(message);
    } finally {
      setSaving(false);
    }
  }, [broadcastVerificationResult, inputValue]);

  const dirty = useMemo(() => inputValue !== token, [inputValue, token]);

  const verificationMessage = useMemo(() => {
    if (dirty) {
      return { text: "Token has not been saved yet — Save to verify.", color: "#f0c36d" };
    }
    const message = verification.message || "";
    if (!message) {
      return { text: "", color: "#bbb" };
    }
    if (verification.valid) {
      return { text: message, color: "#7dffac" };
    }
    if ((verification.status || "").toLowerCase() === "missing") {
      return { text: message, color: "#bbb" };
    }
    return { text: message, color: "#ff8080" };
  }, [dirty, verification]);

  const toggleReveal = useCallback(() => {
    setShowToken((prev) => !prev);
  }, []);

  useEffect(() => {
    onPageMetaChange?.({
      page_title: "GitHub API Token",
      page_subtitle: "Increase GitHub API rate limits for Borealis by storing a personal access token.",
      page_icon: GitHubIcon,
    });
    return () => onPageMetaChange?.(null);
  }, [onPageMetaChange]);

  if (!isAdmin) {
    return (
      <Paper sx={{ m: 2, p: 3, bgcolor: "transparent" }}>
        <Typography variant="h6" sx={{ color: "#ff8080" }}>
          Access denied
        </Typography>
        <Typography variant="body2" sx={{ color: "#bbb" }}>
          You do not have permission to manage the GitHub API token.
        </Typography>
      </Paper>
    );
  }

  return (
    <Paper sx={paperSx} elevation={0}>
      <Box sx={{ px: 2, py: 2, display: "flex", flexDirection: "column", gap: 1.5 }}>
        <Typography variant="body2" sx={{ color: "#ccc" }}>
          Using a GitHub Personal Access Token raises rate limits from 60/hr to 5,000/hr. Generate one at{" "}
          <Link
            href="https://github.com/settings/tokens"
            target="_blank"
            rel="noopener noreferrer"
            sx={{ color: "#7db7ff" }}
          >
            https://github.com/settings/tokens
          </Link>{" "}
          under <b>Personal Access Tokens → Tokens (Classic)</b>.
        </Typography>
        <Typography variant="body2" sx={{ color: "#ccc" }}>
          <Box component="span" sx={{ fontWeight: 600 }}>Note:</Box>{" "}
          <Box component="code" sx={{ bgcolor: "#222", px: 0.75, py: 0.25, borderRadius: 1, fontSize: "0.85rem" }}>
            Borealis Automation Platform
          </Box>
          {"  "}
          <Box component="span" sx={{ fontWeight: 600, ml: 2 }}>Scope:</Box>{" "}
          <Box component="code" sx={{ bgcolor: "#222", px: 0.75, py: 0.25, borderRadius: 1, fontSize: "0.85rem" }}>
            public_repo
          </Box>
          {"  "}
          <Box component="span" sx={{ fontWeight: 600, ml: 2 }}>Expiration:</Box>{" "}
          <Box component="code" sx={{ bgcolor: "#222", px: 0.75, py: 0.25, borderRadius: 1, fontSize: "0.85rem" }}>
            No Expiration
          </Box>
        </Typography>

        <TextField
          label="Personal Access Token"
          value={inputValue}
          onChange={(event) => setInputValue(event.target.value)}
          fullWidth
          variant="outlined"
          sx={fieldSx}
          disabled={saving || loading}
          type={showToken ? "text" : "password"}
          InputProps={{
            endAdornment: (
              <InputAdornment
                position="end"
                sx={{ mr: -1, display: "flex", alignItems: "center", gap: 1 }}
              >
                <Button
                  variant="contained"
                  size="small"
                  onClick={toggleReveal}
                  disabled={loading || saving}
                  startIcon={showToken ? <VisibilityOffIcon /> : <VisibilityIcon />}
                  sx={{
                    bgcolor: "#3a3a3a",
                    color: "#f5f7fa",
                    minWidth: 96,
                    mr: 0.5,
                    "&:hover": { bgcolor: "#4a4a4a" }
                  }}
                >
                  {showToken ? "Hide" : "Reveal"}
                </Button>
                <Button
                  variant="contained"
                  size="small"
                  onClick={handleSave}
                  disabled={saving || loading}
                  startIcon={!saving ? <SaveIcon /> : null}
                  sx={gradientButtonSx}
                >
                  {saving ? <CircularProgress size={16} sx={{ color: "#0b0f19" }} /> : "Save"}
                </Button>
              </InputAdornment>
            )
          }}
        />

        <Box
          sx={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: 2
          }}
        >
          <Button
            variant="outlined"
            size="small"
            startIcon={<RefreshIcon />}
            sx={{
              borderColor: "rgba(148,163,184,0.35)",
              color: "#e2e8f0",
              textTransform: "none",
              borderRadius: 999,
              px: 1.7,
              minWidth: 86,
              "&:hover": { borderColor: "rgba(148,163,184,0.55)" },
            }}
            onClick={() => hydrate(true)}
            disabled={loading || saving}
          >
            Refresh
          </Button>
          {(verificationMessage.text || (!dirty && verification.rateLimit)) && (
            <Typography
              variant="body2"
              sx={{
                display: "inline-flex",
                alignItems: "center",
                color: verificationMessage.color || "#7db7ff",
                textAlign: "right"
              }}
            >
              {verificationMessage.text && `${verificationMessage.text} `}
              {!dirty &&
                verification.rateLimit &&
                `- Hourly Request Rate Limit: ${verification.rateLimit.toLocaleString()}`}
            </Typography>
          )}
        </Box>

        {loading && (
          <Box
            sx={{
              display: "flex",
              alignItems: "center",
              gap: 1,
              color: "#7db7ff",
              px: 2,
              py: 1.5,
              borderBottom: "1px solid #2a2a2a"
            }}
          >
            <CircularProgress size={18} sx={{ color: "#58a6ff" }} />
            <Typography variant="body2">Loading token…</Typography>
          </Box>
        )}

        {fetchError && (
          <Box sx={{ px: 2, py: 1.5, color: "#ff8080", borderBottom: "1px solid #2a2a2a" }}>
            <Typography variant="body2">{fetchError}</Typography>
          </Box>
        )}
      </Box>
    </Paper>
  );
}
