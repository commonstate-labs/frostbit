package handlers

import (
	"dex/db"
	"dex/rent"
	statedb "dex/stateDB"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cockroachdb/pebble"
)

type rentDBStore struct{ db *db.Manager }

type accountAssetResponse struct {
	*rent.AssetSummary
	Rent *rent.AccountRentStatus `json:"rent,omitempty"`
}

func (s rentDBStore) Get(key string) ([]byte, bool, error) {
	if s.db == nil {
		return nil, false, nil
	}
	data, err := s.db.Get(key)
	if errors.Is(err, statedb.ErrNotFound) || errors.Is(err, pebble.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (s rentDBStore) Scan(prefix string, limit int) (map[string][]byte, error) {
	out := make(map[string][]byte)
	if s.db == nil {
		return out, nil
	}
	kvs, err := s.db.ScanByPrefix(prefix, limit)
	if err != nil {
		return nil, err
	}
	for k, v := range kvs {
		out[k] = []byte(v)
	}
	return out, nil
}

func (hm *HandlerManager) HandleAccountAsset(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleAccountAsset")
	address := r.URL.Query().Get("address")
	if address == "" {
		writeAccountAssetJSON(w, map[string]string{"error": "address is required"})
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		token = rent.FeeToken
	}
	svc := rent.NewService(rentDBStore{db: hm.dbManager})
	summary, err := svc.NominalAsset(address, token)
	if err != nil {
		writeAccountAssetJSON(w, map[string]string{"error": err.Error()})
		return
	}
	var rentStatus *rent.AccountRentStatus
	if token == rent.FeeToken {
		currentHeight := uint64(0)
		if hm.dbManager != nil {
			currentHeight = hm.dbManager.GetCurrentHeight()
		}
		status, err := svc.RentStatus(address, currentHeight)
		if err != nil {
			writeAccountAssetJSON(w, map[string]string{"error": err.Error()})
			return
		}
		rentStatus = status
	}
	writeAccountAssetJSON(w, accountAssetResponse{AssetSummary: summary, Rent: rentStatus})
}

func writeAccountAssetJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}
