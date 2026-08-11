#!/bin/sh
set -eu

ROOT=$1
TMP=$(mktemp -d "${TMPDIR:-/tmp}/zenfm-package.XXXXXX")
trap 'rm -rf "$TMP"' EXIT INT TERM
mkdir -p "$TMP/bin"
for binary in zenfm-hf zenfm-sf zenfm-linux-arm64 zenfm-linux-amd64 zenfm-darwin; do
    printf '#!/bin/sh\nexit 0\n' > "$TMP/bin/$binary"
    chmod +x "$TMP/bin/$binary"
done
printf 'fake apk\n' > "$TMP/ZenFM.apk"

if ZENFM_RELEASE_PUBLIC_KEY_HEX=invalid "$ROOT/build.sh" --package-only "$TMP/bin" --apk "$TMP/ZenFM.apk" >"$TMP/invalid-key.log" 2>&1; then
    echo "release packaging accepted an invalid signing key" >&2
    exit 1
fi
grep -q 'ZENFM_RELEASE_PUBLIC_KEY_HEX' "$TMP/invalid-key.log"

if "$ROOT/build.sh" --package-only --dev >"$TMP/missing-package-dir.log" 2>&1; then
    echo "package-only accepted a missing binary directory" >&2
    exit 1
fi
grep -q '^usage:' "$TMP/missing-package-dir.log"

ZENFM_RELEASE_PUBLIC_KEY_HEX= \
    "$ROOT/build.sh" --package-only "$TMP/bin" --apk "$TMP/ZenFM.apk"
VERSION=$(sed -n '1p' "$ROOT/VERSION")

for platform in ereader linux macos android; do
    archive="$ROOT/dist/ZenFM-koreader-$platform-$VERSION.zip"
    [ -f "$archive" ]
    unzip -Z1 "$archive" | grep -q '^zenfm\.koplugin/main\.lua$'
    unzip -Z1 "$archive" | grep -q '^zenfm\.koplugin/LICENSE$'
    unzip -Z1 "$archive" | grep -q '^zenfm\.koplugin/THIRD_PARTY_NOTICES\.md$'
    if unzip -Z1 "$archive" | grep -Eq '(^|/)(tests?|\.git)(/|$)|\.py$'; then
        echo "source-only file leaked into $archive" >&2
        exit 1
    fi
done

unzip -Z1 "$ROOT/dist/ZenFM-koreader-ereader-$VERSION.zip" | grep -q 'backend/zenfm-hf$'
unzip -Z1 "$ROOT/dist/ZenFM-koreader-ereader-$VERSION.zip" | grep -q 'backend/zenfm-sf$'
for module in control daemon release_public_key settings signature updater util; do
    unzip -Z1 "$ROOT/dist/ZenFM-koreader-ereader-$VERSION.zip" | grep -q "zenfm.koplugin/zenfm_$module.lua$"
done
if unzip -Z1 "$ROOT/dist/ZenFM-koreader-ereader-$VERSION.zip" \
    | grep -Eq 'zenfm\.koplugin/(control|daemon|release_public_key|settings|signature|updater|util)\.lua$'; then
    echo "generic Lua module name leaked into the e-reader bundle" >&2
    exit 1
fi
unzip -Z1 "$ROOT/dist/ZenFM-koreader-linux-$VERSION.zip" | grep -q 'backend/zenfm-linux-arm64$'
unzip -Z1 "$ROOT/dist/ZenFM-koreader-linux-$VERSION.zip" | grep -q 'backend/zenfm-linux-amd64$'
unzip -Z1 "$ROOT/dist/ZenFM-koreader-macos-$VERSION.zip" | grep -q 'backend/zenfm-darwin$'
if unzip -Z1 "$ROOT/dist/ZenFM-koreader-android-$VERSION.zip" | grep -q 'backend/zenfm-'; then
    echo "Android plugin unexpectedly contains a native server" >&2
    exit 1
fi
[ -f "$ROOT/dist/ZenFM-android-$VERSION.apk" ]
[ -f "$ROOT/dist/ZenFM-release-manifest-$VERSION.txt" ]
[ ! -f "$ROOT/dist/ZenFM-release-manifest-$VERSION.sig" ]
grep -q '^zenfm-release-manifest-v1$' "$ROOT/dist/ZenFM-release-manifest-$VERSION.txt"
unzip -p "$ROOT/dist/ZenFM-koreader-linux-$VERSION.zip" zenfm.koplugin/zenfm_release_public_key.lua | grep -q 'UNCONFIGURED'

mkdir -p "$TMP/ereader-bin"
cp "$TMP/bin/zenfm-hf" "$TMP/bin/zenfm-sf" "$TMP/ereader-bin/"
ZENFM_RELEASE_PUBLIC_KEY_HEX=invalid \
    "$ROOT/build.sh" --package-only "$TMP/ereader-bin" --dev
unzip -Z1 "$ROOT/dist/ZenFM-koreader-ereader-$VERSION.zip" | grep -q 'backend/zenfm-hf$'
unzip -Z1 "$ROOT/dist/ZenFM-koreader-ereader-$VERSION.zip" | grep -q 'backend/zenfm-sf$'

command -v openssl >/dev/null 2>&1 || { echo "openssl is required for release-signing test" >&2; exit 1; }
openssl genpkey -algorithm ED25519 -out "$TMP/release-key.pem" >/dev/null 2>&1
openssl pkey -in "$TMP/release-key.pem" -pubout -out "$TMP/release-public.pem"
PUBLIC_KEY=$(openssl pkey -in "$TMP/release-key.pem" -pubout -outform DER \
    | tail -c 32 | od -An -tx1 | tr -d ' \n')
ZENFM_RELEASE_PUBLIC_KEY_HEX="$PUBLIC_KEY" ZENFM_RELEASE_SIGNING_KEY="$TMP/release-key.pem" \
    "$ROOT/build.sh" --package-only "$TMP/bin" --apk "$TMP/ZenFM.apk"
[ -f "$ROOT/dist/ZenFM-release-manifest-$VERSION.sig" ]
openssl base64 -d -A -in "$ROOT/dist/ZenFM-release-manifest-$VERSION.sig" -out "$TMP/manifest.sig"
openssl pkeyutl -verify -rawin -pubin -inkey "$TMP/release-public.pem" \
    -in "$ROOT/dist/ZenFM-release-manifest-$VERSION.txt" -sigfile "$TMP/manifest.sig" >/dev/null
unzip -p "$ROOT/dist/ZenFM-koreader-linux-$VERSION.zip" zenfm.koplugin/zenfm_release_public_key.lua | grep -q "$PUBLIC_KEY"
echo "ok - four KOReader bundles and Android package layout"
