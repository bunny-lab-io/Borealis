import { FIELD_CLASS, validateInputValue } from "../app/utils/inputValidation.js";
import { validateSSHInspectionTarget } from "./Cluster_SSH_Inspection.jsx";

export const SSH_PREPARATION_CONFIRMATION = "PREPARE ENGINE NODES";
export const SSH_PREPARATION_UI_FRESH_MS = 15000;
const uuid = (value) => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
const digest = (value, size) => typeof value === "string" && new RegExp(`^[0-9a-f]{${size}}$`).test(value) && value !== "0".repeat(size);
const hostname = (value) => typeof value === "string" && /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(value);
const object = (value) => value !== null && typeof value === "object" && !Array.isArray(value);
const fields = (value, keys) => object(value) && Object.keys(value).length === keys.length && keys.every((key) => Object.hasOwn(value, key));
const timestamp = (value) => Number.isSafeInteger(value) && value > 0 && value <= 8640000000000;
const checkCodes = ["source_inputs", "host_profile", "replica_capacity", "network_boot", "network_arp", "remaining_prerequisites"];
const pendingKey = (operationID) => `borealis:ssh-preparation-intent:v1:${operationID}`;

// Public identity only, retained before POST across dialog/page reload. This
// negative fence cannot authorize a write or expire an uncertain outcome.
export function readPendingSSHPreparation(operationID) {
  if (!uuid(operationID)) throw new Error("Invalid preparation operation.");
  const raw = window.sessionStorage.getItem(pendingKey(operationID));
  if (raw === null) return null;
  if (raw.length > 512) throw new Error("Invalid retained preparation request.");
  const pending = JSON.parse(raw);
  if (!fields(pending, ["operation_id", "attempt", "plan_sha256", "request_id", "source_id"]) || pending.operation_id !== operationID
    || !Number.isSafeInteger(pending.attempt) || pending.attempt < 1 || !digest(pending.plan_sha256, 64)
    || !uuid(pending.request_id) || !uuid(pending.source_id)) throw new Error("Invalid retained preparation request.");
  return pending;
}

export function retainPendingSSHPreparation(operationID, attempt, body) {
  if (readPendingSSHPreparation(operationID)) throw new Error("Preparation request already awaits reconciliation.");
  const pending = { operation_id: operationID, attempt, plan_sha256: body.plan_sha256, request_id: body.request_id, source_id: body.source_id };
  const raw = JSON.stringify(pending);
  window.sessionStorage.setItem(pendingKey(operationID), raw);
  if (window.sessionStorage.getItem(pendingKey(operationID)) !== raw || !readPendingSSHPreparation(operationID)) throw new Error("Preparation request could not be retained.");
  return pending;
}

export function clearPendingSSHPreparation(operationID, expected) {
  const pending = readPendingSSHPreparation(operationID);
  if (pending) {
    if (!["operation_id", "attempt", "plan_sha256", "request_id", "source_id"].every((key) => pending[key] === expected[key])) throw new Error("Retained preparation request changed.");
    window.sessionStorage.removeItem(pendingKey(operationID));
    if (readPendingSSHPreparation(operationID) !== null) throw new Error("Preparation request remains retained.");
  }
}

export function preparationProgressEligible(progress, operationID) {
  return uuid(operationID) && progress?.operation_id === operationID && progress.state === "waiting"
    && progress.current_step === "qualify_ssh_targets" && Number.isSafeInteger(progress.attempt) && progress.attempt > 0
    && Array.isArray(progress.targets) && [1, 2].includes(progress.targets.length)
    && progress.targets.every((target) => target?.state === "queued" && target.current_step === "qualification_complete"
      && target.credentials_available === true && target.inspected_attempt === progress.attempt && target.report)
    && progress.qualification?.attempt === progress.attempt && progress.qualification.ready === false
    && Array.isArray(progress.qualification.checks) && progress.qualification.checks.length === checkCodes.length
    && progress.qualification.checks.every((check, index) => check?.code === checkCodes[index]
      && check.state === (index === checkCodes.length - 1 ? "pending" : "passed"));
}

// Receipt age is only a browser stale-display guard. The API owns proposal
// expiry and every preparation gate; this is not a server readiness deadline.
export function preparationProgressFresh(receivedAt, now = Date.now()) {
  return receivedAt > 0 && now >= receivedAt && now - receivedAt <= SSH_PREPARATION_UI_FRESH_MS;
}

export function preparationProgressBinding(progress, operationID) {
  return JSON.stringify([operationID, progress?.operation_id, progress?.attempt, progress?.state, progress?.current_step,
    progress?.qualification, progress?.targets?.map((target) => [target.id, target.address, target.port, target.host_key_fingerprint,
      target.state, target.current_step, target.credentials_available, target.inspected_at, target.inspected_attempt,
      target.inspected_generation, target.report])]);
}

