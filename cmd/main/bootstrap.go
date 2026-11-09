package main

import (
	"bytes"
	"dex/config"
	"dex/db"
	"dex/frost/ceremony"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/vm"
	"errors"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"
)

func buildVaultConfigs(frostCfg config.FrostConfig, minerCount int) map[string]*pb.FrostVaultConfig {
	configs := make(map[string]*pb.FrostVaultConfig)

	for name, chainCfg := range frostCfg.Chains {
		vaultRefs := chainCfg.VaultRefs
		if len(vaultRefs) == 0 && len(chainCfg.InitialGroupPubkeys) == 0 {
			vaultRefs = chainCfg.OnlineDKGVaultRefs
		}
		vc := &pb.FrostVaultConfig{
			Chain:                         name,
			SignAlgo:                      parseSignAlgo(chainCfg.SignAlgo),
			VaultCount:                    uint32(chainCfg.VaultsPerChain),
			ThresholdRatio:                float32(frostCfg.Vault.ThresholdRatio),
			CommitteeSize:                 uint32(frostCfg.Vault.DefaultK),
			VaultRefs:                     append([]string(nil), vaultRefs...),
			TransitionTriggerRatio:        float32(frostCfg.Transition.TriggerChangeRatio),
			DkgCommitWindowBlocks:         uint64(frostCfg.Transition.DkgCommitWindowBlocks),
			DkgSharingWindowBlocks:        uint64(frostCfg.Transition.DkgSharingWindowBlocks),
			DkgDisputeWindowBlocks:        uint64(frostCfg.Transition.DkgDisputeWindowBlocks),
			BtcNetwork:                    chainCfg.BTCNetwork,
			BtcFeeSatsPerVbyte:            chainCfg.FeeSatsPerVByte,
			EvmChainId:                    chainCfg.ChainID,
			DepositDexChainId:             chainCfg.DepositDexChainID,
			DepositProtocol:               chainCfg.DepositProtocol,
			AuthorityProtocol:             chainCfg.AuthorityProtocol,
			VaultMaturityBlocks:           frostCfg.Vault.MaturityBlocks,
			CommitteeScaleMinerMultiplier: frostCfg.Vault.ScaleMinerMultiplier,
			TransitionConfirmBlocks:       frostCfg.Transition.ConfirmBlocks,
			CommitteeMaxSize:              uint32(frostCfg.Vault.MaxK),
			CommitteeTermBlocks:           protocol.VaultCommitteeTermBlocks,
		}
		                                                                       
		                                                                     
		vc.GuardianPubkey, _ = frostCfg.GuardianPublicKey(name)
		configs[name] = vc
	}
	return configs
}

func parseSignAlgo(raw string) pb.SignAlgo {
	switch raw {
	case "ECDSA_P256":
		return pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX
	case "BLS_BN256_G2":
		return pb.SignAlgo_SIGN_ALGO_BLS_BN256_G2
	case "SCHNORR_SECP256K1_BIP340":
		return pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340
	case "SCHNORR_ALT_BN128":
		return pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
	case "ECDSA_SECP256K1":
		return pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1
	default:
		return pb.SignAlgo_SIGN_ALGO_UNSPECIFIED
	}
}

func validateFrostStartupMinerSet(peers []PeerInfo, frostCfg config.FrostConfig) error {
	if !frostCfg.Enabled {
		return nil
	}
	if err := config.ValidateFrostGuardianConfig(frostCfg); err != nil {
		return fmt.Errorf("FROST fixed guardian configuration: %w", err)
	}
	if err := config.ValidateOnlineDKGVaultRefs(frostCfg); err != nil {
		return fmt.Errorf("validate FROST online-DKG vault references: %w", err)
	}
	chainNames := make([]string, 0, len(frostCfg.Chains))
	for name := range frostCfg.Chains {
		chainNames = append(chainNames, name)
	}
	sort.Strings(chainNames)
	configuredChains := 0
	missingResults := make([]string, 0, len(chainNames))
	for _, name := range chainNames {
		groupPubkeys, err := config.DecodeInitialFrostGroupPubkeys(name, frostCfg.Chains[name])
		if err != nil {
			return fmt.Errorf("validate FROST configured genesis: %w", err)
		}
		if len(groupPubkeys) == 0 {
			missingResults = append(missingResults, name)
			continue
		}
		configuredChains++
	}
	if configuredChains > 0 && configuredChains != len(chainNames) {
		return fmt.Errorf("validate FROST configured genesis: ceremony results must be all-or-none across configured chains; missing %s", strings.Join(missingResults, ","))
	}
	_, err := selectFrostCandidatePeers(peers, frostCfg)
	return err
}

