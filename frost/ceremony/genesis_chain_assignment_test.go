package ceremony

import (
	"encoding/binary"
	"reflect"
	"sort"
	"strings"
	"testing"

	"dex/config"
	"dex/utils"
)

                                                                           
var server5ChainAssignmentAddresses = []string{
	"fbc1q0nsdkdpy7rjfd4lncjt9m9grxuhygcsesuvh25",
	"fbc1q42e38p6ft6ryw24aqxcandt22wkjpptdjh3fzx",
	"fbc1q5wvmtgztpckpth3mpk4ahyxpjuq8wrnamzh8rn",
	"fbc1qwtw9rlj5ehd3nrtj8893y7ujkav8dsftjyuuvp",
	"fbc1qzn8zvd6674zssl8ckjh9a4htght3yt55sjeegr",
}

func chainAssignmentRosterForTest(t testing.TB, n int) []string {
	t.Helper()
	addresses := make([]string, n)
	for i := range addresses {
		var seed [8]byte
		binary.BigEndian.PutUint64(seed[:], uint64(i+1))
		var err error
		addresses[i], err = utils.AddressFromSecp256k1PubKey(seed[:])
		if err != nil {
			t.Fatal(err)
		}
	}
	return addresses
}

func TestInitialChainCommitteeServer5Vectors(t *testing.T) {
	                                                                             
	                                                                             
	want := map[string][]uint64{
		"arb": {3, 0, 2, 1}, "base": {3, 0, 4, 2}, "bnb": {1, 4, 0, 2},
		"btc": {0, 4, 3, 2}, "eth": {3, 1, 2, 0}, "trx": {4, 0, 2, 1},
	}
	indices := []uint64{0, 1, 2, 3, 4}
	used := make(map[uint64]bool)
	for chain, expected := range want {
		actual, err := InitialCommitteeIndicesForChain(chain, indices, server5ChainAssignmentAddresses, 4)
		if err != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatalf("%s members=%v want=%v err=%v", chain, actual, expected, err)
		}
		seen := make(map[uint64]bool)
		for _, index := range actual {
			if seen[index] {
				t.Fatal("a chain selected one validator more than once")
			}
			seen[index], used[index] = true, true
		}
		permutedIndices := []uint64{4, 1, 3, 0, 2}
		permutedAddresses := make([]string, len(indices))
		for i, index := range permutedIndices {
			permutedAddresses[i] = server5ChainAssignmentAddresses[index]
		}
		permuted, err := InitialCommitteeIndicesForChain(chain, permutedIndices, permutedAddresses, 4)
		if err != nil || !reflect.DeepEqual(permuted, actual) {
			t.Fatalf("input permutation changed %s assignment: %v %v", chain, permuted, err)
		}
	}
	if len(used) != 5 {
		t.Fatalf("server5 committee union=%d want=5", len(used))
	}
}

func TestInitialChainCommitteeRejectsUnboundRosterAndEncoding(t *testing.T) {
	indices := []uint64{0, 1, 2, 3, 4}
	for _, tc := range []struct {
		name string
		edit func(*config.FrostConfig, *[]uint64, *[]string)
	}{
		{"mismatched roster", func(_ *config.FrostConfig, _ *[]uint64, a *[]string) { *a = (*a)[:4] }},
		{"duplicate reserve index", func(_ *config.FrostConfig, i *[]uint64, _ *[]string) { (*i)[4] = 3 }},
		{"duplicate reserve address", func(_ *config.FrostConfig, _ *[]uint64, a *[]string) { (*a)[4] = (*a)[3] }},
		{"uppercase address", func(_ *config.FrostConfig, _ *[]uint64, a *[]string) { (*a)[4] = strings.ToUpper((*a)[4]) }},
		{"space address", func(_ *config.FrostConfig, _ *[]uint64, a *[]string) { (*a)[4] += " " }},
		{"invalid address", func(_ *config.FrostConfig, _ *[]uint64, a *[]string) { (*a)[4] = "miner-4" }},
		{"negative topn", func(c *config.FrostConfig, _ *[]uint64, _ *[]string) { c.Committee.TopN = -1 }},
		{"topn below k", func(c *config.FrostConfig, _ *[]uint64, _ *[]string) { c.Committee.TopN = 3 }},
		{"unsupported k", func(c *config.FrostConfig, _ *[]uint64, _ *[]string) { c.Vault.DefaultK = 5 }},
		{"unknown chain", func(c *config.FrostConfig, _ *[]uint64, _ *[]string) { c.Chains["BTC"] = c.Chains["btc"] }},
		{"invalid vault count", func(c *config.FrostConfig, _ *[]uint64, _ *[]string) {
			cc := c.Chains["btc"]
			cc.VaultsPerChain = 0
			c.Chains["btc"] = cc
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultFrostConfig()
			cfg.Committee.TopN = 4                                                 
			i, a := append([]uint64(nil), indices...), append([]string(nil), server5ChainAssignmentAddresses...)
			tc.edit(&cfg, &i, &a)
			if _, err := InitialCommitteeAssignments(cfg, i, a); err == nil {
				t.Fatal("invalid full roster/configuration accepted")
			}
		})
	}
	if _, err := InitialCommitteeIndicesForChain("BTC", indices, server5ChainAssignmentAddresses, 4); err == nil {
		t.Fatal("noncanonical chain accepted")
	}
}

func TestInitialChainCommitteeTopNAndCommitteeTierBoundaries(t *testing.T) {
	for _, k := range config.VaultCommitteeTiers {
		addresses := chainAssignmentRosterForTest(t, k+1)
		indices := make([]uint64, len(addresses))
		for i := range indices {
			indices[i] = uint64(i)
		}
		cfg := config.DefaultFrostConfig()
		cfg.Vault.DefaultK, cfg.Committee.TopN = k, k
		assignments, err := InitialCommitteeAssignments(cfg, indices, addresses)
		if err != nil {
			t.Fatal(err)
		}
		sorted := append([]string(nil), addresses...)
		sort.Strings(sorted)
		allowed := make(map[string]bool)
		for _, address := range sorted[:k] {
			allowed[address] = true
		}
		for _, committees := range assignments {
			if len(committees[0]) != k {
				t.Fatal("incorrect committee tier")
			}
			for _, index := range committees[0] {
				if !allowed[addresses[index]] {
					t.Fatal("TopN was applied after chain ranking")
				}
			}
		}
		cfg.Committee.TopN = 0
		if _, err := InitialCommitteeAssignments(cfg, indices, addresses); err != nil {
			t.Fatalf("unlimited TopN: %v", err)
		}
		if _, err := InitialCommitteeIndicesForChain("btc", indices[:k-1], addresses[:k-1], k); err == nil {
			t.Fatal("K-1 candidates accepted")
		}
	}
}

func TestInitialChainCommitteeHashCollisionUsesCanonicalAddress(t *testing.T) {
	a := initialCommitteeCandidate{index: 99, address: server5ChainAssignmentAddresses[0]}
	b := initialCommitteeCandidate{index: 0, address: server5ChainAssignmentAddresses[1]}
	if !initialCommitteeCandidateLess(a, b) || initialCommitteeCandidateLess(b, a) || initialCommitteeCandidateLess(a, a) {
		t.Fatal("equal hash ranks must use canonical address as the total-order tie breaker")
	}
}
