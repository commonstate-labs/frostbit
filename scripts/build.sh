#!/usr/bin/env bash
                                                                          
                                    
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

COMMIT="$(git rev-parse --short HEAD 2>/dev/null || true)"
TIME="$(git show -s --format=%cI HEAD 2>/dev/null || true)"
COMMIT="${COMMIT:-unknown}"
TIME="${TIME:-unknown}"
TAG="${1:-dev}"

LDFLAGS="-X main.BuildCommit=${COMMIT} -X main.BuildTime=${TIME} -X main.BuildTag=${TAG}"

mkdir -p bin

echo "==> building node commit=${COMMIT} time=${TIME} tag=${TAG}"
go build -trimpath -buildvcs=false -ldflags "${LDFLAGS}" -o bin/node ./cmd/main

echo "==> building explorer"
go build -trimpath -buildvcs=false -ldflags "${LDFLAGS}" -o bin/explorer ./cmd/explorer

echo "==> building node-key"
go build -trimpath -buildvcs=false -o bin/node-key ./cmd/node-key

echo "==> done: bin/node bin/explorer bin/node-key"