func selectFrostCandidatePeers(peers []PeerInfo, frostCfg config.FrostConfig) ([]PeerInfo, error) {
	minerPeers := make([]PeerInfo, 0, len(peers))
	for _, p := range peers {
		if p.IsMiner {
			minerPeers = append(minerPeers, p)
		}
	}
	                                                                         
	                                                                             
	                                                                          
	sort.Slice(minerPeers, func(i, j int) bool { return minerPeers[i].Address < minerPeers[j].Address })
	for i := range minerPeers {
		minerPeers[i].Index = i
	}

	if topN := frostCfg.Committee.TopN; topN > 0 && len(minerPeers) > topN {
		minerPeers = minerPeers[:topN]
	}
	if required := frostCfg.Vault.DefaultK; required > 0 && len(minerPeers) < required {
		return nil, fmt.Errorf("insufficient frost miners: have %d candidates, need %d (committee.topN=%d, vault.defaultK=%d)", len(minerPeers), required, frostCfg.Committee.TopN, frostCfg.Vault.DefaultK)
	}
	return minerPeers, nil
}

func buildFrostActiveMinersFromPeers(minerPeers []PeerInfo) (*pb.FrostActiveMiners, error) {
	topK := &pb.FrostActiveMiners{
		Height:        0,
		Indices:       make([]uint64, len(minerPeers)),
		Addresses:     make([]string, len(minerPeers)),
		PublicKeys:    make([][]byte, len(minerPeers)),
		EligibleCount: uint32(len(minerPeers)),
	}
	for i, mp := range minerPeers {
		normalizedPeer, err := normalizePeerIdentity(mp)
		if err != nil {
			return nil, err
		}
		if len(normalizedPeer.SecpPublicKey) == 0 {
			return nil, fmt.Errorf("peer %d requires secp_public_key for FROST active miner registration", mp.Index)
		}
		topK.Indices[i] = uint64(mp.Index)
		topK.Addresses[i] = normalizedPeer.Address
		topK.PublicKeys[i] = append([]byte(nil), normalizedPeer.SecpPublicKey...)
	}
	if err := config.ValidateVaultCandidateSnapshot(topK); err != nil {
		return nil, fmt.Errorf("invalid genesis FROST roster: %w", err)
	}
	return topK, nil
}

const initialDkgBootstrapGraceBlocks uint64 = 20

func initialDkgTriggerHeight(commitWindow uint64) uint64 {
	if commitWindow > initialDkgBootstrapGraceBlocks {
		return commitWindow
	}
	return initialDkgBootstrapGraceBlocks
}

func configuredFrostGenesisMode(frostCfg config.FrostConfig) (bool, error) {
	chainNames := make([]string, 0, len(frostCfg.Chains))
	for name := range frostCfg.Chains {
		chainNames = append(chainNames, name)
	}
	sort.Strings(chainNames)
	configuredChains := 0
	missingResults := make([]string, 0, len(chainNames))
	for _, name := range chainNames {
		groupPubkeys, err := config.DecodeInitialFrostGroupPubkeys(name, frostCfg.Chains[name])
		if err != nil {
			return false, err
		}
		if len(groupPubkeys) == 0 {
			missingResults = append(missingResults, name)
			continue
		}
		configuredChains++
	}
	if configuredChains > 0 && configuredChains != len(chainNames) {
		return false, fmt.Errorf("ceremony results must be all-or-none across configured chains; missing %s", strings.Join(missingResults, ","))
	}
	return configuredChains > 0, nil
}

