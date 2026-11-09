                                   
                                                                                                                          
package adapters

import (
	"crypto/sha256"
	"errors"
	"math/big"

	"dex/frost/core/curve"
	"dex/frost/core/dkg"
	"dex/frost/core/roast"
	"dex/frost/runtime"
	"dex/pb"

	"golang.org/x/crypto/sha3"
)

                                                                

var (
	ErrUnsupportedSignAlgo = errors.New("unsupported sign algorithm")
)

                                                          

type Secp256k1ROASTExecutor struct {
	group curve.Group
}

func NewSecp256k1ROASTExecutor() *Secp256k1ROASTExecutor {
	return &Secp256k1ROASTExecutor{
		group: curve.NewSecp256k1Group(),
	}
}

                                                                    
func (e *Secp256k1ROASTExecutor) toRoastNonces(nonces []runtime.NonceInput) []roast.SignerNonce {
	result := make([]roast.SignerNonce, len(nonces))
	for i, n := range nonces {
		result[i] = roast.SignerNonce{
			SignerID:     n.SignerID,
			HidingNonce:  n.HidingNonce,
			BindingNonce: n.BindingNonce,
			HidingPoint:  curve.Point{X: n.HidingPoint.X, Y: n.HidingPoint.Y},
			BindingPoint: curve.Point{X: n.BindingPoint.X, Y: n.BindingPoint.Y},
		}
	}
	return result
}

func (e *Secp256k1ROASTExecutor) ComputeGroupCommitment(nonces []runtime.NonceInput, msg []byte) (runtime.CurvePoint, error) {
	roastNonces := e.toRoastNonces(nonces)
	R, err := roast.ComputeGroupCommitment(roastNonces, msg, e.group)
	if err != nil {
		return runtime.CurvePoint{}, err
	}
	return runtime.CurvePoint{X: R.X, Y: R.Y}, nil
}

                                                                                       
func (e *Secp256k1ROASTExecutor) ComputeBindingCoefficient(signerID int, msg []byte, nonces []runtime.NonceInput) *big.Int {
	roastNonces := e.toRoastNonces(nonces)
	return roast.ComputeBindingCoefficient(signerID, msg, roastNonces, e.group)
}

func (e *Secp256k1ROASTExecutor) ComputeChallenge(R runtime.CurvePoint, groupPubX *big.Int, msg []byte) *big.Int {
	return roast.ComputeChallenge(curve.Point{X: R.X, Y: R.Y}, groupPubX, msg, e.group)
}

                                                                                                                
func (e *Secp256k1ROASTExecutor) ComputeLagrangeCoefficients(signerIDs []int) map[int]*big.Int {
	return roast.ComputeLagrangeCoefficientsForSet(signerIDs, e.group.Order())
}

                                                                                     
func (e *Secp256k1ROASTExecutor) ComputePartialSignature(params runtime.PartialSignParams) *big.Int {
	return roast.ComputePartialSignature(
		params.SignerID,
		params.HidingNonce,
		params.BindingNonce,
		params.SecretShare,
		params.Rho,
		params.Lambda,
		params.Challenge,
		e.group,
	)
}

func (e *Secp256k1ROASTExecutor) AggregateSignatures(R runtime.CurvePoint, shares []runtime.ShareInput) ([]byte, error) {
	roastShares := make([]roast.SignerShare, len(shares))
	for i, s := range shares {
		roastShares[i] = roast.SignerShare{
			SignerID: s.SignerID,
			Share:    s.Share,
		}
	}
	return roast.AggregateSignaturesBIP340(curve.Point{X: R.X, Y: R.Y}, roastShares, e.group)
}

