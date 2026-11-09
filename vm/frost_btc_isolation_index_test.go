package vm

import (
	"math"
	"strconv"
	"testing"

	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestBTCIsolationIndexPreservesLegacyUnsignedAmounts(t *testing.T) {
	for _, rawAmount := range []string{"000100000", strconv.FormatUint(math.MaxUint64, 10)} {
		t.Run(rawAmount, func(t *testing.T) {
			sv := seedBTCIsolationMigration(t, 1)
			key := keys.KeyFrostBtcUtxo(0, "0000000000000000000000000000000000000000000000000000000000000001", 0)
			raw, _, _ := sv.Get(key)
			var u pb.FrostUtxo
			if err := proto.Unmarshal(raw, &u); err != nil {
				t.Fatal(err)
			}
			u.Amount, u.OriginId, u.OriginLegacy = rawAmount, nil, false
			sv.Set(key, mustMarshalProto(t, &u))
			if err := processBTCIsolationAmountIndex(sv); err != nil {
				t.Fatalf("legacy index blocked chain execution: %v", err)
			}
			amount, _ := strconv.ParseUint(rawAmount, 10, 64)
			canonical := strconv.FormatUint(amount, 10)
			for _, recordKey := range []string{key, keys.KeyFrostBtcUtxoFIFOIndex(0, 1), keys.KeyFrostBtcSpendableUtxoIndex(0, 1), keys.KeyFrostBtcUtxoAmount(0, amount, u.Txid, 0)} {
				data, found, _ := sv.Get(recordKey)
				var indexed pb.FrostUtxo
				if !found || proto.Unmarshal(data, &indexed) != nil || indexed.Amount != canonical || len(indexed.OriginId) != 32 || !indexed.OriginLegacy {
					t.Fatalf("legacy record mismatch: %s %+v", recordKey, &indexed)
				}
			}
		})
	}
}

func TestBTCIsolationImpossibleLegacyMigrationIsBlockedWithoutDroppingInput(t *testing.T) {
	sv := seedBTCIsolationMigration(t, 1)
	txid := "0000000000000000000000000000000000000000000000000000000000000001"
	key := keys.KeyFrostBtcUtxo(0, txid, 0)
	raw, _, _ := sv.Get(key)
	var u pb.FrostUtxo
	if err := proto.Unmarshal(raw, &u); err != nil {
		t.Fatal(err)
	}
	u.Amount = strconv.FormatUint(math.MaxUint64, 10)
	sv.Set(key, mustMarshalProto(t, &u))
	if err := (&Executor{}).processFrostMigrationIntents(&pb.Block{Header: &pb.BlockHeader{Height: 50}}, sv); err != nil {
		t.Fatalf("unspendable legacy record stopped the block: %v", err)
	}
	transitionRaw, _, _ := sv.Get(keys.KeyFrostVaultTransition("btc", 0, 2))
	var transition pb.VaultTransitionState
	if proto.Unmarshal(transitionRaw, &transition) != nil || transition.MigrationStatus != migrationStatusIntentBlocked || transition.MigrationError == "" {
		t.Fatalf("missing explicit blocked state: %+v", &transition)
	}
	if _, exists, _ := sv.Get(key); !exists {
		t.Fatal("impossible legacy input was silently dropped")
	}
	if _, exists, _ := sv.Get(keys.KeyFrostBtcLockedUtxo(0, txid, 0)); exists {
		t.Fatal("rejected manifest left an input lock")
	}
}
