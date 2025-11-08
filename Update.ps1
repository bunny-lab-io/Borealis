[CmdletBinding()]
param(
    [switch]$Trace
)

$scriptDir = Split-Path $MyInvocation.MyCommand.Path -Parent
$script:BorealisTlsInitialized = $false
$script:BorealisTrustedThumbprints = @()
$script:BorealisCallbackApplied = $false
$script:AgentPythonHttpHelper = ''
$script:UpdateDebugEnabled = $Trace.IsPresent
$symbols = @{
    Success = [char]0x2705
    Running = [char]0x23F3
    Fail    = [char]0x274C
    Info    = [char]0x2139
}

function Write-UpdateLog {
    param(
        [string]$Message,
        [string]$Level = 'INFO',
        [string]$Color
    )

    if (-not $Message) { return }

    $timestamp = (Get-Date).ToString('yyyy-MM-dd HH:mm:ss')
    $normalized = if ($Level) { $Level } else { 'INFO' }
    $normalized = $normalized.ToUpperInvariant()

    if ($normalized -eq 'DEBUG' -and -not $script:UpdateDebugEnabled) {
        return
    }

    if (-not $Color) {
        switch ($normalized) {
            'WARN' { $Color = 'Yellow' }
            'ERROR' { $Color = 'Red' }
            'STEP' { $Color = 'Cyan' }
            'SUCCESS' { $Color = 'Green' }
            default { $Color = $null }
        }
    }

    $line = "[{0}] [{1}] {2}" -f $timestamp, $normalized, $Message
    if ($Color) {
        Write-Host $line -ForegroundColor $Color
    } else {
        Write-Host $line
    }
}

$repositoryUrl = 'https://github.com/bunny-lab-io/Borealis.git'

function Write-ProgressStep {
    param (
        [string]$Message,
        [string]$Status = $symbols["Info"]
    )
    Write-Host "`r$Status $Message... " -NoNewline
}

function Run-Step {
    param (
        [string]     $Message,
        [scriptblock]$Script
    )
    Write-ProgressStep -Message $Message -Status "$($symbols.Running)"
    try {
        & $Script
        if ($LASTEXITCODE -eq 0 -or $?) {
            Write-Host "`r$($symbols.Success) $Message                        "
        } else {
            throw "Non-zero exit code"
        }
    } catch {
        Write-Host "`r$($symbols.Fail) $Message - Failed: $_                        " -ForegroundColor Red
        throw
    }
}

function Get-GitExecutablePath {
    param(
        [string]$ProjectRoot
    )

    $candidates = @()
    if ($ProjectRoot) {
        $candidates += (Join-Path $ProjectRoot 'Dependencies\git\cmd\git.exe')
        $candidates += (Join-Path $ProjectRoot 'Dependencies\git\bin\git.exe')
    }

    foreach ($candidate in ($candidates | Select-Object -Unique)) {
        try {
            if (Test-Path $candidate -PathType Leaf) { return $candidate }
        } catch {}
    }

    return ''
}

function Invoke-GitCommand {
    param(
        [Parameter(Mandatory = $true)]
        [string]$GitExe,

        [Parameter(Mandatory = $true)]
        [string]$WorkingDirectory,

        [Parameter(Mandatory = $true)]
        [string[]]$Arguments
    )

    if ([string]::IsNullOrWhiteSpace($GitExe) -or -not (Test-Path $GitExe -PathType Leaf)) {
        throw "Git executable not found at '$GitExe'"
    }

    if (-not (Test-Path $WorkingDirectory -PathType Container)) {
        throw "Working directory '$WorkingDirectory' does not exist."
    }

    $fullArgs = @('-C', $WorkingDirectory) + $Arguments
    $output = & $GitExe @fullArgs 2>&1
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) {
        $joined = ($Arguments -join ' ')
        $message = "git $joined failed with exit code $exitCode."
        if ($output) {
            $message = "$message Output: $output"
        }
        throw $message
    }

    return $output
}

function Stop-AgentScheduledTasks {
    param(
        [string[]]$TaskNames
    )

    $stopped = @()
    foreach ($name in $TaskNames) {
        $taskExists = $false
        try {
            $null = Get-ScheduledTask -TaskName $name -ErrorAction Stop
            $taskExists = $true
        } catch {
            try {
                schtasks.exe /Query /TN "$name" 2>$null | Out-Null
                if ($LASTEXITCODE -eq 0) { $taskExists = $true }
            } catch {}
        }

        if (-not $taskExists) { continue }

        Write-Host "Stopping scheduled task: $name" -ForegroundColor Yellow
        $stopped += $name
        try { Stop-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue } catch {}
        try { schtasks.exe /End /TN "$name" /F 2>$null | Out-Null } catch {}
        try {
            for ($i = 0; $i -lt 20; $i++) {
                $info = Get-ScheduledTaskInfo -TaskName $name -ErrorAction Stop
                if ($info.State -ne 'Running' -and $info.State -ne 'Queued') { break }
                Start-Sleep -Milliseconds 500
            }
        } catch {}
    }

    return ,$stopped
}

function Start-AgentScheduledTasks {
    param(
        [string[]]$TaskNames
    )

    foreach ($name in $TaskNames) {
        Write-Host "Restarting scheduled task: $name" -ForegroundColor Green
        try {
            Start-ScheduledTask -TaskName $name -ErrorAction Stop | Out-Null
            continue
        } catch {}

        try { schtasks.exe /Run /TN "$name" 2>$null | Out-Null } catch {}
    }
}

function Stop-AgentPythonProcesses {
    param(
        [string[]]$ProcessNames = @('python', 'pythonw')
    )

    foreach ($name in ($ProcessNames | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | Select-Object -Unique)) {
        $name = $name.Trim()
        if (-not $name) { continue }

        $processes = @()
        try {
            $processes = Get-Process -Name $name -ErrorAction Stop
        } catch {
            $processes = @()
        }

        foreach ($proc in $processes) {
            $procId = $null
            $procName = $null
            try {
                $procId = $proc.Id
                $procName = $proc.ProcessName
            } catch {}

            if ($procId -eq $null) { continue }

            if (-not $procName) { $procName = $name }

            $stopped = $false
            Write-Host "Stopping process: $procName (PID $procId)" -ForegroundColor Yellow

            try {
                Stop-Process -Id $procId -Force -ErrorAction Stop
                $stopped = $true
            } catch {
                Write-Host "Unable to stop process via Stop-Process: $procName (PID $procId). $_" -ForegroundColor DarkYellow
            }

            if (-not $stopped) {
                try {
                    $taskkillOutput = taskkill.exe /PID $procId /F 2>&1
                    if ($LASTEXITCODE -eq 0) {
                        $stopped = $true
                    } else {
                        if ($taskkillOutput) {
                            Write-Host "taskkill.exe returned exit code ${LASTEXITCODE} for PID ${procId}: $taskkillOutput" -ForegroundColor DarkYellow
                        }
                    }
                } catch {
                    Write-Host "Unable to stop process via taskkill.exe: $procName (PID $procId). $_" -ForegroundColor DarkYellow
                }
            }

            if (-not $stopped) {
                Write-Host "Process still running after termination attempts: $procName (PID $procId)" -ForegroundColor DarkYellow
            }
        }
    }
}

function Get-BorealisServerUrl {
    param(
        [string]$AgentRoot
    )

    $serverBaseUrl = $env:BOREALIS_SERVER_URL
    if (-not $serverBaseUrl) {
        try {
            if (-not $AgentRoot) { $AgentRoot = $scriptDir }
            $settingsDir = Join-Path $AgentRoot 'Settings'
            $serverUrlFile = Join-Path $settingsDir 'server_url.txt'
            if (Test-Path $serverUrlFile -PathType Leaf) {
                $content = Get-Content -Path $serverUrlFile -Raw -ErrorAction Stop
                if ($content) { $serverBaseUrl = $content.Trim() }
            }
        } catch {}
    }

    if (-not $serverBaseUrl) { $serverBaseUrl = 'https://localhost:5000' }

    $resolved = Resolve-BorealisServerUrl -Url $serverBaseUrl
    if ([string]::IsNullOrWhiteSpace($resolved)) {
        return 'https://localhost:5000'
    }

    Write-UpdateLog ("Resolved Borealis server URL: {0}" -f $resolved) 'INFO'
    return $resolved
}

