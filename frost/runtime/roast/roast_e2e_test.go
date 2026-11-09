                                        
                                         
                                                                                               
                                                                      

package roast

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"testing"

	"dex/frost/core/curve"
	frost "dex/frost/core/frost"
	coreRoast "dex/frost/core/roast"
	"dex/frost/runtime/session"
	"dex/pb"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

                                                                
  
                  
                                                 
                                        
                                                                                                
                              
                                                                 
                                         
  
                                                                  
func TestROAST_EndToEnd_BIP340(t *testing.T) {
	group := curve.NewSecp256k1Group()
	signAlgo := int32(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340)

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
			msgText:   "e2e test message for BIP340 ROAST signing",
		},
		{
			name: "2-of-3_minimal", threshold: 2, numSigners: 3,
			signerIDs: []int{1, 2, 3},
			secret:    new(big.Int).SetBytes([]byte{0xab, 0xcd, 0xef}),
			msgText:   "minimal threshold test",
		},
		{
			name: "3-of-5_subset", threshold: 3, numSigners: 5,
			signerIDs: []int{2, 3, 4, 5},                               
			secret:    new(big.Int).SetBytes([]byte{0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10}),
			msgText:   "subset signing test",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runEndToEndROAST(t, group, signAlgo, tc.threshold, tc.numSigners, tc.signerIDs, tc.secret, tc.msgText)
		})
	}
}

