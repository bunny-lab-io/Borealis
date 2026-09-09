import { describe, expect, it } from "vitest";
import { createSSHInspectionSubmission, validateSSHInspectionSubmission } from "@/Admin/clusterSSHOnboarding.js";
import { validateBorealisFetchRequest } from "@/app/utils/inputValidation.js";

const requestID = "5bdb3c22-6cf3-4c18-a22a-c020e47bd286";
const targets = () => [1, 2].map((n) => ({ address: `192.168.90.${20 + n}`, port: 22, username: "operator", auth_method: "password",
  password: "  <password> $ exact  ", sudo_password: "  <sudo> $ exact  ", host_key_approved: true,
  host_key_algorithm: "ssh-ed25519", host_key_fingerprint: `SHA256:${String(n).repeat(43)}`, host_key_base64: "onerror=" }));

describe("SSH inspection submission contract", () => {
  it("keeps one request identity and exact nested secret/wire values through shared fetch guard", () => {
    const input = targets();
    input[1].auth_method = "private_key";
    delete input[1].password;
    input[1].private_key = "-----BEGIN OPENSSH PRIVATE KEY-----\nfixture\n-----END OPENSSH PRIVATE KEY-----";
    input[1].passphrase = "  <passphrase> $ exact  ";
    const body = createSSHInspectionSubmission(input, requestID);
    const checked = validateBorealisFetchRequest("/api/server/cluster/onboarding/operations", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }, "https://borealis.test");
    expect(checked.errors).toEqual([]);
    expect(JSON.parse(checked.init.body)).toEqual(body);
    expect(body.request_id).toBe(requestID);
    expect(body.confirmation).toBe("INSPECT ENGINE NODES");
    expect(validateSSHInspectionSubmission(createSSHInspectionSubmission(targets().slice(0, 1), requestID))).toBe("");
  });

  it.each(["unapproved", "duplicate address", "duplicate key", "invalid port", "sudo newline", "sudo byte limit", "sudo NUL", "mixed credentials", "null passphrase", "extra field", "invalid request ID", "too many targets"])("rejects %s before submission", (mode) => {
    const body = createSSHInspectionSubmission(targets(), requestID);
    const target = body.targets[1];
    if (mode === "unapproved") target.host_key_approved = false;
    if (mode === "duplicate address") target.address = body.targets[0].address;
    if (mode === "duplicate key") target.host_key_fingerprint = body.targets[0].host_key_fingerprint;
    if (mode === "invalid port") target.port = 22.5;
    if (mode === "sudo newline") target.sudo_password = "line1\nline2";
    if (mode === "sudo byte limit") target.sudo_password = "é".repeat(2049);
    if (mode === "sudo NUL") target.sudo_password = "before\0after";
    if (mode === "mixed credentials") target.private_key = "key";
    if (mode === "null passphrase") target.passphrase = null;
    if (mode === "extra field") target.command = "anything";
    if (mode === "invalid request ID") body.request_id = "../operations";
    if (mode === "too many targets") body.targets.push({ ...target });
    expect(validateSSHInspectionSubmission(body)).not.toBe("");
  });
});
