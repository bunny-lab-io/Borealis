import { describe, expect, it } from "vitest";
import { buildInstallCommand } from "@/Sites/Site_List.jsx";

describe("site install command builder", () => {
  it("builds Linux Agent bootstrap commands without a mandatory sudo pipe", () => {
    const command = buildInstallCommand(
      "linux",
      "https://borealis.example.com",
      "E925-448B-626D-D595-5A0F-FB24-B4D6-6983"
    );

    expect(command).toContain('| { if [ "$(id -u)" -eq 0 ]; then bash -s -- deploy');
    expect(command).toContain("else sudo bash -s -- deploy");
    expect(command).not.toContain("| sudo bash");
    expect(command).toContain('--serverurl "https://borealis.example.com"');
    expect(command).toContain('--enrollmentcode "E925-448B-626D-D595-5A0F-FB24-B4D6-6983"');
  });

  it("preserves branch selection while using the root-safe Linux bootstrap path", () => {
    const command = buildInstallCommand(
      "linux",
      "https://borealis.example.com",
      "CODE-1234",
      "feature/proxmox-agent"
    );

    expect(command).toContain('/refs/heads/feature/proxmox-agent/Agent.sh" | { if [ "$(id -u)" -eq 0 ]; then bash -s --');
    expect(command).toContain('--repo-branch "feature/proxmox-agent" deploy');
  });

  it("keeps macOS bootstrap commands on the plain bash path", () => {
    const command = buildInstallCommand(
      "macos",
      "https://borealis.example.com",
      "CODE-1234"
    );

    expect(command).toContain("| bash -s -- deploy");
    expect(command).not.toContain("sudo bash");
  });

  it("builds Windows Agent.exe bootstrap commands with canonical arguments", () => {
    const command = buildInstallCommand(
      "windows",
      "https://borealis.example.com",
      "CODE-1234",
      "feature/automatic-local-network-device-enrollment"
    );

    expect(command).toContain("Data/Agent/Bootstrap/Agent.exe");
    expect(command).toContain("Invoke-WebRequest -UseBasicParsing");
    expect(command).toContain('--server-url "https://borealis.example.com"');
    expect(command).toContain('--site-enrollment-code "CODE-1234"');
    expect(command).not.toContain(".ps1");
    expect(command).not.toContain("--serverurl");
    expect(command).not.toContain("--enrollmentcode");
  });
});
