#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "${script_dir}/../../../.." && pwd)"
minimum_go_major=1
minimum_go_minor=25
go_version="${BOREALIS_GO_VERSION:-1.25.12}"
go_linux_amd64_sha256="234828b7a89e0e303d2556310ee549fbcf253d28de937bac3da13d6294262ac1"
go_linux_arm64_sha256="8b5884aef89600aef5b0b051fb971f11f49bb996521e911f30f02a66884f7bd2"
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

go_archive_sha256() {
  case "${go_version}:${go_arch}" in
    1.25.12:amd64) printf '%s\n' "${go_linux_amd64_sha256}" ;;
    1.25.12:arm64) printf '%s\n' "${go_linux_arm64_sha256}" ;;
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
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${go_cmd}" build -mod=readonly -trimpath -buildvcs=false -ldflags="-s -w -X main.version=${version_value}" -o "${output_root}/api-backend" ./cmd/api-backend
  cp "${output_root}/api-backend" "${output_root}/borealis-cluster-controller"
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${go_cmd}" build -mod=readonly -trimpath -buildvcs=false -o "${output_root}/wireguard-control" ./cmd/wireguard-control
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${go_cmd}" build -mod=readonly -trimpath -buildvcs=false -o "${output_root}/wireguard-control-client" ./cmd/wireguard-control-client
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${go_cmd}" build -mod=readonly -trimpath -buildvcs=false -o "${output_root}/wireguard-route-daemon" ./cmd/wireguard-route-daemon
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 "${go_cmd}" build -mod=readonly -trimpath -buildvcs=false -o "${output_root}/borealis-node-manager" ./cmd/borealis-node-manager
)

printf 'Built Go Engine binaries under %s\n' "${output_root}"
