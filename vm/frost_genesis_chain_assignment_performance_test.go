package vm

import (
	"fmt"
	"testing"
	"time"

	"dex/config"
	"dex/keys"
)

                                                                            
                                                                             
                                                                      
func TestFreshGenesisChainHashInitBoundedIORealState(t *testing.T) {
	f := newCustodyTimingFixture(t, 20000)
	for _, vaultCount := range []uint32{1, 42} {
		t.Run(fmt.Sprintf("vaults-%d", vaultCount), func(t *testing.T) {
			sv, io := f.view(t, true)
			cfg := testVaultCommitteePolicyConfig("btc", 4)
			cfg.VaultCount = vaultCount
			sv.Set(keys.KeyFrostVaultConfig("btc", 0), custodyTimingMarshal(t, cfg))
			sv.Set(keys.KeyFrostActiveMiners(), custodyTimingMarshal(t, testGenesisFrostSnapshot(canonicalGenesisAddressesForTest(t, config.VaultCandidateSnapshotLimit))))
			sv.Del(keys.KeyFrostVaultState("btc", 0))
			releaseStateViewHistory(sv.StateView)
			io.reset()
			started := time.Now()
			if err := InitVaultStates(sv, "btc", 1); err != nil {
				t.Fatal(err)
			}
			elapsed := time.Since(started)
			if io.logicalGets != int(vaultCount)+2 || io.sets != int(vaultCount) || io.dels != 0 || io.scans != 0 || io.batches != 0 || io.logicalBatches != 0 {
				t.Fatalf("initial assignment amplified state IO: %+v", *io)
			}
			t.Logf("N=%d V=%d overlay=20000 pointGets=%d writes=%d scans=%d elapsed=%s", config.VaultCandidateSnapshotLimit, vaultCount, io.logicalGets, io.sets, io.scans, elapsed)
		})
	}
}
