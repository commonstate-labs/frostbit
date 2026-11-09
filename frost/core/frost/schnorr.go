package frost

import (
	"crypto/sha256"
	"math/big"

	"dex/frost/core/curve"

	"github.com/btcsuite/btcd/btcec/v2"
)

                                                                                        
var rfc6979ExtraDataV0 = [32]byte{
	0xa3, 0xeb, 0x4c, 0x18, 0x2f, 0xae, 0x7e, 0xf4,
	0xe8, 0x10, 0xc6, 0xee, 0x13, 0xb0, 0xe9, 0x26,
	0x68, 0x6d, 0x71, 0xe8, 0x7f, 0x39, 0x4f, 0x79,
	0x9c, 0x00, 0xa5, 0x21, 0x03, 0xcb, 0x4e, 0x17,
}                        

                                                                         
                                                             
                               
func SchnorrSign(grp curve.Group, x *big.Int, m []byte) (Rx, Ry, z, k *big.Int) {
	if len(m) != 32 {
		panic("BIP-340: message must be a 32-byte hash")
	}
	n := grp.Order()

	                                               
	d := new(big.Int).Set(x)
	Px, Py := grp.ScalarBaseMult(d).XY()
	if Py.Bit(0) == 1 {                               
		d.Sub(n, d)
	}

	                                         
	priv32 := make([]byte, 32)
	copy(priv32[32-len(d.Bytes()):], d.Bytes())

	var kScalar *btcec.ModNScalar
	for iter := uint32(0); ; iter++ {
		kScalar = btcec.NonceRFC6979(priv32, m, rfc6979ExtraDataV0[:], nil, iter)

		if !kScalar.IsZero() {
			break
		}
	}

	                                     
	var kArr [32]byte
	kScalar.PutBytes(&kArr)
	kInt := new(big.Int).SetBytes(kArr[:])

	                                                           
	Rx, Ry = grp.ScalarBaseMultBytes(kArr[:]).XY()
	if Ry.Bit(0) == 1 {
		kInt.Sub(n, kInt)
		kBytes := kInt.FillBytes(make([]byte, 32))
		Rx, Ry = grp.ScalarBaseMultBytes(kBytes).XY()
	}

	                                                                                             
	eBytes := taggedHashBIP340Challenge(Rx, Px, m)
	e := new(big.Int).SetBytes(eBytes)
	e.Mod(e, n)

	                                                
	z = new(big.Int).Mul(e, d)
	z.Add(z, kInt)
	z.Mod(z, n)

	return Rx, Ry, z, kInt
}

                                                                                           
func taggedHash(tag string, data ...[]byte) []byte {
	tagSum := sha256.Sum256([]byte(tag))
	h := sha256.New()
	h.Write(tagSum[:])
	h.Write(tagSum[:])
	for _, b := range data {
		h.Write(b)
	}
	return h.Sum(nil)
}

func taggedHashBIP340Challenge(rx, px *big.Int, msg []byte) []byte {
	rx32 := make([]byte, 32)
	px32 := make([]byte, 32)
	rx.FillBytes(rx32)
	px.FillBytes(px32)
	return taggedHash("BIP0340/challenge", rx32, px32, msg)
}

                                                            
func SchnorrVerify(grp curve.Group, Xx, Xy, Rx, Ry, z *big.Int, m []byte) bool {
	n := grp.Order()
	if len(m) != 32 || Ry.Bit(0) == 1 {                      
		return false
	}

	                                                      
	                                                                        
	if Xy.Bit(0) == 1 {
		Xy = new(big.Int).Sub(grp.Modulus(), Xy)
	}
	eBytes := taggedHashBIP340Challenge(Rx, Xx, m)
	e := new(big.Int).SetBytes(eBytes)
	e.Mod(e, n)

	                              
	Gz_x, Gz_y := grp.ScalarBaseMultBytes(z.Bytes()).XY()
	X := curve.Point{X: Xx, Y: Xy}
	eX := grp.ScalarMultBytes(X, e.Bytes())
	R := curve.Point{X: Rx, Y: Ry}
	sum_x, sum_y := grp.Add(R, eX).XY()

	return Gz_x.Cmp(sum_x) == 0 && Gz_y.Cmp(sum_y) == 0
}