function Resolve-BorealisServerUrl {
    param(
        [string]$Url
    )

    if ([string]::IsNullOrWhiteSpace($Url)) {
        return ''
    }

    $candidate = $Url.Trim()
    if ($candidate -notmatch '^[A-Za-z][A-Za-z0-9+.-]*://') {
        $candidate = "https://$candidate"
    }

    try {
        $builder = New-Object System.UriBuilder($candidate)
    } catch {
        return $candidate.TrimEnd('/')
    }

    $allowPlaintext = $false
    try {
        $allowValue = $env:BOREALIS_ALLOW_PLAINTEXT
        if ($allowValue) {
            $normalizedAllow = $allowValue.ToString().Trim().ToLowerInvariant()
            if ($normalizedAllow -and @('1','true','yes','on') -contains $normalizedAllow) {
                $allowPlaintext = $true
            }
        }
    } catch {}

    if ($builder.Scheme -eq 'http' -and -not $allowPlaintext) {
        $hostName = $builder.Host.ToLowerInvariant()
        if ($hostName -in @('localhost','127.0.0.1','::1')) {
            $builder.Scheme = 'https'
            if ($builder.Port -eq -1 -or $builder.Port -eq 80) {
                $builder.Port = 5000
            }
        }
    }

    return $builder.Uri.AbsoluteUri.TrimEnd('/')
}

function Get-AgentCertificateCachePath {
    param(
        [string]$AgentRoot
    )

    $settingsDir = Get-AgentSettingsDirectory -AgentRoot $AgentRoot
    if (-not $settingsDir) { return '' }
    return (Join-Path $settingsDir 'server_certificate.crt')
}

function Get-ExistingServerCertificatePath {
    param(
        [string]$AgentRoot
    )

    $path = Get-AgentCertificateCachePath -AgentRoot $AgentRoot
    if ($path -and (Test-Path $path -PathType Leaf)) {
        return $path
    }
    return ''
}

function Save-ServerCertificateCache {
    param(
        [string]$AgentRoot,
        [string]$CertificatePem
    )

    if ([string]::IsNullOrWhiteSpace($CertificatePem)) {
        return ''
    }

    $targetPath = Get-AgentCertificateCachePath -AgentRoot $AgentRoot
    if (-not $targetPath) {
        return ''
    }

    $targetDir = Split-Path -Path $targetPath -Parent
    try {
        if (-not (Test-Path $targetDir -PathType Container)) {
            New-Item -ItemType Directory -Force -Path $targetDir | Out-Null
        }
    } catch {
        Write-UpdateLog ("Failed to create certificate cache directory {0}: {1}" -f $targetDir, $_.Exception.Message) 'WARN'
        return ''
    }

    try {
        Set-Content -Path $targetPath -Value $CertificatePem -Encoding UTF8
        Write-UpdateLog ("Saved Borealis Engine certificate to {0}" -f $targetPath) 'INFO'
        return $targetPath
    } catch {
        Write-UpdateLog ("Failed to cache server certificate: {0}" -f $_.Exception.Message) 'WARN'
        return ''
    }
}

function Get-CertificatesFromPem {
    param(
        [string]$Path
    )

    if (-not $Path -or -not (Test-Path $Path -PathType Leaf)) {
        return @()
    }

    $lines = @()
    try {
        $lines = Get-Content -Path $Path -ErrorAction Stop
    } catch {
        return @()
    }

    if (-not $lines -or $lines.Count -eq 0) {
        Write-Verbose ("PEM content at {0} is empty." -f $Path)
        return @()
    }

    $collecting = $false
    $buffer = ''
    $certificates = @()

    foreach ($line in $lines) {
        $lineValue = if ($null -ne $line) { $line } else { '' }
        $trimmed = $lineValue.ToString().Trim()
        if ($trimmed -eq '-----BEGIN CERTIFICATE-----') {
            Write-Verbose ("Detected certificate begin marker in {0}." -f $Path)
            $collecting = $true
            $buffer = ''
            continue
        }
        if ($trimmed -eq '-----END CERTIFICATE-----') {
            Write-Verbose ("Detected certificate end marker in {0}; buffer length = {1} characters." -f $Path, $buffer.Length)
            if ($collecting -and $buffer) {
                try {
                    $raw = [Convert]::FromBase64String($buffer)
                    $cert = $null
                    try { $cert = [System.Security.Cryptography.X509Certificates.X509Certificate2]::new($raw) } catch {}
                    if (-not $cert) {
                        try {
                            $cert = [System.Security.Cryptography.X509Certificates.X509Certificate2]::new(
                                $raw,
                                '',
                                [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]::EphemeralKeySet
                            )
                        } catch {}
                    }
                    if (-not $cert) {
                        throw "Unable to hydrate X509Certificate2 from PEM fragment."
                    }
                    $certificates += $cert
                    Write-Verbose ("Loaded certificate '{0}' from {1}" -f $cert.Subject, $Path)
                } catch {
                    Write-Verbose ("Failed to decode certificate block from {0}: {1}" -f $Path, $_.Exception.Message)
                }
            }
            $collecting = $false
            $buffer = ''
            continue
        }
        if ($collecting) {
            $buffer += $trimmed
        }
    }

    Write-Verbose ("Discovered {0} certificate(s) in {1}" -f $certificates.Count, $Path)
    return $certificates
}

function Ensure-BorealisCertificateValidator {
    if (-not ('Borealis.Update.CertificateValidator' -as [Type])) {
        $typeDefinition = @"
using System;
using System.Collections.Generic;
using System.Net;
using System.Net.Security;
using System.Security.Cryptography.X509Certificates;

namespace Borealis.Update
{
    public static class CertificateValidator
    {
        private static readonly HashSet<string> _trusted = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        public static bool AllowLoopback { get; set; }

        static CertificateValidator()
        {
            AllowLoopback = true;
        }

        public static void ReplaceTrustedThumbprints(string[] thumbprints)
        {
            _trusted.Clear();
            if (thumbprints == null)
            {
                return;
            }

            foreach (var thumb in thumbprints)
            {
                if (string.IsNullOrWhiteSpace(thumb))
                {
                    continue;
                }

                _trusted.Add(thumb);
            }
        }

        public static bool Validate(object sender, X509Certificate certificate, X509Chain chain, SslPolicyErrors errors)
        {
            if (errors == SslPolicyErrors.None)
            {
                return true;
            }

            X509Certificate2 cert2 = certificate as X509Certificate2;
            if (cert2 == null && certificate != null)
            {
                cert2 = new X509Certificate2(certificate);
            }

            if (cert2 != null && _trusted.Contains(cert2.Thumbprint))
            {
                return true;
            }

            if (chain != null)
            {
                foreach (var element in chain.ChainElements)
                {
                    if (element.Certificate != null && _trusted.Contains(element.Certificate.Thumbprint))
                    {
                        return true;
                    }
                }
            }

            if (AllowLoopback)
            {
                var request = sender as HttpWebRequest;
                if (request != null && request.RequestUri != null)
                {
                    var host = request.RequestUri.DnsSafeHost;
                    if (string.Equals(host, "localhost", StringComparison.OrdinalIgnoreCase) ||
                        string.Equals(host, "127.0.0.1", StringComparison.OrdinalIgnoreCase) ||
                        string.Equals(host, "::1", StringComparison.OrdinalIgnoreCase))
                    {
                        return true;
                    }
                }
            }

            return false;
        }
    }
}
"@
        Add-Type -TypeDefinition $typeDefinition -Language CSharp -ErrorAction Stop
    }
}

