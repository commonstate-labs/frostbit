# 矿工活跃性与 Vault 成熟边界

## 成员规则

最终化区块的 proposer 签名绑定 `SampledReports` / `OfflineReports`。单个
proposer 的报告不能改变成员状态；Tracker 在滑动窗口内同时检查：

- 样本数达到 `MinSamples`；
- 样本由至少 `MinReporters` 个独立报告者提交；
- Probation 到龄后，只有“离线比例超过 `ActivationThreshold`”且离线断言本身也
  来自至少 `MinReporters` 个独立报告者时才阻止晋升；
- Active 的离线比例达到 `Threshold` 才退出热成员集。

离线剔除只改变成员状态，不扣质押、不产生 slash 奖励。报告者不能在自己
产出的区块中把自己报告为离线。缺少 reporter 的兼容调用只更新计数，不能满足
多报告者门槛。

## 共识与探测

只有 Active 可以提案、发起 Snowman poll、进入普通 poll 或
accepted-frontier 样本、发送普通 Chit。Probation 只接收空 `BlockID` 的签名
reachability probe；其空偏好回复进入独立 probe session，仅用于下一块的
liveness 报告，不提交给 Snowman，也不提供同步高度授权。

每个 Probation 在一个 10 个 finalized height 的轮次内，由链上完整 Active 集合
确定性选出至多 `K × Beta` 个报告者探测（不足时取全部 Active，且不少于 3 个），
下一轮按有界轮转更换。这个预算与普通 Snowman 在一个决定中的逻辑采样容量一致；
由于只有最终区块 proposer 能发布本地观测，不能固定为 3，否则 Active 数量增长后会在
滑动窗口内永远凑不够晋升样本。探测验签响应记 sampled，
明确超时原子记 sampled+offline，发送失败不形成证据。报告缓冲将 sampled/offline
作为一个关联批次出队，offline 始终是 sampled 子集；单块最多 256 项，超出部分按
index 游标公平轮转到后续区块，不丢弃。

## Vault 成熟

- 创世 Active 的现有质押直接成熟；
- 运行时矿工记录 `ActiveSinceHeight`，连续 Active 满环境规定的 Vault 成熟期后
  才达到 `VaultEligibleHeight`；
- 每次新增质押形成独立的 pending 批次，在各自的成熟高度并入
  `MaturedStake`；后续 top-up 不改变先前批次的成熟高度；相同成熟高度的批次合并，
  并按成熟高度升序持久化；
- 已成熟质押不会因 top-up 重新锁成熟期；质押减少时先扣 pending，再扣 matured，
  pending 内优先扣最晚成熟批次，始终保持 `MaturedStake + PendingStake <= Stake`。

`last_vault_eligibility_change_height` 在资格边界、成熟质押或合格成员变化时更新，
作为 Vault TopK 候选快照的确定性刷新信号。

## 可重放持久化

```text
liveness miner   -> 生命周期高度、总质押、matured/pending 批次
liveness counter -> 滑动窗口 sampled/offline 计数
liveness report  -> reporter + 本高度观测，用于过期与重建独立报告者集合
liveness global  -> index/count/window/lastProcessed/Vault change height
```

所有状态使用最终化高度而非墙钟；索引处理和写入按矿工 index/height 排序。
