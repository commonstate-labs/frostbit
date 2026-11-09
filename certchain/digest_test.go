package certchain

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"

	"dex/pb"
	"dex/utils"

	"go.dedis.ch/kyber/v3"
)

                                                     
type minerKey struct {
	priv *ecdsa.PrivateKey
	pub  kyber.Point
	idx  uint32
	addr string
}

func makeMiners(t *testing.T, n int) []minerKey {
	t.Helper()
	out := make([]minerKey, n)
	for i := 0; i < n; i++ {
		priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("ecdsa keygen: %v", err)
		}
		pub, err := utils.GetPublicKey(priv)
		if err != nil {
			t.Fatalf("bls pub: %v", err)
		}
		out[i] = minerKey{priv: priv, pub: pub, idx: uint32(i), addr: string([]byte{'a', 'd', 'd', '0' + byte(i)})}
	}
	return out
}

func minerSetMap(miners []minerKey) map[uint32]*MinerSnapshot {
	m := make(map[uint32]*MinerSnapshot, len(miners))
	for _, mk := range miners {
		raw, _ := utils.SerializePublicKey(mk.pub)
		m[mk.idx] = &MinerSnapshot{
			Index:           mk.idx,
			Address:         mk.addr,
			PubKey:          raw,
			EffectiveWeight: 100,
		}
	}
	return m
}

                                                              
func assembleCert(t *testing.T, height, prevCheckpoint uint64, blockHash, stateRoot []byte, diffs []MinerDiffEntry, miners []minerKey) *pb.CheckpointCertificate {
	t.Helper()
	normalizedDiffs := append([]MinerDiffEntry(nil), diffs...)
	for i := range normalizedDiffs {
		if normalizedDiffs[i].Joined && normalizedDiffs[i].EffectiveWeight == 0 {
			normalizedDiffs[i].EffectiveWeight = 100
		}
	}
	current := minerSetMap(miners)
	committee, _, err := SelectTopStakeCommittee(current)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := cloneMinerSet(current)
	if err := applyMinerDiffs(next, normalizedDiffs); err != nil {
		t.Fatalf("apply test cert diffs: %v", err)
	}
	nextRoot, err := ValidatorSetRoot(pb.GetDefaultChainID(), next)
	if err != nil {
		t.Fatal(err)
	}
	digest := ComputeCertDigest(CertDigestInput{
		ChainID:              pb.GetDefaultChainID(),
		Height:               height,
		BlockHash:            blockHash,
		StateRoot:            stateRoot,
		PrevCheckpoint:       prevCheckpoint,
		MinerCount:           uint32(len(current)),
		CommitteeVersion:     CommitteeVersionTopStakeV1,
		CommitteeSize:        uint32(len(committee)),
		NextValidatorSetRoot: nextRoot,
		Diffs:                normalizedDiffs,
	})
	sigs := make([][]byte, 0, len(committee))
	pubs := make([]kyber.Point, 0, len(committee))
	maxIndex := uint32(0)
	for index := range committee {
		if index > maxIndex {
			maxIndex = index
		}
	}
	bitmap := make([]byte, maxIndex/8+1)
	for _, mk := range miners {
		if committee[mk.idx] == nil {
			continue
		}
		s, err := utils.BLSSignWithCache(mk.priv, string(digest))
		if err != nil {
			t.Fatalf("bls sign: %v", err)
		}
		sigs = append(sigs, s)
		pubs = append(pubs, mk.pub)
		bitmap[mk.idx/8] |= 1 << (mk.idx % 8)
	}
	agg, err := utils.AggregateBLS(sigs)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if err := utils.BLSAggregateVerify(pubs, digest, agg); err != nil {
		t.Fatalf("self-verify: %v", err)
	}
	pbDiffs := minerDiffsToPB(normalizedDiffs)
	return &pb.CheckpointCertificate{
		Height:               height,
		BlockHash:            blockHash,
		StateRoot:            stateRoot,
		Diffs:                pbDiffs,
		Bitmap:               bitmap,
		AggSig:               agg,
		PrevCheckpoint:       prevCheckpoint,
		MinerCount:           uint32(len(current)),
		SignerCount:          int32(len(committee)),
		ChainId:              pb.GetDefaultChainID(),
		CommitteeVersion:     CommitteeVersionTopStakeV1,
		CommitteeSize:        uint32(len(committee)),
		NextValidatorSetRoot: nextRoot,
	}
}

                                                                                 
func TestCertDigest_HeightTamperingRejected(t *testing.T) {
	miners := makeMiners(t, 3)
	cert := assembleCert(t, 100, 0, []byte("blk-100"), []byte("root-100"), nil, miners)

	                
	cert.Height = 200

	if _, _, err := VerifyCertChain(minerSetMap(miners), []*pb.CheckpointCertificate{cert}, 0.667, 100); err == nil {
		t.Fatal("expected verification to fail when height is tampered")
	}
}

                                     
func TestCertDigest_PrevCheckpointTamperingRejected(t *testing.T) {
	miners := makeMiners(t, 3)
	cert := assembleCert(t, 100, 0, []byte("blk-100"), []byte("root-100"), nil, miners)
	cert.PrevCheckpoint = 50
	if _, _, err := VerifyCertChain(minerSetMap(miners), []*pb.CheckpointCertificate{cert}, 0.667, 100); err == nil {
		t.Fatal("expected verification to fail when prev_checkpoint is tampered")
	}
}