func applyFrostBootstrap(dbManager *db.Manager, frostCfg config.FrostConfig, topK *pb.FrostActiveMiners, vaultConfigs map[string]*pb.FrostVaultConfig) error {
	if dbManager == nil || topK == nil || len(vaultConfigs) == 0 {
		return nil
	}

	readFn := func(key string) ([]byte, error) {
		val, err := dbManager.Get(key)
		if err != nil {
			if isNotFoundError(err) {
				return nil, nil
			}
			return nil, err
		}
		return val, nil
	}
	scanFn := func(prefix string) (map[string][]byte, error) {
		return dbManager.Scan(prefix)
	}
	sv := vm.NewStateView(readFn, scanFn)
	configuredGenesis, err := configuredFrostGenesisMode(frostCfg)
	if err != nil {
		return fmt.Errorf("validate FROST configured genesis: %w", err)
	}
	expectedMarker := ""
	if configuredGenesis {
		expectedMarker, err = configuredFrostGenesisMarkerValue(frostCfg)
		if err != nil {
			return fmt.Errorf("validate signed FROST configured-genesis marker: %w", err)
		}
	}
	existingMarker, markerExists, err := sv.Get(keys.KeyFrostConfiguredGenesisMarker())
	if err != nil {
		return fmt.Errorf("load FROST configured-genesis state marker: %w", err)
	}
	if markerExists && string(existingMarker) != expectedMarker {
		return errors.New("invalid or conflicting FROST configured-genesis state marker")
	}
	if markerExists && !configuredGenesis {
		return errors.New("FROST configured-genesis state marker and public configuration disagree")
	}
	_, activeMinersExist, err := sv.Get(keys.KeyFrostActiveMiners())
	if err != nil {
		return fmt.Errorf("load FROST active-miner genesis state: %w", err)
	}
	if configuredGenesis && !markerExists && activeMinersExist {
		return errors.New("configured FROST genesis state is missing its standalone-DKG marker")
	}

	if existing, exists, err := sv.Get(keys.KeyFrostActiveMiners()); err != nil {
		return err
	} else if !exists || len(existing) == 0 {
		data, err := proto.Marshal(topK)
		if err != nil {
			return err
		}
		sv.Set(keys.KeyFrostActiveMiners(), data)
	}

	if frostCfg.Transition.DkgCommitWindowBlocks <= 0 ||
		frostCfg.Transition.DkgSharingWindowBlocks <= 0 ||
		frostCfg.Transition.DkgDisputeWindowBlocks <= 0 {
		return fmt.Errorf("FROST DKG transition windows must all be positive")
	}

	epochID := uint64(1)
	commitWindow := uint64(frostCfg.Transition.DkgCommitWindowBlocks)
	sharingWindow := uint64(frostCfg.Transition.DkgSharingWindowBlocks)
	disputeWindow := uint64(frostCfg.Transition.DkgDisputeWindowBlocks)
	                                                                         
	                                                                           
	                                                                   
	triggerHeight := initialDkgTriggerHeight(commitWindow)
	var candidateAddresses map[uint64]string
	var initialAssignments map[string][][]uint64
	if configuredGenesis {
		if err := config.ValidateVaultCandidateSnapshot(topK); err != nil {
			return fmt.Errorf("invalid configured-genesis FROST roster: %w", err)
		}
		candidateAddresses = make(map[uint64]string, len(topK.Indices))
		for i, index := range topK.Indices {
			candidateAddresses[index] = topK.Addresses[i]
		}
		initialAssignments, err = ceremony.InitialCommitteeAssignments(frostCfg, topK.Indices, topK.Addresses)
		if err != nil {
			return fmt.Errorf("assign configured-genesis committees: %w", err)
		}
	}

	chainNames := make([]string, 0, len(vaultConfigs))
	for chainName := range vaultConfigs {
		chainNames = append(chainNames, chainName)
	}
	sort.Strings(chainNames)
	for _, chainName := range chainNames {
		cfg := vaultConfigs[chainName]
		if cfg == nil {
			return fmt.Errorf("missing vault config for chain %s during bootstrap", chainName)
		}
		chainCfg, ok := frostCfg.Chains[chainName]
		if !ok {
			return fmt.Errorf("missing chain config for chain %s during bootstrap", chainName)
		}
		guardian, err := frostCfg.GuardianPublicKey(chainName)
		if err != nil || !bytes.Equal(cfg.GuardianPubkey, guardian) {
			return fmt.Errorf("fixed guardian config mismatch for chain %s", chainName)
		}
		var configuredCommittees [][]string
		if configuredGenesis {
			configuredCommittees = make([][]string, cfg.VaultCount)
			assignments := initialAssignments[chainName]
			if len(assignments) != len(configuredCommittees) {
				return errors.New("configured-genesis assignment count differs from vault configuration")
			}
			for vaultID := range configuredCommittees {
				for _, index := range assignments[vaultID] {
					if candidateAddresses[index] == "" {
						return errors.New("configured-genesis committee member missing from roster")
					}
					configuredCommittees[vaultID] = append(configuredCommittees[vaultID], candidateAddresses[index])
				}
			}
		}
		if err := applyChainFrostBootstrap(sv, chainName, chainCfg, cfg, epochID, triggerHeight, commitWindow, sharingWindow, disputeWindow, configuredCommittees); err != nil {
			return fmt.Errorf("applyChainFrostBootstrap failed for chain %s: %w", chainName, err)
		}
	}
	if configuredGenesis && !markerExists {
		sv.Set(keys.KeyFrostConfiguredGenesisMarker(), []byte(expectedMarker))
	}

	for _, op := range sv.Diff() {
		if op.Del {
			dbManager.EnqueueDel(op.Key)
			continue
		}
		dbManager.EnqueueSet(op.Key, string(op.Value))
	}

	return dbManager.ForceFlush()
}

