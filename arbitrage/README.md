# Miner arbitrage machine

`arbitrage` is a node-local, proposer-only strategy component. It does not
enumerate configured token routes, own VM matching logic, submit transactions
to the txpool, or broadcast transactions independently.

The proposal path is:

1. Consensus builds the same guard-valid, nonce-ready ordinary transaction
   body it would normally propose, then withholds a suffix of at most 100
   transactions while projecting proposer-private opportunities. The final
   merge restores every unused suffix slot, so a three-leg candidate in a full
   block produces 4,997 public transactions plus three private transactions
   rather than leaving 97 positions empty.
2. The machine groups successful spot and futures limit orders already present
   in that exact ordered public prefix. It does not scan configured token
   combinations. Market orders, closes/cancels, self-owned orders, invalid
   execution guards, and malformed pairs are ignored.
3. For an earlier public ask and later crossed public bid, it schedules
   `ask -> private BUY -> bid -> private SELL`. For the reverse public order it
   schedules `bid -> private SELL -> ask -> private BUY`. Public transactions
   keep their original relative order; only the two private insertion points
   are added. On futures, the first private order opens the position and the
   second is an ordinary `is_close=true` IOC reduce-only order against the real
   book, so no private position or resting close order remains after success.
4. Both directions are sized from the independent arbitrage account's actual
   available balance at the first private insertion point, with two FB runtime
   fees reserved separately (or deducted from capital when the start token is
   FB). Futures sizing also includes initial margin and both real-book taker
   fees. A later public transfer can neither fund an earlier private order nor
   invalidate the balance timeline silently. Partial sizing is allowed only
   when the later public order can clear the first maker's remainder and still
   leave the exact quantity required by the second private leg.
5. Compatible intersections are combined into one portfolio by a bounded
   state beam. Every state tracks the funding account's spendable token
   balances, two-leg fee reserve, pending second-leg input, used maker IDs, and
   active semantic orderbooks. Independent orderbooks may therefore have
   nested or crossing intervals. One orderbook is not opened twice before its
   earlier private pair restores the traced baseline, and no public maker is
   reused. Each opportunity is sized from the balance available at its actual
   first-leg position. The body cutoff includes its `2*k` private transactions.
6. The detached VM projection of the ordinary prefix remains available for the
   existing two- or three-leg closed-route strategy. The combined cross-order
   portfolio and best post-prefix closed route compete before signing; only the
   global winner can enter the block.
7. Consensus weaves that winner into the public body, recomputes its full
   commitment, and simulates the complete block once. It publishes the candidate
   only when every private leg succeeds, fills completely against the exact
   intended public maker IDs, and meets the minimum actual return. Otherwise it
   publishes the unchanged ordinary candidate.

`baseToken` and `quoteToken` stay as a structured tuple throughout discovery.
`utils.GeneratePairKey(baseToken, quoteToken)` is used only for VM orderbook
lookups; no code tries to recover tokens by splitting a pair key. This preserves
the VM's semantic `base_quote` order and supports dynamically created tokens.

Example node-local policy:

```json
{
  "Arbitrage": {
    "enabled": true,
    "txFee": "1000",
    "capitalBps": 5000,
    "minProfitBps": 10,
    "maxOrdersPerBook": 64,
    "maxCycleEvaluations": 4096
  }
}
```

The policy contains no private key; secret-like or unknown fields under
`Arbitrage` are rejected at startup. On Unix, pass the independent account key
through its own inherited descriptor:

```sh
exec 3<./miner.key
exec 4<./arbitrage-account.key
./dex-node --private-key-fd 3 --arbitrage-private-key-fd 4
```

For Windows local debugging, use a separate, explicitly opted-in directory
containing exactly one canonical `<arbitrage-address>.key` file and pass
`--windows-arbitrage-secret-dir`. `--expected-arbitrage-address` can pin the
derived public address on either platform. The derived arbitrage address must
differ from the miner address; startup fails instead of silently falling back
to miner capital when the same key is supplied twice.

- `capitalBps` is the maximum share of the arbitrage account's currently
  spendable balance in a route's start token used when that group's first leg is
  inserted. The search withholds the second-leg input and both FB fees at that
  point, so overlapping groups cannot double-spend them. Capital and profit from
  a group that closes before a later group starts become available to that later
  sizing decision.
- `minProfitBps` is checked first against the quote and again against actual VM
  match events during full-candidate preflight.