export function validSSHPreparationPlan(plan, progress, operationID) {
  if (!fields(plan, ["operation_id", "attempt", "plan_sha256", "baseline_release", "source_sha", "targets", "identity_sources", "confirmation", "preparation_authorized", "state"])
    || !preparationProgressEligible(progress, operationID) || plan.operation_id !== operationID || plan.attempt !== progress.attempt
    || !digest(plan.plan_sha256, 64) || !digest(plan.source_sha, 40) || plan.preparation_authorized !== false || plan.state !== "awaiting_readiness"
    || typeof plan.baseline_release !== "string" || plan.baseline_release.length > 64
    || !/^[0-9]{4}\.[0-9]{1,2}\.[0-9]+(?:\.[0-9]+)?(?:-rc\.[1-9][0-9]*)?$/.test(plan.baseline_release)
    || !Array.isArray(plan.targets) || plan.targets.length !== progress.targets.length || !Array.isArray(plan.identity_sources)
    // Existing queue contract: two joining targets from one source, or one
    // replacement target with two original surviving sources.
    || plan.identity_sources.length !== 3 - plan.targets.length) return false;
  const targets = new Set(), names = new Set();
  for (let index = 0; index < plan.targets.length; index++) {
    const target = plan.targets[index], known = progress.targets[index];
    if (!fields(target, ["target_id", "address", "port", "hostname", "host_key_fingerprint"]) || !uuid(target.target_id) || targets.has(target.target_id)
      || !hostname(target.hostname) || names.has(target.hostname) || typeof target.address !== "string" || !Number.isInteger(target.port) || validateSSHInspectionTarget(target.address, target.port)
      || !/^SHA256:[A-Za-z0-9+/]{43}$/.test(target.host_key_fingerprint) || target.target_id !== known.id || target.address !== known.address
      || target.port !== known.port || target.hostname !== known.report.hostname || target.host_key_fingerprint !== known.host_key_fingerprint) return false;
    targets.add(target.target_id); names.add(target.hostname);
  }
  const sources = new Set();
  for (const source of plan.identity_sources) {
    if (!fields(source, ["source_id", "hostname"]) || !uuid(source.source_id) || targets.has(source.source_id)
      || sources.has(source.source_id) || !hostname(source.hostname) || names.has(source.hostname)) return false;
    sources.add(source.source_id); names.add(source.hostname);
  }
  return plan.confirmation === null || (fields(plan.confirmation, ["request_id", "confirmed_at", "source_id"])
    && uuid(plan.confirmation.request_id) && timestamp(plan.confirmation.confirmed_at) && sources.has(plan.confirmation.source_id));
}

export function sameSSHPreparationPlan(left, right) {
  const targets = (plan) => plan.targets.map((target) => [target.target_id, target.address, target.port, target.hostname, target.host_key_fingerprint]);
  const sources = (plan) => plan.identity_sources.map((source) => [source.source_id, source.hostname]);
  return left.operation_id === right.operation_id && left.attempt === right.attempt && left.plan_sha256 === right.plan_sha256
    && left.baseline_release === right.baseline_release && left.source_sha === right.source_sha
    && JSON.stringify(targets(left)) === JSON.stringify(targets(right)) && JSON.stringify(sources(left)) === JSON.stringify(sources(right));
}

export function createSSHPreparationRequest(plan, sourceID, confirmation, requestID = crypto.randomUUID()) {
  if (!uuid(requestID) || !digest(plan?.plan_sha256, 64) || !uuid(sourceID) || !plan.identity_sources.some((source) => source.source_id === sourceID)
    || typeof confirmation !== "string" || confirmation.length > 32 || validateInputValue("confirmation", confirmation, FIELD_CLASS.PLAIN_SINGLE_LINE)
    || confirmation !== SSH_PREPARATION_CONFIRMATION) throw new Error("Review the plan, select its identity source and enter the exact confirmation.");
  return Object.freeze({ request_id: requestID, plan_sha256: plan.plan_sha256, source_id: sourceID, confirmation });
}

export function validSSHPreparationReceipt(receipt, operationID, request) {
  return fields(receipt, ["operation_id", "request_id", "plan_sha256", "confirmed_at", "source_id", "state", "preparation_authorized"])
    && receipt.operation_id === operationID && receipt.request_id === request.request_id && receipt.plan_sha256 === request.plan_sha256
    && receipt.source_id === request.source_id && timestamp(receipt.confirmed_at) && receipt.state === "awaiting_readiness" && receipt.preparation_authorized === false;
}
