package handlers

import (
	"encoding/json"
	"io"
	"net/http"

	"dex/pb"
	"dex/types"
)

                                     
type RoundChitsEntry struct {
	Round     int         `json:"round"`
	Timestamp int64       `json:"timestamp"`
	Votes     []ChitEntry `json:"votes"`
}

                                      
type GetChitsResponse struct {
	Height      uint64            `json:"height"`
	BlockID     string            `json:"block_id,omitempty"`
	TotalVotes  int               `json:"total_votes"`
	TotalRounds int               `json:"total_rounds"`
	Rounds      []RoundChitsEntry `json:"rounds,omitempty"`
	FinalizedAt int64             `json:"finalized_at,omitempty"`
	Chits       []ChitEntry       `json:"chits,omitempty"`
	Error       string            `json:"error,omitempty"`
}

                               
type ChitEntry struct {
	NodeID       string `json:"node_id"`
	PreferredID  string `json:"preferred_id"`
	Timestamp    int64  `json:"timestamp"`
	SampleWeight int    `json:"sample_weight"`
}

                                                               
func (hm *HandlerManager) HandleGetChits(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleGetChits")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeChitsError(w, "Failed to read request body")
		return
	}

	                                                                      
	var req pb.GetBlockRequest
	if err := pb.UnmarshalSanitize(bodyBytes, &req); err != nil {
		writeChitsError(w, "Invalid request proto")
		return
	}

	if hm.consensusManager == nil {
		writeChitsError(w, "Consensus manager not available")
		return
	}

	                                           
	chits := hm.consensusManager.GetFinalizationChits(req.Height)
	if chits == nil {
		resp := GetChitsResponse{
			Height: req.Height,
			Error:  "Finalization chits not found for this height",
		}
		writeChitsJSON(w, resp)
		return
	}

	                                       
	resp := GetChitsResponse{
		Height:      chits.Height,
		BlockID:     chits.BlockID,
		TotalVotes:  chits.TotalVotes,
		TotalRounds: chits.TotalRounds,
		FinalizedAt: chits.ObservedAt(),
		Rounds:      make([]RoundChitsEntry, 0, len(chits.Rounds)),
		Chits:       make([]ChitEntry, 0, len(chits.Chits)),
	}

	                     
	for _, round := range chits.Rounds {
		roundEntry := RoundChitsEntry{
			Round:     round.Round,
			Timestamp: round.Timestamp,
			Votes:     make([]ChitEntry, 0, len(round.Votes)),
		}
		for _, v := range round.Votes {
			roundEntry.Votes = append(roundEntry.Votes, finalizationChitEntry(v))
		}
		resp.Rounds = append(resp.Rounds, roundEntry)
	}

	                                                 
	for _, c := range chits.Chits {
		resp.Chits = append(resp.Chits, finalizationChitEntry(c))
	}

	writeChitsJSON(w, resp)
}

func finalizationChitEntry(chit types.FinalizationChit) ChitEntry {
	return ChitEntry{
		NodeID:       chit.NodeID,
		PreferredID:  chit.PreferredID,
		Timestamp:    chit.Timestamp,
		SampleWeight: chit.EffectiveSampleWeight(),
	}
}

func writeChitsJSON(w http.ResponseWriter, resp GetChitsResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func writeChitsError(w http.ResponseWriter, errMsg string) {
	resp := GetChitsResponse{Error: errMsg}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(resp)
}
