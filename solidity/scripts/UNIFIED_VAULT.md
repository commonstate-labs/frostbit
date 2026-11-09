# 统一 Vault：同一地址充值和提现

统一 Vault 已接通 server5 一键部署、阶段二全员验收、主链配置与防重、私有 dev 见证和前端调用。本地测试通过不代表完成主网部署；实际进度以各链已核验回执和阶段二签名为准，线上地址按下述流程重建后才能切换。

统一 Vault 的充值接口为 `depositNative(string receiver)`、`depositToken(address token,uint256 amount,string receiver)`；充值收款地址、代币授权 spender、提现调用地址均为这个 Vault。`Deposited(uint256,address,address,string,uint256)` 事件中的 `vault` 是合约自身。提现协议为 `DEX_VAULT_MIXED_WITHDRAW_BATCH_V1`，批次绑定严格连续且跨 token/epoch 不重置的 `batchSeq`，支持一个批次混合多种资产，只提供 `withdrawBatch`、`withdrawBatchWithHandovers` 和 `claimReward`；不再提供单笔 `withdraw`、旧 `withdrawWithHandover` 或独立 `changePub`。

普通直接转账走 `receive` 时不会发出 `Deposited` 事件；充值必须调用 `depositNative` 或 `depositToken`，才能留下绑定 DEX 收款人的充值证据。

本版仅新创世 V3 双授权；固定安全公钥只在 `frost_default.json.guardian` 配置。运行服务与密钥生成见 [双授权说明](../../.agent/docs/frost_multigeneration_handover.md)。

## server5 操作流程

同时部署四条 EVM 链和 TRX 的新金库：

```powershell
powershell -NoProfile -File D:\dex\dev\server5\deploy-genesis-vaults-mainnet.ps1 -FrostConfig D:\secure\public\frost_default.pre-ceremony.json -SkipContractTests
```

若 Base 已广播并留下 `pending` 回执，但本轮 Base RPC 核验持续超时，可显式只跳过该笔 Base 核验，继续部署其余缺失链：

```powershell
powershell -NoProfile -File D:\dex\dev\server5\deploy-genesis-vaults-mainnet.ps1 -FrostConfig D:\secure\public\frost_default.pre-ceremony.json -SkipContractTests -SkipPendingVerificationForChain Base
```

此模式保留 Base 原交易及 `pending` 状态，不会重播 Base。其他链仍逐笔验证；结束会报告部分完成并返回非零状态，不生成完整五链验收材料。Base RPC 恢复后，用上面的默认命令核验原交易。

脚本编译当前源码，只创建 `SchnorrExampleV3` / `SchnorrExampleTronV3` 统一金库。它先只读恢复所有已有新版 pending/verified 交易，只有缺少新版金库的链才读取部署私钥、核对付款地址与余额，列出计划并要求输入 `DEPLOY`。`-FrostConfig D:\secure\public\frost_default.pre-ceremony.json -SkipContractTests` 仅跳过合约测试，不跳过编译、身份、代码或交易核验。

仅检查本地 manifest、产物、回执并准备公开材料，可以增加 `-PrepareOnly`。此模式使用已有编译产物，不读取私钥、不编译、不连接 RPC/SSH，也不发送交易：

```powershell
powershell -NoProfile -File D:\dex\dev\server5\deploy-genesis-vaults-mainnet.ps1 -FrostConfig D:\secure\public\frost_default.pre-ceremony.json -SkipContractTests -PrepareOnly
```

首发流程不提供跳过 TRX、复用旧 TRX Vault 或占位回执模式。五条账户链必须全部产生本轮源码对应的真实回执。

部署完成后，脚本打印本轮三个材料路径：

- `deployments`：恰好五份本轮真实 Vault 回执。
- `artifacts`：冻结的当前新源码编译产物和 build-info。

这些材料位于 `.local/server5/ceremony/unified-vault-<DEX域>-<EVM代码摘要>-<TRX代码摘要>/`。`ready=false` 表示仍缺本轮部署，不能开始阶段二。不要把其他文件混进 `deployments`。

随后用当前源码重新构建 `cmd/frost-ceremony`。每名验证者独立执行 `attest-deployments`，收齐全员新批准后执行 `build-config`。两步共用原阶段一的 manifest、无结果 base-frost-config、peers，以及本轮真实 RPC/最终性配置；以下是新增或替换的参数：