func (e *Secp256k1ROASTExecutor) GenerateNoncePair() (hiding, binding *big.Int, hidingPoint, bindingPoint runtime.CurvePoint, err error) {
	hiding = dkg.RandomScalar(e.group.Order())
	binding = dkg.RandomScalar(e.group.Order())

	hp := e.group.ScalarBaseMult(hiding)
	bp := e.group.ScalarBaseMult(binding)

	hidingPoint = runtime.CurvePoint{X: hp.X, Y: hp.Y}
	bindingPoint = runtime.CurvePoint{X: bp.X, Y: bp.Y}
	return
}

                                                          
func (e *Secp256k1ROASTExecutor) ScalarBaseMult(k *big.Int) runtime.CurvePoint {
	p := e.group.ScalarBaseMult(k)
	return runtime.CurvePoint{X: p.X, Y: p.Y}
}

                                                          
func (e *Secp256k1ROASTExecutor) SerializePoint(P runtime.CurvePoint) []byte {
	return e.group.SerializePoint(curve.Point{X: P.X, Y: P.Y})
}

type BN256ROASTExecutor struct {
	group curve.Group
}

func NewBN256ROASTExecutor() *BN256ROASTExecutor {
	return &BN256ROASTExecutor{
		group: curve.NewBN256Group(),
	}
}

func (e *BN256ROASTExecutor) toRoastNonces(nonces []runtime.NonceInput) []roast.SignerNonce {
	result := make([]roast.SignerNonce, len(nonces))
	for i, n := range nonces {
		result[i] = roast.SignerNonce{
			SignerID:     n.SignerID,
			HidingNonce:  n.HidingNonce,
			BindingNonce: n.BindingNonce,
			HidingPoint:  curve.Point{X: n.HidingPoint.X, Y: n.HidingPoint.Y},
			BindingPoint: curve.Point{X: n.BindingPoint.X, Y: n.BindingPoint.Y},
		}
	}
	return result
}

func (e *BN256ROASTExecutor) ComputeGroupCommitment(nonces []runtime.NonceInput, msg []byte) (runtime.CurvePoint, error) {
	roastNonces := e.toRoastNonces(nonces)
	R, err := roast.ComputeGroupCommitment(roastNonces, msg, e.group)
	if err != nil {
		return runtime.CurvePoint{}, err
	}
	return runtime.CurvePoint{X: R.X, Y: R.Y}, nil
}

func (e *BN256ROASTExecutor) ComputeBindingCoefficient(signerID int, msg []byte, nonces []runtime.NonceInput) *big.Int {
	roastNonces := e.toRoastNonces(nonces)
	return roast.ComputeBindingCoefficient(signerID, msg, roastNonces, e.group)
}

func (e *BN256ROASTExecutor) ComputeChallenge(R runtime.CurvePoint, groupPubX *big.Int, msg []byte) *big.Int {
	                                                                                                                                     
	grp := e.group.(*curve.BN256Group)
	groupPubY := recoverCanonicalYBN128(grp, groupPubX)

	h := sha3.NewLegacyKeccak256()
	pad32 := func(n *big.Int) []byte {
		out := make([]byte, 32)
		b := n.Bytes()
		copy(out[32-len(b):], b)
		return out
	}
	h.Write(pad32(R.X))
	h.Write(pad32(R.Y))
	h.Write(pad32(groupPubX))
	h.Write(pad32(groupPubY))
	h.Write(msg)
	eBytes := h.Sum(nil)
	res := new(big.Int).SetBytes(eBytes)
	return res.Mod(res, e.group.Order())
}

func (e *BN256ROASTExecutor) ComputeLagrangeCoefficients(signerIDs []int) map[int]*big.Int {
	return roast.ComputeLagrangeCoefficientsForSet(signerIDs, e.group.Order())
}

func (e *BN256ROASTExecutor) ComputePartialSignature(params runtime.PartialSignParams) *big.Int {
	return roast.ComputePartialSignature(
		params.SignerID,
		params.HidingNonce,
		params.BindingNonce,
		params.SecretShare,
		params.Rho,
		params.Lambda,
		params.Challenge,
		e.group,
	)
}

