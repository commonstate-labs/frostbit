package workers

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/utils"
)

func testLocalIdentity(t testing.TB, privateKey string) LocalIdentity {
	t.Helper()
	identity := utils.NewKeyManager()
	if err := identity.InitKey(privateKey); err != nil {
		t.Fatalf("initialize fixture identity: %v", err)
	}
	return identity
}

type decryptOnlyIdentity struct {
	LocalIdentity
	decrypt func([]byte) ([]byte, error)
}

func (i *decryptOnlyIdentity) DecryptECIES(ciphertext []byte) ([]byte, error) {
	return i.decrypt(ciphertext)
}

type identityStateReader struct {
	*complaintTestStateReader
	scans int
	gets  int
}

func (r *identityStateReader) Get(key string) ([]byte, bool, error) {
	r.gets++
	return r.complaintTestStateReader.Get(key)
}

func (r *identityStateReader) Scan(prefix string, fn func(string, []byte) bool) error {
	r.scans++
	return r.complaintTestStateReader.Scan(prefix, fn)
}

type identityShareVerifier struct {
	DKGExecutor
	t testing.TB
}

func (v *identityShareVerifier) VerifyShare(share []byte, commitments [][]byte, senderIndex, receiverIndex int) bool {
	v.t.Helper()
	if len(commitments) != 1 || !bytes.Equal(share, commitments[0]) || receiverIndex != 1 || senderIndex != 0 {
		v.t.Fatalf("decrypted share or verification context changed: share=%x commitments=%x sender=%d receiver=%d", share, commitments, senderIndex, receiverIndex)
	}
	return true
}

func TestTransitionWorkerUsesInjectedDecryptIdentity(t *testing.T) {
	                                                                              
	                                                                    
	for _, committeeSize := range []int{1, 19, 20, 21} {
		t.Run(fmt.Sprintf("committee-%d", committeeSize), func(t *testing.T) {
			reader := &identityStateReader{complaintTestStateReader: &complaintTestStateReader{data: make(map[string][]byte)}}
			session := &DKGSession{Chain: "btc", VaultID: 1, EpochID: 1, SessionID: "injected-identity", MyIndex: 1, Committee: []string{"local"}}
			for idx := 1; idx < committeeSize; idx++ {
				dealer := fmt.Sprintf("dealer-%d", idx)
				session.Committee = append(session.Committee, dealer)
				payload := []byte(dealer)
				reader.data[keys.KeyFrostVaultDkgCommit(session.Chain, session.VaultID, session.EpochID, session.SessionID, dealer)] = mustComplaintProto(t, &pb.FrostVaultDkgCommitment{CommitmentPoints: [][]byte{payload}})
				reader.data[keys.KeyFrostVaultDkgShare(session.Chain, session.VaultID, session.EpochID, session.SessionID, dealer, "local")] = mustComplaintProto(t, &pb.FrostVaultDkgShare{DealerId: dealer, ReceiverId: "local", Ciphertext: payload})
			}
			decryptCalls := 0
			identity := &decryptOnlyIdentity{decrypt: func(ciphertext []byte) ([]byte, error) {
				decryptCalls++
				return append([]byte(nil), ciphertext...), nil
			}}
			worker := &TransitionWorker{stateReader: reader, localAddress: "local", localIdentity: identity, Logger: complaintTestLogger{}}
			shares, err := worker.collectAndVerifyShares(context.Background(), &identityShareVerifier{t: t}, session)
			if err != nil {
				t.Fatalf("collect shares through injected identity: %v", err)
			}
			if worker.localIdentity != identity || len(shares) != committeeSize-1 || decryptCalls != committeeSize-1 {
				t.Fatalf("identity reuse or decrypt work changed: shares=%d decryptCalls=%d committee=%d", len(shares), decryptCalls, committeeSize)
			}
			if reader.scans != 2 || reader.gets != 0 {
				t.Fatalf("share collection IO changed: scans=%d pointReads=%d", reader.scans, reader.gets)
			}
		})
	}
}

func TestTransitionWorkerRequiresExplicitDecryptIdentity(t *testing.T) {
	reader := &complaintTestStateReader{data: make(map[string][]byte)}
	worker := &TransitionWorker{stateReader: reader, cryptoFactory: complaintTestCryptoFactory{}, localAddress: "local", Logger: complaintTestLogger{}}
	session := &DKGSession{Chain: "btc", SessionID: "missing-identity", Committee: []string{"local"}}
	_, err := worker.collectAndVerifyShares(context.Background(), nil, session)
	if err == nil || !strings.Contains(err.Error(), "local identity not configured") {
		t.Fatalf("collect shares without identity: got=%v", err)
	}
	if err := worker.verifyAndComplain(context.Background(), session); err == nil || !strings.Contains(err.Error(), "local identity not configured") {
		t.Fatalf("verify shares without identity: got=%v", err)
	}
}
