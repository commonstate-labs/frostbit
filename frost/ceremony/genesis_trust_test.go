package ceremony

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"dex/config"
	"dex/frost/assignment"
	"dex/utils"

	"github.com/btcsuite/btcd/btcec/v2"
)

func TestExpectedGenesisDeploymentTrustUsesChainHashCommittees(t *testing.T) {
	validators := make([]GenesisTrustValidator, 0, 5)
	for _, scalar := range []byte{29, 7, 23, 11, 17} {
		raw := make([]byte, 32)
		raw[31] = scalar
		privateKey, _ := btcec.PrivKeyFromBytes(raw)
		publicKey := privateKey.PubKey().SerializeCompressed()
		address, err := utils.AddressFromSecp256k1PubKey(publicKey)
		privateKey.Zero()
		if err != nil {
			t.Fatal(err)
		}
		validators = append(validators, GenesisTrustValidator{Address: address, PublicKey: publicKey, IsMiner: true})
	}

	cfg := config.DefaultFrostConfig()
	participants, committeeDigests, err := ExpectedGenesisDeploymentTrust(validators, cfg)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(validators, func(i, j int) bool { return validators[i].Address < validators[j].Address })
	if len(participants) != 5 {
		t.Fatalf("participants = %d, want the five-member committee union", len(participants))
	}
	for i, participant := range participants {
		if participant.Index != uint64(i) || participant.ID != validators[i].Address {
			t.Fatalf("participant %d = (%d,%s), want deterministic index %d member %s", i, participant.Index, participant.ID, i, validators[i].Address)
		}
	}

	if len(committeeDigests) != len(genesisCeremonyChainOrder) {
		t.Fatalf("committee digests = %d, want %d", len(committeeDigests), len(genesisCeremonyChainOrder))
	}
	for i, chainName := range genesisCeremonyChainOrder {
		indices := []uint64{0, 1, 2, 3, 4}
		addresses := make([]string, len(validators))
		for j := range validators {
			addresses[j] = validators[j].Address
		}
		selected, err := InitialCommitteeIndicesForChain(chainName, indices, addresses, 4)
		if err != nil {
			t.Fatal(err)
		}
		members := make([]string, len(selected))
		for offset, index := range selected {
			members[offset] = validators[index].Address
		}
		algorithm, err := configuredGenesisSignAlgo(chainName, cfg.Chains[chainName].SignAlgo)
		if err != nil {
			t.Fatal(err)
		}
		wantDigest, err := CommitteeDigest(chainName, 0, 1, algorithm, members)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(committeeDigests[i], wantDigest) {
			t.Fatalf("chain %s committee did not match the shared chain hash assignment", chainName)
		}
	}
}

func TestFreshInitialCommitteeValidatesCompleteRosterAndVaultIdentity(t *testing.T) {
	cfg := config.DefaultFrostConfig()
	indices := []uint64{4, 1, 3, 0, 2}
	addresses := make([]string, len(indices))
	roster := chainAssignmentRosterForTest(t, len(indices))
	for i, index := range indices {
		addresses[i] = roster[index]
	}
	before := append([]uint64(nil), indices...)
	for _, chain := range GenesisCeremonyChainOrder() {
		chainCfg := cfg.Chains[chain]
		chainCfg.VaultsPerChain = 3
		cfg.Chains[chain] = chainCfg
	}
	assignments, err := InitialCommitteeAssignments(cfg, indices, addresses)
	if err != nil {
		t.Fatal(err)
	}
	for _, chain := range GenesisCeremonyChainOrder() {
		want, err := InitialCommitteeIndicesForChain(chain, indices, addresses, 4)
		if err != nil {
			t.Fatal(err)
		}
		for vaultID := 0; vaultID < 3; vaultID++ {
			got := assignments[chain][vaultID]
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s/%d committee=%v want=%v", chain, vaultID, got, want)
			}
			got[0] = 99
		}
	}
	if !reflect.DeepEqual(indices, before) {
		t.Fatal("committee assignment mutated the full candidate roster")
	}
	if _, err := InitialCommitteeIndices(cfg, indices, "btc", 0); err == nil {
		t.Fatal("fresh indices-only API silently assigned unbound addresses")
	}
}

                                                                              
                                                                                  
                                                                         
func BenchmarkFreshGenesisVaultAssignment(b *testing.B) {
	for _, n := range []int{5, 20, config.VaultCandidateSnapshotLimit} {
		for _, vaults := range []int{6, MaxVaults / 6 * 6} {
			indices := make([]uint64, n)
			addresses := chainAssignmentRosterForTest(b, n)
			for i := range indices {
				indices[i] = uint64(i)
			}
			cfg := config.DefaultFrostConfig()
			for chain, chainCfg := range cfg.Chains {
				chainCfg.VaultsPerChain = vaults / 6
				cfg.Chains[chain] = chainCfg
			}
			name := fmt.Sprintf("N%d_V%d", n, vaults)
			b.Run(name+"/rotation-baseline", func(b *testing.B) {
				b.ReportAllocs()
				for iteration := 0; iteration < b.N; iteration++ {
					for _, chain := range GenesisCeremonyChainOrder() {
						for vaultID := 0; vaultID < vaults/6; vaultID++ {
							benchmarkGenesisCommittee = legacyRotatedCommitteeForBenchmark(cfg, indices, chain, uint32(vaultID))
						}
					}
				}
			})
			b.Run(name+"/canonical-topk-baseline", func(b *testing.B) {
				b.ReportAllocs()
				topk := indices[:cfg.Vault.DefaultK]
				for iteration := 0; iteration < b.N; iteration++ {
					for _, chain := range GenesisCeremonyChainOrder() {
						for vaultID := 0; vaultID < cfg.Chains[chain].VaultsPerChain; vaultID++ {
							benchmarkGenesisCommittee = canonicalTopKCommitteeBeforeChainHash(cfg, topk, chain, uint32(vaultID))
						}
					}
				}
			})
			b.Run(name+"/chain-hash", func(b *testing.B) {
				b.ReportAllocs()
				for iteration := 0; iteration < b.N; iteration++ {
					assignments, err := InitialCommitteeAssignments(cfg, indices, addresses)
					if err != nil {
						b.Fatal(err)
					}
					benchmarkGenesisCommittee = assignments["btc"][0]
				}
			})
		}
	}
}

