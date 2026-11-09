package txverify

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"dex/pb"
	"dex/utils"
	"strings"
	"testing"
)

const testChainID uint64 = 1

                                                                                                                  
                                                                                     
func buildSignedTransfer(t *testing.T) *pb.AnyTx {
	t.Helper()
	alice := utils.GenerateTestIdentity(1)
	bob := utils.GenerateTestIdentity(2)

	tx := &pb.AnyTx{
		Content: &pb.AnyTx_Transaction{
			Transaction: &pb.Transaction{
				Base: &pb.BaseMessage{
					FromAddress: alice.Address,
					Nonce:       1,
					Fee:         "1000",
					ChainId:     testChainID,
					PublicKeys: &pb.PublicKeys{
						Keys: map[int32][]byte{
							int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX): alice.PublicKey,
						},
					},
				},
				To:           bob.Address,
				Amount:       "100",
				TokenAddress: "FB",
			},
		},
	}

	txID := tx.GenerateTxID()
	tx.GetBase().TxId = txID

	digest := sha256.Sum256([]byte(txID))
	r, s, err := ecdsa.Sign(rand.Reader, alice.PrivateKey.ToECDSA(), digest[:])
	if err != nil {
		t.Fatalf("ecdsa sign: %v", err)
	}
	sig := make([]byte, 64)
	rB, sB := r.Bytes(), s.Bytes()
	copy(sig[32-len(rB):32], rB)
	copy(sig[64-len(sB):64], sB)
	tx.GetBase().Signature = sig
	return tx
}

func TestVerify_HappyPath(t *testing.T) {
	tx := buildSignedTransfer(t)
	if err := Verify(tx, testChainID); err != nil {
		t.Fatalf("valid tx should pass, got %v", err)
	}
}

func TestSignWithPrivateKey_RecomputesTxIDAfterPublicKey(t *testing.T) {
	alice := utils.GenerateTestIdentity(1)
	bob := utils.GenerateTestIdentity(2)
	tx := &pb.AnyTx{
		Content: &pb.AnyTx_Transaction{
			Transaction: &pb.Transaction{
				Base: &pb.BaseMessage{
					FromAddress:  alice.Address,
					Nonce:        1,
					Fee:          "1000",
					ChainId:      testChainID,
					TargetHeight: 7,
					OnlyProposer: alice.Address,
				},
				To:           bob.Address,
				Amount:       "100",
				TokenAddress: "FB",
			},
		},
	}

	oldTxID := tx.GenerateTxID()
	tx.GetBase().TxId = oldTxID
	newTxID, err := SignWithPrivateKey(tx, alice.PrivateKeyHex)
	if err != nil {
		t.Fatalf("sign tx: %v", err)
	}
	if newTxID == oldTxID {
		t.Fatal("txID should be recomputed after public key is injected")
	}
	if err := Verify(tx, testChainID); err != nil {
		t.Fatalf("signed tx should pass verify, got %v", err)
	}
}

                                                                                           
                                                                                                
func TestVerify_TamperedPayloadRejected(t *testing.T) {
	tx := buildSignedTransfer(t)
	attacker := utils.GenerateTestIdentity(99)
	tx.GetTransaction().To = attacker.Address                                              

	err := Verify(tx, testChainID)
	if err == nil {
		t.Fatalf("tampered tx must be rejected")
	}
	if !strings.Contains(err.Error(), "tx_id mismatch") {
		t.Fatalf("expected tx_id mismatch, got %v", err)
	}
}

func TestVerify_ExecutionGuardFieldsAreCanonicalIntent(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*pb.BaseMessage)
	}{
		{name: "target height", mutate: func(base *pb.BaseMessage) { base.TargetHeight = 42 }},
		{name: "only proposer", mutate: func(base *pb.BaseMessage) { base.OnlyProposer = "fbc1qotherminer" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tx := buildSignedTransfer(t)
			tc.mutate(tx.GetBase())
			err := Verify(tx, testChainID)
			if err == nil || !strings.Contains(err.Error(), "tx_id mismatch") {
				t.Fatalf("tampered execution guard must invalidate canonical tx id, got %v", err)
			}
		})
	}
}

func TestVerify_CacheDoesNotBypassPayloadTamper(t *testing.T) {
	tx := buildSignedTransfer(t)
	if err := Verify(tx, testChainID); err != nil {
		t.Fatalf("valid tx should pass before tamper, got %v", err)
	}

	attacker := utils.GenerateTestIdentity(99)
	tx.GetTransaction().To = attacker.Address
	err := Verify(tx, testChainID)
	if err == nil || !strings.Contains(err.Error(), "tx_id mismatch") {
		t.Fatalf("cached valid tx must not bypass payload tamper, got %v", err)
	}
}