func TestCertChain_FirstCertificateMustAnchorGenesis(t *testing.T) {
	miners := makeMiners(t, 3)
	cert := assembleCert(t, 200, 100, []byte("blk-200"), []byte("root-200"), nil, miners)
	if _, _, err := VerifyCertChain(minerSetMap(miners), []*pb.CheckpointCertificate{cert}, 0.667, 100); err == nil {
		t.Fatal("expected standalone certificate above genesis checkpoint to be rejected")
	}
}

                                                                  
func TestCertDigest_DiffsTamperingRejected(t *testing.T) {
	miners := makeMiners(t, 3)
	cert := assembleCert(t, 100, 0, []byte("blk-100"), []byte("root-100"), nil, miners)

	                                 
	cert.Diffs = []*pb.CertMinerDiffEntry{
		{Index: 9, Joined: true, Address: "attacker", PubKey: []byte("attacker-pk")},
	}
	if _, _, err := VerifyCertChain(minerSetMap(miners), []*pb.CheckpointCertificate{cert}, 0.667, 100); err == nil {
		t.Fatal("expected verification to fail when diffs are tampered")
	}
}

                                
func TestCertDigest_BlockHashTamperingRejected(t *testing.T) {
	miners := makeMiners(t, 3)
	cert := assembleCert(t, 100, 0, []byte("blk-100"), []byte("root-100"), nil, miners)
	cert.BlockHash = []byte("blk-999")
	if _, _, err := VerifyCertChain(minerSetMap(miners), []*pb.CheckpointCertificate{cert}, 0.667, 100); err == nil {
		t.Fatal("expected verification to fail when block_hash is tampered")
	}
}

func TestCertDigest_CommitteeMetadataTamperingRejected(t *testing.T) {
	miners := makeMiners(t, 3)
	tests := []struct {
		name   string
		mutate func(*pb.CheckpointCertificate)
	}{
		{"chain-id", func(cert *pb.CheckpointCertificate) { cert.ChainId++ }},
		{"committee-size", func(cert *pb.CheckpointCertificate) { cert.CommitteeSize-- }},
		{"next-set-root", func(cert *pb.CheckpointCertificate) { cert.NextValidatorSetRoot[0] ^= 0xff }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cert := assembleCert(t, 100, 0, []byte("blk-100"), []byte("root-100"), nil, miners)
			tc.mutate(cert)
			if _, _, err := VerifyCertChain(minerSetMap(miners), []*pb.CheckpointCertificate{cert}, 0.667, 100); err == nil {
				t.Fatal("expected tampered committee metadata to be rejected")
			}
		})
	}
}

                           
func TestCertDigest_HappyPath(t *testing.T) {
	miners := makeMiners(t, 3)
	cert := assembleCert(t, 100, 0, []byte("blk-100"), []byte("root-100"), nil, miners)
	height, root, err := VerifyCertChain(minerSetMap(miners), []*pb.CheckpointCertificate{cert}, 0.667, 100)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if height != 100 {
		t.Fatalf("expected height 100, got %d", height)
	}
	if !bytes.Equal(root, []byte("root-100")) {
		t.Fatalf("unexpected root: %x", root)
	}
}

func TestCertChainReplaysActiveSetBeforeDerivingNextCommittee(t *testing.T) {
	miners := makeMiners(t, 4)
	initial := miners[:3]
	joinedPub, err := utils.SerializePublicKey(miners[3].pub)
	if err != nil {
		t.Fatal(err)
	}
	cert5 := assembleCert(t, 5, 0, []byte("blk-5"), []byte("root-5"), []MinerDiffEntry{{
		Index:           miners[3].idx,
		Joined:          true,
		Address:         miners[3].addr,
		PubKey:          joinedPub,
		EffectiveWeight: 100,
	}}, initial)
	cert10 := assembleCert(t, 10, 5, []byte("blk-10"), []byte("root-10"), nil, miners)

	height, root, err := VerifyCertChain(minerSetMap(initial), []*pb.CheckpointCertificate{cert5, cert10}, 0.667, 5)
	if err != nil {
		t.Fatalf("verify committee transition: %v", err)
	}
	if height != 10 || !bytes.Equal(root, []byte("root-10")) {
		t.Fatalf("unexpected verified anchor height=%d root=%q", height, root)
	}
}

                                             
func TestCertChain_SignerCountMismatchRejected(t *testing.T) {
	miners := makeMiners(t, 3)
	cert := assembleCert(t, 100, 0, []byte("blk-100"), []byte("root-100"), nil, miners)
	cert.SignerCount = 99
	if _, _, err := VerifyCertChain(minerSetMap(miners), []*pb.CheckpointCertificate{cert}, 0.667, 100); err == nil {
		t.Fatal("expected verification to fail on signer_count mismatch")
	}
}

                                                                            
func TestCertChain_NonMonotonicHeightRejected(t *testing.T) {
	miners := makeMiners(t, 3)
	cert1 := assembleCert(t, 100, 0, []byte("blk-100"), []byte("root-100"), nil, miners)
	                             
	cert3 := assembleCert(t, 300, 100, []byte("blk-300"), []byte("root-300"), nil, miners)
	if _, _, err := VerifyCertChain(minerSetMap(miners), []*pb.CheckpointCertificate{cert1, cert3}, 0.667, 100); err == nil {
		t.Fatal("expected verification to fail when interval is skipped")
	}
}