```text
--deployments <脚本打印的本轮deployments目录>
--solidity-artifacts-dir <脚本打印的本轮artifacts目录>
```

`attest-deployments` 保留每名验证者自己的 `--node-id`、受控身份密钥 FD 和新输出路径；`build-config` 保留新 `--deployment-approvals-dir` 及 `--out`，禁止混入旧批准。

新签名声明绑定每个 Vault 的地址、部署交易、精确字节码、DEX 域和充值协议。旧批准不能复用。只改变部署结果、原冻结网络/委员会/门限等输入不变时，阶段一 manifest 和密封份额可复用，无需重做 DKG。

server5 已有旧最终配置时，在全员新验收完成后仍使用现有 `server5.ps1 -Action promote-frost -RebuildGenesis` 验证、备份并提升本地候选。随后按既有整体停链、清 DEX/Explorer 数据、重新部署流程切换；保留原身份、份额、wrap key 和外链节点数据。不能对旧运行链只替换 JSON 或滚动启用新协议。新版充值前端、节点及私有见证程序须一起更新。

## 运行期接口边界

新版链上配置使用 `depositProtocol=2` 和正确的 `depositDexChainId`；每个 Vault 的地址就是自身充值入口。私有 `dev` 按请求对应金库核对实际成功回执、事件、收款人、代币、金额和确认数。主链不访问外链 RPC；沿用同一外链事件的规范哈希与原子入账防重，协议号不进入防重键，避免跨版本重复记账。

前端授权、调用和事件定位使用同一 Vault；读取事件只是得到申报所需的交易内事件序号，完成状态由 DEX 见证结果决定，不承担外链最终性判断。旧本地充值记录保留哈希和 DEX 请求查询，不能自动转成新 ABI 或再次付款。用户可手动“保留记录并开始另一笔充值”：先原样归档并提供下载，再清当前记录；不依赖外链状态，点击本身不付款，存储失败则保留当前项。

## 构造参数与产物

- `SchnorrExampleV3(px, rawPy, guardianPx, guardianPy, initialEpoch, dexChainId)`。
- `SchnorrExampleTronV3(px, rawPy, guardianPx, guardianPy, initialEpoch, dexChainId, falseReturningToken)`。
- `VAULT_DEX_CHAIN_ID` 必须显式填写非零十进制 uint64，与外链的 `VAULT_CHAIN_ID` 分开。
- 公钥必须来自已审核的 DKG 结果。脚本验证 BN128 曲线点，保留 `rawPy`，核对合约使用的 low-Y 公钥和初始 epoch。
- 当前编译产物在 `solidity/artifacts/`，包含对应 `.dbg.json` 指向的 `build-info/`。部署器检查完整创建输入、精确运行字节码，以及每一个 `dexChainId` / TRON `nonstandardToken` immutable 槽；不能拿旧编译产物替代。

## EVM 专用命令

从 `solidity/` 执行。环境变量示例中的 DEX 域、群公钥和 RPC 必须替换为实际已审核值。付款私钥由操作者自行通过 `DEPLOYER_PRIVATE_KEY` 提供；示例和回执均不包含私钥。

```powershell
$env:VAULT_FROST_CONFIG = 'D:\secure\public\frost_default.pre-ceremony.json'
$env:VAULT_AUTHORITY_PROTOCOL = '3'
$env:VAULT_NETWORK = 'ethereum'
$env:VAULT_CHAIN_ID = '1'
$env:VAULT_DEX_CHAIN_ID = '<实际 DEX chain ID>'
$env:VAULT_ID = '0'
$env:VAULT_EPOCH = '1'
$env:VAULT_GROUP_PUBKEY = '<阶段一的原始 64 字节公钥，128 位十六进制>'
$env:VAULT_RPC_URL = '<保留交易索引的 HTTP(S) RPC>'
$env:VAULT_ALLOW_MAINNET_DEPLOY = 'true'
npm run deploy:vault
```

| `VAULT_NETWORK` | `VAULT_CHAIN_ID` | 可替代 `VAULT_RPC_URL` 的变量 |
| --- | --- | --- |
| ethereum | 1 | RPC_URL_ETHEREUM |
| base | 8453 | RPC_URL_BASE |
| bnb | 56 | RPC_URL_BNB |
| arbitrum | 42161 | RPC_URL_ARBITRUM |

