#!/bin/sh
set -eu

[ "$#" -eq 3 ] || {
    echo "usage: sign-manifest.sh MANIFEST SIGNATURE PUBLIC_KEY_HEX" >&2
    exit 2
}
: "${ZENFM_RELEASE_SIGNING_KEY:?Set ZENFM_RELEASE_SIGNING_KEY to the Ed25519 private-key file}"

manifest=$1
signature=$2
expected_public=$3

case "$expected_public" in
    *[!0-9a-fA-F]*|'') echo "release public key must be 64 hexadecimal characters" >&2; exit 2 ;;
esac
[ "${#expected_public}" -eq 64 ] || { echo "release public key must be 64 hexadecimal characters" >&2; exit 2; }
[ -f "$ZENFM_RELEASE_SIGNING_KEY" ] || { echo "release signing key does not exist" >&2; exit 2; }
command -v openssl >/dev/null 2>&1 || { echo "openssl is required to sign releases" >&2; exit 2; }

derived_public=$(openssl pkey -in "$ZENFM_RELEASE_SIGNING_KEY" -pubout -outform DER \
    | tail -c 32 | od -An -tx1 | tr -d ' \n')
[ "$derived_public" = "$(printf '%s' "$expected_public" | tr 'A-F' 'a-f')" ] || {
    echo "release private key does not match ZENFM_RELEASE_PUBLIC_KEY_HEX" >&2
    exit 2
}

temporary="$signature.tmp"
trap 'rm -f "$temporary.raw" "$temporary"' EXIT INT TERM
openssl pkeyutl -sign -rawin -inkey "$ZENFM_RELEASE_SIGNING_KEY" \
    -in "$manifest" -out "$temporary.raw"
openssl base64 -A -in "$temporary.raw" -out "$temporary"
printf '\n' >> "$temporary"
mv "$temporary" "$signature"
rm -f "$temporary.raw"
trap - EXIT INT TERM
