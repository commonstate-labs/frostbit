                                                                                                                  
  
                  
                                                                                                        
                                                                                                                               
                                                                                                            
                                                                                                                 
                                                                                         
package txverify

import (
	"crypto/sha256"
	"dex/pb"
	"dex/utils"
	"fmt"
	"strconv"
	"sync"
)

const verifiedSignatureCacheLimit = 131072

var verifiedSignatureCache = struct {
	sync.Mutex
	seen map[string]struct{}
	keys []string
	next int
}{seen: make(map[string]struct{}, verifiedSignatureCacheLimit), keys: make([]string, 0, verifiedSignatureCacheLimit)}

func verifiedSignatureCacheKey(base *pb.BaseMessage, expectedChainID uint64, pubBytes []byte) string {
	pubHash := sha256.Sum256(pubBytes)
	sigHash := sha256.Sum256(base.Signature)
	return strconv.FormatUint(expectedChainID, 10) + "|" + base.TxId + "|" + base.FromAddress + "|" + string(pubHash[:]) + "|" + string(sigHash[:])
}

func verifiedSignatureCached(key string) bool {
	verifiedSignatureCache.Lock()
	defer verifiedSignatureCache.Unlock()
	_, ok := verifiedSignatureCache.seen[key]
	return ok
}

func rememberVerifiedSignature(key string) {
	verifiedSignatureCache.Lock()
	defer verifiedSignatureCache.Unlock()
	if _, ok := verifiedSignatureCache.seen[key]; ok {
		return
	}
	if len(verifiedSignatureCache.keys) < verifiedSignatureCacheLimit {
		verifiedSignatureCache.keys = append(verifiedSignatureCache.keys, key)
	} else {
		old := verifiedSignatureCache.keys[verifiedSignatureCache.next]
		delete(verifiedSignatureCache.seen, old)
		verifiedSignatureCache.keys[verifiedSignatureCache.next] = key
		verifiedSignatureCache.next = (verifiedSignatureCache.next + 1) % verifiedSignatureCacheLimit
	}
	verifiedSignatureCache.seen[key] = struct{}{}
}

                                                                                   
                                                                                 
func Verify(tx *pb.AnyTx, expectedChainID uint64) error {
	if tx == nil {
		return fmt.Errorf("nil transaction")
	}
	base := tx.GetBase()
	if base == nil {
		return fmt.Errorf("missing base message")
	}
	if base.TxId == "" {
		return fmt.Errorf("empty tx id")
	}
	if tx.GetContent() == nil {
		return fmt.Errorf("transaction content is empty (no oneof set)")
	}
	if base.ChainId != expectedChainID {
		return fmt.Errorf("InvalidChainID: got %d, want %d", base.ChainId, expectedChainID)
	}
	                                                                            
	if canonical := tx.GenerateTxID(); canonical != base.TxId {
		return fmt.Errorf("tx_id mismatch: got %s, canonical %s", base.TxId, canonical)
	}
	if len(base.Signature) == 0 {
		return fmt.Errorf("missing signature")
	}
	if base.PublicKeys == nil || len(base.PublicKeys.Keys) == 0 {
		return fmt.Errorf("missing public_keys")
	}
	pubBytes := base.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)]
	if len(pubBytes) == 0 {
		return fmt.Errorf("missing secp256k1 public key")
	}
	cacheKey := verifiedSignatureCacheKey(base, expectedChainID, pubBytes)
	if verifiedSignatureCached(cacheKey) {
		return nil
	}
	derivedAddr, err := utils.AddressFromSecp256k1PubKey(pubBytes)
	if err != nil || derivedAddr != base.FromAddress {
		return fmt.Errorf("public key does not match from_address")
	}
	digest := sha256.Sum256([]byte(base.TxId))
	if !utils.VerifyECDSASignature(pubBytes, digest[:], base.Signature) {
		return fmt.Errorf("invalid ECDSA signature")
	}
	rememberVerifiedSignature(cacheKey)
	return nil
}
