import { describe, expect, it } from "vitest";
import {
  buildRDPConnectionCredential,
  eligibleRDPCredentials,
  validateManualRDPCredential,
} from "../../../src/Devices/Tabs/remoteDesktopRdp.js";

describe("remote desktop RDP credentials", () => {
  it("offers only usable global and matching-site Windows credentials", () => {
    const credentials = [
      { id: 1, name: "Global Windows", credential_type: "machine", connection_type: "windows", username: "admin", has_password: true },
      { id: 2, name: "Site Windows", site_id: 7, credential_type: "domain", connection_type: "winrm", username: "admin", has_password: true },
      { id: 3, name: "Other Site", site_id: 8, credential_type: "machine", connection_type: "windows", username: "admin", has_password: true },
      { id: 4, name: "SSH", credential_type: "machine", connection_type: "ssh", username: "root", has_password: true },
      { id: 5, name: "Reset", credential_type: "machine", connection_type: "windows", username: "admin", has_password: true, secret_reset_required: true },
      { id: 6, name: "Lost", credential_type: "machine", connection_type: "windows", username: "admin", has_password: true, lost_secret_fields: ["password"] },
    ];

    expect(eligibleRDPCredentials(credentials, 7).map((credential) => credential.id)).toEqual([1, 2]);
  });

  it("sends only stored credential ID for stored credential connections", () => {
    expect(buildRDPConnectionCredential("17", {})).toEqual({ credential_id: 17 });
  });

  it("preserves manual password syntax while trimming username and domain edges", () => {
    const values = {
      username: "  LAB\\operator  ",
      password: " leading & trailing ",
      domain: "  LAB  ",
    };

    expect(validateManualRDPCredential(values)).toBe("");
    expect(buildRDPConnectionCredential("manual", values)).toEqual({
      rdp_username: "LAB\\operator",
      rdp_password: " leading & trailing ",
      rdp_domain: "LAB",
    });
  });

  it("rejects missing fields, controls, and oversized RDP fields", () => {
    expect(validateManualRDPCredential({ username: "", password: "secret" })).toBe("Username required.");
    expect(validateManualRDPCredential({ username: "admin", password: "" })).toBe("Password required.");
    expect(validateManualRDPCredential({ username: "admin\nother", password: "secret" })).toContain("control characters");
    expect(validateManualRDPCredential({ username: "admin", password: "x".repeat(4097) })).toContain("4096");
  });
});
