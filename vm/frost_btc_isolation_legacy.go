package vm

import (
	"bytes"
	"fmt"
	"strings"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

func validateBTCIsolationLegacyCoordinator(sv StateView, job *pb.FrostWithdrawJobState, submitter string) error {
	if job.KeyEpoch == ^uint64(0) {
		return fmt.Errorf("invalid legacy BTC epoch")
	}
	raw, found, err := sv.Get(keys.KeyFrostVaultState(chain.ChainBTC, job.VaultId))
	if err != nil {
		return err
	}
	var vault pb.FrostVaultState
	if !found || proto.Unmarshal(raw, &vault) != nil {
		return fmt.Errorf("legacy BTC vault unavailable")
	}
	committee := vault.CommitteeMembers
	if vault.KeyEpoch != job.KeyEpoch {
		raw, found, err = sv.Get(keys.KeyFrostVaultTransition(chain.ChainBTC, job.VaultId, job.KeyEpoch+1))
		if err != nil {
			return err
		}
		var transition pb.VaultTransitionState
		if !found || proto.Unmarshal(raw, &transition) != nil || transition.Chain != chain.ChainBTC ||
			transition.VaultId != job.VaultId || transition.EpochId != job.KeyEpoch+1 {
			return fmt.Errorf("legacy BTC authority history unavailable")
		}
		committee = transition.OldCommitteeMembers
	}
	expected, err := chain.WithdrawCoordinator(job.JobId, job.KeyEpoch, committee)
	if err != nil || submitter != expected {
		return fmt.Errorf("legacy BTC replay submitter is not coordinator")
	}
	return nil
}

                                                                           
                                                                             
                                                                              
                                                                              
func replayBTCIsolationLegacySigned(sv StateView, signed *pb.FrostWithdrawSignedTx, job *pb.FrostWithdrawJobState) ([]WriteOp, *Receipt, error) {
	fail := func(message string) ([]WriteOp, *Receipt, error) {
		err := fmt.Errorf("%s", message)
		return nil, &Receipt{TxID: signed.Base.TxId, Status: "FAILED", Error: err.Error()}, err
	}
	if job.BtcProtocolVersion != 0 || job.Status != frostWithdrawJobStatusSigned {
		return fail("invalid legacy signed replay")
	}
	if signed.Chain != "" && chain.NormalizeChain(signed.Chain) != chain.ChainBTC || signed.Asset != "" && !strings.EqualFold(signed.Asset, job.Asset) {
		return fail("legacy BTC replay chain/asset mismatch")
	}
	claimed := proto.Clone(job).(*pb.FrostWithdrawJobState)
	claimed.Status = frostWithdrawJobStatusClaimed
	if err := validateWithdrawJobStateForSigned(claimed, signed, chain.ChainBTC, job.Asset, signed.WithdrawIds); err != nil {
		return fail(err.Error())
	}
	if !bytes.Equal(signed.TemplateData, job.TemplateData) || signed.BatchSeq != job.BatchSeq || signed.IncludesHandover != job.IncludesHandover {
		return fail("legacy BTC signed template differs")
	}
	raw, found, err := sv.Get(keys.KeyFrostPermanentSignedPackage(job.JobId, 0))
	if err != nil {
		return fail(err.Error())
	}
	var pkg pb.FrostSignedPackage
	if !found || proto.Unmarshal(raw, &pkg) != nil || pkg.JobId != job.JobId ||
		!bytes.Equal(pkg.SignedPackageBytes, signed.SignedPackageBytes) || !equalByteSlices(pkg.GuardianSigs, signed.GuardianSigs) ||
		!signedPackageMatchesInputSigs(signed.SignedPackageBytes, signed.InputSigs) {
		return fail("legacy BTC signed package differs")
	}
	template, err := btc.FromJSON(job.TemplateData)
	if err != nil || !bytes.Equal(template.TemplateHash(), job.TemplateHash) || len(signed.ScriptPubkeys) != len(template.Inputs) {
		return fail("invalid permanent BTC template")
	}
	for i, input := range template.Inputs {
		if len(input.SpendScript) != 68 {
			return fail("legacy BTC input policy unavailable")
		}
		script, leaf, control, err := utils.BuildDualCustodyTaproot(input.SpendScript[1:33], input.SpendScript[35:67])
		if err != nil || !bytes.Equal(leaf, input.SpendScript) || !bytes.Equal(control, input.ControlBlock) || !bytes.Equal(script, signed.ScriptPubkeys[i]) {
			return fail("legacy BTC replay input script differs")
		}
	}
	return nil, &Receipt{TxID: signed.Base.TxId, Status: "SUCCEED"}, nil
}
