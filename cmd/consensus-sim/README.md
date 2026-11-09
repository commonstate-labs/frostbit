# 共识稳定性虚拟时钟实验器

这个程序只模拟一个高度上的关键协议状态，不启动 HTTP、数据库或真实
goroutine 定时器。传播延迟和轮询间隔都进入事件队列，因此相同 seed 可重复，
几秒钟的虚拟过程通常能在毫秒级完成。

不传 `--scenario` 时继续运行原来的 `propagation-race`。模拟器现在提供五个场景：

- `propagation-race`：原有 A/B 两候选事故模型，用于保留旧协议回归；
- `fragmented`：固定复现 20 个诚实节点的 `A=7 / B=6 / C=7` 首到偏好，
  然后再让每个节点收到全部三个候选；
- `vrf-network`：20 个等质押虚拟矿工使用生产共用的纯提案资格函数选择
  proposer；每个 proposer 先在本地注册自己的候选，再通过虚拟 PushQuery、
  Gossip 以及 PullQuery 缺块时的 Get/Put 往返，按网络到达顺序传播。
- `vrf-phase-skew`：在 `vrf-network` 上增加父块 VM 提交完成错相，模拟部分节点
  已进入下一高度、其余节点尚未就绪时的弃权和收敛延迟。
- `single-node-not-ready`：最小跨高度模型；全网只有一个候选，且只有最后一个诚实节点
  尚未完成父高度提交，候选先到后缓存，准备好前不发 poll 并对收到的 poll 弃权。

默认参数使用 20 节点拓扑：

- `K=15`、`AlphaPreference=8`、`AlphaConfidence=12`、`Beta=15`
- 普通 Snowball poll 固定使用生产逻辑：从完整矿工集合带放回抽取
  15 个逻辑槽位，同一 peer 重复命中时只发一个请求，回复按预先固定的槽位权重计票
- 每条消息的单向网络延迟独立在 `60ms～200ms` 之间随机采样
- 0ms 时全网看到候选 A，其中 3 个节点立即开始轮询
- 400ms 时全网看到候选 B，B 代表 hash 更小的候选
- 其余 17 个节点从 425ms 开始轮询

原事故的快速网络是单独保留的回归场景。复现旧动态 Chit fallback 的双重最终化：

```powershell
go run ./cmd/consensus-sim --policy current --latency-min 5ms --latency-max 15ms --expect fork
```

比较“首次 Chit 偏好持久化”和“累计偏好强度”：

```powershell
go run ./cmd/consensus-sim --policy sticky --latency-min 5ms --latency-max 15ms --expect safe
go run ./cmd/consensus-sim --policy snowball --latency-min 5ms --latency-max 15ms --expect safe
```

验证生产和模拟器共同使用的带放回采样能让 `7/6/7` 场景安全收敛：

```powershell
go run ./cmd/consensus-sim --scenario fragmented --policy snowball --expect safe
```

这里的 15 是“逻辑样本槽位”，不是 15 个不同签名者。比如某个 peer 命中 3 个
槽位，本轮只向它请求一次；它的一次有效签名回复在本地计 3 票。重复发送同一回复
不会再次计票，未回复则这 3 个槽位全部弃权，也不会重抽或降低阈值。

## 实验性 Strength 平局规则

`--strength-tiebreak canonical` 只用于模拟 A/B 对照，生产 Snowball 默认仍是
`none`。它在多个候选拥有相同最高 Strength 时，统一按“更低 proposal window →
更低 block hash → 更低完整 BlockID”选择累计偏好。

这个规则不是可直接上线的安全修复。节点只能对自己已经收到的候选执行固定排序；
若更优候选迟到，一部分节点可能已经最终化旧候选，其余节点随后免费切到新候选。
快速网络的 A/B 延迟场景会确定性复现该双最终化：

```powershell
go run ./cmd/consensus-sim --scenario propagation-race --policy snowball --strength-tiebreak canonical --latency-min 5ms --latency-max 15ms --expect fork
```

因此该开关只保留为“不应把 canonical 免费切换带进生产”的安全回归；直接比较可由
proposer 调整交易内容得到的 block hash 还会引入磨 hash 动机。

使用生产提案资格数学和虚拟候选传播批量搜索 seed：

```powershell
go run ./cmd/consensus-sim --scenario vrf-network --policy snowball --max-time 25s --runs 1000 --expect safe
```

