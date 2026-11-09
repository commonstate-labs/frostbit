package withdrawresult

import (
	"bytes"
	"context"
	"math/big"
	"net/http"
	"strconv"
	"strings"

	"dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"
)

                                                                              
                                                                           
type HandoverCertificate struct {
	FromEpoch          string `json:"from_epoch"`
	ToEpoch            string `json:"to_epoch"`
	NewPX              string `json:"new_px"`
	NewPY              string `json:"new_py"`
	FrozenNextBatchSeq string `json:"frozen_next_batch_seq"`
	RX                 string `json:"rx"`
	RY                 string `json:"ry"`
	S                  string `json:"s"`
	GuardianRX         string `json:"guardian_rx"`
	GuardianRY         string `json:"guardian_ry"`
	GuardianS          string `json:"guardian_s"`
}

type HandoverPage struct {
	AuthorityProtocol uint32                `json:"authority_protocol"`
	DexChainID        string                `json:"dex_chain_id"`
	ChainID           string                `json:"chain_id"`
	Contract          string                `json:"contract"`
	Chain             string                `json:"chain"`
	VaultID           uint32                `json:"vault_id"`
	FromEpoch         string                `json:"from_epoch"`
	TargetEpoch       string                `json:"target_epoch"`
	NextEpoch         string                `json:"next_epoch"`
	HasMore           bool                  `json:"has_more"`
	Certificates      []HandoverCertificate `json:"certificates"`
}

func accountConfig(get Get, name string, vault uint32) (*pb.FrostVaultConfig, error) {
	name = chain.NormalizeChain(name)
	switch name {
	case chain.ChainETH, chain.ChainBNB, chain.ChainBASE, chain.ChainARB, chain.ChainTRX:
	default:
		return nil, fail(http.StatusBadRequest, "unsupported account chain")
	}
	var cfg pb.FrostVaultConfig
	if err := readProto(get, &cfg, keys.KeyFrostVaultConfig(name, 0), keys.KeyFrostVaultConfig(strings.ToUpper(name), 0)); err != nil {
		return nil, err
	}
	if chain.NormalizeChain(cfg.Chain) != name || cfg.EvmChainId == 0 || int(vault) >= len(cfg.VaultRefs) {
		return nil, fail(http.StatusConflict, "account vault configuration mismatch")
	}
	return &cfg, nil
}

                                                                            
                                                                             
func signedHandover(get Get, cfg *pb.FrostVaultConfig, name string, vault uint32, target uint64) (*pb.FrostVaultMigrationState, evm.SignedHandover, error) {
	var marker pb.FrostVaultMigrationState
	var empty evm.SignedHandover
	if err := readProto(get, &marker, keys.KeyFrostVaultMigration(name, vault, target)); err != nil {
		return nil, empty, err
	}
	if marker.Status != "SIGNED" || chain.NormalizeChain(marker.Chain) != name || marker.VaultId != vault || marker.OldEpoch == 0 || marker.OldEpoch == ^uint64(0) || marker.NewEpoch != marker.OldEpoch+1 || marker.NewEpoch != target || marker.MigrationId != chain.MigrationID(name, vault, target) || marker.AuthorityProtocol != 3 {
		return nil, empty, fail(http.StatusConflict, "invalid signed V3 handover marker")
	}
	canonical, _, err := evm.CanonicalMigrationTemplate(cfg, &marker)
	if err != nil || !bytes.Equal(canonical.TemplateHash, marker.TemplateHash) || (len(marker.TemplateData) != 0 && !bytes.Equal(marker.TemplateData, canonical.TemplateData)) || len(canonical.SigHashes) != 1 || !bytes.Equal(canonical.SigHashes[0], marker.IntentHash) {
		return nil, empty, fail(http.StatusConflict, "handover template differs from consensus intent")
	}
	signature, found, err := get(keys.KeyFrostVaultHandoverProof(name, vault, target))
	if err != nil {
		return nil, empty, err
	}
	if !found || len(signature) != 2*evm.SchnorrSigWithYSize {
		return nil, empty, fail(http.StatusConflict, "handover signature unavailable")
	}
	valid, err := evm.NewEVMAdapter(name, cfg.EvmChainId).VerifySignature(marker.OldGroupPubkey, marker.IntentHash, signature[:96])
	if err != nil || !valid {
		return nil, empty, fail(http.StatusConflict, "handover signature mismatch")
	}
	guardianOK, guardianErr := evm.NewEVMAdapter(name, cfg.EvmChainId).VerifySignature(cfg.GuardianPubkey, marker.IntentHash, signature[96:])
	if guardianErr != nil || !guardianOK {
		return nil, empty, fail(http.StatusConflict, "invalid guardian handover proof")
	}
	key, err := evm.CanonicalBN128PublicKey(marker.NewGroupPubkey)
	if err != nil || len(key) != 64 {
		return nil, empty, fail(http.StatusConflict, "invalid handover authority")
	}
	return &marker, evm.SignedHandover{FromEpoch: marker.OldEpoch, ToEpoch: marker.NewEpoch, NewGroupPubkey: append([]byte(nil), key...), FrozenNextBatchSeq: marker.FrozenNextBatchSeq, Signature: append([]byte(nil), signature[:96]...), GuardianSignature: append([]byte(nil), signature[96:]...)}, nil
}

