package vm

import (
	"bytes"
	iface "dex/interfaces"
	"dex/keys"
	"dex/pb"
	"dex/utils"
	"fmt"

	"google.golang.org/protobuf/proto"
)

                                                                   
type CertSignTxHandler struct {
	certTracker iface.CertChainTracker
}

func (h *CertSignTxHandler) Kind() string { return "cert_sign" }

func (h *CertSignTxHandler) SetCertChainTracker(tracker iface.CertChainTracker) {
	h.certTracker = tracker
}

func (h *CertSignTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	csTx := tx.GetCertSignTx()
	if csTx == nil || csTx.Base == nil {
		return nil, nil, fmt.Errorf("invalid cert_sign tx")
	}
	if len(csTx.BlsSignature) == 0 {
		return nil, &Receipt{TxID: csTx.Base.TxId, Status: "FAILED", Error: "empty BLS signature"}, fmt.Errorf("empty BLS signature")
	}
	if len(csTx.CheckpointDigest) == 0 {
		return nil, &Receipt{TxID: csTx.Base.TxId, Status: "FAILED", Error: "empty checkpoint digest"}, fmt.Errorf("empty checkpoint digest")
	}

	fromAddr := csTx.Base.FromAddress
	pubKeyBytes, signerIndex, err := h.resolveSigner(csTx.CheckpointHeight, fromAddr, csTx.SignerIndex, sv)
	if err != nil {
		return nil, &Receipt{TxID: csTx.Base.TxId, Status: "FAILED", Error: err.Error()}, err
	}
	if csTx.SignerIndex != signerIndex {
		err := fmt.Errorf("signer index mismatch: tx=%d expected=%d", csTx.SignerIndex, signerIndex)
		return nil, &Receipt{TxID: csTx.Base.TxId, Status: "FAILED", Error: err.Error()}, err
	}
	if h.certTracker != nil {
		expectedDigest, found := h.certTracker.GetPendingCertDigest(csTx.CheckpointHeight)
		if !found || !bytes.Equal(csTx.CheckpointDigest, expectedDigest) {
			err := fmt.Errorf("checkpoint digest mismatch for height %d", csTx.CheckpointHeight)
			return nil, &Receipt{TxID: csTx.Base.TxId, Status: "FAILED", Error: err.Error()}, err
		}
	}

	pubKey, err := utils.DeserializePublicKey(pubKeyBytes)
	if err != nil {
		return nil, &Receipt{TxID: csTx.Base.TxId, Status: "FAILED", Error: "bad BLS pubkey"},
			fmt.Errorf("deserialize BLS pubkey for %s: %w", fromAddr, err)
	}
	if err := utils.BLSVerifySignature(pubKey, string(csTx.CheckpointDigest), csTx.BlsSignature); err != nil {
		return nil, &Receipt{TxID: csTx.Base.TxId, Status: "FAILED", Error: "BLS verify failed"},
			fmt.Errorf("BLS verify failed for %s: %w", fromAddr, err)
	}

	return nil, &Receipt{TxID: csTx.Base.TxId, Status: "SUCCEED"}, nil
}

func (h *CertSignTxHandler) resolveSigner(checkpointHeight uint64, fromAddr string, fallbackIndex uint32, sv StateView) ([]byte, uint32, error) {
	if h.certTracker != nil {
		pubKeyBytes, signerIndex, found := h.certTracker.GetPendingMinerByAddress(checkpointHeight, fromAddr)
		if !found {
			return nil, 0, fmt.Errorf("signer %s not in checkpoint signer set", fromAddr)
		}
		if len(pubKeyBytes) == 0 {
			return nil, 0, fmt.Errorf("signer %s has no BLS public key in checkpoint signer set", fromAddr)
		}
		return pubKeyBytes, signerIndex, nil
	}

	acctData, exists, _ := sv.Get(keys.KeyAccount(fromAddr))
	if !exists || len(acctData) == 0 {
		return nil, 0, fmt.Errorf("account %s not found", fromAddr)
	}
	var acct pb.Account
	if err := proto.Unmarshal(acctData, &acct); err != nil {
		return nil, 0, fmt.Errorf("unmarshal account %s: %w", fromAddr, err)
	}
	blsKey := int32(pb.SignAlgo_SIGN_ALGO_BLS_BN256_G2)
	if acct.PublicKeys == nil || len(acct.PublicKeys.Keys[blsKey]) == 0 {
		return nil, 0, fmt.Errorf("account %s has no BLS public key", fromAddr)
	}
	return acct.PublicKeys.Keys[blsKey], fallbackIndex, nil
}

func (h *CertSignTxHandler) Apply(tx *pb.AnyTx) error { return ErrNotImplemented }
