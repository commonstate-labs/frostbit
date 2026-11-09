                                                                               
                                                                             
package withdrawresult

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"dex/frost/artifact"
	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"
	"dex/txverify"

	"github.com/btcsuite/btcd/wire"
	"golang.org/x/crypto/sha3"
	"google.golang.org/protobuf/proto"
)

const MaxArtifacts = 256
const MaxRawBytes = 4 << 20

type Dependency struct {
	TxID     string `json:"txid"`
	RawTxHex string `json:"raw_tx_hex"`
}

type BatchItem struct {
	WithdrawID   string `json:"withdraw_id"`
	RequestID    string `json:"request_id"`
	Token        string `json:"token"`
	To           string `json:"to"`
	GrossAmount  string `json:"gross_amount"`
	BroadcastFee string `json:"broadcast_fee"`
	NetAmount    string `json:"net_amount"`
	RewardIndex  uint32 `json:"reward_index"`
}

type Result struct {
	BTCProtocolVersion uint32       `json:"btc_protocol_version,omitempty"`
	UnallocatedAmount  string       `json:"unallocated_amount,omitempty"`
	ClaimedAmount      string       `json:"claimed_amount,omitempty"`
	SignedAmount       string       `json:"signed_amount,omitempty"`
	RefundedAmount     string       `json:"refunded_amount,omitempty"`
	MaxTotalFee        string       `json:"max_total_fee,omitempty"`
	CommittedFee       string       `json:"committed_fee,omitempty"`
	MaxParts           uint32       `json:"max_parts,omitempty"`
	NextPartSeq        string       `json:"next_part_seq,omitempty"`
	JobID              string       `json:"job_id,omitempty"`
	PartSeq            string       `json:"part_seq,omitempty"`
	OriginID           string       `json:"origin_id,omitempty"`
	AuthorityProtocol  uint32       `json:"authority_protocol,omitempty"`
	DexChainID         string       `json:"dex_chain_id,omitempty"`
	Version            uint32       `json:"version"`
	Status             string       `json:"status"`
	WithdrawID         string       `json:"withdraw_id"`
	RequestTxID        string       `json:"request_tx_id"`
	Requester          string       `json:"requester,omitempty"`
	OriginalRequestTx  string       `json:"original_request_tx,omitempty"`
	SignedTxID         string       `json:"signed_tx_id,omitempty"`
	CancelTxID         string       `json:"cancel_tx_id,omitempty"`
	CancelHeight       uint64       `json:"cancel_height,omitempty"`
	Chain              string       `json:"chain"`
	Asset              string       `json:"asset"`
	Amount             string       `json:"amount"`
	GrossAmount        string       `json:"gross_amount"`
	BroadcastFee       string       `json:"broadcast_fee"`
	NetAmount          string       `json:"net_amount"`
	RequestedAmount    string       `json:"requested_amount,omitempty"`
	NetworkFee         string       `json:"network_fee,omitempty"`
	ReceivedAmount     string       `json:"received_amount,omitempty"`
	Receiver           string       `json:"receiver"`
	VaultID            uint32       `json:"vault_id"`
	Epoch              string       `json:"epoch,omitempty"`
	ChainID            string       `json:"chain_id,omitempty"`
	BTCNetwork         string       `json:"btc_network,omitempty"`
	ArtifactID         string       `json:"artifact_id,omitempty"`
	RequestID          string       `json:"request_id,omitempty"`
	IsNative           bool         `json:"is_native"`
	TokenAddress       string       `json:"token_address,omitempty"`
	VaultAddress       string       `json:"vault_address,omitempty"`
	Calldata           string       `json:"calldata,omitempty"`
	IncludesHandover   bool         `json:"includes_handover,omitempty"`
	RawTxHex           string       `json:"raw_tx_hex,omitempty"`
	TxID               string       `json:"txid,omitempty"`
	Dependencies       []Dependency `json:"dependencies,omitempty"`
	BatchSeq           string       `json:"batch_seq,omitempty"`
	RewardTokens       []string     `json:"reward_tokens,omitempty"`
	Items              []BatchItem  `json:"items,omitempty"`
	FeeVersion         uint32       `json:"fee_version,omitempty"`
}

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string              { return e.Message }
func fail(status int, message string) error { return &Error{status, message} }

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

                                                                             
                                                                
type Get func(string) ([]byte, bool, error)

func ValidID(id string) bool {
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == ':' || c == '.') {
			return false
		}
	}
	return true
}