func certificateJSON(cert evm.SignedHandover) HandoverCertificate {
	word := func(b []byte) string { return new(big.Int).SetBytes(b).String() }
	return HandoverCertificate{FromEpoch: strconv.FormatUint(cert.FromEpoch, 10), ToEpoch: strconv.FormatUint(cert.ToEpoch, 10), NewPX: word(cert.NewGroupPubkey[:32]), NewPY: word(cert.NewGroupPubkey[32:]), FrozenNextBatchSeq: cert.FrozenNextBatchSeq, RX: word(cert.Signature[:32]), RY: word(cert.Signature[32:64]), S: word(cert.Signature[64:]), GuardianRX: word(cert.GuardianSignature[:32]), GuardianRY: word(cert.GuardianSignature[32:64]), GuardianS: word(cert.GuardianSignature[64:])}
}

                                                                               
                                                                             
func ResolveHandoverPage(ctx context.Context, get Get, name string, vault uint32, from, target uint64) (*HandoverPage, error) {
	name = chain.NormalizeChain(name)
	if from == 0 || target < from {
		return nil, fail(http.StatusBadRequest, "invalid handover epoch range")
	}
	cfg, err := accountConfig(get, name, vault)
	if err != nil {
		return nil, err
	}
	if cfg.AuthorityProtocol != 3 || cfg.DepositDexChainId == 0 {
		return nil, fail(http.StatusConflict, "vault does not support V3 handovers")
	}
	var state pb.FrostVaultState
	if err := readProto(get, &state, keys.KeyFrostVaultState(name, vault)); err != nil {
		return nil, err
	}
	if chain.NormalizeChain(state.Chain) != name || state.VaultId != vault || state.KeyEpoch < target {
		return nil, fail(http.StatusConflict, "handover target is not a finalized active authority")
	}
	contract, err := evm.NormalizeAccountAddress(name, cfg.VaultRefs[vault], false)
	if err != nil {
		return nil, fail(http.StatusConflict, "invalid vault contract")
	}
	page := &HandoverPage{AuthorityProtocol: 3, DexChainID: strconv.FormatUint(cfg.DepositDexChainId, 10), ChainID: strconv.FormatUint(cfg.EvmChainId, 10), Contract: contract, Chain: name, VaultID: vault, FromEpoch: strconv.FormatUint(from, 10), TargetEpoch: strconv.FormatUint(target, 10), Certificates: []HandoverCertificate{}}
	current := from
	var previousKey []byte
	for current < target && len(page.Certificates) < evm.MaxHandoversPerCall {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		marker, cert, err := signedHandover(get, cfg, name, vault, current+1)
		if err != nil {
			return nil, err
		}
		if previousKey != nil {
			old, err := evm.CanonicalBN128PublicKey(marker.OldGroupPubkey)
			if err != nil || !bytes.Equal(old, previousKey) {
				return nil, fail(http.StatusConflict, "handover authority chain is discontinuous")
			}
		}
		previousKey = cert.NewGroupPubkey
		page.Certificates = append(page.Certificates, certificateJSON(cert))
		current++
	}
	page.NextEpoch = strconv.FormatUint(current, 10)
	page.HasMore = current < target
	return page, nil
}

                                                                           
                                                                             
func ResolveBatch(ctx context.Context, get Get, name string, vault uint32, sequence string) (*Result, error) {
	name = chain.NormalizeChain(name)
	if _, err := evm.ParseBatchSeq(sequence); err != nil {
		return nil, fail(http.StatusBadRequest, "invalid batch_seq")
	}
	if _, err := accountConfig(get, name, vault); err != nil {
		return nil, err
	}
	raw, found, err := get(keys.KeyFrostWithdrawBatch(name, vault, sequence))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fail(http.StatusNotFound, "signed batch unavailable")
	}
	id := string(raw)
	if !ValidID(id) || strings.TrimSpace(id) != id {
		return nil, fail(http.StatusConflict, "invalid permanent batch index")
	}
	result, err := Resolve(ctx, get, id, "")
	if err != nil {
		return nil, err
	}
	if result.Status != "SIGNED" || chain.NormalizeChain(result.Chain) != name || result.VaultID != vault || result.BatchSeq != sequence {
		return nil, fail(http.StatusConflict, "permanent batch index scope mismatch")
	}
	return result, nil
}
