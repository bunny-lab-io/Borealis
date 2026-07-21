# Software Bill of Materials

This software bill of materials inventories direct, repo-declared, bundled, container base image, or script-installed third-party software used by Borealis. Engine dependencies are grouped by service so reviewers can trace licensing impact to the runtime that uses each dependency.

Repeated entries are intentional when multiple Engine containers install the same requirement file or use the same runtime package. Python requirement files are currently unpinned, so exact resolved versions can change between installs. Explicitly pinned script, Go, Node, and container versions are called out below.

**Primary Sources**:

- `Data/Agent/go.mod`
- `Data/Agent/build-agent.sh`
- `Engine.sh`
- `Data/Engine/Containers/build-manifest.json`
- `Data/Engine/Containers/compose.yaml`
- `Data/Engine/Containers/*/Dockerfile`
- `Data/Engine/Containers/api-backend/go.mod`
- `Data/Engine/Containers/api-backend/build-api-backend.sh`
- `Data/Engine/Containers/api-backend/data/engine-requirements.txt`
- `Data/Engine/Containers/api-backend/data/engine-worker-requirements.txt`
- `Data/Engine/Containers/api-backend/data/Ansible/collections.yml`
- `Data/Engine/Containers/webui-frontend/data/web-interface/package.json`
- `Data/Engine/Containers/webui-frontend/data/web-interface/src/vendor/guacamole/guacamole-common-js.js`

## Borealis Agent Dependencies

