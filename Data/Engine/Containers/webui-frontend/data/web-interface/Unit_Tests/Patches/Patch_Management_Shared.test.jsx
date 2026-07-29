import { describe, expect, it } from "vitest";
import {
  normalizePatchPlatform,
  resolvePatchPlatformCopy,
} from "@/Patches/Patch_Management_Shared.jsx";

describe("patch management shared platform helpers", () => {
  it("normalizes supported platform tab keys and aliases", () => {
    expect(normalizePatchPlatform("windows")).toBe("windows");
    expect(normalizePatchPlatform("linux")).toBe("linux");
    expect(normalizePatchPlatform("mac")).toBe("macos");
    expect(normalizePatchPlatform("darwin")).toBe("macos");
    expect(normalizePatchPlatform("unsupported")).toBe("windows");
  });

  it("returns platform-specific tab copy", () => {
    expect(resolvePatchPlatformCopy("windows")).toMatchObject({ label: "Windows" });
    expect(resolvePatchPlatformCopy("linux")).toMatchObject({ label: "Linux" });
    expect(resolvePatchPlatformCopy("macos")).toMatchObject({ label: "MacOS" });
  });
});
