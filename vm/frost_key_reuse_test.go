package vm

import (
	"dex/config"
	"dex/keys"
	"dex/pb"
	"strings"
	"testing"
)

func TestRetainedGenesisKeysNeverStartAutomaticDKG(t *testing.T) {
	for _, mode := range []string{"rotation", "expansion", "ordinary-expansion"} {
		t.Run(mode, func(t *testing.T) {
			current := frostAutoPolicyMembers("original", 4)
			top := frostAutoPolicyMembers("replacement", 16)
			executor, sv, cfg, _, snapshot := seedFrostAutoTransitionPolicy(t, current, top, 16, 2, 3)
			if mode == "rotation" {
				cfg.CommitteeScaleMinerMultiplier = 100
				frostAutoPolicyStoreProto(t, sv, keys.KeyFrostVaultConfig(cfg.Chain, 0), cfg)
			}
			if mode != "ordinary-expansion" {
				sv.Set(keys.KeyFrostConfiguredGenesisMarker(), []byte(config.RetainedFrostGenesisMarkerPrefix+strings.Repeat("a", 64)))
			}
			for _, h := range []uint64{1, 200, 201, 10000, 864001} {
				executor.processFrostAutoTransitions(&pb.Block{Header: &pb.BlockHeader{Height: h}}, snapshot, sv)
			}
			_, exists, err := sv.Get(keys.KeyFrostVaultTransition(cfg.Chain, 0, 2))
			if err != nil {
				t.Fatal(err)
			}
			if exists != (mode == "ordinary-expansion") {
				t.Fatalf("unexpected transition for %s", mode)
			}
		})
	}
}
