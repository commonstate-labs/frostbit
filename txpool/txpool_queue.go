package txpool

import (
	"dex/lifecycle"
	"dex/pb"
	"fmt"
	"log"
)

                     
type txMsgType int

const (
	msgAddTx txMsgType = iota
	msgRemoveTx
)

                                                                                    
type OnTxAddedCallback func(txID string)

                                                                
type txPoolMessage struct {
	Bytes   int64
	Type    txMsgType
	AnyTx   *pb.AnyTx
	IP      string
	OnAdded OnTxAddedCallback
	Result  chan<- error
}

type txPoolQueue struct {
	pool      *TxPool
	validator TxValidator
	MsgChan   chan *txPoolMessage
}

                                       
type TxValidator interface {
	CheckAnyTx(tx *pb.AnyTx) error
}

                                          
func newTxPoolQueue(pool *TxPool, validator TxValidator) *txPoolQueue {
	return &txPoolQueue{
		pool:      pool,
		validator: validator,
		MsgChan:   make(chan *txPoolMessage, 10000),
	}
}

func (tq *txPoolQueue) runLoop() {
	defer tq.pool.wg.Done()
	                                                               

	for {
		select {
		case <-tq.pool.stopChan:
			return
		case msg := <-tq.MsgChan:
			if msg == nil {
				continue
			}
			tq.pool.queuedBytes.Add(-msg.Bytes)
			switch msg.Type {
			case msgAddTx:
				if tq.pool.emergency.IsStopped() {                                         
					reportAdmissionResult(msg.Result, lifecycle.ErrEmergencyStop)
					continue
				}
				reportAdmissionResult(msg.Result, tq.handleAddTx(msg.AnyTx, msg.IP, msg.OnAdded))
			case msgRemoveTx:
				txID := msg.AnyTx.GetTxId()
				tq.pool.RemoveTx(txID)
				tq.pool.Logger.Debug("[TxPoolQueue] removed tx=%s from TxPool", txID)
			default:
				tq.pool.Logger.Debug("[TxPoolQueue] unknown msg type: %d", msg.Type)
			}
		}
	}
}

func reportAdmissionResult(result chan<- error, err error) {
	if result == nil {
		return
	}
	result <- err
}

func (tq *txPoolQueue) handleAddTx(incoming *pb.AnyTx, ip string, onAdded OnTxAddedCallback) error {
	if incoming == nil {
		return fmt.Errorf("nil transaction")
	}
	txID := incoming.GetTxId()
	if txID == "" {
		log.Println("[TxPoolQueue] AddTx but no txID, skip.")
		return fmt.Errorf("empty tx id")
	}

	if tq.pool.HasTransaction(txID) {
		return nil
	}

	base := incoming.GetBase()
	if base == nil {
		tq.pool.Logger.Warn("[TxPoolQueue] tx=%s dropped: missing BaseMessage", txID)
		return fmt.Errorf("missing BaseMessage")
	}

	         
	isNew, err := tq.pool.storeAnyTx(incoming)
	if err != nil {
		tq.pool.Logger.Warn("[TxPoolQueue] tx=%s dropped: store fail: %v", txID, err)
		return err
	}

	                                                                                                                              
	if isNew && onAdded != nil {
		onAdded(txID)
	}
	return nil
}