func (e *BN256ROASTExecutor) AggregateSignatures(R runtime.CurvePoint, shares []runtime.ShareInput) ([]byte, error) {
	if len(shares) == 0 {
		return nil, errors.New("insufficient shares")
	}
	                    
	z := big.NewInt(0)
	for _, s := range shares {
		z.Add(z, s.Share)
	}
	z.Mod(z, e.group.Order())

	if R.X == nil || R.Y == nil {
		return nil, errors.New("invalid group commitment")
	}

	                                                                                   
	sig := make([]byte, 96)
	RxBytes := R.X.Bytes()
	copy(sig[32-len(RxBytes):32], RxBytes)
	RyBytes := R.Y.Bytes()
	copy(sig[64-len(RyBytes):64], RyBytes)
	zBytes := z.Bytes()
	copy(sig[96-len(zBytes):], zBytes)
	return sig, nil
}

func (e *BN256ROASTExecutor) GenerateNoncePair() (hiding, binding *big.Int, hidingPoint, bindingPoint runtime.CurvePoint, err error) {
	hiding = dkg.RandomScalar(e.group.Order())
	binding = dkg.RandomScalar(e.group.Order())
	hp := e.group.ScalarBaseMult(hiding)
	bp := e.group.ScalarBaseMult(binding)
	hidingPoint = runtime.CurvePoint{X: hp.X, Y: hp.Y}
	bindingPoint = runtime.CurvePoint{X: bp.X, Y: bp.Y}
	return
}

func (e *BN256ROASTExecutor) ScalarBaseMult(k *big.Int) runtime.CurvePoint {
	p := e.group.ScalarBaseMult(k)
	return runtime.CurvePoint{X: p.X, Y: p.Y}
}

func (e *BN256ROASTExecutor) SerializePoint(P runtime.CurvePoint) []byte {
	return e.group.SerializePoint(curve.Point{X: P.X, Y: P.Y})
}

                                                        

type Secp256k1DKGExecutor struct {
	group curve.Group
}

func NewSecp256k1DKGExecutor() *Secp256k1DKGExecutor {
	return &Secp256k1DKGExecutor{
		group: curve.NewSecp256k1Group(),
	}
}

                                                                            
type polynomialAdapter struct {
	poly  *dkg.Polynomial
	group curve.Group
}

func (p *polynomialAdapter) Evaluate(x int) *big.Int {
	return p.poly.Evaluate(big.NewInt(int64(x)), p.group)
}

func (p *polynomialAdapter) Coefficients() []*big.Int {
	result := make([]*big.Int, len(p.poly.Coefficients))
	copy(result, p.poly.Coefficients)
	return result
}

func (e *Secp256k1DKGExecutor) GeneratePolynomial(threshold int) (runtime.PolynomialHandle, error) {
	poly := dkg.NewPolynomial(threshold, e.group)
	return &polynomialAdapter{poly: poly, group: e.group}, nil
}

func (e *Secp256k1DKGExecutor) ComputeCommitments(poly runtime.PolynomialHandle) [][]byte {
	coeffs := poly.Coefficients()
	result := make([][]byte, len(coeffs))
	for i, coef := range coeffs {
		point := e.group.ScalarBaseMult(coef)
		result[i] = e.group.SerializePoint(point)
	}
	return result
}

                                                                                                                    
func (e *Secp256k1DKGExecutor) EvaluateShare(poly runtime.PolynomialHandle, receiverIndex int) []byte {
	share := poly.Evaluate(receiverIndex)
	result := make([]byte, 32)
	share.FillBytes(result)
	return result
}

                                            
func (e *Secp256k1DKGExecutor) AggregateShares(shares [][]byte) []byte {
	sum := big.NewInt(0)
	order := e.group.Order()
	for _, s := range shares {
		val := new(big.Int).SetBytes(s)
		sum.Add(sum, val)
		sum.Mod(sum, order)
	}
	result := make([]byte, 32)
	sum.FillBytes(result)
	return result
}

func (e *Secp256k1DKGExecutor) ComputeGroupPubkey(ai0List [][]byte) []byte {
	if len(ai0List) == 0 {
		return nil
	}

	                                                             
	result := e.group.DecompressPoint(ai0List[0])

	for i := 1; i < len(ai0List); i++ {
		p := e.group.DecompressPoint(ai0List[i])
		result = e.group.Add(result, p)
	}

	return e.group.SerializePoint(result)
}

