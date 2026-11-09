# 当前代码 DKG 实际流程图（现状版）

这份图描述的是**当前代码实际在跑的路径**，不是目标方案。

重点只有一句话：

- 当前 DKG 主路径是“链上提交 commitment/share + 本地扫描聚合 + 最后提交一笔 validation tx”
- 当前 `KEY_READY` 更接近“链上状态推进成功”，**还不是**“已经被一次真实门限聚合签名验收”

---

## 1. 当前主流程时序图

```mermaid
sequenceDiagram
    participant TW as TransitionWorker
    participant CH as 链上 VM / TxHandler
    participant DB as 链上状态
    participant N as 其他 Committee 节点

    Note over TW,N: Phase 1: 提交 commitments

    TW->>TW: 生成随机多项式 f_i(x)
    TW->>TW: 计算 commitment points, AI0
    TW->>CH: FrostVaultDkgCommitTx
    CH->>DB: 存 commitment\nNOT_STARTED -> COMMITTING

    N->>CH: 其他成员提交 FrostVaultDkgCommitTx
    CH->>DB: 存其他 commitment

    Note over TW,N: Phase 2: 提交 encrypted shares

    TW->>TW: 为每个 receiver 计算 share f_i(j)
    TW->>TW: 用 ECIES 加密 share
    TW->>CH: FrostVaultDkgShareTx(dealer -> receiver)
    CH->>DB: 存 ciphertext\nCOMMITTING -> SHARING

    N->>CH: 其他成员提交给本节点的 share
    CH->>DB: 存其他 ciphertext

    Note over TW,N: Phase 3: 投诉 / reveal 窗口

    opt 有人投诉
        N->>CH: FrostVaultDkgComplaintTx
        CH->>DB: 存 complaint\nSHARING -> RESOLVING
        TW->>CH: FrostVaultDkgRevealTx(share, encRand)
        CH->>DB: 按 reveal 结果处理 complaint
    end

    Note over TW,N: Phase 4: 本地导出 key

    TW->>DB: 扫描本 session 的 commitments
    TW->>DB: 扫描发给自己的 encrypted shares
    TW->>TW: 解密 shares
    TW->>TW: 用 commitment 验证 shares
    TW->>TW: 聚合得到 localShare = Σ f_j(i)
    TW->>TW: 聚合得到 groupPubkey = Σ A_j0

    Note over TW,N: Phase 5: 提交 validation tx

    TW->>TW: msgHash = H(chain, vault, epoch, signAlgo, groupPubkey)
    TW->>TW: signature = SchnorrSign(localShare, msgHash)
    TW->>CH: FrostVaultDkgValidationSignedTx(msgHash, groupPubkey, signature)
    CH->>CH: 校验 sender / msgHash / groupPubkey 推导
    CH->>DB: 直接推进为 KEY_READY
```

---

## 2. 当前状态机

```mermaid
stateDiagram-v2
    [*] --> NOT_STARTED
    NOT_STARTED --> COMMITTING: 第一笔 DkgCommitTx
    NOT_STARTED --> SHARING: 第一笔 DkgShareTx 也可直接推进
    COMMITTING --> SHARING: 第一笔 DkgShareTx
    SHARING --> RESOLVING: DkgComplaintTx
    RESOLVING --> FAILED: reveal 后 qualified_set < t
    SHARING --> KEY_READY: validation tx 通过当前 handler
    RESOLVING --> KEY_READY: validation tx 通过当前 handler
    COMMITTING --> KEY_READY: 当前 handler 也允许
```

这张图里最需要注意的是最后三条：

- 当前没有单独的 `DKG_COMPLETE`
- 当前 `KEY_READY` 不是“真实签名验收成功”后的状态
- 当前 handler 甚至允许从 `COMMITTING / SHARING / RESOLVING` 直接进 `KEY_READY`

---

## 3. 当前 complaint / reveal 实际处理

```mermaid
flowchart TD
    A[收到 DkgComplaintTx] --> B[链上存 complaint]
    B --> C[dealer 提交 DkgRevealTx share + encRand]
    C --> D[verifyRevealInDryRun]

    D -->|当前实现返回 true| E[认定 share 有效]
    E --> F[投诉者 receiver 被视为恶意]
    F --> G[slash receiver bond]
    G --> H[qualified_set 删除 receiver]

    D -->|返回 false| I[认定 dealer 作恶]
    I --> J[slash dealer stake]
    J --> K[qualified_set 删除 dealer]

    H --> L{当前 n < t ?}
    K --> L
    L -->|是| M[DKG FAILED]
    L -->|否| N[继续流程]
```

但当前真正的问题是：

- `verifyRevealInDryRun` 现在基本还是占位实现
- 所以 complaint / reveal 这条分支的密码学裁决并不可靠

---

## 4. 当前 KEY_READY 的真实含义

```mermaid
flowchart LR
    A[链上 commitments 已存在] --> B[VM 重新推导 groupPubkey]
    B --> C[msgHash 匹配]
    C --> D[sender 在 committee 中]
    D --> E[推进 KEY_READY]

    S[tx.signature 字段存在] -.当前生效路径未真正作为完成证明验收.-> E

    style E fill:#ffe8cc,stroke:#d87a00
```

也就是说，当前 `KEY_READY` 更接近：

- `groupPubkey` 可以从链上 commitments 推导出来
- 有人提交了一笔 validation tx
- VM 接受了这次状态推进

它**还不等价于**：

- 新 key 已经完成过一次真实 `t-of-n` ROAST/FROST 聚合签名
- VM 已经验证过那次聚合签名

---

## 5. 当前代码的实际分层

```mermaid
flowchart TD
    A[链上 Tx 驱动 DKG] --> A1[DkgCommitTx]
    A --> A2[DkgShareTx]
    A --> A3[DkgComplaintTx / DkgRevealTx]
    A --> A4[DkgValidationSignedTx]

    B[本地离线计算] --> B1[解密 share]
    B --> B2[验证 share]
    B --> B3[聚合 localShare]
    B --> B4[聚合 groupPubkey]

    A4 --> C[当前 VM 直接置 KEY_READY]

    style C fill:#ffefcc,stroke:#cc8a00
```

这说明当前实现其实是：

- DKG 数学过程：主要在本地算
- DKG 输入材料：主要靠链上 tx 收集
- DKG 最终状态：靠链上 validation tx 推进

---

## 6. 当前版本的三个关键现状

```mermaid
flowchart TB
    A[现状 1] --> A1[share 加密随机数设计有问题]
    B[现状 2] --> B1[reveal 裁决链仍未真正闭环]
    C[现状 3] --> C1[KEY_READY 还不是强语义状态]
```

---

## 7. 一句话版本

```mermaid
flowchart LR
    A[commitments / shares 上链] --> B[本地导出 localShare 和 groupPubkey]
    B --> C[提交一笔 validation tx]
    C --> D[当前 handler 直接置 KEY_READY]
```

这就是当前代码的现状：

- **DKG 是能走通的**
- 但 `KEY_READY` 目前仍然偏“状态推进完成”
- 还不是你想要的“已经被真实聚合签名验收过的新 key”
