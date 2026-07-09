#!/bin/sh
# Canton DevKit installer
# Usage: curl -fsSL https://raw.githubusercontent.com/bitdynamics-ab/canton-devkit/main/install.sh | sh
#
# Environment variables:
# VERSION - pin a specific version (e.g. VERSION=0.12.2)
# INSTALL_DIR - override install location (default: ~/.local/bin)

set -eu

REPO="bitdynamics-ab/canton-devkit"
GITHUB_API="https://api.github.com/repos/${REPO}/releases/latest"
GITHUB_DL="https://github.com/${REPO}/releases/download"
DEFAULT_INSTALL_DIR="${HOME}/.local/bin"

info() { printf '[canton-devkit] %s\n' "$*"; }
error() { printf '[canton-devkit] ERROR: %s\n' "$*" >&2; exit 1; }

fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO- "$1"
	else
		error "need 'curl' or 'wget' to download files"
	fi
}

detect_platform() {
	os="$(uname -s)"
	arch="$(uname -m)"

	case "${os}" in
	Darwin) os="darwin" ;;
	Linux) os="linux" ;;
	*) error "unsupported OS: ${os} (supported: macOS, Linux)" ;;
	esac

	case "${arch}" in
	arm64 | aarch64) arch="arm64" ;;
	x86_64 | amd64) arch="amd64" ;;
	*) error "unsupported architecture: ${arch} (supported: arm64, amd64)" ;;
	esac

	if [ "${os}" = "darwin" ] && [ "${arch}" != "arm64" ]; then
		error "macOS builds are only available for Apple Silicon (arm64), got: ${arch}"
	fi
	if [ "${os}" = "linux" ] && [ "${arch}" != "amd64" ]; then
		error "Linux builds are only available for x86_64 (amd64), got: ${arch}"
	fi

	PLATFORM="${os}_${arch}"
}

resolve_version() {
	if [ -n "${VERSION:-}" ]; then
		info "using pinned version: ${VERSION}"
		return
	fi

	info "fetching latest version from GitHub..."
	VERSION=$(
		fetch "${GITHUB_API}" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"v\{0,1\}\([^"]*\)".*/\1/p'
	)

	if [ -z "${VERSION}" ]; then
		error "could not determine latest version from GitHub API"
	fi

	info "latest version: ${VERSION}"
}

download_and_verify() {
	TARBALL="canton-devkit_v${VERSION}_${PLATFORM}.tar.gz"
	TARBALL_URL="${GITHUB_DL}/v${VERSION}/${TARBALL}"
	CHECKSUMS_URL="${GITHUB_DL}/v${VERSION}/SHA256SUMS"

	TMPDIR="$(mktemp -d)"
	trap 'rm -rf "${TMPDIR}"' EXIT

	info "downloading ${TARBALL}..."
	fetch "${TARBALL_URL}" >"${TMPDIR}/${TARBALL}" || error "download failed: ${TARBALL_URL}"

	info "downloading SHA256SUMS..."
	fetch "${CHECKSUMS_URL}" >"${TMPDIR}/SHA256SUMS" || error "download failed: ${CHECKSUMS_URL}"

	info "verifying checksum..."
	EXPECTED=$(grep " ${TARBALL}\$" "${TMPDIR}/SHA256SUMS" | awk '{print $1}')
	if [ -z "${EXPECTED}" ]; then
		error "no checksum found for ${TARBALL} in SHA256SUMS"
	fi

	if command -v sha256sum >/dev/null 2>&1; then
		ACTUAL=$(sha256sum "${TMPDIR}/${TARBALL}" | awk '{print $1}')
	elif command -v shasum >/dev/null 2>&1; then
		ACTUAL=$(shasum -a 256 "${TMPDIR}/${TARBALL}" | awk '{print $1}')
	else
		error "need 'sha256sum' or 'shasum' to verify download"
	fi

	if [ "${EXPECTED}" != "${ACTUAL}" ]; then
		error "checksum mismatch!\n expected: ${EXPECTED}\n actual: ${ACTUAL}"
	fi
	info "checksum verified."
}

install_binary() {
	INSTALL_DIR="${INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}"

	mkdir -p "${INSTALL_DIR}"

	info "extracting canton-devkit to ${INSTALL_DIR}..."
	tar -xzf "${TMPDIR}/${TARBALL}" -C "${TMPDIR}"
	mv "${TMPDIR}/canton-devkit" "${INSTALL_DIR}/canton-devkit"
	chmod +x "${INSTALL_DIR}/canton-devkit"

	info "installed canton-devkit v${VERSION} to ${INSTALL_DIR}/canton-devkit"

	case ":${PATH}:" in
	*":${INSTALL_DIR}:"*) ;;
	*)
		printf '\n'
		info "WARNING: ${INSTALL_DIR} is not in your PATH."
		info "Add this to your shell profile (~/.bashrc, ~/.zshrc, etc.):"
		printf '\n export PATH="%s:$PATH"\n\n' "${INSTALL_DIR}"
		;;
	esac
}

main() {
	info "Canton DevKit Installer"
	printf '\n'

	detect_platform
	resolve_version
	download_and_verify
	install_binary

	printf '\n'
	info "Done. Run 'canton-devkit --help' to get started."
}

main
