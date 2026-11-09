package vm

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	frostchain "dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

const accountWithdrawFeeVersion uint32 = 1

func accountWithdrawBroadcastFee(gross *big.Int) *big.Int {
	return evm.CalculateBroadcastFee(gross)
}

type accountWithdrawAllocation struct {
	chain       string
	asset       string
	vaultID     uint32
	keyEpoch    uint64
	liveSeq     uint64
	nextCursor  uint32
	available   *big.Int
	reserved    *big.Int
	livePending uint64
}

func (a *accountWithdrawAllocation) writeOps(withdrawID string) []WriteOp {
	if a == nil {
		return nil
	}
	return []WriteOp{
		{Key: keys.KeyFrostVaultAvailableBalance(a.chain, a.asset, a.vaultID), Value: []byte(a.available.String()), Category: "frost_vault_available"},
		{Key: keys.KeyFrostVaultReservedBalance(a.chain, a.asset, a.vaultID), Value: []byte(a.reserved.String()), Category: "frost_vault_reserved"},
		{Key: keys.KeyFrostWithdrawLiveIndex(a.chain, a.vaultID, a.liveSeq), Value: []byte(withdrawID), Category: "frost_withdraw_live"},
		{Key: keys.KeyFrostWithdrawLiveTail(a.chain, a.vaultID), Value: []byte(strconv.FormatUint(a.liveSeq, 10)), Category: "frost_withdraw_live"},
		{Key: keys.KeyFrostWithdrawLivePending(a.chain, a.vaultID), Value: []byte(strconv.FormatUint(a.livePending, 10)), Category: "frost_withdraw_live"},
		{Key: keys.KeyFrostWithdrawRoundRobinCursor(a.chain), Value: []byte(strconv.FormatUint(uint64(a.nextCursor), 10)), Category: "frost_withdraw_rr"},
	}
}

                                                                          
                                                                                
                                                                            
                                                         
