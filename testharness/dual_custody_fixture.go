package testharness

import (
	"math/big"
	"strings"

	"dex/frost/chain/evm"
	"dex/frost/core/curve"
)

                                                                             
                                                              
func harnessGuardianPubkey(name string) []byte {
	if strings.EqualFold(name, "btc") {
		group := curve.NewSecp256k1Group()
		return group.SerializePoint(group.ScalarBaseMult(big.NewInt(31)))[1:]
	}
	group := curve.NewBN256Group()
	key, _ := evm.CanonicalBN128PublicKey(group.SerializePoint(group.ScalarBaseMult(big.NewInt(31))))
	return key
}
