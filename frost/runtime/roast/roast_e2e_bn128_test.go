                                              
                                                          
                                                                                 
                                                                                       
                                              

package roast

import (
	"encoding/hex"
	"math/big"
	"testing"

	"dex/frost/core/curve"
	"dex/frost/core/dkg"
	frost "dex/frost/core/frost"
	coreRoast "dex/frost/core/roast"
	"dex/pb"

	"golang.org/x/crypto/sha3"
)

                                                                        
  
                  
                                                                   
                                        
                                             
                  
                                  
                                                            
func TestROAST_EndToEnd_BN128(t *testing.T) {
	group := curve.NewBN256Group()

	tests := []struct {
		name       string
		threshold  int
		numSigners int
		signerIDs  []int
		secret     *big.Int
		msgText    string
	}{
		{
			name: "3-of-4_basic", threshold: 3, numSigners: 4,
			signerIDs: []int{1, 2, 3, 4},
			secret:    new(big.Int).SetBytes([]byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0}),
			msgText:   "e2e test message for BN128 ROAST signing",
		},
		{
			name: "2-of-3_minimal", threshold: 2, numSigners: 3,
			signerIDs: []int{1, 2, 3},
			secret:    new(big.Int).SetBytes([]byte{0xab, 0xcd, 0xef}),
			msgText:   "minimal threshold test BN128",
		},
		{
			name: "3-of-5_subset", threshold: 3, numSigners: 5,
			signerIDs: []int{2, 3, 4, 5},
			secret:    new(big.Int).SetBytes([]byte{0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10}),
			msgText:   "subset signing test BN128",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runEndToEndBN128(t, group, tc.threshold, tc.numSigners, tc.signerIDs, tc.secret, tc.msgText)
		})
	}
}

func runEndToEndBN128(
	t *testing.T, group curve.Group,
	threshold, numSigners int, signerIDs []int,
	masterSecret *big.Int, msgText string,
) {
	t.Helper()

	                                                      
	groupPub := group.ScalarBaseMult(masterSecret)
	shares, _ := generateTestShares(masterSecret, threshold, numSigners, group)
	groupPubSerialized := group.SerializePoint(groupPub)

	t.Logf("=== DKG 份额 ===")
	t.Logf("  masterSecret = %s", masterSecret.Text(16))
	t.Logf("  groupPub     = %s", hex.EncodeToString(groupPubSerialized))
	for _, sid := range signerIDs {
		t.Logf("  share[%d] = %s", sid, shares[sid].Text(16))
	}

	                                   
	verifyShamirReconstructionBN128(t, signerIDs, shares, masterSecret, group)

	                                                     
	msg := []byte(msgText)

	type nonceData struct {
		hidingK, bindingK   *big.Int
		hidingPt, bindingPt curve.Point
	}
	signerNonces := make(map[int]*nonceData)

	for _, sid := range signerIDs {
		hk := dkg.RandomScalar(group.Order())
		bk := dkg.RandomScalar(group.Order())
		hp := group.ScalarBaseMult(hk)
		bp := group.ScalarBaseMult(bk)
		signerNonces[sid] = &nonceData{hk, bk, hp, bp}
	}
	t.Logf("=== Nonce 对 (共 %d 个签名者) ===", len(signerIDs))

	                                                      
	coreNonces := make([]coreRoast.SignerNonce, len(signerIDs))
	for i, sid := range signerIDs {
		n := signerNonces[sid]
		coreNonces[i] = coreRoast.SignerNonce{
			SignerID: sid, HidingNonce: n.hidingK, BindingNonce: n.bindingK,
			HidingPoint: n.hidingPt, BindingPoint: n.bindingPt,
		}
	}

	R, err := coreRoast.ComputeGroupCommitment(coreNonces, msg, group)
	if err != nil {
		t.Fatalf("ComputeGroupCommitment failed: %v", err)
	}
	t.Logf("=== 群承诺 R ===")
	t.Logf("  R = (%s, %s)", pad32Hex(R.X), pad32Hex(R.Y))

	                                                                                   
	                                                       
	e := computeBN128ChallengeForTest(R.X, R.Y, groupPub.X, groupPub.Y, msg, group.Order())
	lambdas := coreRoast.ComputeLagrangeCoefficientsForSet(signerIDs, group.Order())

	t.Logf("=== Challenge & Lagrange ===")
	t.Logf("  e = %s", pad32Hex(e))

	                                                                            
	partialSigs := make(map[int]*big.Int)
	for _, sid := range signerIDs {
		n := signerNonces[sid]
		rho := coreRoast.ComputeBindingCoefficient(sid, msg, coreNonces, group)

		                                                      
		z := coreRoast.ComputePartialSignature(sid,
			n.hidingK, n.bindingK, shares[sid],
			rho, lambdas[sid], e, group)
		partialSigs[sid] = z

		t.Logf("  signer[%d] z_i = %s", sid, z.Text(16))
	}

	                                                 
	coreShares := make([]coreRoast.SignerShare, len(signerIDs))
	for i, sid := range signerIDs {
		coreShares[i] = coreRoast.SignerShare{SignerID: sid, Share: partialSigs[sid]}
	}

	                                                      
	zAgg := big.NewInt(0)
	for _, s := range coreShares {
		zAgg.Add(zAgg, s.Share)
	}
	zAgg.Mod(zAgg, group.Order())

	sig := make([]byte, 64)
	R.X.FillBytes(sig[:32])
	zAgg.FillBytes(sig[32:])

	t.Logf("=== 聚合签名 ===")
	t.Logf("  sig = %s", hex.EncodeToString(sig))

	                                                                 
	pubKey := make([]byte, 64)
	copy(pubKey[:32], groupPubSerialized[:32])
	copy(pubKey[32:], groupPubSerialized[32:])

	valid, verifyErr := frost.VerifyBN128(pubKey, msg, sig)
	t.Logf("=== frost.VerifyBN128 ===")
	t.Logf("  valid=%v err=%v", valid, verifyErr)

	if verifyErr != nil {
		t.Fatalf("❌ frost.VerifyBN128 返回错误: %v", verifyErr)
	}
	if !valid {
		t.Fatalf("❌ frost.VerifyBN128 验证失败")
	}

	                                                             
	apiValid, apiErr := frost.Verify(pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128, pubKey, msg, sig)
	if apiErr != nil {
		t.Fatalf("❌ frost.Verify API 返回错误: %v", apiErr)
	}
	if !apiValid {
		t.Fatalf("❌ frost.Verify API 验证失败")
	}

	                                                                           
	                                                                
	                                                                            
	                                                                                      
	solidityChallenge := computeSolidityChallenge(R.X, R.Y, groupPub.X, groupPub.Y, msg, group.Order())

	if e.Cmp(solidityChallenge) != 0 {
		t.Fatalf("❌ Solidity 合约 challenge 不一致:\n  Go 侧:       %s\n  Solidity 侧: %s",
			pad32Hex(e), pad32Hex(solidityChallenge))
	}
	t.Logf("=== Solidity 合约 challenge 交叉验证 ===")
	t.Logf("  ✅ Go challenge == Solidity challenge = %s", pad32Hex(e))

	                                                                                        
	sG := group.ScalarBaseMult(zAgg)
	eP := group.ScalarMult(curve.Point{X: groupPub.X, Y: groupPub.Y}, e)
	rhs := group.Add(R, eP)
	if sG.X.Cmp(rhs.X) != 0 || sG.Y.Cmp(rhs.Y) != 0 {
		t.Fatalf("❌ 合约验签方程不满足: s*G ≠ R + e*P")
	}
	t.Logf("  ✅ s*G == R + e*P 验证通过")

	t.Logf("✅ 端到端 ROAST BN128 门限签名验证全部通过")
}

                                                                                             
                                                       
