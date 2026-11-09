package main

import (
	"dex/config"
	"dex/db"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/utils"
	"dex/vm"
	"fmt"
	"math/big"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
)

                                                                            
                          
func registerPeersLocally(node *NodeInstance, peers []PeerInfo, genesisCfg *config.GenesisConfig, frostCfg config.FrostConfig) error {
	return seedTrustedGenesisState(node, peers, genesisCfg, frostCfg, true)
}

                                                                            
                                                                             
                                                                                
                                                                             
                                                                              
                                                                            
func seedTrustedGenesisState(node *NodeInstance, peers []PeerInfo, genesisCfg *config.GenesisConfig, frostCfg config.FrostConfig, requireLocalValidator bool) error {
	if node == nil || node.DBManager == nil {
		return fmt.Errorf("node or DBManager is nil")
	}
	if err := config.ValidateDeposits(frostCfg, node.NetworkChainID); err != nil {
		return fmt.Errorf("validate unified Vault deposits before state seed: %w", err)
	}
	normalizedPeers := make([]PeerInfo, len(peers))
	for i, p := range peers {
		normalizedPeer, err := normalizePeerIdentity(p)
		if err != nil {
			return err
		}
		if len(normalizedPeer.SecpPublicKey) == 0 {
			return fmt.Errorf("peer %d requires secp_public_key for genesis registration", p.Index)
		}
		if !normalizedPeer.IsMiner {
			return fmt.Errorf("peer %d is not a genesis validator", p.Index)
		}
		if len(normalizedPeer.BLSPublicKey) == 0 {
			return fmt.Errorf("peer %d miner requires bls_public_key for genesis registration", p.Index)
		}
		endpoint, err := trustedGenesisEndpoint(normalizedPeer)
		if err != nil {
			return err
		}
		normalizedPeer.AdvertiseAddr = endpoint
		normalizedPeers[i] = normalizedPeer
	}
	manifest := &config.PeerManifest{ChainID: node.NetworkChainID}
	for _, peer := range normalizedPeers {
		manifest.GenesisValidators = append(manifest.GenesisValidators, config.GenesisValidatorConfig{Address: peer.Address, IsMiner: peer.IsMiner})
	}
	if err := config.ValidateFreshFrostGenesisStakePolicy(frostCfg, manifest, genesisCfg); err != nil {
		return fmt.Errorf("validate fresh FROST genesis ranking before state seed: %w", err)
	}
	totalStake := new(big.Int)
	for _, peer := range normalizedPeers {
		stake, err := genesisMinerStake(genesisCfg, peer.Address)
		if err != nil {
			return fmt.Errorf("genesis validator %s: %w", peer.Address, err)
		}
		totalStake.Add(totalStake, stake)
	}
	peers = normalizedPeers
	if frostCfg.Enabled {
		if _, err := validateFrostGenesisDeploymentProof(peers, frostCfg, node.NetworkChainID, node.FrostPeerManifestDigest); err != nil {
			return fmt.Errorf("validate signed FROST genesis deployments before state seed: %w", err)
		}
	}
	_, localIsValidator := findPeerByAddress(peers, node.Address)
	if requireLocalValidator && !localIsValidator {
		return fmt.Errorf("local address %s missing from genesis peer set", node.Address)
	}
	ordinaryAccounts, err := validateOrdinaryGenesisAccounts(genesisCfg, peers)
	if err != nil {
		return fmt.Errorf("seed ordinary genesis accounts: %w", err)
	}
	if err := validateGenesisAllocationSupply(genesisCfg, peers, ordinaryAccounts); err != nil {
		return fmt.Errorf("validate genesis allocation supply: %w", err)
	}

	                        
	logs.RegisterNodeMapping(strconv.Itoa(node.ID), node.Address)
	logs.RegisterNodeMapping(node.Port, node.Address)
	logs.RegisterNodeMapping(nodeAdvertiseAddr(node), node.Address)
	logs.RegisterNodeMapping(node.Address, node.Address)

	                                        
	blsPubMap := make(map[int][]byte, len(peers))
	secpPubMap := make(map[int][]byte, len(peers))
	blsOwners := make(map[string]string, len(peers))
	for _, p := range peers {
		secpPubMap[p.Index] = append([]byte(nil), p.SecpPublicKey...)
		if len(p.BLSPublicKey) > 0 {
			blsPubMap[p.Index] = append([]byte(nil), p.BLSPublicKey...)
			if p.IsMiner {
				bindingKey := keys.KeyMinerBLSPublicKeyOwner(p.BLSPublicKey)
				if owner, exists := blsOwners[bindingKey]; exists && owner != p.Address {
					return fmt.Errorf("genesis BLS public key is shared by %s and %s", owner, p.Address)
				}
				blsOwners[bindingKey] = p.Address
			}
		}
	}
	                                                                    
	                                                                         
	                                             
	bindingKeys := make([]string, 0, len(blsOwners))
	for bindingKey := range blsOwners {
		bindingKeys = append(bindingKeys, bindingKey)
	}
	sort.Strings(bindingKeys)
	for _, bindingKey := range bindingKeys {
		node.DBManager.EnqueueSet(bindingKey, blsOwners[bindingKey])
	}

	                                                                            
	                                                                        
	if frostCfg.Enabled {
		minerPeers, err := selectFrostCandidatePeers(peers, frostCfg)
		if err != nil {
			return err
		}
		topK, err := buildFrostActiveMinersFromPeers(minerPeers)
		if err != nil {
			return err
		}
		vaultConfigs := buildVaultConfigs(frostCfg, len(topK.Indices))
		if err := applyFrostBootstrap(node.DBManager, frostCfg, topK, vaultConfigs); err != nil {
			return fmt.Errorf("apply deterministic genesis FROST state: %w", err)
		}
	}

	                                                                         
	                                                                          
	                                                                             
	                                                                          
	                                                              
	for _, p := range peers {
		pubKeys := map[int32][]byte{
			int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): secpPubMap[p.Index],
		}
		if blsBytes, ok := blsPubMap[p.Index]; ok {
			pubKeys[int32(pb.SignAlgo_SIGN_ALGO_BLS_BN256_G2)] = blsBytes
		}
		account := &pb.Account{
			Address:       p.Address,
			RewardAddress: p.RewardAddress,
			PublicKeys:    &pb.PublicKeys{Keys: pubKeys},
			Ip:            p.AdvertiseAddr,
			Index:         uint64(p.Index),
			IsMiner:       true,
		}

		applyGenesisBalancesWithConfig(account.Address, node.DBManager, genesisCfg)
		if err := node.DBManager.SaveAccount(account); err != nil {
			return fmt.Errorf("save genesis validator %s: %w", p.Address, err)
		}

		nodeInfo := &pb.NodeInfo{
			PublicKeys: &pb.PublicKeys{Keys: clonePublicKeyMap(pubKeys)},
			Ip:         p.AdvertiseAddr,
			IsOnline:   true,
		}
		if err := node.DBManager.SaveNodeInfo(nodeInfo); err != nil {
			return fmt.Errorf("save genesis node info %s: %w", p.Address, err)
		}

		indexKey := keys.KeyIndexToAccount(uint64(p.Index))
		accountKey := keys.KeyAccount(p.Address)
		node.DBManager.EnqueueSet(indexKey, accountKey)
	}
	if err := seedOrdinaryGenesisAccounts(node.DBManager, ordinaryAccounts); err != nil {
		return fmt.Errorf("seed ordinary genesis accounts: %w", err)
	}

	                            
	if err := initGenesisTokensWithConfig(node.DBManager, genesisCfg); err != nil {
		return fmt.Errorf("initialize genesis token registry: %w", err)
	}
	if totalStake.Sign() > 0 {
		node.DBManager.EnqueueSet(keys.KeyTotalMinerStake(), totalStake.String())
	}

	                            
	if err := node.DBManager.ForceFlush(); err != nil {
		return fmt.Errorf("flush deterministic genesis state: %w", err)
	}
	time.Sleep(100 * time.Millisecond)

	if err := node.DBManager.IndexMgr.RebuildBitmapFromDB(); err != nil {
		return fmt.Errorf("rebuild genesis miner bitmap: %w", err)
	}
	if err := node.DBManager.RefreshMinerParticipantsForEpoch(0); err != nil {
		return fmt.Errorf("initialize genesis miner cache: %w", err)
	}

	return nil
}