测试网络名称也支持 `sepolia`、`baseSepolia`、`bnbTestnet`、`arbSepolia`。脚本同时校验指定网络和 RPC 实际 chain ID。EVM 可设置 `VAULT_CONFIRMATIONS`（默认 1）、`VAULT_CONFIRM_TIMEOUT_MS`（默认 300000）。这份部署回执证明部署执行结果；不能替代 Go 阶段二的最终性和委员会证明。

已编译时可直接执行 `node scripts/deploy_schnorr_vault.js`。保留旧式 `hardhat run ... --network ...` 时，脚本也识别 `HARDHAT_NETWORK`；推荐独立 Node 命令，避免在恢复时加载 Hardhat 网络账户配置。

## TRON 专用命令

公共 DKG 参数、`VAULT_DEX_CHAIN_ID` 和主网显式开关与上面相同，另设置：

```powershell
$env:VAULT_CHAIN_ID = '728126428'
$env:TRON_JSON_RPC_URL = '<TRON JSON-RPC>'
$env:TRON_FULL_HOST = '<同一网络的 TRON FullNode>'
npm run deploy:tron-vault
```

TRON 主网的 `nonstandardToken` 固定为 USDT `TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t`，禁止配置成其他代币。脚本交叉验证 JSON-RPC 与 FullNode 的网络身份，保留资源预检、未签名交易的严格检查及签名前后比对。部署 Energy 预检门槛为主网实测口径 `2,400,000 Energy`；仍会按实时 Energy 单价、账户冻结资源、液态 TRX 和带宽共同判断是否可签名。

可用参数包括 `VAULT_FEE_LIMIT_SUN`、`VAULT_ORIGIN_ENERGY_LIMIT`、`VAULT_CONFIRM_TIMEOUT_MS`。已编译时可执行 `node scripts/deploy_schnorr_vault_tron.js`。

## 回执与恢复

默认目录为 **`solidity/vault-deployments/`**，不会读取或覆盖更早的历史 `deployments/`。固定文件名：

```text
<network>-<externalChainId>-<dexChainId>-<vaultId>.json
ethereum-1-51025-0.json
tron-mainnet-728126428-51025-0.json
```

`VAULT_DEPLOYMENTS_DIR` 可指定新目录，`VAULT_OUTPUT` 可指定完整文件名。不要在广播状态不确定时删除记录或换输出路径重试，否则会失去防止重复付费的依据。

签名后、广播前，脚本独占创建并同步保存公开的 `pending` 记录，包含准确的 `txHash/address/deployer` 和完整部署身份。默认再次执行同一参数时，只核对原交易和合约，不再签名或广播；不读取付款私钥。明确历史恢复可设置 `VAULT_RECOVER_TX_HASH`，它必须和已存在记录一致。TRON 哈希使用无 `0x` 的小写 64 位十六进制，EVM 使用带 `0x` 的小写哈希。

EVM 原交易被节点拒收或未找到时，操作人可显式设置 `VAULT_REBROADCAST_TX_FILE`，提供只含单条 `0x...` 已签名原始交易的文件；主网还必须设置 `VAULT_ALLOW_MAINNET_DEPLOY=true`。此模式仅接受已有 `pending`，严格核对原哈希、付款人、完整创建输入、外链与 DEX 域以及合约地址。已找到成功回执时只核验；否则只有原 nonce 在 latest/pending 两种视图中均未使用、预期地址无代码、pending 余额足够时，才广播一次原字节。它不读取私钥，不改 nonce、费用或 pending 记录；超时和错误均保留原记录。旧区块搜索起点不会阻止此显式恢复，重播后的搜索使用本次起点，原历史线索保留。

server5 对应参数是 `-RetryEvmTransactionFile <绝对路径>`：先显示原交易和最大 Gas 费用、核验其他已有回执，再要求输入 `DEPLOY`；与 `-PrepareOnly` 合用时仅离线检查。成功后继续缺失链的新部署，新部署仍单独确认。此参数仅用于恢复仍为 pending 的原交易，恢复成功后的后续普通运行应去掉它。示例见 `dev/server5/README.md`。可选 `VAULT_DEPLOYER_ADDRESS` 同时约束新签名和已有交易核验；CLI 报错只显示有界摘要，不打印完整签名交易。

