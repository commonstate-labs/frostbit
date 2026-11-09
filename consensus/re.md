# 同步状态机与 accepted-frontier

```mermaid
flowchart TD
    A[收到 Chit 或 HeightResponse 的领先高度] --> B{是否超过深同步阈值}
    B -- 否 --> F[建立浅同步或普通追块 session]
    B -- 是 --> C{是否有足够独立 peer<br/>确认保守深度高度}
    C -- 否 --> C1[仅保留高度提示<br/>继续等待独立确认]
    C -- 是 --> D[连续验证 CheckpointCertificate 链]
    D --> E[下载并校验 StateDB checkpoint<br/>安装 certified anchor]
    E --> F

    F --> G[以本地 finalized tip 为 anchor<br/>选定本批固定 target]
    G --> H[向固定 K 个不同 miner<br/>并发发送签名 frontier 请求]
    H --> I[单次响应同时返回<br/>acceptedHeight / observedAnchorID<br/>targetHeight / targetBlockID]
    I --> J{本轮结果}

    J -- Alpha 个支持同一 target ID --> K[只从这些 supporter 中<br/>下载 anchor+1..target 完整区块]
    J -- Alpha 个 anchor mismatch --> X[保留本地 finalized 状态<br/>触发全局安全停机]
    J -- Alpha 个节点低于 target --> L[按 Alpha 可用高度降低 target<br/>更换 nonce 重新采样]
    J -- 未形成 Alpha --> M[本轮失败或超时<br/>更换 nonce 重试]
    L --> H
    M --> H

    K --> N[整批验证 commitment / parent / pre-state root<br/>proposer / VRF / eligibility / target 末端]
    N --> O[按高度串行 SetFinalized]
    O --> P{仍然落后}
    P -- 是 --> G
    P -- 否 --> Q[同步完成]
```

`CheckpointCertificate` 只为深度同步的 checkpoint 提供可持久化信任锚，不为逐块追块背书。浅同步、普通追块和 checkpoint 后尾追都经过同一个 accepted-frontier 门禁；frontier 响应只属于当前 nonce 轮次，不持久化、不 gossip，也不能跨轮合票或包装成可转移证书。
