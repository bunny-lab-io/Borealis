![Borealis Logo](Data/Server/WebUI/public/Borealis_Logo_Full.png)

Borealis is a remote management platform with a simple, visual automation layer, enabling you to leverage scripts, Ansible playbooks, and advanced nodegraph-based automation workflows.  I originally created Borealis to work towards consolidating the core functionality of several standalone automation platforms in my homelab, such as TacticalRMM, Ansible AWX, SemaphoreUI, and a few others. 

---

## Features
- **Device Inventory**: OS, hardware, and status posted on connect and periodically.
- **Remote Script Execution**: Run PowerShell in `CURRENT USER` context or as `NT AUTHORITY\SYSTEM`.
- **Jobs and Scheduling**: Launch "*Quick Jobs*" instantly or create more advanced schedules.
- **Visual Workflows**: Drag‑and‑drop node canvas for combining steps, analysis, and logic.
- **Playbooks**: Run scripted procedures; ((*Ansible playbook support is in progress*)).
- **Windows‑first**. Linux server and agent deployment scripts are planned, as the core of Borealis is Python-based, it is already Linux-friendly; this just involves some housekeeping to bring the Linux experience to parity with Windows.  

## Screenshots
Device List Overview:
![Device List](Data/Repository_Resources/borealis_device_list.png)

Device Details:
![Device Details](Data/Repository_Resources/borealis_device_details.png)

Assembly List:
![Assembly List](Data/Repository_Resources/borealis_assembly_list.png)

Assembly Editor:
![Assembly Editor](Data/Repository_Resources/borealis_assembly_editor.png)

Scheduled Job History View:
![Scheduled Job History](Data/Repository_Resources/borealis_scheduled_job_history.png)

Workflow Editor:
[![Workflow Editor Demonstration](Data/Repository_Resources/borealis_flow_editor.png)](https://www.youtube.com/watch?v=6GLolR70CTo)


## Getting Started

### Installation
1) Start the Server:
   - Windows: `./Borealis.ps1 -Server -Flask` *Production Server @ http://localhost:5000*
   - Windows: `./Borealis.ps1 -Server -Vite` *Development Server @ http://localhost:5173*
2) (*Optional*) Install the Agent (*elevated PowerShell*):
   - Windows: `./Borealis.ps1 -Agent`

### Reverse Proxy Configuration
Traefik Dynamic Config: `Replace Service URL with actual IP of Borealis server`
```yml
http:
  routers:
    borealis:
      entryPoints:
        - websecure
      tls:
        certResolver: letsencrypt
      service: borealis
      rule: "Host(`borealis.example.com`) && PathPrefix(`/`)"
      middlewares:
        - cors-headers

  middlewares:
    cors-headers:
      headers:
        accessControlAllowOriginList:
          - "*"
        accessControlAllowMethods:
          - GET
          - POST
          - OPTIONS
        accessControlAllowHeaders:
          - Content-Type
          - Upgrade
          - Connection
        accessControlMaxAge: 100
        addVaryHeader: true

  services:
    borealis:
      loadBalancer:
        servers:
          - url: "http://127.0.0.1:5000"
        passHostHeader: true
```

## Security Breakdowns
The process that agents go through when authenticating securely with a Borealis server can be a little complex, so I have included a sequence diagram below to go over the core systems so you can visually understand what is going on behind-the-scenes.