恢复时推荐使用已编译产物直接运行上述 `node scripts/...` 命令；`npm run deploy:vault` 和 `npm run deploy:tron-vault` 会先编译并加载 Hardhat 配置，不适合作为避免读取网络账户配置的恢复入口。

只有成功收据、精确创建参数、运行字节码、immutable 及 authority getter 全部通过后，状态才变为 `verified`。失败或超时保留原记录。使用默认输出遇到旧版回执、不同 DEX 域、公钥、epoch 或编译产物时直接拒绝，不能通过改字段把旧合约“升级”为新合约。

EVM 新部署在广播前把 `searchFromBlock` 定位线索写入 pending。RPC 按哈希明确返回 `null` 时，可从该高度有界读取完整区块交易及区块回执，最多 256 块并受确认超时限制；verified 恢复按已记录的部署高度点查。显式恢复或没有线索的旧 pending 可提供 `VAULT_RECOVER_BLOCK_NUMBER`。高度仅帮助定位，仍完整核对原交易哈希、区块锚、创建输入、地址、代码和 getter；RPC 错误不会被吞掉。未找到证据时保留 pending，核对部署区块或换同链 RPC 恢复，不自动重新付款。

新回执包含 `schemaVersion:1`、`status`、`depositProtocol:"DEX_VAULT_DEPOSIT_V1"`、`protocol:"DEX_VAULT_MIXED_WITHDRAW_BATCH_V1"` 和十进制字符串 `dexChainId`，并绑定网络/金库 ID、初始 epoch、公钥、编译器信息、创建/运行代码哈希。已验证记录还包含真实 `blockNumber`、`deployedAt`。TRON 另有 `nonstandardToken`。已有 `solidity/deployments/*.json` 是旧链上部署的真实历史证据，必须原样保留；新部署导入会因旧提现协议或字节码不匹配而拒绝，不能改写旧回执冒充混合批次协议部署。

## 离线集成接口与测试

`scripts/vault_deployment.js` 不读取私钥、不调用 RPC，导出：

- `loadVaultArtifact({artifacts}, contractName)`：从 Hardhat 本地产物及 build-info 验证并读取 ABI、字节码、immutable 名称和编译器信息。
- `expectedVaultRuntime(artifact, dexChainId, nonstandardToken?)`：精确构造预期运行时代码。
- `vaultIdentity(options, artifact)`、`loadVaultJournal(output, identity)`：审核公共部署身份与已有记录。
- `resolveVaultOutput(env, network, chainId, dexChainId, vaultId)`：解析新输出路径。

EVM 脚本导出 `parseOptions`、`run`、`deployEvmVault`；TRON 脚本导出 `parseOptions`、`run`、`deployTronVault`。部署适配器均接受依赖注入用于纯本地测试，不能在生产以模拟数据代替链上证据。

已有依赖和本地编译产物时，可离线执行部署回归：

```powershell
node node_modules/hardhat/internal/cli/cli.js test --no-compile test/deployment_validation_test.js test/unified_vault_deployment_test.js test/tron_deployment_validation_test.js test/unified_vault_tron_deployment_test.js test/vault_deposit_test.js
```

server5/阶段二/私有见证/前端的回归入口分别为 `go test ./dev/server5 ./cmd/frost-ceremony ./frost/ceremony ./cmd/network-init`、`node --test dev/server5/prepare-unified-vaults.test.js`、Go VM/见证/API 测试及 Explorer Vitest/build。

## 多代交接 V2（2026-10-16）

显式 `VAULT_AUTHORITY_PROTOCOL=2` 选择新工厂 `SchnorrExampleV2` / `SchnorrExampleTronV2`；缺省继续 V1，旧 journal 不覆盖。server5 对应参数 `-AuthorityProtocol 2`。必须用新地址、同版本冻结 artifact 和全员阶段二验收后才启用 DEX capability，二进制升级不能改变旧合约能力。

V2 没有提现也能继续 DEX 换届；公开证书永久保存，真实提现按需携带最多 8 代，长链由领取钱包分段。每代冻结 nextBatchSeq，旧已签批次必须先执行；不得让旧授权失效。无常驻 keeper/独立 gas 钱包。协议、升级边界与验证范围见 `../../.agent/docs/frost_multigeneration_handover.md`。
