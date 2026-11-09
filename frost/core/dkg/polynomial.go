package dkg

import (
	"crypto/rand"
	"io"
	"math/big"

	"dex/frost/core/curve"
)

                                                                
var randReader io.Reader = rand.Reader

                                           
func RandomScalar(n *big.Int) *big.Int {
	scalar, err := rand.Int(randReader, n)
	if err != nil {
		panic(err)
	}
	return scalar
}

                                                              
type Polynomial struct {
	Coefficients []*big.Int                          
}

                                            
func NewPolynomial(t int, grp curve.Group) *Polynomial {
	coeffs := make([]*big.Int, t)
	for i := 0; i < t; i++ {
		coeffs[i] = RandomScalar(grp.Order())
	}
	return &Polynomial{Coefficients: coeffs}
}

                                   
func (p *Polynomial) Evaluate(x *big.Int, grp curve.Group) *big.Int {
	N := grp.Order()
	result := big.NewInt(0)
	temp := big.NewInt(1)
	for i, coef := range p.Coefficients {
		temp.Exp(x, big.NewInt(int64(i)), N)
		temp.Mul(temp, coef)
		temp.Mod(temp, N)
		result.Add(result, temp)
		result.Mod(result, N)
	}
	return result
}
