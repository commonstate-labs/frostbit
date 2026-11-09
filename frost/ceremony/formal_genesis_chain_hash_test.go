package ceremony

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"testing"

	"dex/config"
	"dex/utils"

	"github.com/btcsuite/btcd/btcec/v2"
)

type formalGenesisCandidate struct {
	index uint64
	id    string
	key   []byte
}

func formalGenesisHashRoster(t *testing.T, count int) []formalGenesisCandidate {
	t.Helper()
	result := make([]formalGenesisCandidate, count)
	for i := range result {
		private, _ := btcec.PrivKeyFromBytes(big.NewInt(int64(90000 + i)).Bytes())
		public := private.PubKey().SerializeCompressed()
		private.Zero()
		id, err := utils.AddressFromSecp256k1PubKey(public)
		if err != nil {
			t.Fatal(err)
		}
		result[i] = formalGenesisCandidate{index: uint64(1000 + 7*i), id: id, key: public}
	}
	return result
}

                                                                             
func formalGenesisHashReference(roster []formalGenesisCandidate, chain string, k, topN int) []uint64 {
	candidates := append([]formalGenesisCandidate(nil), roster...)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].id < candidates[j].id })
	if topN > 0 && len(candidates) > topN {
		candidates = candidates[:topN]
	}
	sort.Slice(candidates, func(i, j int) bool {
		a := sha256.Sum256([]byte("DEX_GENESIS_COMMITTEE_CHAIN_RANK_V1\x00" + chain + "\x00" + candidates[i].id))
		b := sha256.Sum256([]byte("DEX_GENESIS_COMMITTEE_CHAIN_RANK_V1\x00" + chain + "\x00" + candidates[j].id))
		if order := bytes.Compare(a[:], b[:]); order != 0 {
			return order < 0
		}
		return candidates[i].id < candidates[j].id
	})
	result := make([]uint64, k)
	for i := range result {
		result[i] = candidates[i].index
	}
	return result
}

func formalGenesisHashInput(roster []formalGenesisCandidate, reverse bool) ([]uint64, []string) {
	indices, addresses := make([]uint64, len(roster)), make([]string, len(roster))
	for i := range roster {
		position := i
		if reverse {
			position = len(roster) - 1 - i
		}
		indices[i], addresses[i] = roster[position].index, roster[position].id
	}
	return indices, addresses
}

func TestFormalGenesisChainHashIndependentSHA256AndIndexProjection(t *testing.T) {
	for _, count := range []int{4, 5, 8, 25, 256} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("n%d-reverse%t", count, reverse), func(t *testing.T) {
				roster := formalGenesisHashRoster(t, count)
				indices, addresses := formalGenesisHashInput(roster, reverse)
				cfg := config.DefaultFrostConfig()
				cfg.Committee.TopN = count
				for name, chain := range cfg.Chains {
					chain.VaultsPerChain = 2
					cfg.Chains[name] = chain
				}
				all, err := InitialCommitteeAssignments(cfg, indices, addresses)
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range GenesisCeremonyChainOrder() {
					want := formalGenesisHashReference(roster, name, 4, count)
					actual, err := InitialCommitteeIndicesForChain(name, indices, addresses, 4)
					if err != nil || !reflect.DeepEqual(actual, want) || len(all[name]) != 2 {
						t.Fatalf("%s hash rank/index mismatch actual=%v expected=%v err=%v", name, actual, want, err)
					}
					for _, committee := range all[name] {
						if !reflect.DeepEqual(committee, want) {
							t.Fatalf("%s vault membership/order differs", name)
						}
					}
					all[name][0][0]++
					if !reflect.DeepEqual(all[name][1], want) {
						t.Fatal("same-chain vault committees alias mutable storage")
					}
				}
			})
		}
	}
}

func TestFormalGenesisChainHashTrustUnionPreservesCanonicalIndices(t *testing.T) {
	roster := formalGenesisHashRoster(t, 25)
	sort.Slice(roster, func(i, j int) bool { return roster[i].id < roster[j].id })
	validators := make([]GenesisTrustValidator, len(roster))
	for i := range roster {
		roster[i].index = uint64(i)
		validators[len(roster)-1-i] = GenesisTrustValidator{Address: roster[i].id, PublicKey: roster[i].key, IsMiner: true}
	}
	cfg := config.DefaultFrostConfig()
	                                                                       
	                                                                            
	cfg.Committee.TopN = 25
	used := map[uint64]bool{}
	for _, name := range GenesisCeremonyChainOrder() {
		for _, index := range formalGenesisHashReference(roster, name, 4, 25) {
			used[index] = true
		}
	}
	participants, _, err := ExpectedGenesisDeploymentTrust(validators, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(participants) != len(used) {
		t.Fatal("trusted participants differ from exact used union")
	}
	position := 0
	for i, candidate := range roster {
		if !used[uint64(i)] {
			continue
		}
		p := participants[position]
		if p.Index != uint64(i) || p.ID != candidate.id || !bytes.Equal(p.PublicKey, candidate.key) {
			t.Fatal("union densely reindexed or substituted an identity")
		}
		position++
	}
}

func TestFormalGenesisChainHashAssignmentTopNPrecedesRanking(t *testing.T) {
	roster := formalGenesisHashRoster(t, 25)
	indices, addresses := formalGenesisHashInput(roster, true)
	cfg := config.DefaultFrostConfig()
	cfg.Committee.TopN = 4
	actual, err := InitialCommitteeAssignments(cfg, indices, addresses)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range GenesisCeremonyChainOrder() {
		want := formalGenesisHashReference(roster, name, 4, 4)
		for _, members := range actual[name] {
			if !reflect.DeepEqual(members, want) {
				t.Fatalf("%s selected candidates outside canonical TopN: actual=%v want=%v", name, members, want)
			}
		}
	}
	cfg.Committee.TopN = 3
	if _, err := InitialCommitteeAssignments(cfg, indices, addresses); err == nil {
		t.Fatal("TopN shortage silently reduced K")
	}
}

func TestFormalGenesisChainHashCollisionAndInvalidRosterFailClosed(t *testing.T) {
	a := initialCommitteeCandidate{index: 9, address: "a"}
	b := initialCommitteeCandidate{index: 1, address: "b"}
	if !initialCommitteeCandidateLess(a, b) || initialCommitteeCandidateLess(b, a) {
		t.Fatal("equal digest tie did not use canonical address order")
	}
	roster := formalGenesisHashRoster(t, 5)
	for _, kind := range []string{"duplicate-index", "duplicate-address", "bad-address", "short-roster", "unknown-chain", "case-chain"} {
		t.Run(kind, func(t *testing.T) {
			indices, addresses := formalGenesisHashInput(roster, false)
			name := "btc"
			switch kind {
			case "duplicate-index":
				indices[1] = indices[0]
			case "duplicate-address":
				addresses[1] = addresses[0]
			case "bad-address":
				addresses[0] = "miner-0"
			case "short-roster":
				indices, addresses = indices[:3], addresses[:3]
			case "unknown-chain":
				name = "other"
			case "case-chain":
				name = "BTC"
			}
			if _, err := InitialCommitteeIndicesForChain(name, indices, addresses, 4); err == nil {
				t.Fatal("invalid canonical roster or domain accepted")
			}
		})
	}
	indices, _ := formalGenesisHashInput(roster, false)
	if _, err := InitialCommitteeIndices(config.DefaultFrostConfig(), indices, "btc", 0); err == nil {
		t.Fatal("fresh indices-only entry fabricated an address-dependent assignment")
	}
}
