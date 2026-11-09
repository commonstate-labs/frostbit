package vm

import (
	"fmt"
	"sort"

	iface "dex/interfaces"
	"dex/keys"
	"dex/pb"
)

                                                                           
                                                                           
                                                                     
func BuildCertificateActiveMiners(tracker iface.LivenessTracker, sv StateView) ([]iface.CertMinerSnapshot, error) {
	if tracker == nil || sv == nil {
		return nil, fmt.Errorf("certificate Active-set source is unavailable")
	}
	addresses := tracker.GetActiveMiners()
	miners := make([]iface.CertMinerSnapshot, 0, len(addresses))
	seenIndexes := make(map[uint32]string, len(addresses))
	seenAddresses := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		if address == "" || !tracker.IsActive(address) {
			continue
		}
		if _, exists := seenAddresses[address]; exists {
			return nil, fmt.Errorf("duplicate Active miner address %q", address)
		}
		snapshot := tracker.GetMinerSnapshot(address)
		if snapshot == nil || snapshot.State != 1 {
			return nil, fmt.Errorf("Active miner %q has inconsistent liveness snapshot", address)
		}
		if prior, exists := seenIndexes[snapshot.Index]; exists {
			return nil, fmt.Errorf("duplicate Active miner index %d for %q and %q", snapshot.Index, prior, address)
		}

		data, ok, err := sv.Get(keys.KeyAccount(address))
		if err != nil {
			return nil, fmt.Errorf("read Active miner account %q: %w", address, err)
		}
		if !ok || len(data) == 0 {
			return nil, fmt.Errorf("Active miner account %q is missing", address)
		}
		var account pb.Account
		if err := unmarshalProtoCompat(data, &account); err != nil {
			return nil, fmt.Errorf("decode Active miner account %q: %w", address, err)
		}
		if account.PublicKeys == nil {
			return nil, fmt.Errorf("Active miner %q has no public keys", address)
		}
		blsKey := account.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_BLS_BN256_G2)]
		if len(blsKey) == 0 {
			return nil, fmt.Errorf("Active miner %q has no BLS public key", address)
		}
		weight, err := parsePositiveBalanceStrict("certificate effective stake", GetBalance(sv, address, "FB").MinerLockedBalance)
		if err != nil {
			return nil, fmt.Errorf("Active miner %q: %w", address, err)
		}
		if !weight.IsUint64() {
			return nil, fmt.Errorf("Active miner %q effective stake exceeds uint64", address)
		}

		seenAddresses[address] = struct{}{}
		seenIndexes[snapshot.Index] = address
		miners = append(miners, iface.CertMinerSnapshot{
			Index:           snapshot.Index,
			Address:         address,
			PublicKey:       append([]byte(nil), blsKey...),
			EffectiveWeight: weight.Uint64(),
		})
	}
	if len(miners) == 0 {
		return nil, fmt.Errorf("no strict Active miners available for certificate chain")
	}
	sort.Slice(miners, func(i, j int) bool { return miners[i].Index < miners[j].Index })
	return miners, nil
}