func runEndToEndROAST(
	t *testing.T, group curve.Group, signAlgo int32,
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
	t.Logf("  groupPub.Y parity = %d (0=偶, 1=奇)", groupPub.Y.Bit(0))
	for _, sid := range signerIDs {
		t.Logf("  share[%d] = %s", sid, shares[sid].Text(16))
	}

	                                                                  
	verifyShamirReconstruction(t, signerIDs, shares, masterSecret, group)

	                                                     
	msg := sha256.Sum256([]byte(msgText))
	type nonceData struct {
		hidingK, bindingK   *big.Int
		hidingPt, bindingPt curve.Point
	}
	signerNonces := make(map[int]*nonceData)

	for _, sid := range signerIDs {
		h, b, hp, bp := generateNoncePairForTest(group)
		signerNonces[sid] = &nonceData{h, b, hp, bp}
	}
	t.Logf("=== Nonce 对 (共 %d 个签名者) ===", len(signerIDs))
	for _, sid := range signerIDs {
		n := signerNonces[sid]
		t.Logf("  signer[%d] hiding_k=%s hiding_pt=%s", sid, n.hidingK.Text(16), hex.EncodeToString(group.SerializePoint(n.hidingPt)))
	}

	                                                      
	coreNonces := make([]coreRoast.SignerNonce, len(signerIDs))
	for i, sid := range signerIDs {
		n := signerNonces[sid]
		coreNonces[i] = coreRoast.SignerNonce{
			SignerID: sid, HidingNonce: n.hidingK, BindingNonce: n.bindingK,
			HidingPoint: n.hidingPt, BindingPoint: n.bindingPt,
		}
	}

	R, err := coreRoast.ComputeGroupCommitment(coreNonces, msg[:], group)
	if err != nil {
		t.Fatalf("ComputeGroupCommitment failed: %v", err)
	}
	t.Logf("=== 群承诺 R ===")
	t.Logf("  R.X = %s", pad32Hex(R.X))
	t.Logf("  R.Y parity = %d (0=偶, 1=奇)", R.Y.Bit(0))

	                                                                            
	                                                              
	e := coreRoast.ComputeChallenge(R, groupPub.X, msg[:], group)
	lambdas := coreRoast.ComputeLagrangeCoefficientsForSet(signerIDs, group.Order())

	t.Logf("=== Challenge & Lagrange ===")
	t.Logf("  e = %s", pad32Hex(e))
	for _, sid := range signerIDs {
		t.Logf("  λ[%d] = %s", sid, lambdas[sid].Text(16))
	}

	partialSigs := make(map[int]*big.Int)
	for _, sid := range signerIDs {
		n := signerNonces[sid]
		rho := coreRoast.ComputeBindingCoefficient(sid, msg[:], coreNonces, group)

		                                                                                                   
		normShare := normalizeSecretShareForBIP340(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, groupPubSerialized, new(big.Int).Set(shares[sid]))
		adjH, adjB := adjustNoncePairForBIP340(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, CurvePoint(R), new(big.Int).Set(n.hidingK), new(big.Int).Set(n.bindingK))

		z := coreRoast.ComputePartialSignature(sid, adjH, adjB, normShare, rho, lambdas[sid], e, group)
		partialSigs[sid] = z

		t.Logf("=== Signer[%d] partial sig ===", sid)
		t.Logf("  rho = %s", rho.Text(16))
		t.Logf("  share_orig = %s", shares[sid].Text(16))
		t.Logf("  share_norm = %s", normShare.Text(16))
		t.Logf("  nonce_adj  = hiding_negated=%v", adjH.Cmp(n.hidingK) != 0)
		t.Logf("  z_i = %s", z.Text(16))
	}

	                                                                                       
	coreShares := make([]coreRoast.SignerShare, len(signerIDs))
	for i, sid := range signerIDs {
		coreShares[i] = coreRoast.SignerShare{SignerID: sid, Share: partialSigs[sid]}
	}

	sig, err := coreRoast.AggregateSignaturesBIP340(R, coreShares, group)
	if err != nil {
		t.Fatalf("AggregateSignaturesBIP340 failed: %v", err)
	}
	t.Logf("=== 聚合签名 ===")
	t.Logf("  sig = %s", hex.EncodeToString(sig))

	                                                                  
	xOnlyPubkey := make([]byte, 32)
	groupPub.X.FillBytes(xOnlyPubkey)

	frostValid, frostErr, frostPanic := verifyBIP340Safe(xOnlyPubkey, msg[:], sig)
	t.Logf("=== frost.VerifyBIP340 ===")
	t.Logf("  valid=%v err=%v panic=%v", frostValid, frostErr, frostPanic)

	                                                                          
	btcecValid := verifyWithBtcec(t, groupPub.X, groupPub.Y, msg[:], sig)
	t.Logf("=== btcec/schnorr.Verify ===")
	t.Logf("  valid=%v", btcecValid)

	                                                                 
	coordinator := newTestCoordinator(groupPubSerialized)
	committee := make([]session.Participant, numSigners)
	for i := 0; i < numSigners; i++ {
		committee[i] = session.Participant{ID: fmt.Sprintf("node%d", i), Index: i}
	}
	sess := session.NewSession(session.SessionParams{
		JobID: "e2e_test", VaultID: 0, Chain: "btc", KeyEpoch: 1,
		SignAlgo: signAlgo, Messages: [][]byte{msg[:]}, Committee: committee, Threshold: threshold,
	})
	sess.Start()
	selectedSet := make([]int, len(signerIDs))
	for i, sid := range signerIDs {
		selectedSet[i] = sid - 1
	}
	sess.SelectedSet = selectedSet
	sess.SetState(session.SignSessionStateCollectingNonces)

	for _, sid := range signerIDs {
		n := signerNonces[sid]
		sess.AddNonce(sid-1, [][]byte{group.SerializePoint(n.hidingPt)}, [][]byte{group.SerializePoint(n.bindingPt)})
	}
	sess.SetState(session.SignSessionStateCollectingShares)
	for _, sid := range signerIDs {
		shareBytes := make([]byte, 32)
		partialSigs[sid].FillBytes(shareBytes)
		sess.AddShare(sid-1, [][]byte{shareBytes})
	}

	coordSigs, coordErr := coordinator.aggregateSignatures(sess)
	coordValid := coordErr == nil && len(coordSigs) > 0 && coordSigs[0] != nil
	t.Logf("=== Coordinator.aggregateSignatures ===")
	t.Logf("  err=%v valid=%v", coordErr, coordValid)
	if coordValid {
		t.Logf("  sig=%s", hex.EncodeToString(coordSigs[0]))
	}

	                                         
	if !frostValid || frostPanic != nil {
		t.Errorf("❌ frost.VerifyBIP340 失败: valid=%v panic=%v", frostValid, frostPanic)

		                                                
		z := new(big.Int).SetBytes(sig[32:])
		lhs := group.ScalarBaseMultBytes(z.Bytes())
		eP := group.ScalarMultBytes(curve.Point{X: groupPub.X, Y: evenY(group, groupPub.Y)}, e.Bytes())
		rhs := group.Add(R, eP)
		t.Logf("  诊断 z·G:    (%s, %s)", pad32Hex(lhs.X), pad32Hex(lhs.Y))
		t.Logf("  诊断 R+e·P:  (%s, %s)", pad32Hex(rhs.X), pad32Hex(rhs.Y))
		t.Logf("  match: X=%v Y=%v", lhs.X.Cmp(rhs.X) == 0, lhs.Y.Cmp(rhs.Y) == 0)
	}
	if !btcecValid {
		t.Errorf("❌ btcec/schnorr.Verify 失败")
	}
	if !coordValid {
		t.Errorf("❌ Coordinator.aggregateSignatures 失败: %v", coordErr)
	}

	if frostValid && btcecValid && coordValid {
		t.Logf("✅ 端到端 ROAST BIP340 签名验证全部通过")
	}
}

                                                                                              
                                      
