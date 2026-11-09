package handlers

import (
	"dex/config"
	"dex/pb"
	"dex/txpool"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"google.golang.org/protobuf/proto"
)

                              
func (hm *HandlerManager) HandleTx(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleTx")
	waitAdmission := r.Header.Get("X-DEX-Wait-Admission") == "1"
	if waitAdmission && !explorerServiceAuthorized(r, true) {
		http.Error(w, "Explorer service authentication required for admission wait", http.StatusForbidden)
		return
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var incomingAny pb.AnyTx
	if err := pb.UnmarshalSanitize(bodyBytes, &incomingAny); err != nil {
		http.Error(w, "Invalid AnyTx proto", http.StatusBadRequest)
		return
	}

	host, _, _ := net.SplitHostPort(r.RemoteAddr)

	onAdded := func(txID string) {
		               
		hm.senderManager.BroadcastTx(&incomingAny)
	}
	if waitAdmission {
		err = hm.txPool.SubmitTxAndWait(r.Context(), &incomingAny, host, onAdded)
	} else {
		                                                                            
		err = hm.txPool.SubmitTx(&incomingAny, host, onAdded)
	}

	if err != nil {
		status := http.StatusInternalServerError
		errStr := err.Error()
		if errors.Is(err, txpool.ErrSyncCatchUpBackpressure) || strings.Contains(errStr, "txpool queue is full") || strings.Contains(errStr, "txpool pending is full") {
			status = http.StatusTooManyRequests
		}
		http.Error(w, fmt.Sprintf("Failed to submit tx: %v", err), status)
		return
	}

	resProto := &pb.StatusResponse{
		Status: "ok",
		Info:   "Tx received",
	}
	resBytes, _ := proto.Marshal(resProto)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	w.Write(resBytes)
}

                                                      
                                                                                                                   
func (hm *HandlerManager) HandleTxBatch(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleTxBatch")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var batch pb.BatchGetShortTxResponse
	                                                                              
	                                                                           
	                                                                                 
	if len(bodyBytes) > pb.MaxTotalSize {
		http.Error(w, "Invalid tx batch proto", http.StatusBadRequest)
		return
	}
	if err := proto.Unmarshal(bodyBytes, &batch); err != nil {
		http.Error(w, "Invalid tx batch proto", http.StatusBadRequest)
		return
	}
	if len(batch.Transactions) == 0 {
		http.Error(w, "empty tx batch", http.StatusBadRequest)
		return
	}
	if err := validateTxBatchTransactionCount(len(batch.Transactions), hm.cfg); err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	if err := pb.Sanitize(&batch); err != nil {
		http.Error(w, "Invalid tx batch proto", http.StatusBadRequest)
		return
	}

	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	queued := 0
	for _, tx := range batch.Transactions {
		if tx == nil {
			continue
		}
		txForBroadcast := tx
		err := hm.txPool.SubmitTx(txForBroadcast, host, func(txID string) {
			if hm.senderManager != nil {
				hm.senderManager.BroadcastTx(txForBroadcast)
			}
		})
		if err != nil {
			status := http.StatusInternalServerError
			errStr := err.Error()
			if errors.Is(err, txpool.ErrSyncCatchUpBackpressure) || strings.Contains(errStr, "txpool queue is full") || strings.Contains(errStr, "txpool pending is full") {
				status = http.StatusTooManyRequests
			}
			http.Error(w, fmt.Sprintf("Failed to submit tx batch: queued=%d err=%v", queued, err), status)
			return
		}
		queued++
	}

	resProto := &pb.StatusResponse{
		Status: "ok",
		Info:   fmt.Sprintf("Tx batch received: %d", queued),
	}
	resBytes, _ := proto.Marshal(resProto)
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	w.Write(resBytes)
}

func validateTxBatchTransactionCount(count int, cfg *config.Config) error {
	                                                                       
	                                                                             
	limit := config.DefaultMaxTxBatchTransactions
	if cfg != nil && cfg.Server.MaxTxBatchTransactions > 0 {
		limit = cfg.Server.MaxTxBatchTransactions
	}
	if count > limit {
		return fmt.Errorf("tx batch transaction count %d exceeds limit %d", count, limit)
	}
	return nil
}
