function Ensure-LocalhostWinRMHttps {
    [CmdletBinding()]
    param(
        [string]$DnsName = $env:COMPUTERNAME
    )

    try {
        Set-Service WinRM -StartupType Automatic -ErrorAction Stop
        Start-Service WinRM -ErrorAction Stop
    } catch {}

    # Find or create a cert
    try {
        $cert = Get-ChildItem Cert:\LocalMachine\My |
            Where-Object { $_.Subject -match "CN=$DnsName" -and $_.NotAfter -gt (Get-Date).AddMonths(1) } |
            Sort-Object NotAfter -Descending | Select-Object -First 1
    } catch { $cert = $null }
    if (-not $cert) {
        try {
            $cert = New-SelfSignedCertificate -DnsName $DnsName -CertStoreLocation Cert:\LocalMachine\My -KeyLength 2048 -HashAlgorithm SHA256 -NotAfter (Get-Date).AddYears(3)
        } catch { $cert = $null }
    }
    $thumb = if ($cert) { $cert.Thumbprint } else { '' }

    # Ensure HTTPS listener exists; use Address='*' then restrict via IPv4Filter
    try {
        $https = Get-WSManInstance -ResourceURI winrm/config/listener -Enumerate -ErrorAction SilentlyContinue |
            Where-Object { $_.Transport -eq 'HTTPS' }
    } catch { $https = $null }
    if ((-not $https) -and $thumb) {
        $cmd = "winrm create winrm/config/Listener?Address=*+Transport=HTTPS @{Hostname=`"$DnsName`"; CertificateThumbprint=`"$thumb`"}"
        cmd /c $cmd | Out-Null
    }

    # Harden auth and encryption
    try { winrm set winrm/config/service/auth @{Basic="false"; Kerberos="true"; Negotiate="true"; CredSSP="false"} | Out-Null } catch {}
    try { winrm set winrm/config/service @{AllowUnencrypted="false"} | Out-Null } catch {}
    try { winrm set winrm/config/service @{AllowFreshCredentialsWhenNTLMOnly="true"} | Out-Null } catch {}
    try { winrm set winrm/config/service @{AllowCredSspAuthentication="false"} | Out-Null } catch {}
    try { winrm set winrm/config/service @{IPv4Filter="127.0.0.1"} | Out-Null } catch {}
    try { New-ItemProperty -Path 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System' -Name 'LocalAccountTokenFilterPolicy' -PropertyType DWord -Value 1 -Force | Out-Null } catch {}
}

function Ensure-BorealisServiceUser {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$UserName,
        [Parameter(Mandatory)][string]$PlaintextPassword
    )
    $localName = $UserName
    if ($localName.StartsWith('.\')) { $localName = $localName.Substring(2) }
    $secure = ConvertTo-SecureString $PlaintextPassword -AsPlainText -Force
    $u = Get-LocalUser -Name $localName -ErrorAction SilentlyContinue
    if (-not $u) {
        try {
            New-LocalUser -Name $localName -Password $secure -PasswordNeverExpires:$true -AccountNeverExpires:$true | Out-Null
        } catch {}
    } else {
        try { Set-LocalUser -Name $localName -Password $secure } catch {}
        try { Enable-LocalUser -Name $localName } catch {}
    }
    try {
        $admins = Get-LocalGroupMember -Group "Administrators" -ErrorAction SilentlyContinue
        if (-not ($admins | Where-Object { $_.Name -match "\\$localName$" })) {
            Add-LocalGroupMember -Group "Administrators" -Member $localName -ErrorAction SilentlyContinue
        }
    } catch {}
    $legacy = 'svcBorealisAnsibleRunner'
    if ($localName -ne $legacy) {
        try {
            $legacyUser = Get-LocalUser -Name $legacy -ErrorAction SilentlyContinue
            if ($legacyUser) {
                try { Remove-LocalGroupMember -Group "Administrators" -Member $legacy -ErrorAction SilentlyContinue } catch {}
                try { Disable-LocalUser -Name $legacy -ErrorAction SilentlyContinue } catch {}
                try { Remove-LocalUser -Name $legacy -ErrorAction SilentlyContinue } catch {}
                try { cmd /c "net user $legacy /DELETE" | Out-Null } catch {}
            }
        } catch {}
    }
}

function Write-LocalInventory {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][string]$UserName,
        [Parameter(Mandatory)][string]$Password,
        [ValidateSet('ntlm','negotiate','basic')][string]$Transport = 'ntlm'
    )
@"
[local]
localhost

[local:vars]
ansible_connection=winrm
ansible_host=127.0.0.1
ansible_port=5986
ansible_winrm_scheme=https
ansible_winrm_transport=$Transport
ansible_user=$UserName
ansible_password=$Password
ansible_winrm_server_cert_validation=ignore
"@ | Set-Content -Path $Path -Encoding UTF8

    # Lock down ACL to SYSTEM when running as SYSTEM
    try {
        $acl = New-Object System.Security.AccessControl.FileSecurity
        $sid = New-Object System.Security.Principal.SecurityIdentifier("S-1-5-18")
        $rule = New-Object System.Security.AccessControl.FileSystemAccessRule($sid,"FullControl","ContainerInherit,ObjectInherit","None","Allow")
        $acl.SetOwner($sid); $acl.SetAccessRuleProtection($true,$false); $acl.AddAccessRule($rule)
        Set-Acl -Path $Path -AclObject $acl
    } catch {}
}

Export-ModuleMember -Function Ensure-LocalhostWinRMHttps,Ensure-BorealisServiceUser,Write-LocalInventory
