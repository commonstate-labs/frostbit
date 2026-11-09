                          
                                      
package frost

import (
	"errors"
	"fmt"
	"math/big"

	"dex/frost/core/curve"
	"dex/pb"

	"golang.org/x/crypto/sha3"
)

                                                                                                                                                                                                                                                                                                           
var ErrUnsupportedSignAlgo = errors.New("unsupported sign algorithm")

                                                                                                                                                      
var ErrInvalidSignature = errors.New("invalid signature")

                                                                                                                                                         
var ErrInvalidPublicKey = errors.New("invalid public key")

                                     
var ErrInvalidMessage = errors.New("invalid message")

                                                                                                                                                                                                                                 
                                                                                                                                                                                                       
                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 
                                                                                                                                                                                                                                                                                            
func Verify(signAlgo pb.SignAlgo, pubkey, msg, sig []byte) (bool, error) {
	switch signAlgo {
	case pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340:
		return VerifyBIP340(pubkey, msg, sig)
	case pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128:
		return VerifyBN128(pubkey, msg, sig)
	case pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1:
		                                                                                                                                                                                                                                                
		return false, ErrUnsupportedSignAlgo
	default:
		return false, ErrUnsupportedSignAlgo
	}
}

                                                                                                                                                                                                                                                                                                                                                                              
                                                                                                                                                                                                        
                                                                                                                                                            
func VerifyBIP340(pubkey, msg, sig []byte) (bool, error) {
	if len(pubkey) != 32 {
		return false, fmt.Errorf("%w: pubkey length=%d", ErrInvalidPublicKey, len(pubkey))
	}
	if len(msg) != 32 {
		return false, fmt.Errorf("%w: msg length=%d", ErrInvalidMessage, len(msg))
	}
	if len(sig) != 64 {
		return false, fmt.Errorf("%w: sig length=%d", ErrInvalidSignature, len(sig))
	}

	grp := curve.NewSecp256k1Group()
	Xx := new(big.Int).SetBytes(pubkey)

	Xy := recoverYFromX(grp, Xx, false)                 
	if Xy == nil {
		return false, fmt.Errorf("%w: lift pubkey failed", ErrInvalidPublicKey)
	}

	                                                                                                                                 
	Rx := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])

	                                                                                                                                                                                                                                                                                                                                      
	Ry := recoverYFromX(grp, Rx, false)
	if Ry == nil {
		return false, fmt.Errorf("%w: lift R failed", ErrInvalidSignature)
	}

	                                                                                                                                                                                               
	if !SchnorrVerify(grp, Xx, Xy, Rx, Ry, s, msg) {
		return false, nil
	}
	return true, nil
}

                                                                                                                                                                                                              
                                                                                                                                                                                                                       
                                                                                                                                                                                                                                                                                                                                      
func recoverYFromX(grp *curve.Secp256k1Group, x *big.Int, odd bool) *big.Int {
	                                        
	p := grp.Modulus()

	        
	x3 := new(big.Int).Mul(x, x)
	x3.Mod(x3, p)
	x3.Mul(x3, x)
	x3.Mod(x3, p)

	           
	y2 := new(big.Int).Add(x3, big.NewInt(7))
	y2.Mod(y2, p)

	                                                                                                                                                                              
	                                                                                                                                                                                                                                                                                                                                                                            
	exp := new(big.Int).Add(p, big.NewInt(1))
	exp.Rsh(exp, 2)           
	y := new(big.Int).Exp(y2, exp, p)

	                                                                      
	check := new(big.Int).Mul(y, y)
	check.Mod(check, p)
	if check.Cmp(y2) != 0 {
		return nil
	}
	                                                                                                                                                                                                                                                                                             
	if y.Bit(0) == 1 && !odd {
		y.Sub(p, y)
	} else if y.Bit(0) == 0 && odd {
		y.Sub(p, y)
	}

	return y
}

                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      
                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       
func VerifyBN128(pubkey, msg, sig []byte) (bool, error) {
	                                                                                                                                                                                                                              
	if len(pubkey) != 64 {
		return false, ErrInvalidPublicKey
	}

	                                                                                                                                    
	Px := new(big.Int).SetBytes(pubkey[:32])
	Py := new(big.Int).SetBytes(pubkey[32:])

	grp := &curve.BN256Group{}

	                                                                                                                                                                                                                                                                                                                                                                                                                 
	var Rx, s *big.Int
	var RyOptions []*big.Int

	if len(sig) == 64 {
		Rx = new(big.Int).SetBytes(sig[:32])
		s = new(big.Int).SetBytes(sig[32:])
		                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               
		Ry := recoverYFromXBN128(grp, Rx)
		if Ry == nil {
			return false, ErrInvalidSignature
		}
		                                                                                                                                                       
		RyNeg := new(big.Int).Sub(grp.Modulus(), Ry)
		RyOptions = []*big.Int{Ry, RyNeg}
	} else if len(sig) == 96 {
		                                                                                                                                                                        
		Rx = new(big.Int).SetBytes(sig[:32])
		Ry := new(big.Int).SetBytes(sig[32:64])
		s = new(big.Int).SetBytes(sig[64:])
		RyOptions = []*big.Int{Ry}
	} else {
		return false, ErrInvalidSignature
	}

	                                                                                
	                                                                                                                                                                                                                                                                                                                                                                                                                                                                            
	sG := grp.ScalarBaseMult(s)

	for _, Ry := range RyOptions {
		                                                              
		e := bn128Challenge(Rx, Ry, Px, Py, msg, grp)
		eP := grp.ScalarMult(curve.Point{X: Px, Y: Py}, e)
		R := curve.Point{X: Rx, Y: Ry}
		expected := grp.Add(R, eP)
		if sG.X.Cmp(expected.X) == 0 && sG.Y.Cmp(expected.Y) == 0 {
			return true, nil
		}
	}

	return false, nil
}

                                                                                                                                                                                                                                                                                            
                                                       
func bn128Challenge(Rx, Ry, Px, Py *big.Int, msg []byte, grp *curve.BN256Group) *big.Int {
	h := sha3.NewLegacyKeccak256()

	pad32 := func(n *big.Int) []byte {
		out := make([]byte, 32)
		b := n.Bytes()
		copy(out[32-len(b):], b)
		return out
	}

	h.Write(pad32(Rx))
	h.Write(pad32(Ry))
	h.Write(pad32(Px))
	h.Write(pad32(Py))
	h.Write(msg)

	eBytes := h.Sum(nil)
	e := new(big.Int).SetBytes(eBytes)
	e.Mod(e, grp.Order())
	return e
}

                                                                                                                                                                                                                                                                                                                                                                                                          
func recoverYFromXBN128(grp *curve.BN256Group, x *big.Int) *big.Int {
	p := grp.Modulus()

	        
	x3 := new(big.Int).Mul(x, x)
	x3.Mod(x3, p)
	x3.Mul(x3, x)
	x3.Mod(x3, p)

	           
	y2 := new(big.Int).Add(x3, big.NewInt(3))
	y2.Mod(y2, p)

	exp := new(big.Int).Add(p, big.NewInt(1))
	exp.Rsh(exp, 2)           
	y := new(big.Int).Exp(y2, exp, p)

	                                                                      
	check := new(big.Int).Mul(y, y)
	check.Mod(check, p)
	if check.Cmp(y2) != 0 {
		return nil
	}

	return y
}