- `maxOrdersPerBook` and `maxCycleEvaluations` bound proposal-time work under a
  large or adversarial txpool. Cross comparisons consume the latter budget in
  deterministic round-robin order across semantic pairs, preventing one busy
  pair from starving every other funded token.
- Amounts and fees are chain raw integers; price uses the VM's `1e8` scale.
  The arbitrage account must also have enough available FB for one runtime fee
  per leg.

Routes may start and end in any token already held by the arbitrage account.
Runtime fees remain denominated in FB, so every candidate also requires enough
available FB to pay all leg fees. For an FB route, `minProfitBps` is net of those
fees. For another start token, it is token-native route ROI and does not convert
the separate FB fee cost into that token; operators should set a conservative
threshold until a trusted cross-token valuation source is injected.

Every generated leg is a non-market limit order with consecutive account
nonces, exact `target_height`, and exact `only_proposer`; `MaxSlippageBps` stays
zero because execution is bounded by the order's limit price. The public-prefix
hash and count bind discovery, cache entries, private transactions, insertion
points, intended maker IDs, and final assembly to the same ordered txpool
snapshot.

`FromAddress`, balance reads, nonce reads, self-maker checks, fees, and order
signatures all use the arbitrage account. `only_proposer` alone uses the miner
address, because it answers a different question: which miner may include the
already-signed private order at the target height.

This design deliberately adds no `ArbitrageBundleTx` and no VM-level atomic
wrapper. Each leg remains an ordinary transaction. Atomicity is a local block
construction policy: the proposer only publishes a fully simulated candidate,
and otherwise publishes the ordinary body. Validators simply execute and
commit the ordered transactions already covered by `TxsHash` and the BlockID.

The current policy first executes the exact ordered public prefix once in a
detached VM StateView. After every successful public spot or futures limit open,
the VM records its real remaining quantity, ordered maker-match events, and one
real best maker in each taker direction. Futures traces additionally carry the
arbitrage account's quote and FB available balances at that exact insertion
boundary. Cross discovery is disabled when this trace is absent; submitted
quantity/price fields alone are not execution evidence.

The defined cross candidate set is deliberately conservative. The earlier
public order must really remain at the front of its book, and the later opposite
order must consume that entire real remainder as its final maker. The later
order must then be the next executable maker (or have no blocking price if it
was terminal). Intervals on different semantic pairs may overlap; intervals on
one pair are never open simultaneously. Forward crosses return quote capital
plus quote profit. For a reverse cross, the machine automatically compares two
settlements at the traced low ask's own price: buy back exactly the sold base and
retain quote profit, or spend the proceeds against that ask's real remaining
depth and retain incremental base. Equal quote-equivalent value prefers base.
The first form restores the public-trace remainder. The reinvestment form changes
the book, so the planner marks that semantic pair dirty and does not use later
opportunities derived from its old trace.

Within that candidate set, the selector uses a 16-state beam. At each opportunity
it retains both skip and take branches, advances completed second legs, resizes
the take branch from its live spendable balance, and prunes by realized utility
plus deterministic remaining potential. Cross-token wallet-relative utility
uses a 512-bit fixed-point score; within one profit token its precision preserves
native raw-profit ordering. Normal search work is
`O(opportunities * 16)` lightweight state transitions, in addition to one public
projection and one final complete-block VM preflight. It does not re-execute the
whole block for every branch.

Only `maxCycleEvaluations` order-pair comparisons are considered. An extreme
custom budget above the fixed transition bound is deterministically sampled to
keep proposal time predictable. Beam selection targets most realizable profit;
it is not a mathematical global-optimum claim over all subsets or uint256-sized
partial fills. The planner never shares one public order across groups and does
not overlap one semantic orderbook. Coupling through public counterparties or
other VM state remains guarded by the one authoritative full-candidate preflight.

The best restoring cross portfolio and the best post-prefix closed route compete
in the finder. Only the global winner is signed. Consensus performs one complete
candidate VM preflight (including hooks and conservation audits); if it fails,
the proposer emits the ordinary public body rather than simulating strategy
fallbacks.

`target_height` and `only_proposer` are signed consensus fields, so this is a
protocol upgrade rather than only a local plugin switch. Keep
`Arbitrage.enabled=false` during a rolling upgrade. Once a candidate is
published, other miners can see and independently recreate the opportunity,
but they cannot validly reuse these signed legs at another height or under a
different proposer.