function Initialize-BorealisTlsContext {
    param(
        [string]$AgentRoot,
        [string]$ServerBaseUrl
    )

    if ($script:BorealisTlsInitialized -and $script:BorealisTrustedThumbprints.Count -gt 0) {
        return
    }

    $trusted = @()
    $cachedCertPath = Get-ExistingServerCertificatePath -AgentRoot $AgentRoot
    if ($cachedCertPath) {
        Write-UpdateLog ("Attempting Borealis Engine connection using cached certificate: {0}" -f $cachedCertPath) 'INFO'
        try {
            $trusted += Get-CertificatesFromPem -Path $cachedCertPath
        } catch {
            Write-UpdateLog ("Unable to load cached certificate; continuing without it ({0})." -f $_.Exception.Message) 'WARN'
        }
    }

    if ($trusted.Count -gt 0) {
        $script:BorealisTrustedThumbprints = $trusted | ForEach-Object { $_.Thumbprint.ToUpperInvariant() } | Sort-Object -Unique
        Write-Verbose ("Borealis TLS trust store loaded {0} certificate(s)." -f $script:BorealisTrustedThumbprints.Count)
        Write-UpdateLog ("TLS trust store initialized with {0} certificate(s)." -f $script:BorealisTrustedThumbprints.Count) 'INFO'
    } else {
        $script:BorealisTrustedThumbprints = @()
        Write-Verbose "No Borealis TLS certificates located; loopback hosts will be allowed without CA verification."
        Write-UpdateLog "No cached Borealis Engine certificate available yet; limiting TLS checks to loopback hosts." 'WARN'
    }

    Ensure-BorealisCertificateValidator
    try {
        [Borealis.Update.CertificateValidator]::ReplaceTrustedThumbprints($script:BorealisTrustedThumbprints)
    } catch {}

    try {
        $protocolType = [System.Net.SecurityProtocolType]
        $hasSystemDefault = [System.Enum]::IsDefined($protocolType, 'SystemDefault')
        if ($hasSystemDefault) {
            # Allow the OS to negotiate the strongest available protocol (TLS 1.3 on modern hosts).
            [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.SecurityProtocolType]::SystemDefault
            Write-UpdateLog "SecurityProtocol configured to SystemDefault (OS-negotiated)." 'DEBUG'
        } else {
            $protocol = [System.Net.SecurityProtocolType]::Tls12 -bor [System.Net.SecurityProtocolType]::Tls11
            if ([System.Enum]::IsDefined($protocolType, 'Tls13')) {
                $tls13 = [System.Enum]::Parse($protocolType, 'Tls13')
                $protocol = $protocol -bor $tls13
            }
            [System.Net.ServicePointManager]::SecurityProtocol = $protocol
            Write-UpdateLog ("SecurityProtocol configured to legacy mask: {0}" -f $protocol) 'DEBUG'
        }
    } catch {}

    if (-not $script:BorealisCallbackApplied) {
        try {
            $callback = New-Object System.Net.Security.RemoteCertificateValidationCallback([Borealis.Update.CertificateValidator]::Validate)
            [System.Net.ServicePointManager]::ServerCertificateValidationCallback = $callback
            $script:BorealisCallbackApplied = $true
        } catch {}
    }

    $script:BorealisTlsInitialized = $true
}

function Get-AgentPythonExecutable {
    param(
        [string]$AgentRoot
    )

    $candidates = @()
    if ($AgentRoot) {
        try {
            $agentParent = Split-Path $AgentRoot -Parent
            if ($agentParent) {
                $candidates += (Join-Path $agentParent 'Scripts\python.exe')
            }
        } catch {}
    }
    $candidates += (Join-Path $scriptDir 'Agent\Scripts\python.exe')
    $candidates += (Join-Path $scriptDir 'Dependencies\Python\python.exe')
    foreach ($candidate in ($candidates | Select-Object -Unique)) {
        try {
            if (Test-Path $candidate -PathType Leaf) {
                Write-UpdateLog ("Using Python executable for HTTP helper: {0}" -f $candidate) 'DEBUG'
                return $candidate
            }
        } catch {}
    }
    Write-UpdateLog "No Python executable found for HTTP helper fallback." 'WARN'
    return ''
}

function Ensure-AgentPythonHttpHelper {
    if ($script:AgentPythonHttpHelper -and (Test-Path $script:AgentPythonHttpHelper -PathType Leaf)) {
        return $script:AgentPythonHttpHelper
    }

    $helperDir = Join-Path ([System.IO.Path]::GetTempPath()) 'BorealisUpdate'
    try {
        if (-not (Test-Path $helperDir -PathType Container)) {
            New-Item -ItemType Directory -Force -Path $helperDir | Out-Null
        }
    } catch {}

    $helperPath = Join-Path $helperDir 'agent_http_client.py'
    $helperSource = @"
import json
import ssl
import sys
import urllib.error
import urllib.request


def _build_context(cafile):
    if cafile:
        ctx = ssl.create_default_context()
        try:
            ctx.load_verify_locations(cafile=cafile)
        except Exception:
            pass
        minimum = getattr(ssl, "TLSVersion", None)
        if minimum is not None:
            try:
                ctx.minimum_version = ssl.TLSVersion.TLSv1_2
            except Exception:
                pass
        return ctx
    ctx = ssl._create_unverified_context()
    try:
        ctx.check_hostname = False
    except Exception:
        pass
    try:
        ctx.verify_mode = ssl.CERT_NONE
    except Exception:
        pass
    return ctx


def _read_payload():
    data = sys.stdin.buffer.read()
    if not data:
        return {}
    try:
        text = data.decode("utf-8-sig")
    except Exception:
        text = data.decode("utf-8", errors="ignore")
    return json.loads(text)


def _peer_certificate(handle):
    try:
        raw = getattr(handle, "fp", None)
        if raw is None:
            raw = getattr(handle, "file", None)
        if raw is None:
            return None
        raw = getattr(raw, "raw", raw)
        sock = getattr(raw, "_sock", None)
        if sock is None:
            sock = getattr(raw, "socket", None)
        if sock is None:
            return None
        der = sock.getpeercert(True)
        if der:
            return ssl.DER_cert_to_PEM_cert(der)
    except Exception:
        return None
    return None


def main():
    try:
        payload = _read_payload()
    except Exception as exc:  # pragma: no cover - defensive
        json.dump({"error": "payload decode failed: %s" % (exc,)}, sys.stdout)
        return 1

    method = (payload.get("method") or "GET").upper()
    url = payload.get("url")
    headers = payload.get("headers") or {}
    body = payload.get("body")
    content_type = payload.get("content_type")
    timeout = payload.get("timeout") or 30
    cafile = payload.get("cafile")

    if body is not None and not isinstance(body, (bytes, bytearray)):
        body = str(body).encode("utf-8")

    request = urllib.request.Request(url=url, data=body, method=method)
    for key, value in headers.items():
        if value is None:
            continue
        request.add_header(str(key), str(value))

    if content_type and all(k.lower() != "content-type" for k in request.headers):
        request.add_header("Content-Type", str(content_type))

    ctx = _build_context(cafile if isinstance(cafile, str) else None)

    try:
        with urllib.request.urlopen(request, timeout=float(timeout), context=ctx) as response:
            text = response.read().decode("utf-8", errors="replace")
            json.dump({"status": response.status, "body": text, "certificate": _peer_certificate(response)}, sys.stdout)
            return 0
    except urllib.error.HTTPError as exc:
        text = exc.read().decode("utf-8", errors="replace") if exc.fp else ""
        json.dump({"status": exc.code, "body": text, "certificate": _peer_certificate(exc)}, sys.stdout)
        return 0
    except Exception as exc:  # pragma: no cover - defensive
        json.dump({"error": str(exc)}, sys.stdout)
        return 1


if __name__ == "__main__":  # pragma: no cover - defensive
    raise SystemExit(main())
"@

    try {
        Set-Content -Path $helperPath -Value $helperSource -Encoding UTF8 -Force
        $script:AgentPythonHttpHelper = $helperPath
        Write-UpdateLog ("Staged Python HTTP helper at {0}" -f $helperPath) 'DEBUG'
    } catch {
        $script:AgentPythonHttpHelper = ''
    }

    return $script:AgentPythonHttpHelper
}

