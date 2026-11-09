package vm

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math/big"

	"dex/frost/artifact"
	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

func btcIsolationSignedPackageHash(signed *pb.FrostWithdrawSignedTx) []byte {
	if signed == nil {
		return nil
	}
	canonical := proto.Clone(signed).(*pb.FrostWithdrawSignedTx)
	canonical.Base = nil
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(canonical)
	if err != nil {
		return nil
	}
	hash := sha256.Sum256(raw)
	return hash[:]
}

func (h *FrostWithdrawSignedTxHandler) dryRunBTCIsolationSigned(signed *pb.FrostWithdrawSignedTx, job *pb.FrostWithdrawJobState, sv StateView) (ops []WriteOp, receipt *Receipt, resultErr error) {
	fail := func(err error) ([]WriteOp, *Receipt, error) {
		return nil, &Receipt{TxID: signed.Base.TxId, Status: "FAILED", Error: err.Error()}, err
	}
	active, err := chain.BTCIsolationActive(sv.Get)
	if err != nil {
		return fail(err)
	}
	if !active {
		return fail(fmt.Errorf("BTC v3 isolation is not active"))
	}
	hash := btcIsolationSignedPackageHash(signed)
	if job.Status == frostWithdrawJobStatusSigned {
		stored, found, err := sv.Get(keys.KeyFrostBtcSignedPackageHash(job.JobId))
		if err != nil {
			return fail(err)
		}
		if !found || !bytes.Equal(stored, hash) {
			return fail(fmt.Errorf("BTC child is already signed by a different package"))
		}
		return nil, &Receipt{TxID: signed.Base.TxId, Status: "SUCCEED"}, nil
	}
	if len(job.WithdrawIds) != 1 {
		return fail(fmt.Errorf("invalid BTC child parent"))
	}
	parent, err := readBTCIsolationParent(sv, job.WithdrawIds[0])
	if err != nil {
		return fail(err)
	}
	l, err := readBTCIsolationLedger(parent)
	if err != nil {
		return fail(err)
	}
	gross, err := parseBTCIsolationAmount("child gross", job.BtcGrossAmount)
	if err != nil || gross == 0 || gross > l.claimed {
		return fail(fmt.Errorf("BTC claimed amount mismatch"))
	}
	if _, err := parseBTCIsolationAmount("child fee", job.BtcFeeAmount); err != nil {
		return fail(err)
	}
	validated, raw, txid, parents, err := validateBTCIsolationSigned(sv, signed, job)
	if err != nil {
		return fail(err)
	}
	if part, found, err := sv.Get(keys.KeyFrostBtcWithdrawPart(parent.WithdrawID, job.BtcPartSeq)); err != nil {
		return fail(err)
	} else if !found || string(part) != job.JobId {
		return fail(fmt.Errorf("BTC parent child index mismatch"))
	}
	snap := sv.Snapshot()
	defer func() {
		if resultErr != nil || receipt == nil || receipt.Status != "SUCCEED" {
			_ = sv.Revert(snap)
		}
	}()
	if _, err := storeFrostArtifact(sv, artifact.Metadata{ArtifactID: txid, Kind: artifact.KindWithdraw,
		Chain: chain.ChainBTC, VaultID: job.VaultId, Epoch: job.KeyEpoch, Height: signed.Base.ExecutedHeight,
		SourceID: job.JobId, ParentIDs: parents}, raw); err != nil {
		return fail(err)
	}
	created, amount, err := ensureBTCChangeUTXO(sv, job.VaultId, validated.template, signed.InputSigs, signed.Base.ExecutedHeight)
	if err != nil {
		return fail(err)
	}
	if created && amount > 0 {
		if err := addVaultAvailableBalance(sv, chain.ChainBTC, job.Asset, job.VaultId, new(big.Int).SetUint64(amount)); err != nil {
			return fail(err)
		}
	}
	input := validated.utxos[0]
	sv.Del(keys.KeyFrostBtcUtxo(job.VaultId, input.Txid, input.Vout))
	sv.Del(keys.KeyFrostBtcLockedUtxo(job.VaultId, input.Txid, input.Vout))
	sv.Del(keys.KeyFrostBtcUtxoArtifact(job.VaultId, input.Txid, input.Vout))
	l.claimed -= gross
	l.signed += gross
	writeBTCIsolationLedger(parent, l)
	if err := persistBTCIsolationParent(sv, parent); err != nil {
		return fail(err)
	}
	job.Status, job.SignedTxId = frostWithdrawJobStatusSigned, signed.Base.TxId
	jobData, err := proto.Marshal(job)
	if err != nil {
		return fail(err)
	}
	pkg := &pb.FrostSignedPackage{JobId: job.JobId, SignedPackageBytes: signed.SignedPackageBytes,
		GuardianSigs: signed.GuardianSigs, SubmitHeight: signed.Base.ExecutedHeight, Submitter: signed.Base.FromAddress}
	pkgData, err := proto.Marshal(pkg)
	if err != nil {
		return fail(err)
	}
	sv.Set(keys.KeyFrostPermanentWithdrawJob(job.JobId), jobData)
	sv.Set(keys.KeyArchiveFrostWithdrawJob(job.JobId), jobData)
	sv.Set(keys.KeyFrostPermanentSignedPackage(job.JobId, 0), pkgData)
	sv.Set(keys.KeyArchiveFrostSignedPackage(job.JobId, 0), pkgData)
	sv.Set(keys.KeyFrostPermanentWithdrawArtifact(job.JobId), []byte(txid))
	sv.Set(keys.KeyFrostWithdrawArtifact(job.JobId), []byte(txid))
	sv.Set(keys.KeyFrostBtcSignedPackageHash(job.JobId), hash)
	sv.Del(keys.KeyFrostWithdrawJob(job.JobId))
	sv.Del(keys.KeyFrostWithdrawActiveJob(chain.ChainBTC, job.VaultId, job.KeyEpoch, job.JobId))
	sv.Del(keys.KeyFrostBtcWithdrawActiveChild(job.Asset, job.JobId))
	return nil, &Receipt{TxID: signed.Base.TxId, Status: "SUCCEED", BlockHeight: signed.Base.ExecutedHeight}, nil
}

                                                                              
                                                                               
                                    
func validateBTCIsolationSigned(sv StateView, signed *pb.FrostWithdrawSignedTx, job *pb.FrostWithdrawJobState) (*validatedBTCWithdraw, []byte, string, []string, error) {
	if signed == nil || signed.Base == nil || job == nil || job.BtcProtocolVersion != 3 || len(job.WithdrawIds) != 1 ||
		chain.NormalizeChain(signed.Chain) != chain.ChainBTC || signed.Asset != job.Asset || signed.BatchSeq != "" || signed.IncludesHandover {
		return nil, nil, "", nil, fmt.Errorf("invalid BTC v3 signed context")
	}
	if err := validateWithdrawJobStateForSigned(job, signed, chain.ChainBTC, job.Asset, signed.WithdrawIds); err != nil {
		return nil, nil, "", nil, err
	}
	if !bytes.Equal(signed.TemplateData, job.TemplateData) || !bytes.Equal(signed.TemplateHash, job.TemplateHash) {
		return nil, nil, "", nil, fmt.Errorf("BTC signed template differs from claim")
	}
	validated, err := validateBTCClaimedWithdrawTemplate(sv, job.JobId, job)
	if err != nil {
		return nil, nil, "", nil, err
	}
	if len(validated.template.Inputs) != 1 || len(validated.utxos) != 1 || len(signed.InputSigs) != 1 || len(signed.GuardianSigs) != 1 ||
		len(job.BtcOriginId) != 32 || !bytes.Equal(job.BtcOriginId, validated.utxos[0].OriginId) {
		return nil, nil, "", nil, fmt.Errorf("BTC v3 input/origin mismatch")
	}
	if !signedPackageMatchesInputSigs(signed.SignedPackageBytes, signed.InputSigs) || !equalByteSlices(signed.ScriptPubkeys, validated.scriptPubKeys) {
		return nil, nil, "", nil, fmt.Errorf("BTC v3 signed package/script mismatch")
	}
	hashes, err := validated.template.ComputeTaprootSighash(validated.scriptPubKeys, btc.SighashDefault)
	if err != nil {
		return nil, nil, "", nil, err
	}
	operator := validated.vault.GroupPubkey
	if len(validated.utxos[0].Tweak) == 32 {
		operator, err = utils.ComputeTweakedPubkey(operator, validated.utxos[0].Tweak)
		if err != nil {
			return nil, nil, "", nil, err
		}
	}
	for _, item := range []struct{ key, signature []byte }{
		{operator, signed.InputSigs[0]}, {validated.guardianPubkey, signed.GuardianSigs[0]},
	} {
		valid, err := verifySignature(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, item.key, hashes[0], item.signature)
		if len(item.signature) != 64 || err != nil || !valid {
			return nil, nil, "", nil, fmt.Errorf("invalid BTC v3 signature")
		}
	}
	raw, err := validated.template.BuildSignedRawTx(signed.InputSigs, signed.GuardianSigs)
	if err != nil {
		return nil, nil, "", nil, err
	}
	id, err := validated.template.TxID()
	if err != nil {
		return nil, nil, "", nil, err
	}
	parents, err := frostBTCParentArtifactIDs(sv, job.VaultId, validated.template.Inputs)
	if err != nil {
		return nil, nil, "", nil, err
	}
	return validated, raw, id, parents, nil
}
