#!/bin/bash
set -e

cd "$(dirname "$0")/.."

VERSION="${1:-dev}"
RELEASE_DIR="release"

rm -rf "$RELEASE_DIR"
mkdir -p "$RELEASE_DIR"

platforms=(
    "linux/amd64"
    "linux/arm64"
    "linux/arm/7"
    "linux/arm/6"
    "windows/amd64"
    "windows/arm64"
    "darwin/amd64"
    "darwin/arm64"
)

echo "=== undertow $VERSION ==="
echo "Running tests..."
go test -race -count=1 ./internal/...
echo ""

for platform in "${platforms[@]}"; do
    IFS='/' read -r OS ARCH VARIANT <<< "$platform"

    SUFFIX=""
    [[ "$OS" == "windows" ]] && SUFFIX=".exe"

    LABEL="${OS}-${ARCH}"
    [[ -n "$VARIANT" ]] && LABEL="${LABEL}v${VARIANT}"

    FOLDER="undertow-${VERSION}-${LABEL}"
    OUT="$RELEASE_DIR/$FOLDER"
    mkdir -p "$OUT"

    echo "Building $LABEL..."

    export CGO_ENABLED=0 GOOS="$OS" GOARCH="$ARCH"
    [[ -n "$VARIANT" ]] && export GOARM="$VARIANT"

    go build -ldflags="-s -w" -trimpath -o "$OUT/client${SUFFIX}" ./cmd/client
    go build -ldflags="-s -w" -trimpath -o "$OUT/server${SUFFIX}" ./cmd/server
    go build -ldflags="-s -w" -trimpath -o "$OUT/admin${SUFFIX}" ./cmd/admin

    unset GOARM

    cp client_config.json.example server_config.json.example admin_config.json.example README.md "$OUT/"

    (cd "$RELEASE_DIR" && zip -qr "${FOLDER}.zip" "$FOLDER")
    rm -rf "$OUT"
done

echo ""
echo "Done:"
ls -lh "$RELEASE_DIR"
