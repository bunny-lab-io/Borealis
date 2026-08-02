import React, { useEffect, useMemo, useState } from "react";
import { Box, Button, TextField, Typography } from "@mui/material";
import { useNavigate } from "react-router-dom";
import { APP_PATHS } from "./paths.js";

function resolveCopy(phase, hasPendingMfa) {
  if (hasPendingMfa) {
    return {
      title: "Administrator MFA Setup",
      subtitle:
        "Enter the 6-digit code from your authenticator app to finish securing this Borealis Engine and continue.",
      action: "Complete Setup",
    };
  }
  if (phase === "aegis_setup_required") {
    return {
      title: "Set Up Aegis Cipher",
      subtitle:
        "Borealis requires the Aegis Cipher before anything else. The Engine will encrypt stored operator auth data, credentials, GitHub tokens, and passkey material with it.",
      action: "Set Up Aegis Cipher",
    };
  }
  if (phase === "aegis_unlock_required") {
    return {
      title: "Unlock Borealis",
      subtitle:
        "Enter the Aegis Cipher to unlock the Engine after restart. Borealis login becomes available only after the Engine is unlocked.",
      action: "Unlock Engine",
    };
  }
  if (phase === "admin_recovery_required") {
    return {
      title: "Recover An Administrator",
      subtitle:
        "Aegis has been reset and operator auth secrets were wiped. Claim an existing administrator account, set a new password, and re-establish MFA to recover access.",
      action: "Start Admin Recovery",
    };
  }
  return {
    title: "Create The First Administrator",
    subtitle:
      "Aegis is ready. Create the first Borealis administrator account, then complete MFA enrollment before the Engine can be used.",
    action: "Create Administrator",
  };
}

