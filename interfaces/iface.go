package interfaces

import (
	"context"
	"dex/pb"
	"dex/types"
	"time"
)

type BlockStore interface {
	Add(block *types.Block) (bool, error)
	Get(id string) (*types.Block, bool)
	GetByHeight(height uint64) []*types.Block
	GetLastAccepted() (string, uint64)
	GetFinalizedAtHeight(height uint64) (*types.Block, bool)
	GetBlocksFromHeight(from, to uint64) []*types.Block
	GetCurrentHeight() uint64

	                     
	GetPendingBlocksCount() int
	GetPendingBlocks() []*types.Block

	SetFinalized(height uint64, blockID string) error
}

type ConsensusEngine interface {
	Start(ctx context.Context) error
	RegisterQuery(nodeID types.NodeID, requestID uint32, blockID string, height uint64, seqID uint32, vrfSeed []byte, sampleWeights map[types.NodeID]int) string
	SubmitChit(nodeID types.NodeID, queryKey string, preferredID string, chitSignature []byte)
	GetActiveQueryCount() int
	GetPreference(height uint64) string
}

type Event interface {
	Type() types.EventType
	Data() interface{}
}

                                               
                           
                                               

                                 
type BlockProposer interface {
	                                              
	                        
	                       
	                        
	                                                 
	ProposeBlock(parentID string, height uint64, proposer types.NodeID, window int) (*types.Block, error)

	                                                                    
	                   
	                             
	                                                      
	                              
	                              
	                                    
	ShouldPropose(nodeID types.NodeID, window int, currentBlocks int, currentHeight int, proposeHeight int, lastBlockTime time.Time, parentID string) bool
}

                                               
                        
                                               

type Transport interface {
	Send(to types.NodeID, msg types.Message) error
	Receive() <-chan types.Message
	Broadcast(msg types.Message, peers []types.NodeID)
	SamplePeers(exclude types.NodeID, count int) []types.NodeID
	GetAllPeers(exclude types.NodeID) []types.NodeID                                                  
}

                                                                                   
type NodeSigner interface {
	                                                                                       
	Sign(digest []byte) ([]byte, error)
	                                                         
	PublicKeyBytes() []byte
}

type EventBus interface {
	Subscribe(topic types.EventType, handler EventHandler)
	Publish(event Event)
	PublishAsync(event Event)
}

type EventHandler func(Event)

                                               
                                                         
                                               

                                                                               
                                                                                    
type OrderIndexEntry struct {
	OrderID   string
	IndexData []byte
}

type KVPair struct {
	Key   string
	Value []byte
}

                                  
type DBSession interface {
	Get(key string) ([]byte, error)
	ApplyStateUpdate(height uint64, updates []interface{}) ([]byte, error)
	Commit() error
	Rollback() error
	Close() error
}

                                     
                                                                             
                                                                                          
                                                                                      
  
                                                                                     
                                                                                          
type StateReader interface {
	Get(key string) ([]byte, error)
	Scan(prefix string) (map[string][]byte, error)
	ScanKVWithLimit(prefix string, limit int) (map[string][]byte, error)
	ScanKVWithLimitReverse(prefix string, limit int) (map[string][]byte, error)
	ScanOrdersByPairs(pairs []string) (map[string]map[string][]byte, error)
	GetKV(key string) ([]byte, error)
}

                                                              
  
                                                                                                            
                                                                     
                                                                                           
                                                                                           
  
                                                                     
type DBManager interface {
	StateReader
	EnqueueSet(key, value string)
	EnqueueDel(key string)
	ForceFlush() error
	NewSession() (DBSession, error)

	                                                                                 
	GetFrostVaultState(key string) (*pb.FrostVaultState, error)
}

                                               
                              
                                               

                                                                                     
                                                                                                  
                                                       
type LivenessMinerSnapshot struct {
	Index                    uint32
	Address                  string
	State                    int                                   
	Stake                    uint64
	JoinHeight               uint64
	ActiveSinceHeight        uint64
	VaultEligibleHeight      uint64
	MaturedStake             uint64
	PendingStake             uint64
	PendingStakeMatureHeight uint64
	ExitHeight               uint64
	TotalSlashed             uint64
	Kicked                   bool                                                    
}

                                                                                            
type LivenessSlashEvent struct {
	Address string
	Amount  uint64
}

                                                                                                  
                                                                            
type LivenessWriteView interface {
	Set(key string, val []byte)
	Del(key string)
}

                                                      
                                                                                                  
                                                                                                                   
                                                                                               
