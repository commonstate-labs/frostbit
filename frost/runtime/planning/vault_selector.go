                                           
                              

package planning

import (
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	chainpkg "dex/frost/chain"
	"dex/keys"
	"dex/logs"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

type accountVaultCandidate struct {
	id       uint32
	epoch    uint64
	maxItems int
}

                                                                               
                                                                               
                                                                             
                                                                              
func (p *JobWindowPlanner) selectAccountVaultForPrefix(chain, asset string, prefixTotals []*big.Int) (uint32, uint64, int, error) {
	if len(prefixTotals) == 0 || prefixTotals[0] == nil || prefixTotals[0].Sign() <= 0 {
		return 0, 1, 0, fmt.Errorf("select account vault failed: invalid prefix totals")
	}
	cfgData, exists, err := p.stateReader.Get(keys.KeyFrostVaultConfig(chain, 0))
	if err != nil {
		return 0, 1, 0, err
	}
	vaultCount := uint32(1)
	if exists && len(cfgData) > 0 {
		var cfg pb.FrostVaultConfig
		if proto.Unmarshal(cfgData, &cfg) == nil && cfg.VaultCount > 0 {
			vaultCount = cfg.VaultCount
		}
	}
	stateKeys := make([]string, vaultCount)
	for id := uint32(0); id < vaultCount; id++ {
		stateKeys[id] = keys.KeyFrostVaultState(chain, id)
	}
	stateValues, err := getMany(p.stateReader, stateKeys)
	if err != nil {
		return 0, 1, 0, err
	}
	candidates := make([]accountVaultCandidate, 0, vaultCount)
	transitionKeys := make([]string, 0, vaultCount)
	for id := uint32(0); id < vaultCount; id++ {
		data := stateValues[stateKeys[id]]
		if len(data) == 0 {
			continue
		}
		var state pb.FrostVaultState
		if proto.Unmarshal(data, &state) != nil || state.Status != "ACTIVE" || state.KeyEpoch == ^uint64(0) {
			continue
		}
		candidates = append(candidates, accountVaultCandidate{id: id, epoch: state.KeyEpoch})
		transitionKeys = append(transitionKeys, keys.KeyFrostVaultTransition(chain, id, state.KeyEpoch+1))
	}
	transitionValues, err := getMany(p.stateReader, transitionKeys)
	if err != nil {
		return 0, 1, 0, err
	}
	active := candidates[:0]
	for _, candidate := range candidates {
		data := transitionValues[keys.KeyFrostVaultTransition(chain, candidate.id, candidate.epoch+1)]
		if len(data) > 0 {
			var transition pb.VaultTransitionState
			if proto.Unmarshal(data, &transition) != nil || transition.Lifecycle == "DRAINING" {
				continue
			}
		}
		active = append(active, candidate)
	}
	if len(active) == 0 {
		return 0, 1, 0, fmt.Errorf("select account vault failed: no ACTIVE vault for chain=%s (vault_count=%d)", chain, vaultCount)
	}
	balanceKeys := make([]string, len(active))
	for i, candidate := range active {
		balanceKeys[i] = keys.KeyFrostVaultAvailableBalance(chain, asset, candidate.id)
	}
	balanceValues, err := getMany(p.stateReader, balanceKeys)
	if err != nil {
		return 0, 1, 0, err
	}
	best := accountVaultCandidate{maxItems: 0}
	maxAvailable := big.NewInt(0)
	for i := range active {
		available := big.NewInt(0)
		if data := balanceValues[balanceKeys[i]]; len(data) > 0 {
			available, err = parseNonNegativeAmount(string(data))
			if err != nil {
				return 0, 1, 0, fmt.Errorf("invalid aggregate vault balance for %s/%s vault=%d: %w", chain, asset, active[i].id, err)
			}
		}
		if p.transientCache != nil {
			p.transientCache[active[i].id] = chainpkg.CloneAmount(available)
		}
		if available.Cmp(maxAvailable) > 0 {
			maxAvailable = chainpkg.CloneAmount(available)
		}
		active[i].maxItems = sort.Search(len(prefixTotals), func(index int) bool {
			return prefixTotals[index].Cmp(available) > 0
		})
		if active[i].maxItems > best.maxItems {
			best = active[i]
		}
	}
	if best.maxItems == 0 {
		return 0, 1, 0, fmt.Errorf("select account vault failed: insufficient balance for %s/%s first=%s max_available=%s active_count=%d", chain, asset, prefixTotals[0], maxAvailable, len(active))
	}
	return best.id, best.epoch, best.maxItems, nil
}

                                                                                
func (p *JobWindowPlanner) selectVault(chain, asset string, amount *big.Int) (vaultID uint32, keyEpoch uint64, err error) {
	if amount == nil || amount.Sign() <= 0 || amount.BitLen() > 256 {
		return 0, 1, fmt.Errorf("select vault failed: invalid amount")
	}
	                                        
	vaultCfgKey := keys.KeyFrostVaultConfig(chain, 0)
	vaultCfgData, exists, err := p.stateReader.Get(vaultCfgKey)
	if err != nil {
		return 0, 1, err
	}

	var vaultCount uint32 = 1             
	if exists && len(vaultCfgData) > 0 {
		var cfg pb.FrostVaultConfig
		if err := proto.Unmarshal(vaultCfgData, &cfg); err == nil {
			vaultCount = cfg.VaultCount
			if vaultCount == 0 {
				vaultCount = 1
			}
		}
	}

	                                                               
	activeCount := 0
	maxAvailable := big.NewInt(0)
	var maxAvailableVault uint32
	for id := uint32(0); id < vaultCount; id++ {
		vaultStateKey := keys.KeyFrostVaultState(chain, id)
		vaultStateData, exists, err := p.stateReader.Get(vaultStateKey)
		if err != nil {
			continue
		}
		if !exists || len(vaultStateData) == 0 {
			continue
		}

		var state pb.FrostVaultState
		if err := proto.Unmarshal(vaultStateData, &state); err != nil {
			continue
		}

		                
		if state.Status != "ACTIVE" {
			continue
		}
		                                                                        
		                                                                        
		                                 
		transitionData, transitionExists, transitionErr := p.stateReader.Get(keys.KeyFrostVaultTransition(chain, id, state.KeyEpoch+1))
		if transitionErr != nil {
			continue
		}
		if transitionExists && len(transitionData) > 0 {
			var transition pb.VaultTransitionState
			if proto.Unmarshal(transitionData, &transition) != nil || transition.Lifecycle == "DRAINING" {
				continue
			}
		}
		activeCount++

		                                              
		available, err := p.calculateVaultAvailableBalance(chain, asset, id)
		if err != nil {
			logs.Warn("[JobWindowPlanner] failed to calculate balance for vault %d: %v", id, err)
			continue
		}
		if available.Cmp(maxAvailable) > 0 || activeCount == 1 {
			maxAvailable = chainpkg.CloneAmount(available)
			maxAvailableVault = id
		}

		if available.Cmp(amount) >= 0 {
			                                 
			return id, state.KeyEpoch, nil
		}
		                                       
	}

	if activeCount == 0 {
		return 0, 1, fmt.Errorf("select vault failed: no ACTIVE vault for chain=%s (vault_count=%d)", chain, vaultCount)
	}
	return 0, 1, fmt.Errorf(
		"select vault failed: insufficient balance across ACTIVE vaults for %s/%s need=%s max_available=%s max_vault=%d active_count=%d",
		chain, asset, amount, maxAvailable, maxAvailableVault, activeCount,
	)
}

                                                              
func (p *JobWindowPlanner) calculateVaultAvailableBalance(chain, asset string, vaultID uint32) (*big.Int, error) {
	                                       
	if p.transientCache != nil {
		if cachedBal, ok := p.transientCache[vaultID]; ok {
			return chainpkg.CloneAmount(cachedBal), nil
		}
	}

	                                                                                                          
	balanceKey := keys.KeyFrostVaultAvailableBalance(chain, asset, vaultID)
	balanceData, exists, err := p.stateReader.Get(balanceKey)
	if err != nil {
		return nil, err
	}
	if !exists || len(balanceData) == 0 {
		if p.transientCache != nil {
			p.transientCache[vaultID] = big.NewInt(0)
		}
		return big.NewInt(0), nil
	}

	balance, err := parseNonNegativeAmount(string(balanceData))
	if err != nil {
		return nil, fmt.Errorf("invalid aggregate vault balance for %s/%s vault=%d: %w", chain, asset, vaultID, err)
	}
	if p.transientCache != nil {
		p.transientCache[vaultID] = chainpkg.CloneAmount(balance)
	}
	return chainpkg.CloneAmount(balance), nil
}

func (p *JobWindowPlanner) deductVaultBalance(vaultID uint32, amount *big.Int) {
	if amount == nil || amount.Sign() <= 0 {
		return
	}
	if p.transientCache != nil {
		if bal, ok := p.transientCache[vaultID]; ok {
			if bal.Cmp(amount) >= 0 {
				p.transientCache[vaultID] = new(big.Int).Sub(bal, amount)
			} else {
				p.transientCache[vaultID] = big.NewInt(0)                   
			}
		}
	}
}

                                                                                   
func (p *JobWindowPlanner) calculateAccountChainBalance(chain, asset string, vaultID uint32) (*big.Int, error) {
	                                        
	                                                 
	                                                   
	if total, found, err := p.calculateAccountChainBalanceFromLots(chain, asset, vaultID); err != nil {
		return nil, err
	} else if found {
		return total, nil
	}

	lotPrefix := keys.KeyFrostFundsLotPrefix(chain, asset, vaultID)
	total := big.NewInt(0)
	rechargeKeys := make([]string, 0)

	err := p.stateReader.Scan(lotPrefix, func(k string, v []byte) bool {
		                                                                        
		requestID := string(v)
		if requestID == "" {
			return true
		}
		rechargeKeys = append(rechargeKeys, keys.KeyRechargeRequest(requestID))
		return true
	})

	if err != nil {
		return nil, err
	}

	rechargeValues, err := getMany(p.stateReader, rechargeKeys)
	if err != nil {
		return nil, err
	}

	amountCache := make(map[string]*big.Int, len(rechargeValues))
	for _, rechargeKey := range rechargeKeys {
		rechargeData, exists := rechargeValues[rechargeKey]
		if !exists || len(rechargeData) == 0 {
			continue
		}
		amount, cached := amountCache[rechargeKey]
		if !cached {
			var recharge pb.RechargeRequest
			if err := proto.Unmarshal(rechargeData, &recharge); err != nil {
				continue
			}
			parsed, err := chainpkg.ParseWithdrawAmount(recharge.Amount)
			if err != nil {
				return nil, fmt.Errorf("invalid recharge amount for %s: %w", rechargeKey, err)
			}
			amount = parsed
			amountCache[rechargeKey] = amount
		}
		total.Add(total, amount)
		if total.BitLen() > 256 {
			return nil, fmt.Errorf("account vault balance overflows uint256")
		}
	}

	return total, nil
}

type accountFundsLotRef struct {
	height      uint64
	seq         uint64
	rechargeKey string
}

func parseFundsLotHeadBytes(data []byte) (uint64, uint64) {
	if len(data) == 0 {
		return 0, 0
	}
	parts := strings.SplitN(string(data), "|", 2)
	height, _ := strconv.ParseUint(parts[0], 10, 64)
	if len(parts) == 1 {
		return height, 0
	}
	seq, _ := strconv.ParseUint(parts[1], 10, 64)
	return height, seq
}

func parseFundsLotIndexSuffix(key, prefix string) (uint64, uint64, bool) {
	if !strings.HasPrefix(key, prefix) {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(key, prefix), "_")
	if len(parts) != 2 {
		return 0, 0, false
	}
	height, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	seq, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return height, seq, true
}

func fundsLotAtOrAfter(height, seq, headHeight, headSeq uint64) bool {
	if headHeight == 0 {
		return true
	}
	return height > headHeight || (height == headHeight && seq >= headSeq)
}

func (p *JobWindowPlanner) calculateAccountChainBalanceFromLots(chain, asset string, vaultID uint32) (*big.Int, bool, error) {
	lotPrefix := keys.KeyFrostFundsLotPrefix(chain, asset, vaultID)
	refs := make([]accountFundsLotRef, 0)
	headHeight, headSeq := uint64(0), uint64(0)
	if headData, exists, err := p.stateReader.Get(keys.KeyFrostFundsLotHead(chain, asset, vaultID)); err != nil {
		return nil, false, err
	} else if exists {
		headHeight, headSeq = parseFundsLotHeadBytes(headData)
	}

	err := p.stateReader.Scan(lotPrefix, func(k string, v []byte) bool {
		requestID := string(v)
		if requestID == "" {
			return true
		}
		height, seq, ok := parseFundsLotIndexSuffix(k, lotPrefix)
		if !ok || !fundsLotAtOrAfter(height, seq, headHeight, headSeq) {
			return true
		}
		refs = append(refs, accountFundsLotRef{height: height, seq: seq, rechargeKey: keys.KeyRechargeRequest(requestID)})
		return true
	})
	if err != nil {
		return nil, false, err
	}
	if len(refs) == 0 {
		return big.NewInt(0), false, nil
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].height != refs[j].height {
			return refs[i].height < refs[j].height
		}
		if refs[i].seq != refs[j].seq {
			return refs[i].seq < refs[j].seq
		}
		return refs[i].rechargeKey < refs[j].rechargeKey
	})

	rechargeKeys := make([]string, 0, len(refs))
	for _, ref := range refs {
		rechargeKeys = append(rechargeKeys, ref.rechargeKey)
	}
	rechargeValues, err := getMany(p.stateReader, rechargeKeys)
	if err != nil {
		return nil, true, err
	}

	total := big.NewInt(0)
	amountCache := make(map[string]*big.Int, len(rechargeValues))
	for _, ref := range refs {
		rechargeData, exists := rechargeValues[ref.rechargeKey]
		if !exists || len(rechargeData) == 0 {
			continue
		}
		amount, cached := amountCache[ref.rechargeKey]
		if !cached {
			var recharge pb.RechargeRequest
			if err := proto.Unmarshal(rechargeData, &recharge); err != nil {
				continue
			}
			parsed, err := chainpkg.ParseWithdrawAmount(recharge.Amount)
			if err != nil {
				return nil, true, fmt.Errorf("invalid recharge amount for %s: %w", ref.rechargeKey, err)
			}
			amount = parsed
			amountCache[ref.rechargeKey] = amount
		}
		total.Add(total, amount)
		if total.BitLen() > 256 {
			return nil, true, fmt.Errorf("account vault balance overflows uint256")
		}
	}
	return total, true, nil
}

func parseNonNegativeAmount(raw string) (*big.Int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed != "" && strings.Trim(trimmed, "0") == "" {
		return big.NewInt(0), nil
	}
	return chainpkg.ParseWithdrawAmount(trimmed)
}
