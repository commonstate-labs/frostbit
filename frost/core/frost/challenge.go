package frost

import (
	"crypto/sha256"
	"math/big"

	"dex/frost/core/curve"

	"golang.org/x/crypto/sha3"
)

                                                                      
func BIP340Challenge(Rx, Px *big.Int, msg []byte, grp curve.Group) *big.Int {
	                                                  
	tagHash := sha256.Sum256([]byte("BIP0340/challenge"))

	                                                                     
	h := sha256.New()
	h.Write(tagHash[:])
	h.Write(tagHash[:])

	                                                       
	pad32 := func(b *big.Int) []byte {
		out := make([]byte, 32)
		bb := b.Bytes()
		copy(out[32-len(bb):], bb)
		return out
	}
	h.Write(pad32(Rx))
	h.Write(pad32(Px))
	h.Write(msg)                                              

	                                                
	e := new(big.Int).SetBytes(h.Sum(nil))
	e.Mod(e, grp.Order())
	return e
}

                                                                                           
                                                                                  
                                                                                        
func ETHKeccakChallenge(Rx, Px *big.Int, msg []byte, grp curve.Group) *big.Int {
	h := sha3.NewLegacyKeccak256()

	                                                                   
	pad32 := func(n *big.Int) []byte {
		out := make([]byte, 32)
		b := n.Bytes()
		copy(out[32-len(b):], b)
		return out
	}

	h.Write(pad32(Rx))       
	h.Write(pad32(Px))       
	h.Write(msg)                                                                

	eBytes := h.Sum(nil)
	e := new(big.Int).SetBytes(eBytes)
	e.Mod(e, grp.Order())
	return e
}
