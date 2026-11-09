package vm

import (
	iface "dex/interfaces"
	"dex/pb"
)

                                           

                               
type StateView interface {
	                                                                                             
	Get(key string) ([]byte, bool, error)
	Set(key string, val []byte)
	Del(key string)
	                                                                                         
	Snapshot() int
	                                                                            
	                                                                                                                   
	Revision() uint64
	Revert(snap int) error
	                                                                                                        
	Diff() []WriteOp
	                                                                              
	Scan(prefix string) (map[string][]byte, error)
	                                                                         
	ScanWithLimit(prefix string, limit int) (map[string][]byte, error)
	                                                                                                
	ScanWithLimitReverse(prefix string, limit int) (map[string][]byte, error)
}

                                  
type TxHandler interface {
	                                                                      
	Kind() string
	                                                                                                                                        
	DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error)
	                                                                                                                        
	Apply(tx *pb.AnyTx) error                                              
}

type CertChainTrackerAware interface {
	SetCertChainTracker(tracker iface.CertChainTracker)
}

                              
                                                                                       
type SpecExecCache interface {
	                                                           
	Get(blockID string) (*SpecResult, bool)
	Put(res *SpecResult)
	                                                                       
	                                                                                                                      
	EvictBelow(height uint64)
}

                     
                                                                                                               
                                                                                                                                                  
type ReadThroughFn func(key string) ([]byte, error)

type BatchReadFn func(keys []string) (map[string][]byte, error)

                                                         
type ScanFn func(prefix string) (map[string][]byte, error)

                                                       
type ScanWithLimitFn func(prefix string, limit int) (map[string][]byte, error)

                                                              
type ScanWithLimitReverseFn func(prefix string, limit int) (map[string][]byte, error)

type KVPair = iface.KVPair

type ScanWithLimitOrderedFn func(prefix string, limit int, reverse bool) ([]KVPair, error)
type ScanWithLimitOrderedAfterFn func(prefix, startAfter string, limit int, reverse bool) ([]KVPair, error)
type ScanKeysWithLimitOrderedFn func(prefix string, limit int, reverse bool) ([]string, error)
type ScanKeysWithLimitOrderedAfterFn func(prefix, startAfter string, limit int, reverse bool) ([]string, error)
type ScanFutureOrderbookFn func(prefix, startAfter string, limit int, reverse bool) ([]futureOrderbookEntry, bool, error)
type FutureOrderbookVisitFn func(futureOrderbookEntry) bool
type ScanFutureOrderbookVisitFn func(prefix, startAfter string, limit int, reverse bool, visit FutureOrderbookVisitFn) (bool, error)

                                 
                                                                                                                                                                                    
type KindFn func(tx *pb.AnyTx) (string, error)
