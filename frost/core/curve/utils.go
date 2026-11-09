package curve

import (
	"crypto/sha256"
	"math/big"

	"github.com/btcsuite/btcd/btcec/v2"
)

                                                                                
                                                               
                                                                                

                                                                         
func PrivBytesToBig(priv *btcec.PrivateKey) *big.Int {
	return new(big.Int).SetBytes(priv.Serialize())                        
}

                                                               
func TapTweakScalar(Px *big.Int, tag string) *big.Int {
	curve := btcec.S256()
	Htag := sha256.Sum256([]byte(tag))

	h := sha256.New()
	h.Write(Htag[:])
	h.Write(Htag[:])
	h.Write(Px.Bytes())

	t := new(big.Int).SetBytes(h.Sum(nil))
	t.Mod(t, curve.Params().N)
	return t
}

                                                                 
func TweakPrivScalar(d *big.Int, tag string) *big.Int {
	curve := btcec.S256()
	n := curve.Params().N

	Px, _ := curve.ScalarBaseMult(d.Bytes())
	t := TapTweakScalar(Px, tag)

	dp := new(big.Int).Add(d, t)
	dp.Mod(dp, n)

	_, Py := curve.ScalarBaseMult(dp.Bytes())
	if Py.Bit(0) == 1 {
		dp.Sub(n, dp)
	}
	return dp
}

                                                                                              
func TweakPubPoint(Px, Py *big.Int, tag string) (*big.Int, *big.Int) {
	curve := btcec.S256()

	if Py.Bit(0) == 1 {                 
		Py = new(big.Int).Sub(curve.Params().P, Py)
	}
	t := TapTweakScalar(Px, tag)
	tx, ty := curve.ScalarBaseMult(t.Bytes())
	return curve.Add(Px, Py, tx, ty)
}

                                                                                
                                                        
                                                                                

                                                                                    
  
                            
func BigIntToFieldVal(x *big.Int) *btcec.FieldVal {
	if x.Sign() < 0 || x.BitLen() > 256 {
		panic("bigIntToFieldVal: out‑of‑range")
	}

	var be32 [32]byte
	b := x.Bytes()                                                 
	copy(be32[32-len(b):], b)                 

	var fv btcec.FieldVal
	fv.SetBytes(&be32)                        
	return &fv
}
