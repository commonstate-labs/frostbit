package main

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/pb"

	"github.com/btcsuite/btcd/wire"
)

type frostMigrationBTCInput struct {
	TxID         string `json:"txid"`
	Vout         uint32 `json:"vout"`
	Amount       string `json:"amount"`
	Sequence     uint32 `json:"sequence"`
	ScriptPubKey string `json:"script_pubkey,omitempty"`
}

type frostMigrationBTCOutput struct {
	Address string `json:"address"`
	Amount  string `json:"amount"`
}

type frostMigrationBTCDetail struct {
	Version     int32                     `json:"version"`
	LockTime    uint32                    `json:"lock_time"`
	Network     string                    `json:"network"`
	Inputs      []frostMigrationBTCInput  `json:"inputs"`
	Outputs     []frostMigrationBTCOutput `json:"outputs"`
	TotalInput  string                    `json:"total_input"`
	TotalOutput string                    `json:"total_output"`
	Fee         string                    `json:"fee"`
}

type frostMigrationAccountDetail struct {
	AuthorityProtocol  uint32 `json:"authority_protocol,omitempty"`
	DexChainID         string `json:"dex_chain_id,omitempty"`
	FrozenNextBatchSeq string `json:"frozen_next_batch_seq,omitempty"`
	FromEpoch          string `json:"from_epoch,omitempty"`
	ToEpoch            string `json:"to_epoch,omitempty"`
	Kind               string `json:"kind"`
	ChainID            string `json:"chain_id"`
	ContractAddress    string `json:"contract_address"`
	Method             string `json:"method"`
	OldPubkeyX         string `json:"old_pubkey_x"`
	OldPubkeyY         string `json:"old_pubkey_y"`
	NewPubkeyX         string `json:"new_pubkey_x"`
	NewPubkeyY         string `json:"new_pubkey_y"`
	NextEpoch          uint64 `json:"next_epoch"`
	SignatureRX        string `json:"signature_rx"`
	SignatureRY        string `json:"signature_ry"`
	SignatureS         string `json:"signature_s"`
	GuardianRX         string `json:"guardian_rx"`
	GuardianRY         string `json:"guardian_ry"`
	GuardianS          string `json:"guardian_s"`
	Calldata           string `json:"calldata"`
}

type frostMigrationDetailResponse struct {
	BTCProtocolVersion uint32                       `json:"btc_protocol_version,omitempty"`
	ParentMigrationID  string                       `json:"parent_migration_id,omitempty"`
	ChildCount         string                       `json:"child_count,omitempty"`
	SignedChildCount   string                       `json:"signed_child_count,omitempty"`
	ManifestHash       string                       `json:"manifest_hash,omitempty"`
	TotalInput         string                       `json:"total_input,omitempty"`
	TotalOutput        string                       `json:"total_output,omitempty"`
	TotalFee           string                       `json:"total_fee,omitempty"`
	MigrationID        string                       `json:"migration_id"`
	Chain              string                       `json:"chain"`
	VaultID            uint32                       `json:"vault_id"`
	OldEpoch           uint64                       `json:"old_epoch"`
	NewEpoch           uint64                       `json:"new_epoch"`
	Status             string                       `json:"status"`
	IntentHeight       uint64                       `json:"intent_height"`
	SignedHeight       uint64                       `json:"signed_height"`
	NoFunds            bool                         `json:"no_funds"`
	TemplateHash       string                       `json:"template_hash"`
	TxID               string                       `json:"txid,omitempty"`
	RawTxHex           string                       `json:"raw_tx_hex,omitempty"`
	RawSize            int                          `json:"raw_size_bytes"`
	BTC                *frostMigrationBTCDetail     `json:"btc,omitempty"`
	Account            *frostMigrationAccountDetail `json:"account,omitempty"`
}

func addSatoshis(total *uint64, amount uint64) error {
	if ^uint64(0)-*total < amount {
		return fmt.Errorf("BTC amount overflow")
	}
	*total += amount
	return nil
}

func migrationBTCTxID(raw []byte) string {
	var tx wire.MsgTx
	if len(raw) == 0 || tx.Deserialize(bytes.NewReader(raw)) != nil {
		return ""
	}
	return tx.TxHash().String()
}

func decimalWord(raw []byte) string {
	return new(big.Int).SetBytes(raw).String()
}

