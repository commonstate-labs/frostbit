                                 
                                 

package roast

import (
	"crypto/sha256"
	"math/big"
	"testing"

	"dex/frost/core/curve"
)

                                                         
func TestComputeBindingCoefficient(t *testing.T) {
	group := curve.NewSecp256k1Group()

	                      
	nonces := []SignerNonce{
		{
			SignerID:     1,
			HidingPoint:  group.ScalarBaseMult(big.NewInt(100)),
			BindingPoint: group.ScalarBaseMult(big.NewInt(200)),
		},
		{
			SignerID:     2,
			HidingPoint:  group.ScalarBaseMult(big.NewInt(300)),
			BindingPoint: group.ScalarBaseMult(big.NewInt(400)),
		},
	}

	msg := sha256.Sum256([]byte("test message"))

	                     
	rho1 := ComputeBindingCoefficient(1, msg[:], nonces, group)
	rho2 := ComputeBindingCoefficient(2, msg[:], nonces, group)

	                                                
	if rho1.Cmp(rho2) == 0 {
		t.Error("Different signers should have different binding coefficients")
	}

	                                                      
	rho1Again := ComputeBindingCoefficient(1, msg[:], nonces, group)
	if rho1.Cmp(rho1Again) != 0 {
		t.Error("Same input should produce same binding coefficient")
	}

	t.Logf("rho_1 = %x", rho1.Bytes())
	t.Logf("rho_2 = %x", rho2.Bytes())
}

                                                   
func TestComputeGroupCommitment(t *testing.T) {
	group := curve.NewSecp256k1Group()

	                      
	nonces := []SignerNonce{
		{
			SignerID:     1,
			HidingNonce:  big.NewInt(100),
			BindingNonce: big.NewInt(200),
			HidingPoint:  group.ScalarBaseMult(big.NewInt(100)),
			BindingPoint: group.ScalarBaseMult(big.NewInt(200)),
		},
		{
			SignerID:     2,
			HidingNonce:  big.NewInt(300),
			BindingNonce: big.NewInt(400),
			HidingPoint:  group.ScalarBaseMult(big.NewInt(300)),
			BindingPoint: group.ScalarBaseMult(big.NewInt(400)),
		},
	}

	msg := sha256.Sum256([]byte("test message"))

	R, err := ComputeGroupCommitment(nonces, msg[:], group)
	if err != nil {
		t.Fatalf("ComputeGroupCommitment failed: %v", err)
	}

	                           
	if R.X.Cmp(big.NewInt(0)) == 0 && R.Y.Cmp(big.NewInt(0)) == 0 {
		t.Error("Group commitment should not be zero point")
	}

	t.Logf("R.X = %x", R.X.Bytes())
}

                                             
func TestComputeChallenge(t *testing.T) {
	group := curve.NewSecp256k1Group()

	R := group.ScalarBaseMult(big.NewInt(12345))
	Px := group.ScalarBaseMult(big.NewInt(67890)).X
	msg := sha256.Sum256([]byte("test message"))

	e := ComputeChallenge(R, Px, msg[:], group)

	                                    
	if e.Cmp(group.Order()) >= 0 {
		t.Error("Challenge should be less than group order")
	}

	                  
	e2 := ComputeChallenge(R, Px, msg[:], group)
	if e.Cmp(e2) != 0 {
		t.Error("Same input should produce same challenge")
	}

	t.Logf("e = %x", e.Bytes())
}

                                      
func TestSelectSubset(t *testing.T) {
	signerIDs := []int{1, 2, 3, 4, 5}
	threshold := 3
	seed := []byte("test_seed")

	                          
	subset := SelectSubset(signerIDs, threshold, seed, 0)
	if len(subset) != threshold+1 {
		t.Errorf("Expected %d signers, got %d", threshold+1, len(subset))
	}

	                  
	subset2 := SelectSubset(signerIDs, threshold, seed, 0)
	for i := range subset {
		if subset[i] != subset2[i] {
			t.Error("Same seed should produce same subset")
		}
	}

	                                             
	subset3 := SelectSubset(signerIDs, threshold, seed, 1)
	different := false
	for i := range subset {
		if subset[i] != subset3[i] {
			different = true
			break
		}
	}
	if !different {
		t.Error("Different retry count should produce different subset")
	}

	t.Logf("Subset (retry 0): %v", subset)
	t.Logf("Subset (retry 1): %v", subset3)
}

                                             
func TestAggregateSignatures(t *testing.T) {
	group := curve.NewSecp256k1Group()

	                     
	R := group.ScalarBaseMult(big.NewInt(99999))

	                           
	shares := []SignerShare{
		{SignerID: 1, Share: big.NewInt(111)},
		{SignerID: 2, Share: big.NewInt(222)},
		{SignerID: 3, Share: big.NewInt(333)},
	}

	sig, err := AggregateSignatures(R, shares, group)
	if err != nil {
		t.Fatalf("AggregateSignatures failed: %v", err)
	}

	                     
	if len(sig) != 64 {
		t.Errorf("Expected 64 bytes signature, got %d", len(sig))
	}

	                                       
	RxBytes := make([]byte, 32)
	R.X.FillBytes(RxBytes)
	for i := 0; i < 32; i++ {
		if sig[i] != RxBytes[i] {
			t.Error("First 32 bytes should be R.x")
			break
		}
	}

	t.Logf("Signature: %x", sig)
}

                                                       
func TestComputePartialSignature(t *testing.T) {
	group := curve.NewSecp256k1Group()
	order := group.Order()

	               
	hidingNonce := big.NewInt(100)
	bindingNonce := big.NewInt(200)
	share := big.NewInt(12345)
	rho := big.NewInt(5)
	lambda := big.NewInt(3)
	e := big.NewInt(7)

	z := ComputePartialSignature(1, hidingNonce, bindingNonce, share, rho, lambda, e, group)

	                     
	                               
	expected := new(big.Int).Mul(rho, bindingNonce)
	expected.Add(expected, hidingNonce)
	expected.Mod(expected, order)

	term := new(big.Int).Mul(lambda, e)
	term.Mul(term, share)
	term.Mod(term, order)

	expected.Add(expected, term)
	expected.Mod(expected, order)

	if z.Cmp(expected) != 0 {
		t.Errorf("Partial signature mismatch: expected %s, got %s", expected.String(), z.String())
	}

	t.Logf("z = %s", z.String())
}

                                                           
func TestComputeLagrangeCoefficients(t *testing.T) {
	group := curve.NewSecp256k1Group()

	signerIDs := []int{1, 2, 3}
	lambdas := ComputeLagrangeCoefficientsForSet(signerIDs, group.Order())

	                                    
	for _, id := range signerIDs {
		if lambdas[id] == nil {
			t.Errorf("Missing lambda for signer %d", id)
		}
	}

	                                                  
	                                    
	for id, lambda := range lambdas {
		if lambda.Cmp(big.NewInt(0)) == 0 {
			t.Errorf("Lambda for signer %d should not be zero", id)
		}
		t.Logf("lambda_%d = %s", id, lambda.String())
	}
}
