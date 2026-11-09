                            
                                                          
                                                                            

package roast

import (
	"crypto/sha256"
	"errors"
	"math/big"
	"sort"

	"dex/frost/core/curve"
	"dex/frost/core/dkg"
)

                                     

var (
	                                         
	ErrInsufficientSigners = errors.New("insufficient signers")
	                                        
	ErrInvalidShare = errors.New("invalid signature share")
	                                    
	ErrAggregationFailed = errors.New("signature aggregation failed")
)

                                     

                                        
type SignerNonce struct {
	SignerID     int                        
	HidingNonce  *big.Int                                
	BindingNonce *big.Int                                  
	HidingPoint  curve.Point                 
	BindingPoint curve.Point                   
}

                                       
type SignerShare struct {
	SignerID int                     
	Share    *big.Int                                     
}

                           
type ROASTParams struct {
	Threshold  int                    
	NumSigners int                             
	Message    []byte                                             
	GroupPubX  *big.Int                         
	GroupPubY  *big.Int                         
	Group      curve.Group                   
}

                                     

                                                    
                                                                   
func ComputeBindingCoefficient(
	signerID int,
	msg []byte,
	nonces []SignerNonce,
	group curve.Group,
) *big.Int {
	                       
	sortedNonces := make([]SignerNonce, len(nonces))
	copy(sortedNonces, nonces)
	sort.Slice(sortedNonces, func(i, j int) bool {
		return sortedNonces[i].SignerID < sortedNonces[j].SignerID
	})

	                     
	h := sha256.New()
	h.Write([]byte("rho"))
	h.Write(big.NewInt(int64(signerID)).Bytes())
	h.Write(msg)

	for _, n := range sortedNonces {
		h.Write(n.HidingPoint.X.Bytes())
		h.Write(n.HidingPoint.Y.Bytes())
		h.Write(n.BindingPoint.X.Bytes())
		h.Write(n.BindingPoint.Y.Bytes())
	}

	hash := h.Sum(nil)
	rho := new(big.Int).SetBytes(hash)
	return rho.Mod(rho, group.Order())
}

                                                                   
func ComputeGroupCommitment(
	nonces []SignerNonce,
	msg []byte,
	group curve.Group,
) (curve.Point, error) {
	if len(nonces) == 0 {
		return curve.Point{}, ErrInsufficientSigners
	}

	                     
	resultX := big.NewInt(0)
	resultY := big.NewInt(0)
	first := true

	for _, n := range nonces {
		              
		rho := ComputeBindingCoefficient(n.SignerID, msg, nonces, group)

		                     
		rhoRPrime := group.ScalarMultBytes(n.BindingPoint, rho.Bytes())

		                           
		combined := group.Add(n.HidingPoint, rhoRPrime)

		         
		if first {
			resultX = combined.X
			resultY = combined.Y
			first = false
		} else {
			result := group.Add(curve.Point{X: resultX, Y: resultY}, combined)
			resultX = result.X
			resultY = result.Y
		}
	}

	return curve.Point{X: resultX, Y: resultY}, nil
}

                                                                                   
func ComputeChallenge(R curve.Point, Px *big.Int, msg []byte, group curve.Group) *big.Int {
	                      
	tagSum := sha256.Sum256([]byte("BIP0340/challenge"))
	h := sha256.New()
	h.Write(tagSum[:])
	h.Write(tagSum[:])

	                 
	rxBytes := make([]byte, 32)
	R.X.FillBytes(rxBytes)
	h.Write(rxBytes)

	                 
	pxBytes := make([]byte, 32)
	Px.FillBytes(pxBytes)
	h.Write(pxBytes)

	                 
	h.Write(msg)

	hash := h.Sum(nil)
	e := new(big.Int).SetBytes(hash)
	return e.Mod(e, group.Order())
}

                                                                                      
func ComputePartialSignature(
	signerID int,
	hidingNonce, bindingNonce *big.Int,
	share *big.Int,                            
	rho *big.Int,                
	lambda *big.Int,                      
	e *big.Int,             
	group curve.Group,
) *big.Int {
	return computePartialSig(hidingNonce, bindingNonce, share, rho, lambda, e, group, false, false)
}

                                                                     
                                                                                                                     
func ComputePartialSignatureBIP340(
	signerID int,
	hidingNonce, bindingNonce *big.Int,
	share *big.Int,
	rho, lambda, e *big.Int,
	group curve.Group,
	negateNonce bool,                       
	negateShare bool,                       
) *big.Int {
	return computePartialSig(hidingNonce, bindingNonce, share, rho, lambda, e, group, negateNonce, negateShare)
}

