#!/usr/bin/env bash
#
# release.sh — build all platforms, package zips, and publish a GitHub release.
#
# Usage:
#   scripts/release.sh [version]
#
# Defaults:
#   version: v1.0.0
#
# Requirements:
#   - Go toolchain
#   - zip
#   - gh CLI authenticated for the repo (`gh auth login`)
#   - x86_64-w64-mingw32-gcc and aarch64-w64-mingw32-gcc for Windows tray
#     builds (brew install mingw-w64 llvm-mingw)
#
set -euo pipefail

VERSION="${1:-v1.0.0}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
RELEASE_DIR="$REPO_ROOT/release"

cd "$REPO_ROOT"

# ---------- pretty output ----------
if [[ -t 1 ]]; then
    BOLD=$'\e[1m'; GREEN=$'\e[32m'; YELLOW=$'\e[33m'
    RED=$'\e[31m'; CYAN=$'\e[36m'; RESET=$'\e[0m'
else
    BOLD=""; GREEN=""; YELLOW=""; RED=""; CYAN=""; RESET=""
fi
say()  { printf "%s==>%s %s\n" "$CYAN$BOLD" "$RESET" "$*"; }
ok()   { printf "  %s✓%s %s\n" "$GREEN" "$RESET" "$*"; }
warn() { printf "  %s!%s %s\n" "$YELLOW" "$RESET" "$*"; }
die()  { printf "%sERROR:%s %s\n" "$RED$BOLD" "$RESET" "$*" >&2; exit 1; }

# ---------- preflight ----------
say "Preflight"
command -v go  >/dev/null || die "go not found"
command -v zip >/dev/null || die "zip not found"
command -v gh  >/dev/null || die "gh CLI not found"
command -v git >/dev/null || die "git not found"
gh auth status >/dev/null 2>&1 || die "gh is not authenticated. Run: gh auth login"

REPO_SLUG="$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || true)"
[[ -n "$REPO_SLUG" ]] || die "Unable to determine GitHub repo from cwd"
ok "Repo:    $REPO_SLUG"
ok "Version: $VERSION"

if [[ -n "$(git status --porcelain)" ]]; then
    warn "Working tree has uncommitted changes"
fi

# ---------- build ----------
say "Building all platforms (delegating to scripts/build.sh)"
bash "$SCRIPT_DIR/build.sh" "$VERSION"

shopt -s nullglob
ZIPS=("$RELEASE_DIR"/*.zip)
shopt -u nullglob
[[ ${#ZIPS[@]} -gt 0 ]] || die "No zips produced in $RELEASE_DIR"
ok "${#ZIPS[@]} zip(s) built"

# ---------- checksums ----------
say "Generating checksums"
( cd "$RELEASE_DIR" && shasum -a 256 *.zip > SHA256SUMS )
ok "release/SHA256SUMS"

# ---------- tag ----------
say "Git tag"
if git rev-parse "$VERSION" >/dev/null 2>&1; then
    ok "Tag $VERSION already exists locally"
else
    git tag -a "$VERSION" -m "Release $VERSION"
    ok "Created tag $VERSION"
fi

if git ls-remote --tags origin "$VERSION" | grep -q "$VERSION"; then
    ok "Tag $VERSION already pushed"
else
    git push origin "$VERSION"
    ok "Pushed tag $VERSION"
fi

# ---------- release notes ----------
NOTES_FILE="$(mktemp)"
trap 'rm -f "$NOTES_FILE"' EXIT

if [[ -f "$REPO_ROOT/RELEASE.md" ]]; then
    ok "Using RELEASE.md for release notes"
    cat "$REPO_ROOT/RELEASE.md" > "$NOTES_FILE"
    {
        printf "\n\n---\n\n### Checksums\n\n\`\`\`\n"
        cat "$RELEASE_DIR/SHA256SUMS"
        printf "\`\`\`\n"
    } >> "$NOTES_FILE"
else
    warn "No RELEASE.md found — using minimal auto-generated notes"
    {
        printf "## Undertow %s\n\n" "$VERSION"
        printf "Cross-platform SOCKS5 tunnel — release artifacts for all supported platforms.\n\n"
        printf "### Checksums\n\n\`\`\`\n"
        cat "$RELEASE_DIR/SHA256SUMS"
        printf "\`\`\`\n"
    } > "$NOTES_FILE"
fi

# ---------- publish ----------
say "Publishing GitHub release"
if gh release view "$VERSION" >/dev/null 2>&1; then
    warn "Release $VERSION already exists — updating notes and assets"
    gh release edit "$VERSION" --title "Undertow $VERSION" --notes-file "$NOTES_FILE"
    gh release upload "$VERSION" "${ZIPS[@]}" "$RELEASE_DIR/SHA256SUMS" --clobber
else
    gh release create "$VERSION" \
        --title "Undertow $VERSION" \
        --notes-file "$NOTES_FILE" \
        "${ZIPS[@]}" \
        "$RELEASE_DIR/SHA256SUMS"
fi

URL="$(gh release view "$VERSION" --json url -q .url)"
ok "Released: $URL"