func TestVerify_CacheDoesNotBypassSignatureTamper(t *testing.T) {
	tx := buildSignedTransfer(t)
	if err := Verify(tx, testChainID); err != nil {
		t.Fatalf("valid tx should pass before tamper, got %v", err)
	}

	tx.GetBase().Signature[0] ^= 0xff
	err := Verify(tx, testChainID)
	if err == nil || !strings.Contains(err.Error(), "invalid ECDSA signature") {
		t.Fatalf("cached valid tx must not bypass signature tamper, got %v", err)
	}
}

func TestVerify_WrongChainIDRejected(t *testing.T) {
	tx := buildSignedTransfer(t)
	if err := Verify(tx, 999); err == nil || !strings.Contains(err.Error(), "InvalidChainID") {
		t.Fatalf("wrong chainID must be rejected, got %v", err)
	}
}

func TestVerify_MissingSignatureRejected(t *testing.T) {
	tx := buildSignedTransfer(t)
	tx.GetBase().Signature = nil
	if err := Verify(tx, testChainID); err == nil || !strings.Contains(err.Error(), "missing signature") {
		t.Fatalf("missing signature must be rejected, got %v", err)
	}
}

func TestVerify_PubkeyAddrMismatchRejected(t *testing.T) {
	tx := buildSignedTransfer(t)
	                                                                                    
	other := utils.GenerateTestIdentity(77)
	tx.GetBase().PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)] = other.PublicKey
	                                                                          
	tx.GetBase().TxId = tx.GenerateTxID()
	if err := Verify(tx, testChainID); err == nil || !strings.Contains(err.Error(), "public key does not match from_address") {
		t.Fatalf("pubkey/from_address mismatch must be rejected, got %v", err)
	}
}

func buildSignedWitnessVote(t *testing.T) *pb.AnyTx {
	t.Helper()
	alice := utils.GenerateTestIdentity(1)

	tx := &pb.AnyTx{
		Content: &pb.AnyTx_WitnessVoteTx{
			WitnessVoteTx: &pb.WitnessVoteTx{
				Base: &pb.BaseMessage{
					FromAddress: alice.Address,
					Nonce:       1,
					Fee:         "1000",
					ChainId:     testChainID,
				},
				Vote: &pb.WitnessVote{
					RequestId:      "0xreq",
					WitnessAddress: alice.Address,
					VoteType:       pb.WitnessVoteType_VOTE_PASS,
				},
			},
		},
	}
	if _, err := SignWithPrivateKey(tx, alice.PrivateKeyHex); err != nil {
		t.Fatalf("sign witness vote: %v", err)
	}
	return tx
}

func TestVerify_WitnessVoteInnerTxIDIgnored(t *testing.T) {
	tx := buildSignedWitnessVote(t)
	tx.GetWitnessVoteTx().Vote.TxId = tx.GetBase().TxId
	if err := Verify(tx, testChainID); err != nil {
		t.Fatalf("witness vote with derived inner tx_id should pass, got %v", err)
	}
}

func TestVerify_WitnessVotePayloadTamperRejected(t *testing.T) {
	tx := buildSignedWitnessVote(t)
	tx.GetWitnessVoteTx().Vote.RequestId = "0xchanged"
	err := Verify(tx, testChainID)
	if err == nil || !strings.Contains(err.Error(), "tx_id mismatch") {
		t.Fatalf("tampered witness vote must be rejected, got %v", err)
	}
}

func buildSignedArbitrationVote(t *testing.T) *pb.AnyTx {
	t.Helper()
	alice := utils.GenerateTestIdentity(1)

	tx := &pb.AnyTx{
		Content: &pb.AnyTx_ArbitrationVoteTx{
			ArbitrationVoteTx: &pb.ArbitrationVoteTx{
				Base: &pb.BaseMessage{
					FromAddress: alice.Address,
					Nonce:       1,
					Fee:         "1000",
					ChainId:     testChainID,
				},
				ChallengeId: "0xchallenge",
				Vote: &pb.WitnessVote{
					RequestId:      "0xreq",
					WitnessAddress: alice.Address,
					VoteType:       pb.WitnessVoteType_VOTE_PASS,
				},
			},
		},
	}
	if _, err := SignWithPrivateKey(tx, alice.PrivateKeyHex); err != nil {
		t.Fatalf("sign arbitration vote: %v", err)
	}
	return tx
}

func TestVerify_ArbitrationVoteInnerTxIDIgnored(t *testing.T) {
	tx := buildSignedArbitrationVote(t)
	tx.GetArbitrationVoteTx().Vote.TxId = tx.GetBase().TxId
	if err := Verify(tx, testChainID); err != nil {
		t.Fatalf("arbitration vote with derived inner tx_id should pass, got %v", err)
	}
}