function Invoke-AgentHttpRequest {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Method,

        [Parameter(Mandatory = $true)]
        [string]$Uri,

        [hashtable]$Headers,
        [string]$Body,
        [string]$ContentType,
        [string]$AgentRoot,
        [int]$TimeoutSeconds = 30
    )

    $headersToUse = @{}
    if ($Headers) {
        foreach ($key in $Headers.Keys) {
            $value = $Headers[$key]
            if ($null -ne $value -and $value.ToString()) {
                $headersToUse[$key] = $value
            }
        }
    }

    $invokeParams = @{
        Uri            = $Uri
        Method         = $Method
        Headers        = $headersToUse
        UseBasicParsing = $true
        ErrorAction    = 'Stop'
    }
    if ($Body) { $invokeParams['Body'] = $Body }
    if ($ContentType) { $invokeParams['ContentType'] = $ContentType }
    if ($TimeoutSeconds -gt 0) { $invokeParams['TimeoutSec'] = $TimeoutSeconds }

    Write-UpdateLog ("HTTP {0} {1}" -f $Method.ToUpperInvariant(), $Uri) 'DEBUG'
    try {
        $response = Invoke-WebRequest @invokeParams
        Write-UpdateLog ("Invoke-WebRequest succeeded (HTTP {0})." -f $response.StatusCode) 'DEBUG'
        return [pscustomobject]@{
            StatusCode = $response.StatusCode
            Content    = $response.Content
        }
    } catch {
        Write-Verbose ("Invoke-WebRequest failed for {0}: {1}" -f $Uri, $_.Exception.Message)
        Write-UpdateLog ("Invoke-WebRequest failed for {0}: {1}" -f $Uri, $_.Exception.Message) 'WARN'
    }

    $pythonExe = Get-AgentPythonExecutable -AgentRoot $AgentRoot
    if (-not $pythonExe) {
        Write-UpdateLog "Python executable for HTTP fallback not found." 'ERROR'
        return $null
    }

    $helperScript = Ensure-AgentPythonHttpHelper
    if (-not $helperScript) {
        Write-UpdateLog "Unable to stage Python HTTP helper script." 'ERROR'
        return $null
    }

    $cafile = Get-ExistingServerCertificatePath -AgentRoot $AgentRoot
    if ($cafile) {
        Write-UpdateLog ("Attempting to contact Borealis Engine using cached certificate: {0}" -f $cafile) 'INFO'
    } else {
        Write-UpdateLog "No cached Borealis Engine certificate found; establishing connection without validation." 'WARN'
    }
    $payload = @{
        method       = $Method
        url          = $Uri
        headers      = $headersToUse
        body         = $Body
        content_type = $ContentType
        timeout      = $TimeoutSeconds
        cafile       = $cafile
    } | ConvertTo-Json -Depth 6

    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $pythonExe
    $psi.Arguments = ('"{0}"' -f $helperScript)
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.UseShellExecute = $false
    $psi.CreateNoWindow = $true

    try {
        $process = [System.Diagnostics.Process]::Start($psi)
    } catch {
        Write-Verbose ("Failed to start Python helper: {0}" -f $_.Exception.Message)
        Write-UpdateLog ("Failed to launch Python helper: {0}" -f $_.Exception.Message) 'ERROR'
        return $null
    }

    try {
        $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
        $bytes = $utf8NoBom.GetBytes($payload)
        $process.StandardInput.BaseStream.Write($bytes, 0, $bytes.Length)
        $process.StandardInput.BaseStream.Flush()
        $process.StandardInput.Close()
    } catch {
        Write-UpdateLog ("Failed to write payload to Python helper stdin: {0}" -f $_.Exception.Message) 'WARN'
        try { $process.StandardInput.Close() } catch {}
    }

    $stdout = $process.StandardOutput.ReadToEnd()
    $stderr = $process.StandardError.ReadToEnd()
    $process.WaitForExit()

    if ($stderr) {
        Write-Verbose ("Python helper stderr: {0}" -f $stderr.Trim())
        Write-UpdateLog ("Python helper stderr: {0}" -f $stderr.Trim()) 'DEBUG'
    }

    if (-not $stdout) {
        Write-UpdateLog "Python helper returned empty response." 'ERROR'
        return $null
    }

    try {
        $json = $stdout | ConvertFrom-Json -ErrorAction Stop
    } catch {
        Write-Verbose ("Unable to parse Python helper output: {0}" -f $_.Exception.Message)
        return $null
    }

    if ($json.error) {
        Write-Verbose ("Python helper reported error: {0}" -f $json.error)
        Write-UpdateLog ("Python helper error: {0}" -f $json.error) 'ERROR'
        return $null
    }

    if ($json.certificate) {
        $savedPath = Save-ServerCertificateCache -AgentRoot $AgentRoot -CertificatePem $json.certificate
        if ($savedPath) {
            $script:BorealisTlsInitialized = $false
            Initialize-BorealisTlsContext -AgentRoot $AgentRoot -ServerBaseUrl $Uri
        }
    }

    Write-UpdateLog ("Python helper completed HTTP call with status {0}." -f $json.status) 'DEBUG'
    return [pscustomobject]@{
        StatusCode  = $json.status
        Content     = $json.body
        Certificate = $json.certificate
    }
}

function Get-AgentServiceId {
    param(
        [string]$AgentRoot
    )

    if (-not $AgentRoot) { $AgentRoot = $scriptDir }
    $settingsDir = Join-Path $AgentRoot 'Settings'
    $candidates = @(
        (Join-Path $settingsDir 'agent_settings_SYSTEM.json')
        (Join-Path $settingsDir 'agent_settings_CURRENTUSER.json')
        (Join-Path $settingsDir 'agent_settings_svc.json')
        (Join-Path $settingsDir 'agent_settings_user.json')
        (Join-Path $settingsDir 'agent_settings.json')
    )

    foreach ($path in $candidates) {
        try {
            if (Test-Path $path -PathType Leaf) {
                $raw = Get-Content -Path $path -Raw -ErrorAction Stop
                if (-not $raw) { continue }
                $cfg = $raw | ConvertFrom-Json -ErrorAction Stop
                $value = ($cfg.agent_id)
                if ($value) { return ($value.ToString()).Trim() }
            }
        } catch {}
    }

    return ''
}

function Get-AgentGuid {
    param(
        [string]$AgentRoot
    )

    if (-not $AgentRoot) { $AgentRoot = $scriptDir }
    $candidates = @()
    if ($AgentRoot) {
        $settingsDir = Join-Path $AgentRoot 'Settings'
        if ($settingsDir) {
            $settingsGuid = Join-Path $settingsDir 'Agent_GUID.txt'
            if ($candidates -notcontains $settingsGuid) { $candidates += $settingsGuid }
        }
        $legacyPath = Join-Path $AgentRoot 'agent_GUID'
        if ($candidates -notcontains $legacyPath) { $candidates += $legacyPath }
    }

    $projectSettingsGuid = Join-Path $scriptDir 'Agent\Borealis\Settings\Agent_GUID.txt'
    if ($candidates -notcontains $projectSettingsGuid) { $candidates += $projectSettingsGuid }
    $projectLegacyGuid = Join-Path $scriptDir 'Agent\Borealis\agent_GUID'
    if ($candidates -notcontains $projectLegacyGuid) { $candidates += $projectLegacyGuid }

    foreach ($path in ($candidates | Select-Object -Unique)) {
        try {
            if (Test-Path $path -PathType Leaf) {
                $value = (Get-Content -Path $path -Raw -ErrorAction Stop)
                if ($value) { return $value.Trim() }
            }
        } catch {}
    }

    return ''
}

function Get-AgentSettingsDirectory {
    param(
        [string]$AgentRoot
    )

    if (-not $AgentRoot) { $AgentRoot = $scriptDir }
    $settingsDir = Join-Path $AgentRoot 'Settings'
    if ($settingsDir -and (Test-Path $settingsDir -PathType Container)) {
        return $settingsDir
    }
    return ''
}

function Get-ProtectedTokenString {
    param(
        [string]$Path
    )

    if (-not $Path -or -not (Test-Path $Path -PathType Leaf)) {
        return ''
    }

    try {
        $protected = [System.IO.File]::ReadAllBytes($Path)
        if (-not $protected -or $protected.Length -eq 0) { return '' }
    } catch {
        return ''
    }

    $scopes = @(
        [System.Security.Cryptography.DataProtectionScope]::CurrentUser,
        [System.Security.Cryptography.DataProtectionScope]::LocalMachine
    )

    foreach ($scope in $scopes) {
        try {
            $unprotected = [System.Security.Cryptography.ProtectedData]::Unprotect($protected, $null, $scope)
            if ($unprotected -and $unprotected.Length -gt 0) {
                return [System.Text.Encoding]::UTF8.GetString($unprotected)
            }
        } catch {
            continue
        }
    }

    return ''
}

