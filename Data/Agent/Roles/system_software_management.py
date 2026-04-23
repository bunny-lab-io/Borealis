from __future__ import annotations

import base64
import hashlib
import json
import os
import platform
import re
import shutil
import subprocess
import tempfile
from typing import Any, Dict, List, Tuple


_DISPLAY_ICON_QUOTED_RE = re.compile(r'^\s*"(?P<path>[^"]+)"\s*(?:,\s*(?P<index>-?\d+))?\s*$')
_DISPLAY_ICON_RESOURCE_RE = re.compile(
    r'^\s*(?P<path>.+?\.(?:exe|dll|ico|icl|cpl|ocx|scr))\s*(?:,\s*(?P<index>-?\d+))?\s*$',
    re.IGNORECASE,
)
_SOFTWARE_ICON_EXTRACTION_BATCH_SIZE = 40


def _clean_text(value: Any) -> str:
    if value is None:
        return ""
    try:
        return str(value).strip()
    except Exception:
        return ""


def _software_metadata(row: Any) -> Dict[str, Any]:
    if not isinstance(row, dict):
        return {}
    return row.get("metadata") if isinstance(row.get("metadata"), dict) else {}


def _parse_ps_json_output(text: str):
    txt = str(text or "")
    if txt.strip():
        try:
            return json.loads(txt)
        except Exception:
            try:
                start = txt.find("[{")
                if start == -1:
                    start = txt.find("{")
                end = txt.rfind("}")
                if start != -1 and end != -1 and end > start:
                    return json.loads(txt[start : end + 1])
            except Exception:
                pass
    return None


def _ps_json(cmd: str, timeout: int = 60):
    try:
        out = subprocess.run(["powershell", "-NoProfile", "-Command", cmd], capture_output=True, text=True, timeout=timeout)
        return _parse_ps_json_output(out.stdout or "")
    except Exception:
        return None