func accountMigrationKind(chainName string) string {
	if strings.EqualFold(strings.TrimSpace(chainName), "TRX") {
		return "tron"
	}
	return "evm"
}

                                                                           
                                                                           
                                                                              
func (s *server) handleFrostMigrationDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONStatus(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	node := s.pickNode(r.URL.Query().Get("node"))
	if node == "" {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "missing or disallowed node parameter"})
		return
	}
	migrationID := strings.TrimSpace(r.URL.Query().Get("migration_id"))
	if migrationID == "" || len(migrationID) > 256 {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid migration_id"})
		return
	}

	var archive pb.FrostVaultMigrationArchive
	path := "/frost/vault/migration?migration_id=" + url.QueryEscape(migrationID)
	if err := s.fetchProto(r.Context(), node, path, nil, &archive); err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "failed to fetch migration detail: " + err.Error()})
		return
	}
	state := archive.State
	if state == nil || state.MigrationId != migrationID {
		writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "node returned a mismatched migration record"})
		return
	}

	response := frostMigrationDetailResponse{
		BTCProtocolVersion: state.BtcProtocolVersion,
		ParentMigrationID:  state.ParentMigrationId,
		MigrationID:        migrationID,
		Chain:              strings.ToUpper(strings.TrimSpace(state.Chain)),
		VaultID:            state.VaultId,
		OldEpoch:           state.OldEpoch,
		NewEpoch:           state.NewEpoch,
		Status:             state.Status,
		IntentHeight:       state.IntentHeight,
		SignedHeight:       archive.SignedHeight,
		NoFunds:            state.NoFunds,
		TemplateHash:       hex.EncodeToString(state.TemplateHash),
		RawTxHex:           hex.EncodeToString(archive.RawTx),
		RawSize:            len(archive.RawTx),
	}
	if state.BtcProtocolVersion == 3 && state.ParentMigrationId == "" {
		response.ChildCount = strconv.FormatUint(state.BtcChildCount, 10)
		response.SignedChildCount = strconv.FormatUint(state.BtcSignedChildCount, 10)
		response.ManifestHash = hex.EncodeToString(state.BtcManifestHash)
		response.TotalInput, response.TotalOutput, response.TotalFee = state.BtcTotalInputAmount, state.BtcTotalOutputAmount, state.BtcTotalFeeAmount
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, response)
		return
	}

	if response.Chain == "BTC" && !state.NoFunds {
		template, err := btc.FromJSON(state.TemplateData)
		if err != nil || !bytes.Equal(template.TemplateHash(), state.TemplateHash) {
			writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "invalid archived BTC migration template"})
			return
		}
		if len(archive.RawTx) > 0 && len(archive.GuardianSigs) != len(archive.InputSigs)+1 {
			writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "missing guardian migration signatures"})
			return
		}
		var rebuilt []byte
		if len(archive.RawTx) > 0 {
			rebuilt, err = template.BuildSignedRawTx(archive.InputSigs, archive.GuardianSigs[:len(archive.InputSigs)])
		}
		if len(archive.RawTx) > 0 && (err != nil || !bytes.Equal(rebuilt, archive.RawTx)) {
			writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "BTC migration transaction does not match its consensus template"})
			return
		}

		var totalInput, totalOutput uint64
		inputs := make([]frostMigrationBTCInput, len(template.Inputs))
		for i, input := range template.Inputs {
			if err := addSatoshis(&totalInput, input.Amount); err != nil {
				writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			inputs[i] = frostMigrationBTCInput{
				TxID: input.TxID, Vout: input.Vout, Amount: strconv.FormatUint(input.Amount, 10), Sequence: input.Sequence,
			}
			if i < len(archive.ScriptPubkeys) {
				inputs[i].ScriptPubKey = hex.EncodeToString(archive.ScriptPubkeys[i])
			}
		}
		outputs := make([]frostMigrationBTCOutput, len(template.Outputs))
		for i, output := range template.Outputs {
			if err := addSatoshis(&totalOutput, output.Amount); err != nil {
				writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			outputs[i] = frostMigrationBTCOutput{Address: output.Address, Amount: strconv.FormatUint(output.Amount, 10)}
		}
		if totalOutput > totalInput {
			writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "BTC migration outputs exceed inputs"})
			return
		}

		var cfg pb.FrostVaultConfig
		if err := s.fetchProto(r.Context(), node, "/frost/vault/config?chain=btc", nil, &cfg); err != nil || strings.TrimSpace(cfg.BtcNetwork) == "" {
			writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "failed to load BTC migration network"})
			return
		}

		response.TxID = migrationBTCTxID(archive.RawTx)
		if len(archive.RawTx) > 0 && response.TxID == "" {
			writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "invalid archived BTC migration transaction"})
			return
		}
		response.BTC = &frostMigrationBTCDetail{
			Version:     template.Version,
			LockTime:    template.LockTime,
			Network:     strings.TrimSpace(cfg.BtcNetwork),
			Inputs:      inputs,
			Outputs:     outputs,
			TotalInput:  strconv.FormatUint(totalInput, 10),
			TotalOutput: strconv.FormatUint(totalOutput, 10),
			Fee:         strconv.FormatUint(totalInput-totalOutput, 10),
		}
	} else if response.Chain == "ETH" || response.Chain == "BNB" || response.Chain == "BASE" || response.Chain == "ARB" || response.Chain == "TRX" {
		template, err := evm.FromJSON(state.TemplateData)
		if err != nil || len(state.OldGroupPubkey) != 64 || len(state.NewGroupPubkey) != 64 || len(archive.Signature) != evm.SchnorrSigWithYSize {
			writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "invalid archived account migration template"})
			return
		}
		cfg := &pb.FrostVaultConfig{}
		if state.AuthorityProtocol != 3 || len(archive.GuardianSigs) != 1 {
			writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "missing dual custody migration proof"})
			return
		}
		if err := s.fetchProto(r.Context(), node, "/frost/vault/config?chain="+url.QueryEscape(strings.ToLower(response.Chain)), nil, cfg); err != nil || !strings.EqualFold(cfg.Chain, response.Chain) {
			writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "failed to load V3 migration configuration"})
			return
		}
		canonical, _, err := evm.CanonicalMigrationTemplate(cfg, state)
		if err != nil || !bytes.Equal(canonical.TemplateHash, state.TemplateHash) ||
			!bytes.Equal(canonical.TemplateData, state.TemplateData) || len(canonical.SigHashes) != 1 ||
			!bytes.Equal(canonical.SigHashes[0], state.IntentHash) {
			writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "account migration template does not match consensus intent"})
			return
		}
		adapter := evm.NewEVMAdapter(response.Chain, cfg.EvmChainId)
		valid, verifyErr := adapter.VerifySignature(state.OldGroupPubkey, state.IntentHash, archive.Signature)
		guardianValid, guardianErr := adapter.VerifySignature(cfg.GuardianPubkey, state.IntentHash, archive.GuardianSigs[0])
		if verifyErr != nil || !valid || guardianErr != nil || !guardianValid {
			writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "invalid dual custody migration signatures"})
			return
		}
		method := "withdrawBatchWithHandovers"
		oldAuthority, oldErr := evm.CanonicalBN128PublicKey(state.OldGroupPubkey)
		newAuthority, newErr := evm.CanonicalBN128PublicKey(state.NewGroupPubkey)
		if oldErr != nil || newErr != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]string{"error": "invalid migration authority point"})
			return
		}
		response.Account = &frostMigrationAccountDetail{
			Kind:            accountMigrationKind(response.Chain),
			ChainID:         strconv.FormatUint(template.ChainID, 10),
			ContractAddress: template.ContractAddr,
			Method:          method,
			OldPubkeyX:      decimalWord(oldAuthority[:32]),
			OldPubkeyY:      decimalWord(oldAuthority[32:]),
			NewPubkeyX:      decimalWord(newAuthority[:32]),
			NewPubkeyY:      decimalWord(newAuthority[32:]),
			NextEpoch:       state.NewEpoch,
			SignatureRX:     decimalWord(archive.Signature[:32]),
			SignatureRY:     decimalWord(archive.Signature[32:64]),
			SignatureS:      decimalWord(archive.Signature[64:]),
			GuardianRX:      decimalWord(archive.GuardianSigs[0][:32]),
			GuardianRY:      decimalWord(archive.GuardianSigs[0][32:64]),
			GuardianS:       decimalWord(archive.GuardianSigs[0][64:]),
			Calldata:        "",
		}
		if state.AuthorityProtocol == 3 {
			response.Account.AuthorityProtocol = 3
			response.Account.DexChainID = strconv.FormatUint(cfg.DepositDexChainId, 10)
			response.Account.FrozenNextBatchSeq = state.FrozenNextBatchSeq
			response.Account.FromEpoch = strconv.FormatUint(state.OldEpoch, 10)
			response.Account.ToEpoch = strconv.FormatUint(state.NewEpoch, 10)
		}
	}

	writeJSONStatus(w, http.StatusOK, response)
}