func selectAndReserveAccountWithdraw(sv StateView, chainName, asset string, gross *big.Int) (*accountWithdrawAllocation, error) {
	chainName = frostchain.NormalizeChain(chainName)
	if sv == nil || chainName == frostchain.ChainBTC || strings.TrimSpace(asset) == "" || gross == nil || gross.Sign() <= 0 || gross.BitLen() > 256 {
		return nil, fmt.Errorf("invalid account withdrawal allocation")
	}
	cfg, err := loadFrostVaultConfig(sv, chainName)
	if err != nil {
		return nil, err
	}
	if cfg.VaultCount == 0 {
		return nil, fmt.Errorf("no Vault configured for %s", chainName)
	}
	cursorData, _, err := sv.Get(keys.KeyFrostWithdrawRoundRobinCursor(chainName))
	if err != nil {
		return nil, err
	}
	cursor, err := parseOptionalUint64(cursorData)
	if err != nil {
		return nil, fmt.Errorf("invalid account withdrawal round-robin cursor: %w", err)
	}
	start := uint32(cursor % uint64(cfg.VaultCount))

	stateKeys := make([]string, cfg.VaultCount)
	for id := uint32(0); id < cfg.VaultCount; id++ {
		stateKeys[id] = keys.KeyFrostVaultState(chainName, id)
	}
	states, err := frostStateGetMany(sv, stateKeys)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		id    uint32
		epoch uint64
	}
	candidates := make([]candidate, 0, cfg.VaultCount)
	transitionKeys := make([]string, 0, cfg.VaultCount)
	for offset := uint32(0); offset < cfg.VaultCount; offset++ {
		id := (start + offset) % cfg.VaultCount
		data := states[stateKeys[id]]
		if len(data) == 0 {
			continue
		}
		var vault pb.FrostVaultState
		if proto.Unmarshal(data, &vault) != nil || vault.VaultId != id || frostchain.NormalizeChain(vault.Chain) != chainName || vault.Status != "ACTIVE" || vault.KeyEpoch == 0 || vault.KeyEpoch == ^uint64(0) {
			continue
		}
		candidates = append(candidates, candidate{id: id, epoch: vault.KeyEpoch})
		transitionKeys = append(transitionKeys, keys.KeyFrostVaultTransition(chainName, id, vault.KeyEpoch+1))
	}
	transitions, err := frostStateGetMany(sv, transitionKeys)
	if err != nil {
		return nil, err
	}
	eligible := candidates[:0]
	for _, c := range candidates {
		if data := transitions[keys.KeyFrostVaultTransition(chainName, c.id, c.epoch+1)]; len(data) > 0 {
			var transition pb.VaultTransitionState
			if proto.Unmarshal(data, &transition) != nil || transition.Lifecycle == "DRAINING" {
				continue
			}
		}
		eligible = append(eligible, c)
	}
	if len(eligible) == 0 {
		return nil, fmt.Errorf("no ACTIVE Vault available for %s", chainName)
	}

	aggregateKeys := make([]string, 0, len(eligible)*2+3)
	for _, c := range eligible {
		aggregateKeys = append(aggregateKeys,
			keys.KeyFrostVaultAvailableBalance(chainName, asset, c.id),
			keys.KeyFrostVaultReservedBalance(chainName, asset, c.id),
		)
	}
	aggregates, err := frostStateGetMany(sv, aggregateKeys)
	if err != nil {
		return nil, err
	}
	for _, c := range eligible {
		available, err := parseStateUint256(aggregates[keys.KeyFrostVaultAvailableBalance(chainName, asset, c.id)])
		if err != nil {
			return nil, fmt.Errorf("invalid available balance for vault %d: %w", c.id, err)
		}
		if available.Cmp(gross) < 0 {
			continue
		}
		reserved, err := parseStateUint256(aggregates[keys.KeyFrostVaultReservedBalance(chainName, asset, c.id)])
		if err != nil {
			return nil, fmt.Errorf("invalid reserved balance for vault %d: %w", c.id, err)
		}
		reserved.Add(reserved, gross)
		if reserved.BitLen() > 256 {
			return nil, fmt.Errorf("reserved balance overflows uint256 for vault %d", c.id)
		}
		queueKeys := []string{
			keys.KeyFrostWithdrawLiveTail(chainName, c.id),
			keys.KeyFrostWithdrawLivePending(chainName, c.id),
		}
		queue, err := frostStateGetMany(sv, queueKeys)
		if err != nil {
			return nil, err
		}
		tail, err := parseOptionalUint64(queue[queueKeys[0]])
		if err != nil || tail == ^uint64(0) {
			return nil, fmt.Errorf("invalid or exhausted live FIFO for vault %d", c.id)
		}
		pending, err := parseOptionalUint64(queue[queueKeys[1]])
		if err != nil || pending == ^uint64(0) {
			return nil, fmt.Errorf("invalid or exhausted pending counter for vault %d", c.id)
		}
		return &accountWithdrawAllocation{
			chain: chainName, asset: asset, vaultID: c.id, keyEpoch: c.epoch,
			liveSeq: tail + 1, nextCursor: (c.id + 1) % cfg.VaultCount,
			available: new(big.Int).Sub(available, gross), reserved: reserved, livePending: pending + 1, // safe-math-ok: available 已按 uint256 解析且比较不小于 gross
		}, nil
	}
	return nil, fmt.Errorf("insufficient available Vault balance for %s/%s gross=%s", chainName, asset, gross)
}

func frostStateGetMany(sv StateView, stateKeys []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(stateKeys))
	if len(stateKeys) == 0 {
		return out, nil
	}
	if batch, ok := sv.(interface {
		GetMany([]string) (map[string][]byte, error)
	}); ok {
		return batch.GetMany(stateKeys)
	}
	for _, key := range stateKeys {
		if key == "" {
			continue
		}
		value, exists, err := sv.Get(key)
		if err != nil {
			return nil, err
		}
		if exists {
			out[key] = value
		}
	}
	return out, nil
}

