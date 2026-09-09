import { validateSSHInspectionCredential, validateSSHInspectionTarget, validObservedKey } from "./Cluster_SSH_Inspection.jsx";
import { FIELD_CLASS, validateInputValue } from "../app/utils/inputValidation.js";

export const SSH_INSPECTION_CONFIRMATION = "INSPECT ENGINE NODES";
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const byteLength = (value) => new TextEncoder().encode(value).length;
const targetFields = new Set(["address", "port", "host_key_algorithm", "host_key_fingerprint", "host_key_base64", "host_key_approved", "username", "auth_method", "password", "private_key", "passphrase", "sudo_password"]);

// Shared request contract for the upcoming cohort dialog. Validation preserves
// secret and public-key bytes; no storage, network call or preparation occurs.
export function validateSSHInspectionSubmission(body) {
  if (!body || typeof body !== "object" || Array.isArray(body)
    || Object.keys(body).some((key) => !["request_id", "targets", "confirmation"].includes(key))
    || typeof body.request_id !== "string" || !uuid.test(body.request_id)
    || body.confirmation !== SSH_INSPECTION_CONFIRMATION) return "Start a new inspection request.";
  if (!Array.isArray(body.targets) || body.targets.length < 1 || body.targets.length > 2) return "Choose one replacement or two joining Engine hosts.";
  const addresses = new Set();
  const fingerprints = new Set();
  for (const target of body.targets) {
    if (!target || typeof target !== "object" || Array.isArray(target) || Object.keys(target).some((key) => !targetFields.has(key))) return "Invalid target fields.";
    if (typeof target.address !== "string" || !Number.isInteger(target.port)) return "Enter a private IPv4 address and SSH port.";
    const targetError = validateSSHInspectionTarget(target.address, target.port);
    if (targetError) return targetError;
    if (target.host_key_approved !== true || !validObservedKey(target, target.address, target.port)) return "Verify and approve each target's SSH host key.";
    if (addresses.has(target.address) || fingerprints.has(target.host_key_fingerprint)) return "Targets must have distinct addresses and SSH host keys.";
    addresses.add(target.address); fingerprints.add(target.host_key_fingerprint);
    if ((target.auth_method === "password" && (Object.hasOwn(target, "private_key") || Object.hasOwn(target, "passphrase")))
      || (target.auth_method === "private_key" && Object.hasOwn(target, "password"))) return "Use one authentication method per target.";
    if (typeof target.username !== "string") return "Enter a Linux admin username.";
    const credentialError = validateSSHInspectionCredential(target.username, target.auth_method, { ...target, passphrase: target.passphrase ?? "" });
    if (credentialError) return credentialError;
    if (Object.hasOwn(target, "passphrase") && typeof target.passphrase !== "string") return "Enter a valid private key passphrase.";
    if (Object.hasOwn(target, "sudo_password")) {
      const value = target.sudo_password;
      if (typeof value !== "string" || byteLength(value) > 4096 || /[\r\n]/.test(value) || validateInputValue("sudo_password", value, FIELD_CLASS.SECRET)) return "Sudo password must be one line within 4096 bytes.";
    }
    if (byteLength(JSON.stringify(target)) > 128 * 1024) return "Target request exceeds 128 KiB.";
  }
  if (byteLength(JSON.stringify(body)) > 256 * 1024) return "Inspection request exceeds 256 KiB.";
  return "";
}

export function createSSHInspectionSubmission(targets, requestID = crypto.randomUUID()) {
  const body = { request_id: requestID, confirmation: SSH_INSPECTION_CONFIRMATION, targets };
  const error = validateSSHInspectionSubmission(body);
  if (error) throw new Error(error);
  return body;
}
