# 用户自行领取提现

DEX 完成提现扣账和门限签名后，账户链用户连接 EVM 钱包或 TronLink，支付费用并提交金库调用。BTC 交付完整已签名原始交易和必要父交易，用户自行广播；BTC 矿工费从申请扣款金额中扣除。批量交易按请求数平均分摊手续费，不能整除的 satoshi 由 FIFO 靠前请求依次多承担 1 sat。

DEX 状态只有 `QUEUED`、`SIGNING`、`SIGNED`。`SIGNED` 表示签名已生成，不能推断外链已执行。页面不查询回执、不自动判断领取或签名失效，钱包返回哈希后保留领取、复制哈希和外链查询入口。

账户链金库在 DEX 内完成换钥后，新 epoch 的每份提现制品都会携带同一份旧委员会交接证明。首位外链执行用户原子完成交接和提现；若交接已由另一位用户完成，合约核对新公钥后跳过旧证明验签。用户付费钱包可以不同于固定收款地址。

## 查询接口

- 节点：`GET /frost/withdraw/result?withdraw_id=...`，或使用 `request_tx_id=...`。
- Explorer：`GET /api/frost/withdraw/result`，参数一致，额外的 `node` 必须来自配置白名单。
- 个人历史分页使用 `summary_only=1`，仅点读请求和 DEX 状态，不展开签名包。实际领取使用完整结果。
- 两个 ID 同传时必须指向同一条请求。尚未最终化或数据不存在返回 `404`，状态/绑定矛盾返回 `409`。只有升级前已丢失完整副本的旧签名仍可能返回 `410`，要求查询保有旧副本的节点；新签名的完整领取资料永久进入 StateDB。

账户链完整结果包含字符串 `chain_id`、`epoch`、`amount`，固定的 `request_id`、`receiver`、`token_address`、`vault_address` 和完整 `calldata`。迁移后的 epoch 另返回 `includes_handover=true`。接口从 DEX 请求和资产/金库配置重建八 word 消息，与永久元数据认证的原始 calldata 比较。交接模板从永久 migration marker 重建，并用旧公钥验证 calldata 内嵌的交接签名；已有永久迁移全文时还须与其严格一致，读取不依赖旧迁移 archive。前端再次本地解码并规范重编码，外层交易 `value=0`，不修改签名付款字段。资产 decimals 仅来自前端展示元数据，不能改变原始金额。

BTC 结果包含 `requested_amount`、`network_fee`、`received_amount`、`raw_tx_hex`、`txid`、`btc_network` 和按父到子排列的 `dependencies`；其中 `requested_amount = network_fee + received_amount`。包不包含私钥。遍历最多 256 个制品、总 raw 大小 4 MiB；每笔制品须匹配永久元数据，缺少依赖时拒绝生成不完整包。

新签名将完整提现记录、job、签名包、制品全文和提现到制品的映射分别写入 `KeyFrostPermanentWithdraw`、`KeyFrostPermanentWithdrawJob`、`KeyFrostPermanentSignedPackage`、`KeyFrostPermanentArtifact`、`KeyFrostPermanentWithdrawArtifact`，均参与 StateDB 指纹、checkpoint 和状态同步。读取优先使用永久记录；只有永久 key 不存在才允许点读旧 archive，永久记录损坏时直接报错，不能用 archive 掩盖。旧库没有映射时，只按原签名 job 点读资料恢复制品 ID，仍需原始制品及其永久元数据，不扫描历史交易，也不重签。旧副本不会由查询路径导入共识状态。

## 升级与维护

普通自动提现和账户链独立换钥的发现、入队与广播均已停用。operator 启动取消旧 `withdraw.broadcast` 与 `vault.maintenance` 未完成任务，同时保留 checkpoint、结果和审计历史。`actions.vault_maintenance=true` 会被配置校验拒绝。

账户链普通提现不维护公共执行链；`KeyFrostPermanentMigration` 永久保存完整 `FrostVaultMigrationArchive`，包含旧委员会签名和模板，所有验证节点需一致升级。现有 `KeyFrostVaultHandoverProof` 仍是生成新 epoch 提现制品所需的当前交接证明。新永久制品不能被磁盘压力清理；升级前的完整历史若已丢失，不能由摘要恢复，需依赖仍保有资料的节点或认证区块重放。

这些查询不访问外链 RPC。用户钱包仅在主动连接和提交时检查网络身份、构建、签名和发送交易；DEX 不需要确认哪一笔用户交易率先完成了外链交接。
