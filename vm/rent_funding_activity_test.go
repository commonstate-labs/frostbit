package vm

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/rent"
)

func TestRentFundingConversionEmitsExplorerActivity(t *testing.T) {
	sv, asset, _ := fundingFixture(t, false, 5000)
	sv, _ = persistFundingFixture(sv)

	result, err := newRentFundingAttempt(sv, nil)(fundingOwner, 10, big.NewInt(1200), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ReceivedFB == nil || result.ReceivedFB.String() != "1200" {
		t.Fatalf("received FB = %v, want 1200", result.ReceivedFB)
	}

	model, err := buildBlockReadModel(&pb.Block{
		BlockHash: "block-10",
		Header:    &pb.BlockHeader{Height: 10, PrevBlockHash: "block-9"},
	}, sv.Diff())
	if err != nil {
		t.Fatalf("build read model: %v", err)
	}
	mutations := readModelMutationMap(model)
	var activityKey string
	var activitySeq uint32
	var record UserActivityRecord
	for key, mutation := range mutations {
		if mutation.Op != pb.ReadModelMutationOp_READ_MODEL_MUTATION_PUT {
			continue
		}
		addr, height, seq, ok := parseCanonicalActivityKey(strings.TrimPrefix(key, keys.KeyUserActivityFamilyPrefix()))
		if !ok || addr != fundingOwner || height != 10 {
			continue
		}
		var candidate UserActivityRecord
		if json.Unmarshal(mutation.Value, &candidate) == nil && candidate.Kind == ActKindRentConversion {
			activityKey, activitySeq, record = key, seq, candidate
			break
		}
	}
	if activityKey == "" {
		t.Fatal("missing rent conversion activity mutation")
	}
	if record.Height != 10 || record.Amount != "1200" || record.Token != rent.FeeToken || record.Ref != asset || record.TxID == "" {
		t.Fatalf("unexpected rent conversion activity: %+v", record)
	}
	kindKey := keys.KeyUserActivityKindIndex(fundingOwner, ActKindRentConversion, 10, record.TxID, activitySeq)
	if mutation := mutations[kindKey]; mutation == nil || mutation.Op != pb.ReadModelMutationOp_READ_MODEL_MUTATION_PUT {
		t.Fatalf("missing rent conversion kind projection %q", kindKey)
	}
	if got := string(mutations[kindKey].Value); got != activityKey {
		t.Fatalf("rent conversion kind projection points to %q, want %q", got, activityKey)
	}
}
