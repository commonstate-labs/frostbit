                               
                           

package roast

import (
	"crypto/sha256"
	"math/big"
	"testing"

	"dex/frost/core/curve"
	"dex/frost/core/roast"
)

                                                      
func TestSignerService_GenerateNonce(t *testing.T) {
	signer := NewSignerService(NodeID("node1"), nil, nil)

	nonce, err := signer.GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce failed: %v", err)
	}

	                         
	if nonce.HidingNonce.Cmp(big.NewInt(0)) == 0 {
		t.Error("HidingNonce should not be zero")
	}
	if nonce.BindingNonce.Cmp(big.NewInt(0)) == 0 {
		t.Error("BindingNonce should not be zero")
	}

	                                           
	group := curve.NewSecp256k1Group()
	expectedHiding := group.ScalarBaseMult(nonce.HidingNonce)
	if nonce.HidingPoint.X.Cmp(expectedHiding.X) != 0 || nonce.HidingPoint.Y.Cmp(expectedHiding.Y) != 0 {
		t.Error("HidingPoint mismatch")
	}

	expectedBinding := group.ScalarBaseMult(nonce.BindingNonce)
	if nonce.BindingPoint.X.Cmp(expectedBinding.X) != 0 || nonce.BindingPoint.Y.Cmp(expectedBinding.Y) != 0 {
		t.Error("BindingPoint mismatch")
	}
}

                                                                       
func TestSignerService_PartialSignature(t *testing.T) {
	group := curve.NewSecp256k1Group()
	n := group.Order()

	                             
	threshold := 3
	numSigners := 5

	                                                      
	masterSecret := new(big.Int).SetBytes([]byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0})
	groupPub := group.ScalarBaseMult(masterSecret)

	                                         
	shares, pubShares := generateTestShares(masterSecret, threshold, numSigners, group)

	                        
	signerIDs := []int{1, 2, 3}

	               
	msg := sha256.Sum256([]byte("test message"))

	                              
	nonces := make([]roast.SignerNonce, len(signerIDs))
	for i, signerID := range signerIDs {
		signer := NewSignerService(NodeID("node"), nil, nil)
		nonce, err := signer.GenerateNonce()
		if err != nil {
			t.Fatalf("GenerateNonce failed: %v", err)
		}
		nonces[i] = roast.SignerNonce{
			SignerID:     signerID,
			HidingNonce:  nonce.HidingNonce,
			BindingNonce: nonce.BindingNonce,
			HidingPoint:  nonce.HidingPoint,
			BindingPoint: nonce.BindingPoint,
		}
	}

	                  
	R, err := roast.ComputeGroupCommitment(nonces, msg[:], group)
	if err != nil {
		t.Fatalf("ComputeGroupCommitment failed: %v", err)
	}

	                  
	e := roast.ComputeChallenge(R, groupPub.X, msg[:], group)

	                           
	lambdas := roast.ComputeLagrangeCoefficientsForSet(signerIDs, n)

	                                    
	partialSigs := make([]roast.SignerShare, len(signerIDs))
	for i, signerID := range signerIDs {
		rho := roast.ComputeBindingCoefficient(signerID, msg[:], nonces, group)
		z := roast.ComputePartialSignature(
			signerID,
			nonces[i].HidingNonce,
			nonces[i].BindingNonce,
			shares[signerID],
			rho,
			lambdas[signerID],
			e,
			group,
		)
		partialSigs[i] = roast.SignerShare{SignerID: signerID, Share: z}

		                     
		valid := roast.VerifyPartialSignature(
			signerID,
			z,
			nonces[i],
			rho,
			lambdas[signerID],
			e,
			pubShares[signerID],
			group,
		)
		if !valid {
			t.Errorf("VerifyPartialSignature failed for signer %d", signerID)
		}
	}

	               
	sig, err := roast.AggregateSignatures(R, partialSigs, group)
	if err != nil {
		t.Fatalf("AggregateSignatures failed: %v", err)
	}

	                     
	if len(sig) != 64 {
		t.Errorf("signature length should be 64, got %d", len(sig))
	}

	t.Logf("Final signature: %x", sig)
	t.Logf("Group public key X: %x", groupPub.X.Bytes())
}

                                                    
func generateTestShares(secret *big.Int, t, n int, group curve.Group) (map[int]*big.Int, map[int]curve.Point) {
	order := group.Order()

	                             
	coeffs := make([]*big.Int, t)
	coeffs[0] = new(big.Int).Set(secret)
	for i := 1; i < t; i++ {
		coeffs[i] = big.NewInt(int64(1000 * (i + 1)))                                  
	}

	                                                  
	shares := make(map[int]*big.Int)
	pubShares := make(map[int]curve.Point)

	for i := 1; i <= n; i++ {
		x := big.NewInt(int64(i))
		share := evaluatePolynomial(coeffs, x, order)
		shares[i] = share
		pubShares[i] = group.ScalarBaseMult(share)
	}

	return shares, pubShares
}

                                                                                  
func evaluatePolynomial(coeffs []*big.Int, x, order *big.Int) *big.Int {
	result := big.NewInt(0)
	xPow := big.NewInt(1)           

	for _, coef := range coeffs {
		term := new(big.Int).Mul(coef, xPow)
		term.Mod(term, order)
		result.Add(result, term)
		result.Mod(result, order)

		xPow.Mul(xPow, x)
		xPow.Mod(xPow, order)
	}

	return result
}

                                                            
func TestSignerService_VerifyFinalSignature(t *testing.T) {
	group := curve.NewSecp256k1Group()

	                             
	threshold := 2
	numSigners := 3

	                  
	masterSecret := new(big.Int).SetBytes([]byte{0xab, 0xcd, 0xef, 0x12, 0x34, 0x56, 0x78, 0x9a})
	groupPub := group.ScalarBaseMult(masterSecret)

	               
	shares, _ := generateTestShares(masterSecret, threshold, numSigners, group)

	                        
	signerIDs := []int{1, 3}

	                                             
	lambdas := roast.ComputeLagrangeCoefficientsForSet(signerIDs, group.Order())

	reconstructed := big.NewInt(0)
	for _, id := range signerIDs {
		term := new(big.Int).Mul(shares[id], lambdas[id])
		term.Mod(term, group.Order())
		reconstructed.Add(reconstructed, term)
		reconstructed.Mod(reconstructed, group.Order())
	}

	if reconstructed.Cmp(masterSecret) != 0 {
		t.Errorf("Lagrange interpolation failed: expected %s, got %s", masterSecret.String(), reconstructed.String())
	}

	                        
	reconstructedPub := group.ScalarBaseMult(reconstructed)
	if reconstructedPub.X.Cmp(groupPub.X) != 0 || reconstructedPub.Y.Cmp(groupPub.Y) != 0 {
		t.Error("Reconstructed public key mismatch")
	}

	t.Logf("Master secret: %s", masterSecret.String())
	t.Logf("Reconstructed: %s", reconstructed.String())
	t.Logf("Group public key X: %x", groupPub.X.Bytes())
}

                                                           
func TestSignerService_MultipleMessages(t *testing.T) {
	group := curve.NewSecp256k1Group()

	                
	threshold := 3
	numSigners := 5

	masterSecret := new(big.Int).SetBytes([]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88})
	groupPub := group.ScalarBaseMult(masterSecret)

	shares, pubShares := generateTestShares(masterSecret, threshold, numSigners, group)

	signerIDs := []int{2, 3, 4}

	                     
	msg1 := sha256.Sum256([]byte("message 1"))
	msg2 := sha256.Sum256([]byte("message 2"))
	msg3 := sha256.Sum256([]byte("message 3"))
	messages := [][]byte{msg1[:], msg2[:], msg3[:]}

	for msgIdx, msg := range messages {
		t.Run("Message_"+string(rune('1'+msgIdx)), func(t *testing.T) {
			                
			nonces := make([]roast.SignerNonce, len(signerIDs))
			for i, signerID := range signerIDs {
				signer := NewSignerService(NodeID("node"), nil, nil)
				nonce, _ := signer.GenerateNonce()
				nonces[i] = roast.SignerNonce{
					SignerID:     signerID,
					HidingNonce:  nonce.HidingNonce,
					BindingNonce: nonce.BindingNonce,
					HidingPoint:  nonce.HidingPoint,
					BindingPoint: nonce.BindingPoint,
				}
			}

			                  
			R, _ := roast.ComputeGroupCommitment(nonces, msg, group)
			e := roast.ComputeChallenge(R, groupPub.X, msg, group)
			lambdas := roast.ComputeLagrangeCoefficientsForSet(signerIDs, group.Order())

			                     
			partialSigs := make([]roast.SignerShare, len(signerIDs))
			for i, signerID := range signerIDs {
				rho := roast.ComputeBindingCoefficient(signerID, msg, nonces, group)
				z := roast.ComputePartialSignature(
					signerID,
					nonces[i].HidingNonce,
					nonces[i].BindingNonce,
					shares[signerID],
					rho,
					lambdas[signerID],
					e,
					group,
				)
				partialSigs[i] = roast.SignerShare{SignerID: signerID, Share: z}

				                     
				valid := roast.VerifyPartialSignature(
					signerID, z, nonces[i], rho, lambdas[signerID], e, pubShares[signerID], group,
				)
				if !valid {
					t.Errorf("VerifyPartialSignature failed for signer %d", signerID)
				}
			}

			               
			sig, err := roast.AggregateSignatures(R, partialSigs, group)
			if err != nil {
				t.Fatalf("AggregateSignatures failed: %v", err)
			}

			if len(sig) != 64 {
				t.Errorf("signature length should be 64, got %d", len(sig))
			}

			t.Logf("Message %d signature: %x", msgIdx+1, sig[:16])
		})
	}
}
