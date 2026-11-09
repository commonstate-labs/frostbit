package vm

import (
	"bytes"
	"math/big"
	"testing"

	"dex/frost/core/curve"
	"dex/frost/security"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

                                                                                            
                                                                                                 
                                                                                            
                                                                                  
  
                                                                                                  
                                                    

                                                                                      
func seedReceiverECIESPubKey(t *testing.T, sv StateView, receiverID string, compressedPub []byte) {
	t.Helper()
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
	data, err := proto.Marshal(account)
	if err != nil {
		t.Fatalf("marshal account: %v", err)
	}
	sv.Set(keys.KeyAccount(receiverID), data)

	activeMiners := &pb.FrostActiveMiners{
		Addresses:  []string{receiverID},
		PublicKeys: [][]byte{compressedPub},
	}
	amData, err := proto.Marshal(activeMiners)
	if err != nil {
		t.Fatalf("marshal active miners: %v", err)
	}
	sv.Set(keys.KeyFrostActiveMiners(), amData)
}

                                                                                                      
                                                                                                                      
func TestVerifyRevealInDryRun_Secp256k1_MaliciousDealer_Rejected(t *testing.T) {
	sv := NewMockStateView()

	km := utils.NewKeyManager()
	if err := km.InitKeyRandom(); err != nil {
		t.Fatalf("InitKeyRandom: %v", err)
	}
	receiverID := km.GetAddress()
	pub := km.PublicKeyBytes()
	seedReceiverECIESPubKey(t, sv, receiverID, pub)

	secretA := bytes.Repeat([]byte{0xAA}, 32)                                
	secretB := bytes.Repeat([]byte{0xBB}, 32)                                                 
	encRand := bytes.Repeat([]byte{0x10}, 32)

	ciphertext, err := security.ECIESEncrypt(pub, secretB, encRand)
	if err != nil {
		t.Fatalf("ECIESEncrypt: %v", err)
	}

	                                                   
	grp := curve.NewSecp256k1Group()
	commitmentPoints := [][]byte{grp.SerializePoint(grp.ScalarBaseMult(new(big.Int).SetBytes(secretA)))}

	                                                                                             
	if verifyRevealInDryRun(sv, receiverID, secretB, encRand, ciphertext, commitmentPoints, 1, pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340) {
		t.Fatal("malicious dealer (commitment for secretA, share=secretB) must fail Feldman VSS")
	}
}

                                                                   
func TestVerifyRevealInDryRun_BN128_MaliciousDealer_Rejected(t *testing.T) {
	sv := NewMockStateView()

	km := utils.NewKeyManager()
	if err := km.InitKeyRandom(); err != nil {
		t.Fatalf("InitKeyRandom: %v", err)
	}
	receiverID := km.GetAddress()
	pub := km.PublicKeyBytes()
	seedReceiverECIESPubKey(t, sv, receiverID, pub)

	secretA := bytes.Repeat([]byte{0xAA}, 32)
	secretB := bytes.Repeat([]byte{0xBB}, 32)
	encRand := bytes.Repeat([]byte{0x10}, 32)

	ciphertext, err := security.ECIESEncrypt(pub, secretB, encRand)
	if err != nil {
		t.Fatalf("ECIESEncrypt: %v", err)
	}

	bn := curve.NewBN256Group()
	commitmentPoints := [][]byte{bn.SerializePoint(bn.ScalarBaseMult(new(big.Int).SetBytes(secretA)))}

	if verifyRevealInDryRun(sv, receiverID, secretB, encRand, ciphertext, commitmentPoints, 1, pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128) {
		t.Fatal("BN128 malicious dealer must fail Feldman VSS")
	}
}

                                                                                                                  
                                                                                       
func TestVerifyRevealInDryRun_LyingComplainant_CiphertextMismatch_Rejected(t *testing.T) {
	sv := NewMockStateView()

	km := utils.NewKeyManager()
	if err := km.InitKeyRandom(); err != nil {
		t.Fatalf("InitKeyRandom: %v", err)
	}
	receiverID := km.GetAddress()
	pub := km.PublicKeyBytes()
	seedReceiverECIESPubKey(t, sv, receiverID, pub)

	share := bytes.Repeat([]byte{0x77}, 32)
	encRand := bytes.Repeat([]byte{0x88}, 32)
	ciphertext, err := security.ECIESEncrypt(pub, share, encRand)
	if err != nil {
		t.Fatalf("ECIESEncrypt: %v", err)
	}

	                                                                                                   
	tamperedShare := bytes.Repeat([]byte{0x99}, 32)
	grp := curve.NewSecp256k1Group()
	commitmentPoints := [][]byte{grp.SerializePoint(grp.ScalarBaseMult(new(big.Int).SetBytes(share)))}

	if verifyRevealInDryRun(sv, receiverID, tamperedShare, encRand, ciphertext, commitmentPoints, 1, pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340) {
		t.Fatal("ciphertext/share mismatch (lying complainant) must be rejected before Feldman check")
	}
}

                                                                            
                                                                                                        
func TestVerifyRevealInDryRun_MultiCoeffPolynomial_WrongShare_Rejected(t *testing.T) {
	sv := NewMockStateView()

	km := utils.NewKeyManager()
	if err := km.InitKeyRandom(); err != nil {
		t.Fatalf("InitKeyRandom: %v", err)
	}
	receiverID := km.GetAddress()
	pub := km.PublicKeyBytes()
	seedReceiverECIESPubKey(t, sv, receiverID, pub)

	grp := curve.NewSecp256k1Group()
	n := grp.Order()

	a0 := big.NewInt(0).SetBytes(bytes.Repeat([]byte{0x03}, 32))
	a1 := big.NewInt(0).SetBytes(bytes.Repeat([]byte{0x07}, 32))
	a0.Mod(a0, n)
	a1.Mod(a1, n)

	receiverIdx := big.NewInt(2)
	correctShare := new(big.Int).Add(a0, new(big.Int).Mul(a1, receiverIdx))
	correctShare.Mod(correctShare, n)

	                                  
	wrongShare := new(big.Int).Add(correctShare, big.NewInt(1))
	wrongShare.Mod(wrongShare, n)
	wrongShareBytes := make([]byte, 32)
	wbb := wrongShare.Bytes()
	copy(wrongShareBytes[32-len(wbb):], wbb)

	encRand := bytes.Repeat([]byte{0x11}, 32)
	ciphertext, err := security.ECIESEncrypt(pub, wrongShareBytes, encRand)
	if err != nil {
		t.Fatalf("ECIESEncrypt: %v", err)
	}

	commitmentPoints := [][]byte{
		grp.SerializePoint(grp.ScalarBaseMult(a0)),
		grp.SerializePoint(grp.ScalarBaseMult(a1)),
	}

	if verifyRevealInDryRun(sv, receiverID, wrongShareBytes, encRand, ciphertext, commitmentPoints, int(receiverIdx.Int64()), pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340) {
		t.Fatal("wrong share for degree-1 polynomial must fail Feldman VSS")
	}

	                                                                                                                    
	correctShareBytes := make([]byte, 32)
	cbb := correctShare.Bytes()
	copy(correctShareBytes[32-len(cbb):], cbb)
	correctCipher, err := security.ECIESEncrypt(pub, correctShareBytes, encRand)
	if err != nil {
		t.Fatalf("ECIESEncrypt(correct): %v", err)
	}
	if !verifyRevealInDryRun(sv, receiverID, correctShareBytes, encRand, correctCipher, commitmentPoints, int(receiverIdx.Int64()), pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340) {
		t.Fatal("positive control: correct share for degree-1 polynomial must verify")
	}
}