def _ps_json_file(script_text: str, timeout: int = 60):
    script_path = ""
    try:
        temp_dir = os.path.join(tempfile.gettempdir(), "Borealis", "software_management")
        os.makedirs(temp_dir, exist_ok=True)
        fd, script_path = tempfile.mkstemp(prefix="ps_json_", suffix=".ps1", dir=temp_dir, text=True)
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as fh:
            fh.write(str(script_text or ""))

        out = subprocess.run(
            ["powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script_path],
            capture_output=True,
            text=True,
            timeout=timeout,
        )
        return _parse_ps_json_output(out.stdout or "")
    except Exception:
        return None
    finally:
        try:
            if script_path and os.path.isfile(script_path):
                os.remove(script_path)
        except Exception:
            pass


def _software_row_key(row: dict) -> str:
    try:
        return "::".join(
            [
                str(row.get("name") or "").strip().lower(),
                str(row.get("version") or "").strip().lower(),
                str(row.get("source") or "").strip().lower(),
            ]
        )
    except Exception:
        return ""


def _normalize_display_icon_hint(value: Any) -> str:
    try:
        return str(value or "").strip()
    except Exception:
        return ""


normalize_display_icon_hint = _normalize_display_icon_hint


def _coerce_bool(value: Any) -> bool:
    if isinstance(value, bool):
        return value
    return _clean_text(value).lower() in {"1", "true", "yes", "on"}


def _parse_display_icon_resource(value: Any) -> dict | None:
    text = _normalize_display_icon_hint(value)
    if not text:
        return None
    match = _DISPLAY_ICON_QUOTED_RE.match(text)
    if match:
        file_path = str(match.group("path") or "").strip()
        if file_path:
            try:
                index = int(str(match.group("index") or "0").strip())
            except Exception:
                index = 0
            return {"hint": text, "file_path": file_path, "icon_index": index}
    match = _DISPLAY_ICON_RESOURCE_RE.match(text)
    if match:
        file_path = str(match.group("path") or "").strip().rstrip(",")
        if file_path:
            try:
                index = int(str(match.group("index") or "0").strip())
            except Exception:
                index = 0
            return {"hint": text, "file_path": file_path, "icon_index": index}
    return None


def _software_icon_signature(rows: Any) -> str:
    entries = []
    for row in rows or []:
        if not isinstance(row, dict):
            continue
        metadata = row.get("metadata") if isinstance(row.get("metadata"), dict) else {}
        entries.append(
            {
                "key": _software_row_key(row),
                "display_icon": _normalize_display_icon_hint(metadata.get("display_icon")),
                "display_icon_override_cleared": _coerce_bool(metadata.get("display_icon_override_cleared")),
            }
        )
    try:
        encoded = json.dumps(entries, sort_keys=True, separators=(",", ":")).encode("utf-8")
    except Exception:
        encoded = repr(entries).encode("utf-8", errors="ignore")
    return hashlib.sha256(encoded).hexdigest()


software_icon_signature = _software_icon_signature


def _extract_windows_icon_payload_batch(specs: Any) -> dict:
    if not specs:
        return {}
    try:
        specs_json = json.dumps(specs)
    except Exception:
        return {}
    ps = r"""
$specs = @'
""" + specs_json + r"""
'@ | ConvertFrom-Json
Add-Type -AssemblyName System.Drawing
Add-Type @"
using System;
using System.Runtime.InteropServices;

public static class BorealisIconInterop {
  [DllImport("shell32.dll", CharSet = CharSet.Unicode)]
  public static extern uint ExtractIconEx(
    string szFileName,
    int nIconIndex,
    IntPtr[] phiconLarge,
    IntPtr[] phiconSmall,
    uint nIcons
  );

  [DllImport("user32.dll", SetLastError = true)]
  public static extern bool DestroyIcon(IntPtr hIcon);
}
"@
function Get-BorealisIconPayload {
  param(
    [string]$FileName,
    [int]$IconIndex
  )

  try {
    $expanded = [Environment]::ExpandEnvironmentVariables([string]$FileName)
    if (-not $expanded) {
      return $null
    }
    $expanded = $expanded.Trim().Trim('"')
    if (-not $expanded -or -not (Test-Path -LiteralPath $expanded)) {
      return $null
    }

    $extension = [System.IO.Path]::GetExtension($expanded).ToLowerInvariant()
    if ($extension -eq '.ico') {
      $rawBytes = [System.IO.File]::ReadAllBytes($expanded)
      if ($rawBytes -and $rawBytes.Length -gt 0) {
        return [PSCustomObject]@{
          mime_type = 'image/vnd.microsoft.icon'
          data_base64 = [Convert]::ToBase64String($rawBytes)
        }
      }
    }

    $icon = $null
    $ownedIcon = $null
    $largeIcons = $null
    $smallIcons = $null
    $iconHandle = [IntPtr]::Zero
    try {
      $largeIcons = New-Object IntPtr[] 1
      $smallIcons = New-Object IntPtr[] 1
      $extractedCount = [BorealisIconInterop]::ExtractIconEx($expanded, [int]$IconIndex, $largeIcons, $smallIcons, 1)
      if ($extractedCount -gt 0) {
        if ($largeIcons[0] -ne [IntPtr]::Zero) {
          $iconHandle = $largeIcons[0]
        } elseif ($smallIcons[0] -ne [IntPtr]::Zero) {
          $iconHandle = $smallIcons[0]
        }
      }
      if ($iconHandle -ne [IntPtr]::Zero) {
        $icon = [System.Drawing.Icon]::FromHandle($iconHandle)
        if ($icon -ne $null) {
          $ownedIcon = $icon.Clone()
          $icon.Dispose()
          $icon = $null
        }
      }
    } catch {
      $icon = $null
      $ownedIcon = $null
    }
    if ($null -eq $ownedIcon) {
      try {
        $ownedIcon = [System.Drawing.Icon]::ExtractAssociatedIcon($expanded)
      } catch {
        $ownedIcon = $null
      }
      if ($null -eq $ownedIcon) {
        return $null
      }
    }

    $bitmap = $null
    $stream = $null
    try {
      $bitmap = $ownedIcon.ToBitmap()
      if ($null -eq $bitmap) {
        return $null
      }
      $stream = New-Object System.IO.MemoryStream
      $bitmap.Save($stream, [System.Drawing.Imaging.ImageFormat]::Png)
      $pngBytes = $stream.ToArray()
      if (-not $pngBytes -or $pngBytes.Length -le 0) {
        return $null
      }
      return [PSCustomObject]@{
        mime_type = 'image/png'
        data_base64 = [Convert]::ToBase64String($pngBytes)
      }
    } finally {
      if ($stream) { $stream.Dispose() }
      if ($bitmap) { $bitmap.Dispose() }
      if ($ownedIcon) { $ownedIcon.Dispose() }
      if ($largeIcons -and $largeIcons.Length -gt 0 -and $largeIcons[0] -ne [IntPtr]::Zero) {
        [BorealisIconInterop]::DestroyIcon($largeIcons[0]) | Out-Null
      }
      if ($smallIcons -and $smallIcons.Length -gt 0 -and $smallIcons[0] -ne [IntPtr]::Zero) {
        [BorealisIconInterop]::DestroyIcon($smallIcons[0]) | Out-Null
      }
    }
  } catch {
    return $null
  }
}
$results = @()
foreach ($spec in ($specs | Where-Object { $_ -and $_.hint -and $_.path })) {
  try {
    $payload = Get-BorealisIconPayload -FileName ([string]$spec.path) -IconIndex ([int]$spec.index)
    if ($payload -and $payload.data_base64) {
      $results += [PSCustomObject]@{
        hint = [string]$spec.hint
        mime_type = [string]$payload.mime_type
        data_base64 = [string]$payload.data_base64
      }
    }
  } catch {}
}
$results | ConvertTo-Json -Depth 4 -Compress
"""
    timeout = max(120, min(300, 30 + (len(specs) * 2)))
    data = _ps_json_file(ps, timeout=timeout)
    if isinstance(data, dict):
        data = [data]
    payloads = {}
    for item in (data or []):
        if not isinstance(item, dict):
            continue
        hint = _normalize_display_icon_hint(item.get("hint"))
        mime_type = str(item.get("mime_type") or "").strip().lower() or "image/png"
        data_base64 = str(item.get("data_base64") or "").strip()
        if hint and data_base64:
            payloads[hint] = {
                "mime_type": mime_type,
                "data_base64": data_base64,
            }
    return payloads


def _extract_windows_icon_payloads_by_hint(hints: Any) -> dict:
    if platform.system().lower() != "windows":
        return {}
    specs = []
    seen_hints = set()
    for hint in (hints or []):
        parsed = _parse_display_icon_resource(hint)
        if not parsed:
            continue
        normalized_hint = str(parsed.get("hint") or "").strip()
        if not normalized_hint or normalized_hint in seen_hints:
            continue
        seen_hints.add(normalized_hint)
        specs.append(
            {
                "hint": normalized_hint,
                "path": parsed["file_path"],
                "index": int(parsed.get("icon_index") or 0),
            }
        )
    if not specs:
        return {}

    payloads = {}
    batch_size = max(1, int(_SOFTWARE_ICON_EXTRACTION_BATCH_SIZE or 1))
    for start in range(0, len(specs), batch_size):
        batch = specs[start : start + batch_size]
        try:
            payloads.update(_extract_windows_icon_payload_batch(batch))
        except Exception:
            continue
    return payloads


def _software_matches_icon_override(row: Dict[str, Any], rule: Dict[str, Any]) -> bool:
    name = _clean_text(row.get("name"))

    expected_name = _clean_text(rule.get("name"))
    if not expected_name:
        return False
    if expected_name.lower() != name.lower():
        return False

    return True


def apply_software_icon_overrides(rows: Any, overrides: Any) -> List[Dict[str, Any]]:
    normalized_rows = rows if isinstance(rows, list) else []
    normalized_overrides = overrides if isinstance(overrides, list) else []
    for row in normalized_rows:
        if not isinstance(row, dict):
            continue
        metadata = row.get("metadata") if isinstance(row.get("metadata"), dict) else {}
        row["metadata"] = metadata
        for rule in normalized_overrides:
            if not isinstance(rule, dict):
                continue
            if not _software_matches_icon_override(row, rule):
                continue
            clear_icon = _coerce_bool(rule.get("clear_icon") or rule.get("remove_icon"))
            display_icon = normalize_display_icon_hint(rule.get("display_icon") or rule.get("icon_location"))
            if not clear_icon and not display_icon:
                continue
            original_display_icon = _clean_text(metadata.get("display_icon"))
            if original_display_icon and (clear_icon or original_display_icon != display_icon) and not metadata.get("original_display_icon"):
                metadata["original_display_icon"] = original_display_icon
            metadata["display_icon"] = "" if clear_icon else display_icon
            metadata["display_icon_override"] = "" if clear_icon else display_icon
            metadata["display_icon_override_rule_id"] = _clean_text(rule.get("rule_id"))
            metadata["display_icon_override_cleared"] = bool(clear_icon)
            if clear_icon:
                metadata.pop("icon_hash", None)
            break
    return normalized_rows


def attach_windows_software_icons(
    rows: Any,
    *,
    previous_icon_hash_by_key: Any = None,
) -> Tuple[List[Dict[str, Any]], Dict[str, str]]:
    if platform.system().lower() != "windows":
        return [], {}
    icon_hash_by_key = {
        str(key): str(value).strip().lower()
        for key, value in ((previous_icon_hash_by_key or {}) or {}).items()
        if str(key).strip() and str(value or "").strip()
    }
    pending_by_hint = {}
    for row in (rows or []):
        if not isinstance(row, dict):
            continue
        source = str(row.get("source") or "").strip().lower()
        if source != "local_installed":
            continue
        metadata = row.get("metadata") if isinstance(row.get("metadata"), dict) else {}
        row_key = _software_row_key(row)
        if not row_key:
            continue
        if _coerce_bool(metadata.get("display_icon_override_cleared")):
            metadata.pop("icon_hash", None)
            row["metadata"] = metadata
            icon_hash_by_key.pop(row_key, None)
            continue
        cached_icon_hash = icon_hash_by_key.get(row_key)
        if cached_icon_hash:
            metadata["icon_hash"] = cached_icon_hash
            row["metadata"] = metadata
            continue
        hint = _normalize_display_icon_hint(metadata.get("display_icon"))
        if not hint:
            continue
        pending_by_hint.setdefault(hint, []).append((row, metadata, row_key))

    extracted_by_hint = _extract_windows_icon_payloads_by_hint(list(pending_by_hint.keys()))
    icon_payloads_by_hash = {}
    for hint, matches in pending_by_hint.items():
        payload = extracted_by_hint.get(hint)
        if not payload:
            continue
        try:
            icon_bytes = base64.b64decode(str(payload.get("data_base64") or "").strip(), validate=True)
        except Exception:
            continue
        if not icon_bytes:
            continue
        icon_hash = hashlib.sha256(icon_bytes).hexdigest()
        icon_payloads_by_hash.setdefault(
            icon_hash,
            {
                "icon_hash": icon_hash,
                "mime_type": str(payload.get("mime_type") or "image/png").strip().lower() or "image/png",
                "data_base64": base64.b64encode(icon_bytes).decode("ascii"),
            },
        )
        for row, metadata, row_key in matches:
            metadata["icon_hash"] = icon_hash
            row["metadata"] = metadata
            icon_hash_by_key[row_key] = icon_hash

    return list(icon_payloads_by_hash.values()), icon_hash_by_key


def collect_software() -> List[Dict[str, Any]]:
    plat = platform.system().lower()

    def _dedupe_and_sort(rows):
        deduped = {}
        for row in rows or []:
            if not isinstance(row, dict):
                continue
            name = str(row.get("name") or "").strip()
            if not name:
                continue
            version = str(row.get("version") or "").strip()
            source = str(row.get("source") or "local_installed").strip().lower() or "local_installed"
            payload = {
                "name": name,
                "version": version,
                "source": source,
            }
            metadata = row.get("metadata") if isinstance(row.get("metadata"), dict) else {}
            if metadata:
                payload["metadata"] = metadata
            deduped[(name.lower(), version.lower(), source)] = payload
        return sorted(
            deduped.values(),
            key=lambda item: (
                str(item.get("name") or "").lower(),
                str(item.get("source") or "").lower(),
                str(item.get("version") or "").lower(),
            ),
        )

    def _coerce_estimated_size_kb(raw_value):
        if raw_value in (None, "") or isinstance(raw_value, bool):
            return None
        try:
            size_kb = int(str(raw_value).strip().replace(",", ""))
        except Exception:
            return None
        return size_kb if size_kb > 0 else None

    if plat == "linux":
        packages = []
        try:
            if shutil.which("dpkg-query"):
                out = subprocess.run(
                    ["dpkg-query", "-W", "-f=${Package}\t${Version}\n"],
                    capture_output=True,
                    text=True,
                    timeout=120,
                )
                for line in (out.stdout or "").splitlines():
                    parts = line.split("\t", 1)
                    name = (parts[0] if parts else "").strip()
                    version = (parts[1] if len(parts) > 1 else "").strip()
                    if name:
                        packages.append({"name": name, "version": version, "source": "dpkg"})
            elif shutil.which("rpm"):
                out = subprocess.run(
                    ["rpm", "-qa", "--qf", "%{NAME}\t%{VERSION}-%{RELEASE}\n"],
                    capture_output=True,
                    text=True,
                    timeout=120,
                )
                for line in (out.stdout or "").splitlines():
                    parts = line.split("\t", 1)
                    name = (parts[0] if parts else "").strip()
                    version = (parts[1] if len(parts) > 1 else "").strip()
                    if name:
                        packages.append({"name": name, "version": version, "source": "rpm"})
        except Exception:
            packages = []

        return _dedupe_and_sort(packages)

    if plat != "windows":
        return []

    try:
        ps = r"""
$paths = @(
  'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*',
  'HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*',
  'HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*'
)
$list = @()
foreach ($p in $paths) {
  try {
    $list += Get-ItemProperty -Path $p -ErrorAction SilentlyContinue |
      Select-Object DisplayName, DisplayVersion, Publisher, InstallLocation, InstallDate, EstimatedSize, DisplayIcon, UninstallString, QuietUninstallString, WindowsInstaller, PSChildName
  } catch {}
}
$list = $list | Where-Object { $_.DisplayName -and ("$($_.DisplayName)".Trim().Length -gt 0) }
$list | Sort-Object DisplayName -Unique | ConvertTo-Json -Depth 2
"""
        data = _ps_json(ps, timeout=120)
        out = []
        if isinstance(data, dict):
            data = [data]
        for it in (data or []):
            name = str(it.get("DisplayName") or "").strip()
            if not name:
                continue
            ver = str(it.get("DisplayVersion") or "").strip()
            metadata = {}
            publisher = str(it.get("Publisher") or "").strip()
            if publisher:
                metadata["publisher"] = publisher
            install_location = str(it.get("InstallLocation") or "").strip()
            if install_location:
                metadata["install_location"] = install_location
            install_date = str(it.get("InstallDate") or "").strip()
            if install_date:
                metadata["install_date"] = install_date
            estimated_size_kb = _coerce_estimated_size_kb(it.get("EstimatedSize"))
            if estimated_size_kb is not None:
                metadata["estimated_size_kb"] = estimated_size_kb
            display_icon = str(it.get("DisplayIcon") or "").strip()
            if display_icon:
                metadata["display_icon"] = display_icon
            uninstall_string = str(it.get("UninstallString") or "").strip()
            if uninstall_string:
                metadata["uninstall_string"] = uninstall_string
            quiet_uninstall_string = str(it.get("QuietUninstallString") or "").strip()
            if quiet_uninstall_string:
                metadata["quiet_uninstall_string"] = quiet_uninstall_string
            product_code = str(it.get("PSChildName") or "").strip()
            if product_code:
                metadata["product_code"] = product_code
            windows_installer = it.get("WindowsInstaller")
            if windows_installer not in (None, ""):
                metadata["windows_installer"] = bool(windows_installer)
            payload = {"name": name, "version": ver, "source": "local_installed"}
            if metadata:
                payload["metadata"] = metadata
            out.append(payload)
        try:
            appx_ps = r"""
$list = @()
try {
  $list = Get-AppxPackage -AllUsers -ErrorAction Stop |
    Where-Object { -not $_.IsFramework -and -not $_.IsResourcePackage } |
    Select-Object Name, Version, Publisher, InstallLocation, PackageFamilyName, NonRemovable
} catch {
  try {
    $list = Get-AppxPackage -ErrorAction SilentlyContinue |
      Where-Object { -not $_.IsFramework -and -not $_.IsResourcePackage } |
      Select-Object Name, Version, Publisher, InstallLocation, PackageFamilyName, NonRemovable
  } catch {}
}
$list | Sort-Object Name -Unique | ConvertTo-Json -Depth 3
"""
            appx_data = _ps_json(appx_ps, timeout=120)
            if isinstance(appx_data, dict):
                appx_data = [appx_data]
            for it in (appx_data or []):
                name = str(it.get("Name") or "").strip()
                if not name:
                    continue
                ver = str(it.get("Version") or "").strip()
                metadata = {}
                publisher = str(it.get("Publisher") or "").strip()
                if publisher:
                    metadata["publisher"] = publisher
                install_location = str(it.get("InstallLocation") or "").strip()
                if install_location:
                    metadata["install_location"] = install_location
                family_name = str(it.get("PackageFamilyName") or "").strip()
                if family_name:
                    metadata["package_family_name"] = family_name
                non_removable = it.get("NonRemovable")
                if non_removable not in (None, ""):
                    metadata["non_removable"] = bool(non_removable)
                payload = {"name": name, "version": ver, "source": "windows_store"}
                if metadata:
                    payload["metadata"] = metadata
                out.append(payload)
        except Exception:
            pass
        if out:
            return _dedupe_and_sort(out)
    except Exception:
        pass

    try:
        try:
            import winreg  # type: ignore
        except Exception:
            return []

        def _enum_uninstall(root, path, wow_flag=0):
            items = []
            try:
                key = winreg.OpenKey(root, path, 0, winreg.KEY_READ | wow_flag)
            except Exception:
                return items
            try:
                i = 0
                while True:
                    try:
                        sub = winreg.EnumKey(key, i)
                    except OSError:
                        break
                    i += 1
                    try:
                        sk = winreg.OpenKey(key, sub, 0, winreg.KEY_READ | wow_flag)
                        try:
                            name, _ = winreg.QueryValueEx(sk, "DisplayName")
                        except Exception:
                            name = ""
                        if name and str(name).strip():
                            try:
                                ver, _ = winreg.QueryValueEx(sk, "DisplayVersion")
                            except Exception:
                                ver = ""
                            metadata = {}
                            try:
                                publisher, _ = winreg.QueryValueEx(sk, "Publisher")
                                if publisher:
                                    metadata["publisher"] = str(publisher).strip()
                            except Exception:
                                pass
                            try:
                                install_location, _ = winreg.QueryValueEx(sk, "InstallLocation")
                                if install_location:
                                    metadata["install_location"] = str(install_location).strip()
                            except Exception:
                                pass
                            try:
                                install_date, _ = winreg.QueryValueEx(sk, "InstallDate")
                                if install_date:
                                    metadata["install_date"] = str(install_date).strip()
                            except Exception:
                                pass
                            try:
                                estimated_size_kb, _ = winreg.QueryValueEx(sk, "EstimatedSize")
                                normalized_estimated_size_kb = _coerce_estimated_size_kb(estimated_size_kb)
                                if normalized_estimated_size_kb is not None:
                                    metadata["estimated_size_kb"] = normalized_estimated_size_kb
                            except Exception:
                                pass
                            try:
                                display_icon, _ = winreg.QueryValueEx(sk, "DisplayIcon")
                                if display_icon:
                                    metadata["display_icon"] = str(display_icon).strip()
                            except Exception:
                                pass
                            try:
                                uninstall_string, _ = winreg.QueryValueEx(sk, "UninstallString")
                                if uninstall_string:
                                    metadata["uninstall_string"] = str(uninstall_string).strip()
                            except Exception:
                                pass
                            try:
                                quiet_uninstall_string, _ = winreg.QueryValueEx(sk, "QuietUninstallString")
                                if quiet_uninstall_string:
                                    metadata["quiet_uninstall_string"] = str(quiet_uninstall_string).strip()
                            except Exception:
                                pass
                            if sub:
                                metadata["product_code"] = str(sub).strip()
                            try:
                                windows_installer, _ = winreg.QueryValueEx(sk, "WindowsInstaller")
                                metadata["windows_installer"] = bool(windows_installer)
                            except Exception:
                                pass
                            payload = {
                                "name": str(name).strip(),
                                "version": str(ver or "").strip(),
                                "source": "local_installed",
                            }
                            if metadata:
                                payload["metadata"] = metadata
                            items.append(payload)
                    except Exception:
                        continue
            except Exception:
                pass
            return items

        HKLM = getattr(winreg, "HKEY_LOCAL_MACHINE")
        HKCU = getattr(winreg, "HKEY_CURRENT_USER")
        WOW64_64 = getattr(winreg, "KEY_WOW64_64KEY", 0)
        WOW64_32 = getattr(winreg, "KEY_WOW64_32KEY", 0)
        paths = [
            (HKLM, r"SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall", WOW64_64),
            (HKLM, r"SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall", WOW64_32),
            (HKLM, r"SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall", 0),
            (HKCU, r"SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall", 0),
        ]
        merged = {}
        for root, path, flag in paths:
            for it in _enum_uninstall(root, path, flag):
                name_key = (it.get("name") or "").lower()
                if not name_key:
                    continue
                key = (name_key, (it.get("version") or "").lower(), (it.get("source") or "local_installed"))
                if key not in merged:
                    merged[key] = it
        return _dedupe_and_sort(list(merged.values()))
    except Exception:
        return []


def build_software_inventory_snapshot(
    *,
    previous_icon_hash_by_key: Dict[str, str] | None = None,
    previous_signature: str = "",
    icon_overrides: List[Dict[str, Any]] | None = None,
) -> Dict[str, Any]:
    rows = collect_software()
    if icon_overrides:
        rows = apply_software_icon_overrides(rows, icon_overrides)
    signature = software_icon_signature(rows)
    icon_payloads, icon_hash_by_key = attach_windows_software_icons(
        rows,
        previous_icon_hash_by_key=previous_icon_hash_by_key if signature == previous_signature else {},
    )
    icon_candidate_count = 0
    for row in rows:
        if not isinstance(row, dict):
            continue
        if str(row.get("source") or "").strip().lower() != "local_installed":
            continue
        metadata = row.get("metadata") if isinstance(row.get("metadata"), dict) else {}
        if normalize_display_icon_hint(metadata.get("display_icon")):
            icon_candidate_count += 1
    return {
        "software": rows,
        "software_icon_payloads": icon_payloads,
        "software_icon_hash_by_key": icon_hash_by_key,
        "software_icon_signature": signature,
        "software_icon_candidate_count": icon_candidate_count,
    }