function Invoke-AgentTokenRefresh {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ServerBaseUrl,

        [Parameter(Mandatory = $true)]
        [string]$AgentGuid,

        [Parameter(Mandatory = $true)]
        [string]$RefreshToken,

        [string]$AgentRoot
    )

    if ([string]::IsNullOrWhiteSpace($ServerBaseUrl) -or [string]::IsNullOrWhiteSpace($AgentGuid) -or [string]::IsNullOrWhiteSpace($RefreshToken)) {
        Write-UpdateLog "Invoke-AgentTokenRefresh called with missing parameters." 'ERROR'
        return $null
    }

    Write-UpdateLog ("Requesting access token refresh for agent {0}" -f $AgentGuid) 'STEP'
    $base = $ServerBaseUrl.TrimEnd('/')
    $uri = "$base/api/agent/token/refresh"
    $payload = @{
        guid = $AgentGuid
        refresh_token = $RefreshToken
    } | ConvertTo-Json
    $headers = @{
        'User-Agent'    = 'borealis-agent-updater'
        'Content-Type'  = 'application/json'
    }

    $response = Invoke-AgentHttpRequest -Method 'POST' -Uri $uri -Headers $headers -Body $payload -ContentType 'application/json' -AgentRoot $AgentRoot -TimeoutSeconds 60
    if (-not $response) {
        Write-UpdateLog "Token refresh request produced no response." 'ERROR'
        return $null
    }

    try {
        $json = $response.Content | ConvertFrom-Json
    } catch {
        Write-UpdateLog ("Token refresh response decode failed: {0}" -f $_.Exception.Message) 'ERROR'
        return $null
    }

    if ($json -and $json.access_token) {
        $expiresIn = 900
        try {
            if ($json.expires_in) {
                $expiresIn = [int]$json.expires_in
            }
        } catch {}
        $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
        $expiresAt = $now + [Math]::Max(0, $expiresIn - 5)
        return [pscustomobject]@{
            AccessToken = ($json.access_token).Trim()
            ExpiresAt   = $expiresAt
        }
    }

    Write-UpdateLog "Token refresh response did not include access token." 'WARN'
    return $null
}

function Get-AgentAccessTokenContext {
    param(
        [string]$AgentRoot,
        [string]$ServerBaseUrl,
        [string]$AgentGuid
    )

    $settingsDir = Get-AgentSettingsDirectory -AgentRoot $AgentRoot
    if (-not $settingsDir) { return $null }

    Write-UpdateLog ("Loading agent access tokens from {0}" -f $settingsDir) 'DEBUG'
    $accessPath = Join-Path $settingsDir 'access.jwt'
    $metaPath   = Join-Path $settingsDir 'access.meta.json'
    $refreshPath = Join-Path $settingsDir 'refresh.token'

    $accessToken = ''
    $expiresAt = 0

    if (Test-Path $accessPath -PathType Leaf) {
        try {
            $accessToken = (Get-Content -Path $accessPath -Raw -ErrorAction Stop).Trim()
        } catch {
            $accessToken = ''
        }
    }

    if (Test-Path $metaPath -PathType Leaf) {
        try {
            $metaRaw = Get-Content -Path $metaPath -Raw -ErrorAction Stop
            if ($metaRaw) {
                $metaJson = $metaRaw | ConvertFrom-Json -ErrorAction Stop
                if ($metaJson -and $metaJson.access_expires_at) {
                    $expiresAt = [int]$metaJson.access_expires_at
                }
            }
        } catch {
            $expiresAt = 0
        }
    }

    $now = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
    if ($accessToken -and $expiresAt -gt ($now + 30)) {
        $secondsLeft = $expiresAt - $now
        Write-UpdateLog ("Using cached access token (expires in {0} seconds)." -f $secondsLeft) 'INFO'
        return [pscustomobject]@{
            AccessToken = $accessToken
            ExpiresAt   = $expiresAt
        }
    }

    $refreshToken = Get-ProtectedTokenString -Path $refreshPath
    if (-not $refreshToken) {
        Write-UpdateLog "Refresh token unavailable; cannot authenticate with server." 'ERROR'
        return $null
    }

    Write-UpdateLog "Cached token expired or missing; requesting refreshed access token." 'WARN'
    $refreshResult = Invoke-AgentTokenRefresh -ServerBaseUrl $ServerBaseUrl -AgentGuid $AgentGuid -RefreshToken $refreshToken -AgentRoot $AgentRoot
    if ($refreshResult -and $refreshResult.AccessToken) {
        Write-UpdateLog "Access token successfully refreshed." 'SUCCESS'
        return $refreshResult
    }

    Write-UpdateLog "Failed to refresh access token." 'ERROR'
    return $null
}
function Get-RepositoryCommitHash {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ProjectRoot,

        [string]$AgentRoot,

        [string]$GitExe
    )

    $candidates = @()
    if ($ProjectRoot -and ($candidates -notcontains $ProjectRoot)) { $candidates += $ProjectRoot }
    if ($AgentRoot -and ($candidates -notcontains $AgentRoot)) { $candidates += $AgentRoot }
    if ($ProjectRoot) {
        $agentRootCandidate = Join-Path $ProjectRoot 'Agent\Borealis'
        if ((Test-Path $agentRootCandidate -PathType Container) -and ($candidates -notcontains $agentRootCandidate)) {
            $candidates += $agentRootCandidate
        }
    }

    if ($candidates.Count -gt 0) {
        Write-UpdateLog ("Evaluating repository hash from candidate roots: {0}" -f ([string]::Join(', ', $candidates))) 'DEBUG'
    }

    if ($GitExe -and (Test-Path $GitExe -PathType Leaf)) {
        foreach ($root in $candidates) {
            try {
                if (-not (Test-Path (Join-Path $root '.git') -PathType Container)) { continue }
                $revParse = Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $root -Arguments @('rev-parse','HEAD')
                if ($revParse) {
                    $candidate = ($revParse | Select-Object -Last 1)
                    if ($candidate) {
                        $result = $candidate.Trim()
                        if ($result) {
                            Write-UpdateLog ("Repository hash determined via git: {0}" -f $result) 'INFO'
                            return $result
                        }
                    }
                }
            } catch {}
        }
    }

    foreach ($root in $candidates) {
        try {
            $gitDir = Join-Path $root '.git'
            $fetchHead = Join-Path $gitDir 'FETCH_HEAD'
            if (-not (Test-Path $fetchHead -PathType Leaf)) { continue }
            foreach ($line in Get-Content -Path $fetchHead -ErrorAction Stop) {
                $trim = ($line).Trim()
                if (-not $trim -or $trim.StartsWith('#')) { continue }
                $split = $trim.Split(@("`t", ' '), [StringSplitOptions]::RemoveEmptyEntries)
                if ($split.Count -gt 0) {
                    $candidate = $split[0].Trim()
                    if ($candidate) { return $candidate }
                }
            }
        } catch {}
    }

    foreach ($root in $candidates) {
        try {
            $gitDir = Join-Path $root '.git'
            $headPath = Join-Path $gitDir 'HEAD'
            if (-not (Test-Path $headPath -PathType Leaf)) { continue }
            $head = (Get-Content -Path $headPath -Raw -ErrorAction Stop).Trim()
            if (-not $head) { continue }

            if ($head -match '^ref:\s*(.+)$') {
                $ref = $Matches[1].Trim()
                if ($ref) {
                    $refPath = $gitDir
                    foreach ($part in ($ref -split '/')) {
                        if ($part) { $refPath = Join-Path $refPath $part }
                    }
                    if (Test-Path $refPath -PathType Leaf) {
                        $commit = (Get-Content -Path $refPath -Raw -ErrorAction Stop).Trim()
                        if ($commit) { return $commit }
                    }
                    $packedRefs = Join-Path $gitDir 'packed-refs'
                    if (Test-Path $packedRefs -PathType Leaf) {
                        foreach ($line in Get-Content -Path $packedRefs -ErrorAction Stop) {
                            $trim = ($line).Trim()
                            if (-not $trim -or $trim.StartsWith('#') -or $trim.StartsWith('^')) { continue }
                            $parts = $trim.Split(' ', 2)
                            if ($parts.Count -ge 2 -and $parts[1].Trim() -eq $ref) {
                                $candidate = $parts[0].Trim()
                                if ($candidate) { return $candidate }
                            }
                        }
                    }
                }
            } else {
                $detached = $head.Split([Environment]::NewLine, [StringSplitOptions]::RemoveEmptyEntries)
                if ($detached.Length -gt 0) {
                    $candidate = $detached[0].Trim()
                    if ($candidate) { return $candidate }
                }
            }
        } catch {}
    }

    if ($AgentRoot) {
        $stored = Get-StoredAgentHash -AgentRoot $AgentRoot
        if ($stored) {
            Write-UpdateLog ("Using stored agent hash fallback: {0}" -f $stored) 'WARN'
            return $stored
        }
    }

    Write-UpdateLog "Unable to determine repository hash from any source." 'WARN'
    return ''
}

