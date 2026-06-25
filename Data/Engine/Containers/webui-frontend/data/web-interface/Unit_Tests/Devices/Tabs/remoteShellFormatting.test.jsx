import { describe, expect, it } from "vitest";
import {
  cleanShellOutput,
  highlightShellOutput,
  inferShellKind,
  shellLanguageForKind,
} from "@/Devices/Tabs/remoteShellFormatting.js";

describe("remote shell output formatting", () => {
  it("infers PowerShell for Windows devices and Bash for Linux devices", () => {
    expect(inferShellKind({ operating_system: "Microsoft Windows 11 Pro" })).toBe("powershell");
    expect(inferShellKind({ summary: { operating_system: "Ubuntu 24.04 LTS" } })).toBe("bash");
    expect(inferShellKind({})).toBe("powershell");
  });

  it("normalizes terminal output before display and copy", () => {
    const output = "one\r\n\u001b[31mred\u001b[0m\r\ntwo\rthree\u0007";

    expect(cleanShellOutput(output)).toBe("one\nred\ntwothree");
  });

  it("maps shell kinds to Prism languages", () => {
    expect(shellLanguageForKind("bash")).toBe("bash");
    expect(shellLanguageForKind("powershell")).toBe("powershell");
    expect(shellLanguageForKind("unknown")).toBe("powershell");
  });

  it("highlights PowerShell and Bash output with Prism token markup", () => {
    const powershell = highlightShellOutput(
      'Get-Process | Where-Object { $_.Name -eq "svchost" }',
      "powershell"
    );
    const bash = highlightShellOutput('sudo systemctl status nginx && echo "ok"', "bash");

    expect(powershell).toContain("token");
    expect(powershell).toContain("Get-Process");
    expect(bash).toContain("token");
    expect(bash).toContain("systemctl");
  });
});
