package handlers

import (
	"dex/keys"
	"dex/pb"
	"io"
	"net/http"
	"sort"

	"google.golang.org/protobuf/proto"
)

func (hm *HandlerManager) HandleDiscoveryMinerIPMap(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleDiscoveryMinerIPMap")

	req := &pb.GetMinerIPMapRequest{}
	if body, err := io.ReadAll(r.Body); err == nil && len(body) > 0 {
		if err := pb.UnmarshalSanitize(body, req); err != nil {
			http.Error(w, "Invalid discovery request", http.StatusBadRequest)
			return
		}
	}

	if hm.dbManager == nil {
		http.Error(w, "DB unavailable", http.StatusServiceUnavailable)
		return
	}

	height := uint64(0)
	if hm.consensusManager != nil {
		_, height = hm.consensusManager.GetLastAccepted()
	} else if h, err := hm.dbManager.GetLatestBlockHeight(); err == nil {
		height = h
	}

	resp := &pb.GetMinerIPMapResponse{Height: height}
	if req.GetMinHeight() > 0 && height <= req.GetMinHeight() {
		data, _ := proto.Marshal(resp)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		w.Write(data)
		return
	}

	kv, err := hm.dbManager.Scan(keys.KeyAccount(""))
	if err != nil {
		http.Error(w, "Failed to scan miner accounts", http.StatusInternalServerError)
		return
	}

	entries := make([]*pb.MinerIPEntry, 0, len(kv))
	for _, raw := range kv {
		acct := &pb.Account{}
		if err := proto.Unmarshal(raw, acct); err != nil {
			continue
		}
		if acct.Address == "" || !acct.IsMiner {
			continue
		}
		entries = append(entries, &pb.MinerIPEntry{
			Address: acct.Address,
			Ip:      acct.Ip,
			Index:   acct.Index,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Index != entries[j].Index {
			return entries[i].Index < entries[j].Index
		}
		if entries[i].Address != entries[j].Address {
			return entries[i].Address < entries[j].Address
		}
		return entries[i].Ip < entries[j].Ip
	})
	resp.Entries = entries

	data, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "Failed to marshal discovery response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