function Get-StoredAgentHash {
    param(
        [string]$AgentRoot
    )

    if (-not $AgentRoot) { return '' }

    try {
        $settingsDir = Join-Path $AgentRoot 'Settings'
        $hashFile = Join-Path $settingsDir 'agent_hash.txt'
        if (Test-Path $hashFile -PathType Leaf) {
            $value = (Get-Content -Path $hashFile -Raw -ErrorAction Stop).Trim()
            return $value
        }
    } catch {}

    return ''
}

function Set-StoredAgentHash {
    param(
        [string]$AgentRoot,
        [string]$AgentHash
    )

    if ([string]::IsNullOrWhiteSpace($AgentRoot) -or [string]::IsNullOrWhiteSpace($AgentHash)) { return }

    try {
        $settingsDir = Join-Path $AgentRoot 'Settings'
        if (-not (Test-Path $settingsDir -PathType Container)) {
            New-Item -ItemType Directory -Force -Path $settingsDir | Out-Null
        }
        $hashFile = Join-Path $settingsDir 'agent_hash.txt'
        Set-Content -Path $hashFile -Value $AgentHash.Trim() -Encoding UTF8
        Write-UpdateLog ("Stored agent hash to {0}" -f $hashFile) 'DEBUG'
    } catch {}
}

function Set-GitFetchHeadHash {
    param(
        [string]$ProjectRoot,
        [string]$CommitHash,
        [string]$BranchName = 'main'
    )

    if ([string]::IsNullOrWhiteSpace($ProjectRoot) -or [string]::IsNullOrWhiteSpace($CommitHash)) { return }

    try {
        $gitDir = Join-Path $ProjectRoot '.git'
        if (-not (Test-Path $gitDir -PathType Container)) {
            New-Item -ItemType Directory -Force -Path $gitDir | Out-Null
        }
        $fetchHead = Join-Path $gitDir 'FETCH_HEAD'
        $branchSegment = if ([string]::IsNullOrWhiteSpace($BranchName)) { '' } else { "`tbranch '$BranchName'" }
        $content = "{0}{1}" -f ($CommitHash.Trim()), $branchSegment
        Set-Content -Path $fetchHead -Value $content -Encoding UTF8
        Write-UpdateLog ("Wrote FETCH_HEAD in {0} to {1}" -f $gitDir, $CommitHash) 'DEBUG'
    } catch {}
}

function Get-ServerCurrentRepoHash {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ServerBaseUrl,
        [string]$AuthToken,
        [string]$AgentRoot
    )

    if ([string]::IsNullOrWhiteSpace($ServerBaseUrl)) { return $null }

    $base = $ServerBaseUrl.TrimEnd('/')
    $uri = "$base/api/repo/current_hash"
    $headers = @{ 'User-Agent' = 'borealis-agent-updater' }
    if ($AuthToken -and $AuthToken.Trim()) {
        $headers['Authorization'] = "Bearer $AuthToken"
    }

    $response = Invoke-AgentHttpRequest -Method 'GET' -Uri $uri -Headers $headers -AgentRoot $AgentRoot -TimeoutSeconds 40
    if ($response -and $response.StatusCode -ge 200 -and $response.StatusCode -lt 300) {
        try {
            $json = $response.Content | ConvertFrom-Json
            Write-UpdateLog ("Received repo hash payload from server (branch={0}, sha={1})." -f $json.branch, $json.sha) 'SUCCESS'
            return $json
        } catch {
            Write-Verbose ("Unable to decode repo hash response: {0}" -f $_.Exception.Message)
            Write-UpdateLog ("Failed to decode repo hash response JSON: {0}" -f $_.Exception.Message) 'ERROR'
            return $null
        }
    }

    if ($response) {
        Write-Verbose ("Repo hash request returned HTTP {0}: {1}" -f $response.StatusCode, $response.Content)
        Write-UpdateLog ("Repo hash request returned HTTP {0}: {1}" -f $response.StatusCode, $response.Content) 'WARN'
    } else {
        Write-Verbose ("Repo hash request to {0} returned no response." -f $uri)
        Write-UpdateLog ("Repo hash request to {0} returned no response." -f $uri) 'ERROR'
    }

    return $null
}

function Submit-AgentHash {
    param(
        [Parameter(Mandatory = $true)]
        [string]$ServerBaseUrl,

        [Parameter(Mandatory = $true)]
        [string]$AgentId,

        [Parameter(Mandatory = $true)]
        [string]$AgentHash,

        [string]$AgentGuid,

        [string]$AuthToken,

        [string]$AgentRoot
    )

    if ([string]::IsNullOrWhiteSpace($ServerBaseUrl) -or [string]::IsNullOrWhiteSpace($AgentHash)) {
        return
    }

    $base = $ServerBaseUrl.TrimEnd('/')
    $uri = "$base/api/agent/hash"
    $payloadBody = @{ agent_hash = $AgentHash }
    if (-not [string]::IsNullOrWhiteSpace($AgentId)) { $payloadBody.agent_id = $AgentId }
    if (-not [string]::IsNullOrWhiteSpace($AgentGuid)) { $payloadBody.agent_guid = $AgentGuid }
    $payload = $payloadBody | ConvertTo-Json -Depth 3
    $headers = @{ 'User-Agent' = 'borealis-agent-updater' }
    if ($AuthToken -and $AuthToken.Trim()) {
        $headers['Authorization'] = "Bearer $AuthToken"
    }

    $response = Invoke-AgentHttpRequest -Method 'POST' -Uri $uri -Headers $headers -Body $payload -ContentType 'application/json' -AgentRoot $AgentRoot -TimeoutSeconds 60
    if (-not $response) {
        Write-Verbose "Submit-AgentHash request returned no response."
        Write-UpdateLog "Agent hash submission produced no response from server." 'ERROR'
        return $null
    }

    Write-UpdateLog ("Agent hash submission HTTP status: {0}" -f $response.StatusCode) 'DEBUG'
    try {
        $json = $response.Content | ConvertFrom-Json
        return $json
    } catch {
        Write-Verbose ("Submit-AgentHash response decode failed: {0}" -f $_.Exception.Message)
        Write-UpdateLog ("Failed to parse agent hash submission response: {0}" -f $_.Exception.Message) 'ERROR'
        return $null
    }
}

function Sync-AgentHashRecord {
    param(
        [string]$ProjectRoot,
        [string]$AgentRoot,
        [string]$AgentHash,
        [string]$ServerBaseUrl,
        [string]$AgentId,
        [string]$AgentGuid,
        [string]$AuthToken = '',
        [string]$BranchName = 'main'
    )

    if ([string]::IsNullOrWhiteSpace($AgentHash)) { return }

    Write-UpdateLog ("Sync-AgentHashRecord invoked with hash {0}" -f $AgentHash) 'STEP'
    if ($ProjectRoot) {
        Set-GitFetchHeadHash -ProjectRoot $ProjectRoot -CommitHash $AgentHash -BranchName $BranchName
    }
    if ($AgentRoot) {
        Set-StoredAgentHash -AgentRoot $AgentRoot -AgentHash $AgentHash
    }

    if ([string]::IsNullOrWhiteSpace($ServerBaseUrl)) { return }

    Write-Host ("Submitting agent hash to server: {0}" -f $AgentHash)
    Write-UpdateLog ("Submitting agent hash to {0} (AgentId={1}, AgentGuid={2})" -f $ServerBaseUrl, $AgentId, $AgentGuid) 'STEP'

    if ([string]::IsNullOrWhiteSpace($AgentId) -and [string]::IsNullOrWhiteSpace($AgentGuid)) {
        Write-Host "Agent identifier unavailable; skipping agent hash submission." -ForegroundColor DarkYellow
        Write-UpdateLog "Agent identifier unavailable; cannot submit hash to server." 'WARN'
        return
    }

    try {
        $submitResult = Submit-AgentHash -ServerBaseUrl $ServerBaseUrl -AgentId $AgentId -AgentHash $AgentHash -AgentGuid $AgentGuid -AuthToken $AuthToken -AgentRoot $AgentRoot
        if ($submitResult -and ($submitResult.status -eq 'ok')) {
            Write-Host "The server-side agent hash database record was updated successfully."
            Write-UpdateLog "Server acknowledged agent hash update." 'SUCCESS'
        } elseif ($submitResult -and ($submitResult.status -eq 'ignored')) {
            Write-Host "Server ignored the agent hash update (the agent is not enrolled with the server)." -ForegroundColor DarkYellow
            Write-UpdateLog "Server returned 'ignored' for agent hash submission." 'WARN'
        } elseif ($submitResult) {
            Write-Host "Server agent_hash update response unrecognized.  We don't know what to do here. (Panic)" -ForegroundColor DarkYellow
            Write-UpdateLog ("Unexpected server response for agent hash submission: {0}" -f ($submitResult | ConvertTo-Json -Depth 5)) 'WARN'
        }
    } catch {
        Write-Verbose ("Failed to Submit Agent Hash: {0}" -f $_.Exception.Message)
        Write-UpdateLog ("Agent hash submission failed: {0}" -f $_.Exception.Message) 'ERROR'
    }
}

