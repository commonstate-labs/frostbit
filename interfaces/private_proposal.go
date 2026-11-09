package interfaces

import (
	"dex/pb"
	"dex/types"
)

                                                                             
                                                                          
                                                                             
                                      
type ProposalContext struct {
	ParentID string
	Height   uint64
	Proposer types.NodeID
	Window   int
	MaxTxs   int
	                                                                        
	                                                                           
	                                                                          
	                                                                          
	                                                                          
	PrivateTxLimit int
	                                                                              
	                                                                            
	                                                                              
	                                                          
	PublicPrefixCount int
	PublicPrefixHash  string
}

                                                                       
                                                                             
                                                                            
                                                                              
          
type PrivateProposalLimits struct {
	MaxTransactions  int
	MaxOrdersPerBook int
}

                                                                        
                                                           
type ProposalSpotMaker struct {
	OrderID   string
	Owner     string
	Remaining string
}

                                                                      
                                                                           
                                                   
type ProposalSpotLevel struct {
	BaseToken  string
	QuoteToken string
	TakerSide  pb.OrderSide
	Price      string
	Makers     []ProposalSpotMaker
}

                                                                           
                                                                             
                                                                              
                                   
type ProposalSpotMatch struct {
	MakerOrderID string
	Quantity     string
	Price        string
}

                                                                       
                                                                             
                                                                              
                                                                          
type ProposalSpotExecution struct {
	PublicPosition int
	OrderID        string
	Owner          string
	BaseToken      string
	QuoteToken     string
	Side           pb.OrderSide
	Price          string
	Amount         string
	Remaining      string
	Matches        []ProposalSpotMatch
	Levels         []ProposalSpotLevel
}

                                                                            
                                                                              
                                                                        
type ProposalSpotMarket struct {
	PublicPrefixHash  string
	PublicPrefixCount int
	Levels            []ProposalSpotLevel
	Executions        []ProposalSpotExecution
	                                                                           
	                                                                         
	                                                                      
	                                                                       
	FutureLevels     []ProposalFutureLevel
	FutureExecutions []ProposalFutureExecution
	                                                                           
	                                                                       
	                                                                          
	FutureAvailable map[string]string
}

                                                                           
                                                   
type ProposalFutureMaker struct {
	OrderID   string
	Owner     string
	Remaining string
	Leverage  uint64
}

                                                                          
                                                                             
                                                                      
type ProposalFutureLevel struct {
	Pair       string
	QuoteToken string
	TakerSide  pb.OrderSide
	Price      string
	Makers     []ProposalFutureMaker
}

                                                                        
                                                        
type ProposalFutureMatch struct {
	MakerOrderID string
	Quantity     string
	Price        string
}

                                                                              
                                                 
type ProposalFutureExecution struct {
	PublicPosition int
	OrderID        string
	Owner          string
	Pair           string
	QuoteToken     string
	Side           pb.OrderSide
	Price          string
	Quantity       string
	Remaining      string
	Leverage       uint64
	Matches        []ProposalFutureMatch
	Levels         []ProposalFutureLevel
	                                                                      
	                                                                         
	                                                                            
	TakerAvailable map[string]string
}

                                                                         
                                                                             
                                                                          
                                                                     
type PrivateTxBatch struct {
	ID           string
	Transactions []*pb.AnyTx
	                                                                         
	                                                                         
	                                                                            
	                                                                            
	                                                                           
	                                                                            
	                                                                           
	                                                                             
	                                                                            
	InsertAfterPublic []int
	PublicPrefixHash  string
	PublicPrefixCount int
}

                                                                        
                                                   
type ProposalSimulationEvent struct {
	Kind    string
	Payload map[string]string
}

                                                                      
                                                                           
type ProposalSimulationReceipt struct {
	TxID           string
	Status         string
	Error          string
	BlockHeight    uint64
	Fee            string
	FBBalanceAfter string
	Events         []ProposalSimulationEvent
}

                                                                              
                                                                           
                                                 
type ProposalSimulationResult struct {
	Valid    bool
	Reason   string
	Receipts []ProposalSimulationReceipt
}

                                                                             
                                                                           
                                                   
type PrivateTxProvider interface {
	                                                                
	                                                                               
	                                                      
	SenderAddress() string
	Limits() PrivateProposalLimits
	HasPending(ctx ProposalContext) bool
	                                                                            
	                                                                            
	                                                                         
	                                                                          
	Prepare(ctx ProposalContext, orderedPublic []*pb.AnyTx, market *ProposalSpotMarket) (*PrivateTxBatch, error)
	ValidateCandidate(ctx ProposalContext, batch *PrivateTxBatch, result *ProposalSimulationResult) error
}

                                                                              
                                                                          
                                                                             
                                                                         
                                                                             
                                                       
type PrivateTxCandidateProvider interface {
	PrivateTxProvider
	PrepareCandidates(ctx ProposalContext, orderedPublic []*pb.AnyTx, market *ProposalSpotMarket) ([]*PrivateTxBatch, error)
}

                                                                            
                                                                             
                                                                        
                                                                             
                                                                    
type ProposalPreflight interface {
	ProjectSpotPrefix(ctx ProposalContext, orderedPublic []*pb.AnyTx, maxOrdersPerBook int, takerAddress string) (*ProposalSpotMarket, error)
	Simulate(block *pb.Block) (*ProposalSimulationResult, error)
}
