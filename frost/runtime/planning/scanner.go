                                    
                             
package planning

import (
	"dex/frost/runtime/types"
	"dex/keys"
	"dex/pb"
	"strconv"

	"google.golang.org/protobuf/proto"
)

                                                                                           
type ChainStateReader = types.ChainStateReader

                          
type ScanResult struct {
	Chain      string
	Asset      string
	WithdrawID string
	Seq        uint64
	                                                                               
	state *pb.FrostWithdrawState
}

                               
type Scanner struct {
	stateReader ChainStateReader
}

                                  
func NewScanner(stateReader ChainStateReader) *Scanner {
	return &Scanner{
		stateReader: stateReader,
	}
}

                                                             
                                                                  
func (s *Scanner) ScanOnce(chain, asset string) (*ScanResult, error) {
	                                                      
	headKey := keys.KeyFrostWithdrawFIFOHead(chain, asset)
	headData, exists, err := s.stateReader.Get(headKey)
	if err != nil {
		return nil, err
	}

	var head uint64 = 1                          
	if exists && len(headData) > 0 {
		head, _ = strconv.ParseUint(string(headData), 10, 64)
		if head == 0 {
			head = 1
		}
	}

	                                                  
	seqKey := keys.KeyFrostWithdrawFIFOSeq(chain, asset)
	seqData, exists, err := s.stateReader.Get(seqKey)
	if err != nil {
		return nil, err
	}

	if !exists || len(seqData) == 0 {
		                                
		return nil, nil
	}

	maxSeq, _ := strconv.ParseUint(string(seqData), 10, 64)
	if head > maxSeq {
		                               
		return nil, nil
	}

	                                       
	indexKey := keys.KeyFrostWithdrawFIFOIndex(chain, asset, head)
	withdrawID, exists, err := s.stateReader.Get(indexKey)
	if err != nil {
		return nil, err
	}

	if !exists || len(withdrawID) == 0 {
		                                             
		return nil, nil
	}

	                                           
	withdrawKey := keys.KeyFrostWithdraw(string(withdrawID))
	withdrawData, exists, err := s.stateReader.Get(withdrawKey)
	if err != nil {
		return nil, err
	}

	if !exists || len(withdrawData) == 0 {
		return nil, nil
	}

	                         
	state := &pb.FrostWithdrawState{}
	if err := proto.Unmarshal(withdrawData, state); err != nil {
		return nil, err
	}

	                                      
	if state.Status != "QUEUED" {
		return nil, nil
	}

	return &ScanResult{
		Chain:      chain,
		Asset:      asset,
		WithdrawID: string(withdrawID),
		Seq:        head,
	}, nil
}
