import React, { useEffect, useMemo, useState } from "react";
import { Box, TextField, Button, Typography } from "@mui/material";

export default function Login({ onLogin }) {
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [step, setStep] = useState("credentials"); // 'credentials' | 'mfa'
  const [pendingToken, setPendingToken] = useState("");
  const [mfaStage, setMfaStage] = useState(null);
  const [mfaCode, setMfaCode] = useState("");
  const [setupSecret, setSetupSecret] = useState("");
  const [setupQr, setSetupQr] = useState("");
  const [setupUri, setSetupUri] = useState("");

  const formattedSecret = useMemo(() => {
    if (!setupSecret) return "";
    return setupSecret.replace(/(.{4})/g, "$1 ").trim();
  }, [setupSecret]);

  useEffect(() => {
    if (mfaStage !== "setup" || setupQr || !setupUri) {
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const module = await import("qrcode");
        const toDataURL = module?.toDataURL || module?.default?.toDataURL;
        if (!toDataURL) {
          throw new Error("qrcode.toDataURL unavailable");
        }
        const url = await toDataURL(setupUri, { margin: 1, width: 180 });
        if (!cancelled) {
          setSetupQr(url);
        }
      } catch (err) {
        if (!cancelled) {
          console.warn("Failed to generate MFA QR code locally", err);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [mfaStage, setupQr, setupUri]);

  const sha512 = async (text) => {
    try {
      if (window.crypto && window.crypto.subtle && window.isSecureContext) {
        const encoder = new TextEncoder();
        const data = encoder.encode(text);
        const hashBuffer = await window.crypto.subtle.digest("SHA-512", data);
        const hashArray = Array.from(new Uint8Array(hashBuffer));
        return hashArray.map((b) => b.toString(16).padStart(2, "0")).join("");
      }
    } catch (_) {
      // fall through to return null
    }
    // Not a secure context or subtle crypto unavailable
    return null;
  };

  const resetMfaState = () => {
    setStep("credentials");
    setPendingToken("");
    setMfaStage(null);
    setMfaCode("");
    setSetupSecret("");
    setSetupQr("");
    setSetupUri("");
  };

  const handleCredentialsSubmit = async (e) => {
    e.preventDefault();
    setIsSubmitting(true);
    setError("");
    try {
      const hash = await sha512(password);
      const body = hash
        ? { username, password_sha512: hash }
        : { username, password };
      const resp = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify(body)
      });
      const data = await resp.json();
      if (!resp.ok) {
        throw new Error(data?.error || "Invalid username or password");
      }
      if (data?.status === "mfa_required") {
        setPendingToken(data.pending_token || "");
        setMfaStage(data.stage || "verify");
        setStep("mfa");
        setMfaCode("");
        setSetupSecret(data.secret || "");
        setSetupQr(data.qr_image || "");
        setSetupUri(data.otpauth_url || "");
        setError("");
        setPassword("");
        return;
      }
      if (data?.token) {
        try {
          document.cookie = `borealis_auth=${data.token}; Path=/; SameSite=Lax`;
        } catch (_) {}
      }
      onLogin({ username: data.username, role: data.role });
    } catch (err) {
      const msg = err?.message || "Unable to log in";
      setError(msg);
      resetMfaState();
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleMfaSubmit = async (e) => {
    e.preventDefault();
    if (!pendingToken) {
      setError("Your MFA session expired. Please log in again.");
      resetMfaState();
      return;
    }
    if (!mfaCode || mfaCode.trim().length < 6) {
      setError("Enter the 6-digit code from your authenticator app.");
      return;
    }
    setIsSubmitting(true);
    setError("");
    try {
      const resp = await fetch("/api/auth/mfa/verify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ pending_token: pendingToken, code: mfaCode })
      });
      const data = await resp.json();
      if (!resp.ok) {
        const errKey = data?.error;
        if (errKey === "expired" || errKey === "invalid_session" || errKey === "mfa_pending") {
          setError("Your MFA session expired. Please log in again.");
          resetMfaState();
          return;
        }
        const msgMap = {
          invalid_code: "Incorrect code. Please try again.",
          mfa_not_configured: "MFA is not configured for this account."
        };
        setError(msgMap[errKey] || data?.error || "Failed to verify code.");
        return;
      }
      if (data?.token) {
        try {
          document.cookie = `borealis_auth=${data.token}; Path=/; SameSite=Lax`;
        } catch (_) {}
      }
      setError("");
      onLogin({ username: data.username, role: data.role });
    } catch (err) {
      setError("Failed to verify code.");
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleBackToLogin = () => {
    resetMfaState();
    setPassword("");
    setError("");
  };

  const onCodeChange = (event) => {
    const raw = event.target.value || "";
    const digits = raw.replace(/\D/g, "").slice(0, 6);
    setMfaCode(digits);
  };

  const formTitle = step === "mfa"
    ? "Multi-Factor Authentication"
    : "Borealis - Automation Platform";

  return (
    <Box
      sx={{
        display: "flex",
        justifyContent: "center",
        alignItems: "center",
        height: "100vh",
        backgroundColor: "#2b2b2b",
      }}
    >
      <Box
        component="form"
        onSubmit={step === "mfa" ? handleMfaSubmit : handleCredentialsSubmit}
        sx={{
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          width: 320,
        }}
      >
        <img
          src="/Borealis_Logo.png"
          alt="Borealis Logo"
          style={{ width: "120px", marginBottom: "16px" }}
        />
        <Typography variant="h6" sx={{ mb: 2, textAlign: "center" }}>
          {formTitle}
        </Typography>

        {step === "credentials" ? (
          <>
            <TextField
              label="Username"
              variant="outlined"
              fullWidth
              value={username}
              disabled={isSubmitting}
              onChange={(e) => setUsername(e.target.value)}
              margin="normal"
            />
            <TextField
              label="Password"
              type="password"
              variant="outlined"
              fullWidth
              value={password}
              disabled={isSubmitting}
              onChange={(e) => setPassword(e.target.value)}
              margin="normal"
            />
            {error && (
              <Typography color="error" sx={{ mt: 1 }}>
                {error}
              </Typography>
            )}
            <Button
              type="submit"
              variant="contained"
              fullWidth
              disabled={isSubmitting}
              sx={{ mt: 2, bgcolor: "#58a6ff", "&:hover": { bgcolor: "#1d82d3" } }}
            >
              {isSubmitting ? "Signing In..." : "Login"}
            </Button>
          </>
        ) : (
          <>
            {mfaStage === "setup" ? (
              <>
                <Typography variant="body2" sx={{ color: "#ccc", textAlign: "center", mb: 2 }}>
                  Scan the QR code with your authenticator app, then enter the 6-digit code to complete setup for {username}.
                </Typography>
                {setupQr ? (
                  <img
                    src={setupQr}
                    alt="MFA enrollment QR code"
                    style={{ width: "180px", height: "180px", marginBottom: "12px" }}
                  />
                ) : null}
                {formattedSecret ? (
                  <Box
                    sx={{
                      bgcolor: "#1d1d1d",
                      borderRadius: 1,
                      px: 2,
                      py: 1,
                      mb: 1.5,
                      width: "100%",
                    }}
                  >
                    <Typography variant="caption" sx={{ color: "#999" }}>
                      Manual code
                    </Typography>
                    <Typography
                      variant="body1"
                      sx={{
                        fontFamily: "monospace",
                        letterSpacing: "0.3rem",
                        color: "#fff",
                        mt: 0.5,
                        textAlign: "center",
                        wordBreak: "break-word",
                      }}
                    >
                      {formattedSecret}
                    </Typography>
                  </Box>
                ) : null}
                {setupUri ? (
                  <Typography
                    variant="caption"
                    sx={{
                      color: "#888",
                      mb: 2,
                      wordBreak: "break-all",
                      textAlign: "center",
                    }}
                  >
                    {setupUri}
                  </Typography>
                ) : null}
              </>
            ) : (
              <Typography variant="body2" sx={{ color: "#ccc", textAlign: "center", mb: 2 }}>
                Enter the 6-digit code from your authenticator app for {username}.
              </Typography>
            )}

            <TextField
              label="6-digit code"
              variant="outlined"
              fullWidth
              value={mfaCode}
              onChange={onCodeChange}
              disabled={isSubmitting}
              margin="normal"
              inputProps={{
                inputMode: "numeric",
                pattern: "[0-9]*",
                maxLength: 6,
                style: { letterSpacing: "0.4rem", textAlign: "center", fontSize: "1.2rem" }
              }}
              autoComplete="one-time-code"
            />
            {error && (
              <Typography color="error" sx={{ mt: 1, textAlign: "center" }}>
                {error}
              </Typography>
            )}
            <Button
              type="submit"
              variant="contained"
              fullWidth
              disabled={isSubmitting || mfaCode.length < 6}
              sx={{ mt: 2, bgcolor: "#58a6ff", "&:hover": { bgcolor: "#1d82d3" } }}
            >
              {isSubmitting ? "Verifying..." : "Verify Code"}
            </Button>
            <Button
              type="button"
              variant="text"
              fullWidth
              disabled={isSubmitting}
              onClick={handleBackToLogin}
              sx={{ mt: 1, color: "#58a6ff" }}
            >
              Use a different account
            </Button>
          </>
        )}
      </Box>
    </Box>
  );
}
