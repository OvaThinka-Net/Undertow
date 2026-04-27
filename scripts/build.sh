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

    go build -ldflags="-s -w -X main.Version=${VERSION}" -trimpath -o "$OUT/client${SUFFIX}" ./cmd/client
    go build -ldflags="-s -w -X main.Version=${VERSION}" -trimpath -o "$OUT/server${SUFFIX}" ./cmd/server
    go build -ldflags="-s -w -X main.Version=${VERSION}" -trimpath -o "$OUT/admin${SUFFIX}" ./cmd/admin

    unset GOARM

    cp client_config.json.example server_config.json.example admin_config.json.example README.md "$OUT/"
    cp install.sh uninstall.sh undertow.service "$OUT/"

    # Build client binaries for all platforms into clients/ subdirectory
    mkdir -p "$OUT/clients"
    for cp_platform in "darwin/arm64" "darwin/amd64" "windows/amd64" "windows/arm64" "linux/amd64" "linux/arm64"; do
        IFS='/' read -r CP_OS CP_ARCH <<< "$cp_platform"
        CP_SUFFIX=""
        [[ "$CP_OS" == "windows" ]] && CP_SUFFIX=".exe"
        if [[ "$CP_OS" == "darwin" || "$CP_OS" == "windows" ]]; then
            CP_NAME="Undertow-${CP_OS}-${CP_ARCH}${CP_SUFFIX}"
        else
            CP_NAME="client-${CP_OS}-${CP_ARCH}"
        fi
        CGO_ENABLED=0 GOOS="$CP_OS" GOARCH="$CP_ARCH" \
            go build -ldflags="-s -w -X main.Version=${VERSION}" -trimpath -o "$OUT/clients/$CP_NAME" ./cmd/client
    done

    # Build tray app (GUI) for macOS only (requires CGO for systray)
    # Windows GUI requires building on Windows; not cross-compilable from macOS
    for tp_platform in "darwin/arm64" "darwin/amd64"; do
        IFS='/' read -r TP_OS TP_ARCH <<< "$tp_platform"
        TP_NAME="Undertow-GUI-${TP_OS}-${TP_ARCH}"
        CGO_ENABLED=1 GOOS="$TP_OS" GOARCH="$TP_ARCH" \
            go build -ldflags="-s -w" -trimpath -o "$OUT/clients/$TP_NAME" ./cmd/tray
    done

    # Restore outer loop env
    export CGO_ENABLED=0 GOOS="$OS" GOARCH="$ARCH"

    (cd "$RELEASE_DIR" && zip -qr "${FOLDER}.zip" "$FOLDER")
    rm -rf "$OUT"
done

echo ""
echo "Done:"
ls -lh "$RELEASE_DIR"
