#!/bin/sh

set -eu

REPO="${DUPEHOUND_REPO:-AxeForging/dupehound}"
VERSION="${DUPEHOUND_VERSION:-latest}"
if [ -n "${DUPEHOUND_INSTALL_DIR:-}" ]; then
	INSTALL_DIR="$DUPEHOUND_INSTALL_DIR"
elif [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
	INSTALL_DIR=/usr/local/bin
else
	INSTALL_DIR="${HOME:?HOME is required when /usr/local/bin is not writable}/.local/bin"
fi

die() {
	printf 'dupehound installer: %s\n' "$*" >&2
	exit 1
}

command -v curl >/dev/null 2>&1 || die "curl is required"
command -v tar >/dev/null 2>&1 || die "tar is required"

case "$(uname -s)" in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) die "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	i386 | i486 | i586 | i686) arch=386 ;;
	armv6l | armv7l) arch=arm ;;
	*) die "unsupported architecture: $(uname -m)" ;;
esac

asset="dupehound-${os}-${arch}.tar.gz"
if [ "$VERSION" = latest ]; then
	base_url="https://github.com/${REPO}/releases/latest/download"
else
	base_url="https://github.com/${REPO}/releases/download/${VERSION}"
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

curl -fsSL "${base_url}/${asset}" -o "${tmp_dir}/${asset}"
curl -fsSL "${base_url}/checksums.txt" -o "${tmp_dir}/checksums.txt"

expected="$(awk -v asset="$asset" '$2 == asset || $2 == "*" asset { print $1; exit }' "${tmp_dir}/checksums.txt")"
[ -n "$expected" ] || die "checksum for ${asset} not found"

if command -v sha256sum >/dev/null 2>&1; then
	actual="$(sha256sum "${tmp_dir}/${asset}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
	actual="$(shasum -a 256 "${tmp_dir}/${asset}" | awk '{print $1}')"
else
	die "sha256sum or shasum is required"
fi
[ "$actual" = "$expected" ] || die "checksum verification failed for ${asset}"

tar -xzf "${tmp_dir}/${asset}" -C "$tmp_dir"
binary="${tmp_dir}/dupehound-${os}-${arch}"
[ -f "$binary" ] || die "release archive does not contain dupehound-${os}-${arch}"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$binary" "${INSTALL_DIR}/dupehound"
printf 'dupehound installed to %s/dupehound\n' "$INSTALL_DIR"
case ":${PATH}:" in
	*":${INSTALL_DIR}:"*) ;;
	*) printf 'Add %s to PATH to run dupehound from any directory.\n' "$INSTALL_DIR" ;;
esac
