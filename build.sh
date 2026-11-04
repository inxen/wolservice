#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo 0.6.6)}"
LDFLAGS="-s -w -X wol/internal/version.Version=${VERSION}"

EMBEDDED_KEY="${WOL_EMBEDDED_KEY:-}"
if [ -z "${EMBEDDED_KEY}" ] && [ -f .wol-embedded-key ]; then
  EMBEDDED_KEY=$(tr -d '\r\n' < .wol-embedded-key)
fi
if [ -n "${EMBEDDED_KEY}" ]; then
  case "${EMBEDDED_KEY}" in
    *[!0-9A-Fa-f]*) echo "WOL_EMBEDDED_KEY must be 64 hex characters" >&2; exit 1 ;;
  esac
  if [ "${#EMBEDDED_KEY}" -ne 64 ]; then
    echo "WOL_EMBEDDED_KEY must be 64 hex characters" >&2
    exit 1
  fi
  LDFLAGS="${LDFLAGS} -X wol/internal/secrets.EmbeddedKey=${EMBEDDED_KEY}"
  echo "==> embedded secrets key: enabled"
else
  echo "==> WARNING: no embedded secrets key (set WOL_EMBEDDED_KEY or .wol-embedded-key); using key file fallback" >&2
fi

echo "==> building version ${VERSION}"
# dist 只留本次构建的产物：先整体清掉，旧版本不留。
rm -rf dist
mkdir -p dist

targets=(
  "windows amd64"
  "windows arm64"
  "linux amd64"
  "linux arm64"
  "linux arm"
  "linux mipsle"
  "darwin amd64"
  "darwin arm64"
)

for target in "${targets[@]}"; do
  read -r os arch <<< "${target}"
  ext=""
  if [ "${os}" = "windows" ]; then
    ext=".exe"
  fi
  output="dist/wol-${os}-${arch}${ext}"
  echo "==> ${os}/${arch} -> ${output}"
  CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" GOARM=7 GOMIPS=softfloat \
    go build -trimpath -ldflags "${LDFLAGS}" -o "${output}" ./cmd/wol
done

echo "==> done"
ls -lh dist
