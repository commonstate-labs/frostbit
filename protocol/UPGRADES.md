# Binary-governed protocol upgrades

This mechanism changes consensus state without adding governance transactions.
The source-controlled binary is the authority, while activation remains visible
and replayable in StateDB.

## Invariants

- `compiledUpgradeSchedules` is keyed by the immutable network chain ID.
- Successfully applied entries are append-only. Never change an activation
  height, version, or migration ID after its receipt has finalized. If a failed
  rollout lets legacy state cross a planned height without the receipt, the
  replacement schedule must move the still-unapplied migration to a future
  height so historical replay continues to match the legacy blocks.
- Versions are consecutive. A database without `KeyProtocolVersion` is protocol
  v1.
- The VM applies migrations before transactions at the activation height and
  writes both `KeyProtocolVersion` and a permanent upgrade receipt.
- Startup is read-only. It rejects a database newer than the binary and rejects
  a chain that crossed an activation height without the expected receipt.
- A consensus-changing release must be installed on the complete validator set
  before activation. The server5 updater therefore rejects rolling deployment
  while a compiled upgrade is pending.

## Adding an upgrade

1. Increment `CurrentVersion` by one.
2. Add a stable migration ID and its deterministic implementation in
   `vm/protocol_migrations.go`.
3. Append one activation entry for every affected network. Choose a future
   height at which the full validator set can already be running the compatible
   binary.
4. Add migration, idempotency, malformed-state, startup, replay, and deployment
   tests.
5. Deploy with the coordinated updater and verify the on-chain version and
   receipt after the activation block finalizes.

Do not use this mechanism for node-local database tuning, caches, RPC limits, or
monitoring. Those remain ordinary local configuration. Do not use a startup
write or an out-of-band database editor for consensus state migrations.

## Current schedule

No upgrade is scheduled for the fresh `51025` genesis. Its BTC FROST fee is
`25 sat/vB` in genesis; the generic migration machinery remains available for
future upgrades.

The launch network (`chain_id=51025`) has a compiled genesis rule:
every recharge request from its first block prepays at least `0.01 FB` witness
fee and separately locks `10 FB` requester bond. A final rejection burns the
bond; a credited finalization returns it. This rule has no migration receipt or
activation height. Chains `1`, `1001`, and `1002` retain their current request rules.

The same `51025` genesis policy splits each successful verified gross deposit
`G` into receiver net `G-2*floor(G/10000)`, requester reward
`floor(G/10000)`, and an equal total reward for eligible correct witnesses.
Vault available funds, credited funds and bridged token supply each increase
by `G` only. Failed deposits mint no bridged token or success reward.