func applyChainFrostBootstrap(
	sv vm.StateView,
	chainName string,
	chainCfg config.ChainFrostConfig,
	cfg *pb.FrostVaultConfig,
	epochID uint64,
	triggerHeight uint64,
	commitWindow uint64,
	sharingWindow uint64,
	disputeWindow uint64,
	verifiedCommittees ...[][]string,
) error {
	if cfg == nil || cfg.VaultCount == 0 || int(cfg.VaultCount) != chainCfg.VaultsPerChain || cfg.SignAlgo != parseSignAlgo(chainCfg.SignAlgo) {
		return fmt.Errorf("chain %s bootstrap config disagrees with public chain configuration", chainName)
	}
	groupPubkeys, err := config.DecodeInitialFrostGroupPubkeys(chainName, chainCfg)
	if err != nil {
		return err
	}
	expectedVaultRefs := chainCfg.VaultRefs
	if len(groupPubkeys) == 0 {
		expectedVaultRefs = chainCfg.OnlineDKGVaultRefs
	}
	if len(cfg.VaultRefs) != len(expectedVaultRefs) {
		return fmt.Errorf("chain %s bootstrap vaultRefs disagree with public chain configuration", chainName)
	}
	if cfg.DepositDexChainId != chainCfg.DepositDexChainID || cfg.DepositProtocol != chainCfg.DepositProtocol || cfg.AuthorityProtocol != chainCfg.AuthorityProtocol {
		return fmt.Errorf("chain %s deposit configuration disagrees with public chain configuration", chainName)
	}
	if strings.EqualFold(chainName, "btc") && cfg.BtcFeeSatsPerVbyte != chainCfg.FeeSatsPerVByte {
		return fmt.Errorf("chain %s BTC fee configuration disagrees with public chain configuration", chainName)
	}
	for i, ref := range cfg.VaultRefs {
		if ref != expectedVaultRefs[i] {
			return fmt.Errorf("chain %s bootstrap vaultRefs[%d] disagrees with public chain configuration", chainName, i)
		}
	}
	if err := vm.InitVaultConfig(sv, chainName, cfg); err != nil {
		return fmt.Errorf("InitVaultConfig: %w", err)
	}

	if len(groupPubkeys) > 0 {
		if err := vm.InitConfiguredActiveVaultStates(sv, chainName, epochID, 1, groupPubkeys, verifiedCommittees...); err != nil {
			return fmt.Errorf("InitConfiguredActiveVaultStates: %w", err)
		}
		return nil
	}

	if err := vm.InitVaultStates(sv, chainName, epochID); err != nil {
		return fmt.Errorf("InitVaultStates: %w", err)
	}
	if err := vm.InitVaultTransitions(sv, chainName, epochID, triggerHeight, commitWindow, sharingWindow, disputeWindow); err != nil {
		return fmt.Errorf("InitVaultTransitions: %w", err)
	}
	return nil
}

                                                   
func initGenesisTokens(dbManager *db.Manager, genesisCfg *config.GenesisConfig) error {
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

func enqueueGenesisToken(dbManager *db.Manager, t config.GenesisToken) {
	token := buildGenesisToken(t)
	tokenData, _ := proto.Marshal(token)
	dbManager.EnqueueSet(keys.KeyToken(t.Address), string(tokenData))
	if token.Type == pb.TokenType_TOKEN_BRIDGED {
		dbManager.EnqueueSet(keys.KeyNativeTokenIndex(token.NativeChain, token.NativeContract), token.Address)
	}
}

                                                                                    
func applyGenesisBalances(address string, dbManager *db.Manager, genesisCfg *config.GenesisConfig) {
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

                                                                          
                                                                                  
func buildGenesisToken(t config.GenesisToken) *pb.Token {
	var typ pb.TokenType
	switch strings.ToUpper(strings.TrimSpace(t.Type)) {
	case "BRIDGED":
		typ = pb.TokenType_TOKEN_BRIDGED
	case "SYSTEM":
		typ = pb.TokenType_TOKEN_SYSTEM
	default:
		typ = pb.TokenType_TOKEN_NATIVE
	}
	owner := t.Owner
	if owner == "" {
		owner = "0x0"
	}
	return &pb.Token{
		Address:        t.Address,
		Owner:          owner,
		TotalSupply:    t.TotalSupply,
		CanMint:        t.CanMint,
		Type:           typ,
		NativeChain:    t.NativeChain,
		NativeContract: t.NativeContract,
	}
}
