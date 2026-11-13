#!/bin/sh
# Sign the development app with a stable, local self-signed identity.
#
# Ad-hoc signing ("codesign -s -") pins the designated requirement to the
# binary's cdhash, so every rebuild invalidates Accessibility/Input Monitoring
# grants. A self-signed certificate makes the requirement depend on the
# certificate hash instead, which is stable across rebuilds.
set -eu

APP="${1:?usage: sign-app.sh APP}"
IDENTITY="${OC_SIGN_IDENTITY:-Offline Complete Dev}"
KEYCHAIN="${OC_SIGN_KEYCHAIN:-$HOME/Library/Keychains/offline-complete-dev.keychain-db}"

if [ "$IDENTITY" = "-" ] || [ "$IDENTITY" = "adhoc" ]; then
  codesign --force --deep --sign - "$APP"
  exit 0
fi
SIGN_DIR="${OC_SIGN_DIR:-$HOME/Library/Application Support/offline-complete/signing}"
PASS_FILE="$SIGN_DIR/keychain-password"

# CI imports a prebuilt identity into a temporary keychain. Do not run the
# local certificate-generation path in that case.
if [ "${OC_SIGN_IMPORTED:-}" = "1" ]; then
  security unlock-keychain -p "$(cat "$PASS_FILE")" "$KEYCHAIN"
  codesign --force --deep --keychain "$KEYCHAIN" --sign "$IDENTITY" "$APP"
  exit 0
fi

if [ ! -d "$APP" ]; then
  echo "sign-app: missing app bundle: $APP" >&2
  exit 1
fi

mkdir -p "$SIGN_DIR"
chmod 0700 "$SIGN_DIR"

if [ -f "$KEYCHAIN" ] && [ ! -f "$PASS_FILE" ]; then
  echo "sign-app: $KEYCHAIN exists but $PASS_FILE is missing" >&2
  echo "sign-app: remove the keychain or restore its password file" >&2
  exit 1
fi

if [ ! -f "$PASS_FILE" ]; then
  umask 077
  openssl rand -hex 32 >"$PASS_FILE"
fi
chmod 0600 "$PASS_FILE"
PASS="$(cat "$PASS_FILE")"

if [ ! -f "$KEYCHAIN" ]; then
  security create-keychain -p "$PASS" "$KEYCHAIN"
fi
security set-keychain-settings -lut 21600 "$KEYCHAIN" >/dev/null 2>&1 || true
security unlock-keychain -p "$PASS" "$KEYCHAIN"

if ! security find-certificate -c "$IDENTITY" "$KEYCHAIN" >/dev/null 2>&1; then
  TMP="$(mktemp -d "${TMPDIR:-/tmp}/offline-complete-sign.XXXXXX")"
  trap 'rm -rf "$TMP"' EXIT INT TERM

  cat >"$TMP/req.cnf" <<EOF
[req]
distinguished_name=dn
x509_extensions=ext
prompt=no
[dn]
CN=$IDENTITY
O=Offline Complete
[ext]
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=codeSigning
subjectKeyIdentifier=hash
authorityKeyIdentifier=keyid,issuer
EOF

  if ! openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 3650 \
    -keyout "$TMP/key.pem" -out "$TMP/cert.pem" -config "$TMP/req.cnf" >/dev/null 2>&1; then
    echo "sign-app: failed to generate signing certificate" >&2
    exit 1
  fi
  if ! openssl pkcs12 -export \
    -inkey "$TMP/key.pem" -in "$TMP/cert.pem" \
    -out "$TMP/id.p12" -passout "pass:$PASS" >/dev/null 2>&1; then
    echo "sign-app: failed to package signing certificate" >&2
    exit 1
  fi

  if ! security import "$TMP/id.p12" -k "$KEYCHAIN" -P "$PASS" \
    -T /usr/bin/codesign -T /usr/bin/security >/dev/null; then
    echo "sign-app: failed to import signing identity" >&2
    exit 1
  fi
  if ! security set-key-partition-list \
    -S apple-tool:,apple:,codesign: -s -k "$PASS" "$KEYCHAIN" >/dev/null 2>&1; then
    echo "sign-app: failed to authorize codesign for the signing key" >&2
    exit 1
  fi

  rm -rf "$TMP"
  trap - EXIT INT TERM
fi

security unlock-keychain -p "$PASS" "$KEYCHAIN"
codesign --force --deep --keychain "$KEYCHAIN" --sign "$IDENTITY" "$APP"
