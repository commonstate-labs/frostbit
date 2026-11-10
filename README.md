[简体中文](README.zh-CN.md) | **English**

# FrostBit DEX

FrostBit is a fully decentralized blockchain and on-chain trading system with decentralized order matching and custody of funds. It includes spot and perpetual trading, FROST cross-chain vaults, an Explorer, and a browser wallet.

[Open Explorer](https://frostbit.io/) · [Docs Center](https://frostbit.io/#docs_center/docs-home) · [Download a release](https://github.com/commonstate-labs/frostbit/releases/latest)

## Run a node

[v0.8.4](https://github.com/commonstate-labs/frostbit/releases/tag/v0.8.4) provides a native Linux amd64 bundle. For Docker quick start, obtain the image digest and runtime configuration separately from the target network.

### Docker quick start

On Linux, prepare Docker, the release bundle's `frostbit-node` script, and the target network's published `image:version@sha256:digest` with its five matching configuration files (`config.json`, `genesis.json`, `frost_default.json`, `witness_default.json`, and `peers.json`). Store the 64-character hex miner key in a separate file and choose a separate, fixed `fbc1` reward address. Once the image and configuration are available, set the variables below and run one command from the extracted release directory to start syncing.

```bash
sudo ./frostbit-node up \
  --image "$IMAGE" \
  --network-dir "$NETWORK_DIR" \
  --private-key-file "$MINER_KEY_FILE" \
  --reward-address "$REWARD_ADDRESS" \
  --explorer-url "$EXPLORER_URL"
```

Allow inbound TCP and UDP on the node port, usually 6000. On a host behind NAT or with multiple public IPs, set the reachable public endpoint with `--advertise-endpoint`. When FROST is enabled, add `--frost-wrap-key-file` and keep the original wrap key for existing nodes.

After syncing, verify the block ID at a common height and confirm public TCP and UDP reachability before registering for the first time. The current default minimum total stake is 32 FB. You also need enough balance for the transaction fee.

```bash
sudo ./frostbit-node register --confirm-ready
```

[See the quick deployment and registration checks](https://frostbit.io/#docs_center/node-mining-deploy-join)

### Native Linux node

Download the Linux bundle for your architecture, extract it, and run `./dex --help` for that version's full flag list. If the bundle contains `CONFIGURATION_REQUIRED`, replace the templates in `config/runtime/` with the target network's complete public configuration, then run `./dex --check-config --config-dir "$NETWORK_DIR"`.

| Common flags | Purpose |
| --- | --- |
| `--bootstrap-mode=latejoin`, `--resume=true` | Join an existing network and reuse existing node data. |
| `--advertise-addr`, `--listen-addr` | Set the endpoint peers see and the address the node binds to, respectively. |
| `--keystore`, `--keystore-import-from` | Import an existing miner key and start the node. Unlock at the terminal on subsequent starts. The plaintext source is kept by default. Use `--delete-keystore-import-source` to remove it after startup checks. Use `--private-key-fd` for FD-based startup. |
| `--auto-update` | Enabled by default to check for compatible official releases. Set `--auto-update=false` to stop checking for new releases. |

[Miner Keystore and Manual Unlock](https://frostbit.io/#docs_center/node-mining-keystore) · [Upgrades, Backup, and Recovery](https://frostbit.io/#docs_center/node-mining-maintenance) · [Auto-update flags](https://github.com/commonstate-labs/frostbit/blob/main/deploy/updater/README.md)

## Explorer and wallet

The Linux bundle includes `explorer` and `explorer_web/dist/`. You can also download `frostbit-wallet-<version>.zip` separately and load the extracted `wallet/` directory containing `manifest.json` in Chrome or Edge developer mode. See the [Explorer Backend guide](https://frostbit.io/#docs_center/explorer-backend) and the [Wallet Extension guide](https://frostbit.io/#docs_center/wallet-extension).

## Develop from source

Use Go 1.24 and Node.js 22. Run `bash scripts/verify-public.sh` from the source root to build and test. To create a separate development network, see `go run ./cmd/network-init --help`. See the [publication and deployment guide](https://github.com/commonstate-labs/frostbit/blob/main/deploy/PUBLICATION.md).

Except where stated otherwise, project source is licensed under [GPL-3.0-only](LICENSE). See [NOTICE](NOTICE) for component licenses.
