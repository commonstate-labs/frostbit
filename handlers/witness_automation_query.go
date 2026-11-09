package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

                                                                              
                                                                               
func (hm *HandlerManager) handleActiveWitnessRequests(w http.ResponseWriter, r *http.Request) {
	limit := boundedPositiveInt(r.URL.Query().Get("limit"), 100, 500)
	after := strings.TrimSpace(r.URL.Query().Get("after"))
	start := ""
	if after != "" {
		if len(after) > 256 {
			http.Error(w, "invalid after cursor", http.StatusBadRequest)
			return
		}
		raw, _, err := hm.frostResultGet(keys.KeyRechargeRequest(after))
		if err != nil {
			http.Error(w, "cursor lookup failed", 500)
			return
		}
		var req pb.RechargeRequest
		if len(raw) == 0 || proto.Unmarshal(raw, &req) != nil || req.RequestId != after {
			http.Error(w, "unknown active request cursor", http.StatusBadRequest)
			return
		}
		start = keys.KeyRechargeQueryRecentGlobal(false, req.CreateHeight, after)
	}
	rows, err := hm.scanRechargeQueryRefs("", false, 0, false, start, limit, false)
	if err != nil {
		http.Error(w, "active request query unavailable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	requests := make([]*pb.RechargeRequest, 0, len(rows))
	for _, row := range rows {
		requests = append(requests, row.request)
	}
	hm.writeRechargeRequestList(w, requests...)
}

func (hm *HandlerManager) handleAssignedWitnessChallenges(w http.ResponseWriter, r *http.Request) {
	address := strings.TrimSpace(r.URL.Query().Get("arbitrator"))
	after := strings.TrimSpace(r.URL.Query().Get("after"))
	if address == "" || len(address) > 256 || len(after) > 256 {
		http.Error(w, "invalid arbitrator/cursor", 400)
		return
	}
	limit := boundedPositiveInt(r.URL.Query().Get("limit"), 100, 500)
	start := ""
	if after != "" {
		start = keys.KeyArbitratorChallenge(address, after)
	}
	rows, err := hm.dbManager.ScanKVWithLimitOrderedAfter(keys.KeyArbitratorChallengePrefix(address), start, limit, false)
	if err != nil {
		http.Error(w, "arbitration assignment scan failed", 500)
		return
	}
	response := &pb.ChallengeRecordList{}
	for _, row := range rows {
		id := string(row.Value)
		raw, err := hm.dbManager.Get(keys.KeyChallengeRecord(id))
		if err != nil {
			http.Error(w, "challenge read failed", 500)
			return
		}
		var ch pb.ChallengeRecord
		if len(raw) == 0 || proto.Unmarshal(raw, &ch) != nil || ch.ChallengeId != id {
			http.Error(w, fmt.Sprintf("invalid arbitration assignment %s", id), 500)
			return
		}
		response.Records = append(response.Records, &ch)
	}
	data, err := proto.Marshal(response)
	if err != nil {
		http.Error(w, "encode arbitration assignments failed", 500)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(data)
}
