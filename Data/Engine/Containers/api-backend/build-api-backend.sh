#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "${script_dir}/../../../.." && pwd)"
minimum_go_major=1
minimum_go_minor=23
go_version="${BOREALIS_GO_VERSION:-1.23.12}"
go_install_root="${BOREALIS_GO_INSTALL_ROOT:-${repo_root}/Dependencies/Go/go${go_version}}"
version_value="${BOREALIS_API_BACKEND_VERSION:-$(git -C "${repo_root}" rev-parse HEAD 2>/dev/null || echo dev)}"
output_root="${BOREALIS_GO_API_BACKEND_OUTPUT_ROOT:-${script_dir}/dist}"

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
  mkdir -p "${unpack_dir}" "$(dirname -- "${go_install_root}")"
  tar -C "${unpack_dir}" -xzf "${archive_path}"
  rm -rf "${go_install_root}"
  mv "${unpack_dir}/go" "${go_install_root}"
  export PATH="${go_install_root}/bin:${PATH}"
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
mkdir -p "${output_root}"
(
  cd "${script_dir}"
  "${go_cmd}" mod tidy
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${go_cmd}" build -trimpath -buildvcs=false -ldflags="-s -w -X main.version=${version_value}" -o "${output_root}/api-backend" ./cmd/api-backend
)

printf 'Built Go api-backend binary at %s\n' "${output_root}/api-backend"
