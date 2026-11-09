package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dex/pb"
	"dex/txverify"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

func TestRegistrationBaseUsesExplorerNonceVerbatim(t *testing.T) {
	publicKey := []byte{2, 3, 4}
	base := registrationBase("hot", 42, 99, publicKey, "1000")
	if base.Nonce != 42 {
		t.Fatalf("nonce=%d want current expected nonce 42", base.Nonce)
	}
	if base.ChainId != 99 || base.FromAddress != "hot" || base.Fee != "1000" {
		t.Fatalf("unexpected base: %+v", base)
	}
	got := base.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1_DEX)]
	if !bytes.Equal(got, publicKey) {
		t.Fatalf("public key=%x want %x", got, publicKey)
	}
	publicKey[0] = 9
	if got[0] == 9 {
		t.Fatal("registration base retained caller-owned public key storage")
	}
}

func TestExistingRegistrationIsNoOpOnlyForExactPublicIntent(t *testing.T) {
	status := &minerAccountStatus{
		Address:               "fbc1miner",
		Exists:                true,
		IsMiner:               true,
		MinerIdentityComplete: true,
		IP:                    "203.0.113.10:6000",
		RewardAddress:         "fbc1reward",
	}
	ok, err := validateExistingMinerRegistration(status, "fbc1miner", "fbc1reward", "203.0.113.10:6000")
	if err != nil || !ok {
		t.Fatalf("exact registration was not a no-op: ok=%v err=%v", ok, err)
	}
	if ok, err := validateExistingMinerRegistration(status, "fbc1miner", "fbc1other", "203.0.113.10:6000"); err == nil || ok {
		t.Fatalf("reward mismatch accepted: ok=%v err=%v", ok, err)
	}
	if ok, err := validateExistingMinerRegistration(status, "fbc1miner", "fbc1reward", "203.0.113.11:6000"); err == nil || ok {
		t.Fatalf("endpoint mismatch accepted: ok=%v err=%v", ok, err)
	}
	status.MinerIdentityComplete = false
	if ok, err := validateExistingMinerRegistration(status, "fbc1miner", "fbc1reward", "203.0.113.10:6000"); err == nil || ok {
		t.Fatalf("incomplete identity accepted: ok=%v err=%v", ok, err)
	}
}

func TestRegistrationAttemptBlocksNewNonceAndResubmitsExactRawTx(t *testing.T) {
	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKey("0000000000000000000000000000000000000000000000000000000000000001"); err != nil {
		t.Fatal(err)
	}
	address := keyManager.GetAddress()
	base := &pb.BaseMessage{FromAddress: address, ChainId: 7, Nonce: 4, Fee: "1000"}
	anyTx := &pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: &pb.MinerTx{
		Base: base, Op: pb.OrderOp_ADD, Amount: "3200000000", RewardAddress: address, Endpoint: "203.0.113.10:6000",
	}}}
	txID, err := txverify.SignWithKeyManager(anyTx, keyManager)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(anyTx)
	if err != nil {
		t.Fatal(err)
	}
	attempt := registrationAttempt{
		Version: 1, Address: address, RewardAddress: address, Endpoint: "203.0.113.10:6000",
		Amount: "3200000000", Fee: "1000", ChainID: 7, Nonce: 4, TxID: txID,
		RawTx: base64.StdEncoding.EncodeToString(raw), CreatedAt: "2026-09-24T00:00:00Z",
	}
	path := filepath.Join(t.TempDir(), "registration.json")
	if err := writeRegistrationAttempt(path, attempt); err != nil {
		t.Fatal(err)
	}
	if _, err := guardRegistrationAttempt(path, attempt.Address, attempt.RewardAddress, attempt.Endpoint, attempt.Amount, attempt.Fee, false); err == nil || !strings.Contains(err.Error(), "unresolved tx_id="+txID) {
		t.Fatalf("unresolved attempt did not block a new transaction: %v", err)
	}
	previous, err := guardRegistrationAttempt(path, attempt.Address, attempt.RewardAddress, attempt.Endpoint, attempt.Amount, attempt.Fee, true)
	if err != nil || previous == nil {
		t.Fatalf("exact resubmit was unavailable: previous=%+v err=%v", previous, err)
	}
	validated, err := validateRecordedRegistration(*previous)
	if err != nil || !bytes.Equal(validated, raw) {
		t.Fatalf("recorded transaction changed: err=%v", err)
	}
	previous.Amount = "6400000000"
	if _, err := validateRecordedRegistration(*previous); err == nil {
		t.Fatal("mutated registration intent was accepted")
	}
	previous = &attempt
	var corrupted pb.AnyTx
	if err := proto.Unmarshal(raw, &corrupted); err != nil {
		t.Fatal(err)
	}
	corrupted.GetBase().Signature[0] ^= 0xff
	corruptedRaw, err := proto.Marshal(&corrupted)
	if err != nil {
		t.Fatal(err)
	}
	previous.RawTx = base64.StdEncoding.EncodeToString(corruptedRaw)
	if _, err := validateRecordedRegistration(*previous); err == nil || !strings.Contains(err.Error(), "invalid ECDSA signature") {
		t.Fatalf("corrupted signature was accepted: %v", err)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatalf("attempt journal permissions=%v", info.Mode().Perm())
	}
}

