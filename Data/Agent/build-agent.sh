#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "${script_dir}/../.." && pwd)"
minimum_go_major=1
minimum_go_minor=22
go_version="${BOREALIS_GO_VERSION:-1.22.12}"
go_linux_amd64_sha256="4fa4f869b0f7fc6bb1eb2660e74657fbf04cdd290b5aef905585c86051b34d43"
go_linux_arm64_sha256="fd017e647ec28525e86ae8203236e0653242722a7436929b1f775744e26278e7"
go_install_root="${BOREALIS_GO_INSTALL_ROOT:-${repo_root}/Dependencies/Go/go${go_version}}"
version_value="${BOREALIS_AGENT_VERSION:-$(git -C "${repo_root}" rev-parse HEAD 2>/dev/null || echo dev)}"
output_root="${BOREALIS_GO_AGENT_OUTPUT_ROOT:-${script_dir}/dist}"
windows_icon_source="${script_dir}/Agent.syso"
windows_icon_target="${script_dir}/cmd/agent/agent_windows.syso"
staged_windows_icon=""

go_version_ok() {
  version="$1"
  major="${version%%.*}"
  minor="${version#*.}"
  minor="${minor%%.*}"
  [ "${major}" -gt "${minimum_go_major}" ] || {
    [ "${major}" -eq "${minimum_go_major}" ] && [ "${minor}" -ge "${minimum_go_minor}" ]
  }
}

installed_go_version() {
  "$1" version 2>/dev/null | sed -n 's/.* go\([0-9][0-9.]*\).*/\1/p'
}

download_file() {
  url="$1"
  destination="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "${url}" -o "${destination}"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "${url}" -O "${destination}"
  else
    printf 'Need curl or wget to install native Go toolchain.\n' >&2
    exit 127
  fi
}

go_archive_sha256() {
  case "${go_version}:${go_arch}" in
    1.22.12:amd64) printf '%s\n' "${go_linux_amd64_sha256}" ;;
    1.22.12:arm64) printf '%s\n' "${go_linux_arm64_sha256}" ;;
    *)
      [ -n "${BOREALIS_GO_SHA256:-}" ] || {
        printf 'No pinned checksum for Go %s linux-%s. Set BOREALIS_GO_SHA256 for an intentional override.\n' "${go_version}" "${go_arch}" >&2
        exit 64
      }
      printf '%s\n' "${BOREALIS_GO_SHA256}"
      ;;
  esac
}

install_native_go() {
  if [ "$(uname -s)" != "Linux" ]; then
    printf 'Automatic Go install supports Linux only.\n' >&2
    exit 127
  fi
  case "$(uname -m)" in
    x86_64|amd64) go_arch="amd64" ;;
    aarch64|arm64) go_arch="arm64" ;;
    *) printf 'Unsupported Go install architecture: %s\n' "$(uname -m)" >&2; exit 127 ;;
  esac
  if [ -x "${go_install_root}/bin/go" ]; then
    export PATH="${go_install_root}/bin:${PATH}"
    return
  fi
  url="${BOREALIS_GO_DOWNLOAD_URL:-https://go.dev/dl/go${go_version}.linux-${go_arch}.tar.gz}"
  tmp_dir="$(mktemp -d)"
  trap 'rm -rf "${tmp_dir}"' EXIT
  archive_path="${tmp_dir}/go.tar.gz"
  unpack_dir="${tmp_dir}/unpack"
  printf 'Installing native Go %s into %s\n' "${go_version}" "${go_install_root}"
  download_file "${url}" "${archive_path}"
  printf '%s  %s\n' "$(go_archive_sha256)" "${archive_path}" | sha256sum -c - >/dev/null \
    || { printf 'Go %s linux-%s checksum verification failed.\n' "${go_version}" "${go_arch}" >&2; exit 1; }
  mkdir -p "${unpack_dir}" "$(dirname -- "${go_install_root}")"
  tar -C "${unpack_dir}" -xzf "${archive_path}"
  rm -rf "${go_install_root}"
  mv "${unpack_dir}/go" "${go_install_root}"
  export PATH="${go_install_root}/bin:${PATH}"
}

stage_windows_icon() {
  if [ -f "${windows_icon_source}" ]; then
    cp "${windows_icon_source}" "${windows_icon_target}"
    staged_windows_icon="${windows_icon_target}"
  fi
}

cleanup_windows_icon() {
  if [ -n "${staged_windows_icon}" ]; then
    rm -f "${staged_windows_icon}"
    staged_windows_icon=""
  fi
}

go_cmd="$(command -v go || true)"
if [ -n "${go_cmd}" ]; then
  detected_version="$(installed_go_version "${go_cmd}")"
  if ! go_version_ok "${detected_version:-0.0}"; then
    install_native_go
  fi
else
  install_native_go
fi

go_cmd="$(command -v go)"
mkdir -p "${output_root}/windows-amd64" "${output_root}/linux-amd64"
(
  cd "${script_dir}"
  trap cleanup_windows_icon EXIT
  stage_windows_icon
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 "${go_cmd}" build -mod=readonly -trimpath -buildvcs=false -ldflags="-s -w -X main.version=${version_value}" -o "${output_root}/windows-amd64/Agent.exe" ./cmd/agent
  cleanup_windows_icon
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${go_cmd}" build -mod=readonly -trimpath -buildvcs=false -ldflags="-s -w -X main.version=${version_value}" -o "${output_root}/linux-amd64/Agent" ./cmd/agent
)

printf 'Built Go Agent binaries under %s\n' "${output_root}"