| Service | Dependency | License |
| --- | --- | --- |
| agent | Go standard library/runtime (compiled into `Agent.exe`) | [BSD-3-Clause](https://go.dev/LICENSE) |
| agent | github.com/gorilla/websocket v1.5.3 | [BSD-2-Clause](https://github.com/gorilla/websocket/blob/main/LICENSE) |
| agent | golang.org/x/sys v0.28.0 | [BSD-3-Clause](https://cs.opensource.google/go/x/sys/+/master:LICENSE) |
| agent | WireGuard (Windows MSI package 1.1 / client 0.5.3 and Linux `wireguard-tools`) | [GPL-2.0-only](https://spdx.org/licenses/GPL-2.0-only.html) |
| agent | UltraVNC Server 1.8.2.1 | [GPL-2.0-only](https://spdx.org/licenses/GPL-2.0-only.html) |
| agent | Go toolchain (native Linux build helper installs official Go into `Dependencies/Go` when missing) | [BSD-3-Clause](https://go.dev/LICENSE) |

## Borealis Engine Dependencies

Use `shared-engine` for dependencies that support host deployment, build orchestration, or cross-container service management rather than one container runtime.

| Service | Dependency | License |
| :--- | :--- | :--- |
| api-backend | Alpine Linux container base image (`alpine:3.24`) | [Package-specific Alpine Linux licenses](https://pkgs.alpinelinux.org/packages) |
| api-backend | ca-certificates | [MPL-2.0](https://spdx.org/licenses/MPL-2.0.html) |
| api-backend | Git | [GPL-2.0-only](https://github.com/git/git/blob/master/COPYING) |
| api-backend | tzdata | [Public Domain](https://data.iana.org/time-zones/tzdb/LICENSE) |
| api-backend | Go standard library/runtime (compiled into the Go `api-backend` gateway) | [BSD-3-Clause](https://go.dev/LICENSE) |
| api-backend | github.com/lib/pq v1.10.9 (Go PostgreSQL driver) | [MIT](https://github.com/lib/pq/blob/master/LICENSE.md) |
| api-backend | golang.org/x/crypto v0.35.0 (Go scrypt KDF, Curve25519 tunnel helper, and SSH private-key parsing support) | [BSD-3-Clause](https://cs.opensource.google/go/x/crypto/+/master:LICENSE) |
| api-backend | github.com/go-ldap/ldap/v3 v3.4.8 (Go LDAP/LDAPS directory-provider support) | [MIT](https://github.com/go-ldap/ldap/blob/master/LICENSE) |
| api-backend | github.com/Azure/go-ntlmssp v0.0.0-20221128193559-754e69321358 (Go LDAP NTLM support dependency) | [MIT](https://github.com/Azure/go-ntlmssp/blob/master/LICENSE) |
| api-backend | github.com/go-asn1-ber/asn1-ber v1.5.5 (Go LDAP ASN.1 BER codec dependency) | [MIT](https://github.com/go-asn1-ber/asn1-ber/blob/master/LICENSE) |
| api-backend | github.com/go-webauthn/webauthn v0.10.2 (Go WebAuthn passkey ceremonies) | [BSD-3-Clause](https://github.com/go-webauthn/webauthn/blob/master/LICENSE) |
| api-backend | github.com/fxamacker/cbor/v2 v2.6.0 (Go WebAuthn CBOR codec dependency) | [MIT](https://github.com/fxamacker/cbor/blob/master/LICENSE) |
| api-backend | github.com/go-webauthn/x v0.1.9 (Go WebAuthn support dependency) | [BSD-3-Clause](https://github.com/go-webauthn/x/blob/master/LICENSE) |
| api-backend | github.com/golang-jwt/jwt/v5 v5.2.2 (Go WebAuthn transitive JWT support dependency) | [MIT](https://github.com/golang-jwt/jwt/blob/main/LICENSE) |
| api-backend | github.com/google/go-tpm v0.9.0 (Go WebAuthn TPM attestation support dependency) | [Apache-2.0](https://github.com/google/go-tpm/blob/main/LICENSE) |
| api-backend | github.com/google/uuid v1.6.0 (Go WebAuthn UUID support dependency) | [BSD-3-Clause](https://github.com/google/uuid/blob/master/LICENSE) |
| api-backend | github.com/mitchellh/mapstructure v1.5.0 (Go WebAuthn config decode dependency) | [MIT](https://github.com/mitchellh/mapstructure/blob/main/LICENSE) |
| api-backend | github.com/x448/float16 v0.8.4 (Go CBOR half-float dependency) | [MIT](https://github.com/x448/float16/blob/master/LICENSE) |
| api-backend | golang.org/x/sys v0.30.0 (Go WebAuthn system support dependency) | [BSD-3-Clause](https://cs.opensource.google/go/x/sys/+/master:LICENSE) |
| borealis-operator | Alpine Linux container base image (`alpine:3.24`) | [Package-specific Alpine Linux licenses](https://pkgs.alpinelinux.org/packages) |
| borealis-operator | ca-certificates | [MPL-2.0](https://spdx.org/licenses/MPL-2.0.html) |
| borealis-operator | Go standard library/runtime (compiled into the Go `api-backend` binary in operator mode) | [BSD-3-Clause](https://go.dev/LICENSE) |
| borealis-operator | tzdata | [Public Domain](https://data.iana.org/time-zones/tzdb/LICENSE) |
| job-scheduler | Alpine Linux container base image (`alpine:3.24`) | [Package-specific Alpine Linux licenses](https://pkgs.alpinelinux.org/packages) |
| job-scheduler | Bash | [GPL-3.0-or-later](https://www.gnu.org/licenses/gpl-3.0.html) |
| job-scheduler | ca-certificates | [MPL-2.0](https://spdx.org/licenses/MPL-2.0.html) |
| job-scheduler | Go standard library/runtime (compiled into the Go `api-backend` binary in scheduler mode, with retired orchestrator code still compiled for legacy tests) | [BSD-3-Clause](https://go.dev/LICENSE) |
| job-scheduler | Docker CLI (`docker-cli`, retained only for legacy orchestrator-compatible image paths; Stage 11 runtime has no Docker socket) | [Apache-2.0](https://github.com/docker/cli/blob/master/LICENSE) |
| job-scheduler | Docker Compose plugin (`docker-cli-compose`, retained only for legacy orchestrator-compatible image paths; Stage 11 runtime has no Docker socket) | [Apache-2.0](https://github.com/docker/compose/blob/main/LICENSE) |
| job-scheduler | Python 3 (used by detached `Engine.sh --service` helpers for manifest/env work) | [PSF License](https://docs.python.org/3/license.html) |
| job-scheduler | tzdata | [Public Domain](https://data.iana.org/time-zones/tzdb/LICENSE) |
| postgres-db | PostgreSQL container image (`postgres:17-bookworm`) | [PostgreSQL License plus Debian package licenses](https://www.postgresql.org/about/licence/) |
| remote-desktop-guacd | Apache Guacamole Server container image (`guacamole/guacd:1.6.0`) | [Apache-2.0](https://github.com/apache/guacamole-server/blob/1.6.0/LICENSE) |
| remote-desktop-guacd | Apache Guacamole Server (`guacd` and VNC plugin) 1.6.0 | [Apache-2.0](https://github.com/apache/guacamole-server/blob/1.6.0/LICENSE) |
| remote-desktop-guacd | LibVNCServer / LibVNCClient | [GPL-2.0-or-later](https://github.com/LibVNC/libvncserver/blob/master/COPYING) |
| site-worker | Python container base image (`python:3.12-slim-bookworm`) | [PSF License plus Debian package licenses](https://github.com/docker-library/python/blob/master/LICENSE) |
| site-worker | Flask | [BSD-3-Clause](https://spdx.org/licenses/BSD-3-Clause.html) |
| site-worker | flask-cors | [MIT](https://spdx.org/licenses/MIT.html) |
| site-worker | Flask-SocketIO | [MIT](https://spdx.org/licenses/MIT.html) |
| site-worker | eventlet | [MIT](https://spdx.org/licenses/MIT.html) |
| site-worker | cryptography | [Apache-2.0 OR BSD-3-Clause](https://github.com/pyca/cryptography/blob/main/LICENSE) |
| site-worker | PyJWT | [MIT](https://spdx.org/licenses/MIT.html) |
| site-worker | pyotp | [MIT](https://spdx.org/licenses/MIT.html) |
| site-worker | qrcode | [BSD](https://github.com/lincolnloop/python-qrcode/blob/main/LICENSE) |
| site-worker | webauthn | [BSD-3-Clause](https://github.com/duo-labs/py_webauthn/blob/master/LICENSE) |
| site-worker | Pillow | [MIT-CMU](https://github.com/python-pillow/Pillow/blob/main/LICENSE) |
| site-worker | requests | [Apache-2.0](https://spdx.org/licenses/Apache-2.0.html) |
| site-worker | aiohttp | [Apache-2.0 AND MIT](https://github.com/aio-libs/aiohttp/blob/master/LICENSE.txt) |
| site-worker | python-socketio | [MIT](https://spdx.org/licenses/MIT.html) |
| site-worker | websockets | [BSD-3-Clause](https://spdx.org/licenses/BSD-3-Clause.html) |
| site-worker | packaging | [Apache-2.0 OR BSD-2-Clause](https://github.com/pypa/packaging/blob/main/LICENSE) |
| site-worker | regex | [Apache-2.0 AND CNRI-Python](https://github.com/mrabarnett/mrab-regex/blob/hg/LICENSE.txt) |
| site-worker | SQLAlchemy | [MIT](https://spdx.org/licenses/MIT.html) |
| site-worker | alembic | [MIT](https://spdx.org/licenses/MIT.html) |
| site-worker | psycopg (`psycopg[binary]`) | [LGPL-3.0-only](https://spdx.org/licenses/LGPL-3.0-only.html) |
| site-worker | ldap3 | [LGPL-3.0-or-later](https://github.com/cannatag/ldap3/blob/dev/COPYING) |
| site-worker | pytest | [MIT](https://github.com/pytest-dev/pytest/blob/main/LICENSE) |
| site-worker | ansible-core | [GPL-3.0-or-later](https://spdx.org/licenses/GPL-3.0-or-later.html) |
| site-worker | ansible-runner | [Apache-2.0](https://spdx.org/licenses/Apache-2.0.html) |
| site-worker | jmespath | [MIT](https://spdx.org/licenses/MIT.html) |
| site-worker | pywinrm (`pywinrm[credssp]`) | [MIT](https://github.com/diyan/pywinrm/blob/master/LICENSE) |
| site-worker | pypsrp (`pypsrp[credssp]`) | [MIT](https://github.com/jborean93/pypsrp/blob/master/LICENSE) |
| site-worker | Impacket | [Apache-2.0](https://github.com/fortra/impacket/blob/master/LICENSE) |
| site-worker | ansible.windows collection | [GPL-3.0-or-later](https://github.com/ansible-collections/ansible.windows/blob/main/LICENSE) |
| site-worker | ansible.posix collection | [GPL-3.0-or-later](https://github.com/ansible-collections/ansible.posix/blob/main/LICENSE) |
| site-worker | community.general collection | [GPL-3.0-or-later](https://github.com/ansible-collections/community.general/blob/main/COPYING) |
| site-worker | WireGuard tools | [GPL-2.0-only](https://spdx.org/licenses/GPL-2.0-only.html) |
| traefik-edge | Traefik container image (`traefik:v3.3`) | [MIT](https://github.com/traefik/traefik/blob/master/LICENSE.md) |
| traefik-edge | Traefik (Borealis-managed local HTTPS edge and ACME client) | [MIT](https://github.com/traefik/traefik/blob/master/LICENSE.md) |
| webui-frontend | Node.js container base image (`node:22-alpine`) | [MIT plus Alpine package licenses](https://github.com/nodejs/node/blob/main/LICENSE) |
| webui-frontend | @emotion/react | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @emotion/styled | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @fortawesome/fontawesome-free | [CC-BY-4.0 AND OFL-1.1 AND MIT](https://github.com/FortAwesome/Font-Awesome/blob/7.x/LICENSE.txt) |
| webui-frontend | @fontsource/ibm-plex-sans | [OFL-1.1](https://openfontlicense.org/open-font-license-official-text/) |
| webui-frontend | @mui/icons-material | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @mui/material | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @mui/x-date-pickers | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @mui/x-tree-view | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @simplewebauthn/browser | [MIT](https://github.com/MasterKale/SimpleWebAuthn/blob/master/LICENSE) |
| webui-frontend | ag-grid-community | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | ag-grid-react | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | Apache Guacamole Client (`guacamole-common-js`) 1.6.0 | [Apache-2.0](https://github.com/apache/guacamole-client/blob/1.6.0/LICENSE) |
| webui-frontend | @codemirror/lang-css | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/lang-html | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/lang-javascript | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/lang-json | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/lang-markdown | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/lang-python | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/lang-sql | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/lang-xml | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/lang-yaml | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/language | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/legacy-modes | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/lint | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/merge | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/search | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/state | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/theme-one-dark | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @codemirror/view | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @lezer/highlight | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @uiw/react-codemirror | [MIT](https://github.com/uiwjs/react-codemirror/blob/master/LICENSE) |
| webui-frontend | codemirror | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | dayjs | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | normalize.css | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | prismjs | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | react-simple-code-editor | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | react | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | react-color | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | react-dom | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | react-router-dom | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | react-resizable | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | react-markdown | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | reactflow | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | react-simple-keyboard | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | socket.io-client | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @testing-library/jest-dom | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @testing-library/react | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | @vitejs/plugin-react ^4.7.0 | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | jsdom | [MIT](https://github.com/jsdom/jsdom/blob/main/LICENSE.txt) |
| webui-frontend | vite ^6.4.3 | [MIT](https://spdx.org/licenses/MIT.html) |
| webui-frontend | vitest | [MIT](https://github.com/vitest-dev/vitest/blob/main/LICENSE) |
| wireguard-tunnel | Debian Bookworm base image (`debian:bookworm-slim`) | [Debian Free Software Guidelines / package-specific licenses](https://www.debian.org/legal/licenses/) |
| wireguard-tunnel | Python (system Python on Linux) | [PSF License](https://docs.python.org/3/license.html) |
| wireguard-tunnel | WireGuard tools | [GPL-2.0-only](https://spdx.org/licenses/GPL-2.0-only.html) |
| shared-engine | Docker Engine (Linux Engine deployment runtime; Docker Desktop not used) | [Apache-2.0](https://github.com/moby/moby/blob/master/LICENSE) |
| shared-engine | Docker CLI (`docker-ce-cli`, host deployment and service-management helper) | [Apache-2.0](https://github.com/docker/cli/blob/master/LICENSE) |
| shared-engine | Docker Compose plugin (Linux Engine deployment orchestration) | [Apache-2.0](https://github.com/docker/compose/blob/main/LICENSE) |
| shared-engine | Docker Buildx plugin / BuildKit (optional local Engine image build cache acceleration) | [Apache-2.0](https://github.com/docker/buildx/blob/master/LICENSE) |
| shared-engine | K3s Kubernetes runtime (single-node baseline installed by `Engine.sh`; stable channel unless `BOREALIS_K3S_VERSION` is set) | [Apache-2.0](https://github.com/k3s-io/k3s/blob/master/LICENSE) |
| shared-engine | Longhorn `v1.12.0` default K3s storage baseline manifest (installed by `Engine.sh` unless `BOREALIS_K3S_LONGHORN_ENABLED=0`) | [Apache-2.0](https://github.com/longhorn/longhorn/blob/master/LICENSE) |
| shared-engine | Open-iSCSI / `iscsi-initiator-utils` host dependency for Longhorn volumes (installed by `Engine.sh` when missing) | [GPL-2.0-only](https://github.com/open-iscsi/open-iscsi/blob/master/COPYING) |
| shared-engine | iptables (host K3s API firewall rule management) | [GPL-2.0-only](https://git.netfilter.org/iptables/tree/COPYING) |
| shared-engine | Python (system Python on Linux, used by `Engine.sh` deployment helpers) | [PSF License](https://docs.python.org/3/license.html) |
| shared-engine | Go toolchain 1.23.12 (native Linux `api-backend` build helper installs official Go into `Dependencies/Go` when missing) | [BSD-3-Clause](https://go.dev/LICENSE) |

## Maintenance Notes

- Update this file whenever a dependency is added, removed, upgraded to a materially different licensed product, bundled into `Dependencies/`, or downloaded by bootstrap/runtime scripts.
- Keep Agent and Engine inventories separate so deployment reviewers can quickly assess licensing impact by runtime.
- Keep Engine dependency entries under the service that installs or vendors them. Use `shared-engine` for deployment or cross-container orchestration dependencies.
- If Borealis later adopts lockfiles or generated SBOM tooling, this file can be expanded to include resolved transitive dependencies.
