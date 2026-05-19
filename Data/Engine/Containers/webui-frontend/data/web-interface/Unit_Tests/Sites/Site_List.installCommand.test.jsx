import { describe, expect, it } from "vitest";
import { buildInstallCommand } from "@/Sites/Site_List.jsx";

describe("site install command builder", () => {
  it("builds Linux Go Agent bootstrap commands without a mandatory sudo pipe", () => {
    const command = buildInstallCommand(
      "linux",
      "https://borealis.example.com",
      "E925-448B-626D-D595-5A0F-FB24-B4D6-6983"
    );

    expect(command).toContain("Data/Agent/dist/linux-amd64/Agent");
    expect(command).toContain("curl -fsSL");
    expect(command).toContain("mktemp /tmp/borealis-agent.XXXXXX");
    expect(command).toContain("trap 'rm -f \"$tmp_file\"' EXIT");
    expect(command).toContain('sudo "$tmp_file"');
    expect(command).not.toContain("| sudo bash");
    expect(command).not.toContain("/tmp/Borealis-Agent");
    expect(command).not.toContain("sudo install");
    expect(command).not.toContain("wget");
    expect(command).toContain('--server-url "https://borealis.example.com"');
    expect(command).toContain('--repo-ref "main"');
    expect(command).toContain('--site-enrollment-code "E925-448B-626D-D595-5A0F-FB24-B4D6-6983"');
    expect(command).toContain("--install-service");
    expect(command).not.toContain("Agent.exe");
  });

  it("preserves branch selection while using the Linux Go Agent artifact path", () => {
    const command = buildInstallCommand(
      "linux",
      "https://borealis.example.com",
      "CODE-1234",
      "feature/proxmox-agent"
    );

    expect(command).toContain("/refs/heads/feature/proxmox-agent/Data/Agent/dist/linux-amd64/Agent");
    expect(command).toContain('--repo-ref "feature/proxmox-agent"');
    expect(command).not.toContain("--repo-branch");
  });

  it("does not generate macOS Agent commands", () => {
    const command = buildInstallCommand(
      "macos",
      "https://borealis.example.com",
      "CODE-1234"
    );

    expect(command).toBe("");
  });

  it("builds Windows Agent.exe bootstrap commands with canonical arguments", () => {
    const command = buildInstallCommand(
      "windows",
      "https://borealis.example.com",
      "CODE-1234",
      "feature/test-agent-install"
    );

    expect(command).toContain("Data/Agent/dist/windows-amd64/Agent.exe");
    expect(command).toContain('Join-Path $env:TEMP "Borealis-Agent.exe"');
    expect(command).toContain("Invoke-WebRequest -UseBasicParsing");
    expect(command).toContain('--server-url "https://borealis.example.com"');
    expect(command).toContain('--repo-ref "feature/test-agent-install"');
    expect(command).toContain('--site-enrollment-code "CODE-1234"');
    expect(command).not.toContain('$ErrorActionPreference');
    expect(command).not.toContain("NewGuid");
    expect(command).not.toContain("try {");
    expect(command).not.toContain("Unblock-File");
    expect(command).not.toContain("Remove-Item");
    expect(command).not.toContain("Data/Agent/Bootstrap/Agent.exe");
    expect(command).not.toContain(".ps1");
    expect(command).not.toContain("--serverurl");
    expect(command).not.toContain("--enrollmentcode");
  });
});
