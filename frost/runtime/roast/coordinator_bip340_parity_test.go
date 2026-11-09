package roast

                                                                           
                                                                                                          
                                                                                           
                             
  
                                                                                                

import (
	"crypto/sha256"
	"math/big"
	"testing"

	"dex/frost/core/curve"
	frost "dex/frost/core/frost"
	coreRoast "dex/frost/core/roast"
	"dex/pb"
)

                                                                                                             
func findMasterSecretWithOddGroupPubY(group curve.Group) *big.Int {
	                                 
	seed := new(big.Int).SetBytes([]byte{0xde, 0xad, 0xbe, 0xef, 0xca, 0xfe, 0xba, 0xbe})
	for i := 0; i < 10000; i++ {
		candidate := new(big.Int).Add(seed, big.NewInt(int64(i)))
		pub := group.ScalarBaseMult(candidate)
		if pub.Y.Bit(0) == 1 {               
			return candidate
		}
	}
	panic("unreachable: could not find master secret with odd Y group pubkey")
}

                                                                       
func findNoncesWithOddRy(signerIDs []int, msg []byte, group curve.Group) ([]coreRoast.SignerNonce, curve.Point) {
	for attempt := 0; attempt < 10000; attempt++ {
		nonces := make([]coreRoast.SignerNonce, len(signerIDs))
		for i, sid := range signerIDs {
			signer := NewSignerService(NodeID("node"), nil, nil)
			n, _ := signer.GenerateNonce()
			nonces[i] = coreRoast.SignerNonce{
				SignerID: sid, HidingNonce: n.HidingNonce, BindingNonce: n.BindingNonce,
				HidingPoint: n.HidingPoint, BindingPoint: n.BindingPoint,
			}
		}
		R, err := coreRoast.ComputeGroupCommitment(nonces, msg, group)
		if err != nil {
			continue
		}
		if R.Y != nil && R.Y.Bit(0) == 1 {                 
			return nonces, R
		}
	}
	panic("unreachable: could not find nonces with odd R.y")
}

                                                                 
  
                                                   
                                                              
                                                                                 
                                                                  
                                                                              
                                                                        
func TestBIP340_DoubleNegation_Bug(t *testing.T) {
	group := curve.NewSecp256k1Group()

	                                         
	masterSecret := findMasterSecretWithOddGroupPubY(group)
	groupPub := group.ScalarBaseMult(masterSecret)
	groupPubSerialized := group.SerializePoint(groupPub)                               
	if groupPubSerialized[0] != 0x03 {
		t.Fatalf("expected group pubkey prefix 0x03, got 0x%02x", groupPubSerialized[0])
	}
	t.Logf("✅ group pubkey prefix = 0x03 (Y is odd)")

	threshold, numSigners := 2, 4
	shares, _ := generateTestShares(masterSecret, threshold, numSigners, group)
	signerIDs := []int{1, 2, 3}               

	msg := sha256.Sum256([]byte("BIP340 double negation test"))

	                                                    
	nonces, R := findNoncesWithOddRy(signerIDs, msg[:], group)
	t.Logf("✅ R.y is odd (parity=%d)", R.Y.Bit(0))

	                                                    
	lambdas := coreRoast.ComputeLagrangeCoefficientsForSet(signerIDs, group.Order())
	e := coreRoast.ComputeChallenge(R, groupPub.X, msg[:], group)

	partialSigs := make([]coreRoast.SignerShare, len(signerIDs))
	for i, sid := range signerIDs {
		myShare := new(big.Int).Set(shares[sid])

		                                             
		                                          
		normalizedShare := normalizeSecretShareForBIP340(
			pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
			groupPubSerialized,
			myShare,
		)

		                                        
		                                    
		adjH, adjB := adjustNoncePairForBIP340(
			pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
			CurvePoint(R),
			nonces[i].HidingNonce,
			nonces[i].BindingNonce,
		)

		rho := coreRoast.ComputeBindingCoefficient(sid, msg[:], nonces, group)

		                                                                                   
		z := coreRoast.ComputePartialSignature(
			sid, adjH, adjB, normalizedShare, rho, lambdas[sid], e, group,
		)
		partialSigs[i] = coreRoast.SignerShare{SignerID: sid, Share: z}
	}
	t.Logf("✅ Participant 端 partial signatures 已生成（含 BIP340 parity 调整）")

	                                                                              
	sigBuggy, err := coreRoast.AggregateSignatures(R, partialSigs, group)
	if err != nil {
		t.Fatalf("AggregateSignatures failed: %v", err)
	}

	                                 
	xOnlyPubkey := groupPubSerialized[1:]                                

	                                                 
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Logf("✅ AggregateSignatures (legacy) 验证 panic（符合预期）: %v", r)
			}
		}()
		valid, verifyErr := frost.VerifyBIP340(xOnlyPubkey, msg[:], sigBuggy)
		if verifyErr != nil {
			t.Logf("✅ AggregateSignatures (legacy) 验证出错（符合预期）: %v", verifyErr)
			return
		}
		if valid {
			t.Fatalf("❌ 不应通过验证！AggregateSignatures 不应该产生合法的 BIP340 签名（当 participant 已做 parity 调整时）")
		}
		t.Logf("✅ AggregateSignatures (legacy) 验证失败（符合预期）: valid=false")
	}()

	                                                                             
	sigFixed, err := coreRoast.AggregateSignaturesBIP340(R, partialSigs, group)
	if err != nil {
		t.Fatalf("AggregateSignaturesBIP340 failed: %v", err)
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("❌ AggregateSignaturesBIP340 验证 panic（不应发生）: %v", r)
			}
		}()
		valid, verifyErr := frost.VerifyBIP340(xOnlyPubkey, msg[:], sigFixed)
		if verifyErr != nil {
			t.Fatalf("❌ AggregateSignaturesBIP340 验证出错: %v", verifyErr)
		}
		if !valid {
			t.Fatalf("❌ AggregateSignaturesBIP340 验证失败")
		}
		t.Logf("✅ AggregateSignaturesBIP340 验证通过！签名合法")
	}()

	t.Logf("\n=== 结论 ===")
	t.Logf("当 Participant 端已做 BIP340 parity 调整时：")
	t.Logf("  AggregateSignatures   → ❌ 验证失败（R.y 奇数时 z 被重复取反）")
	t.Logf("  AggregateSignaturesBIP340 → ✅ 验证成功")
	t.Logf("修复：Secp256k1ROASTExecutor.AggregateSignatures 应调用 AggregateSignaturesBIP340")
}
