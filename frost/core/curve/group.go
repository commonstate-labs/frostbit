package curve

import "math/big"

type Point struct{ X, Y *big.Int }

                      
func (p Point) XY() (*big.Int, *big.Int) { return p.X, p.Y }

type Group interface {
	                                      
	Order() *big.Int

	                                               
	Modulus() *big.Int

	                                                     
	BitSize() int

	            
	ScalarBaseMult(k *big.Int) Point

	         
	ScalarMult(P Point, k *big.Int) Point

	         
	Add(P, Q Point) Point
	ScalarBaseMultBytes(k []byte) Point
	ScalarMultBytes(P Point, k []byte) Point

	               
	SerializePoint(P Point) []byte

	            
	DecompressPoint(data []byte) Point
}
