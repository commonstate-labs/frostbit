package vm

import (
	"math/big"
	"testing"
)

func bi(s string) *big.Int { v, _ := new(big.Int).SetString(s, 10); return v }

                                                                                               
func TestMulDivFloor_Basic(t *testing.T) {
	cases := []struct {
		a, b, c string
		want    string
	}{
		{"10", "3", "2", "15"},          
		{"10", "3", "4", "7"},                                  
		{"0", "5", "4", "0"},
		{"5", "0", "4", "0"},
		{"5", "5", "0", "0"},              
	}
	for _, tc := range cases {
		got := MulDivFloor(bi(tc.a), bi(tc.b), bi(tc.c))
		if got.String() != tc.want {
			t.Fatalf("MulDivFloor(%s,%s,%s) = %s, want %s", tc.a, tc.b, tc.c, got, tc.want)
		}
	}
}

func TestMulDivCeil_RoundsUp(t *testing.T) {
	cases := []struct {
		a, b, c string
		want    string
	}{
		{"10", "3", "4", "8"},                   
		{"10", "3", "2", "15"},
		{"1", "1", "2", "1"},
		{"0", "0", "5", "0"},
	}
	for _, tc := range cases {
		got := MulDivCeil(bi(tc.a), bi(tc.b), bi(tc.c))
		if got.String() != tc.want {
			t.Fatalf("MulDivCeil(%s,%s,%s) = %s, want %s", tc.a, tc.b, tc.c, got, tc.want)
		}
	}
}

                                                                                                     
func TestMulDivResidue_Invariant(t *testing.T) {
	                                       
	triples := [][3]string{
		{"12345", "67890", "7"},
		{"999999999", "333333", "11"},
		{"1", "1", "100"},
		{"50000000000000", "100000000", "1000"},
	}
	for _, tr := range triples {
		a, b, c := bi(tr[0]), bi(tr[1]), bi(tr[2])
		q, r := MulDivResidue(a, b, c)
		lhs := new(big.Int).Mul(q, c)
		lhs.Add(lhs, r)
		rhs := new(big.Int).Mul(a, b)
		if lhs.Cmp(rhs) != 0 {
			t.Fatalf("invariant broken for (%s,%s,%s): q*c+r=%s a*b=%s", tr[0], tr[1], tr[2], lhs, rhs)
		}
		if r.Sign() < 0 || r.Cmp(c) >= 0 {
			t.Fatalf("residue out of range: %s (c=%s)", r, c)
		}
	}
}
