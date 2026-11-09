package vm

import (
	"bytes"
	"fmt"
	"strings"

	"dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                                                            
                                                                              
                                                                                
func packageAccountWithdrawal(
	sv StateView,
	chainName string,
	vaultID uint32,
	withdrawTemplateData []byte,
	withdrawalSignature []byte,
	guardianSignature []byte,
	includesHandover bool,
) (*chain.SignedPackage, error) {
	template, err := evm.FromJSON(withdrawTemplateData)
	if err != nil {
		return nil, evm.ErrInvalidTemplateData
	}
	adapter := evm.NewEVMAdapter(chainName, template.ChainID)
	if !includesHandover {
		return adapter.PackageSigned(withdrawTemplateData, [][]byte{withdrawalSignature}, [][]byte{guardianSignature})
	}
	migrationData, found, err := sv.Get(keys.KeyFrostVaultMigration(chainName, vaultID, template.KeyEpoch))
	if err != nil {
		return nil, fmt.Errorf("read account migration marker: %w", err)
	}
	if !found {
		return nil, fmt.Errorf("account migration marker is unavailable")
	}
	if len(migrationData) == 0 {
		return nil, fmt.Errorf("empty account migration marker")
	}

	var marker pb.FrostVaultMigrationState
	if err := proto.Unmarshal(migrationData, &marker); err != nil {
		return nil, fmt.Errorf("decode account migration marker: %w", err)
	}
	if marker.Status != migrationStatusSigned || marker.MigrationId == "" ||
		!strings.EqualFold(marker.Chain, chainName) || marker.VaultId != vaultID ||
		marker.NewEpoch != template.KeyEpoch || marker.OldEpoch == ^uint64(0) ||
		marker.NewEpoch != marker.OldEpoch+1 {
		return nil, fmt.Errorf("account migration marker does not match withdrawal epoch")
	}

	signature, found, err := sv.Get(keys.KeyFrostVaultHandoverProof(chainName, vaultID, template.KeyEpoch))
	if err != nil {
		return nil, fmt.Errorf("read account handover proof: %w", err)
	}
	if !found || len(signature) == 0 {
		return nil, fmt.Errorf("account migration proof is unavailable")
	}

	cfg := &pb.FrostVaultConfig{GuardianPubkey: append([]byte(nil), template.GuardianPubkey...), EvmChainId: template.ChainID, VaultRefs: make([]string, int(vaultID)+1), AuthorityProtocol: template.AuthorityProtocol, DepositDexChainId: template.DexChainID}
	cfg.VaultRefs[vaultID] = template.ContractAddr
	canonical, _, err := evm.CanonicalMigrationTemplate(cfg, &marker)
	if err != nil || !bytes.Equal(canonical.TemplateHash, marker.TemplateHash) || len(canonical.SigHashes) != 1 ||
		!bytes.Equal(canonical.SigHashes[0], marker.IntentHash) {
		return nil, fmt.Errorf("account migration proof is not canonical")
	}
	if len(signature) != 2*evm.SchnorrSigWithYSize {
		return nil, fmt.Errorf("account migration dual proof size invalid")
	}
	valid, verifyErr := adapter.VerifySignature(marker.OldGroupPubkey, marker.IntentHash, signature[:evm.SchnorrSigWithYSize])
	if verifyErr != nil || !valid {
		return nil, fmt.Errorf("account migration proof signature is invalid")
	}
	valid, verifyErr = adapter.VerifySignature(cfg.GuardianPubkey, marker.IntentHash, signature[evm.SchnorrSigWithYSize:])
	if verifyErr != nil || !valid {
		return nil, fmt.Errorf("account migration guardian proof is invalid")
	}
	return adapter.PackageSignedWithHandover(
		withdrawTemplateData,
		withdrawalSignature,
		guardianSignature,
		canonical.TemplateData,
		signature[:evm.SchnorrSigWithYSize],
		signature[evm.SchnorrSigWithYSize:],
	)
}

                                                                               
                                                                                
func accountEpochAuthorityReady(sv StateView, chainName string, vaultID uint32, epoch uint64, cfg *pb.FrostVaultConfig) (bool, error) {
	if cfg == nil || cfg.AuthorityProtocol != 3 {
		return false, fmt.Errorf("unsupported authority protocol")
	}
	if epoch <= 1 {
		return epoch == 1, nil
	}
	raw, found, err := sv.Get(keys.KeyFrostVaultMigration(chainName, vaultID, epoch))
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	var m pb.FrostVaultMigrationState
	if proto.Unmarshal(raw, &m) != nil || m.Status != migrationStatusSigned || m.AuthorityProtocol != 3 || m.MigrationId != chain.MigrationID(chainName, vaultID, epoch) || !strings.EqualFold(m.Chain, chainName) || m.VaultId != vaultID || m.NewEpoch != epoch || m.OldEpoch != epoch-1 {
		return false, fmt.Errorf("invalid V3 migration marker")
	}
	canonical, _, err := evm.CanonicalMigrationTemplate(cfg, &m)
	if err != nil || !bytes.Equal(canonical.TemplateHash, m.TemplateHash) || len(canonical.SigHashes) != 1 || !bytes.Equal(canonical.SigHashes[0], m.IntentHash) {
		return false, fmt.Errorf("noncanonical V3 migration marker")
	}
	proof, found, err := sv.Get(keys.KeyFrostVaultHandoverProof(chainName, vaultID, epoch))
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	if len(proof) != 2*evm.SchnorrSigWithYSize {
		return false, fmt.Errorf("invalid V3 authority signature size")
	}
	valid, err := evm.NewEVMAdapter(chainName, cfg.EvmChainId).VerifySignature(m.OldGroupPubkey, m.IntentHash, proof[:evm.SchnorrSigWithYSize])
	if err != nil || !valid {
		return false, fmt.Errorf("invalid V3 authority signature")
	}
	valid, err = evm.NewEVMAdapter(chainName, cfg.EvmChainId).VerifySignature(cfg.GuardianPubkey, m.IntentHash, proof[evm.SchnorrSigWithYSize:])
	if err != nil || !valid {
		return false, fmt.Errorf("invalid V3 guardian authority signature")
	}
	return true, nil
}
