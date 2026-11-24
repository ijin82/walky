#!/usr/bin/env bash
# Walky release packaging: fresh build, strip, archive, SHA-256 checksum.
#
#   ./package-release.sh            — use the version from VERSION
#   ./package-release.sh v0.2.0     — set this version in VERSION
#   VERSION=v0.2.0 ./package-release.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

APP_NAME="walky"
APP_ID="io.github.ijin.Walky"
DIST_DIR="${SCRIPT_DIR}/dist"

[ -f "Makefile" ] && [ -d "core" ] && [ -d "ui" ] || {
    echo "Error: run this script from a Walky source checkout." >&2
    exit 1
}

# --- version ---------------------------------------------------------------

VERSION_SPECIFIED=false
if [ -n "${1:-}" ]; then
    VERSION="$1"
    VERSION_SPECIFIED=true
elif [ -n "${VERSION:-}" ]; then
    VERSION_SPECIFIED=true
elif [ -f "VERSION" ]; then
    VERSION="$(xargs < VERSION)"
else
    read -r -p "Enter release version (e.g. 0.1.0): " VERSION
    VERSION_SPECIFIED=true
fi

VERSION="$(echo "${VERSION}" | xargs)"
[ -n "${VERSION}" ] || { echo "Error: release version cannot be empty." >&2; exit 1; }

case "${VERSION}" in
    v*) ;;
    *) VERSION="v${VERSION}" ;;
esac

if [[ ! "${VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]]; then
    echo "Error: '${VERSION}' is not a valid release version." >&2
    echo "       Expected a semantic version such as 0.1.0 or v0.2.0-rc1." >&2
    exit 1
fi

BARE_VERSION="${VERSION#v}"

if [ "${VERSION_SPECIFIED}" = true ]; then
    echo "==> Setting project version to ${BARE_VERSION}..."
    printf '%s\n' "${BARE_VERSION}" > VERSION
fi

ARCH="$(uname -m)"
RELEASE_TAG="${APP_NAME}-${VERSION}-linux-${ARCH}"
STAGE_DIR="${DIST_DIR}/${RELEASE_TAG}"
ARCHIVE_NAME="${RELEASE_TAG}.tar.gz"
ARCHIVE_PATH="${DIST_DIR}/${ARCHIVE_NAME}"

echo "=================================================="
echo " Preparing release package: ${RELEASE_TAG}"
echo "=================================================="

# --- 1. build --------------------------------------------------------------

echo "==> Building fresh binaries..."
make build

[ -x "bin/walky" ] || { echo "Error: bin/walky not found." >&2; exit 1; }
[ -x "bin/walky-ui" ] || { echo "Error: bin/walky-ui not found." >&2; exit 1; }

BUILT_VERSION="$(./bin/walky version 2>/dev/null || true)"
case "${BUILT_VERSION}" in
    "${BARE_VERSION}"*|"walky ${BARE_VERSION}"*) ;;
    *)
        echo "Error: bin/walky reports '${BUILT_VERSION}', expected '${BARE_VERSION}...'." >&2
        exit 1
        ;;
esac

# --- 2. staging ------------------------------------------------------------

echo "==> Staging release files..."
rm -rf "${STAGE_DIR}"
mkdir -p "${STAGE_DIR}/bin" "${STAGE_DIR}/data"

cp -p "bin/walky" "${STAGE_DIR}/bin/walky"
cp -p "bin/walky-ui" "${STAGE_DIR}/bin/walky-ui"

if command -v strip >/dev/null 2>&1; then
    strip --strip-unneeded "${STAGE_DIR}/bin/walky-ui" 2>/dev/null || true
fi

cp -p "install.sh" "${STAGE_DIR}/install.sh"
chmod 755 "${STAGE_DIR}/install.sh"

for f in \
    "data/${APP_ID}.desktop" \
    "data/walky.service" \
    "data/walky.svg" \
    "data/walky-symbolic.svg" \
    "data/walky-paused-symbolic.svg" \
    "data/walky-offline-symbolic.svg"
do
    cp -p "${f}" "${STAGE_DIR}/${f}"
done

for f in README.md LICENSE VERSION; do
    if [ -e "${f}" ]; then
        cp -p "${f}" "${STAGE_DIR}/${f}"
    fi
done

# --- 3. archive ------------------------------------------------------------

echo "==> Creating ${ARCHIVE_NAME}..."
mkdir -p "${DIST_DIR}"
tar -czf "${ARCHIVE_PATH}" -C "${DIST_DIR}" "${RELEASE_TAG}"

if command -v sha256sum >/dev/null 2>&1; then
    (cd "${DIST_DIR}" && sha256sum "${ARCHIVE_NAME}" > "${ARCHIVE_NAME}.sha256")
fi

rm -rf "${STAGE_DIR}"

# --- 4. summary ------------------------------------------------------------

echo ""
echo "=================================================="
echo "Release package ready."
echo "=================================================="
echo "  Archive:  ${ARCHIVE_PATH}"
echo "  Size:     $(du -h "${ARCHIVE_PATH}" | cut -f1)"
if [ -f "${ARCHIVE_PATH}.sha256" ]; then
    echo "  SHA256:   $(cut -d' ' -f1 < "${ARCHIVE_PATH}.sha256")"
fi
echo ""
echo "Archive contents:"
tar -ztvf "${ARCHIVE_PATH}"
echo ""
echo "To install from the archive:"
echo "  tar -xzf ${ARCHIVE_NAME}"
echo "  cd ${RELEASE_TAG}"
echo "  ./install.sh local"
echo "=================================================="
