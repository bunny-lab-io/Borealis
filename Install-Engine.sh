#!/usr/bin/env bash
# Verified stable-release bootstrap for fresh Borealis Engine installs and updates.

set -o errexit
set -o nounset
set -o pipefail

DEFAULT_REPOSITORY="bunny-lab-io/Borealis"
DEFAULT_REPO_URL="https://github.com/bunny-lab-io/Borealis.git"
GITHUB_API_BASE_URL="${BOREALIS_GITHUB_API_BASE_URL:-https://api.github.com}"
GITHUB_REPOSITORY="${BOREALIS_GITHUB_REPOSITORY:-${DEFAULT_REPOSITORY}}"
REPO_URL="${BOREALIS_ENGINE_REPO_URL:-${DEFAULT_REPO_URL}}"
RELEASE=""
NETWORK_MODE=""
INSTALL_DIR="${BOREALIS_INSTALL_DIR:-/opt/Borealis}"
TEMP_DIR=""

die() {
  printf 'Borealis Engine installer: %s\n' "$*" >&2
  exit 64
}

cleanup() {
  if [[ -n "${TEMP_DIR}" && -d "${TEMP_DIR}" ]]; then
    rm -rf -- "${TEMP_DIR}"
  fi
}
trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage:
  Install-Engine.sh --release YYYY.MM.REVISION[.HOTFIX] --network-mode <public|local> [--install-dir PATH]
EOF
}

while (($#)); do
  case "$1" in
    --release|--network-mode|--install-dir)
      [[ $# -ge 2 ]] || die "Missing value for $1."
      case "$1" in
        --release) RELEASE="$2" ;;
        --network-mode) NETWORK_MODE="$2" ;;
        --install-dir) INSTALL_DIR="$2" ;;
      esac
      shift 2
      ;;
    --release=*|--network-mode=*|--install-dir=*)
      key="${1%%=*}"
      value="${1#*=}"
      case "${key}" in
        --release) RELEASE="${value}" ;;
        --network-mode) NETWORK_MODE="${value}" ;;
        --install-dir) INSTALL_DIR="${value}" ;;
      esac
      shift
      ;;
    -h|--help|help)
      usage
      exit 0
      ;;
    *)
      die "Unsupported argument '$1'."
      ;;
  esac
done

[[ "${RELEASE}" =~ ^[0-9]{4}\.[0-9]{1,2}\.[0-9]+(\.[0-9]+)?$ ]] \
  || die "--release must use stable YYYY.MM.REVISION[.HOTFIX] form."
case "${NETWORK_MODE}" in
  public|local) ;;
  *) die "--network-mode must be public or local." ;;
esac
[[ -n "${INSTALL_DIR}" && "${INSTALL_DIR}" != "/" ]] \
  || die "--install-dir cannot be empty or '/'."
