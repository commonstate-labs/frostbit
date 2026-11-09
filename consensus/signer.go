package consensus

import (
	"crypto/sha256"
	"dex/interfaces"
	"dex/logs"
	"dex/pb"
	"dex/types"
	"dex/utils"
	"encoding/binary"
	"hash"
	"sync"
)

var (
	nodePublicKeys          = make(map[string][]byte)
	nodePublicKeysMu        sync.RWMutex
	nodePublicKeyResolver   func(string) ([]byte, bool)
	nodePublicKeyResolverMu sync.RWMutex
)

func RegisterNodePublicKey(nodeID types.NodeID, pubKeyBytes []byte) {
	if len(pubKeyBytes) == 0 {
		return
	}
	nodePublicKeysMu.Lock()
	nodePublicKeys[string(nodeID)] = append([]byte(nil), pubKeyBytes...)
	nodePublicKeysMu.Unlock()
}

func GetNodePublicKey(nodeID string) ([]byte, bool) {
	nodePublicKeysMu.RLock()
	pub, ok := nodePublicKeys[nodeID]
	nodePublicKeysMu.RUnlock()
	return pub, ok
}

                                                                                                        
func SetNodePublicKeyResolver(resolver func(string) ([]byte, bool)) {
	nodePublicKeyResolverMu.Lock()
	nodePublicKeyResolver = resolver
	nodePublicKeyResolverMu.Unlock()
}

func ResolveNodePublicKey(nodeID string) ([]byte, bool) {
	if pub, ok := GetNodePublicKey(nodeID); ok && len(pub) > 0 {
		return pub, true
	}
	nodePublicKeyResolverMu.RLock()
	resolver := nodePublicKeyResolver
	nodePublicKeyResolverMu.RUnlock()
	if resolver == nil {
		return nil, false
	}
	pub, ok := resolver(nodeID)
	if !ok || len(pub) == 0 {
		return nil, false
	}
	RegisterNodePublicKey(types.NodeID(nodeID), pub)
	return GetNodePublicKey(nodeID)
}

func ClearNodePublicKeys() {
	nodePublicKeysMu.Lock()
	nodePublicKeys = make(map[string][]byte)
	nodePublicKeysMu.Unlock()
}

const (
	SnowmanQueryNonceSize = 32
	pushQueryV2Domain     = "DEX_SNOWMAN_PUSH_QUERY_V2"
	pullQueryV2Domain     = "DEX_SNOWMAN_PULL_QUERY_V2"
	chitV2Domain          = "DEX_SNOWMAN_CHIT_V2"
)

type ChitDigestContext struct {
	ChainID        uint64
	RequestID      uint32
	Requester      string
	Responder      string
	PreferredBlock string
	QueryHeight    uint64
	AcceptedBlock  string
	AcceptedHeight uint64
	VRFSeed        []byte
	SeqID          uint32
	QueryNonce     []byte
}

func writeQueryDigestBytes(h hash.Hash, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(value)
}

func writeQueryDigestUint32(h hash.Hash, value uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], value)
	_, _ = h.Write(buf[:])
}

func writeQueryDigestUint64(h hash.Hash, value uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	_, _ = h.Write(buf[:])
}

                                                                        
                                                                           
                                                                            
func ComputeChitDigest(context ChitDigestContext) []byte {
	h := sha256.New()
	writeQueryDigestBytes(h, []byte(chitV2Domain))
	writeQueryDigestUint64(h, context.ChainID)
	writeQueryDigestUint32(h, context.RequestID)
	writeQueryDigestBytes(h, []byte(context.Requester))
	writeQueryDigestBytes(h, []byte(context.Responder))
	writeQueryDigestBytes(h, []byte(context.PreferredBlock))
	writeQueryDigestUint64(h, context.QueryHeight)
	writeQueryDigestBytes(h, []byte(context.AcceptedBlock))
	writeQueryDigestUint64(h, context.AcceptedHeight)
	writeQueryDigestBytes(h, context.VRFSeed)
	writeQueryDigestUint32(h, context.SeqID)
	writeQueryDigestBytes(h, context.QueryNonce)
	return h.Sum(nil)
}

                                                                              
                                                                            
                                                                        
                                                            
func ComputePushQueryDigest(query *pb.PushQuery) []byte {
	if query == nil {
		return nil
	}
	h := sha256.New()
	writeQueryDigestBytes(h, []byte(pushQueryV2Domain))
	writeQueryDigestUint64(h, query.ChainId)
	writeQueryDigestUint32(h, query.RequestId)
	writeQueryDigestBytes(h, []byte(query.Address))
	writeQueryDigestBytes(h, []byte(query.Responder))
	if query.ContainerIsBlock {
		_, _ = h.Write([]byte{1})
	} else {
		_, _ = h.Write([]byte{0})
	}
	containerDigest := sha256.Sum256(query.Container)
	_, _ = h.Write(containerDigest[:])
	writeQueryDigestUint64(h, query.RequestedHeight)
	writeQueryDigestBytes(h, []byte(query.BlockId))
	writeQueryDigestBytes(h, query.Nonce)
	writeQueryDigestBytes(h, query.VrfSeed)
	writeQueryDigestUint32(h, query.SeqId)
	return h.Sum(nil)
}

                                                                     
func ComputePullQueryDigest(query *pb.PullQuery) []byte {
	if query == nil {
		return nil
	}
	h := sha256.New()
	writeQueryDigestBytes(h, []byte(pullQueryV2Domain))
	writeQueryDigestUint64(h, query.ChainId)
	writeQueryDigestUint32(h, query.RequestId)
	writeQueryDigestBytes(h, []byte(query.Address))
	writeQueryDigestBytes(h, []byte(query.Responder))
	writeQueryDigestBytes(h, []byte(query.BlockId))
	writeQueryDigestUint64(h, query.RequestedHeight)
	writeQueryDigestBytes(h, query.VrfSeed)
	writeQueryDigestUint32(h, query.SeqId)
	writeQueryDigestBytes(h, query.Nonce)
	return h.Sum(nil)
}

func VerifyChitSignature(nodeID string, digest []byte, sig []byte) bool {
	pubBytes, ok := ResolveNodePublicKey(nodeID)
	if !ok {
		logs.Debug("[VerifyChitSignature] No public key registered for node %s", nodeID)
		return false
	}
	return utils.VerifyECDSASignature(pubBytes, digest, sig)
}

func RegisterSignerPublicKey(nodeID types.NodeID, signer interfaces.NodeSigner) {
	if signer == nil {
		return
	}
	pubBytes := signer.PublicKeyBytes()
	if len(pubBytes) > 0 {
		RegisterNodePublicKey(nodeID, pubBytes)
	}
}
