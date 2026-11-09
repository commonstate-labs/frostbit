package vm

import (
	"bytes"
	"dex/frost/core/curve"
	"dex/frost/security"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"math/big"
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestVerifyRevealInDryRunUsesActiveMinerECIESPubKey(t *testing.T) {
	sv := NewMockStateView()

	km := utils.NewKeyManager()
	if err := km.InitKeyRandom(); err != nil {
		t.Fatalf("InitKeyRandom: %v", err)
	}
	receiverID := km.GetAddress()
	compressedPub := km.PublicKeyBytes()
	xOnlyPub, err := utils.NormalizeSecp256k1XOnlyPubKey(compressedPub)
	if err != nil {
		t.Fatalf("NormalizeSecp256k1XOnlyPubKey: %v", err)
	}

	account := &pb.Account{
		Address: receiverID,
		PublicKeys: &pb.PublicKeys{
			Keys: map[int32][]byte{
				int32(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340): xOnlyPub,
			},
		},
	}
	accountData, err := proto.Marshal(account)
	if err != nil {
		t.Fatalf("marshal account: %v", err)
	}
	sv.Set(keys.KeyAccount(receiverID), accountData)

	activeMiners := &pb.FrostActiveMiners{
		Addresses:  []string{receiverID},
		PublicKeys: [][]byte{compressedPub},
	}
	activeMinersData, err := proto.Marshal(activeMiners)
	if err != nil {
		t.Fatalf("marshal active miners: %v", err)
	}
	sv.Set(keys.KeyFrostActiveMiners(), activeMinersData)

	share := bytes.Repeat([]byte{0x11}, 32)
	encRand := bytes.Repeat([]byte{0x22}, 32)
	ciphertext, err := security.ECIESEncrypt(compressedPub, share, encRand)
	if err != nil {
		t.Fatalf("ECIESEncrypt: %v", err)
	}

	grp := curve.NewSecp256k1Group()
	commitmentPoints := [][]byte{grp.SerializePoint(grp.ScalarBaseMult(new(big.Int).SetBytes(share)))}

	if !verifyRevealInDryRun(sv, receiverID, share, encRand, ciphertext, commitmentPoints, 1, pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340) {
		t.Fatal("expected reveal verification to succeed using active miner ECIES pubkey")
	}
}

                                                                                                                   
                                                                                                           
                                                                                                                 
func TestVerifyRevealInDryRun_BN128_HonestDealerNotSlashed(t *testing.T) {
	sv := NewMockStateView()

	km := utils.NewKeyManager()
	if err := km.InitKeyRandom(); err != nil {
		t.Fatalf("InitKeyRandom: %v", err)
	}
	receiverID := km.GetAddress()
	compressedPub := km.PublicKeyBytes()
	xOnlyPub, err := utils.NormalizeSecp256k1XOnlyPubKey(compressedPub)
	if err != nil {
		t.Fatalf("NormalizeSecp256k1XOnlyPubKey: %v", err)
	}

	account := &pb.Account{
		Address: receiverID,
		PublicKeys: &pb.PublicKeys{
			Keys: map[int32][]byte{
				int32(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340): xOnlyPub,
			},
		},
	}
	accountData, err := proto.Marshal(account)
	if err != nil {
		t.Fatalf("marshal account: %v", err)
	}
	sv.Set(keys.KeyAccount(receiverID), accountData)

	activeMiners := &pb.FrostActiveMiners{
		Addresses:  []string{receiverID},
		PublicKeys: [][]byte{compressedPub},
	}
	activeMinersData, err := proto.Marshal(activeMiners)
	if err != nil {
		t.Fatalf("marshal active miners: %v", err)
	}
	sv.Set(keys.KeyFrostActiveMiners(), activeMinersData)

	share := bytes.Repeat([]byte{0x33}, 32)
	encRand := bytes.Repeat([]byte{0x44}, 32)
	ciphertext, err := security.ECIESEncrypt(compressedPub, share, encRand)
	if err != nil {
		t.Fatalf("ECIESEncrypt: %v", err)
	}

	                                                                                            
	                                                                                   
	bnGrp := curve.NewBN256Group()
	commitmentPoints := [][]byte{bnGrp.SerializePoint(bnGrp.ScalarBaseMult(new(big.Int).SetBytes(share)))}

	if !verifyRevealInDryRun(sv, receiverID, share, encRand, ciphertext, commitmentPoints, 1, pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128) {
		t.Fatal("D-1 regression: BN128 vault honest dealer reveal must verify as valid")
	}
}

                                                                                                                                       
                                                                                                            
func TestVerifyShareAgainstCommitment_BN128Bytes_ReturnsFalseOnLegacyAPI(t *testing.T) {
	bnGrp := curve.NewBN256Group()
	share := bytes.Repeat([]byte{0x55}, 32)
	commitmentPoints := [][]byte{bnGrp.SerializePoint(bnGrp.ScalarBaseMult(new(big.Int).SetBytes(share)))}

	if security.VerifyShareAgainstCommitment(share, commitmentPoints, big.NewInt(1)) {
		t.Fatal("legacy secp256k1 API must reject BN128-formatted commitment points")
	}
	if !security.VerifyShareAgainstCommitmentOnGroup(share, commitmentPoints, big.NewInt(1), bnGrp) {
		t.Fatal("algo-aware API must accept BN128-formatted commitment points")
	}
}
