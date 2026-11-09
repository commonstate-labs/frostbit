FROM golang:1.24 AS go-build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY blocktrace ./blocktrace
COPY certchain ./certchain
COPY cmd/main ./cmd/main
COPY cmd/explorer ./cmd/explorer
COPY cmd/miner-register ./cmd/miner-register
COPY config/*.go ./config/
COPY consensus ./consensus
COPY crt ./crt
COPY db ./db
COPY frost ./frost
COPY future ./future
COPY handlers ./handlers
COPY interfaces ./interfaces
COPY keys ./keys
COPY lifecycle ./lifecycle
COPY liveness ./liveness
COPY logs ./logs
COPY pb ./pb
COPY protocol ./protocol
COPY referral ./referral
COPY rent ./rent
COPY sender ./sender
COPY stateDB ./stateDB
COPY stats ./stats
COPY txpool ./txpool
COPY txverify ./txverify
COPY types ./types
COPY updater ./updater
COPY utils ./utils
COPY vm ./vm
COPY witness ./witness
ARG BUILD_TAG=dev
ARG BUILD_COMMIT=unknown
ARG BUILD_TIME=unknown
ARG BUILD_VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X main.BuildCommit=${BUILD_COMMIT} -X main.BuildTime=${BUILD_TIME} -X main.BuildTag=${BUILD_TAG} -X main.BuildVersion=${BUILD_VERSION}" -o /out/node ./cmd/main
RUN CGO_ENABLED=0 go build -ldflags "-X main.BuildCommit=${BUILD_COMMIT} -X main.BuildTime=${BUILD_TIME} -X main.BuildTag=${BUILD_TAG}" -o /out/explorer ./cmd/explorer
RUN CGO_ENABLED=0 go build -o /out/miner-register ./cmd/miner-register

FROM node:22-bookworm-slim AS explorer-build

WORKDIR /src/explorer
COPY explorer/package.json explorer/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY explorer/index.html explorer/style.css explorer/tsconfig.json explorer/vite.config.ts ./
COPY explorer/public ./public
COPY explorer/src ./src
RUN npm run build

FROM debian:bookworm-slim

WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
RUN install -d -o 65532 -g 65532 /app /data
COPY --chown=65532:65532 --chmod=0444 LICENSE /usr/share/licenses/frostbit-dex/LICENSE
COPY --chown=65532:65532 --chmod=0444 NOTICE /usr/share/licenses/frostbit-dex/NOTICE
RUN test -f /usr/share/licenses/frostbit-dex/LICENSE \
    && test ! -L /usr/share/licenses/frostbit-dex/LICENSE \
    && test -f /usr/share/licenses/frostbit-dex/NOTICE \
    && test ! -L /usr/share/licenses/frostbit-dex/NOTICE
COPY --from=go-build --chown=65532:65532 /out/node /app/node
COPY --from=go-build --chown=65532:65532 /out/explorer /app/explorer
COPY --from=go-build --chown=65532:65532 /out/miner-register /app/miner-register
COPY --from=explorer-build --chown=65532:65532 /src/explorer/dist /app/explorer_web/dist
COPY --chown=65532:65532 --chmod=0555 deploy/container-node-entrypoint.sh /usr/local/bin/dex-node-entrypoint
LABEL org.opencontainers.image.licenses="GPL-3.0-only"
ENV HOME=/tmp
USER 65532:65532
EXPOSE 6000 7000 8080
ENTRYPOINT ["/usr/local/bin/dex-node-entrypoint"]
