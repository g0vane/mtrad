#!/usr/bin/env bash
# Gera o binário do mtrad: um único arquivo estático, sem dependências.
#   ./build.sh              só para esta máquina  -> dist/mtrad
#   ./build.sh all          Linux, macOS e Windows -> dist/mtrad-<sistema>-<arquitetura>
# A versão vem de VERSION (ambiente) ou da data de hoje.
set -euo pipefail
cd "$(dirname "$0")"
VERSION="${VERSION:-0.9 ($(date +%d/%m/%Y))}"
export CGO_ENABLED=0
FLAGS=(-trimpath -ldflags "-s -w -X 'main.toolVersion=${VERSION}'")
mkdir -p dist
if [ "${1:-}" = "all" ]; then
  for t in linux/amd64 linux/arm64 linux/riscv64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
    os=${t%/*}; arch=${t#*/}; ext=""; [ "$os" = windows ] && ext=".exe"
    GOOS=$os GOARCH=$arch go build "${FLAGS[@]}" -o "dist/mtrad-$os-$arch$ext" ./cmd/mtrad
    echo "ok dist/mtrad-$os-$arch$ext"
  done
  (cd dist && sha256sum mtrad-* > SHA256SUMS)
else
  go build "${FLAGS[@]}" -o dist/mtrad ./cmd/mtrad
  echo "ok dist/mtrad"
fi
