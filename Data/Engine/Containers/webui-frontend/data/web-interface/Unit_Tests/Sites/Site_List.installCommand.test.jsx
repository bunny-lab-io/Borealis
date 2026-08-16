import { describe, expect, it } from "vitest";
import {
  buildInstallCommand,
  insertExpandedInstallLinkRows,
  siteInstallLinkForOS,
  siteInstallLinkGridRows,
  siteInstallLinkSummary,
  siteInstallLinkStatus,
} from "@/Sites/Site_List.jsx";

describe("site install command builder", () => {
  it("builds Linux Go Agent commands from signed Engine downloads", () => {
    const command = buildInstallCommand(
      "linux",
      "https://borealis.example.com",
      "E925-448B-626D-D595-5A0F-FB24-B4D6-6983",
      null,
      "",
      {
        downloads: {
          linux: {
            path: "/api/agent/install/download/linux-amd64?site_id=7&artifact=engine-build&download_signature=signed-query",
          },
        },
      }
    );

    expect(command).toContain("/api/agent/install/download/linux-amd64");
    expect(command).toContain("curl -fsSL");
    expect(command).toContain("-o /tmp/Borealis-Agent");
    expect(command).toContain("chmod 700 /tmp/Borealis-Agent");
    expect(command).toContain("sudo /tmp/Borealis-Agent");
    expect(command).not.toContain("| sudo bash");
    expect(command).not.toContain("mktemp");
    expect(command).not.toContain("trap ");
    expect(command).not.toContain("sudo install");
    expect(command).not.toContain("wget");
    expect(command).toContain("--server-url");
    expect(command).toContain("https://borealis.example.com");
    expect(command).not.toContain("--repo-ref");
    expect(command).not.toContain("--release-channel");
    expect(command).toContain("--site-enrollment-code");
    expect(command).toContain("E925-448B-626D-D595-5A0F-FB24-B4D6-6983");
    expect(command).toContain("--install-service");
    expect(command).not.toContain("Agent.exe");
  });

  it("does not generate commands without a signed Engine download", () => {
    const command = buildInstallCommand(
      "linux",
      "https://borealis.example.com",
      "CODE-1234"
    );

    expect(command).toBe("");
  });

  it("uses Engine-hosted installer download URLs when Engine source is selected", () => {
    const command = buildInstallCommand(
      "linux",
      "https://borealis.example.com",
      "CODE-1234",
      null,
      "",
      {
        source: "engine",
        downloads: {
          linux: {
            path: "/api/agent/install/download/linux-amd64?site_id=7&artifact=stable-build&expires=2026-08-04T19%3A18%3A00Z&download_signature=signed-query",
          },
        },
      }
    );

    expect(command).toContain("https://borealis.example.com/api/agent/install/download/linux-amd64?site_id=7&artifact=stable-build&expires=2026-08-04T19%3A18%3A00Z&download_signature=signed-query");
    expect(command).not.toContain("--release-channel");
    expect(command).not.toContain("--repo-ref");
    expect(command).not.toContain("raw.githubusercontent.com");
  });

  it("summarizes active Windows and Linux install link counters", () => {
    const site = {
      agent_install_downloads: {
        windows: {
          url: "https://borealis.example.com/api/agent/install/download/windows-amd64?site_id=7&artifact=stable-build&expires=2100-01-01T00%3A00%3A00Z&download_signature=signed-query",
          active: true,
          expires_at: 4102444800,
          download_count: 3,
        },
        linux: {
          path: "/api/agent/install/download/linux-amd64?site_id=7&artifact=stable-build&expires=2100-01-01T00%3A00%3A00Z&download_signature=signed-query",
          active: true,
          expires_at: 4102448400,
          download_count: 2,
        },
      },
    };

    expect(siteInstallLinkForOS(site, "windows")?.download_count).toBe(3);
    const summary = siteInstallLinkSummary(site, { available: true, link_state_available: true }, 1700000000);
    expect(summary.activeCount).toBe(2);
    expect(summary.totalDownloads).toBe(5);
    expect(summary.nearestExpiry).toBe(4102444800);
    expect(summary.warning).toBe(false);
  });

  it("inserts an install-link drawer row after the expanded site", () => {
    const rows = [
      { id: 7, name: "Lab" },
      { id: 8, name: "Prod" },
    ];

    const expanded = insertExpandedInstallLinkRows(rows, 7);

    expect(expanded).toHaveLength(3);
    expect(expanded[0]).toBe(rows[0]);
    expect(expanded[1].__installLinkDetail).toBe(true);
    expect(expanded[1].site).toBe(rows[0]);
    expect(expanded[1].id).toBe("install-links:7");
    expect(expanded[2]).toBe(rows[1]);
  });

  it("builds nested install-link grid rows for Windows and Linux actions", () => {
    const site = {
      id: 7,
      name: "Lab",
      enrollment_code: "CODE-1234",
      agent_install_downloads: {
        windows: {
          url: "https://borealis.example.com/api/agent/install/download/windows-amd64?site_id=7&artifact=stable-build&expires=2100-01-01T00%3A00%3A00Z&download_signature=signed-query",
          platform: "windows-amd64",
          active: true,
          artifact_id: "stable-build",
          issued_at: 1700000000,
          expires_at: 4102444800,
          download_count: 3,
          last_downloaded_at: 1700000100,
        },
        linux: {
          path: "/api/agent/install/download/linux-amd64?site_id=7&artifact=stable-build&expires=2100-01-01T00%3A00%3A00Z&download_signature=signed-query",
          platform: "linux-amd64",
          active: true,
          artifact_id: "stable-build",
          issued_at: 1700000000,
          expires_at: 4102448400,
          download_count: 2,
          last_downloaded_at: 0,
        },
      },
    };

    const rows = siteInstallLinkGridRows(site, "https://borealis.example.com", 1700000000, { compiled_at: 1699990000 });

    expect(rows).toHaveLength(2);
    expect(rows[0].agentType).toBe("Install on Windows");
    expect(rows[0].platform).toBe("windows-amd64");
    expect(rows[0].artifact).toBe("stable-build");
    expect(rows[0].agentCompileDate).toBe(1699990000);
    expect(rows[0].downloadCount).toBe(3);
    expect(rows[0].url).toContain("/api/agent/install/download/windows-amd64?");
    expect(rows[1].agentType).toBe("Install on Linux");
    expect(rows[1].platform).toBe("linux-amd64");
    expect(rows[1].url).toBe("https://borealis.example.com/api/agent/install/download/linux-amd64?site_id=7&artifact=stable-build&expires=2100-01-01T00%3A00%3A00Z&download_signature=signed-query");
  });

  it("labels install-link rows by Engine artifact and site-worker state", () => {
    const activeLinkRow = {
      active: true,
      link: { artifact_id: "stable-build" },
    };
    const readySite = {
      site_worker_payload_ready: true,
      site_worker_guid: "worker-7",
      site_worker_status: "running",
    };
    const currentArtifact = {
      available: true,
      engine_cache_available: true,
      link_state_available: true,
      artifact_id: "stable-build",
      build_status: "ready",
    };

    expect(siteInstallLinkStatus(activeLinkRow, readySite, currentArtifact).label).toBe("Up-to-Date");
    expect(siteInstallLinkStatus(activeLinkRow, readySite, { ...currentArtifact, build_status: "compiling", engine_cache_available: false }).label).toBe("Compiling...");
    expect(siteInstallLinkStatus(activeLinkRow, readySite, { ...currentArtifact, artifact_id: "new-build" }).label).toBe("Out-of-Date");
    expect(siteInstallLinkStatus(activeLinkRow, { site_worker_payload_ready: true, site_worker_status: "" }, currentArtifact).label).toBe("Sending to Site Worker...");
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
      null,
      "",
      {
        downloads: {
          windows: {
            path: "/api/agent/install/download/windows-amd64?site_id=7&artifact=engine-build&download_signature=signed-query",
          },
        },
      }
    );

    expect(command).toContain("/api/agent/install/download/windows-amd64");
    expect(command).toContain('$borealisAgent = Join-Path $env:TEMP "Borealis-Agent.exe"');
    expect(command).toContain("-OutFile $borealisAgent");
    expect(command).toContain("& $borealisAgent");
    expect(command).toContain("Invoke-WebRequest -UseBasicParsing");
    expect(command).toContain('--server-url "https://borealis.example.com"');
    expect(command).not.toContain("--repo-ref");
    expect(command).not.toContain("--release-channel");
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
