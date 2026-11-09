package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"dex/frost/guardian"
	"dex/frost/runtime/types"
	"dex/keys"
	"dex/pb"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"google.golang.org/protobuf/proto"
)

type formalDualInner struct{ result *types.SignedPackage }

func (i *formalDualInner) StartSigningSession(context.Context, *types.SigningSessionParams) (string, error) {
	return "same-inner-session", nil
}
func (i *formalDualInner) GetSessionStatus(id string) (*types.SessionStatus, error) {
	return &types.SessionStatus{SessionID: id, State: "COMPLETE", Progress: 1}, nil
}
func (i *formalDualInner) CancelSession(string) error { return nil }
func (i *formalDualInner) WaitForCompletion(context.Context, string, time.Duration) (*types.SignedPackage, error) {
	return i.result, nil
}

type formalDualReader map[string][]byte

func (r formalDualReader) Get(key string) ([]byte, bool, error) { v, ok := r[key]; return v, ok, nil }
func (r formalDualReader) Scan(string, func(string, []byte) bool) error {
	return fmt.Errorf("custody signing must not scan consensus state")
}

type formalDualClient struct {
	sign func(context.Context, guardian.Request) ([][]byte, error)
}

func (c *formalDualClient) Sign(ctx context.Context, r guardian.Request) ([][]byte, error) {
	return c.sign(ctx, r)
}

func formalDualServiceFixture(t *testing.T) (*DualCustodySigningService, *types.SigningSessionParams, *formalDualClient, []byte) {
	t.Helper()
	secret, _ := btcec.PrivKeyFromBytes([]byte{31})
	hash := sha256.Sum256([]byte("finalized dual-custody template input"))
	signature, err := schnorr.Sign(secret, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	encoded := signature.Serialize()
	templateHash := bytes.Repeat([]byte{7}, 32)
	reader := formalDualReader{}
	reader[keys.KeyFrostVaultConfig("btc", 0)], err = proto.Marshal(&pb.FrostVaultConfig{Chain: "btc", GuardianPubkey: schnorr.SerializePubKey(secret.PubKey())})
	if err != nil {
		t.Fatal(err)
	}
	reader[keys.KeyFrostWithdrawJob("formal-dual-job")], err = proto.Marshal(&pb.FrostWithdrawJobState{JobId: "formal-dual-job", TemplateHash: templateHash})
	if err != nil {
		t.Fatal(err)
	}
	client := &formalDualClient{sign: func(_ context.Context, r guardian.Request) ([][]byte, error) {
		if r.Chain != "btc" || r.JobID != "formal-dual-job" || r.Epoch != 1 || !bytes.Equal(r.TemplateHash, templateHash) {
			return nil, fmt.Errorf("request was not independently identified")
		}
		return [][]byte{encoded}, nil
	}}
	inner := &formalDualInner{result: &types.SignedPackage{JobID: "formal-dual-job", Signatures: [][]byte{{1}}, TemplateHash: templateHash}}
	service := NewDualCustodySigningService(inner, reader, client)
	params := &types.SigningSessionParams{RequireGuardian: true, JobID: "formal-dual-job", Chain: "btc", KeyEpoch: 1, SignAlgo: pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340, Messages: [][]byte{hash[:]}}
	return service, params, client, encoded
}

func TestFormalDualCustodyServiceWaitsWithoutDeliveringPartial(t *testing.T) {
	service, params, client, signature := formalDualServiceFixture(t)
	id, err := service.StartSigningSession(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	client.sign = func(context.Context, guardian.Request) ([][]byte, error) { return nil, fmt.Errorf("guardian offline") }
	result, err := service.WaitForCompletion(context.Background(), id, time.Second)
	if err == nil || result != nil {
		t.Fatal("operator-only result was delivered while guardian offline")
	}
	status, err := service.GetSessionStatus(id)
	if err != nil || status.State != "WAITING_GUARDIAN" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	client.sign = func(context.Context, guardian.Request) ([][]byte, error) { return [][]byte{signature}, nil }
	result, err = service.WaitForCompletion(context.Background(), id, time.Second)
	if err != nil || result == nil || len(result.GuardianSignatures) != 1 {
		t.Fatalf("retry failed: %v", err)
	}
	result.GuardianSignatures[0][0] ^= 1
	again, err := service.WaitForCompletion(context.Background(), id, time.Second)
	if err != nil || !bytes.Equal(again.GuardianSignatures[0], signature) {
		t.Fatal("caller mutation corrupted completed authorization cache")
	}
}

func TestFormalDualCustodyServiceRejectsWrongGuardianMaterial(t *testing.T) {
	for _, name := range []string{"missing", "extra", "wrong-key", "wrong-message"} {
		t.Run(name, func(t *testing.T) {
			service, params, client, signature := formalDualServiceFixture(t)
			if name == "wrong-message" {
				params.Messages = [][]byte{bytes.Repeat([]byte{1}, 32)}
			}
			client.sign = func(context.Context, guardian.Request) ([][]byte, error) {
				switch name {
				case "missing":
					return nil, nil
				case "extra":
					return [][]byte{signature, signature}, nil
				case "wrong-key":
					key, _ := btcec.PrivKeyFromBytes([]byte{43})
					sig, _ := schnorr.Sign(key, params.Messages[0])
					return [][]byte{sig.Serialize()}, nil
				default:
					return [][]byte{signature}, nil
				}
			}
			id, err := service.StartSigningSession(context.Background(), params)
			if err != nil {
				t.Fatal(err)
			}
			if result, err := service.WaitForCompletion(context.Background(), id, time.Second); err == nil || result != nil {
				t.Fatal("invalid guardian material delivered a completed package")
			}
		})
	}
}

func TestFormalDualCustodyServiceRejectsUnknownContext(t *testing.T) {
	service, _, _, _ := formalDualServiceFixture(t)
	if result, err := service.WaitForCompletion(context.Background(), "unregistered", time.Second); err == nil || result != nil {
		t.Fatal("unregistered session returned operator-only package without guardian policy context")
	}
}

func TestFormalDualCustodyServiceRejectsConflictingSessionReuse(t *testing.T) {
	service, params, _, _ := formalDualServiceFixture(t)
	id, err := service.StartSigningSession(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.WaitForCompletion(context.Background(), id, time.Second); err != nil {
		t.Fatal(err)
	}
	changed := *params
	changed.Messages = [][]byte{bytes.Repeat([]byte{9}, 32)}
	if _, err := service.StartSigningSession(context.Background(), &changed); err == nil {
		t.Fatal("same session accepted a conflicting canonical intent and retained old completion")
	}
}
