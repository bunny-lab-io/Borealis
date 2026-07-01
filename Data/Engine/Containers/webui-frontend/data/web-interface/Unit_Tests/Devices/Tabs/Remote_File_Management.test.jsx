import { describe, expect, it } from "vitest";
import {
  buildRemoteChildPath,
  validateNewTextFileName,
} from "@/Devices/Tabs/Remote_File_Management.jsx";

describe("Remote File Management helpers", () => {
  it("validates new text file names for target platform", () => {
    expect(validateNewTextFileName("script.ps1", "windows")).toBe("");
    expect(validateNewTextFileName("notes.log", "linux")).toBe("");
    expect(validateNewTextFileName("../script.ps1", "linux")).toContain("path separators");
    expect(validateNewTextFileName("CON", "windows")).toContain("reserved");
    expect(validateNewTextFileName("script.ps1.", "windows")).toContain("space or period");
  });

  it("builds child file paths using remote platform separators", () => {
    expect(buildRemoteChildPath("C:\\Temp\\", "script.ps1", "windows")).toBe("C:\\Temp\\script.ps1");
    expect(buildRemoteChildPath("/opt/Borealis/", "script.sh", "linux")).toBe("/opt/Borealis/script.sh");
    expect(buildRemoteChildPath("/", "agent.log", "linux")).toBe("/agent.log");
  });
});
