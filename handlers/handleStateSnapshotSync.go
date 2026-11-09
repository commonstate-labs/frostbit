package handlers

import (
	"bytes"
	statedb "dex/stateDB"
	"dex/types"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

const archiveCheckpointChunkBufferLimit int64 = statedb.CheckpointChunkSize

func (hm *HandlerManager) HandleStateSnapshotShards(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleStateSnapshotShards")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !hm.allowArchiveDataRequest(r) {
		http.Error(w, "verified service peer or registered miner required", http.StatusForbidden)
		return
	}
	var req types.StateSnapshotShardsRequest
	if err := decodeArchiveJSONRequest(w, r, &req, true); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	release, ok := hm.acquireArchiveDataServe(r)
	if !ok {
		http.Error(w, "block sync service capacity exhausted", http.StatusServiceUnavailable)
		return
	}
	defer release()

	targetHeight, _ := hm.resolveStateSnapshotHeight(req.TargetHeight)

	shards, fps, err := hm.dbManager.LoadStateSnapshotShardMetadata(targetHeight)
	if err != nil {
		writeStateSnapshotShardsError(w, targetHeight, err)
		return
	}

	fpByShard := make(map[string]statedb.ShardFingerprint, len(fps))
	for _, fp := range fps {
		fpByShard[fp.Shard] = fp
	}

	resp := types.StateSnapshotShardsResponse{
		SnapshotHeight: targetHeight,
		Shards:         make([]types.StateSnapshotShard, 0, len(shards)),
	}
	for _, sh := range shards {
		item := types.StateSnapshotShard{Shard: sh.Shard, Count: sh.Count}
		if fp, ok := fpByShard[sh.Shard]; ok {
			item.XorHash = fp.XorHash
			item.TotalKeyBytes = fp.TotalKeyBytes
			item.TotalValueBytes = fp.TotalValueBytes
			item.XorDigest = append([]byte(nil), fp.XorDigest[:]...)
		}
		resp.Shards = append(resp.Shards, item)
	}

	if !hm.authorizeArchiveDataRequest(r) {
		http.Error(w, "service peer contribution proof required", http.StatusForbidden)
		return
	}
	defer beginArchiveResponse(w)()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (hm *HandlerManager) HandleStateCheckpointFiles(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleStateCheckpointFiles")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !hm.allowArchiveDataRequest(r) {
		http.Error(w, "verified service peer or registered miner required", http.StatusForbidden)
		return
	}
	var req types.StateCheckpointFilesRequest
	if err := decodeArchiveJSONRequest(w, r, &req, false); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	release, ok := hm.acquireArchiveDataServe(r)
	if !ok {
		http.Error(w, "block sync service capacity exhausted", http.StatusServiceUnavailable)
		return
	}
	defer release()

	targetHeight, _ := hm.resolveStateSnapshotHeight(req.TargetHeight)
	if targetHeight == 0 {
		writeStateCheckpointFilesError(w, targetHeight, "checkpoint height must be > 0")
		return
	}

	files, totalBytes, err := hm.dbManager.ListStateCheckpointFiles(targetHeight)
	if err != nil {
		writeStateCheckpointFilesError(w, targetHeight, err.Error())
		return
	}
	resp := types.StateCheckpointFilesResponse{
		SnapshotHeight: targetHeight,
		ChunkSize:      statedb.CheckpointChunkSize,
		TotalBytes:     totalBytes,
		Files:          make([]types.StateCheckpointFile, 0, len(files)),
	}
	for _, f := range files {
		chunks := int64(0)
		if f.Size > 0 {
			chunks = (f.Size + statedb.CheckpointChunkSize - 1) / statedb.CheckpointChunkSize
		}
		resp.Files = append(resp.Files, types.StateCheckpointFile{Name: f.Name, Size: f.Size, Chunks: chunks})
	}
	if !hm.authorizeArchiveDataRequest(r) {
		http.Error(w, "service peer contribution proof required", http.StatusForbidden)
		return
	}
	defer beginArchiveResponse(w)()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (hm *HandlerManager) HandleStateCheckpointChunk(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleStateCheckpointChunk")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !hm.allowArchiveDataRequest(r) {
		http.Error(w, "verified service peer or registered miner required", http.StatusForbidden)
		return
	}
	var req types.StateCheckpointChunkRequest
	if err := decodeArchiveJSONRequest(w, r, &req, false); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.TargetHeight == 0 {
		http.Error(w, "checkpoint height must be > 0", http.StatusBadRequest)
		return
	}
	if req.Limit <= 0 || req.Limit > statedb.CheckpointChunkSize || req.Limit > archiveCheckpointChunkBufferLimit || req.Offset < 0 {
		http.Error(w, "invalid checkpoint chunk range", http.StatusBadRequest)
		return
	}
	release, ok := hm.acquireArchiveDataServe(r)
	if !ok {
		http.Error(w, "block sync service capacity exhausted", http.StatusServiceUnavailable)
		return
	}
	defer release()

	targetHeight, _ := hm.resolveStateSnapshotHeight(req.TargetHeight)
	if targetHeight != req.TargetHeight {
		http.Error(w, "checkpoint height unavailable", http.StatusNotFound)
		return
	}

	chunk := &boundedArchiveChunkBuffer{limit: req.Limit}
	written, err := hm.dbManager.WriteStateCheckpointChunk(targetHeight, req.Name, req.Offset, req.Limit, chunk)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if written <= 0 {
		http.Error(w, "checkpoint chunk is empty", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	if written != int64(chunk.Len()) {
		http.Error(w, "checkpoint chunk write count mismatch", http.StatusInternalServerError)
		return
	}
	if !hm.authorizeArchiveDataRequest(r) {
		http.Error(w, "service peer contribution proof required", http.StatusForbidden)
		return
	}

	defer beginArchiveResponse(w)()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-State-Snapshot-Height", strconv.FormatUint(targetHeight, 10))
	w.Header().Set("X-State-Checkpoint-File", req.Name)
	w.Header().Set("X-State-Checkpoint-Offset", strconv.FormatInt(req.Offset, 10))
	_, _ = w.Write(chunk.Bytes())
}

type boundedArchiveChunkBuffer struct {
	bytes.Buffer
	limit int64
}

func (b *boundedArchiveChunkBuffer) Write(payload []byte) (int, error) {
	if b == nil {
		return 0, fmt.Errorf("nil checkpoint chunk buffer")
	}
	if b.limit < 0 || int64(b.Len()) > b.limit || int64(len(payload)) > b.limit-int64(b.Len()) {
		return 0, fmt.Errorf("checkpoint chunk exceeds %d-byte buffer limit", b.limit)
	}
	return b.Buffer.Write(payload)
}

func (hm *HandlerManager) resolveStateSnapshotHeight(targetHeight uint64) (uint64, bool) {
	if hm.consensusManager == nil {
		return targetHeight, targetHeight > 0
	}
	_, accepted := hm.consensusManager.GetLastAccepted()
	if targetHeight == 0 || targetHeight > accepted {
		targetHeight = accepted
	}
	return targetHeight, targetHeight > 0 && targetHeight == accepted
}

func writeStateSnapshotShardsError(w http.ResponseWriter, snapshotHeight uint64, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(types.StateSnapshotShardsResponse{
		SnapshotHeight: snapshotHeight,
		Error:          err.Error(),
	})
}

func writeStateCheckpointFilesError(w http.ResponseWriter, snapshotHeight uint64, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(types.StateCheckpointFilesResponse{
		SnapshotHeight: snapshotHeight,
		Error:          msg,
	})
}