func TestEndpointUpdateAttemptBlocksIntentDriftAndResubmitsExactRawTx(t *testing.T) {
	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKey("0000000000000000000000000000000000000000000000000000000000000001"); err != nil {
		t.Fatal(err)
	}
	address := keyManager.GetAddress()
	oldEndpoint := "192.168.1.10:6000"
	newEndpoint := "192.168.1.7:6000"
	base := registrationBase(address, 5, 51016, keyManager.PublicKeyBytes(), "1000")
	anyTx := &pb.AnyTx{Content: &pb.AnyTx_MinerInfoTx{MinerInfoTx: &pb.MinerInfoTx{Base: base, Endpoint: newEndpoint}}}
	txID, err := txverify.SignWithKeyManager(anyTx, keyManager)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(anyTx)
	if err != nil {
		t.Fatal(err)
	}
	attempt := registrationAttempt{
		Version:                 1,
		Kind:                    "endpoint_update",
		Address:                 address,
		ExpectedCurrentEndpoint: oldEndpoint,
		Endpoint:                newEndpoint,
		Fee:                     "1000",
		ChainID:                 51016,
		Nonce:                   5,
		TxID:                    txID,
		RawTx:                   base64.StdEncoding.EncodeToString(raw),
		CreatedAt:               "2026-09-24T00:00:00Z",
	}
	path := filepath.Join(t.TempDir(), "endpoint-update.json")
	if err := writeRegistrationAttempt(path, attempt); err != nil {
		t.Fatal(err)
	}
	if _, err := guardEndpointUpdateAttempt(path, address, oldEndpoint, newEndpoint, "1000", false); err == nil || !strings.Contains(err.Error(), "unresolved tx_id="+txID) {
		t.Fatalf("unresolved endpoint update did not block a new transaction: %v", err)
	}
	previous, err := guardEndpointUpdateAttempt(path, address, oldEndpoint, newEndpoint, "1000", true)
	if err != nil || previous == nil {
		t.Fatalf("exact endpoint resubmit was unavailable: previous=%+v err=%v", previous, err)
	}
	validated, err := validateRecordedEndpointUpdate(*previous)
	if err != nil || !bytes.Equal(validated, raw) {
		t.Fatalf("recorded endpoint transaction changed: err=%v", err)
	}
	if _, err := guardEndpointUpdateAttempt(path, address, "192.168.1.11:6000", newEndpoint, "1000", true); err == nil {
		t.Fatal("changed expected-current endpoint was accepted")
	}
	previous.Endpoint = "192.168.1.8:6000"
	if _, err := validateRecordedEndpointUpdate(*previous); err == nil {
		t.Fatal("changed endpoint update intent was accepted")
	}
}

func TestRegistrationAttemptLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registration.json")
	first, err := lockRegistrationAttempt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Unlock()
	if second, err := lockRegistrationAttempt(path); err == nil {
		second.Unlock()
		t.Fatal("second registration process acquired the same intent lock")
	}
}

func TestFetchMinerStatusUsesExplorerAddressEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/address" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var request addressRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Address != "fbc1miner" || request.Node != "node.example:6000" || !request.RequireRemote {
			t.Fatalf("unexpected request body: %+v", request)
		}
		_ = json.NewEncoder(w).Encode(addressResponse{Account: &minerAccountStatus{
			Address: "fbc1miner", Exists: true, IsMiner: true, MinerIdentityComplete: true,
		}})
	}))
	defer server.Close()

	status, err := fetchMinerStatus(context.Background(), server.Client(), server.URL, "node.example:6000", "fbc1miner")
	if err != nil || status == nil || !status.IsMiner {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestFetchNonceRequiresAuthoritativeExplorerNode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/wallet/nonce" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var request nonceRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Address != "fbc1miner" || !request.RequireRemote {
			t.Fatalf("unexpected request body: %+v", request)
		}
		_ = json.NewEncoder(w).Encode(nonceResponse{Nonce: 4, ChainID: 7})
	}))
	defer server.Close()

	nonce, chainID, err := fetchNonce(context.Background(), server.Client(), server.URL, "", "fbc1miner")
	if err != nil || nonce != 4 || chainID != 7 {
		t.Fatalf("nonce=%d chainID=%d err=%v", nonce, chainID, err)
	}
}
