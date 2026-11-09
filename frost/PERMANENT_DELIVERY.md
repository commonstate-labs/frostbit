# FROST 永久领取资料

用户选择：完整领取与迁移资料永久存入 StateDB，全节点随状态根、checkpoint 和状态同步保存。V2 保留旧 SIGNED 批次义务：每代证书冻结 nextBatchSeq，外链先执行旧批次，再安装后续 authority；没有提现不阻止 DEX 持续换届。V1 合约能力保持原样，不能靠二进制更新获得多代能力。

## 写入与读取

已通过 VM 签名验证的交易，在同一执行视图中写入：

| StateDB key | 内容 |
| --- | --- |
| `frost_permanent_artifact_` | 原始已签交易/调用数据及直接父制品引用 |
| `frost_permanent_withdraw_` | 每笔已签提现的完整参数 |
| `frost_permanent_job_` | 已签 job、提现列表、规范模板和模板哈希 |
| `frost_permanent_package_` | 已接受签名包 |
| `frost_permanent_migration_` | 完整换钥 intent、模板和授权签名 |
| `frost_permanent_withdrawref_` | withdrawID→artifactID |
| `frost_permanent_batch_` | V2 的 chain/vault/batchSeq→代表 withdrawID 精确索引 |

既有 artifact metadata、requestTxID→withdrawID 和 migration→artifact 引用继续保留在 StateDB。活动请求、job 和 FIFO 仍按原规则终结删除；新永久 key 不参与活动队列扫描。

领取、制品、签名包与迁移下载优先读取永久资料，永久值存在但损坏时返回错误，不用旧归档替代。旧 archive 查询兼容仅用于永久值不存在的历史数据。账户链带 handover 的领取校验可以从共识 migration marker 和已认证 calldata 重建模板并验证授权，不要求旧归档存在；存在永久 migration 全文时仍严格核对。

每个新制品的直接 ParentID 必须已有完整且与永久 metadata 一致的 StateDB 制品。检查只读取 StateDB；它与“所有新父制品也经过相同校验”的规则共同保证依赖链可恢复，避免每次递归遍历全部历史。

## 归档清理

旧 archive key 分类不变。清理只删除已经有字节一致永久副本的 artifact/job/package/migration；未迁入永久状态或内容不一致的旧案归档保留。先刷冷队列，再 Sync 提交归档删除，最后 Sync 推进另一库的游标。此清理不能修改 StateDB 或状态根。规划日志通过永久提现记录识别 SIGNED 终态，避免归档副本清除后误把日志当成未完成任务而一直保留。

## 升级边界

- 新永久写入改变共识执行结果和状态根，节点需要一致启用版本；本次代码修改不代表已部署。
- 旧案不自动迁入 StateDB。各节点可能已经裁剪不同历史，因此不得在启动、查询或 checkpoint 安装时按本地归档回填共识状态。
- 新 BTC 子交易或后续迁移若引用仅剩 archive 的旧父制品，会明确拒绝，需先设计统一的历史重放或经共识验证的导入流程。不能忽略父依赖，也不能只在某台节点补数据。
- 已经丢失的历史全文不能从哈希恢复。保留旧案归档只防止继续丢失，不等于历史永久化迁移已经完成。
- 实际库所在卷的压力采样、operator 审计日志分段保留属于独立审查项，本次未修改；删除与游标的同步顺序仅在此次涉及的 FROST 制品清理路径调整。未完成提现列表的清理、旧见证投票残留和 operator 任务保留也不由这项存储修改自动解决。

## 验证范围

测试覆盖永久 key 分类与非历史清理属性、真实 VM 提现/换钥写入、旧父缺件拒绝、archive-only 旧案保护、归档副本清理不改变状态根、StateDB checkpoint 安装与重启、账户链和 BTC 仅 StateDB 领取及父交易包读取、损坏永久值拒绝回退。

这些是本地验证，不包含线上升级、旧链资料导入或真实外链广播。

## V2 多代恢复

公开交接证明按目标 epoch 保存在 StateDB 的 compact migration marker 与 handover proof；每个永久提现只携带其直接证书，避免复制所有祖先。API 以至多 8 代的游标页读取，领取先按外链 nextBatchSeq 查永久批次索引；未付款历史批次先执行，随后再追赶缺失证书。超过 8 代时由当前领取钱包显式分段确认，无常驻 keeper 或独立维护钱包。新 envelope 不覆盖原 artifact/hash，也不重新签授权或扣款。详见 `../.agent/docs/frost_multigeneration_handover.md`。
