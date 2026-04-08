import { startAuthentication, startRegistration } from "@simplewebauthn/browser";

function extractError(payload, fallbackMessage) {
  const key = String(payload?.error || "").trim();
  const map = {
    passkeys_unavailable: "Passkeys are not available on this Borealis server yet.",
    passkey_pending: "Your passkey session expired. Please sign in again.",
    passkey_not_configured: "No passkey is configured for this account yet.",
    invalid_passkey: "The passkey response was invalid.",
    passkey_already_registered: "That passkey is already registered to this account.",
    invalid_session: "Your passkey session expired. Please sign in again.",
    expired: "Your passkey session expired. Please sign in again.",
    mfa_pending: "Your MFA session expired. Please sign in again.",
    user_not_found: "That Borealis account could not be found.",
  };
  return map[key] || key || fallbackMessage;
}

function normalizeBrowserError(error, fallbackMessage) {
  const name = String(error?.name || "");
  if (name === "NotAllowedError") {
    return "The passkey prompt was cancelled or no matching passkey was available.";
  }
  if (name === "InvalidStateError") {
    return "This passkey is already registered on this device.";
  }
  return error instanceof Error && error.message ? error.message : fallbackMessage;
}

async function readJson(response) {
  return response.json().catch(() => ({}));
}

export function isPasskeySupported() {
  return Boolean(window.PublicKeyCredential && window.isSecureContext);
}

export async function registerPasskey({ pendingToken = "", label = "" } = {}) {
  if (!isPasskeySupported()) {
    throw new Error("Passkeys are not available in this browser.");
  }

  const beginResponse = await fetch("/api/auth/passkeys/register/options", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "include",
    body: JSON.stringify({
      pending_token: pendingToken || undefined,
      label: label || undefined,
    }),
  });
  const beginPayload = await readJson(beginResponse);
  if (!beginResponse.ok) {
    throw new Error(extractError(beginPayload, "Failed to start passkey setup."));
  }

  let credential;
  try {
    credential = await startRegistration({ optionsJSON: beginPayload.options });
  } catch (error) {
    throw new Error(normalizeBrowserError(error, "Passkey setup was cancelled."));
  }

  const verifyResponse = await fetch("/api/auth/passkeys/register/verify", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "include",
    body: JSON.stringify({
      pending_token: pendingToken || undefined,
      request_id: beginPayload.request_id,
      credential,
      label: label || undefined,
    }),
  });
  const verifyPayload = await readJson(verifyResponse);
  if (!verifyResponse.ok) {
    throw new Error(extractError(verifyPayload, "Failed to verify the new passkey."));
  }
  return verifyPayload;
}

export async function authenticateWithPasskey({ pendingToken }) {
  if (!isPasskeySupported()) {
    throw new Error("Passkeys are not available in this browser.");
  }

  const beginResponse = await fetch("/api/auth/passkeys/authenticate/options", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "include",
    body: JSON.stringify({ pending_token: pendingToken || undefined }),
  });
  const beginPayload = await readJson(beginResponse);
  if (!beginResponse.ok) {
    throw new Error(extractError(beginPayload, "Failed to start passkey sign-in."));
  }

  let credential;
  try {
    credential = await startAuthentication({ optionsJSON: beginPayload.options });
  } catch (error) {
    throw new Error(normalizeBrowserError(error, "Passkey sign-in was cancelled."));
  }

  const verifyResponse = await fetch("/api/auth/passkeys/authenticate/verify", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "include",
    body: JSON.stringify({
      pending_token: pendingToken || undefined,
      request_id: beginPayload.request_id,
      credential,
    }),
  });
  const verifyPayload = await readJson(verifyResponse);
  if (!verifyResponse.ok) {
    throw new Error(extractError(verifyPayload, "Failed to verify the passkey sign-in."));
  }
  return verifyPayload;
}