function Invoke-BorealisUpdate {
    param(
        [Parameter(Mandatory = $true)]
        [string]$GitExe,

        [Parameter(Mandatory = $true)]
        [string]$RepositoryUrl,

        [Parameter(Mandatory = $true)]
        [string]$TargetHash,

        [string]$BranchName = 'main',

        [switch]$Silent
    )

    if ([string]::IsNullOrWhiteSpace($TargetHash)) {
        throw 'Target commit hash is required for Borealis update.'
    }

    $preservePath = Join-Path $scriptDir "Data\Server\Python_API_Endpoints\Tesseract-OCR"
    $preserveBackupPath = Join-Path $scriptDir "Update_Staging\Tesseract-OCR"
    $ansibleEePath = Join-Path $scriptDir "Agent\Ansible_EE"
    $ansibleEeBackupPath = Join-Path $scriptDir "Update_Staging\Ansible_EE"

    Run-Step "Updating: Move Tesseract-OCR Folder Somewhere Safe to Restore Later" {
        if (Test-Path $preservePath) {
            $stagingPath = Join-Path $scriptDir "Update_Staging"
            if (-not (Test-Path $stagingPath)) { New-Item -ItemType Directory -Force -Path $stagingPath | Out-Null }
            Move-Item -Path $preservePath -Destination $preserveBackupPath -Force
        }
    }

    Run-Step "Updating: Preserve Ansible Execution Environment" {
        if (Test-Path $ansibleEePath) {
            $stagingPath = Join-Path $scriptDir "Update_Staging"
            if (-not (Test-Path $stagingPath)) { New-Item -ItemType Directory -Force -Path $stagingPath | Out-Null }
            if (Test-Path $ansibleEeBackupPath) {
                Remove-Item -Path $ansibleEeBackupPath -Recurse -Force -ErrorAction SilentlyContinue
            }
            Move-Item -Path $ansibleEePath -Destination $ansibleEeBackupPath -Force
        }
    }

    Run-Step "Updating: Clean Up Folders to Prepare for Update" {
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue `
            (Join-Path $scriptDir "Data"), `
            (Join-Path $scriptDir "Server\web-interface\src"), `
            (Join-Path $scriptDir "Server\web-interface\build"), `
            (Join-Path $scriptDir "Server\web-interface\public"), `
            (Join-Path $scriptDir "Server\Borealis"), `
            (Join-Path $scriptDir '.git')
    }

    $stagingPath = Join-Path $scriptDir "Update_Staging"
    $cloneDir = Join-Path $stagingPath 'repo'

    Run-Step "Updating: Create Update Staging Folder" {
        if (-not (Test-Path $stagingPath)) { New-Item -ItemType Directory -Force -Path $stagingPath | Out-Null }
        if (Test-Path $cloneDir) {
            Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $cloneDir
        }
    }

    Run-Step "Updating: Clone Repository Source" {
        $cloneArgs = @('clone','--no-tags')
        if (-not [string]::IsNullOrWhiteSpace($BranchName)) {
            $cloneArgs += @('--branch', $BranchName)
        }
        $cloneArgs += @($RepositoryUrl, $cloneDir)
        Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $stagingPath -Arguments $cloneArgs | Out-Null
    }

    Run-Step "Updating: Checkout Target Revision" {
        $normalizedHash = $TargetHash.Trim()
        $haveHash = $false
        try {
            Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $cloneDir -Arguments @('rev-parse', $normalizedHash) | Out-Null
            $haveHash = $true
        } catch {
            $haveHash = $false
        }

        if (-not $haveHash) {
            Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $cloneDir -Arguments @('fetch','origin',$normalizedHash) | Out-Null
        }

        if ([string]::IsNullOrWhiteSpace($BranchName)) {
            Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $cloneDir -Arguments @('checkout', $normalizedHash) | Out-Null
        } else {
            Invoke-GitCommand -GitExe $GitExe -WorkingDirectory $cloneDir -Arguments @('checkout','-B',$BranchName,$normalizedHash) | Out-Null
        }
    }

    Run-Step "Updating: Copy Update Files into Production Borealis Root Folder" {
        Get-ChildItem -Path $cloneDir -Force | ForEach-Object {
            $destination = Join-Path $scriptDir $_.Name
            if ($_.PSIsContainer) {
                Copy-Item -Path $_.FullName -Destination $destination -Recurse -Force
            } else {
                Copy-Item -Path $_.FullName -Destination $scriptDir -Force
            }
        }
    }

    Run-Step "Updating: Restore Tesseract-OCR Folder" {
        $restorePath = Join-Path $scriptDir "Data\Server\Python_API_Endpoints"
        if (Test-Path $preserveBackupPath) {
            if (-not (Test-Path $restorePath)) { New-Item -ItemType Directory -Force -Path $restorePath | Out-Null }
            Move-Item -Path $preserveBackupPath -Destination $restorePath -Force
        }
    }

    Run-Step "Updating: Restore Ansible Execution Environment" {
        $restorePath = Join-Path $scriptDir "Agent"
        if (Test-Path $ansibleEeBackupPath) {
            if (-not (Test-Path $restorePath)) { New-Item -ItemType Directory -Force -Path $restorePath | Out-Null }
            Move-Item -Path $ansibleEeBackupPath -Destination $restorePath -Force
        }
    }

    Run-Step "Updating: Clean Up Update Staging Folder" {
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $stagingPath
    }

    if (-not $Silent) {
        Write-Host "Unattended Borealis update completed." -ForegroundColor Green
    }
}

