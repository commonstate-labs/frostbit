                   
package statedb

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash"
)

const StateRootSize = sha256.Size

var (
	stateLeafDomain = []byte("dex-state-leaf-v1")
	stateRootDomain = []byte("dex-state-root-v1")
)

                       
func u64ToBytes(u uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, u)
	return b
}

func bytesToU64(b []byte) uint64 {
	if len(b) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}

func encodeToken(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func decodeToken(tok string) string {
	if tok == "" {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return ""
	}
	return string(b)
}

                                                                                 
type StateRootAccumulator struct {
	KeyCount        int64
	TotalKeyBytes   int64
	TotalValueBytes int64
	XorDigest       [StateRootSize]byte
}

                                                     
func (a *StateRootAccumulator) Add(key string, value []byte) {
	leaf := HashStateKVLeaf(key, value)
	xorStateDigest(&a.XorDigest, leaf)
	a.KeyCount++
	a.TotalKeyBytes += int64(len(key))
	a.TotalValueBytes += int64(len(value))
}

                                                                                
func (a StateRootAccumulator) RootBytes() []byte {
	return StateRootFromParts(a.KeyCount, a.TotalKeyBytes, a.TotalValueBytes, a.XorDigest)
}

                                                                             
func HashStateKVLeaf(key string, value []byte) [StateRootSize]byte {
	h := sha256.New()
	_, _ = h.Write(stateLeafDomain)
	writeU64Hash(h, uint64(len(key)))
	_, _ = h.Write([]byte(key))
	writeU64Hash(h, uint64(len(value)))
	_, _ = h.Write(value)
	var out [StateRootSize]byte
	copy(out[:], h.Sum(nil))
	return out
}

                                                                                  
func StateRootFromParts(keyCount, totalKeyBytes, totalValueBytes int64, xorDigest [StateRootSize]byte) []byte {
	h := sha256.New()
	_, _ = h.Write(stateRootDomain)
	writeU64Hash(h, uint64(keyCount))
	writeU64Hash(h, uint64(totalKeyBytes))
	writeU64Hash(h, uint64(totalValueBytes))
	_, _ = h.Write(xorDigest[:])
	return h.Sum(nil)
}

                                                                                   
func DigestKV64(key string, value []byte) uint64 {
	leaf := HashStateKVLeaf(key, value)
	return digestLogID(leaf[:])
}

                                                                                    
                                                                        
func xxHashKV(key string, value []byte) uint64 {
	return DigestKV64(key, value)
}

func xorStateDigest(dst *[StateRootSize]byte, digest [StateRootSize]byte) {
	for i := 0; i < StateRootSize; i++ {
		dst[i] ^= digest[i]
	}
}

func writeU64Hash(h hash.Hash, v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	_, _ = h.Write(buf[:])
}

func digestLogID(b []byte) uint64 {
	if len(b) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(b[:8])
}

func isAllZeroBytes(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

                                            
var (
	ErrNotFound = errors.New("not found")
)
