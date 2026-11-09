package handlers

import (
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
)

const (
	defaultCertChainLimit              = 100
	maxCertChainLimit                  = 500
	defaultCertSummaryLimit            = 100
	maxCertSummaryLimit                = 200
	maxCertSummarySignatureReads       = 4096
	maxCertSignaturesPerCheckpointRead = 1024
)

type certEntry struct {
	Height uint64 `json:"height"`
	Data   []byte `json:"data"`
}

type certSummaryEntry struct {
	Height            uint64 `json:"height"`
	SignerCount       int32  `json:"signer_count"`
	SignerCountCapped bool   `json:"signer_count_capped,omitempty"`
	MinerCount        uint32 `json:"miner_count"`
	CommitteeSize     uint32 `json:"committee_size"`
	StateRoot         string `json:"state_root,omitempty"`
	Status            string `json:"status"`
}

func parseCertLimit(raw string, defaultLimit, maxLimit int) int {
	limit, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

func parseOptionalCertHeight(raw string) (uint64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseUint(raw, 10, 64)
}

                                                                     
                                                        
func (hm *HandlerManager) HandleGetCertChain(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, fromErr := strconv.ParseUint(q.Get("from"), 10, 64)
	to, toErr := strconv.ParseUint(q.Get("to"), 10, 64)
	if fromErr != nil || toErr != nil || to == 0 || to <= from {
		http.Error(w, "invalid range", http.StatusBadRequest)
		return
	}
	limit := parseCertLimit(q.Get("limit"), defaultCertChainLimit, maxCertChainLimit)

	                                                                   
	                                                                           
	startAfter := ""
	if from > 0 {
		startAfter = keys.KeyCertCertificate(from - 1)
	}
	rows, err := hm.dbManager.ScanKVWithLimitOrderedAfter(
		keys.KeyCertCertificatePrefix(), startAfter, limit+1, false,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	capacity := len(rows)
	if capacity > limit {
		capacity = limit
	}
	results := make([]certEntry, 0, capacity)
	hasMore := false
	for _, row := range rows {
		height := parseCertHeightFromKey(row.Key)
		if height < from {
			continue
		}
		if height > to {
			break
		}
		if len(results) == limit {
			hasMore = true
			break
		}
		results = append(results, certEntry{Height: height, Data: row.Value})
	}
	nextFrom := uint64(0)
	if hasMore && len(results) > 0 {
		nextFrom = results[len(results)-1].Height + 1
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"certs":     results,
		"count":     len(results),
		"has_more":  hasMore,
		"next_from": nextFrom,
	})
}

                                                                        
func (hm *HandlerManager) HandleGetCertificate(w http.ResponseWriter, r *http.Request) {
	height, err := strconv.ParseUint(r.URL.Query().Get("height"), 10, 64)
	if err != nil || height == 0 {
		http.Error(w, "invalid height", http.StatusBadRequest)
		return
	}

	data, err := hm.dbManager.Get(keys.KeyCertCertificate(height))
	if err != nil || len(data) == 0 {
		http.Error(w, "certificate not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(data)
}

func parseCertHeightFromKey(key string) uint64 {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '_' {
			height, _ := strconv.ParseUint(key[i+1:], 10, 64)
			return height
		}
	}
	return 0
}

                                                                          
                                                                               
func (hm *HandlerManager) HandleGetCertSummary(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := parseCertLimit(q.Get("limit"), defaultCertSummaryLimit, maxCertSummaryLimit)
	before, err := parseOptionalCertHeight(q.Get("before"))
	if err != nil {
		http.Error(w, "invalid before height", http.StatusBadRequest)
		return
	}

	certificateCursor := ""
	pendingCursor := ""
	if before > 0 {
		certificateCursor = keys.KeyCertCertificate(before)
		pendingCursor = keys.KeyCertPendingCheckpoint(before)
	}
	certificateRows, err := hm.dbManager.ScanKVWithLimitOrderedAfter(
		keys.KeyCertCertificatePrefix(), certificateCursor, limit+1, true,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pendingRows, err := hm.dbManager.ScanKVWithLimitOrderedAfter(
		keys.KeyCertPendingPrefix(), pendingCursor, limit+1, true,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	byHeight := make(map[uint64]certSummaryEntry, len(certificateRows)+len(pendingRows))
	for _, row := range certificateRows {
		var cert pb.CheckpointCertificate
		if proto.Unmarshal(row.Value, &cert) != nil {
			continue
		}
		height := parseCertHeightFromKey(row.Key)
		if height == 0 {
			height = cert.Height
		}
		stateRootHex := ""
		if len(cert.StateRoot) > 0 {
			stateRootHex = fmt.Sprintf("%016x", cert.StateRoot)
		}
		byHeight[height] = certSummaryEntry{
			Height: height, SignerCount: cert.SignerCount, MinerCount: cert.MinerCount,
			CommitteeSize: cert.CommitteeSize, StateRoot: stateRootHex, Status: "certified",
		}
	}

	                                                                         
	                                                                           
	signatureBudget := maxCertSummarySignatureReads
	for _, row := range pendingRows {
		var pending pb.CheckpointCertificate
		if proto.Unmarshal(row.Value, &pending) != nil {
			continue
		}
		height := parseCertHeightFromKey(row.Key)
		if height == 0 {
			height = pending.Height
		}
		if _, certified := byHeight[height]; certified {
			continue
		}
		if assembled, getErr := hm.dbManager.Get(keys.KeyCertCertificate(height)); getErr == nil && len(assembled) > 0 {
			continue
		}
		signerLimit := pending.CommitteeSize
		if signerLimit == 0 {
			signerLimit = pending.MinerCount
		}
		signerCount, capped, countErr := hm.countCertSummarySignatures(height, signerLimit, &signatureBudget)
		if countErr != nil {
			http.Error(w, countErr.Error(), http.StatusInternalServerError)
			return
		}
		stateRootHex := ""
		if len(pending.StateRoot) > 0 {
			stateRootHex = fmt.Sprintf("%016x", pending.StateRoot)
		}
		byHeight[height] = certSummaryEntry{
			Height: height, SignerCount: signerCount, SignerCountCapped: capped,
			MinerCount: pending.MinerCount, CommitteeSize: pending.CommitteeSize, StateRoot: stateRootHex, Status: "pending",
		}
	}

	results := make([]certSummaryEntry, 0, len(byHeight))
	for _, entry := range byHeight {
		results = append(results, entry)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Height > results[j].Height })
	hasMore := len(certificateRows) > limit || len(pendingRows) > limit || len(results) > limit
	if len(results) > limit {
		results = results[:limit]
	}
	nextBefore := uint64(0)
	if hasMore && len(results) > 0 {
		nextBefore = results[len(results)-1].Height
	}
	                                                                     
	sort.Slice(results, func(i, j int) bool { return results[i].Height < results[j].Height })

	var lastCheckpoint, lastAssembledCert uint64
	if data, getErr := hm.dbManager.Get(keys.KeyCertGlobal()); getErr == nil && len(data) > 0 {
		var global pb.CertGlobalState
		if proto.Unmarshal(data, &global) == nil {
			lastCheckpoint = global.LastCheckpoint
			lastAssembledCert = global.LastAssembledCert
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"certs":               results,
		"count":               len(results),
		"last_checkpoint":     lastCheckpoint,
		"last_assembled_cert": lastAssembledCert,
		"has_more":            hasMore,
		"next_before":         nextBefore,
	})
}

func (hm *HandlerManager) countCertSummarySignatures(height uint64, minerCount uint32, budget *int) (int32, bool, error) {
	if budget == nil || *budget <= 0 {
		return 0, true, nil
	}
	readLimit := int(minerCount)
	capped := false
	if readLimit <= 0 || readLimit > maxCertSignaturesPerCheckpointRead {
		readLimit = maxCertSignaturesPerCheckpointRead
		capped = minerCount > uint32(readLimit)
	}
	if readLimit > *budget {
		readLimit = *budget
		capped = true
	}
	rows, err := hm.dbManager.ScanKeysWithLimitOrdered(keys.KeyCertSignaturePrefix(height), readLimit, false)
	if err != nil {
		return 0, false, err
	}
	*budget -= len(rows)
	if minerCount == 0 && len(rows) == readLimit {
		capped = true
	}
	return int32(len(rows)), capped, nil
}
