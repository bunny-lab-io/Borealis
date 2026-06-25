import Prism from "prismjs";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-powershell";

export const OUTPUT_LIMIT = 40000;

const ANSI_OSC_PATTERN = /\u001B\][^\u0007]*(?:\u0007|\u001B\\)/g;
const ANSI_ST_PATTERN = /\u001B[P^_][\s\S]*?\u001B\\/g;
const ANSI_CSI_PATTERN = /\u001B\[[0-?]*[ -/]*[@-~]/g;
const ANSI_ESC_PATTERN = /\u001B[@-Z\\-_]/g;
const ANSI_C1_CSI_PATTERN = /\u009B[0-?]*[ -/]*[@-~]/g;

export function normalizeText(value) {
  if (value == null) return "";
  try {
    return String(value).trim();
  } catch {
    return "";
  }
}

export function clampOutput(value) {
  return value.length > OUTPUT_LIMIT ? value.slice(value.length - OUTPUT_LIMIT) : value;
}

export function inferShellKind(device) {
  const fingerprint = [
    device?.operating_system,
    device?.agent_operating_system,
    device?.os,
    device?.summary?.operating_system,
    device?.summary?.agent_operating_system,
    device?.summary?.os,
  ]
    .map(normalizeText)
    .join(" ")
    .toLowerCase();

  if (!fingerprint) return "powershell";
  if (fingerprint.includes("windows")) return "powershell";
  if (
    fingerprint.includes("linux") ||
    fingerprint.includes("ubuntu") ||
    fingerprint.includes("debian") ||
    fingerprint.includes("rocky") ||
    fingerprint.includes("centos") ||
    fingerprint.includes("red hat") ||
    fingerprint.includes("rhel") ||
    fingerprint.includes("fedora") ||
    fingerprint.includes("suse") ||
    fingerprint.includes("alpine")
  ) {
    return "bash";
  }
  return "powershell";
}

export function shellLanguageForKind(shellKind) {
  return shellKind === "bash" ? "bash" : "powershell";
}

export function cleanShellOutput(value) {
  if (value == null) return "";
  try {
    return String(value)
      .replace(/\r\n/g, "\n")
      .replace(/\r(?!\n)/g, "")
      .replace(ANSI_OSC_PATTERN, "")
      .replace(ANSI_ST_PATTERN, "")
      .replace(ANSI_CSI_PATTERN, "")
      .replace(ANSI_C1_CSI_PATTERN, "")
      .replace(ANSI_ESC_PATTERN, "")
      .replace(/[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]/g, "");
  } catch {
    return "";
  }
}

function escapeHtml(value) {
  return String(value || "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

export function highlightShellOutput(output, shellKind) {
  const raw = String(output || "");
  const language = shellLanguageForKind(shellKind);
  try {
    return Prism.highlight(raw, Prism.languages[language] || Prism.languages.markup, language);
  } catch {
    return escapeHtml(raw);
  }
}
