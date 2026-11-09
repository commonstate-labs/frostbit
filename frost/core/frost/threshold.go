package frost

import (
	"errors"
	"math/big"

	"dex/frost/core/curve"
	"dex/frost/core/dkg"
)

                                                 
                                                                                                               
func ThresholdSchnorrPartialSign(grp curve.Group, s_j *big.Int, Qx, Qy *big.Int, m []byte) (k, R_x, R_y *big.Int) {
	N := grp.Order()
	k = dkg.RandomScalar(N)
	R_x, R_y = grp.ScalarBaseMultBytes(k.Bytes()).XY()
	if R_y.Bit(0) == 1 {
		k = new(big.Int).Sub(N, k)
		R_x, R_y = grp.ScalarBaseMultBytes(k.Bytes()).XY()
	}

	return k, R_x, R_y
}

                                                                                       
                                     
func ThresholdSchnorrFinalize(grp curve.Group,
	k_i, lambda_i, e, shareSi *big.Int) *big.Int {

	zi := new(big.Int).Mul(e, shareSi)             
	zi.Mul(zi, lambda_i)                                 
	zi.Add(zi, k_i)                                
	zi.Mod(zi, grp.Order())
	return zi
}

                                                                
func AggregateThresholdSignature(
	grp curve.Group,
	ids, RxParts, RyParts, zParts []*big.Int,
	Qx *big.Int, msg []byte,
) (R_x, R_y, zAgg *big.Int) {

	N := grp.Order()
	t := len(ids)
	if len(RxParts) != t || len(RyParts) != t || len(zParts) != t {
		panic("AggregateThresholdSignature: slice length mismatch")
	}

	                
	R_x, R_y = RxParts[0], RyParts[0]
	for i := 1; i < t; i++ {
		R := curve.Point{X: R_x, Y: R_y}
		RParts := curve.Point{X: RxParts[i], Y: RyParts[i]}
		R_x, R_y = grp.Add(R, RParts).XY()
	}

	                
	zAgg = big.NewInt(0)
	for _, zi := range zParts {
		zAgg.Add(zAgg, zi)
	}
	zAgg.Mod(zAgg, N)

	                                           
	return R_x, R_y, zAgg
}

                                                                                                                
type ChallengeFunc func(Rx, Px *big.Int, msg []byte, grp curve.Group) *big.Int

                                                                   
                                                                  
func ThresholdSign(
	grp curve.Group,
	idsSel, sjSel []*big.Int,
	msg32 []byte,
	Qx *big.Int,
	challenge ChallengeFunc,
) ([]byte, error) {
	const tMin = 3
	if len(idsSel) != len(sjSel) || len(idsSel) < tMin {
		return nil, errors.New("ids / shares length mismatch or too few signers")
	}
	N := grp.Order()
	t := len(idsSel)

	                                                        
	k := make([]*big.Int, t)
	Rx := make([]*big.Int, t)
	Ry := make([]*big.Int, t)
	for i := 0; i < t; i++ {
		ki, Rxi, Ryi := ThresholdSchnorrPartialSign(grp, sjSel[i], nil, nil, msg32)
		k[i], Rx[i], Ry[i] = ki, Rxi, Ryi
	}
	                                     
	RxSum, RySum := Rx[0], Ry[0]
	for i := 1; i < t; i++ {
		sum := curve.Point{X: RxSum, Y: RySum}
		part := curve.Point{X: Rx[i], Y: Ry[i]}
		RxSum, RySum = grp.Add(sum, part).XY()
	}
	if RySum.Bit(0) == 1 {
		for i := range k {
			k[i].Sub(N, k[i])
			Ry[i].Sub(grp.Modulus(), Ry[i])
		}
		RySum.Sub(grp.Modulus(), RySum)
	}

	                                                          
	e := challenge(RxSum, Qx, msg32, grp)

	                                           
	zAgg := big.NewInt(0)
	λ := dkg.ComputeLagrangeCoefficients(idsSel, N)
	for i := 0; i < t; i++ {
		zi := ThresholdSchnorrFinalize(grp, k[i], λ[i], e, sjSel[i])
		zAgg.Add(zAgg, zi)
	}
	zAgg.Mod(zAgg, N)

	                                      
	sig := make([]byte, 64)
	RxSum.FillBytes(sig[:32])
	zAgg.FillBytes(sig[32:])
	return sig, nil
}