export default function BootstrapEntry({
  bootstrapState,
  refreshBootstrapState,
  onAuthenticated,
}) {
  const navigate = useNavigate();
  const [cipher, setCipher] = useState("");
  const [confirmCipher, setConfirmCipher] = useState("");
  const [username, setUsername] = useState("admin");
  const [displayName, setDisplayName] = useState("Administrator");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [pendingToken, setPendingToken] = useState("");
  const [setupSecret, setSetupSecret] = useState("");
  const [setupQr, setSetupQr] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  const [error, setError] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);

  const phase = String(bootstrapState?.phase || "");
  const hasPendingMfa = Boolean(pendingToken);
  const copy = useMemo(() => resolveCopy(phase, hasPendingMfa), [hasPendingMfa, phase]);

  const FieldLabel = ({ children }) => (
    <Typography
      variant="caption"
      sx={{
        color: "var(--text-dim)",
        letterSpacing: ".04em",
        display: "block",
        mt: 1.25,
        mb: 0.1,
      }}
    >
      {children}
    </Typography>
  );

  useEffect(() => {
    if (phase === "admin_setup_required" || phase === "admin_recovery_required") {
      return;
    }
    setPendingToken("");
    setSetupSecret("");
    setSetupQr("");
    setMfaCode("");
  }, [phase]);

  const handleCipherSubmit = async (event) => {
    event.preventDefault();
    if (phase === "aegis_setup_required" && cipher !== confirmCipher) {
      setError("The Aegis Cipher confirmation does not match.");
      return;
    }

    setIsSubmitting(true);
    setError("");
    try {
      const endpoint =
        phase === "aegis_setup_required"
          ? "/api/bootstrap/aegis/setup"
          : "/api/bootstrap/aegis/unlock";
      const response = await fetch(endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ cipher }),
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.message || payload?.error || "Unable to continue.");
      }
      setCipher("");
      setConfirmCipher("");
      await refreshBootstrapState();
    } catch (nextError) {
      setError(nextError instanceof Error ? nextError.message : "Unable to continue.");
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleAdminStart = async (event) => {
    event.preventDefault();
    if (!username.trim() || !password) {
      setError("Enter a username and password.");
      return;
    }
    if (password !== confirmPassword) {
      setError("The password confirmation does not match.");
      return;
    }

    setIsSubmitting(true);
    setError("");
    try {
      const endpoint =
        phase === "admin_recovery_required"
          ? "/api/bootstrap/admin/recover"
          : "/api/bootstrap/admin/setup";
      const response = await fetch(endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          username: username.trim(),
          display_name: displayName.trim(),
          password,
        }),
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.error || "Unable to continue.");
      }
      setPendingToken(String(payload?.pending_token || ""));
      setSetupSecret(String(payload?.secret || ""));
      setSetupQr(String(payload?.qr_image || ""));
      setPassword("");
      setConfirmPassword("");
    } catch (nextError) {
      setError(nextError instanceof Error ? nextError.message : "Unable to continue.");
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleMfaVerify = async (event) => {
    event.preventDefault();
    if (!pendingToken || mfaCode.trim().length < 6) {
      setError("Enter the 6-digit code from your authenticator app.");
      return;
    }

    setIsSubmitting(true);
    setError("");
    try {
      const response = await fetch("/api/bootstrap/admin/mfa/verify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({ pending_token: pendingToken, code: mfaCode }),
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(payload?.error || "Unable to complete MFA verification.");
      }
      await refreshBootstrapState();
      if (typeof onAuthenticated === "function") {
        await onAuthenticated({ username: payload.username, role: payload.role });
      }
    } catch (nextError) {
      setError(
        nextError instanceof Error ? nextError.message : "Unable to complete MFA verification."
      );
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <>
      <style>{`
        :root {
          --bg: #0b0e14;
          --panel: #0f1320;
          --text: #e6f1ff;
          --text-dim: #9ab0c8;
          --accent: #58a6ff;
          --accent-2: #a78bfa;
          --accent-3: #22d3ee;
          --radius-xl: 20px;
          --card-w: min(94vw, 540px);
        }

        .bootstrap-wrap {
          position: relative;
          display: grid;
          place-items: center;
          min-height: 100vh;
          padding: 24px;
          background: radial-gradient(1200px 800px at 80% 20%, rgba(75,0,130,.18), transparent 60%),
                      radial-gradient(900px 700px at 10% 90%, rgba(0,128,128,.16), transparent 60%),
                      linear-gradient(160deg, #0b0e14 0%, #0a0f1f 50%, #0b0e14 100%);
          overflow: hidden;
          isolation: isolate;
        }

        .bootstrap-aurora {
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
          animation: bootstrapAuroraShift 22s ease-in-out infinite alternate;
        }

        @keyframes bootstrapAuroraShift {
          0%   { transform: translate3d(-3%, -2%, 0) scale(1.02); }
          50%  { transform: translate3d(2%, 1%, 0) scale(1.04); }
          100% { transform: translate3d(-1%, 3%, 0) scale(1.03); }
        }

        .bootstrap-grid {
          position: absolute;
          inset: 0;
          background-image:
            linear-gradient(rgba(255,255,255,0.04) 1px, transparent 1px),
            linear-gradient(90deg, rgba(255,255,255,0.04) 1px, transparent 1px);
          background-size: 32px 32px, 32px 32px;
          mask-image: radial-gradient(60% 60% at 50% 50%, black 60%, transparent 100%);
          z-index: 1;
        }

        .bootstrap-grid::after {
          content: "";
          position: absolute;
          inset: 0;
          background:
            repeating-radial-gradient(circle at 30% 30%, rgba(88,166,255,0.04) 0 2px, transparent 2px 6px);
          mix-blend-mode: screen;
          animation: bootstrapFlicker 4s infinite steps(60);
          opacity: .7;
        }

        @keyframes bootstrapFlicker {
          0%, 19%, 21%, 23%, 25%, 54%, 56%, 100% { opacity: .75; }
          20%, 24%, 55% { opacity: .45; }
        }

        .bootstrap-shine {
          position: relative;
          width: var(--card-w);
          border-radius: var(--radius-xl);
          box-shadow:
            0 0 0 1px rgba(255,255,255,0.06) inset,
            0 10px 30px rgba(0,0,0,.5);
          z-index: 2;
        }

        .bootstrap-shine-inner {
          border-radius: inherit;
          position: relative;
          border: 1px solid transparent;
          background:
            linear-gradient(180deg, rgba(17,22,36,.92), rgba(11,14,20,.92)) padding-box,
            conic-gradient(from 0deg,
              rgba(88,166,255,.0),
              rgba(88,166,255,.45),
              rgba(34,211,238,.0),
              rgba(125,255,183,0.3),
              rgba(34,211,238,.0),
              rgba(88,166,255,.45),
              rgba(167,139,250,.0)) border-box;
          backdrop-filter: blur(10px);
          padding: 30px 28px;
        }

        .bootstrap-brand {
          display: flex;
          justify-content: center;
          margin-bottom: 6px;
        }

        .bootstrap-brand img {
          width: min(100%, 470px);
          height: auto;
        }

        .bootstrap-title {
          margin: 0 0 8px 0;
          color: var(--text);
          text-align: center;
          font-weight: 700;
          letter-spacing: .02em;
        }

        .bootstrap-helper {
          color: var(--text-dim);
          text-align: center;
          margin-bottom: 8px;
        }

        .bootstrap-submit {
          margin-top: 12px;
          background: linear-gradient(135deg, #34d399, #22d3ee);
          color: #0b0e14;
          font-weight: 700;
          text-transform: none;
          border-radius: 14px;
          box-shadow: 0 8px 18px rgba(88,166,255,.25);
        }

        .bootstrap-submit:hover {
          filter: brightness(1.05);
          box-shadow: 0 10px 22px rgba(88,166,255,.35);
        }

        .bootstrap-secret-box {
          background: #0f172a;
          border: 1px solid rgba(255,255,255,.08);
          border-radius: 12px;
          padding: 10px 12px;
          margin-bottom: 12px;
        }

        .bootstrap-secret-text {
          font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
          letter-spacing: .28rem;
          color: #e5e7eb;
          text-align: center;
          user-select: all;
          word-break: break-word;
        }

        .bootstrap-error {
          color: #ff6b6b;
          text-align: center;
          margin-top: 8px;
          font-size: .9rem;
        }
      `}</style>

      <div className="bootstrap-wrap">
        <div className="bootstrap-aurora" />
        <div className="bootstrap-grid" />

        <div className="bootstrap-shine">
          <div className="bootstrap-shine-inner">
            <Box
              component="form"
              onSubmit={
                hasPendingMfa
                  ? handleMfaVerify
                  : phase === "aegis_setup_required" || phase === "aegis_unlock_required"
                  ? handleCipherSubmit
                  : handleAdminStart
              }
              sx={{ display: "flex", flexDirection: "column", gap: 1 }}
            >
              <div className="bootstrap-brand">
                <img src="/Borealis_Logo_Full.png" alt="Borealis Automation" />
              </div>

              <Typography variant="h5" className="bootstrap-title">
                {copy.title}
              </Typography>
              <Typography variant="body2" className="bootstrap-helper">
                {copy.subtitle}
              </Typography>

              {phase === "aegis_setup_required" && !hasPendingMfa ? (
                <>
                  <FieldLabel>New Aegis Cipher</FieldLabel>
                  <TextField
                    type="password"
                    value={cipher}
                    onChange={(event) => setCipher(event.target.value)}
                    fullWidth
                    disabled={isSubmitting}
                    InputProps={{ sx: { borderRadius: 2 } }}
                  />
                  <FieldLabel>Confirm Aegis Cipher</FieldLabel>
                  <TextField
                    type="password"
                    value={confirmCipher}
                    onChange={(event) => setConfirmCipher(event.target.value)}
                    fullWidth
                    disabled={isSubmitting}
                    InputProps={{ sx: { borderRadius: 2 } }}
                  />
                </>
              ) : null}

              {phase === "aegis_unlock_required" && !hasPendingMfa ? (
                <>
                  <FieldLabel>Aegis Cipher</FieldLabel>
                  <TextField
                    type="password"
                    value={cipher}
                    onChange={(event) => setCipher(event.target.value)}
                    fullWidth
                    disabled={isSubmitting}
                    InputProps={{ sx: { borderRadius: 2 } }}
                  />
                </>
              ) : null}

              {(phase === "admin_setup_required" || phase === "admin_recovery_required") && !hasPendingMfa ? (
                <>
                  <FieldLabel>Administrator Username</FieldLabel>
                  <TextField
                    value={username}
                    onChange={(event) => setUsername(event.target.value)}
                    fullWidth
                    disabled={isSubmitting}
                    InputProps={{ sx: { borderRadius: 2 } }}
                  />
                  {phase === "admin_setup_required" ? (
                    <>
                      <FieldLabel>Display Name</FieldLabel>
                      <TextField
                        value={displayName}
                        onChange={(event) => setDisplayName(event.target.value)}
                        fullWidth
                        disabled={isSubmitting}
                        InputProps={{ sx: { borderRadius: 2 } }}
                      />
                    </>
                  ) : null}
                  <FieldLabel>
                    {phase === "admin_recovery_required" ? "New Password" : "Password"}
                  </FieldLabel>
                  <TextField
                    type="password"
                    value={password}
                    onChange={(event) => setPassword(event.target.value)}
                    fullWidth
                    disabled={isSubmitting}
                    InputProps={{ sx: { borderRadius: 2 } }}
                  />
                  <FieldLabel>Confirm Password</FieldLabel>
                  <TextField
                    type="password"
                    value={confirmPassword}
                    onChange={(event) => setConfirmPassword(event.target.value)}
                    fullWidth
                    disabled={isSubmitting}
                    InputProps={{ sx: { borderRadius: 2 } }}
                  />
                </>
              ) : null}

              {hasPendingMfa ? (
                <>
                  {setupQr ? (
                    <Box sx={{ display: "flex", justifyContent: "center", mb: 1.5 }}>
                      <Box
                        component="img"
                        src={setupQr}
                        alt="Borealis MFA QR code"
                        sx={{
                          width: 180,
                          height: 180,
                          borderRadius: 2,
                          background: "#fff",
                          p: 1.5,
                          boxShadow: "0 6px 18px rgba(0,0,0,.4)",
                        }}
                      />
                    </Box>
                  ) : null}
                  {setupSecret ? (
                    <div className="bootstrap-secret-box">
                      <FieldLabel>Manual code</FieldLabel>
                      <div className="bootstrap-secret-text">{setupSecret}</div>
                    </div>
                  ) : null}
                  <FieldLabel>Authenticator Code</FieldLabel>
                  <TextField
                    value={mfaCode}
                    onChange={(event) => setMfaCode(event.target.value.replace(/\D/g, "").slice(0, 6))}
                    fullWidth
                    disabled={isSubmitting}
                    inputProps={{
                      inputMode: "numeric",
                      pattern: "[0-9]*",
                      maxLength: 6,
                      style: { letterSpacing: "0.4rem", textAlign: "center", fontSize: "1.15rem" },
                    }}
                    autoComplete="one-time-code"
                    placeholder="• • • • • •"
                    InputProps={{ sx: { borderRadius: 2 } }}
                  />
                </>
              ) : null}

              {error ? <div className="bootstrap-error">{error}</div> : null}

              <Button
                type="submit"
                variant="contained"
                className="bootstrap-submit"
                disabled={isSubmitting}
              >
                {copy.action}
              </Button>
              {phase === "aegis_setup_required" && !hasPendingMfa ? (
                <Button
                  type="button"
                  variant="outlined"
                  onClick={() => navigate(APP_PATHS.bootstrapBackupRestore)}
                  disabled={isSubmitting}
                  sx={{
                    mt: 0.5,
                    borderRadius: "14px",
                    textTransform: "none",
                    fontWeight: 700,
                    color: "#dbeafe",
                    borderColor: "rgba(125, 211, 252, 0.45)",
                  }}
                >
                  Restore Engine Config Backup
                </Button>
              ) : null}
            </Box>
          </div>
        </div>
      </div>
    </>
  );
}
