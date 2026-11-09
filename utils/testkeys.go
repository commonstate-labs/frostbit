package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

                                                          
type TestIdentity struct {
	PrivateKey    *secp256k1.PrivateKey
	PrivateKeyHex string                 
	PublicKey     []byte                         
	Address       string                                
}

                                                                                                                     
func GenerateTestIdentity(seed int) *TestIdentity {
	                                        
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(seed))
	hash := sha256.Sum256(append([]byte("testkey_seed_"), buf[:]...))

	priv := secp256k1.PrivKeyFromBytes(hash[:])
	addr, _ := DeriveDexAddress(priv)

	return &TestIdentity{
		PrivateKey:    priv,
		PrivateKeyHex: hex.EncodeToString(hash[:]),
		PublicKey:     priv.PubKey().SerializeCompressed(),
		Address:       addr,
	}
}

                                                        
func GenerateTestIdentities(n int) []*TestIdentity {
	ids := make([]*TestIdentity, n)
	for i := range ids {
		ids[i] = GenerateTestIdentity(i)
	}
	return ids
}

                                                   
func RandomHex(n int) string {
	b := make([]byte, n)
	                                        
	                                         
	rand.Read(b)
	return hex.EncodeToString(b)
}

                                                                  
func RandomAddress() string { return "0x" + RandomHex(20) }