function Invoke-BorealisAgentUpdate {
    Write-Host "==============================================="
    Write-Host " Borealis - Automation Platform Updater Script "
    Write-Host "==============================================="
    Write-UpdateLog "Starting Borealis updater execution." 'STEP'

    $agentRootCandidate = Join-Path $scriptDir 'Agent\Borealis'
    $agentRoot = $scriptDir
    if (Test-Path $agentRootCandidate -PathType Container) {
        try {
            $agentRoot = (Resolve-Path -Path $agentRootCandidate -ErrorAction Stop).Path
        } catch {
            $agentRoot = $agentRootCandidate
        }
    }
    Write-UpdateLog ("Agent root resolved to {0}" -f $agentRoot) 'INFO'

    $agentGuid = Get-AgentGuid -AgentRoot $agentRoot
    if ($agentGuid) {
        Write-Host ("Agent GUID: {0}" -f $agentGuid)
        Write-UpdateLog ("Operating on agent GUID {0}" -f $agentGuid) 'INFO'
    } else {
        Write-Host "Warning: No agent GUID detected - Please deploy the agent, associating it with a Borealis server then try running the updater script again." -ForegroundColor Yellow
        Write-Host "⚠️ Borealis update aborted."
        Write-UpdateLog "Agent GUID missing; aborting update." 'ERROR'
        return
    }

    $gitExe = Get-GitExecutablePath -ProjectRoot $scriptDir
    $currentHash = Get-RepositoryCommitHash -ProjectRoot $scriptDir -AgentRoot $agentRoot -GitExe $gitExe
    $serverBaseUrl = Get-BorealisServerUrl -AgentRoot $agentRoot
    Initialize-BorealisTlsContext -AgentRoot $agentRoot -ServerBaseUrl $serverBaseUrl
    $agentId = Get-AgentServiceId -AgentRoot $agentRoot
    Write-UpdateLog ("Agent service ID detected: {0}" -f $agentId) 'DEBUG'

    $authContext = Get-AgentAccessTokenContext -AgentRoot $agentRoot -ServerBaseUrl $serverBaseUrl -AgentGuid $agentGuid
    if (-not $authContext -or -not $authContext.AccessToken) {
        Write-Host "Unable to obtain agent authentication token. Ensure the agent is running and enrolled, then rerun the updater." -ForegroundColor Yellow
        Write-Host "⚠️ Borealis update aborted."
        Write-UpdateLog "Authentication context unavailable; aborting update." 'ERROR'
        return
    }
    $authToken = $authContext.AccessToken

    Write-UpdateLog "Querying Borealis server for current repository hash." 'STEP'
    $serverRepoInfo = Get-ServerCurrentRepoHash -ServerBaseUrl $serverBaseUrl -AuthToken $authToken -AgentRoot $agentRoot
    $serverHash = ''
    $serverBranch = 'main'
    if ($serverRepoInfo) {
        try { $serverHash = (($serverRepoInfo.sha) -as [string]).Trim() } catch { $serverHash = '' }
        try {
            $branchCandidate = (($serverRepoInfo.branch) -as [string]).Trim()
            if ($branchCandidate) { $serverBranch = $branchCandidate }
        } catch { $serverBranch = 'main' }
    }

    $updateMode = $env:update_mode
    if ($updateMode) { $updateMode = $updateMode.ToLowerInvariant() } else { $updateMode = 'update' }
    $forceUpdate = $updateMode -eq 'force_update'
    Write-UpdateLog ("Updater mode: {0} (force={1})" -f $updateMode, $forceUpdate) 'INFO'

    if ($currentHash) {
        Write-Host ("Local Agent Hash: {0}" -f $currentHash)
    } else {
        Write-Host "Local Agent Hash: unavailable"
    }

    if ($serverHash) {
        Write-Host ("Borealis Server Hash: {0}" -f $serverHash)
    } else {
        Write-Host "Borealis Server Hash: unavailable"
    }

    $normalizedLocalHash = if ($currentHash) { $currentHash.Trim().ToLowerInvariant() } else { '' }
    $normalizedServerHash = if ($serverHash) { $serverHash.Trim().ToLowerInvariant() } else { '' }
    $hashesMatch = ($normalizedLocalHash -and $normalizedServerHash -and ($normalizedLocalHash -eq $normalizedServerHash))
    $needsUpdate = $forceUpdate -or (-not $hashesMatch)

    if ($forceUpdate) {
        Write-Host "Force update requested; skipping hash comparison." -ForegroundColor Yellow
        Write-UpdateLog "Force update requested; bypassing hash comparison." 'WARN'
    } elseif (-not $serverHash) {
        Write-Host "Borealis server hash unavailable; cannot continue." -ForegroundColor Yellow
        Write-Host "⚠️ Borealis update aborted."
        Write-UpdateLog "Server hash unavailable; aborting." 'ERROR'
        return
    } elseif (-not $needsUpdate) {
        Write-Host "Local agent files already match the server repository hash." -ForegroundColor Green
        Write-UpdateLog "Local agent hash matches remote; ensuring server record is updated." 'SUCCESS'
        Sync-AgentHashRecord -ProjectRoot $scriptDir -AgentRoot $agentRoot -AgentHash $serverHash -ServerBaseUrl $serverBaseUrl -AgentId $agentId -AgentGuid $agentGuid -AuthToken $authToken -BranchName $serverBranch
        Write-Host "✅ Borealis - Automation Platform Already Up-to-Date"
        return
    } else {
        Write-Host "Repository hash mismatch detected; update required."
        Write-UpdateLog ("Repository hash mismatch detected (local={0}, remote={1})." -f $currentHash, $serverHash) 'WARN'
    }

    if (-not ($gitExe) -or -not (Test-Path $gitExe -PathType Leaf)) {
        Write-Host "Bundled Git dependency not found. Run '.\Borealis.ps1 -Agent' to redeploy the agent dependencies and try again." -ForegroundColor Yellow
        Write-Host "⚠️ Borealis update aborted."
        Write-UpdateLog "Bundled Git dependency missing; aborting update." 'ERROR'
        return
    }

    $mutex = $null
    $gotMutex = $false
    $managedTasks = @()
    try {
        $mutex = New-Object System.Threading.Mutex($false, 'Global\BorealisUpdate')
        $gotMutex = $mutex.WaitOne(0)
        if (-not $gotMutex) {
            Write-Verbose 'Another update is already running (mutex held). Exiting quietly.'
            Write-Host "⚠️ Borealis update already in progress on this device."
            return
        }

        $staging = Join-Path $scriptDir 'Update_Staging'

        $managedTasks = Stop-AgentScheduledTasks -TaskNames @('Borealis Agent','Borealis Agent (UserHelper)')
        if ($managedTasks.Count -gt 0) {
            Write-UpdateLog ("Managed tasks stopped: {0}" -f ($managedTasks -join ', ')) 'INFO'
        } else {
            Write-UpdateLog "No managed tasks were running when update started." 'DEBUG'
        }
        Run-Step "Updating: Terminate Running Python Processes" { Stop-AgentPythonProcesses }

        $updateSucceeded = $false
        try {
            Write-UpdateLog ("Starting repository sync to commit {0} (branch={1})." -f $serverHash, $serverBranch) 'STEP'
            Invoke-BorealisUpdate -GitExe $gitExe -RepositoryUrl $repositoryUrl -TargetHash $serverHash -BranchName $serverBranch -Silent
            $updateSucceeded = $true
            Write-UpdateLog "Repository sync completed successfully." 'SUCCESS'
        } finally {
            if ($managedTasks.Count -gt 0) {
                Start-AgentScheduledTasks -TaskNames $managedTasks
                Write-UpdateLog "Agent scheduled tasks restarted." 'INFO'
            }
        }

        if (-not $updateSucceeded) {
            Write-UpdateLog "Repository sync reported failure." 'ERROR'
            throw 'Borealis update failed.'
        }

        $refreshedContext = Get-AgentAccessTokenContext -AgentRoot $agentRoot -ServerBaseUrl $serverBaseUrl -AgentGuid $agentGuid
        if ($refreshedContext -and $refreshedContext.AccessToken) {
            $authToken = $refreshedContext.AccessToken
        }
        $postUpdateInfo = Get-ServerCurrentRepoHash -ServerBaseUrl $serverBaseUrl -AuthToken $authToken -AgentRoot $agentRoot
        if ($postUpdateInfo) {
            try {
                $refreshedSha = (($postUpdateInfo.sha) -as [string]).Trim()
                if ($refreshedSha) { $serverHash = $refreshedSha }
            } catch {}
            try {
                $branchCandidate = (($postUpdateInfo.branch) -as [string]).Trim()
                if ($branchCandidate) { $serverBranch = $branchCandidate }
            } catch {}
        }

        $newHash = Get-RepositoryCommitHash -ProjectRoot $scriptDir -AgentRoot $agentRoot -GitExe $gitExe

        $normalizedNewHash = if ($newHash) { $newHash.Trim().ToLowerInvariant() } else { '' }
        $normalizedServerHash = if ($serverHash) { $serverHash.Trim().ToLowerInvariant() } else { '' }

        if ($normalizedServerHash -and (-not $normalizedNewHash -or $normalizedNewHash -ne $normalizedServerHash)) {
            $newHash = $serverHash
            $normalizedNewHash = $normalizedServerHash
        } elseif (-not $newHash -and $serverHash) {
            $newHash = $serverHash
        }

        if ($newHash) {
            Write-UpdateLog ("Final agent hash determined: {0}" -f $newHash) 'INFO'
            Sync-AgentHashRecord -ProjectRoot $scriptDir -AgentRoot $agentRoot -AgentHash $newHash -ServerBaseUrl $serverBaseUrl -AgentId $agentId -AgentGuid $agentGuid -AuthToken $authToken -BranchName $serverBranch
        } else {
            Write-Host "Unable to determine repository hash for submission; server hash not updated." -ForegroundColor DarkYellow
            Write-UpdateLog "Unable to determine final agent hash; skipping submission." 'WARN'
        }

        Write-Host "✅ Borealis - Automation Platform Successfully Updated"
        Write-UpdateLog "Update workflow completed successfully." 'SUCCESS'
    } finally {
        if ($mutex -and $gotMutex) { $mutex.ReleaseMutex() | Out-Null }
        if ($mutex) { $mutex.Dispose() }
        Write-UpdateLog "Released update mutex and cleaned up resources." 'DEBUG'
    }
}

Invoke-BorealisAgentUpdate
