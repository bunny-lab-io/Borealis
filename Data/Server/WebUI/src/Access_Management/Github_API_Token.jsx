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
  const [verification, setVerification] = useState({
    message: "",
    valid: null,
    status: "",
    rateLimit: null,
    error: ""
  });

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
      setVerification({
        message: typeof data?.message === "string" ? data.message : "",
        valid: data?.valid === true,
        status: typeof data?.status === "string" ? data.status : "",
        rateLimit: typeof data?.rate_limit === "number" ? data.rate_limit : null,
        error: typeof data?.error === "string" ? data.error : ""
      });
    } catch (err) {
      const message = err && typeof err.message === "string" ? err.message : String(err);
      setFetchError(message);
      setToken("");
      setInputValue("");
      setVerification({ message: "", valid: null, status: "", rateLimit: null, error: "" });
    } finally {
      setLoading(false);
    }
  }, []);

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
      setVerification({
        message: typeof data?.message === "string" ? data.message : "",
        valid: data?.valid === true,
        status: typeof data?.status === "string" ? data.status : "",
        rateLimit: typeof data?.rate_limit === "number" ? data.rate_limit : null,
        error: typeof data?.error === "string" ? data.error : ""
      });
    } catch (err) {
      const message = err && typeof err.message === "string" ? err.message : String(err);
      setFetchError(message);
    } finally {
      setSaving(false);
    }
  }, [inputValue]);

  const dirty = useMemo(() => inputValue !== token, [inputValue, token]);

  const verificationMessage = useMemo(() => {
    if (dirty) {
      return { text: "Token has unsaved changes — save to verify.", color: "#f0c36d" };
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
            Using a Github Personal Access Token increased the Github API rate limits from 60/hr to 5000/hr.
          </Typography>
        </Box>
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

      <Box sx={{ px: 2, py: 2, display: "flex", flexDirection: "column", gap: 1.5 }}>
        <Typography variant="body2" sx={{ color: "#ccc" }}>
          Navigate to{' '}
          <Link
            href="https://github.com/settings/tokens"
            target="_blank"
            rel="noopener noreferrer"
            sx={{ color: "#7db7ff" }}
          >
            https://github.com/settings/tokens
          </Link>{' '}
          &gt; Personal Access Tokens &gt; Tokens (Classic) &gt; Generate New Token &gt; New Personal Access Token (Classic)
        </Typography>
        <Typography variant="body2" sx={{ color: "#ccc" }}>
          <Box component="span" sx={{ fontWeight: 600 }}>Note:</Box> Borealis Automation Platform
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

        <TextField
          label="Personal Access Token"
          value={inputValue}
          onChange={(event) => setInputValue(event.target.value)}
          fullWidth
          variant="outlined"
          sx={fieldSx}
          disabled={saving || loading}
          InputProps={{
            endAdornment: (
              <InputAdornment position="end" sx={{ mr: -1 }}>
                <Button
                  variant="contained"
                  size="small"
                  onClick={handleSave}
                  disabled={saving || loading}
                  sx={{
                    bgcolor: "#58a6ff",
                    color: "#0b0f19",
                    minWidth: 88,
                    "&:hover": { bgcolor: "#7db7ff" }
                  }}
                >
                  {saving ? <CircularProgress size={16} sx={{ color: "#0b0f19" }} /> : "Save"}
                </Button>
              </InputAdornment>
            )
          }}
        />

        {verificationMessage.text && (
          <Typography variant="body2" sx={{ color: verificationMessage.color }}>
            {verificationMessage.text}
          </Typography>
        )}

        {!dirty && verification.rateLimit && (
          <Typography variant="caption" sx={{ color: "#7db7ff" }}>
            Authenticated GitHub rate limit: {verification.rateLimit.toLocaleString()} requests / hour
          </Typography>
        )}
      </Box>
    </Paper>
  );
}
