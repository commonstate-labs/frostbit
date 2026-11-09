package types

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

                                                                  
type BlockHeader struct {
	Height    uint64                
	ParentID  string                     
	Timestamp int64                   
	StateRoot string                                            
	TxHash    string                                                   
	Proposer  string                       
	Miner     string                                                                  
	Window    int                  
	VRFProof  []byte             
	VRFOutput []byte              
	PublicKey []byte                                                
	                                                                                      
	OfflineReports []uint32
	SampledReports []uint32
	OnlineReports  []uint32
}

                                                                                         
func LivenessReportsTag(sampled, offline []uint32, online ...[]uint32) string {
	h := sha256.New()
	writeList := func(name string, xs []uint32) {
		_, _ = h.Write([]byte(name))
		var lenBuf [8]byte
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(xs)))
		_, _ = h.Write(lenBuf[:])
		var idxBuf [4]byte
		for _, idx := range xs {
			binary.BigEndian.PutUint32(idxBuf[:], idx)
			_, _ = h.Write(idxBuf[:])
		}
	}
	writeList("sampled", sampled)
	writeList("offline", offline)
	if len(online) > 0 && len(online[0]) > 0 {
		writeList("dex-online-reports-v1", online[0])
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:4])
}

                                                                               
                                                               
func (h *BlockHeader) ComputeID() string {
	return h.ComputeIDForChain(0)
}

                                                                            
                                                                            
                                                             
func (h *BlockHeader) ComputeIDForChain(chainID uint64) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("dex-block-v1"))
	writeBlockIDUint64(hash, chainID)
	writeBlockIDUint64(hash, h.Height)
	writeBlockIDBytes(hash, []byte(h.ParentID))
	writeBlockIDUint64(hash, uint64(h.Timestamp))
	writeBlockIDBytes(hash, []byte(h.StateRoot))
	writeBlockIDBytes(hash, []byte(h.TxHash))
	writeBlockIDBytes(hash, []byte(h.Proposer))
	writeBlockIDBytes(hash, []byte(h.Miner))
	writeBlockIDUint64(hash, uint64(int64(h.Window)))
	writeBlockIDBytes(hash, h.VRFProof)
	writeBlockIDBytes(hash, h.VRFOutput)
	writeBlockIDBytes(hash, h.PublicKey)
	writeBlockIDUint32s(hash, h.OfflineReports)
	writeBlockIDUint32s(hash, h.SampledReports)
	                                                               
	if len(h.OnlineReports) > 0 {
		writeBlockIDBytes(hash, []byte("dex-online-reports-v1"))
		writeBlockIDUint32s(hash, h.OnlineReports)
	}
	return fmt.Sprintf("block-%d-%s-w%d-%x", h.Height, h.Proposer, h.Window, hash.Sum(nil))
}

func writeBlockIDBytes(hash interface{ Write([]byte) (int, error) }, value []byte) {
	writeBlockIDUint64(hash, uint64(len(value)))
	_, _ = hash.Write(value)
}

func writeBlockIDUint32s(hash interface{ Write([]byte) (int, error) }, values []uint32) {
	writeBlockIDUint64(hash, uint64(len(values)))
	var buf [4]byte
	for _, value := range values {
		binary.BigEndian.PutUint32(buf[:], value)
		_, _ = hash.Write(buf[:])
	}
}

func writeBlockIDUint64(hash interface{ Write([]byte) (int, error) }, value uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	_, _ = hash.Write(buf[:])
}

                                                         
type Block struct {
	ID             string                                                          
	Header         BlockHeader            
	MinerSignature []byte                                                              
	                                                                              
	                                                                           
	                                                                               
	                                                                  
	CanonicalPayload []byte
	Transactions     []string                          
	Signatures       [][]byte                                               
}

                                               
type FinalizationChit struct {
	NodeID       string `json:"node_id"`                                   
	PreferredID  string `json:"preferred_id"`                                       
	Timestamp    int64  `json:"timestamp"`                                          
	Signature    []byte `json:"signature,omitempty"`                                    
	SampleWeight int    `json:"sample_weight,omitempty"`                                                         
}

func (c FinalizationChit) EffectiveSampleWeight() int {
	if c.SampleWeight > 0 {
		return c.SampleWeight
	}
	return 1
}

                                
type RoundChits struct {
	Round     int                `json:"round"`                                      
	Timestamp int64              `json:"timestamp"`                               
	Votes     []FinalizationChit `json:"votes"`                          
}

                                                      
type FinalizationChits struct {
	BlockID     string             `json:"block_id"`                                 
	Height      uint64             `json:"height"`                          
	TotalVotes  int                `json:"total_votes"`                                    
	TotalRounds int                `json:"total_rounds"`                    
	Rounds      []RoundChits       `json:"rounds,omitempty"`                            
	Chits       []FinalizationChit `json:"chits,omitempty"`                                         
	FinalizedAt int64              `json:"finalized_at"`                                   
}

func (c *FinalizationChits) ObservedAt() int64 {
	if c == nil {
		return 0
	}
	if c.FinalizedAt > 0 {
		return c.FinalizedAt
	}
	var latest int64
	for _, round := range c.Rounds {
		if round.Timestamp > latest {
			latest = round.Timestamp
		}
		for _, vote := range round.Votes {
			if vote.Timestamp > latest {
				latest = vote.Timestamp
			}
		}
	}
	for _, chit := range c.Chits {
		if chit.Timestamp > latest {
			latest = chit.Timestamp
		}
	}
	return latest
}

func (c *FinalizationChits) EnsureObservedAt(ts int64) {
	if c == nil || c.FinalizedAt > 0 {
		return
	}
	if observed := c.ObservedAt(); observed > 0 {
		c.FinalizedAt = observed
		return
	}
	c.FinalizedAt = ts
}

                                                       
type BlockFinalizedData struct {
	Block *Block             `json:"block"`
	Chits *FinalizationChits `json:"chits,omitempty"`
}

                                                 
func (b *Block) GetHeight() uint64    { return b.Header.Height }
func (b *Block) GetParentID() string  { return b.Header.ParentID }
func (b *Block) GetProposer() string  { return b.Header.Proposer }
func (b *Block) GetWindow() int       { return b.Header.Window }
func (b *Block) GetStateRoot() string { return b.Header.StateRoot }
func (b *Block) GetTxHash() string    { return b.Header.TxHash }