func Resolve(ctx context.Context, get Get, withdrawID, requestTxID string) (*Result, error) {
	return resolve(ctx, get, withdrawID, requestTxID, false)
}

                                                                            
                                                                                
func ResolveSummary(ctx context.Context, get Get, withdrawID, requestTxID string) (*Result, error) {
	return resolve(ctx, get, withdrawID, requestTxID, true)
}

func resolve(ctx context.Context, get Get, withdrawID, requestTxID string, summaryOnly bool) (*Result, error) {
	if (withdrawID == "" && requestTxID == "") || (withdrawID != "" && !ValidID(withdrawID)) || (requestTxID != "" && !ValidID(requestTxID)) {
		return nil, fail(http.StatusBadRequest, "invalid withdraw_id or request_tx_id")
	}
	if requestTxID != "" {
		raw, found, err := get(keys.KeyFrostWithdrawTxRef(requestTxID))
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fail(http.StatusNotFound, "withdrawal request not finalized or index unavailable")
		}
		mapped := string(raw)
		if !ValidID(mapped) || (withdrawID != "" && withdrawID != mapped) {
			return nil, fail(http.StatusConflict, "withdrawal request reference mismatch")
		}
		withdrawID = mapped
	}
	var withdrawal pb.FrostWithdrawState
	if err := readProto(get, &withdrawal, keys.KeyFrostPermanentWithdraw(withdrawID), keys.KeyFrostWithdraw(withdrawID), keys.KeyArchiveFrostWithdraw(withdrawID)); err != nil {
		return nil, err
	}
	if withdrawal.WithdrawId != withdrawID || (requestTxID != "" && withdrawal.TxId != requestTxID) {
		return nil, fail(http.StatusConflict, "withdrawal identity mismatch")
	}
	result := &Result{Version: 1, Status: withdrawal.Status, WithdrawID: withdrawID, RequestTxID: withdrawal.TxId,
		Requester: withdrawal.Requester, SignedTxID: withdrawal.SignedTxId, CancelTxID: withdrawal.CancelTxId,
		CancelHeight: withdrawal.CancelHeight, Chain: strings.ToUpper(withdrawal.Chain), Asset: withdrawal.Asset,
		Amount: firstNonEmpty(withdrawal.GrossAmount, withdrawal.Amount), GrossAmount: firstNonEmpty(withdrawal.GrossAmount, withdrawal.Amount), BroadcastFee: withdrawal.FeeAmount,
		RequestedAmount: firstNonEmpty(withdrawal.GrossAmount, withdrawal.Amount), Receiver: withdrawal.To, VaultID: withdrawal.VaultId, FeeVersion: withdrawal.FeeVersion}
	if result.BroadcastFee == "" {
		result.BroadcastFee = "0"
	}
	gross, grossOK := new(big.Int).SetString(result.GrossAmount, 10)
	fee, feeOK := new(big.Int).SetString(result.BroadcastFee, 10)
	chainName := chain.NormalizeChain(withdrawal.Chain)
	if !grossOK || !feeOK || gross.Sign() <= 0 || fee.Sign() < 0 || fee.Cmp(gross) >= 0 || chainName == chain.ChainBTC && fee.Sign() != 0 {
		return nil, fail(http.StatusConflict, "invalid withdrawal gross/fee fields")
	}
	result.NetAmount = new(big.Int).Sub(gross, fee).String()
	if withdrawal.BtcProtocolVersion == 3 {
		if chainName != chain.ChainBTC {
			return nil, fail(http.StatusConflict, "isolated BTC parent chain mismatch")
		}
		if err := fillBTCParentSummary(result, &withdrawal); err != nil {
			return nil, err
		}
		return result, nil
	}
	if result.Status == "QUEUED" && result.Requester == "" && !summaryOnly {
		requester, proof, err := resolveLegacyRequesterProof(get, &withdrawal)
		if err != nil {
			return nil, err
		}
		result.Requester = requester
		if len(proof) > 0 {
			result.OriginalRequestTx = "0x" + hex.EncodeToString(proof)
		}
	}
	if result.Status == "QUEUED" || result.Status == "SIGNING" || result.Status == "CANCELLED" {
		return result, nil
	}
	if result.Status != "SIGNED" || !ValidID(withdrawal.JobId) {
		return nil, fail(http.StatusConflict, "withdrawal signing state unavailable")
	}
	if summaryOnly {
		return result, nil
	}
	var cfg pb.FrostVaultConfig
	if err := readProto(get, &cfg, keys.KeyFrostVaultConfig(withdrawal.Chain, 0)); err != nil {
		return nil, err
	}
	if !strings.EqualFold(cfg.Chain, withdrawal.Chain) || withdrawal.VaultId >= cfg.VaultCount {
		return nil, fail(http.StatusConflict, "withdrawal vault config mismatch")
	}
	id, found, err := get(keys.KeyFrostPermanentWithdrawArtifact(withdrawID))
	if err != nil {
		return nil, err
	}
	if !found {
		id, found, err = get(keys.KeyFrostWithdrawArtifact(withdrawID))
		if err != nil {
			return nil, err
		}
	}
	if !found {
		                                                                      
		                                                                         
		                                                               
		id, err = legacyArtifactID(get, &withdrawal)
		if err != nil {
			return nil, err
		}
	}
	record, err := readArtifact(get, string(id))
	if err != nil {
		return nil, err
	}
	meta := record.Metadata
	if meta.Kind != artifact.KindWithdraw || !strings.EqualFold(meta.Chain, withdrawal.Chain) || meta.VaultID != withdrawal.VaultId || meta.SourceID != withdrawal.JobId {
		return nil, fail(http.StatusConflict, "withdrawal artifact scope mismatch")
	}
	result.ArtifactID, result.Epoch = meta.ArtifactID, strconv.FormatUint(meta.Epoch, 10)
	if result.Chain == "BTC" {
		err = fillBTC(ctx, get, result, &cfg, record, withdrawal.JobId)
	} else {
		err = fillAccount(get, result, &cfg, record, withdrawal.JobId)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

                                                                              
                                                                            
                                                                            
func resolveLegacyRequesterProof(get Get, withdrawal *pb.FrostWithdrawState) (string, []byte, error) {
	if withdrawal == nil || strings.TrimSpace(withdrawal.TxId) == "" {
		return "", nil, nil
	}
	raw, found, err := get(keys.KeyTxRaw(withdrawal.TxId))
	if err != nil {
		return "", nil, err
	}
	if !found || len(raw) == 0 || len(raw) > 16*1024 {
		return "", nil, nil
	}

	var original pb.AnyTx
	if proto.Unmarshal(raw, &original) != nil || pb.Sanitize(&original) != nil {
		return "", nil, nil
	}
	request, ok := original.GetContent().(*pb.AnyTx_FrostWithdrawRequestTx)
	if !ok || request.FrostWithdrawRequestTx == nil || request.FrostWithdrawRequestTx.Base == nil {
		return "", nil, nil
	}
	req := request.FrostWithdrawRequestTx
	base := req.Base
	if base.ChainId == 0 || base.TxId != withdrawal.TxId || strings.TrimSpace(base.FromAddress) == "" || txverify.Verify(&original, base.ChainId) != nil {
		return "", nil, nil
	}
	if strings.TrimSpace(req.Asset) != withdrawal.Asset {
		return "", nil, nil
	}
	if requestedChain := chain.NormalizeChain(req.Chain); requestedChain != "" && requestedChain != chain.NormalizeChain(withdrawal.Chain) {
		return "", nil, nil
	}
	requestAmount, requestErr := chain.ParseWithdrawAmount(req.Amount)
	storedAmount, storedErr := chain.ParseWithdrawAmount(withdrawal.Amount)
	if requestErr != nil || storedErr != nil || requestAmount.Cmp(storedAmount) != 0 {
		return "", nil, nil
	}
	if !legacyReceiverMatches(get, withdrawal, req.To) {
		return "", nil, nil
	}
	return strings.TrimSpace(base.FromAddress), append([]byte(nil), raw...), nil
}

func legacyReceiverMatches(get Get, withdrawal *pb.FrostWithdrawState, original string) bool {
	original = strings.TrimSpace(original)
	if original == withdrawal.To {
		return true
	}
	switch chain.NormalizeChain(withdrawal.Chain) {
	case chain.ChainETH, chain.ChainBNB, chain.ChainBASE, chain.ChainARB, chain.ChainTRX:
		canonical, err := evm.NormalizeAccountAddress(withdrawal.Chain, original, false)
		return err == nil && canonical == withdrawal.To
	case chain.ChainBTC:
		var cfg pb.FrostVaultConfig
		raw, found, err := get(keys.KeyFrostVaultConfig(withdrawal.Chain, 0))
		if err != nil || !found || proto.Unmarshal(raw, &cfg) != nil {
			return false
		}
		canonical, _, err := btc.NormalizeAddressForNetwork(original, cfg.BtcNetwork)
		return err == nil && canonical == withdrawal.To
	default:
		return false
	}
}

func readProto(get Get, out proto.Message, candidates ...string) error {
	for _, key := range candidates {
		raw, found, err := get(key)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		if len(raw) == 0 || proto.Unmarshal(raw, out) != nil {
			return fail(http.StatusConflict, "invalid withdrawal source record")
		}
		return nil
	}
	return fail(http.StatusNotFound, "withdrawal data unavailable; use an archive node")
}

func unavailableSigningRecord(err error, message string) error {
	var queryErr *Error
	if errors.As(err, &queryErr) && queryErr.Status == http.StatusNotFound {
		return fail(http.StatusGone, message)
	}
	return err
}

func readArtifact(get Get, id string) (*artifact.Record, error) {
	record, err := artifact.ReadRecord(get, id, MaxRawBytes*2)
	switch {
	case errors.Is(err, artifact.ErrRecordUnavailable):
		return nil, fail(http.StatusGone, "legacy signed transaction bytes unavailable; use an archive node")
	case errors.Is(err, artifact.ErrRecordTooLarge):
		return nil, fail(http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, artifact.ErrInvalidRecord):
		return nil, fail(http.StatusConflict, err.Error())
	case err != nil:
		return nil, err
	}
	return record, nil
}

func legacyArtifactID(get Get, wd *pb.FrostWithdrawState) ([]byte, error) {
	var job pb.FrostWithdrawJobState
	var sig pb.FrostSignedPackage
	if err := readProto(get, &job, keys.KeyFrostPermanentWithdrawJob(wd.JobId), keys.KeyFrostWithdrawJob(wd.JobId), keys.KeyArchiveFrostWithdrawJob(wd.JobId)); err != nil {
		return nil, unavailableSigningRecord(err, "legacy signed job unavailable; use an archive node")
	}
	if err := readProto(get, &sig, keys.KeyFrostPermanentSignedPackage(wd.JobId, 0), keys.KeyFrostSignedPackage(wd.JobId, 0), keys.KeyArchiveFrostSignedPackage(wd.JobId, 0)); err != nil {
		return nil, unavailableSigningRecord(err, "legacy signature unavailable; use an archive node")
	}
	contains := false
	for _, id := range job.WithdrawIds {
		contains = contains || id == wd.WithdrawId
	}
	if !contains || job.JobId != wd.JobId || sig.JobId != wd.JobId || job.Status != "SIGNED" || !strings.EqualFold(job.Chain, wd.Chain) || job.VaultId != wd.VaultId {
		return nil, fail(http.StatusConflict, "legacy signing job mismatch")
	}
	var raw []byte
	var err error
	meta := artifact.Metadata{Kind: artifact.KindWithdraw, Chain: wd.Chain}
	if strings.EqualFold(wd.Chain, "BTC") {
		template, e := btc.FromJSON(job.TemplateData)
		if e != nil || len(template.Inputs) == 0 || len(sig.SignedPackageBytes) != len(template.Inputs)*64 {
			return nil, fail(http.StatusConflict, "invalid legacy BTC signatures")
		}
		parts := make([][]byte, len(template.Inputs))
		for i := range parts {
			parts[i] = sig.SignedPackageBytes[i*64 : (i+1)*64]
		}
		raw, err = template.BuildSignedRawTx(parts, sig.GuardianSigs)
		if err == nil {
			var tx wire.MsgTx
			err = tx.Deserialize(bytes.NewReader(raw))
			meta.ArtifactID = tx.TxHash().String()
		}
	} else {
		var packaged *chain.SignedPackage
		t, e := evm.FromJSON(job.TemplateData)
		if e != nil || len(sig.SignedPackageBytes) != 192 {
			return nil, fail(http.StatusConflict, "invalid dual-custody account package")
		}
		packaged, err = evm.NewEVMAdapter(wd.Chain, t.ChainID).PackageSigned(job.TemplateData, [][]byte{sig.SignedPackageBytes[:96]}, [][]byte{sig.SignedPackageBytes[96:]})
		if err == nil {
			raw = packaged.RawTx
		}
	}
	if err != nil {
		return nil, fail(http.StatusConflict, "cannot reconstruct legacy artifact reference")
	}
	record, err := artifact.NewRecord(meta, raw)
	if err != nil {
		return nil, err
	}
	return []byte(record.Metadata.ArtifactID), nil
}

func keccak(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write(data)
	return h.Sum(nil)
}

func fillAccount(get Get, result *Result, cfg *pb.FrostVaultConfig, record *artifact.Record, jobID string) error {
	switch result.Chain {
	case "ETH", "BNB", "BASE", "ARB", "TRX":
	default:
		return fail(http.StatusConflict, "unsupported withdrawal chain")
	}
	if cfg.EvmChainId == 0 || int(result.VaultID) >= len(cfg.VaultRefs) {
		return fail(http.StatusConflict, "account vault configuration unavailable")
	}
	var token pb.Token
	if err := readProto(get, &token, keys.KeyToken(result.Asset)); err != nil {
		return err
	}
	if !strings.EqualFold(token.NativeChain, result.Chain) {
		return fail(http.StatusConflict, "withdrawal asset chain mismatch")
	}
	vault, err := evm.NormalizeAccountAddress(result.Chain, cfg.VaultRefs[result.VaultID], false)
	if err != nil {
		return err
	}
	to, err := evm.NormalizeAccountAddress(result.Chain, result.Receiver, false)
	if err != nil {
		return err
	}
	native := strings.TrimSpace(token.NativeContract) == ""
	tokenRaw := token.NativeContract
	if native {
		tokenRaw = "0x" + strings.Repeat("0", 40)
	}
	tokenAddress, err := evm.NormalizeAccountAddress(result.Chain, tokenRaw, true)
	if err != nil {
		return err
	}
	native = strings.EqualFold(tokenAddress, "0x"+strings.Repeat("0", 40))
	var job pb.FrostWithdrawJobState
	if err := readProto(get, &job, keys.KeyFrostPermanentWithdrawJob(jobID), keys.KeyFrostWithdrawJob(jobID), keys.KeyArchiveFrostWithdrawJob(jobID)); err != nil {
		return unavailableSigningRecord(err, "signed account job unavailable; use an archive node")
	}
	if job.JobId != jobID || job.Status != "SIGNED" || !strings.EqualFold(job.Chain, result.Chain) || job.VaultId != result.VaultID || job.KeyEpoch != record.Metadata.Epoch {
		return fail(http.StatusConflict, "signed account job scope mismatch")
	}
	template, err := evm.FromJSON(job.TemplateData)
	if err != nil || !bytes.Equal(template.TemplateHash(), job.TemplateHash) || len(template.WithdrawIDs) != len(job.WithdrawIds) || len(template.Outputs) != len(job.WithdrawIds) {
		return fail(http.StatusConflict, "invalid signed account job template")
	}
	if _, err := evm.ParseBatchSeq(template.BatchSeq); err != nil || job.BatchSeq != template.BatchSeq {
		return fail(http.StatusConflict, "invalid signed account batch sequence")
	}
	if template.AuthorityProtocol == 3 {
		if cfg.AuthorityProtocol != 3 || template.DexChainID == 0 || template.DexChainID != cfg.DepositDexChainId || !bytes.Equal(template.GuardianPubkey, cfg.GuardianPubkey) {
			return fail(http.StatusConflict, "signed account authority protocol mismatch")
		}
		result.AuthorityProtocol = 3
		result.DexChainID = strconv.FormatUint(template.DexChainID, 10)
	} else {
		return fail(http.StatusConflict, "unsupported signed account authority protocol")
	}
	result.BatchSeq = template.BatchSeq
	result.RewardTokens = append([]string(nil), template.RewardTokens...)
	result.Items = make([]BatchItem, 0, len(job.WithdrawIds))
	found := false
	for i, id := range job.WithdrawIds {
		if template.WithdrawIDs[i] != id || template.Outputs[i].WithdrawID != id {
			return fail(http.StatusConflict, "signed account job withdrawal order mismatch")
		}
		out := template.Outputs[i]
		grossRaw := firstNonEmpty(out.GrossAmount, out.Amount)
		gross, ok := new(big.Int).SetString(grossRaw, 10)
		if !ok || gross.Sign() <= 0 {
			return fail(http.StatusConflict, "invalid signed account batch amount")
		}
		fee := evm.CalculateBroadcastFee(gross)
		net := new(big.Int).Sub(new(big.Int).Set(gross), fee)
		if int(out.RewardIndex) >= len(template.RewardTokens) || out.Token != template.RewardTokens[out.RewardIndex] || net.Sign() <= 0 {
			return fail(http.StatusConflict, "invalid signed account batch reward mapping")
		}
		result.Items = append(result.Items, BatchItem{
			WithdrawID: id, RequestID: "0x" + hex.EncodeToString(keccak([]byte(id))), Token: out.Token,
			To: out.To, GrossAmount: gross.String(), BroadcastFee: fee.String(), NetAmount: net.String(), RewardIndex: out.RewardIndex,
		})
		if id == result.WithdrawID {
			outToken := strings.TrimSpace(out.Token)
			if outToken == "" {
				outToken = "0x" + strings.Repeat("0", 40)
			}
			if out.To != to || gross.String() != result.GrossAmount || fee.String() != result.BroadcastFee || net.String() != result.NetAmount || !strings.EqualFold(outToken, tokenAddress) {
				return fail(http.StatusConflict, "signed calldata does not match the withdrawal request")
			}
			found = true
		}
	}
	if !found {
		return fail(http.StatusConflict, "signed account batch does not contain the withdrawal")
	}
	raw := record.RawTx
	if len(raw) < 228 {
		return fail(http.StatusConflict, "signed calldata does not match the withdrawal request")
	}
	adapter := evm.NewEVMAdapter(result.Chain, cfg.EvmChainId)
	withdrawSig := append([]byte(nil), raw[36:132]...)
	if normal, packageErr := adapter.PackageSigned(job.TemplateData, [][]byte{withdrawSig}, [][]byte{raw[132:228]}); packageErr == nil && bytes.Equal(normal.RawTx, raw) {
		                  
	} else if template.AuthorityProtocol == 3 {
		certs, signature, guardianSignature, err := evm.DecodeSignedHandovers(raw)
		if err != nil {
			return fail(http.StatusConflict, "invalid V3 withdrawal handover calldata")
		}
		for _, cert := range certs {
			_, expected, err := signedHandover(get, cfg, chain.NormalizeChain(result.Chain), result.VaultID, cert.ToEpoch)
			if err != nil || cert.FromEpoch != expected.FromEpoch || cert.ToEpoch != expected.ToEpoch || cert.ToEpoch > record.Metadata.Epoch || cert.FrozenNextBatchSeq != expected.FrozenNextBatchSeq || !bytes.Equal(cert.NewGroupPubkey, expected.NewGroupPubkey) || !bytes.Equal(cert.Signature, expected.Signature) || !bytes.Equal(cert.GuardianSignature, expected.GuardianSignature) {
				return fail(http.StatusConflict, "V3 withdrawal handover differs from permanent authority proof")
			}
		}
		expected, err := adapter.PackageSignedWithHandovers(job.TemplateData, signature, guardianSignature, certs)
		if err != nil || !bytes.Equal(expected.RawTx, raw) {
			return fail(http.StatusConflict, "V3 signed calldata does not match the withdrawal request")
		}
		result.IncludesHandover = len(certs) != 0
	} else {
		return fail(http.StatusConflict, "signed calldata does not match the withdrawal request")
	}
	result.ChainID = strconv.FormatUint(cfg.EvmChainId, 10)
	result.RequestID = "0x" + hex.EncodeToString(keccak([]byte(result.WithdrawID)))
	result.IsNative, result.TokenAddress, result.VaultAddress = native, tokenAddress, vault
	result.Calldata = "0x" + hex.EncodeToString(raw)
	result.ReceivedAmount = result.NetAmount
	return nil
}

func fillBTC(ctx context.Context, get Get, result *Result, cfg *pb.FrostVaultConfig, root *artifact.Record, jobID string) error {
	script, err := btc.AddressScriptForNetwork(result.Receiver, cfg.BtcNetwork)
	if err != nil {
		return fail(http.StatusConflict, "invalid BTC recipient or network")
	}
	amount, err := chain.ParseWithdrawAmount(result.Amount)
	if err != nil || !amount.IsInt64() {
		return fail(http.StatusConflict, "invalid BTC withdrawal amount")
	}
	var tx wire.MsgTx
	reader := bytes.NewReader(root.RawTx)
	if err = tx.Deserialize(reader); err != nil || reader.Len() != 0 || len(tx.TxIn) == 0 {
		return fail(http.StatusConflict, "invalid signed BTC transaction")
	}
	var job pb.FrostWithdrawJobState
	if err := readProto(get, &job, keys.KeyFrostPermanentWithdrawJob(jobID), keys.KeyFrostWithdrawJob(jobID), keys.KeyArchiveFrostWithdrawJob(jobID)); err != nil {
		return unavailableSigningRecord(err, "signed BTC job unavailable; use an archive node")
	}
	if job.JobId != jobID || job.Status != "SIGNED" || !strings.EqualFold(job.Chain, result.Chain) || job.VaultId != result.VaultID {
		return fail(http.StatusConflict, "signed BTC job scope mismatch")
	}
	template, err := btc.FromJSON(job.TemplateData)
	if err != nil || !bytes.Equal(template.TemplateHash(), job.TemplateHash) || len(template.WithdrawIDs) != len(job.WithdrawIds) || len(template.Outputs) < len(job.WithdrawIds) {
		return fail(http.StatusConflict, "invalid signed BTC job template")
	}
	withdrawIndex := -1
	for i, id := range job.WithdrawIds {
		if template.WithdrawIDs[i] != id {
			return fail(http.StatusConflict, "signed BTC job withdrawal order mismatch")
		}
		if id == result.WithdrawID {
			withdrawIndex = i
		}
	}
	if withdrawIndex < 0 || len(tx.TxOut) != len(template.Outputs) {
		return fail(http.StatusConflict, "signed BTC transaction does not contain the withdrawal")
	}
	for i, output := range template.Outputs {
		outputScript, scriptErr := btc.AddressScriptForNetwork(output.Address, cfg.BtcNetwork)
		if scriptErr != nil || output.Amount > uint64(math.MaxInt64) || tx.TxOut[i].Value != int64(output.Amount) || !bytes.Equal(tx.TxOut[i].PkScript, outputScript) {
			return fail(http.StatusConflict, "signed BTC transaction outputs do not match the finalized template")
		}
	}
	received := template.Outputs[withdrawIndex].Amount
	if received == 0 || received > amount.Uint64() || !bytes.Equal(tx.TxOut[withdrawIndex].PkScript, script) {
		return fail(http.StatusConflict, "signed BTC transaction has no matching payment")
	}
	result.ReceivedAmount = strconv.FormatUint(received, 10)
	result.NetworkFee = strconv.FormatUint(amount.Uint64()-received, 10)
	for _, in := range tx.TxIn {
		if len(in.Witness) == 0 {
			return fail(http.StatusConflict, "BTC input signature unavailable")
		}
	}
	seen, visiting := map[string]bool{}, map[string]bool{}
	totalBytes, count := 0, 0
	var visit func(*artifact.Record) error
	visit = func(record *artifact.Record) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := record.Metadata.ArtifactID
		if visiting[id] {
			return fail(http.StatusConflict, "cyclic BTC artifact dependency")
		}
		if seen[id] {
			return nil
		}
		count++
		totalBytes += len(record.RawTx)
		if count > MaxArtifacts || totalBytes > MaxRawBytes {
			return fail(http.StatusRequestEntityTooLarge, "BTC dependency package exceeds download budget")
		}
		if record.Metadata.Chain != "BTC" || len(record.RawTx) == 0 {
			return fail(http.StatusConflict, "invalid BTC dependency artifact")
		}
		visiting[id] = true
		for _, parent := range record.Metadata.ParentIDs {
			if seen[parent] {
				continue
			}
			if count >= MaxArtifacts {
				return fail(http.StatusRequestEntityTooLarge, "BTC dependency count exceeds download budget")
			}
			dependency, err := readArtifact(get, parent)
			if err != nil {
				return err
			}
			if err = visit(dependency); err != nil {
				return err
			}
		}
		delete(visiting, id)
		seen[id] = true
		if id != root.Metadata.ArtifactID {
			result.Dependencies = append(result.Dependencies, Dependency{TxID: id, RawTxHex: hex.EncodeToString(record.RawTx)})
		}
		return nil
	}
	if err := visit(root); err != nil {
		return err
	}
	result.BTCNetwork, result.IsNative = cfg.BtcNetwork, true
	result.RawTxHex, result.TxID = hex.EncodeToString(root.RawTx), tx.TxHash().String()
	if result.TxID != root.Metadata.ArtifactID {
		return fmt.Errorf("BTC artifact txid mismatch")
	}
	return nil
}
