package vm

import (
	"dex/config"
	frostchain "dex/frost/chain"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/utils"
	"fmt"
	"sort"
	"strings"
)

func validateAccountChainRecharge(cfg *pb.FrostVaultConfig, request *pb.WitnessRequestTx) error {
	if cfg == nil || request == nil || request.Base == nil {
		return fmt.Errorf("missing account-chain recharge configuration or request")
	}
	if request.DepositProtocol != protocol.DepositProtocolVault || request.DepositProtocol != cfg.DepositProtocol {
		return fmt.Errorf("account-chain recharge protocol does not match the configured receiver-bound entry")
	}
	if err := config.ValidateDepositConfig(cfg.Chain, cfg.DepositProtocol, cfg.DepositDexChainId, cfg.VaultRefs); err != nil {
		return err
	}
	if request.Base.ChainId != cfg.DepositDexChainId {
		return fmt.Errorf("deposit entry targets a different DEX chain")
	}
	if !utils.IsDexAddress(request.ReceiverAddress) {
		return fmt.Errorf("deposit receiver must be a canonical DEX address")
	}
	if request.VaultId >= cfg.VaultCount || int(request.VaultId) >= len(cfg.VaultRefs) || cfg.VaultRefs[request.VaultId] == "" {
		return fmt.Errorf("deposit vault is not configured")
	}
	if request.NativeVout != 0 || len(request.NativeScript) != 0 {
		return fmt.Errorf("account-chain deposits must not contain BTC output fields")
	}
	return nil
}

                                                                                     
                                                                              
                                                                           
func ensureLegacyRechargeIndex(sv StateView) error {
	if _, ok, err := sv.Get(keys.KeyRechargeLegacyMigration()); err != nil {
		return err
	} else if ok {
		return nil
	}
	rows, err := sv.Scan(keys.KeyRechargeNativePrefix())
	if err != nil {
		return err
	}
	ordered := make([]string, 0, len(rows))
	for key := range rows {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		suffix := strings.TrimPrefix(key, keys.KeyRechargeNativePrefix())
		name, rawHash, found := strings.Cut(suffix, "_")
		if !found || name == "btc" {
			continue
		}
		canonical, err := protocol.NormalizeExternalTxHash(name, rawHash)
		if err != nil {
			continue
		}
		guardKey := keys.KeyRechargeLegacyNative(name, canonical)
		value := rows[key]
		if len(value) == 0 {
			value = []byte("legacy-reserved")
		}
		if old, exists, err := sv.Get(guardKey); err != nil {
			return err
		} else if exists && string(old) != string(value) {
			value = []byte("legacy-conflict")
		}
		sv.Set(guardKey, value)
	}
	sv.Set(keys.KeyRechargeLegacyMigration(), []byte("1"))
	return nil
}

                                                                              
                                                                             
                                                                            
                                                                            
func ensureGlobalRechargePendingIndex(sv StateView) error {
	if _, ok, err := sv.Get(keys.KeyRechargePendingMigration()); err != nil {
		return err
	} else if ok {
		return nil
	}
	rows, err := sv.Scan(keys.KeyRechargeRequestPrefix())
	if err != nil {
		return err
	}
	type candidate struct {
		requestID   string
		height      uint64
		consumedKey string
	}
	winners := make(map[string]candidate)
	for _, requestKey := range sortedBytesMapKeys(rows) {
		var request pb.RechargeRequest
		if err := unmarshalProtoCompat(rows[requestKey], &request); err != nil {
			return fmt.Errorf("decode recharge request %s: %w", requestKey, err)
		}
		if request.RequestId == "" || requestKey != keys.KeyRechargeRequest(request.RequestId) {
			return fmt.Errorf("recharge request key identity mismatch: %s", requestKey)
		}
		if rechargeRequestTerminal(request.Status) || isBTCChainName(request.NativeChain) ||
			!protocol.IsBoundDepositProtocol(request.DepositProtocol) {
			continue
		}
		chainName := frostchain.NormalizeChain(request.NativeChain)
		canonicalHash, err := protocol.NormalizeExternalTxHash(chainName, request.NativeTxHash)
		if err != nil {
			return fmt.Errorf("normalize recharge request %s event: %w", request.RequestId, err)
		}
		eventKey := keys.KeyRechargeEventPending(chainName, canonicalHash, request.DepositEventIndex)
		current, exists := winners[eventKey]
		if !exists || request.CreateHeight < current.height || (request.CreateHeight == current.height && request.RequestId < current.requestID) {
			winners[eventKey] = candidate{
				requestID: request.RequestId, height: request.CreateHeight,
				consumedKey: keys.KeyRechargeEvent(chainName, canonicalHash, request.DepositEventIndex),
			}
		}
	}
	legacyLocks, err := sv.Scan(keys.KeyRechargeEventPendingPrefix())
	if err != nil {
		return err
	}
	for key := range legacyLocks {
		sv.Del(key)
	}
	eventKeys := make([]string, 0, len(winners))
	for key := range winners {
		eventKeys = append(eventKeys, key)
	}
	sort.Strings(eventKeys)
	for _, key := range eventKeys {
		if _, consumed, err := sv.Get(winners[key].consumedKey); err != nil {
			return err
		} else if consumed {
			continue
		}
		sv.Set(key, []byte(winners[key].requestID))
	}
	sv.Set(keys.KeyRechargePendingMigration(), []byte("1"))
	return nil
}

func accountChainRechargePendingKey(req *pb.RechargeRequest) string {
	return keys.KeyRechargeEventPending(req.NativeChain, req.NativeTxHash, req.DepositEventIndex)
}

                                                                                
                                                                                  
func rejectUncreditableRecharge(sv StateView, stored *pb.RechargeRequest, height, mix uint64) error {
	stored.FinalizeHeight = height
	return applyRechargeRejected(sv, stored, mix)
}

func checkAccountChainRechargeFinalization(sv StateView, stored *pb.RechargeRequest) (bool, error) {
	if !protocol.IsBoundDepositProtocol(stored.DepositProtocol) || !utils.IsDexAddress(stored.ReceiverAddress) {
		return false, nil
	}
	if _, err := protocol.CanonicalExternalTxHash(stored.NativeChain, stored.NativeTxHash); err != nil {
		return false, nil
	}
	cfg, err := loadFrostVaultConfig(sv, stored.NativeChain)
	if err != nil {
		return false, err
	}
	if err := validateAccountChainRecharge(cfg, &pb.WitnessRequestTx{
		Base: &pb.BaseMessage{ChainId: cfg.DepositDexChainId}, DepositProtocol: stored.DepositProtocol,
		ReceiverAddress: stored.ReceiverAddress, VaultId: stored.VaultId,
		NativeVout: stored.NativeVout, NativeScript: stored.NativeScript,
	}); err != nil {
		return false, nil
	}
	if err := ensureLegacyRechargeIndex(sv); err != nil {
		return false, err
	}
	if _, exists, err := sv.Get(keys.KeyRechargeLegacyNative(stored.NativeChain, stored.NativeTxHash)); err != nil {
		return false, err
	} else if exists {
		return false, nil
	}
	if _, exists, err := sv.Get(keys.KeyRechargeEvent(stored.NativeChain, stored.NativeTxHash, stored.DepositEventIndex)); err != nil {
		return false, err
	} else if exists {
		return false, nil
	}
	owner, exists, err := sv.Get(accountChainRechargePendingKey(stored))
	if err != nil {
		return false, err
	}
	return exists && string(owner) == stored.RequestId, nil
}