func (e *Secp256k1DKGExecutor) VerifyShare(share []byte, commitments [][]byte, senderIndex, receiverIndex int) bool {
	if len(share) == 0 || len(commitments) == 0 {
		return false
	}

	                             
	shareVal := new(big.Int).SetBytes(share)
	expectedPoint := e.group.ScalarBaseMult(shareVal)

	                                                 
	x := big.NewInt(int64(receiverIndex))
	var computedPoint curve.Point
	first := true

	for k, commitBytes := range commitments {
		Ak := e.group.DecompressPoint(commitBytes)
		      
		xPowK := new(big.Int).Exp(x, big.NewInt(int64(k)), e.group.Order())
		            
		term := e.group.ScalarMult(Ak, xPowK)

		if first {
			computedPoint = term
			first = false
		} else {
			computedPoint = e.group.Add(computedPoint, term)
		}
	}

	return expectedPoint.X.Cmp(computedPoint.X) == 0 && expectedPoint.Y.Cmp(computedPoint.Y) == 0
}

                                                                                                          
func (e *Secp256k1DKGExecutor) SchnorrSign(privateKey *big.Int, msgHash []byte) ([]byte, error) {
	                                                
	k := dkg.RandomScalar(e.group.Order())

	          
	R := e.group.ScalarBaseMult(k)

	                   
	P := e.group.ScalarBaseMult(privateKey)

	                     
	h := sha256.New()
	h.Write(e.group.SerializePoint(R))
	h.Write(e.group.SerializePoint(P))
	h.Write(msgHash)
	eBytes := h.Sum(nil)
	eVal := new(big.Int).SetBytes(eBytes)
	eVal.Mod(eVal, e.group.Order())

	                         
	s := new(big.Int).Mul(eVal, privateKey)
	s.Add(s, k)
	s.Mod(s, e.group.Order())

	                                
	sig := make([]byte, 64)
	R.X.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return sig, nil
}

                                                          
func (e *Secp256k1DKGExecutor) ScalarBaseMult(k *big.Int) runtime.CurvePoint {
	p := e.group.ScalarBaseMult(k)
	return runtime.CurvePoint{X: p.X, Y: p.Y}
}

func (e *Secp256k1DKGExecutor) SerializePoint(P runtime.CurvePoint) []byte {
	return e.group.SerializePoint(curve.Point{X: P.X, Y: P.Y})
}

type BN256DKGExecutor struct {
	group curve.Group
}

func NewBN256DKGExecutor() *BN256DKGExecutor {
	return &BN256DKGExecutor{
		group: curve.NewBN256Group(),
	}
}

func (e *BN256DKGExecutor) GeneratePolynomial(threshold int) (runtime.PolynomialHandle, error) {
	poly := dkg.NewPolynomial(threshold, e.group)
	return &polynomialAdapter{poly: poly, group: e.group}, nil
}

func (e *BN256DKGExecutor) ComputeCommitments(poly runtime.PolynomialHandle) [][]byte {
	coeffs := poly.Coefficients()
	result := make([][]byte, len(coeffs))
	for i, coef := range coeffs {
		point := e.group.ScalarBaseMult(coef)
		result[i] = e.group.SerializePoint(point)
	}
	return result
}

func (e *BN256DKGExecutor) EvaluateShare(poly runtime.PolynomialHandle, receiverIndex int) []byte {
	share := poly.Evaluate(receiverIndex)
	result := make([]byte, 32)
	share.FillBytes(result)
	return result
}

func (e *BN256DKGExecutor) AggregateShares(shares [][]byte) []byte {
	sum := big.NewInt(0)
	order := e.group.Order()
	for _, s := range shares {
		val := new(big.Int).SetBytes(s)
		sum.Add(sum, val)
		sum.Mod(sum, order)
	}
	result := make([]byte, 32)
	sum.FillBytes(result)
	return result
}