type ordinaryGenesisBalanceWrite struct {
	key   string
	value string
}

type ordinaryGenesisAccountPlan struct {
	address       string
	balanceWrites []ordinaryGenesisBalanceWrite
}

                                                                              
                                                                              
                                                                              
                                                                    
func validateOrdinaryGenesisAccounts(genesisCfg *config.GenesisConfig, peers []PeerInfo) ([]ordinaryGenesisAccountPlan, error) {
	if genesisCfg == nil || len(genesisCfg.Alloc) == 0 {
		return nil, nil
	}
	validators := make(map[string]struct{}, len(peers))
	for _, peer := range peers {
		validators[peer.Address] = struct{}{}
	}
	registeredTokens := make(map[string]struct{}, len(genesisCfg.Tokens))
	for _, token := range genesisCfg.Tokens {
		registeredTokens[token.Address] = struct{}{}
	}
	addresses := make([]string, 0, len(genesisCfg.Alloc))
	for address := range genesisCfg.Alloc {
		if address == "default" {
			continue
		}
		if _, validator := validators[address]; validator {
			continue
		}
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	plans := make([]ordinaryGenesisAccountPlan, 0, len(addresses))
	for _, address := range addresses {
		if !utils.IsDexAddress(address) {
			return nil, fmt.Errorf("ordinary allocation address %q is not a canonical fbc account", address)
		}
		allocation := genesisCfg.Alloc[address]
		if len(allocation.Balances) == 0 {
			return nil, fmt.Errorf("ordinary allocation for %s has no balances", address)
		}
		tokens := make([]string, 0, len(allocation.Balances))
		for token := range allocation.Balances {
			tokens = append(tokens, token)
		}
		sort.Strings(tokens)
		plan := ordinaryGenesisAccountPlan{
			address:       address,
			balanceWrites: make([]ordinaryGenesisBalanceWrite, 0, len(tokens)),
		}
		for _, token := range tokens {
			balance := allocation.Balances[token]
			if _, registered := registeredTokens[token]; !registered {
				return nil, fmt.Errorf("ordinary allocation for %s references unregistered token %q", address, token)
			}
			if strings.TrimSpace(balance.MinerLockedBalance) != "" {
				return nil, fmt.Errorf("ordinary allocation for %s token %s must not contain miner_locked_balance", address, token)
			}
			rawAmount := balance.Balance
			amount, ok := new(big.Int).SetString(rawAmount, 10)
			if !ok || amount.Sign() <= 0 || strings.TrimSpace(rawAmount) != rawAmount || (len(rawAmount) > 1 && rawAmount[0] == '0') {
				return nil, fmt.Errorf("ordinary allocation for %s token %s has an invalid balance", address, token)
			}
			record := &pb.TokenBalanceRecord{
				Address: address,
				Token:   token,
				Balance: &pb.TokenBalance{Balance: rawAmount},
			}
			data, err := proto.Marshal(record)
			if err != nil {
				return nil, fmt.Errorf("marshal ordinary allocation for %s token %s: %w", address, token, err)
			}
			plan.balanceWrites = append(plan.balanceWrites, ordinaryGenesisBalanceWrite{
				key:   keys.KeyBalance(address, token),
				value: string(data),
			})
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

type genesisTokenSupply struct {
	total     *big.Int
	allocated *big.Int
}

                                                                              
                                                                             
                                                                             
                                                                            
func validateGenesisAllocationSupply(genesisCfg *config.GenesisConfig, peers []PeerInfo, ordinaryPlans []ordinaryGenesisAccountPlan) error {
	if genesisCfg == nil {
		return fmt.Errorf("genesis config is missing")
	}
	supplies := make(map[string]*genesisTokenSupply, len(genesisCfg.Tokens))
	tokenAddresses := make([]string, 0, len(genesisCfg.Tokens))
	for _, token := range genesisCfg.Tokens {
		if _, duplicate := supplies[token.Address]; duplicate {
			return fmt.Errorf("duplicate genesis token %q", token.Address)
		}
		total, err := parseCanonicalGenesisAmount(token.TotalSupply, false)
		if err != nil {
			return fmt.Errorf("token %s has invalid total_supply: %w", token.Address, err)
		}
		supplies[token.Address] = &genesisTokenSupply{total: total, allocated: new(big.Int)}
		tokenAddresses = append(tokenAddresses, token.Address)
	}
	sort.Strings(tokenAddresses)

	addAllocation := func(owner string, allocation config.GenesisAlloc) error {
		tokens := make([]string, 0, len(allocation.Balances))
		for token := range allocation.Balances {
			tokens = append(tokens, token)
		}
		sort.Strings(tokens)
		for _, token := range tokens {
			supply, registered := supplies[token]
			if !registered {
				return fmt.Errorf("allocation for %s references unregistered token %q", owner, token)
			}
			balance := allocation.Balances[token]
			available, err := parseCanonicalGenesisAmount(balance.Balance, true)
			if err != nil {
				return fmt.Errorf("allocation for %s token %s has invalid available balance: %w", owner, token, err)
			}
			locked, err := parseCanonicalGenesisAmount(balance.MinerLockedBalance, true)
			if err != nil {
				return fmt.Errorf("allocation for %s token %s has invalid miner_locked_balance: %w", owner, token, err)
			}
			supply.allocated.Add(supply.allocated, available)
			supply.allocated.Add(supply.allocated, locked)
		}
		return nil
	}

	validatorSet := make(map[string]struct{}, len(peers))
	validatorAddresses := make([]string, 0, len(peers))
	for _, peer := range peers {
		if _, exists := validatorSet[peer.Address]; exists {
			continue
		}
		validatorSet[peer.Address] = struct{}{}
		validatorAddresses = append(validatorAddresses, peer.Address)
	}
	sort.Strings(validatorAddresses)
	for _, address := range validatorAddresses {
		allocation, exists := genesisCfg.Alloc[address]
		if !exists {
			allocation, exists = genesisCfg.Alloc["default"]
		}
		if !exists {
			return fmt.Errorf("genesis validator %s has no allocation", address)
		}
		if err := addAllocation("genesis validator "+address, allocation); err != nil {
			return err
		}
	}
	for _, plan := range ordinaryPlans {
		if err := addAllocation("ordinary account "+plan.address, genesisCfg.Alloc[plan.address]); err != nil {
			return err
		}
	}
	for _, token := range tokenAddresses {
		supply := supplies[token]
		if supply.allocated.Cmp(supply.total) > 0 {
			return fmt.Errorf("token %s allocations total %s exceeds total_supply %s", token, supply.allocated, supply.total)
		}
	}
	return nil
}

func parseCanonicalGenesisAmount(raw string, emptyIsZero bool) (*big.Int, error) {
	if raw == "" && emptyIsZero {
		return new(big.Int), nil
	}
	if raw == "" || strings.TrimSpace(raw) != raw || (len(raw) > 1 && raw[0] == '0') {
		return nil, fmt.Errorf("must be a canonical non-negative base-10 integer")
	}
	amount, ok := new(big.Int).SetString(raw, 10)
	if !ok || amount.Sign() < 0 {
		return nil, fmt.Errorf("must be a canonical non-negative base-10 integer")
	}
	return amount, nil
}

                                                                             
                                                                          
                                                                         
func seedOrdinaryGenesisAccounts(dbManager *db.Manager, plans []ordinaryGenesisAccountPlan) error {
	if dbManager == nil || len(plans) == 0 {
		return nil
	}
	for _, plan := range plans {
		for _, write := range plan.balanceWrites {
			dbManager.EnqueueSet(write.key, write.value)
		}
		if err := dbManager.SaveAccount(&pb.Account{Address: plan.address, IsMiner: false}); err != nil {
			return fmt.Errorf("save ordinary genesis account %s: %w", plan.address, err)
		}
	}
	return nil
}

                                                                        
                                                                               
                                                                           
                                                         
func trustedGenesisEndpoint(peer PeerInfo) (string, error) {
	endpoint := strings.TrimSpace(peer.AdvertiseAddr)
	if endpoint == "" {
		return "", fmt.Errorf("peer %d requires advertise_addr for genesis registration", peer.Index)
	}
	host, rawPort, err := net.SplitHostPort(endpoint)
	if err != nil || strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("peer %d advertise_addr %q must be host:port", peer.Index, endpoint)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("peer %d advertise_addr %q port must be in 1..65535", peer.Index, endpoint)
	}
	return endpoint, nil
}

func genesisMinerStake(genesisCfg *config.GenesisConfig, address string) (*big.Int, error) {
	if genesisCfg == nil {
		return nil, fmt.Errorf("genesis config is missing")
	}
	alloc, exists := genesisCfg.Alloc[address]
	if !exists {
		alloc, exists = genesisCfg.Alloc["default"]
	}
	if !exists {
		return nil, fmt.Errorf("FB allocation is missing")
	}
	balance, exists := alloc.Balances["FB"]
	if !exists || balance.MinerLockedBalance == "" {
		return nil, fmt.Errorf("FB miner_locked_balance is missing")
	}
	stake, ok := new(big.Int).SetString(balance.MinerLockedBalance, 10)
	if !ok || stake.Sign() < 0 {
		return nil, fmt.Errorf("FB miner_locked_balance is invalid")
	}
	if stake.Cmp(vm.MinMinerStake) < 0 {
		return nil, fmt.Errorf("FB miner_locked_balance %s is below minimum %s", stake, vm.MinMinerStake)
	}
	if stake.BitLen() > 64 {
		return nil, fmt.Errorf("FB miner_locked_balance exceeds the uint64 consensus-weight limit")
	}
	return stake, nil
}

func clonePublicKeyMap(keysByAlgo map[int32][]byte) map[int32][]byte {
	cloned := make(map[int32][]byte, len(keysByAlgo))
	for algo, publicKey := range keysByAlgo {
		cloned[algo] = append([]byte(nil), publicKey...)
	}
	return cloned
}

                                                                                    
func applyGenesisBalancesWithConfig(address string, dbManager *db.Manager, genesisCfg *config.GenesisConfig) {
	if genesisCfg == nil || dbManager == nil {
		return
	}

	alloc, exists := genesisCfg.Alloc[address]
	if !exists {
		alloc, exists = genesisCfg.Alloc["default"]
		if !exists {
			return
		}
	}

	for tokenAddr, gb := range alloc.Balances {
		bal := &pb.TokenBalanceRecord{
			Address: address,
			Token:   tokenAddr,
			Balance: &pb.TokenBalance{
				Balance:            gb.Balance,
				MinerLockedBalance: gb.MinerLockedBalance,
			},
		}
		balData, _ := proto.Marshal(bal)
		balKey := keys.KeyBalance(address, tokenAddr)
		dbManager.EnqueueSet(balKey, string(balData))
	}
}

                                                                                   
func initGenesisTokensWithConfig(dbManager *db.Manager, genesisCfg *config.GenesisConfig) error {
	if genesisCfg == nil {
		return nil
	}
	if err := config.ValidateGenesisConfig(genesisCfg); err != nil {
		return err
	}

	registry := &pb.TokenRegistry{}

	for _, t := range genesisCfg.Tokens {
		enqueueGenesisToken(dbManager, t)
	}

	registryData, _ := proto.Marshal(registry)
	dbManager.EnqueueSet(keys.KeyTokenRegistry(), string(registryData))

	return nil
}
