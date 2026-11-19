#!/bin/sh
# Install the haiven CLI on Linux or macOS from a GitHub release.
#
#   curl -fsSL <haiven>/install.sh | sh
#   curl -fsSL <haiven>/install.sh | sh -s -- --version 1.2.3
#
# Options (flags win over environment):
#   --version X.Y.Z   HAIVEN_VERSION        release to install (default: latest)
#   --dir PATH        HAIVEN_INSTALL_DIR    install directory (default: $HOME/.local/bin)
#                     HAIVEN_DOWNLOAD_BASE  release mirror (default: GitHub releases)
#
# The archive is verified against the release's checksums.txt before anything
# is installed. Windows: use install.ps1.
set -eu

repo="haiven-dev/haiven-cli"
version="${HAIVEN_VERSION:-}"
install_dir="${HAIVEN_INSTALL_DIR:-${HOME}/.local/bin}"
base="${HAIVEN_DOWNLOAD_BASE:-https://github.com/${repo}/releases/download}"

fail() {
	echo "haiven install: $*" >&2
	exit 1
}

while [ $# -gt 0 ]; do
	case "$1" in
	--version)
		[ $# -ge 2 ] || fail "--version needs a value"
		version="$2"
		shift 2
		;;
	--dir)
		[ $# -ge 2 ] || fail "--dir needs a value"
		install_dir="$2"
		shift 2
		;;
	-h | --help)
		sed -n '2,13p' "$0" 2>/dev/null || true
		exit 0
		;;
	*) fail "unknown argument: $1" ;;
	esac
done

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) fail "unsupported OS $(uname -s); on Windows use install.ps1" ;;
esac

case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) fail "unsupported architecture $(uname -m)" ;;
esac

if command -v curl >/dev/null 2>&1; then
	download() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	download() { wget -q -O "$2" "$1"; }
else
	fail "curl or wget is required"
fi

if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	fail "sha256sum or shasum is required to verify the download"
fi

if [ -z "$version" ]; then
	command -v curl >/dev/null 2>&1 || fail "pass --version when curl is unavailable"
	latest=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${repo}/releases/latest") ||
		fail "could not resolve the latest release; pass --version"
	version="${latest##*/v}"
fi
version="${version#v}"
case "$version" in
*[!0-9A-Za-z.+-]* | "") fail "invalid version: $version" ;;
esac

asset="haiven_${version}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d 2>/dev/null || mktemp -d -t haiven)
trap 'rm -rf "$tmp"' EXIT INT TERM

download "${base}/v${version}/checksums.txt" "$tmp/checksums.txt" ||
	fail "could not download checksums for haiven ${version}"
download "${base}/v${version}/${asset}" "$tmp/${asset}" ||
	fail "could not download ${asset}"

want=$(awk -v a="$asset" '$2 == a || $2 == "*" a { print $1; exit }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "checksums.txt lists no ${asset}"
got=$(sha256 "$tmp/${asset}")
[ "$got" = "$want" ] || fail "checksum mismatch for ${asset}; nothing was installed"

mkdir -p "$tmp/x"
tar -xzf "$tmp/${asset}" -C "$tmp/x"
bin=$(find "$tmp/x" -type f -name haiven | head -n 1)
[ -n "$bin" ] || fail "${asset} contains no haiven binary"

mkdir -p "$install_dir"
cp "$bin" "$install_dir/haiven.tmp"
chmod 0755 "$install_dir/haiven.tmp"
mv -f "$install_dir/haiven.tmp" "$install_dir/haiven"

echo "installed haiven ${version} to ${install_dir}/haiven"
case ":${PATH}:" in
*":${install_dir}:"*) ;;
*) echo "add ${install_dir} to PATH to run haiven" ;;
esac
