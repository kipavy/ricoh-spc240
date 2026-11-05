#!/usr/bin/env bash
#
# fetch-ricoh-assets.sh - download Ricoh's official macOS driver and extract
# the halftone screens and the ICC profile the print pipeline needs.
#
# Why: these files are proprietary Ricoh data. They are never stored in this
# repository; they are downloaded from Ricoh at Docker image build time only.
#
# What: downloads the driver .dmg, verifies its SHA-256, extracts
#   dmg -> "DDST Driver/Ricoh Aficio SP C240DN.pkg" (xar)
#       -> Ricoh_Aficio_SP_C240DN.pkg/Payload (gzip + cpio)
# and installs only:
#   DESTDIR/screens/RPSC4S{C,M,Y,K}{1,2,4}_Mac.bin   (12 files, sizes checked)
#   DESTDIR/ricoh-c240dn.icc
#
# Usage: fetch-ricoh-assets.sh DESTDIR
# Requires: curl sha256sum 7z gzip cpio

set -euo pipefail

URL="https://support.ricoh.com/bb/pub_e/dr_ut_e/0001323/0001323934/V205/r93503Lf.dmg"
SHA256="18837ae59df5c5ae2ccbc003ed71ce665cfe4de0040ce96b9d4092d77ce6ab4e"
DMG="r93503Lf.dmg"
PKG_IN_DMG="DDST Driver/Ricoh Aficio SP C240DN.pkg"
PAYLOAD_IN_PKG="Ricoh_Aficio_SP_C240DN.pkg/Payload"
RES="Library/Printers/Ricoh/Filters/RicohAficioSPC240DNFilter.app/Contents/Resources"
ICC_NAME="RICOH Aficio SP C240DN CS.icc"

# Expected screen sizes in bytes (guards against a silent Ricoh re-release).
SCREEN_SIZES="
RPSC4SC1_Mac.bin 819214
RPSC4SM1_Mac.bin 819214
RPSC4SY1_Mac.bin 32780
RPSC4SK1_Mac.bin 294924
RPSC4SC2_Mac.bin 65548
RPSC4SM2_Mac.bin 65548
RPSC4SY2_Mac.bin 16396
RPSC4SK2_Mac.bin 147468
RPSC4SC4_Mac.bin 131084
RPSC4SM4_Mac.bin 131084
RPSC4SY4_Mac.bin 32780
RPSC4SK4_Mac.bin 73740
"

die() { echo "fetch-ricoh-assets: error: $*" >&2; exit 1; }

if [ "$#" -ne 1 ] || [ -z "$1" ]; then
  echo "usage: $0 DESTDIR" >&2
  exit 2
fi
DEST=$1

missing=""
for tool in curl sha256sum 7z gzip cpio; do
  command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done
[ -z "$missing" ] || die "missing required tools:$missing"

mkdir -p "$DEST/screens"
DEST=$(cd "$DEST" && pwd)

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

echo "Downloading $URL"
curl -fsSL --retry 3 -o "$WORK/$DMG" "$URL" || die "download failed"

echo "$SHA256  $DMG" > "$WORK/$DMG.sha256"
(cd "$WORK" && sha256sum -c "$DMG.sha256") >&2 \
  || die "SHA-256 mismatch for $DMG (expected $SHA256); refusing to continue"

# 7z may exit non-zero on DMGs (trailing data / headers warnings): judge by output.
mkdir -p "$WORK/dmg" "$WORK/pkg" "$WORK/root"
(cd "$WORK/dmg" && 7z x -y "$WORK/$DMG" >/dev/null) || true
[ -f "$WORK/dmg/$PKG_IN_DMG" ] || die "extraction of $DMG failed: '$PKG_IN_DMG' not found"

(cd "$WORK/pkg" && 7z x -y "$WORK/dmg/$PKG_IN_DMG" >/dev/null) || true
[ -f "$WORK/pkg/$PAYLOAD_IN_PKG" ] || die "extraction of pkg failed: '$PAYLOAD_IN_PKG' not found"

(cd "$WORK/root" && gzip -dc "$WORK/pkg/$PAYLOAD_IN_PKG" | cpio -idm --quiet) \
  || die "unpacking Payload failed"

SRC="$WORK/root/$RES"
[ -f "$SRC/$ICC_NAME" ] || die "ICC profile '$ICC_NAME' not found in payload"

echo "$SCREEN_SIZES" | while read -r name size; do
  [ -n "$name" ] || continue
  f="$SRC/Screens/$name"
  [ -f "$f" ] || die "screen $name not found in payload"
  actual=$(wc -c < "$f" | tr -d ' ')
  [ "$actual" = "$size" ] || die "screen $name has size $actual, expected $size (Ricoh re-release?)"
done

echo "$SCREEN_SIZES" | while read -r name size; do
  [ -n "$name" ] || continue
  cp "$SRC/Screens/$name" "$DEST/screens/$name"
done
cp "$SRC/$ICC_NAME" "$DEST/ricoh-c240dn.icc"

echo "Installed:"
for f in "$DEST"/screens/*.bin "$DEST/ricoh-c240dn.icc"; do
  printf '  %s %s bytes\n' "$f" "$(wc -c < "$f" | tr -d ' ')"
done
