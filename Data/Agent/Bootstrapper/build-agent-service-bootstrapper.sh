#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "${script_dir}/../../.." && pwd)"
output_dir="${script_dir}/dist"
primary_output_path="${output_dir}/Agent_Service_Bootstrapper.exe"
fallback_output_path="${script_dir}/Agent_Service_Bootstrapper.exe"
output_path="${BOREALIS_AGENT_SERVICE_BOOTSTRAPPER_OUTPUT_PATH:-${fallback_output_path}}"
minimum_go_major=1
minimum_go_minor=22
go_version="${BOREALIS_GO_VERSION:-1.22.12}"
go_install_root="${BOREALIS_GO_INSTALL_ROOT:-${repo_root}/Dependencies/Go/go${go_version}}"

mkdir -p "${output_dir}"
if [ "${output_path}" = "${primary_output_path}" ] && [ ! -w "${output_dir}" ]; then
  printf 'Output directory not writable: %s\n' "${output_dir}" >&2
  exit 1
fi
output_parent="$(dirname -- "${output_path}")"
mkdir -p "${output_parent}"
if [ ! -w "${output_parent}" ]; then
  printf 'Output directory not writable: %s\n' "${output_parent}" >&2
  exit 1
fi

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
    *)
      printf 'Unsupported Go install architecture: %s\n' "$(uname -m)" >&2
      exit 127
      ;;
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
    printf 'Native Go %s found, but Go %s.%s+ required.\n' "${detected_version:-unknown}" "${minimum_go_major}" "${minimum_go_minor}"
    install_native_go
  fi
else
  install_native_go
fi

go_cmd="$(command -v go)"

(
  cd "${script_dir}"
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 "${go_cmd}" mod tidy
  rm -f "${output_path}"
  GOOS=windows GOARCH=amd64 CGO_ENABLED=0 "${go_cmd}" build -trimpath -ldflags='-s -w' -o "${output_path}" .
)

printf 'Built %s\n' "${output_path}"