func (e *BN256DKGExecutor) ComputeGroupPubkey(ai0List [][]byte) []byte {
	if len(ai0List) == 0 {
		return nil
	}
	result := e.group.DecompressPoint(ai0List[0])
	for i := 1; i < len(ai0List); i++ {
		p := e.group.DecompressPoint(ai0List[i])
		result = e.group.Add(result, p)
	}
	return e.group.SerializePoint(result)
}

func (e *BN256DKGExecutor) VerifyShare(share []byte, commitments [][]byte, senderIndex, receiverIndex int) bool {
	if len(share) == 0 || len(commitments) == 0 {
		return false
	}
	shareVal := new(big.Int).SetBytes(share)
	expectedPoint := e.group.ScalarBaseMult(shareVal)
	x := big.NewInt(int64(receiverIndex))
	var computedPoint curve.Point
	first := true
	for k, commitBytes := range commitments {
		Ak := e.group.DecompressPoint(commitBytes)
		xPowK := new(big.Int).Exp(x, big.NewInt(int64(k)), e.group.Order())
		term := e.group.ScalarMult(Ak, xPowK)
		if first {
			computedPoint = term
			first = false
		} else {
			computedPoint = e.group.Add(computedPoint, term)
		}
	}
	return expectedPoint.X.Cmp(computedPoint.X) == 0 && expectedPoint.Y.Cmp(computedPoint.Y) == 0
}

func (e *BN256DKGExecutor) SchnorrSign(privateKey *big.Int, msgHash []byte) ([]byte, error) {
	k := dkg.RandomScalar(e.group.Order())
	R := e.group.ScalarBaseMult(k)
	P := e.group.ScalarBaseMult(privateKey)

	                                                           
	h := sha3.NewLegacyKeccak256()
	pad32 := func(n *big.Int) []byte {
		out := make([]byte, 32)
		b := n.Bytes()
		copy(out[32-len(b):], b)
		return out
	}
	h.Write(pad32(R.X))
	h.Write(pad32(R.Y))
	h.Write(pad32(P.X))
	h.Write(pad32(P.Y))
	h.Write(msgHash)
	eVal := new(big.Int).SetBytes(h.Sum(nil))
	eVal.Mod(eVal, e.group.Order())

	s := new(big.Int).Mul(eVal, privateKey)
	s.Add(s, k)
	s.Mod(s, e.group.Order())

	                                                                   
	sig := make([]byte, 64)
	R.X.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return sig, nil
}

func (e *BN256DKGExecutor) ScalarBaseMult(k *big.Int) runtime.CurvePoint {
	p := e.group.ScalarBaseMult(k)
	return runtime.CurvePoint{X: p.X, Y: p.Y}
}

func (e *BN256DKGExecutor) SerializePoint(P runtime.CurvePoint) []byte {
	return e.group.SerializePoint(curve.Point{X: P.X, Y: P.Y})
}

func recoverCanonicalYBN128(grp *curve.BN256Group, x *big.Int) *big.Int {
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
	halfP := new(big.Int).Rsh(p, 1)
	if y.Cmp(halfP) > 0 {
		y.Sub(p, y)
	}
	return y
}

                                                                

                                                                                                                                          
type DefaultCryptoExecutorFactory struct{}

                                                                                                
func NewDefaultCryptoExecutorFactory() *DefaultCryptoExecutorFactory {
	return &DefaultCryptoExecutorFactory{}
}

func (f *DefaultCryptoExecutorFactory) NewROASTExecutor(signAlgo int32) (runtime.ROASTExecutor, error) {
	switch pb.SignAlgo(signAlgo) {
	case pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340:
		return NewSecp256k1ROASTExecutor(), nil
	case pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128:
		return NewBN256ROASTExecutor(), nil
	default:
		return nil, ErrUnsupportedSignAlgo
	}
}

func (f *DefaultCryptoExecutorFactory) NewDKGExecutor(signAlgo int32) (runtime.DKGExecutor, error) {
	switch pb.SignAlgo(signAlgo) {
	case pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340:
		return NewSecp256k1DKGExecutor(), nil
	case pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128:
		return NewBN256DKGExecutor(), nil
	default:
		return nil, ErrUnsupportedSignAlgo
	}
}