func parseStateUint256(data []byte) (*big.Int, error) {
	if len(data) == 0 {
		return big.NewInt(0), nil
	}
	raw := string(data)
	if strings.TrimSpace(raw) != raw || raw == "" || (len(raw) > 1 && raw[0] == '0') {
		return nil, fmt.Errorf("non-canonical uint256")
	}
	v, ok := new(big.Int).SetString(raw, 10)
	if !ok || v.Sign() < 0 || v.BitLen() > 256 {
		return nil, fmt.Errorf("invalid uint256")
	}
	return v, nil
}

func parseOptionalUint64(data []byte) (uint64, error) {
	if len(data) == 0 {
		return 0, nil
	}
	raw := string(data)
	if strings.TrimSpace(raw) != raw || (len(raw) > 1 && raw[0] == '0') {
		return 0, fmt.Errorf("non-canonical uint64")
	}
	return strconv.ParseUint(raw, 10, 64)
}

func accountWithdrawCancelQueueOps(sv StateView, withdraw *FrostWithdrawRequest, gross *big.Int) ([]WriteOp, error) {
	if withdraw == nil || gross == nil || gross.Sign() <= 0 || frostchain.NormalizeChain(withdraw.Chain) == frostchain.ChainBTC {
		return nil, fmt.Errorf("invalid account withdrawal cancellation")
	}
	chainName := frostchain.NormalizeChain(withdraw.Chain)
	stateKeys := []string{
		keys.KeyFrostVaultAvailableBalance(chainName, withdraw.Asset, withdraw.VaultID),
		keys.KeyFrostVaultReservedBalance(chainName, withdraw.Asset, withdraw.VaultID),
		keys.KeyFrostWithdrawLivePending(chainName, withdraw.VaultID),
		keys.KeyFrostWithdrawLiveHead(chainName, withdraw.VaultID),
		keys.KeyFrostWithdrawLiveTail(chainName, withdraw.VaultID),
	}
	values, err := frostStateGetMany(sv, stateKeys)
	if err != nil {
		return nil, err
	}
	available, err := parseStateUint256(values[stateKeys[0]])
	if err != nil {
		return nil, err
	}
	reserved, err := parseStateUint256(values[stateKeys[1]])
	if err != nil || reserved.Cmp(gross) < 0 {
		return nil, fmt.Errorf("account withdrawal reservation is insufficient")
	}
	pending, err := parseOptionalUint64(values[stateKeys[2]])
	if err != nil || pending == 0 {
		return nil, fmt.Errorf("account withdrawal pending counter is invalid")
	}
	available.Add(available, gross)
	if available.BitLen() > 256 {
		return nil, fmt.Errorf("account withdrawal available balance overflows")
	}
	reserved.Sub(reserved, gross)
	ops := []WriteOp{
		{Key: stateKeys[0], Value: []byte(available.String()), Category: "frost_vault_available"},
		{Key: stateKeys[1], Value: []byte(reserved.String()), Category: "frost_vault_reserved"},
		{Key: stateKeys[2], Value: []byte(strconv.FormatUint(pending-1, 10)), Category: "frost_withdraw_live"},
		deleteWriteOp(keys.KeyFrostWithdrawLiveIndex(chainName, withdraw.VaultID, withdraw.LiveSeq), "frost_withdraw_live"),
	}
	head, err := parseOptionalUint64(values[stateKeys[3]])
	if err != nil {
		return nil, fmt.Errorf("invalid account withdrawal FIFO head: %w", err)
	}
	if head == 0 {
		head = 1
	}
	tail, err := parseOptionalUint64(values[stateKeys[4]])
	if err != nil || tail < head || withdraw.LiveSeq < head || withdraw.LiveSeq > tail {
		return nil, fmt.Errorf("invalid account withdrawal FIFO bounds")
	}
	if withdraw.LiveSeq == head {
		if head == ^uint64(0) {
			return nil, fmt.Errorf("account withdrawal FIFO head exhausted")
		}
		next, err := nextMixedLiveHead(sv, chainName, withdraw.VaultID, head+1, tail)
		if err != nil {
			return nil, err
		}
		ops = append(ops, WriteOp{Key: stateKeys[3], Value: []byte(strconv.FormatUint(next, 10)), Category: "frost_withdraw_live"})
	}
	return ops, nil
}
