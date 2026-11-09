#!/usr/bin/env bash
                                                                               
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

mapfile -t GO_PACKAGES < <(go list -buildvcs=false ./... | awk '$0 != "dex/tmp"')
mapfile -t GO_BUILD_PACKAGES < <(go list -buildvcs=false -f '{{if or .GoFiles .CgoFiles}}{{.ImportPath}}{{end}}' ./... | awk 'NF && $0 != "dex/tmp"')
if (( ${                                                               
  echo "FAILED: no Go packages discovered" >&2
  exit 1
fi

echo "==> public config credential-field check"
if grep -Eirn --include='*.json' --include='*.yml' --include='*.yaml' '"[^"[:cntrl:]]*(private[_-]?key|privateKey|mnemonic|seed[_-]?phrase|password|api[_-]?token|access[_-]?token)[^"[:cntrl:]]*"[[:space:]]*:' config deploy; then
  echo "FAILED: public config contains a secret-bearing field name" >&2
  exit 1
fi
if find config deploy -type f \( -iname '*.key' -o -iname '*.pem' -o -iname '*.p12' -o -iname '*.pfx' -o -iname '*.keystore' \) -print -quit | grep -q .; then
  echo "FAILED: public config contains a secret-bearing file type" >&2
  exit 1
fi

echo "==> go build"
go build -buildvcs=false "${GO_BUILD_PACKAGES[@]}"

echo "==> go test"
go test -buildvcs=false "${GO_PACKAGES[@]}" -count=1 -short
go test -buildvcs=false ./keys/... -run Registry -count=1
go test -buildvcs=false ./deploy ./cmd/release -count=1

echo "==> go vet"
go vet -buildvcs=false "${GO_PACKAGES[@]}"

echo "==> safe-math lint"
bash scripts/lint_safe_math.sh

echo "==> deterministic node and explorer build"
bash scripts/build.sh verify

echo "==> explorer"
npm --prefix explorer ci --no-audit --no-fund
npm --prefix explorer test
npm --prefix explorer run build

echo "==> wallet"
npm --prefix wallet ci --no-audit --no-fund
npm --prefix wallet test
npm --prefix wallet run build

echo "==> generated protobuf check"
GENERATED_PROTO="$(mktemp)"
trap 'rm -f "$GENERATED_PROTO"' EXIT
(cd wallet && npx --no-install pbjs -t static-module -w es6 --es6 -o "$GENERATED_PROTO" ../pb/data.proto)
cmp "$GENERATED_PROTO" wallet/lib/data_proto.js
grep -Fq '// source: pb/data.proto' pb/data.pb.go

echo "==> public verification passed"
