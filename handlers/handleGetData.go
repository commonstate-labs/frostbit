package handlers

import (
	"dex/consensus"
	"dex/logs"
	"dex/pb"
	"fmt"
	"io"
	"net/http"
	"time"

	"google.golang.org/protobuf/proto"
)

                                                                   
func (hm *HandlerManager) HandleGetData(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleGetData")
	if !hm.checkAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read getdata request body", http.StatusBadRequest)
		return
	}

	var getDataMsg pb.GetData
	if err := pb.UnmarshalSanitize(bodyBytes, &getDataMsg); err != nil {
		http.Error(w, "Invalid GetData proto", http.StatusBadRequest)
		return
	}

	                    
	txFromPool := hm.txPool.GetTransactionById(getDataMsg.TxId)
	if txFromPool != nil {
		respData, err := proto.Marshal(txFromPool)
		if err != nil {
			http.Error(w, "Failed to marshal tx payload", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		w.Write(respData)
		return
	}

	                  
	anyTx, err := hm.dbManager.GetAnyTxById(getDataMsg.TxId)
	if err != nil {
		http.Error(w, fmt.Sprintf("[TxPool: not found] [DB error: %v]", err), http.StatusNotFound)
		return
	}
	if anyTx == nil {
		http.Error(w, fmt.Sprintf("[TxPool: not found] [DB: returned nil for %s]", getDataMsg.TxId), http.StatusNotFound)
		return
	}

	respData, err := proto.Marshal(anyTx)
	if err != nil {
		http.Error(w, "Failed to marshal tx payload", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	w.Write(respData)
}

                                                    
func (hm *HandlerManager) HandleGetTxReceipt(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleGetTxReceipt")
	if !hm.checkAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read getdata request body", http.StatusBadRequest)
		return
	}

	var getDataMsg pb.GetData
	if err := pb.UnmarshalSanitize(bodyBytes, &getDataMsg); err != nil {
		http.Error(w, "Invalid GetData proto", http.StatusBadRequest)
		return
	}

	receipt, err := hm.dbManager.GetTxReceipt(getDataMsg.TxId)
	if err != nil {
		http.Error(w, fmt.Sprintf("Receipt for %s not found: %v", getDataMsg.TxId, err), http.StatusNotFound)
		return
	}

	respData, err := proto.Marshal(receipt)
	if err != nil {
		http.Error(w, "Failed to marshal receipt payload", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	w.Write(respData)
}

                                           
func (hm *HandlerManager) HandleGet(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleGet")
	if !hm.allowArchiveDataRequest(r) {
		http.Error(w, "verified service peer or registered miner required", http.StatusForbidden)
		return
	}
	if !hm.checkAuth(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	body, err := readArchiveRequestBody(w, r)
	if err != nil {
		http.Error(w, "Failed to read getblockbyid request body", http.StatusBadRequest)
		return
	}
	var req pb.GetBlockByIDRequest
	if err := pb.UnmarshalSanitize(body, &req); err != nil {
		http.Error(w, "Invalid GetBlockByID proto", http.StatusBadRequest)
		return
	}
	                                                                                                    
	cached, _ := consensus.GetCachedBlock(req.BlockId)
	var acceptedHeight uint64
	if hm.consensusManager != nil {
		_, acceptedHeight = hm.consensusManager.GetLastAccepted()
	}
	release, ok := hm.acquireBlockByIDServe(r, cached, acceptedHeight)
	if !ok {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "block sync service capacity exhausted", http.StatusServiceUnavailable)
		return
	}
	defer release()

	                                                                            
	var blk *pb.Block
	source := "unknown"
	if cached != nil {
		blk = cached
		source = "cache"
	}

	                     
	if blk == nil {
		dbBlock, err := hm.dbManager.GetBlockByID(req.BlockId)
		if err == nil && dbBlock != nil {
			blk = dbBlock
			source = "db"
		}
	}

	                                                  
	if blk == nil && hm.consensusManager != nil {
		if hm.consensusManager.Node != nil && hm.adapter != nil {
			if b, ok := hm.consensusManager.Node.GetBlock(req.BlockId); ok && b != nil {
				blk = hm.adapter.ConsensusBlockToDB(b, nil)
				source = "consensus_node"
			}
		}

		if blk == nil {
			if pbb := hm.consensusManager.GetPendingBlockBuffer(); pbb != nil {
				if tBlk, shortTxs := pbb.GetPendingBlock(req.BlockId); tBlk != nil {
					blk = hm.adapter.ConsensusBlockToDB(tBlk, nil)
					blk.ShortTxs = shortTxs
					source = "pending_buffer"
				}
			}
		}
	}

	if blk == nil {
		logs.Warn("[HandleGet] Block %s not found for %s (source=%s)",
			req.BlockId, r.RemoteAddr, source)
		http.Error(w, fmt.Sprintf("Block %s not found (pending or missing)", req.BlockId), http.StatusNotFound)
		return
	}

	                                                          
	enrichedFromCache := false
	if len(blk.ShortTxs) == 0 || len(blk.Body) == 0 {
		if cached, exists := consensus.GetCachedBlock(req.BlockId); exists && cached != nil {
			if len(blk.ShortTxs) == 0 && len(cached.ShortTxs) > 0 {
				blk.ShortTxs = cached.ShortTxs
				enrichedFromCache = true
			}
			if len(blk.Body) == 0 && len(cached.Body) > 0 {
				blk.Body = cached.Body
				enrichedFromCache = true
			}
			if blk.Header == nil && cached.Header != nil {
				blk.Header = cached.Header
				enrichedFromCache = true
			}
		}
	}
	if enrichedFromCache {
		source += "+cache_enrich"
	}
	if len(blk.Body) == 0 && len(blk.ShortTxs) > 0 {
		logs.Warn("[HandleGet] Serving block %s from %s without body (shortTxs=%d)",
			req.BlockId, source, len(blk.ShortTxs))
	}

	                                                                   
	                                                                                                  
	blkCopy, ok := proto.Clone(blk).(*pb.Block)
	if !ok || blkCopy == nil {
		http.Error(w, "Failed to clone block for marshal", http.StatusInternalServerError)
		return
	}
	if blkCopy.Header != nil {
		blkCopy.AccumulatedReward = hm.blockRewardAmount(blkCopy.Header.Height, blkCopy.AccumulatedReward)
	}
	resp := &pb.GetBlockResponse{Block: blkCopy}
	bytes, err := proto.Marshal(resp)
	if err != nil {
		detail := buildGetBlockMarshalDetail(req.BlockId, source, blkCopy, err)
		logs.Error("[HandleGet] Failed to marshal GetBlockResponse: %s", detail)
		http.Error(w, "Failed to marshal GetBlockResponse: "+detail, http.StatusInternalServerError)
		return
	}
	if !hm.authorizeArchiveDataRequest(r) {
		http.Error(w, "service peer contribution proof required", http.StatusForbidden)
		return
	}
	defer beginArchiveResponse(w)()
	                                                                    
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	w.Write(bytes)
}

func (hm *HandlerManager) acquireBlockByIDServe(r *http.Request, cached *pb.Block, acceptedHeight uint64) (func(), bool) {
	if hm.senderManager != nil && cached != nil && cached.Header != nil &&
		cached.Header.Height > acceptedHeight && !consensus.IsTooFarFutureHeight(cached.Header.Height, acceptedHeight) &&
		proto.Size(cached) <= pb.MaxTotalSize &&
		hm.isPrivilegedArchiveDataRequest(r) {
		return hm.senderManager.AcquireLiveBlockServe(extractIP(r))
	}
	return hm.acquireArchiveDataServe(r)
}