func TestROAST_EndToEnd_BIP340_SingleSigner(t *testing.T) {
	group := curve.NewSecp256k1Group()

	masterSecret := new(big.Int).SetBytes([]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x11, 0x22})
	groupPub := group.ScalarBaseMult(masterSecret)
	msg := sha256.Sum256([]byte("single signer baseline"))

	                                                   
	Rx, _, z, _ := frost.SchnorrSign(group, masterSecret, msg[:])

	baselineSig := make([]byte, 64)
	Rx.FillBytes(baselineSig[:32])
	z.FillBytes(baselineSig[32:])

	btcecValid := verifyWithBtcec(t, groupPub.X, groupPub.Y, msg[:], baselineSig)
	if !btcecValid {
		t.Fatalf("❌ baseline SchnorrSign 验证失败，说明验签函数本身有问题")
	}
	t.Logf("✅ baseline SchnorrSign 验证通过")
}

                                         

func pad32Hex(x *big.Int) string {
	b := make([]byte, 32)
	x.FillBytes(b)
	return hex.EncodeToString(b)
}

func evenY(group curve.Group, y *big.Int) *big.Int {
	if y.Bit(0) == 0 {
		return y
	}
	return new(big.Int).Sub(group.Modulus(), y)
}

func verifyShamirReconstruction(t *testing.T, signerIDs []int, shares map[int]*big.Int, expected *big.Int, group curve.Group) {
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

func verifyBIP340Safe(pubkey, msg, sig []byte) (valid bool, err error, panicVal any) {
	defer func() {
		if r := recover(); r != nil {
			panicVal = r
		}
	}()
	valid, err = frost.VerifyBIP340(pubkey, msg, sig)
	return
}

func verifyWithBtcec(t *testing.T, pubX, pubY *big.Int, msg, sig []byte) bool {
	t.Helper()
	if len(sig) != 64 {
		t.Logf("btcec verify: invalid sig length %d", len(sig))
		return false
	}

	                      
	btcPubKey, err := btcec.ParsePubKey(serializeCompressed(pubX, pubY))
	if err != nil {
		t.Logf("btcec ParsePubKey failed: %v", err)
		return false
	}

	                        
	schnorrSig, err := schnorr.ParseSignature(sig)
	if err != nil {
		t.Logf("schnorr ParseSignature failed: %v", err)
		return false
	}

	return schnorrSig.Verify(msg, btcPubKey)
}

func serializeCompressed(x, y *big.Int) []byte {
	b := make([]byte, 33)
	if y.Bit(0) == 0 {
		b[0] = 0x02
	} else {
		b[0] = 0x03
	}
	x.FillBytes(b[1:])
	return b
}
