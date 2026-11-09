package guardian

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	frostcrypto "dex/frost/core/frost"
	"dex/pb"
)

func formalGuardianFixture(t *testing.T, resolve ResolverFunc) (*Signer, Request, *Intent) {
	t.Helper()
	keys := KeyFile{BTCPrivateKey: fmt.Sprintf("%064x", 23), AccountPrivateKey: fmt.Sprintf("%064x", 31)}
	b, a, err := publicKeys(keys)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{Chain: "btc", VaultID: 0, Epoch: 1, JobID: "formal-finalized-job", TemplateHash: bytes.Repeat([]byte{7}, 32)}
	hash := sha256.Sum256([]byte("independently finalized script sighash"))
	intent := &Intent{Request: request, SignAlgo: int32(pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340), PublicKey: b, Messages: [][]byte{hash[:]}}
	if resolve == nil {
		resolve = func(context.Context, Request) (*Intent, error) { return intent, nil }
	}
	signer, err := NewSigner(keys, b, a, resolve)
	if err != nil {
		t.Fatal(err)
	}
	return signer, request, intent
}

func TestFormalDualCustodyGuardianRejectsCallerSelectedMessages(t *testing.T) {
	calls := 0
	signer, request, _ := formalGuardianFixture(t, func(context.Context, Request) (*Intent, error) { calls++; return nil, fmt.Errorf("not finalized") })
	base, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"messages", "messageHash", "signAlgo", "publicKey", "privateKey", "rawTx"} {
		t.Run(field, func(t *testing.T) {
			var body map[string]any
			if err := json.Unmarshal(base, &body); err != nil {
				t.Fatal(err)
			}
			body[field] = "attacker-selected"
			raw, _ := json.Marshal(body)
			response := httptest.NewRecorder()
			signer.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/sign", bytes.NewReader(raw)))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("arbitrary %s accepted: status=%d", field, response.Code)
			}
		})
	}
	if calls != 0 {
		t.Fatal("caller-selected signing payload reached trusted resolver")
	}
}

func TestFormalDualCustodyGuardianBindsResolvedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		edit     func(*Intent)
		accepted bool
	}{
		{"canonical", func(*Intent) {}, true},
		{"job", func(i *Intent) { i.Request.JobID += "-different" }, false},
		{"epoch", func(i *Intent) { i.Request.Epoch++ }, false},
		{"chain", func(i *Intent) { i.Request.Chain = "eth" }, false},
		{"vault", func(i *Intent) { i.Request.VaultID++ }, false},
		{"template", func(i *Intent) { i.Request.TemplateHash = bytes.Repeat([]byte{9}, 32) }, false},
		{"guardian", func(i *Intent) { i.PublicKey = bytes.Repeat([]byte{1}, 32) }, false},
		{"algorithm", func(i *Intent) { i.SignAlgo = int32(pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128) }, false},
		{"empty", func(i *Intent) { i.Messages = nil }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signer, request, intent := formalGuardianFixture(t, nil)
			tc.edit(intent)
			signatures, err := signer.Sign(context.Background(), request)
			if (err == nil) != tc.accepted {
				t.Fatalf("accepted=%v want=%v err=%v", err == nil, tc.accepted, err)
			}
			if err == nil {
				if len(signatures) != 1 {
					t.Fatal("incomplete result")
				}
				valid, e := frostcrypto.Verify(pb.SignAlgo(intent.SignAlgo), intent.PublicKey, intent.Messages[0], signatures[0])
				if e != nil || !valid {
					t.Fatal("produced signature does not match independently resolved message")
				}
			}
		})
	}
}
