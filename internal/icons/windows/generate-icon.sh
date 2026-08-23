#!/usr/bin/env bash
# Regenerate the Windows multi-resolution .ico from the app logo. Runs via
# `go generate ./...`. Prefers the raster source (lildisc_logo.png) when
# available so the conversion is a straight PNG -> ICO multi-resize, which
# only needs imagemagick. Falls back to the SVG source via inkscape +
# imagemagick for the nix-shell path used in CI.
#
# If neither toolchain is available, the script exits 0 with a message so a
# local `go generate` on a minimal dev machine doesn't fail the build. CI
# (which has nix tooling) will still regenerate the .ico on its own runs.

set -e

# go generate runs this with CWD = internal/icons/ (the directory of the
# Go source with the //go:generate directive). When run manually, chdir
# to the same place so relative paths below resolve identically.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR/.."

pngInput="./lildisc_logo.png"
svgInput="./hicolor/scalable/apps/io.github.dijama.lildisc.svg"
icoOutput="./windows/lildisc.ico"
hashFile="./windows/io.github.dijama.lildisc.svg.sha256"

# Hash the primary source of truth so we only regenerate on real changes.
if [[ -f "$pngInput" ]]; then
    sourceHash=$(sha256sum "$pngInput" | cut -d' ' -f1)
else
    sourceHash=$(sha256sum "$svgInput" | cut -d' ' -f1)
fi

if [[ -f "$hashFile" && "$(< "$hashFile")" == "$sourceHash" ]]; then
    exit 0
fi

have() { command -v "$1" >/dev/null 2>&1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if have magick; then
    convertCmd=(magick)
elif have convert; then
    convertCmd=(convert)
else
    echo "generate-icon.sh: imagemagick not found; skipping .ico regeneration."
    echo "generate-icon.sh: install imagemagick (or run via nix-shell) to refresh windows/lildisc.ico."
    exit 0
fi

if [[ -f "$pngInput" ]]; then
    "${convertCmd[@]}" "$pngInput" \
        -background none \
        -define icon:auto-resize=256,128,96,64,48,32,16 \
        -colors 256 \
        "$tmp/logo.ico"
elif have inkscape; then
    inkscape -w 256 -h 256 -o "$tmp/logo.png" "$svgInput"
    "${convertCmd[@]}" "$tmp/logo.png" \
        -define icon:auto-resize=256,128,96,64,48,32,16 \
        -colors 256 \
        "$tmp/logo.ico"
else
    echo "generate-icon.sh: neither $pngInput nor inkscape is available; skipping .ico regeneration."
    exit 0
fi

mv "$tmp/logo.ico" "$icoOutput"
echo "$sourceHash" > "$hashFile"
echo "generate-icon.sh: regenerated $icoOutput"
