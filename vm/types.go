package vm

import (
	"dex/pb"
	"errors"
	"math/big"
)

                                                      
  
                                                                                   
  
             
                                                                                                  
                                                                                                                                    
  
                                            
                                                                    
  
                                           
                                                                                                                     
                                                                                       
  
                                                                                                             
                                                                                                     
                                                                                                  

                                     

var (
	ErrNotImplemented  = errors.New("not implemented")
	ErrNilBlock        = errors.New("nil block")
	ErrNilTx           = errors.New("nil transaction")
	ErrInvalidSnapshot = errors.New("invalid snapshot index")
)

                                           

                                    
type WriteOp struct {
	Key                   string                                               
	Value                 []byte                      
	Del                   bool                            
	Category              string                                                                                   
	OldValue              []byte                                                                            
	OldExists             bool
	HasOldValue           bool
	OldValueAuthoritative bool
}

                                       

                    
func (w *WriteOp) GetKey() string {
	return w.Key
}

                        
func (w *WriteOp) GetValue() []byte {
	return w.Value
}

                           
func (w *WriteOp) IsDel() bool {
	return w.Del
}

func (w *WriteOp) OldValueKnown() bool {
	return w.HasOldValue
}

func (w *WriteOp) GetOldValue() []byte {
	return w.OldValue
}

func (w *WriteOp) OldValueExists() bool {
	return w.OldExists
}

func (w *WriteOp) OldValueIsAuthoritative() bool {
	return w.HasOldValue && w.OldValueAuthoritative
}

                     
type Receipt struct {
	TxID           string
	Status         string                         
	Error          string
	BlockHeight    uint64
	Timestamp      int64
	GasUsed        uint64
	Fee            string
	Logs           []string
	WriteCount     int
	FBBalanceAfter string                                                
	Events         []Event
	                                                                                                            
	skipGlobalPersistence bool
}

                                                                                                                
func (r *Receipt) AppendEvent(kind EventKind, payload map[string]string) {
	if r == nil {
		return
	}
	r.Events = append(r.Events, Event{Kind: kind, Payload: payload})
}

                                                                                                               
  
                                      
                                                                               
                                                                               
                                                                                               
                                                       
type EventKind string

const (
	EventOrderMatched    EventKind = "ORDER_MATCHED"
	EventOrderCancelled  EventKind = "ORDER_CANCELLED"
	EventPositionOpened  EventKind = "POSITION_OPENED"
	EventPositionReduced EventKind = "POSITION_REDUCED"
	EventPositionClosed  EventKind = "POSITION_CLOSED"
	EventFundingSettled  EventKind = "FUNDING_SETTLED"
	EventLiquidated      EventKind = "LIQUIDATED"
	EventADLExecuted     EventKind = "ADL_EXECUTED"
	EventFeeCharged      EventKind = "FEE_CHARGED"
)

                                           
                                                                
                                                                              
                                                                        
  
                                                                               
type Event struct {
	Kind    EventKind
	Payload map[string]string
}

                          
type SpecResult struct {
	BlockID  string
	ParentID string
	Height   uint64
	                                                                                               
	BaseCommittedHeight uint64
	BaseCommittedHash   string
	Valid               bool
	Reason              string                          
	Receipts            []*Receipt                      
	Diff                []WriteOp                       
}

                                                         
type SpotTradeUpdate struct {
	OrderID    string
	TradeAmt   *big.Int
	TradePrice *big.Int
	IsFilled   bool
}

                                                                    
                                                                                                     
func failedReceipt(base *pb.BaseMessage, errMsg string) *Receipt {
	if base == nil {
		return &Receipt{Status: "FAILED", Error: errMsg}
	}
	return &Receipt{
		TxID:        base.TxId,
		Status:      "FAILED",
		Error:       errMsg,
		BlockHeight: base.ExecutedHeight,
	}
}
