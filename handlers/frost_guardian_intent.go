package handlers

import (
	"encoding/json"
	"net/http"

	"dex/frost/guardian"
	frostruntime "dex/frost/runtime"
	"dex/frost/runtime/adapters"
)

                                                                               
                                                                              
func (hm *HandlerManager) HandleFrostGuardianIntent(w http.ResponseWriter, r *http.Request) {
	request, err := guardian.DecodeRequest(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if hm.dbManager == nil {
		http.Error(w, "finalized state unavailable", http.StatusServiceUnavailable)
		return
	}
	intent, err := frostruntime.ResolveGuardianIntent(adapters.NewStateDBReader(hm.dbManager), request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(intent)
}