### Agent/Server Enrollment
```mermaid
sequenceDiagram
    participant Operator
    participant Server
    participant SYS as "SYSTEM Agent"
    participant CUR as "CURRENTUSER Agent"

    Operator->>Server: Request installer code
    Server-->>Operator: Deliver hashed installer code
    Note over Operator,Server: Human-controlled code binds enrollment to known device

    par TLS Handshake (SYSTEM)
        SYS->>Server: Initiate TLS session
        Server-->>SYS: Present TLS certificate
    and TLS Handshake (CURRENTUSER)
        CUR->>Server: Initiate TLS session
        Server-->>CUR: Present TLS certificate
    end
    Note over SYS,Server: Certificate pinning plus CA checks stop MITM
    Note over CUR,Server: Pinning also blocks spoofed control planes

    SYS->>SYS: Generate Ed25519 identity key pair
    Note right of SYS: Private key stored under Certificates/... protected by DPAPI or chmod 600
    CUR->>CUR: Generate Ed25519 identity key pair
    Note right of CUR: Private key stored in user context and DPAPI-protected

    SYS->>Server: Enrollment request (installer code, public key, fingerprint)
    CUR->>Server: Enrollment request (installer code, public key, fingerprint)

    Server->>Operator: Prompt for enrollment approval
    Operator-->>Server: Approve device enrollment
    Note over Operator,Server: Manual approval blocks rogue agents

    Server-->>SYS: Send enrollment nonce
    Server-->>CUR: Send enrollment nonce
    SYS->>Server: Return signed nonce to prove key possession
    CUR->>Server: Return signed nonce
    Note over Server,Operator: Server verifies signatures and records GUID plus key fingerprint

    Server->>SYS: Issue GUID, short-lived token, refresh token, server cert, script-signing key
    Server->>CUR: Issue GUID, short-lived token, refresh token, server cert, script-signing key
    Note over SYS,Server: Agent pins cert, stores GUID, DPAPI-encrypts refresh token
    Note over CUR,Server: Agent stores GUID, pins cert, encrypts refresh token
    Note over Server,Operator: Database keeps refresh token hash, key fingerprint, audit trail

    loop Secure Sessions
        SYS->>Server: REST heartbeat and job polling with Bearer token
        CUR->>Server: REST heartbeat and WebSocket connect with Bearer token
        Server-->>SYS: Provide new access token before expiry
        Server-->>CUR: Provide new access token before expiry
        SYS->>Server: Refresh request over pinned TLS
        CUR->>Server: Refresh request over pinned TLS
    end

    Server-->>SYS: Deliver script payload plus Ed25519 signature
    SYS->>SYS: Verify signature before execution
    Server-->>CUR: Deliver script payload plus Ed25519 signature
    CUR->>CUR: Verify signature and reject tampered content
    Note over SYS,CUR: Signature failure triggers re-enrollment and detailed logging
    Note over Server,Operator: Persistent records and approvals sustain long term trust
```

### Code-Signed Remote Script Execution
```mermaid
sequenceDiagram
    participant Operator
    participant Server
    participant SYS as "SYSTEM Agent"
    participant CUR as "CURRENTUSER Agent"

    Operator->>Server: Upload or author script
    Server->>Server: Store script and metadata on-disk

    Operator->>Server: Request script execution on a specific device + execution context (NT Authority\SYSTEM or Current-User)
    Server->>Server: Load Ed25519 code signing key from secure store
    Server->>Server: Sign script hash and execution manifest (The Assembly)

    Server->>Server: Enqueue job with signed payload for target agent (SYSTEM or CurrentUser)
    Note over Server: Dispatch limited to enrolled agents with valid GUID + tokens

    loop Agent job polling (pinned TLS + Bearer token)
        SYS->>Server: REST heartbeat and job poll
        CUR->>Server: REST heartbeat and job poll
        Server-->>SYS: Pending job payloads
        Server-->>CUR: Pending job payloads
    end

    alt SYSTEM context
        Server-->>SYS: Script, signature, hash, execution parameters
        SYS->>SYS: Verify TLS pinning and token freshness
        SYS->>SYS: Verify Ed25519 signature using pinned server key
        SYS->>SYS: Recalculate script hash and compare
        Note right of SYS: Verification failure stops execution and logs incident
        SYS->>SYS: Execute via SYSTEM scheduled-task runner
        SYS-->>Server: Return execution status, output, telemetry
    else CURRENTUSER context
        Server-->>CUR: Script, signature, hash, execution parameters
        CUR->>CUR: Verify TLS pinning and token freshness
        CUR->>CUR: Verify Ed25519 signature using pinned server key
        CUR->>CUR: Recalculate script hash and compare
        Note right of CUR: Validation failure stops execution and logs incident
        CUR->>CUR: Execute within interactive PowerShell host
        CUR-->>Server: Return execution status, output, telemetry
    end

    Server->>Server: Record results and logs alongside job metadata
    Note over SYS,CUR: Pinned TLS, signed payloads, and DPAPI-protected secrets defend against tampering and replay
```