[[ "${GITHUB_REPOSITORY}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] \
  || die "BOREALIS_GITHUB_REPOSITORY must use owner/name form."
[[ "${GITHUB_API_BASE_URL}" == https://* ]] \
  || die "BOREALIS_GITHUB_API_BASE_URL must use HTTPS."
[[ "${REPO_URL}" == https://* ]] \
  || die "BOREALIS_ENGINE_REPO_URL must use HTTPS."

for command in curl python3 sha256sum stat mktemp uname; do
  command -v "${command}" >/dev/null 2>&1 || die "${command} is required."
done

TEMP_DIR="$(mktemp -d)"
chmod 0700 "${TEMP_DIR}"
release_json="${TEMP_DIR}/release.json"
manifest_path="${TEMP_DIR}/borealis-engine-install-manifest.json"
engine_path="${TEMP_DIR}/Engine.sh"
tag_json="${TEMP_DIR}/tag.json"

case "$(uname -s)-$(uname -m)" in
  Linux-x86_64|Linux-amd64) install_platform="linux-amd64" ;;
  Linux-aarch64|Linux-arm64) install_platform="linux-arm64" ;;
  *) die "Supported Engine installer platforms are linux-amd64 and linux-arm64." ;;
esac

curl_args=(
  --fail
  --silent
  --show-error
  --location
  --proto '=https'
  --tlsv1.2
)
api_curl_args=(
  "${curl_args[@]}"
  -H 'Accept: application/vnd.github+json'
  -H 'X-GitHub-Api-Version: 2022-11-28'
)
asset_curl_args=("${curl_args[@]}" -H 'Accept: application/octet-stream')
if [[ -n "${BOREALIS_GITHUB_TOKEN:-}" ]]; then
  auth_header="${TEMP_DIR}/github-auth-header"
  printf 'Authorization: Bearer %s\n' "${BOREALIS_GITHUB_TOKEN}" > "${auth_header}"
  chmod 0600 "${auth_header}"
  api_curl_args+=(-H "@${auth_header}")
  asset_curl_args+=(-H "@${auth_header}")
fi

release_url="${GITHUB_API_BASE_URL%/}/repos/${GITHUB_REPOSITORY}/releases/tags/${RELEASE}"
curl "${api_curl_args[@]}" "${release_url}" -o "${release_json}" \
  || die "Cannot read published GitHub release '${RELEASE}'."

release_metadata="$(python3 - "${release_json}" "${GITHUB_API_BASE_URL%/}" "${GITHUB_REPOSITORY}" "${RELEASE}" <<'PY'
import json
import pathlib
import re
import sys

path, api_base, expected_repository, expected_release = sys.argv[1:]
try:
    release = json.loads(pathlib.Path(path).read_text(encoding="utf-8"))
except Exception as exc:
    raise SystemExit(f"invalid GitHub release response: {exc}")
if release.get("tag_name") != expected_release:
    raise SystemExit("GitHub release tag does not match requested release")
if release.get("draft") is not False or release.get("prerelease") is not False:
    raise SystemExit("requested release must be published and non-prerelease")
if release.get("immutable") is not True:
    raise SystemExit("requested GitHub release is not immutable")

required = {
    "Install-Engine.sh": None,
    "Engine.sh": None,
    "borealis-engine-install-manifest.json": None,
    "SHA256SUMS": None,
}
asset_url_pattern = re.compile(
    re.escape(api_base + "/repos/" + expected_repository + "/releases/assets/")
    + r"[0-9]+"
)
for asset in release.get("assets") or []:
    name = str(asset.get("name") or "")
    if name not in required:
        continue
    digest = str(asset.get("digest") or "")
    url = str(asset.get("url") or "")
    if asset.get("state") != "uploaded":
        raise SystemExit(f"release asset {name} is not uploaded")
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        raise SystemExit(f"release asset {name} lacks GitHub SHA-256 digest")
    if not asset_url_pattern.fullmatch(url):
        raise SystemExit(f"release asset {name} has unexpected API URL")
    required[name] = (url, digest.removeprefix("sha256:"))
missing = [name for name, value in required.items() if value is None]
if missing:
    raise SystemExit("release lacks required asset(s): " + ", ".join(missing))
for name in ("Install-Engine.sh", "Engine.sh", "borealis-engine-install-manifest.json", "SHA256SUMS"):
    print(required[name][0])
    print(required[name][1])
PY
)" || die "GitHub release metadata validation failed."
mapfile -t release_fields <<< "${release_metadata}"
[[ "${#release_fields[@]}" -eq 8 ]] || die "GitHub release metadata is incomplete."
bootstrap_digest="${release_fields[1]}"
engine_url="${release_fields[2]}"
engine_api_digest="${release_fields[3]}"
manifest_url="${release_fields[4]}"
manifest_api_digest="${release_fields[5]}"

bootstrap_path="${BASH_SOURCE[0]:-}"
[[ -n "${bootstrap_path}" && -f "${bootstrap_path}" ]] \
  || die "Installer must be downloaded as a file; pipe-to-shell execution is blocked."
printf '%s  %s\n' "${bootstrap_digest}" "${bootstrap_path}" | sha256sum -c - >/dev/null \
  || die "Install-Engine.sh does not match immutable GitHub release asset digest."

curl "${asset_curl_args[@]}" "${manifest_url}" -o "${manifest_path}" \
  || die "Cannot download Engine install manifest."
printf '%s  %s\n' "${manifest_api_digest}" "${manifest_path}" | sha256sum -c - >/dev/null \
  || die "Engine install manifest does not match immutable GitHub asset digest."

tag_url="${GITHUB_API_BASE_URL%/}/repos/${GITHUB_REPOSITORY}/git/ref/tags/${RELEASE}"
curl "${api_curl_args[@]}" "${tag_url}" -o "${tag_json}" \
  || die "Cannot resolve GitHub release tag '${RELEASE}'."
for _ in 1 2 3; do
  tag_fields="$(python3 - "${tag_json}" "${GITHUB_API_BASE_URL%/}" "${GITHUB_REPOSITORY}" <<'PY'
import json
import pathlib
import re
import sys

path, api_base, repository = sys.argv[1:]
try:
    payload = json.loads(pathlib.Path(path).read_text(encoding="utf-8"))
except Exception as exc:
    raise SystemExit(f"invalid Git tag response: {exc}")
obj = payload.get("object") or {}
kind = str(obj.get("type") or "")
sha = str(obj.get("sha") or "").lower()
url = str(obj.get("url") or "")
if kind not in {"commit", "tag"} or not re.fullmatch(r"[0-9a-f]{40}", sha):
    raise SystemExit("Git tag does not resolve to commit or annotated tag")
annotated_tag_url_pattern = re.compile(
    re.escape(api_base + "/repos/" + repository + "/git/tags/") + r"[0-9a-f]{40}"
)
if kind == "tag" and not annotated_tag_url_pattern.fullmatch(url):
    raise SystemExit("annotated tag has unexpected API URL")
print(kind)
print(sha)
print(url)
PY
)" || die "GitHub release tag validation failed."
  mapfile -t resolved_tag <<< "${tag_fields}"
  [[ "${#resolved_tag[@]}" -eq 3 ]] || die "GitHub release tag metadata is incomplete."
  if [[ "${resolved_tag[0]}" == "commit" ]]; then
    source_sha="${resolved_tag[1]}"
    break
  fi
  curl "${api_curl_args[@]}" "${resolved_tag[2]}" -o "${tag_json}" \
    || die "Cannot resolve annotated GitHub release tag."
done
[[ -n "${source_sha:-}" ]] || die "GitHub release tag annotation depth exceeds supported limit."

manifest_fields="$(python3 - "${manifest_path}" "${GITHUB_REPOSITORY}" "${RELEASE}" "${source_sha}" "${install_platform}" <<'PY'
import json
import pathlib
import re
import sys

path, repository, release, source_sha, install_platform = sys.argv[1:]
try:
    manifest = json.loads(pathlib.Path(path).read_text(encoding="utf-8"))
except Exception as exc:
    raise SystemExit(f"invalid Engine install manifest: {exc}")
if manifest.get("schema_version") != 1:
    raise SystemExit("unsupported Engine install manifest schema")
if manifest.get("repository") != repository or manifest.get("release") != release:
    raise SystemExit("Engine install manifest identity mismatch")
if manifest.get("source_sha") != source_sha:
    raise SystemExit("Engine install manifest source SHA differs from release tag")
platforms = manifest.get("supported_platforms")
if not isinstance(platforms, list) or install_platform not in platforms:
    raise SystemExit(f"Engine install manifest does not support {install_platform}")
engine = (manifest.get("assets") or {}).get("engine") or {}
bootstrap = (manifest.get("assets") or {}).get("bootstrap") or {}
name = str(engine.get("name") or "")
url = str(engine.get("url") or "")
digest = str(engine.get("sha256") or "")
size = engine.get("size")
expected_base = f"https://github.com/{repository}/releases/download/{release}/"
if name != "Engine.sh" or url != expected_base + name or not re.fullmatch(r"[0-9a-f]{64}", digest):
    raise SystemExit("Engine install manifest has invalid Engine.sh identity")
if not isinstance(size, int) or isinstance(size, bool) or size < 1:
    raise SystemExit("Engine install manifest has invalid Engine.sh size")
bootstrap_name = str(bootstrap.get("name") or "")
bootstrap_url = str(bootstrap.get("url") or "")
bootstrap_digest = str(bootstrap.get("sha256") or "")
bootstrap_size = bootstrap.get("size")
if bootstrap_name != "Install-Engine.sh" or bootstrap_url != expected_base + bootstrap_name or not re.fullmatch(r"[0-9a-f]{64}", bootstrap_digest):
    raise SystemExit("Engine install manifest has invalid Install-Engine.sh identity")
if not isinstance(bootstrap_size, int) or isinstance(bootstrap_size, bool) or bootstrap_size < 1:
    raise SystemExit("Engine install manifest has invalid Install-Engine.sh size")
print(digest)
print(size)
print(bootstrap_digest)
print(bootstrap_size)
PY
)" || die "Engine install manifest validation failed."
mapfile -t manifest_engine <<< "${manifest_fields}"
[[ "${#manifest_engine[@]}" -eq 4 ]] || die "Engine install manifest is incomplete."
[[ "${manifest_engine[0]}" == "${engine_api_digest}" ]] \
  || die "Manifest and GitHub disagree on Engine.sh digest."
[[ "${manifest_engine[2]}" == "${bootstrap_digest}" ]] \
  || die "Manifest and GitHub disagree on Install-Engine.sh digest."
[[ "$(stat -c '%s' "${bootstrap_path}")" == "${manifest_engine[3]}" ]] \
  || die "Install-Engine.sh size verification failed."

curl "${asset_curl_args[@]}" "${engine_url}" -o "${engine_path}" \
  || die "Cannot download Engine.sh release asset."
printf '%s  %s\n' "${manifest_engine[0]}" "${engine_path}" | sha256sum -c - >/dev/null \
  || die "Engine.sh checksum verification failed."
[[ "$(stat -c '%s' "${engine_path}")" == "${manifest_engine[1]}" ]] \
  || die "Engine.sh size verification failed."
chmod 0700 "${engine_path}"

printf 'Verified immutable Borealis Engine release %s at %s.\n' "${RELEASE}" "${source_sha}"
unset BOREALIS_GITHUB_TOKEN
bash "${engine_path}" \
  --install-dir "${INSTALL_DIR}" \
  --repo-url "${REPO_URL}" \
  --release "${RELEASE}" \
  --release-sha "${source_sha}" \
  --network-mode "${NETWORK_MODE}" \
  deploy prod
