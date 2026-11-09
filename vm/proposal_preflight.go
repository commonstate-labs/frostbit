package vm

import (
	"dex/logs"
	"dex/pb"
	"dex/witness"
	"fmt"

	"google.golang.org/protobuf/proto"
)

                                                                            
                                                                                
  
                                                                               
                                                                         
                                                                          
                                                                                
                                    
func (x *Executor) BuildProposalSpecResult(block *pb.Block) (*SpecResult, error) {
	return x.buildDetachedProposal(block, proposalFullPreflightCapture{})
}

                                                                           
                                                                             
                                                                               
                                  
type proposalFullPreflightCapture struct{}

func (proposalFullPreflightCapture) capturePostTransactions(StateView, *pb.Block, []*Receipt) error {
	return nil
}

                                                                             
                                                                               
                                                                             
                                                                             
                     
func (x *Executor) BuildProposalSpotProjection(block *pb.Block, maxOrdersPerBook int, takerAddress string) (*ProposalSpotProjection, error) {
	if maxOrdersPerBook < 0 {
		return nil, fmt.Errorf("max orders per spot book must be non-negative")
	}
	capture := &proposalSpotCapture{maxOrders: maxOrdersPerBook, takerAddress: takerAddress}
	result, err := x.buildDetachedProposal(block, capture)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("proposal spot projection returned no VM result")
	}
	if !result.Valid {
		return nil, fmt.Errorf("proposal public prefix is invalid: %s", result.Reason)
	}
	if capture.result == nil {
		return &ProposalSpotProjection{}, nil
	}
	return capture.result, nil
}

                                                                              
                                                                           
                                                                            
                                              
func (x *Executor) BuildProposalMarketProjection(block *pb.Block, maxOrdersPerBook int, takerAddress string) (*ProposalMarketProjection, error) {
	if maxOrdersPerBook < 0 {
		return nil, fmt.Errorf("max orders per proposal book must be non-negative")
	}
	capture := &proposalMarketCapture{
		spot:   &proposalSpotCapture{maxOrders: maxOrdersPerBook, takerAddress: takerAddress},
		future: &proposalFutureCapture{maxOrders: maxOrdersPerBook, takerAddress: takerAddress},
	}
	result, err := x.buildDetachedProposal(block, capture)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("proposal market projection returned no VM result")
	}
	if !result.Valid {
		return nil, fmt.Errorf("proposal public prefix is invalid: %s", result.Reason)
	}
	if capture.spot.result == nil {
		capture.spot.result = &ProposalSpotProjection{}
	}
	if capture.future.result == nil {
		capture.future.result = &ProposalFutureProjection{TakerAvailable: make(map[string]string)}
	}
	return &ProposalMarketProjection{Spot: capture.spot.result, Future: capture.future.result}, nil
}

func (x *Executor) buildDetachedProposal(block *pb.Block, capture specExecutionCapture) (*SpecResult, error) {
	if x == nil {
		return nil, fmt.Errorf("nil executor")
	}
	if block == nil {
		return nil, ErrNilBlock
	}

	cloned, ok := proto.Clone(block).(*pb.Block)
	if !ok || cloned == nil {
		return nil, fmt.Errorf("clone proposal block")
	}

	x.mu.Lock()
	defer x.mu.Unlock()
	x.logDetachedProposalBase(cloned, capture)

	                                                                        
	                                                                         
	                                                                            
	                                                                            
	liveHotState := x.HotState
	x.HotState = NewHotStateCache()
	defer func() { x.HotState = liveHotState }()

	                                                                          
	                                                                            
	                                                
	liveWitness := x.WitnessService
	if liveWitness != nil {
		simulationWitness := liveWitness.CloneForSimulation()
		x.WitnessService = simulationWitness
		defer func() {
			x.WitnessService = liveWitness
			x.bindWitnessServiceHandlers(liveWitness)
			simulationWitness.Stop()
		}()
	}
	if capture == nil {
		return x.buildSpecResult(cloned, false)
	}
	return x.buildSpecResult(cloned, false, capture)
}

func (x *Executor) logDetachedProposalBase(block *pb.Block, capture specExecutionCapture) {
	if x == nil || x.DB == nil || block == nil || block.Header == nil {
		return
	}

	phase := "detached"
	switch capture.(type) {
	case proposalFullPreflightCapture:
		phase = "candidate_preflight"
	case *proposalSpotCapture:
		phase = "public_spot_projection"
	case *proposalMarketCapture:
		phase = "public_market_projection"
	}

	height := block.Header.Height
	expectedParent := block.Header.PrevBlockHash
	parentCommitted := height == 0
	parentMatches := height == 0 && expectedParent == ""
	observedParent := ""
	if height > 0 {
		parentCommitted, observedParent = x.IsBlockCommitted(height - 1)
		parentMatches = parentCommitted && observedParent == expectedParent
	}
	targetCommitted, committedTarget := x.IsBlockCommitted(height)
	format := "[ArbitrageDiag][ProposalVMBase] phase=%s height=%d expected_parent=%q observed_parent=%q parent_committed=%t parent_match=%t target_already_committed=%t committed_target=%q block=%q txs_hash=%q body_txs=%d"
	args := []interface{}{
		phase,
		height,
		expectedParent,
		observedParent,
		parentCommitted,
		parentMatches,
		targetCommitted,
		committedTarget,
		block.BlockHash,
		block.Header.TxsHash,
		len(block.Body),
	}
	if targetCommitted || !parentMatches {
		logs.Warn(format, args...)
		return
	}
	logs.Info(format, args...)
}

func (x *Executor) bindWitnessServiceHandlers(service *witness.Service) {
	if x == nil || x.Reg == nil {
		return
	}
	for _, kind := range x.Reg.List() {
		handler, ok := x.Reg.Get(kind)
		if !ok {
			continue
		}
		if aware, ok := handler.(WitnessServiceAware); ok {
			aware.SetWitnessService(service)
		}
	}
}
