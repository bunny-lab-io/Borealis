import { describe, expect, it, vi } from "vitest";
import {
  installBorealisInputValidationGuard,
  sanitizeNotificationMessage,
  sanitizePayload,
  validateBorealisFetchRequest,
  validateInputValue,
} from "../../../src/app/utils/inputValidation.js";

describe("inputValidation", () => {
  it("sanitizes plain display text while preserving code content", () => {
    const result = sanitizePayload({
      name: "  Alpha\nBeta  ",
      content: "if (1 < 2) {\n  echo ok\n}",
    });

    expect(result.errors).toEqual([]);
    expect(result.value.name).toBe("Alpha Beta");
    expect(result.value.content).toContain("1 < 2");
    expect(result.value.content).toContain("\n");
  });

  it("rejects invalid regex fields", () => {
    const result = sanitizePayload({ regex: "[" });

    expect(result.errors).toHaveLength(1);
    expect(result.errors[0].field).toBe("regex");
  });

  it("validates hostnames, IPv6 CIDR, and malformed host input", () => {
    expect(validateInputValue("hostname", "borealis.local")).toBe("");
    expect(validateInputValue("cidr", "fd00::1/64")).toBe("");
    expect(validateInputValue("hostname", "borealis/evil")).toContain("hostname");
  });

  it("sanitizes API JSON bodies before fetch sends them", () => {
    const request = validateBorealisFetchRequest(
      "/api/sites",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name: "  Bunny\nLab  ", content: "Write-Output '<ok>'" }),
      },
      "https://borealis.test"
    );

    expect(request.errors).toEqual([]);
    expect(JSON.parse(request.init.body)).toEqual({
      name: "Bunny Lab",
      content: "Write-Output '<ok>'",
    });
  });

  it("keeps passkey verify credential JSON opaque", () => {
    const body = JSON.stringify({
      request_id: "pending.token.value",
      credential: {
        id: "abc+/=",
        rawId: "abc+/=",
        type: "public-key",
        response: {
          clientDataJSON: "abc+/=",
          authenticatorData: "abc+/=",
          signature: "abc+/=",
        },
      },
    });
    const request = validateBorealisFetchRequest(
      "/api/auth/passkeys/authenticate/verify",
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body,
      },
      "https://borealis.test"
    );

    expect(request.errors).toEqual([]);
    expect(request.init.body).toBe(body);
  });

  it("rejects unsafe same-origin API paths", () => {
    const request = validateBorealisFetchRequest("/api/devices/%3Cscript%3E", {}, "https://borealis.test");

    expect(request.errors).toEqual(expect.arrayContaining([expect.objectContaining({ field: "path" })]));
  });

  it("blocks unsafe same-origin API payloads before network fetch", async () => {
    const originalFetch = vi.fn(async () => new Response("ok", { status: 200 }));
    const win = {
      location: { origin: "https://borealis.test" },
      fetch: originalFetch,
    };
    installBorealisInputValidationGuard(win);

    const response = await win.fetch("/api/sites", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: "<script>alert(1)</script>" }),
    });

    expect(response.status).toBe(400);
    expect(originalFetch).not.toHaveBeenCalled();
    await expect(response.json()).resolves.toMatchObject({ error: "validation_failed" });
  });

  it("leaves non-API fetches untouched", async () => {
    const originalFetch = vi.fn(async () => new Response("ok", { status: 200 }));
    const win = {
      location: { origin: "https://borealis.test" },
      fetch: originalFetch,
    };
    installBorealisInputValidationGuard(win);

    const response = await win.fetch("https://assets.example/file.json", {
      method: "POST",
      body: JSON.stringify({ name: "<script>external</script>" }),
    });

    expect(response.status).toBe(200);
    expect(originalFetch).toHaveBeenCalledTimes(1);
  });

  it("preserves notification bold tags but strips executable tags", () => {
    const message = sanitizeNotificationMessage("Hello <b>World</b> <script>alert(1)</script>");

    expect(message).toContain("<b>World</b>");
    expect(message.toLowerCase()).not.toContain("<script");
  });
});
