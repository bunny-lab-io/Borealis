import { FIELD_CLASS, validateInputValue } from "../../app/utils/inputValidation.js";

export const REMOTE_DESKTOP_PROTOCOL_VNC = "vnc";
export const REMOTE_DESKTOP_PROTOCOL_RDP = "rdp";
export const RDP_USERNAME_MAX_LENGTH = 256;
export const RDP_DOMAIN_MAX_LENGTH = 256;
export const RDP_PASSWORD_MAX_LENGTH = 4096;
export const RDP_VIEWPORT_DEFAULT_WIDTH = 1440;
export const RDP_VIEWPORT_DEFAULT_HEIGHT = 900;
export const RDP_VIEWPORT_DPI = 96;
export const RDP_VIEWPORT_MIN_WIDTH = 320;
export const RDP_VIEWPORT_MIN_HEIGHT = 200;
export const RDP_VIEWPORT_MAX_DIMENSION = 8192;

function normalizedSiteId(value) {
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null;
}

function utf8Length(value) {
  return new TextEncoder().encode(String(value ?? "")).length;
}

function rdpViewportDimension(value, fallback, minimum) {
  const parsed = Math.round(Number(value));
  if (!Number.isFinite(parsed) || parsed < minimum) return fallback;
  return Math.min(parsed, RDP_VIEWPORT_MAX_DIMENSION);
}

export function rdpViewportDimensions(element) {
  const rect = element?.getBoundingClientRect?.() || null;
  const measuredWidth = Number(rect?.width || element?.clientWidth || 0);
  const measuredHeight = Number(rect?.height || element?.clientHeight || 0);
  return {
    width: rdpViewportDimension(
      measuredWidth,
      RDP_VIEWPORT_DEFAULT_WIDTH,
      RDP_VIEWPORT_MIN_WIDTH
    ),
    height: rdpViewportDimension(
      measuredHeight,
      RDP_VIEWPORT_DEFAULT_HEIGHT,
      RDP_VIEWPORT_MIN_HEIGHT
    ),
    dpi: RDP_VIEWPORT_DPI,
  };
}

export function eligibleRDPCredentials(credentials, deviceSiteId) {
  const targetSiteId = normalizedSiteId(deviceSiteId);
  return (Array.isArray(credentials) ? credentials : []).filter((credential) => {
    const id = Number(credential?.id);
    const credentialType = String(credential?.credential_type || "").trim().toLowerCase();
    const connectionType = String(credential?.connection_type || "").trim().toLowerCase();
    const credentialSiteId = normalizedSiteId(credential?.site_id);
    const lostSecretFields = Array.isArray(credential?.lost_secret_fields)
      ? credential.lost_secret_fields.map((field) => String(field || "").trim().toLowerCase())
      : [];
    return (
      Number.isSafeInteger(id) &&
      id > 0 &&
      ["machine", "domain"].includes(credentialType) &&
      ["windows", "winrm"].includes(connectionType) &&
      Boolean(String(credential?.username || "").trim()) &&
      Boolean(credential?.has_password) &&
      !Boolean(credential?.secret_reset_required) &&
      !lostSecretFields.includes("password") &&
      (credentialSiteId === null || (targetSiteId !== null && credentialSiteId === targetSiteId))
    );
  });
}

export function rdpCredentialLabel(credential) {
  const name = String(credential?.name || credential?.username || `Credential ${credential?.id || ""}`).trim();
  const scope = credential?.site_id
    ? String(credential?.site_name || `Site ${credential.site_id}`).trim()
    : "Global";
  return `${name} (${scope})`;
}

export function validateManualRDPCredential(values) {
  const username = String(values?.username ?? "");
  const password = String(values?.password ?? "");
  const domain = String(values?.domain ?? "");
  if (!username.trim()) return "Username required.";
  if (!password) return "Password required.";
  if (utf8Length(username) > RDP_USERNAME_MAX_LENGTH) {
    return `Username exceeds ${RDP_USERNAME_MAX_LENGTH} bytes.`;
  }
  if (utf8Length(domain) > RDP_DOMAIN_MAX_LENGTH) {
    return `Domain exceeds ${RDP_DOMAIN_MAX_LENGTH} bytes.`;
  }
  if (utf8Length(password) > RDP_PASSWORD_MAX_LENGTH) {
    return `Password exceeds ${RDP_PASSWORD_MAX_LENGTH} bytes.`;
  }
  const usernameError = validateInputValue("RDP username", username, FIELD_CLASS.PLAIN_SINGLE_LINE);
  if (usernameError) return usernameError;
  const domainError = validateInputValue("RDP domain", domain, FIELD_CLASS.PLAIN_SINGLE_LINE);
  if (domainError) return domainError;
  const passwordError = validateInputValue("RDP password", password, FIELD_CLASS.SECRET);
  if (passwordError) return passwordError;
  if (/[\u0000-\u001f\u007f]/.test(password)) {
    return "RDP password cannot include control characters.";
  }
  return "";
}

export function buildRDPConnectionCredential(selection, values) {
  if (selection !== "manual") {
    const credentialId = Number(selection);
    if (!Number.isSafeInteger(credentialId) || credentialId <= 0) {
      throw new Error("Select valid stored credential.");
    }
    return { credential_id: credentialId };
  }
  const error = validateManualRDPCredential(values);
  if (error) throw new Error(error);
  return {
    rdp_username: String(values?.username ?? "").trim(),
    rdp_password: String(values?.password ?? ""),
    rdp_domain: String(values?.domain ?? "").trim(),
  };
}
