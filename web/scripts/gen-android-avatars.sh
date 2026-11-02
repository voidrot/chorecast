#!/usr/bin/env bash
# Renders the bundled avatars (web/public/avatars/*.svg) to PNGs for the Android app, which has no SVG
# support. Needs rsvg-convert (librsvg). Run after `mise run gen:avatars`; the output is committed.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
out="$root/android/app/src/main/res/drawable-nodpi"
mkdir -p "$out"
for svg in "$root"/web/public/avatars/pixel-*.svg; do
  id="$(basename "$svg" .svg)"
  rsvg-convert -w 192 -h 192 "$svg" -o "$out/avatar_${id//-/_}.png"
done
echo "wrote $(ls "$out"/avatar_pixel_*.png | wc -l) avatars"
