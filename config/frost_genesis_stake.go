package config

import (
	"dex/protocol"
	"fmt"
	"math/big"
	"strings"
)

                                                                              
                                                                              
                                                                                
                                                                     
                                                                               
                                                                              
func ValidateFreshFrostGenesisStakePolicy(frost FrostConfig, peers *PeerManifest, genesis *GenesisConfig) error {
	if !frost.Enabled || len(frost.GenesisKeyReuse) != 0 {
		return nil
	}
	if peers == nil || genesis == nil {
		return fmt.Errorf("fresh FROST genesis requires peers and genesis configuration")
	}
	if peers.ChainID == 0 || peers.ChainID != genesis.EffectiveChainID() {
		return fmt.Errorf("fresh FROST genesis chain ID does not match peer manifest")
	}
	seen := make(map[string]struct{}, len(peers.GenesisValidators))
	minimum := new(big.Int).SetUint64(protocol.MinMinerStakeRaw)
	var firstStake *big.Int
	for _, validator := range peers.GenesisValidators {
		if !validator.IsMiner {
			continue
		}
		address := strings.TrimSpace(validator.Address)
		if address == "" || address != validator.Address {
			return fmt.Errorf("fresh FROST genesis requires canonical nonempty miner addresses")
		}
		if _, duplicate := seen[address]; duplicate {
			return fmt.Errorf("fresh FROST genesis repeats miner %s", address)
		}
		seen[address] = struct{}{}
		alloc, exists := genesis.Alloc[address]
		if !exists {
			alloc, exists = genesis.Alloc["default"]
		}
		balance, hasFB := alloc.Balances["FB"]
		if !exists || !hasFB || balance.MinerLockedBalance == "" {
			return fmt.Errorf("fresh FROST genesis miner %s requires FB miner_locked_balance", address)
		}
		stake, valid := new(big.Int).SetString(balance.MinerLockedBalance, 10)
		if !valid || stake.Sign() < 0 || stake.BitLen() > 64 || stake.Cmp(minimum) < 0 {
			return fmt.Errorf("fresh FROST genesis miner %s requires a uint64 FB miner_locked_balance of at least %s", address, minimum)
		}
		if firstStake == nil {
			firstStake = stake
		} else if stake.Cmp(firstStake) != 0 {
			return fmt.Errorf("fresh FROST genesis requires equal FB miner_locked_balance for all validators to preserve the initial TopK; miner %s has %s, expected %s", address, stake, firstStake)
		}
	}
	if firstStake == nil {
		return fmt.Errorf("fresh FROST genesis requires genesis miners")
	}
	return nil
}
