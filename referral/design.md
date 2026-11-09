# 邀请返现模块 (referral) — 顶层设计

## 目标

用户可绑定邀请者地址；永续合约成交时收取 trading fee，按比例分配给邀请者、被邀请者（折扣）、保险基金。

## 费用流转

```
成交名义价值 = qty × price

trading fee = notional × FeeRate / BPS

有邀请关系时：
  ├─ 20% → 邀请者（referrer rebate）
  ├─ 10% → 被邀请者（fee discount，即实际只扣 90% 的 fee）
  └─ 70% → 保险基金

无邀请关系时：
  └─ 100% → 保险基金
```

## 数据模型

### 邀请关系（一次性绑定，不可更改）

```
KeyReferrer(userAddr) → referrerAddr
```

- 每个地址最多绑定一个邀请者
- 绑定后不可更改、不可解除
- 不能自己邀请自己
- 不做链式返现（A 邀请 B，B 邀请 C，A 不从 C 获益）

### 全局配置

```go
type ReferralConfig struct {
    TradingFeeRate     uint64 // 交易手续费率 bps（如 5 = 0.05%）
    ReferrerRebateRate uint64 // 邀请者返现比例 bps（如 2000 = 20%）
    RefereeDiscountRate uint64 // 被邀请者折扣比例 bps（如 1000 = 10%）
}
```

## 模块结构

```
referral/
├── types.go        // ReferralConfig, FeeResult
├── interfaces.go   // ReferrerStore, BalanceAccessor, InsuranceFundDepositor
├── engine.go       // Engine: CalcAndDistributeFee
└── engine_test.go
```

### 接口定义（interfaces.go）

```go
// ReferrerStore 邀请关系读写
type ReferrerStore interface {
    GetReferrer(user string) (referrer string, exists bool, err error)
    SetReferrer(user, referrer string) error
}

// BalanceAccessor 余额操作（复用 future 同名接口语义）
type BalanceAccessor interface {
    DeductBalance(owner, token string, amount *big.Int) error
    AddBalance(owner, token string, amount *big.Int) error
}

// InsuranceFundDepositor 保险基金注入
type InsuranceFundDepositor interface {
    DepositInsuranceFund(pair string, amount *big.Int) error
}
```

### 核心引擎（engine.go）

```go
type Engine struct {
    referrers ReferrerStore
    balance   BalanceAccessor
    insurance InsuranceFundDepositor
    config    ReferralConfig
}

// CalcAndDistributeFee 成交后调用，扣费并分配
// 返回 FeeResult 供回执记录
func (e *Engine) CalcAndDistributeFee(
    trader, pair, quoteToken string,
    notional *big.Int,
) (*FeeResult, error)
```

### 结果类型（types.go）

```go
type FeeResult struct {
    Trader          string
    TotalFee        *big.Int // 实际扣除总额（有折扣时 = 90% of full fee）
    ReferrerRebate  *big.Int // 邀请者获得
    InsuranceAmount *big.Int // 保险基金获得
    Referrer        string   // 邀请者地址（空 = 无邀请关系）
}
```

## VM 集成

### 新增 TxHandler：SetReferrerTxHandler

- proto: `SetReferrerTx { referrer_address string }`
- 验证：referrer 地址有效、非自己、未绑定过
- 写入 `KeyReferrer(fromAddr) → referrer_address`

### 撮合成交时调用

在 `vm/matching_sv.go` 的 `MatchFutureOrder` 成交回调中：

```
成交 → Engine.ProcessTradeForPosition（已有）
     → referral.Engine.CalcAndDistributeFee（新增）
```

对 taker 和 maker 双方各调用一次。

## Key Schema

```go
// keys/keys.go 新增
var pfxReferrer = regState("referrer_", "邀请关系绑定")
func KeyReferrer(userAddr string) string { return pfxReferrer + userAddr }
```

## 不做的事

- 不做链式/多级返现
- 不做分级费率（后续可扩展 ReferralConfig）
- 不从 tx gas fee (FB) 中分成，只从 trading fee 中分
- 不做返现上限/锁仓