func computePartialSig(
	hidingNonce, bindingNonce, share, rho, lambda, e *big.Int,
	group curve.Group, negateNonce, negateShare bool,
) *big.Int {
	n := group.Order()

	hk := new(big.Int).Set(hidingNonce)
	bk := new(big.Int).Set(bindingNonce)
	if negateNonce {
		hk.Sub(n, hk)
		bk.Sub(n, bk)
	}

	sk := new(big.Int).Set(share)
	if negateShare {
		sk.Sub(n, sk)
	}

	                    
	z := new(big.Int).Mul(rho, bk)
	z.Add(z, hk)
	z.Mod(z, n)

	                 
	term := new(big.Int).Mul(lambda, e)
	term.Mul(term, sk)
	term.Mod(term, n)

	z.Add(z, term)
	z.Mod(z, n)
	return z
}

                                         
                                
func AggregateSignatures(
	R curve.Point,
	shares []SignerShare,
	group curve.Group,
) ([]byte, error) {
	if len(shares) == 0 {
		return nil, ErrInsufficientSigners
	}

	             
	z := big.NewInt(0)
	for _, s := range shares {
		z.Add(z, s.Share)
	}
	z.Mod(z, group.Order())

	                                           
	if R.Y.Bit(0) == 1 {
		                                        
		z.Sub(group.Order(), z)
	}

	                                                  
	sig := make([]byte, 64)
	R.X.FillBytes(sig[:32])
	z.FillBytes(sig[32:])

	return sig, nil
}

                                                                   
                                                    
                                                                               
                                                         
func AggregateSignaturesBIP340(
	R curve.Point,
	shares []SignerShare,
	group curve.Group,
) ([]byte, error) {
	if len(shares) == 0 {
		return nil, ErrInsufficientSigners
	}

	z := big.NewInt(0)
	for _, s := range shares {
		z.Add(z, s.Share)
	}
	z.Mod(z, group.Order())

	sig := make([]byte, 64)
	R.X.FillBytes(sig[:32])
	z.FillBytes(sig[32:])

	return sig, nil
}

                                           

                                     
                                               
func SelectSubset(signerIDs []int, threshold int, seed []byte, retryCount int) []int {
	if len(signerIDs) <= threshold+1 {
		return signerIDs
	}

	                  
	permuted := make([]int, len(signerIDs))
	copy(permuted, signerIDs)

	                                                         
	for i := len(permuted) - 1; i > 0; i-- {
		seedWithRetry := append(seed, byte(retryCount), byte(i))
		hash := sha256.Sum256(seedWithRetry)
		                                                          
		hashInt := new(big.Int).SetBytes(hash[:])
		modulus := big.NewInt(int64(i + 1))
		jBig := new(big.Int).Mod(hashInt, modulus)
		j := int(jBig.Int64())
		permuted[i], permuted[j] = permuted[j], permuted[i]
	}

	                    
	return permuted[:threshold+1]
}

                                                     
                                                
func ComputeAggregatorIndex(
	signerIDs []int,
	seed []byte,
	startHeight, currentHeight, rotateBlocks uint64,
) int {
	if len(signerIDs) == 0 {
		return 0
	}

	                     
	rotations := (currentHeight - startHeight) / rotateBlocks

	                  
	permuted := make([]int, len(signerIDs))
	copy(permuted, signerIDs)

	for i := len(permuted) - 1; i > 0; i-- {
		seedWithIdx := append(seed, byte(i))
		hash := sha256.Sum256(seedWithIdx)
		j := int(new(big.Int).SetBytes(hash[:]).Uint64()) % (i + 1)
		permuted[i], permuted[j] = permuted[j], permuted[i]
	}

	                  
	return permuted[int(rotations)%len(permuted)]
}

                                            
                                                
func VerifyPartialSignature(
	signerID int,
	share *big.Int,
	nonce SignerNonce,
	rho, lambda, e *big.Int,
	publicKeyShare curve.Point,                                
	group curve.Group,
) bool {
	          
	zG := group.ScalarBaseMultBytes(share.Bytes())

	                    
	rhoRPrime := group.ScalarMultBytes(nonce.BindingPoint, rho.Bytes())
	RTotal := group.Add(nonce.HidingPoint, rhoRPrime)

	                 
	lambdaE := new(big.Int).Mul(lambda, e)
	lambdaE.Mod(lambdaE, group.Order())
	lambdaEP := group.ScalarMultBytes(publicKeyShare, lambdaE.Bytes())

	                                     
	expected := group.Add(RTotal, lambdaEP)

	         
	return zG.X.Cmp(expected.X) == 0 && zG.Y.Cmp(expected.Y) == 0
}

                                                                                        
func ComputeLagrangeCoefficientsForSet(signerIDs []int, order *big.Int) map[int]*big.Int {
	ids := make([]*big.Int, len(signerIDs))
	for i, id := range signerIDs {
		ids[i] = big.NewInt(int64(id))
	}

	lambdas := dkg.ComputeLagrangeCoefficients(ids, order)

	result := make(map[int]*big.Int)
	for i, id := range signerIDs {
		result[id] = lambdas[i]
	}
	return result
}
