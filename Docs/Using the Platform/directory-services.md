# Directory Services

Directory Services connects Borealis operator login to LDAP, LDAPS, or Active Directory providers. Use it when operators should authenticate with directory credentials while Borealis still controls MFA, role mapping, and site scope.

<figure class="bo-screenshot">
  <img src="../Reference/images/repo_screenshots/Directory_Services.png" alt="Borealis Directory Services page" loading="lazy">
  <figcaption>Directory Services configures external identity providers and directory authentication behavior.</figcaption>
</figure>

## Add Provider

1. Open `Access Management > Directory Services`.
2. Select `New Provider`.
3. Fill `Basic` connection settings.
4. Configure `User / Group Mapping`.
5. Configure `Site Assignment` mappings for non-admin users.
6. Use `Diagnostics` to test lookup and access.
7. Save, test, then enable.

Providers stay disabled until they pass connectivity testing.

## Configure Trust

Use LDAPS when possible. Borealis can use system trust, uploaded CA PEM, or a reviewed/pinned server certificate. Host overrides let the Engine connect to a specific IP while preserving FQDN certificate validation.

When a provider lists more than one domain controller, uploaded Root/Intermediate CA PEM is preferred because each DC can present a different leaf certificate. If you pin leaf certificates instead, the PEM field must cover the non-expired certificate used by each reachable LDAPS server, or Borealis should connect only to the server whose leaf certificate is pinned. Expired certificates are rejected even when pinned.

## Map Access

- Admin group DNs grant Borealis Admin.
- User group DNs admit normal Borealis users.
- Site mapping rows assign directory groups to Borealis sites.
- Cached directory users keep Borealis TOTP MFA.

Directory users cannot use local Borealis passwords or passkeys.

??? example "Detailed Codex Breakdown"

    ### API endpoints

    - `GET /api/directory/providers` - list providers.
    - `POST /api/directory/providers` - create provider.
    - `PATCH /api/directory/providers/<provider_id>` - update or enable/disable provider.
    - `DELETE /api/directory/providers/<provider_id>` - delete unused provider.
    - `POST /api/directory/providers/certificate` - fetch LDAPS peer certificate metadata and PEM.
    - `POST /api/directory/providers/<provider_id>/test` - test connectivity.
    - `POST /api/directory/providers/<provider_id>/lookup-user` - lookup diagnostics.
    - `POST /api/directory/providers/<provider_id>/effective-access` - group/site access diagnostics.
    - `POST /api/directory/providers/<provider_id>/sync` - sync cached users.
    - `POST /api/users/<username>/directory-cache` - disable or re-enable cached user.

    ### Related documentation

    - [User Management](user-management.md)
    - [Site Assignments](site-assignments.md)
    - [Security Whitepaper](../Reference/security-whitepaper.md)
    - [Database Reference](../Reference/Data%20and%20Schema/db-reference.md)

    ### Source map

    - Directory API: `Data/Engine/Containers/api-backend/cmd/api-backend/directory.go`, `directory_management.go`, and `directory_ldap.go`
    - Directory UI: `Data/Engine/Containers/webui-frontend/data/web-interface/src/Access_Management/Directory_Services.jsx`
    - Auth flow: `Data/Engine/Containers/api-backend/data/services/API/access_management/login.py`

    ### Runtime behavior

    - Provider config lives in `directory_providers`.
    - Group role mapping lives in `directory_provider_group_mappings`.
    - Group-to-site mapping lives in `directory_provider_site_mappings`.
    - Directory bind passwords are Aegis-protected.
    - Directory user sessions are revalidated against cached user state on authenticated requests.