`vrf-network` 默认使用 `--expected-proposers 5`、`--gossip-fanout 15` 和
`--gossip-relay-min 50ms --gossip-relay-max 150ms`。它使用由 seed、节点和
window 确定生成的合成 VRF output，不生成真实密钥或 VRF proof；同一个 output
进入生产和模拟器共用的资格判断，因此复用的是提案概率数学，而不是完整的
`RealBlockProposer`、TxPool 或 StateDB。

`--expected-proposers` 表示 W0 的基准值。模拟器按当前生产默认窗口逐步放宽：

- W0（虚拟 0s）：`1×` 基准期望数；
- W1（5s）：`3×`；
- W2（10s）：`6×`；
- W3（20s）：`10×`。

虚拟 0s 对应生产中最终化后的第一次 W0 提案检查，不表示机器绝对时间。同一节点
每高度最多成功创建一个候选；节点只在本地尚未收到任何完整合法候选时才尝试后续
窗口。多个矿工在同一 W0 并发通过资格检查时仍可分别生成候选。
默认 `--max-time 6s` 只覆盖 W0/W1；要观察完整 W0～W3 退让过程，显式使用
`--max-time 25s`。该场景假定所有矿工都处于 active 状态且有可提交易；它不模拟
liveness/质押状态读取、交易池内容、真实 VRF proof、VM 执行或落盘。

`snowball` 的纯状态机把两类偏好分开：

- 首个完整收到的候选初始化累计偏好，后来的小 hash 不会免费改色；
- 达到 `AlphaPreference` 的软多数为赢家增加累计 `Strength`，严格反超后才改变累计偏好；
- 达到 `AlphaConfidence` 的硬多数独立更新近期强胜偏好和连续 `Confidence`；
- `Confidence` 达到 `Beta` 后最终化近期强胜偏好，不要求它先追平全部历史 `Strength`。

观察把 poll 改回 125ms 对默认竞态的缓解效果：

```powershell
go run ./cmd/consensus-sim --policy current --latency-min 5ms --latency-max 15ms --poll-interval 125ms --max-time 8s --expect safe
```

批量跑 seed、输出完整轨迹或 JSON：

```powershell
go run ./cmd/consensus-sim --policy snowball --runs 1000 --expect safe
go run ./cmd/consensus-sim --policy current --latency-min 5ms --latency-max 15ms --expect fork --trace
go run ./cmd/consensus-sim --policy current --json
go run ./cmd/consensus-sim --scenario vrf-network --policy snowball --json
```

批量模式默认只打印汇总；需要逐 seed 输出时加 `--verbose-runs`。

单次文本输出额外报告：

- `proposers` / `candidates`：本次实验参与过提案的唯一节点数和唯一候选数量；
- `initial_preferences`：每个诚实节点第一次收到候选时形成的偏好分布；
- `preferences`：模拟结束时的持久偏好分布；
- `poll_sampling logical_slots / unique_peer_requests`：抽出的逻辑槽位数和实际发送
  的唯一 peer 请求数；`request_ratio < 1` 表示重复槽位已被合并发送。

批量汇总报告 proposer/candidate 的平均数和范围；如需查看每个 seed 的
`initial_preferences`，使用 `--verbose-runs` 或 `--json`。JSON 中对应字段为
`scenario`、`proposer_count`、`candidate_count` 和
`initial_preference_counts`。

输出中的时间全部是虚拟时间：

- `first_finalized`：第一个诚实节点累计满 Beta 并最终化所需时间；
- `avg_node_finalized`：所有已最终化诚实节点的平均最终化时间；
- `all_finalized` / `avg_height_finalized`：全部诚实节点完成该高度的时间，最接近这个单高度模型里的“出块时间”；
- `avg_one_way_latency`：实际随机采样出来的平均单向网络延迟；
- `avg_round_trip`：一轮请求与响应的平均 RTT；PullQuery 对端缺块时包含中间
  Get/Put 的额外网络往返。

`seed` 不是延迟，而是伪随机序列的起点。相同 seed 会重放相同的 peer 采样和
网络延迟；对 seed 求平均没有协议或网络含义，所以批量输出保留 seed 范围，并对
实际采样延迟求平均。

`current` 是迁移前关键偏好/计票逻辑的历史协议模型，不是完整节点替身。仓库原有的
`consensus.RunLoop` 仍适合作为真实共识代码的集成模拟；这个命令用于快速、可重复
地搜索时序和参数反例。

## 与生产代码的关系

生产 `Snowball` 和这个模拟器现在共同调用
`consensus/preference_state_machine.go` 中的纯状态转移函数：

