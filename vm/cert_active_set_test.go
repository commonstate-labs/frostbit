package vm

import (
	"testing"

	"dex/keys"
	"dex/liveness"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

func seedCertificateMinerAccount(t *testing.T, sv StateView, address string, blsKey []byte) {
	t.Helper()
	raw, err := proto.Marshal(&pb.Account{
		Address: address,
		PublicKeys: &pb.PublicKeys{Keys: map[int32][]byte{
			int32(pb.SignAlgo_SIGN_ALGO_BLS_BN256_G2): blsKey,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyAccount(address), raw)
}

func TestBuildCertificateActiveMinersExcludesProbationAndUsesHotStake(t *testing.T) {
	sv := NewStateView(nil, nil)
	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	blsKey, err := keyManager.BLSPublicKeyBytes()
	if err != nil {
		t.Fatal(err)
	}
	seedCertificateMinerAccount(t, sv, "active-hot", blsKey)
	seedCertificateMinerAccount(t, sv, "probation-hot", blsKey)
	SetBalance(sv, "active-hot", "FB", &pb.TokenBalance{Balance: "0", MinerLockedBalance: "70000"})
	SetBalance(sv, "probation-hot", "FB", &pb.TokenBalance{Balance: "0", MinerLockedBalance: "90000"})

	livenessCfg := liveness.DefaultConfig()
	livenessCfg.MinStake = 1                                      
	tracker := liveness.NewTracker(livenessCfg)
	if _, err := tracker.RegisterMinerAtHeight(sv, "active-hot", 70000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.RegisterMinerAtHeight(sv, "probation-hot", 90000, 1); err != nil {
		t.Fatal(err)
	}
	miners, err := BuildCertificateActiveMiners(tracker, sv)
	if err != nil {
		t.Fatal(err)
	}
	if len(miners) != 1 {
		t.Fatalf("certificate Active set size=%d want 1", len(miners))
	}
	if miners[0].Address != "active-hot" || miners[0].EffectiveWeight != 70000 {
		t.Fatalf("unexpected Active record: %+v", miners[0])
	}
}