type NodeLocalWriter interface {
	Get(key string) ([]byte, error)
	EnqueueSet(key, value string)
	EnqueueDel(key string)
	Scan(prefix string) (map[string][]byte, error)
	ForceFlush() error
}

                                                                                 
type LivenessReportSink interface {
	ReportSampled(addresses []string)
	ReportOffline(addresses []string)
}

                                                                          
                                                                           
                               
type LivenessAtomicReportSink interface {
	ReportLiveness(sampled, offline []string)
}

                                                                               
                                                                               
type LivenessOnlineReportSink interface {
	ReportLivenessWithOnline(sampled, offline, online []string)
}

                                                                                 
type LivenessHeaderReporter interface {
	GetSampledReports() []uint32
	GetOfflineReports() []uint32
}

                                                                               
                                                                              
                                                 
type LivenessAtomicHeaderReporter interface {
	GetLivenessReports() (sampled, offline []uint32)
}

                                                                               
type LivenessOnlineHeaderReporter interface {
	GetLivenessReportsWithOnline() (sampled, offline, online []uint32)
}

type LivenessLocalReporter interface {
	LivenessReportSink
	LivenessHeaderReporter
}

type LivenessTracker interface {
	                                                      
	                                                           
	ProcessBlock(sv LivenessWriteView, height uint64, sampled, offline []uint32) ([]uint32, []LivenessSlashEvent)
	                                 
	RegisterMinerAtHeight(sv LivenessWriteView, address string, stake uint64, height uint64) (uint32, error)
	                                                                
	RemoveMiner(sv LivenessWriteView, address string, height uint64) error
	                                                                             
	UnregisterMiner(sv LivenessWriteView, address string) error
	                                                                                          
	                                                                              
	UnregisterMinersBatch(sv LivenessWriteView, addresses []string) error
	         
	IsActive(address string) bool
	IsActiveOrProbation(address string) bool
	IsRegistered(address string) bool                                          
	GetActiveCount() int
	GetAddressByIndex(idx uint32) (string, bool)
	                                                             
	GetActiveMiners() []string
	                     
	GetMinerState(address string) int
	                                                                                               
	GetMinerSnapshot(address string) *LivenessMinerSnapshot
}

                                                                               
                                                                                 
                                                                              
                                                     
type LivenessReporterTracker interface {
	ProcessBlockWithReporter(sv LivenessWriteView, height uint64, reporter string, sampled, offline []uint32) ([]uint32, []LivenessSlashEvent)
}

                                                                        
                                                                         
type LivenessOnlineReporterTracker interface {
	ProcessBlockWithOnlineReporter(sv LivenessWriteView, height uint64, reporter string, sampled, offline, online []uint32) ([]uint32, []LivenessSlashEvent)
}

                                                                            
                                                                             
                                                                               
type LivenessVaultEligibilityTracker interface {
	VaultEligibilityChangedAt() uint64
}

                                                                              
                                                                                
                                                                          
type LivenessStakeTracker interface {
	UpdateMinerStake(sv LivenessWriteView, address string, totalStake uint64, height uint64) error
}

                                               
                                   
                                               

type CertChainTracker interface {
	                                                                                                           
	                                                                                                         
	OnCheckpointExecuted(height uint64, blockHash []byte, stateRoot []byte, activeMiners []CertMinerSnapshot)
	                                                                                                   
	CollectCertSignTx(blockHeight uint64, checkpointHeight uint64, fromAddress string, blsSig []byte, txDigest []byte)
	                                                                                         
	ProcessCollectedCertSigns(fromHeight uint64, toHeight uint64)
	                                                                                           
	OnFinalizedHeight(height uint64)
	                                                                                                     
	                                                                                                      
	ProcessCertSign(checkpointHeight uint64, fromAddress string, blsSig []byte, txDigest []byte) error
	                                           
	GetCertificate(height uint64) ([]byte, bool)
	                                                  
	IsCheckpointHeight(height uint64) bool
	                                   
	GetCheckpointInterval() uint64
	                                                                                                              
	GetPrevMinerPubKeys() map[uint32][]byte
	                                                    
	GetPrevMinerByAddress(addr string) (pubKey []byte, index uint32, found bool)
	                                                                                   
	GetPendingMinerByAddress(height uint64, addr string) (pubKey []byte, index uint32, found bool)
	                                                                                            
	GetPendingCertDigest(height uint64) (digest []byte, found bool)
	                                                                     
	SyncGlobalState(lastCheckpoint, lastAssembledCert uint64)
	                                                                            
	ReloadFromDB()
}

                                                                                      
                                                                                              
type CertMinerSnapshot struct {
	Index           uint32
	Address         string
	PublicKey       []byte
	EffectiveWeight uint64
}
