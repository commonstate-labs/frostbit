package handlers

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"dex/frost/artifact"
	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

                                                                                                 
func (hm *HandlerManager) HandleFrostArtifactHead(w http.ResponseWriter, r *http.Request) {
	chain := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("chain")))
	vault, err := strconv.ParseUint(r.URL.Query().Get("vault_id"), 10, 32)
	if err != nil || (chain != "eth" && chain != "bnb" && chain != "base" && chain != "arb" && chain != "trx") {
		http.Error(w, "invalid account chain or vault_id", http.StatusBadRequest)
		return
	}
	                                                                      
	config, err := hm.dbManager.Get(keys.KeyFrostVaultConfig(chain, 0))
	if err != nil || len(config) == 0 {
		http.Error(w, "vault config unavailable", http.StatusNotFound)
		return
	}
	var cfg pb.FrostVaultConfig
	if proto.Unmarshal(config, &cfg) != nil || vault >= uint64(cfg.VaultCount) {
		http.Error(w, "invalid vault config", http.StatusNotFound)
		return
	}
	rawState, err := hm.dbManager.Get(keys.KeyFrostVaultState(chain, uint32(vault)))
	var state pb.FrostVaultState
	if err != nil || len(rawState) == 0 || proto.Unmarshal(rawState, &state) != nil || state.VaultId != uint32(vault) || !strings.EqualFold(state.Chain, chain) {
		http.Error(w, "vault state unavailable", http.StatusServiceUnavailable)
		return
	}
	head, err := artifact.ResolveAccountMigrationHead(hm.frostResultGet, chain, uint32(vault), state.KeyEpoch)
	if err != nil {
		http.Error(w, "artifact head unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"artifact_id": head})
}

                                                                           
                                                                                 
func (hm *HandlerManager) frostPermanentOrLegacy(permanent, legacy string) ([]byte, bool, error) {
	raw, found, err := hm.frostResultGet(permanent)
	if err != nil || found {
		return raw, found, err
	}
	return hm.frostResultGet(legacy)
}

                                                                               
func (hm *HandlerManager) HandleFrostArtifact(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("metadata_only")
	if mode != "" && mode != "1" {
		http.Error(w, "invalid metadata_only", http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("artifact_id"))
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 32 {
		http.Error(w, "invalid artifact_id", http.StatusBadRequest)
		return
	}
	metaRaw, err := hm.dbManager.Get(keys.KeyFrostArtifactMeta(id))
	if err != nil || len(metaRaw) == 0 {
		http.Error(w, "artifact metadata not found", http.StatusNotFound)
		return
	}
	if mode == "1" {
		meta, err := artifact.UnmarshalMetadata(metaRaw)
		if err != nil || meta.Version != artifact.Version || meta.ArtifactID != id {
			http.Error(w, "invalid permanent artifact metadata", http.StatusInternalServerError)
			return
		}
		                                                                       
		                                                                     
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(meta)
		return
	}
	record, err := artifact.ReadRecord(hm.frostResultGet, id, maxFrostMigrationBundleRaw*2)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, artifact.ErrRecordUnavailable) {
			status = http.StatusGone
		} else if errors.Is(err, artifact.ErrRecordTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(record)
}
