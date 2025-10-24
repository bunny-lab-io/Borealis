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

const coerceNumber = (value) => {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value;
  }
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) {
      return parsed;
    }
  }
  return null;
};

const normalizeRateLimit = (raw) => {
  if (raw === null || raw === undefined) {
    return null;
  }
  if (typeof raw === "number") {
    const limit = coerceNumber(raw);
    return limit === null
      ? null
      : { limit, remaining: null, reset: null, used: null };
  }
  if (typeof raw === "object") {
    const limit = coerceNumber(raw.limit);
    const remaining = coerceNumber(raw.remaining);
    const reset = coerceNumber(raw.reset);
    const used = coerceNumber(raw.used);
    if (limit === null && remaining === null && reset === null && used === null) {
      return null;
    }
    return { limit, remaining, reset, used };
  }
  return null;
};

const paperSx = {
  m: 2,
  p: 0,
  bgcolor: "#1e1e1e",
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
    bgcolor: "#181818",
    color: "#f5f7fa",
    "& fieldset": { borderColor: "#2a2a2a" },
    "&:hover fieldset": { borderColor: "#58a6ff" },
    "&.Mui-focused fieldset": { borderColor: "#58a6ff" }
  },
  "& .MuiInputLabel-root": { color: "#bbb" },
  "& .MuiInputLabel-root.Mui-focused": { color: "#7db7ff" }
};

export default function GithubAPIToken({ isAdmin = false }) {
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

  const mapVerification = useCallback((data) => ({
    message: typeof data?.message === "string" ? data.message : "",
    valid: data?.valid === true,
    status: typeof data?.status === "string" ? data.status : "",
    rateLimit: normalizeRateLimit(data?.rate_limit),
    error: typeof data?.error === "string" ? data.error : ""
  }), []);

  const hydrate = useCallback(async () => {
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
      setVerification(mapVerification(data));
    } catch (err) {
      const message = err && typeof err.message === "string" ? err.message : String(err);
      setFetchError(message);
      setToken("");
      setInputValue("");
      setVerification({ message: "", valid: null, status: "", rateLimit: null, error: "" });
    } finally {
      setLoading(false);
    }
  }, [mapVerification]);

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
      setVerification(mapVerification(data));
    } catch (err) {
      const message = err && typeof err.message === "string" ? err.message : String(err);
      setFetchError(message);
    } finally {
      setSaving(false);
    }
  }, [inputValue, mapVerification]);

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

  const rateLimitSummary = useMemo(() => {
    if (dirty || !verification.rateLimit) {
      return "";
    }
    const parts = [];
    if (typeof verification.rateLimit.limit === "number") {
      parts.push(`Hourly Request Rate Limit: ${verification.rateLimit.limit.toLocaleString()}`);
    }
    if (typeof verification.rateLimit.remaining === "number") {
      parts.push(`Remaining: ${verification.rateLimit.remaining.toLocaleString()}`);
    }
    return parts.join(" · ");
  }, [dirty, verification.rateLimit]);

  const verificationDetail = useMemo(() => {
    if (dirty) {
      return "";
    }
    if (!verification.valid && verification.error) {
      return verification.error;
    }
    return "";
  }, [dirty, verification.error, verification.valid]);

  const toggleReveal = useCallback(() => {
    setShowToken((prev) => !prev);
  }, []);

  if (!isAdmin) {
    return (
      <Paper sx={{ m: 2, p: 3, bgcolor: "#1e1e1e" }}>
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
    <Paper sx={paperSx} elevation={2}>
      <Box
        sx={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          p: 2,
          borderBottom: "1px solid #2a2a2a"
        }}
      >
        <Box>
          <Typography variant="h6" sx={{ color: "#58a6ff", mb: 0.3 }}>
            Github API Token
          </Typography>
          <Typography variant="body2" sx={{ color: "#aaa" }}>
            Using a Github "Personal Access Token" increases the Github API rate limits from 60/hr to 5,000/hr.  This is important for production Borealis usage as it likes to hit its unauthenticated API limits sometimes despite my best efforts.
            <br></br>Navigate to{' '}
          <Link
            href="https://github.com/settings/tokens"
            target="_blank"
            rel="noopener noreferrer"
            sx={{ color: "#7db7ff" }}
          >
            https://github.com/settings/tokens
          </Link>{' '}
          &#10095; <b>Personal Access Tokens &#10095; Tokens (Classic) &#10095; Generate New Token &#10095; New Personal Access Token (Classic)</b>
          </Typography>

        <br></br>
        <Typography variant="body2" sx={{ color: "#ccc" }}>
          <Box component="span" sx={{ fontWeight: 600 }}>Note:</Box>{' '}
          <Box component="code" sx={{ bgcolor: "#222", px: 0.75, py: 0.25, borderRadius: 1, fontSize: "0.85rem" }}>
            Borealis Automation Platform
          </Box>
        </Typography>
        <Typography variant="body2" sx={{ color: "#ccc" }}>
          <Box component="span" sx={{ fontWeight: 600 }}>Scope:</Box>{' '}
          <Box component="code" sx={{ bgcolor: "#222", px: 0.75, py: 0.25, borderRadius: 1, fontSize: "0.85rem" }}>
            public_repo
          </Box>
        </Typography>
        <Typography variant="body2" sx={{ color: "#ccc" }}>
          <Box component="span" sx={{ fontWeight: 600 }}>Expiration:</Box>{' '}
          <Box component="code" sx={{ bgcolor: "#222", px: 0.75, py: 0.25, borderRadius: 1, fontSize: "0.85rem" }}>
            No Expiration
          </Box>
        </Typography>
        </Box>
      </Box>
      <Box sx={{ px: 2, py: 2, display: "flex", flexDirection: "column", gap: 1.5 }}>
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
                  sx={{
                    bgcolor: "#58a6ff",
                    color: "#0b0f19",
                    minWidth: 88,
                    mr: 1,
                    "&:hover": { bgcolor: "#7db7ff" }
                  }}
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
            sx={{ borderColor: "#58a6ff", color: "#58a6ff" }}
            onClick={hydrate}
            disabled={loading || saving}
          >
            Refresh
          </Button>
          {(verificationMessage.text || rateLimitSummary || verificationDetail) && (
            <Typography
              variant="body2"
              sx={{
                display: "inline-flex",
                alignItems: "center",
                color: verificationMessage.color || "#7db7ff",
                textAlign: "right",
                flexDirection: "column",
                gap: 0.5
              }}
            >
              <span>
                {verificationMessage.text && `${verificationMessage.text} `}
                {rateLimitSummary}
              </span>
              {verificationDetail && (
                <span style={{ color: "#f0c36d", whiteSpace: "pre-line" }}>
                  {verificationDetail}
                </span>
              )}
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