var benchmarkGenesisCommittee []uint64

                                                                            
                                                                               
func canonicalTopKCommitteeBeforeChainHash(cfg config.FrostConfig, indices []uint64, chain string, vaultID uint32) []uint64 {
	k := cfg.Vault.DefaultK
	if k <= 0 || len(indices) < k {
		panic("insufficient initial committee candidates")
	}
	sorted := indices
	if !sort.SliceIsSorted(indices, func(i, j int) bool { return indices[i] < indices[j] }) {
		sorted = assignment.SortByBitIndex(indices)
	}
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			panic("duplicate initial committee candidate")
		}
	}
	found := false
	for _, name := range genesisCeremonyChainOrder {
		chainCfg, ok := cfg.Chains[name]
		if !ok {
			continue
		}
		if chainCfg.VaultsPerChain <= 0 {
			panic("invalid genesis vault count")
		}
		if name == chain {
			if vaultID >= uint32(chainCfg.VaultsPerChain) {
				panic("unknown genesis vault")
			}
			found = true
			break
		}
	}
	if !found {
		panic("unknown genesis chain")
	}
	return append([]uint64(nil), sorted[:k]...)
}

                                                                                
func legacyRotatedCommitteeForBenchmark(cfg config.FrostConfig, indices []uint64, chain string, vaultID uint32) []uint64 {
	sorted := assignment.SortByBitIndex(indices)
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			panic("duplicate candidate")
		}
	}
	ordinal := 0
	found := false
	for _, name := range GenesisCeremonyChainOrder() {
		chainConfig, ok := cfg.Chains[name]
		if !ok {
			continue
		}
		if chainConfig.VaultsPerChain <= 0 {
			panic("invalid vault count")
		}
		if name == chain {
			if vaultID >= uint32(chainConfig.VaultsPerChain) {
				panic("unknown vault")
			}
			ordinal += int(vaultID)
			found = true
			break
		}
		ordinal += chainConfig.VaultsPerChain
	}
	if !found {
		panic("unknown chain")
	}
	result := make([]uint64, cfg.Vault.DefaultK)
	for i := range result {
		result[i] = sorted[(ordinal+i)%len(sorted)]
	}
	return result
}

func TestExpectedGenesisDeploymentTrustRejectsNonProtocolThresholdPolicy(t *testing.T) {
	validators := make([]GenesisTrustValidator, 0, 5)
	for scalar := byte(1); scalar <= 5; scalar++ {
		raw := make([]byte, 32)
		raw[31] = scalar
		privateKey, _ := btcec.PrivKeyFromBytes(raw)
		publicKey := privateKey.PubKey().SerializeCompressed()
		address, err := utils.AddressFromSecp256k1PubKey(publicKey)
		privateKey.Zero()
		if err != nil {
			t.Fatal(err)
		}
		validators = append(validators, GenesisTrustValidator{Address: address, PublicKey: publicKey, IsMiner: true})
	}

	t.Run("unsupported K", func(t *testing.T) {
		cfg := config.DefaultFrostConfig()
		cfg.Vault.DefaultK = 5
		if _, _, err := ExpectedGenesisDeploymentTrust(validators, cfg); err == nil {
			t.Fatal("unsupported genesis committee size accepted")
		}
	})
	t.Run("non exact ratio", func(t *testing.T) {
		cfg := config.DefaultFrostConfig()
		cfg.Vault.ThresholdRatio = 0.74
		if _, _, err := ExpectedGenesisDeploymentTrust(validators, cfg); err == nil {
			t.Fatal("non-3/4 genesis threshold ratio accepted")
		}
	})
}