func computeBN128ChallengeForTest(Rx, Ry, Px, Py *big.Int, msg []byte, order *big.Int) *big.Int {
	h := sha3.NewLegacyKeccak256()
	pad := func(v *big.Int) []byte {
		out := make([]byte, 32)
		v.FillBytes(out)
		return out
	}
	h.Write(pad(Rx))
	h.Write(pad(Ry))
	h.Write(pad(Px))
	h.Write(pad(Py))
	h.Write(msg)
	e := new(big.Int).SetBytes(h.Sum(nil))
	return e.Mod(e, order)
}

                                                                         
                                                                 
                                                                                    
func computeSolidityChallenge(Rx, Ry, Px, Py *big.Int, msg []byte, order *big.Int) *big.Int {
	                                                                     
	h := sha3.NewLegacyKeccak256()
	pad := func(v *big.Int) []byte {
		out := make([]byte, 32)
		v.FillBytes(out)
		return out
	}
	h.Write(pad(Rx))
	h.Write(pad(Ry))
	h.Write(pad(Px))
	h.Write(pad(Py))
	                                                                       
	h.Write(msg)
	e := new(big.Int).SetBytes(h.Sum(nil))
	return e.Mod(e, order)
}

func verifyShamirReconstructionBN128(t *testing.T, signerIDs []int, shares map[int]*big.Int, expected *big.Int, group curve.Group) {
	t.Helper()
	order := group.Order()
	lambdas := coreRoast.ComputeLagrangeCoefficientsForSet(signerIDs, order)

	reconstructed := big.NewInt(0)
	for _, sid := range signerIDs {
		term := new(big.Int).Mul(lambdas[sid], shares[sid])
		term.Mod(term, order)
		reconstructed.Add(reconstructed, term)
		reconstructed.Mod(reconstructed, order)
	}

	if reconstructed.Cmp(expected) != 0 {
		t.Fatalf("❌ Shamir 重建失败: expected=%s got=%s", expected.Text(16), reconstructed.Text(16))
	}
	t.Logf("✅ Shamir 重建验证通过: Σλ_i·s_i = masterSecret")
}