- `AdvancePreferenceState`：输入旧状态和一轮投票，返回新状态及转移原因；
- `AddSnowballCandidate`：注册合法候选，并用首个候选初始化 Snowball 偏好；
- `ResolvePreferenceChit`：只供 `current` / `sticky` 历史事故基线复现；
- `CanFinalizePreferenceState` / `FinalizePreferenceState`：最终化资格和单向状态。

普通 Snowball poll 还共同调用：

- `BuildReplacementSample`：带放回抽 K 个槽位并聚合重复 peer；
- `PollAccumulator`：拒绝未采样/重复回复，按本地权重计票，判断硬阈值早停、
  全部逻辑槽返回和超时关闭，并返回同样的部分投票结果。

生产侧用 `VRF seed + seqID + 本轮随机 nonce` 生成抽样 seed；模拟器用可重放的
伪随机 seed。两边还会在 poll 开始时固定同一语义的候选快照，因此中途新到候选
不会只在模拟器中触发提前结束。生产侧仍独立负责完整矿工快照、签名/nonce/采样
集合校验、HTTP 发送、真实超时和落盘；模拟器只用虚拟事件驱动这些输入。真实
调度器的唤醒抖动并不等同于虚拟时间，所以模拟出的毫秒数仍不是生产墙钟承诺。

`vrf-network` 还与生产提案生成和接收验证路径共同调用：

- `EvaluateProposalEligibility(ProposalEligibilityInput)`：根据固定概率或
  `ExpectedProposers × stake ratio` 计算阈值，应用单矿工 20% 权重上限（目标至少
  5 个独立矿工），并用 VRF output 作最终资格判断。

这些共享函数不读取时间、不发网络请求、不写数据库、不发布事件，也不修改输入。
生产引擎固定使用 `--policy snowball` 对应的状态转移；`current` 和 `sticky`
仅保留为旧实现的事故回归基线，不再进入线上 Chit 或 poll 路径。虚拟网络和轮询
调度由模拟器提供。达到 Beta 后立即最终化，不模拟签名校验、VM 执行或数据库落盘。

## 跨高度 VM 完成错相

只验证“一个节点还没准备好，下一高度已经到达”时，使用不包含 VRF、多候选或
gossip 竞争的最小场景：

```powershell
go run ./cmd/consensus-sim --scenario single-node-not-ready --policy snowball --parent-ready-delay 6s --max-time 12s --expect safe --trace
```

该场景在虚拟 `0s` 把唯一的下一高度候选交给全部节点。前 19 个节点立即进入 poll，
最后一个诚实节点缓存候选，在 `6s` 前对下一高度查询弃权；到 `6s` 才激活缓存候选并参与
后续 poll。默认仍复用生产参数：每节点每高度最多 4 个网络活跃 poll、单 poll 4 秒
超时、活跃加按序等待总上限 8。它只模拟 readiness gate，不执行 VM 或数据库提交。

`vrf-phase-skew` 在 `vrf-network` 的基础上模拟上一高度 VM
提交完成时间不一致。虚拟 `0s` 归一化为第一个节点完成父块提交后的首次 W0
检查，其余节点在 `--parent-ready-spread`（默认 `3s`）内依次就绪。尚未就绪
的节点可以缓存和转发下一高度候选，但不会发起下一高度 poll；收到下一高度
查询时返回弃权。

```powershell
go run ./cmd/consensus-sim --scenario vrf-phase-skew --policy snowball --parent-ready-spread 3s --max-time 25s --expect safe
```

设置 `--parent-ready-spread 0s` 可得到同 seed 的无错相基线。该场景额外输出
`parent_not_ready_responses`，用于确认慢化确实来自跨高度错相，而不是随机 seed。
它只建模 VM 完成时刻，不执行真实 VM。为便于和 `vrf-network` 直接比较，生产
固定的 `500ms` 最终化后提案宽限被归一化掉；把结果映射到生产端到端时间时应另行
计入该固定开销。

模拟器的 poll 管线与生产默认值一致：

- `--pipeline 4` 只限制仍在等待网络响应的 poll；
- 已完成但等待较小 `seqID` 的结果进入 ordered-pending 区；
- `--poll-capacity 8` 限制 network-active 与 ordered-pending 的总数。

严格的 `seqID` 应用顺序不变，但已完成的 poll 不再占用网络槽。同一节点在同一
高度最多成功创建一个候选；本地已经收到任一完整合法候选时，后续 proposal
window 不再继续制造新候选。
