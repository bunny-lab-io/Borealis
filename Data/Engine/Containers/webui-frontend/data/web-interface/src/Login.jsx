import React, { useMemo, useState } from "react";
import { Box, TextField, Button, Typography } from "@mui/material";
import { authenticateWithPasskey, isPasskeySupported } from "./app/utils/passkeys.js";

export default function Login({ onLogin }) {
  // ----------------- Original state & logic -----------------
  const [username, setUsername] = useState("");
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
  const passkeySupported = isPasskeySupported();

  const formattedSecret = useMemo(() => {
    if (!setupSecret) return "";
    return setupSecret.replace(/(.{4})/g, "$1 ").trim();
  }, [setupSecret]);

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
        ? { username, password, password_sha512: hash }
        : { username, password };
      const resp = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify(body)
      });
      const data = await resp.json();
      if (!resp.ok) {
        const messageMap = {
          auth_reset_required:
            "This account needs administrator recovery before it can sign in again.",
          bootstrap_required:
            "Borealis must finish Aegis bootstrap before sign-in is available.",
          directory_password_required:
            "Directory accounts require password sign-in from this page.",
          ambiguous_directory_username:
            "Use your domain-qualified username for this directory account.",
          directory_group_not_allowed:
            "This directory account is not authorized for Borealis.",
          directory_user_disabled:
            "This directory account is disabled in Borealis.",
        };
        throw new Error(messageMap[data?.error] || data?.error || "Invalid username or password");
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
      onLogin({ username: data.username, role: data.role, auth_source: data.auth_source });
    } catch (err) {
      const msg = err?.message || "Unable to log in";
      setError(msg);
      resetMfaState();
    } finally {
      setIsSubmitting(false);
    }
  };

  const handlePrimaryPasskeySignIn = async () => {
    setIsSubmitting(true);
    setError("");
    try {
      const data = await authenticateWithPasskey();
      setError("");
      onLogin({ username: data.username, role: data.role, auth_source: data.auth_source });
    } catch (err) {
      setError(
        err instanceof Error && err.message
          ? err.message
          : "Failed to sign in with a passkey."
      );
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
          mfa_not_configured: "MFA is not configured for this account.",
          bootstrap_required: "Borealis must finish Aegis bootstrap before sign-in is available.",
        };
        setError(msgMap[errKey] || data?.error || "Failed to verify code.");
        return;
      }
      setError("");
      onLogin({ username: data.username, role: data.role, auth_source: data.auth_source });
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
    ? mfaStage === "setup"
      ? "Multi-Factor Setup Required"
      : "Multi-Factor Authentication"
    : "Borealis - Automation Platform";

  // ----------------- UI helpers -----------------
  const FieldLabel = ({ children }) => (
    <Typography variant="caption" sx={{ color: "var(--text-dim)", letterSpacing: ".04em", display: "block", mt: 1.4, mb: 0.1 }}>
      {children}
    </Typography>
  );

  return (
    <>
      {/* Component-scoped styles for MagicUI effects */}
      <style>{`
        :root {
          --bg: #0b0e14;
          --panel: #0f1320;
          --text: #e6f1ff;
          --text-dim: #9ab0c8;
          --accent: #58a6ff;
          --accent-2: #a78bfa; /* purple */
          --accent-3: #22d3ee; /* cyan */
          --radius-xl: 20px;
          --card-w: min(94vw, 500px);
        }

        .login-wrap {
          position: relative;
          display: grid;
          place-items: center;
          min-height: 100vh;
          background: radial-gradient(1200px 800px at 80% 20%, rgba(75,0,130,.18), transparent 60%),
                      radial-gradient(900px 700px at 10% 90%, rgba(0,128,128,.16), transparent 60%),
                      linear-gradient(160deg, #0b0e14 0%, #0a0f1f 50%, #0b0e14 100%);
          overflow: hidden;
          isolation: isolate;
        }

        /* Aurora blobs */
        .aurora {
          position: absolute;
          inset: -20% -20% -20% -20%;
          pointer-events: none;
          z-index: 0;
          filter: blur(40px) saturate(120%);
          opacity: .7;
          background:
            radial-gradient(650px 380px at 10% 20%, rgba(88,166,255,.25), transparent 60%),
            radial-gradient(700px 420px at 80% 10%, rgba(167,139,250,.18), transparent 60%),
            radial-gradient(600px 360px at 70% 90%, rgba(34,211,238,.18), transparent 60%);
          animation: auroraShift 22s ease-in-out infinite alternate;
        }
        @keyframes auroraShift {
          0%   { transform: translate3d(-3%, -2%, 0) scale(1.02); }
          50%  { transform: translate3d(2%, 1%, 0) scale(1.04); }
          100% { transform: translate3d(-1%, 3%, 0) scale(1.03); }
        }

        /* Flickering grid */
        .grid {
          position: absolute;
          inset: 0;
          background-image:
            linear-gradient(rgba(255,255,255,0.04) 1px, transparent 1px),
            linear-gradient(90deg, rgba(255,255,255,0.04) 1px, transparent 1px);
          background-size: 32px 32px, 32px 32px;
          mask-image: radial-gradient(60% 60% at 50% 50%, black 60%, transparent 100%);
          z-index: 1;
        }
        .grid::after {
          content: "";
          position: absolute;
          inset: 0;
          background:
            repeating-radial-gradient(circle at 30% 30%, rgba(88,166,255,0.04) 0 2px, transparent 2px 6px);
          mix-blend-mode: screen;
          animation: flicker 4s infinite steps(60);
          opacity: .7;
        }
        @keyframes flicker {
          0%, 19%, 21%, 23%, 25%, 54%, 56%, 100% { opacity: .75 }
          20%, 24%, 55% { opacity: .45 }
        }

        /* Shine border container */
        .shine {
          position: relative;
          width: var(--card-w);
          border-radius: var(--radius-xl);
          box-shadow:
            0 0 0 1px rgba(255,255,255,0.06) inset,
            0 10px 30px rgba(0,0,0,.5);
          z-index: 2;
        }
        @keyframes spin {
          to { transform: rotate(360deg); }
        }
        .shine-inner {
          border-radius: inherit;
          position: relative;
          border: 1px solid transparent;
          background:
            linear-gradient(180deg, rgba(17,22,36,.92), rgba(11,14,20,.92)) padding-box,
            conic-gradient(from 0deg,
              rgba(88,166,255,.0),
              rgba(88,166,255,.45),
              rgba(34,211,238,.0),
              rgba(125, 255, 183, 0.3),
              rgba(34,211,238,.0),
              rgba(88,166,255,.45),
              rgba(167,139,250,.0)) border-box;
          backdrop-filter: blur(10px);
          padding: 30px 28px;
        }

        .brand {
          display: flex;
          align-items: center;
          gap: 12px;
          margin-bottom: 8px;
        }
        .brand img { height: 32px; width: auto; }
        .brand-title {
          font-weight: 700;
          letter-spacing: .04em;
          color: var(--text);
          font-size: 0.95rem;
          text-transform: uppercase;
          opacity: .9;
        }

        .title {
          margin: 0 0 8px 0;
          color: var(--text);
          text-align: center;
          font-weight: 700;
          letter-spacing: .02em;
        }

        .helper {
          color: var(--text-dim);
          text-align: center;
          margin-bottom: 8px;
        }

        .submit {
          margin-top: 12px;
          background: linear-gradient(135deg, #34d399, #22d3ee);
          color: #0b0e14;
          font-weight: 700;
          text-transform: none;
          border-radius: 14px;
          box-shadow: 0 8px 18px rgba(88,166,255,.25);
        }
        .submit:hover {
          filter: brightness(1.05);
          box-shadow: 0 10px 22px rgba(88,166,255,.35);
        }

        .secondary-submit {
          margin-top: 10px;
          color: #8ecbff;
          border-color: rgba(125,183,255,0.24);
          text-transform: none;
          font-weight: 700;
          border-radius: 14px;
          background: linear-gradient(180deg, rgba(10,18,34,0.92), rgba(9,15,28,0.92));
          box-shadow: inset 0 0 0 1px rgba(125,183,255,0.06);
        }
        .secondary-submit:hover {
          border-color: rgba(125,183,255,0.34);
          background: linear-gradient(180deg, rgba(14,24,44,0.98), rgba(10,18,34,0.96));
        }

        .muted-btn {
          color: var(--accent);
          text-transform: none;
          font-weight: 600;
        }

        .mono-box {
          background: #0f172a;
          border: 1px solid rgba(255,255,255,.08);
          border-radius: 12px;
          padding: 10px 12px;
          margin-bottom: 12px;
        }
        .mono-text {
          font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
          letter-spacing: .28rem;
          color: #e5e7eb;
          text-align: center;
          user-select: all;
          word-break: break-word;
        }

        .error {
          color: #ff6b6b;
          text-align: center;
          margin-top: 8px;
          font-size: .9rem;
        }
      `}</style>

      <div className="login-wrap">
        {/* Background layers */}
        <div className="aurora" />
        <div className="grid" />

        {/* Card with ShineBorder */}
        <div className="shine">
          <div className="shine-inner">
            <Box
              component="form"
              onSubmit={step === "credentials" ? handleCredentialsSubmit : handleMfaSubmit}
              sx={{ display: "flex", flexDirection: "column", gap: 1 }}
            >
              <div className="brand">
                <img src="/Borealis_Logo_Full.png" alt="Borealis Automation" style={{ width: "480px", height: "auto" }} />
              </div>

              <Typography variant="h5" className="title">
                {step === "mfa" ? formTitle : ""}
              </Typography>

              {step === "credentials" ? (
                <>
                  <FieldLabel>Username</FieldLabel>
                  <TextField
                    placeholder="you@borealis"
                    variant="outlined"
                    fullWidth
                    value={username}
                    disabled={isSubmitting}
                    onChange={(e) => setUsername(e.target.value)}
                    InputProps={{ sx: { borderRadius: 2 } }}
                  />

                  <FieldLabel>Password</FieldLabel>
                  <TextField
                    type="password"
                    variant="outlined"
                    fullWidth
                    value={password}
                    disabled={isSubmitting}
                    onChange={(e) => setPassword(e.target.value)}
                    InputProps={{ sx: { borderRadius: 2 } }}
                  />

                  {error && <div className="error">{error}</div>}

                  <Button
                    type="submit"
                    variant="contained"
                    className="submit"
                    disabled={isSubmitting}
                  >
                    {isSubmitting ? "Signing In..." : "Sign In"}
                  </Button>

                  <Button
                    type="button"
                    variant="outlined"
                    className="secondary-submit"
                    disabled={isSubmitting || !passkeySupported}
                    onClick={() => void handlePrimaryPasskeySignIn()}
                  >
                    Sign In with Passkey
                  </Button>

                  {!passkeySupported ? (
                    <Typography variant="body2" className="helper">
                      This browser does not support passkeys in a secure context.
                    </Typography>
                  ) : null}
                </>
              ) : (
                <>
                  {mfaStage === "setup" ? (
                    <>
                      <Typography variant="body2" className="helper">
                        Multi-factor authentication is required for Borealis accounts. Scan the QR
                        code with your authenticator app, then enter the 6-digit code to finish
                        setup for <strong>{username}</strong>.
                      </Typography>
                      {setupQr ? (
                        <Box sx={{ display: "flex", justifyContent: "center", mb: 1.5 }}>
                          <img
                            src={setupQr}
                            alt="MFA enrollment QR code"
                            style={{ width: 180, height: 180, borderRadius: 12, boxShadow: "0 6px 18px rgba(0,0,0,.4)" }}
                          />
                        </Box>
                      ) : null}
                      {formattedSecret ? (
                        <div className="mono-box">
                          <FieldLabel>Manual code</FieldLabel>
                          <div className="mono-text">{formattedSecret}</div>
                        </div>
                      ) : null}
                      {setupUri ? (
                        <Typography variant="caption" sx={{ color: "var(--text-dim)", wordBreak: "break-all", textAlign: "center" }}>
                          {setupUri}
                        </Typography>
                      ) : null}
                    </>
                  ) : (
                    <Typography variant="body2" className="helper">
                      Enter the 6-digit code from your authenticator app for <strong>{username}</strong>.
                    </Typography>
                  )}

                  <FieldLabel>One‑time code</FieldLabel>
                  <TextField
                    variant="outlined"
                    fullWidth
                    value={mfaCode}
                    onChange={onCodeChange}
                    disabled={isSubmitting}
                    inputProps={{
                      inputMode: "numeric",
                      pattern: "[0-9]*",
                      maxLength: 6,
                      style: { letterSpacing: "0.4rem", textAlign: "center", fontSize: "1.15rem" }
                    }}
                    autoComplete="one-time-code"
                    placeholder="• • • • • •"
                  />

                  {error && <div className="error">{error}</div>}

                  <Button
                    type="submit"
                    variant="contained"
                    className="submit"
                    disabled={isSubmitting || mfaCode.length < 6}
                  >
                    {isSubmitting ? "Verifying..." : "Verify code"}
                  </Button>

                  <Button
                    type="button"
                    variant="text"
                    className="muted-btn"
                    disabled={isSubmitting}
                    onClick={handleBackToLogin}
                  >
                    Use a different account
                  </Button>
                </>
              )}
            </Box>
          </div>
        </div>
      </div>
    </>
  );
}
