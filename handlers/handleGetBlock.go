package handlers

import (
	"compress/gzip"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"fmt"
	"net/http"

	"google.golang.org/protobuf/proto"
)

                           
func (hm *HandlerManager) HandleGetBlock(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleGetBlock")
	if !hm.allowArchiveDataRequest(r) {
		http.Error(w, "verified service peer or registered miner required", http.StatusForbidden)
		return
	}
	bodyBytes, err := readArchiveRequestBody(w, r)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	var req pb.GetBlockRequest
	if err := pb.UnmarshalSanitize(bodyBytes, &req); err != nil {
		http.Error(w, "Invalid GetBlockRequest proto", http.StatusBadRequest)
		return
	}
	release, ok := hm.acquireArchiveDataServe(r)
	if !ok {
		http.Error(w, "block sync service capacity exhausted", http.StatusServiceUnavailable)
		return
	}
	defer release()

	var block *pb.Block
	source := "unknown"
	finalizedAttempted := false
	if hm.consensusManager != nil {
		if blockID, ok := hm.consensusManager.GetFinalizedBlockID(req.Height); ok {
			finalizedAttempted = true
			b, err := hm.dbManager.GetBlockByID(blockID)
			if err == nil && b != nil {
				block = b
				source = "finalized_db"
			} else {
				clientIdent := r.Header.Get("X-Node-Address")
				logs.Warn("[HandleGetBlock] Finalized block %s at height %d not found in DB. X-Node-Address: %q, RemoteAddr: %q, err: %v",
					blockID, req.Height, clientIdent, r.RemoteAddr, err)
			}
		} else {
			logs.Warn("[HandleGetBlock] No finalized block at height %d; fallback to non-finalized", req.Height)
		}
	}

	if block == nil && !finalizedAttempted {
		if blocksAtHeight, err := hm.dbManager.GetBlocksByHeight(req.Height); err == nil && len(blocksAtHeight) > 1 {
			logs.Warn("[HandleGetBlock] Multiple blocks at height %d (count=%d); returning first",
				req.Height, len(blocksAtHeight))
		}
		b, err := hm.dbManager.GetBlock(req.Height)
		if err == nil && b != nil {
			block = b
			source = "height_fallback_db"
		}
	}

	if block == nil {
		resp := &pb.GetBlockResponse{
			Error: fmt.Sprintf("Block not found at height %d", req.Height),
		}
		respBytes, err := proto.Marshal(resp)
		if err != nil {
			logs.Error("[HandleGetBlock] Failed to marshal not-found response: height=%d err=%v", req.Height, err)
			http.Error(w, fmt.Sprintf("Failed to marshal GetBlockResponse: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusNotFound)
		w.Write(respBytes)
		return
	}

	respBlock := hm.cloneBlockWithReward(block)
	if respBlock == nil {
		http.Error(w, "Failed to clone block for marshal", http.StatusInternalServerError)
		return
	}
	resp := &pb.GetBlockResponse{Block: respBlock}
	if req.IncludeReadModel {
		data, readErr := hm.dbManager.Get(keys.KeyBlockReadModel(block.BlockHash))
		if readErr != nil {
			logs.Error("[HandleGetBlock] Failed to load read model: height=%d block=%s err=%v", req.Height, block.BlockHash, readErr)
			http.Error(w, "Failed to load finalized read model", http.StatusInternalServerError)
			return
		}
		if len(data) > 0 {
			var model pb.BlockReadModel
			if unmarshalErr := proto.Unmarshal(data, &model); unmarshalErr != nil {
				logs.Error("[HandleGetBlock] Corrupt read model: height=%d block=%s err=%v", req.Height, block.BlockHash, unmarshalErr)
				http.Error(w, "Corrupt finalized read model", http.StatusInternalServerError)
				return
			}
			if verifyErr := pb.VerifyBlockReadModel(&model, block.Header.Height, block.BlockHash); verifyErr != nil {
				logs.Error("[HandleGetBlock] Invalid read model: height=%d block=%s err=%v", req.Height, block.BlockHash, verifyErr)
				http.Error(w, "Invalid finalized read model", http.StatusInternalServerError)
				return
			}
			resp.ReadModel = &model
		}
	}
	respBytes, err := proto.Marshal(resp)
	if err != nil {
		detail := buildGetBlockMarshalDetail(block.BlockHash, source, respBlock, err)
		logs.Error("[HandleGetBlock] Failed to marshal GetBlockResponse: %s", detail)
		http.Error(w, "Failed to marshal GetBlockResponse: "+detail, http.StatusInternalServerError)
		return
	}
	if !hm.authorizeArchiveDataRequest(r) {
		http.Error(w, "service peer contribution proof required", http.StatusForbidden)
		return
	}

	defer beginArchiveResponse(w)()
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)

	gz := gzip.NewWriter(w)
	defer gz.Close()
	gz.Write(respBytes)
}
