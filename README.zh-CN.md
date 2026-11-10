**简体中文** | [English](README.md)

# FrostBit DEX

FrostBit 是用 Go 编写的区块链与链上交易系统，提供现货、永续合约、FROST 跨链金库、Explorer 和浏览器钱包。

[打开 Explorer](https://frostbit.io/) · [文档中心](https://frostbit.io/#docs_center/docs-home) · [下载正式发布](https://github.com/commonstate-labs/frostbit/releases/latest)

## 启动节点

[v0.8.4](https://github.com/commonstate-labs/frostbit/releases/tag/v0.8.4) 提供 Linux amd64 原生包。Docker 快速部署需另外取得目标网络的镜像摘要和运行配置。

### Docker 快速部署

在 Linux 上准备 Docker、发布包里的 `frostbit-node`、目标网络发布的 `镜像:版本@sha256:摘要` 和五个配套配置文件（`config.json`、`genesis.json`、`frost_default.json`、`witness_default.json`、`peers.json`）。单独保管含 64 位十六进制私钥的文件，并准备一个独立、固定的 `fbc1` 奖励地址。镜像与配置齐备后，在解压目录里按实际值填写以下变量，一条命令启动同步。

```bash
sudo ./frostbit-node up \
  --image "$IMAGE" \
  --network-dir "$NETWORK_DIR" \
  --private-key-file "$MINER_KEY_FILE" \
  --reward-address "$REWARD_ADDRESS" \
  --explorer-url "$EXPLORER_URL"
```

节点端口通常为 6000，需开放 TCP 和 UDP。NAT 或多公网 IP 主机用 `--advertise-endpoint` 指定可入站的公网端点。启用 FROST 时加 `--frost-wrap-key-file`，已有节点沿用原 wrap key。

同步完成后，先核对同高区块 ID 和公网 TCP/UDP 可达性，再执行首次登记。最低总质押当前默认 32 FB，另需交易费。

```bash
sudo ./frostbit-node register --confirm-ready
```

[查看快速部署和登记前的检查步骤](https://frostbit.io/#docs_center/node-mining-deploy-join)

### Linux 原生节点

从发布页选择适用于本机架构的 Linux 包，解压后运行 `./dex --help` 查看该版本的完整参数。包内如有 `CONFIGURATION_REQUIRED`，请使用目标网络的完整公开配置替换 `config/runtime/` 下的模板，再用 `./dex --check-config --config-dir "$NETWORK_DIR"` 预检。

| 常用参数 | 用途 |
| --- | --- |
| `--bootstrap-mode=latejoin`、`--resume=true` | 加入已有网络并复用已有节点数据。 |
| `--advertise-addr`、`--listen-addr` | 分别设置对外通告端点和本机监听地址。 |
| `--keystore`、`--keystore-import-from` | 导入已有矿工私钥并启动，之后每次可在终端解锁。默认保留明文源。确需在就绪后清理时使用 `--delete-keystore-import-source`。FD 启动方式使用 `--private-key-fd`。 |
| `--auto-update` | 默认开启，检查兼容的官方发布。用 `--auto-update=false` 停止新版本检查。 |

[矿工私钥加密与人工解锁](https://frostbit.io/#docs_center/node-mining-keystore) · [升级、备份与换机恢复](https://frostbit.io/#docs_center/node-mining-maintenance) · [自动升级参数](https://github.com/commonstate-labs/frostbit/blob/main/deploy/updater/README.md)

## Explorer 与钱包

Linux 发布包带有 `explorer` 和 `explorer_web/dist/`。也可单独下载 `frostbit-wallet-<版本>.zip`，解压后在 Chrome 或 Edge 开发者模式加载含 `manifest.json` 的 `wallet/`。详见[运行 Explorer 后端](https://frostbit.io/#docs_center/explorer-backend)和[安装与使用钱包插件](https://frostbit.io/#docs_center/wallet-extension)。

## 从源码开发

使用 Go 1.24 和 Node.js 22。在源码根目录运行 `bash scripts/verify-public.sh` 完成构建与测试。创建独立开发网络可用 `go run ./cmd/network-init --help` 查看参数。部署边界见[发布与部署说明](https://github.com/commonstate-labs/frostbit/blob/main/deploy/PUBLICATION.md)。

除另有注明外，项目源码遵循 [GPL-3.0-only](LICENSE)。各组件许可见 [NOTICE](NOTICE)。
