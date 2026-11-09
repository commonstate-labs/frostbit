package main

import (
	"bytes"
	"context"
	"dex/frost/core/curve"
	"dex/frost/guardian"
	"dex/utils"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestFrostMigrationDetailReturnsVerifiedBTCInputsOutputsAndFee(t *testing.T) {
	template := &btc.BTCTemplate{
		Version: 2,
		Inputs: []btc.TxInput{{
			TxID: strings.Repeat("11", 32), Vout: 3, Amount: 125_000, Sequence: btc.DefaultSequence,
		}},
		Outputs: []btc.TxOutput{{
			Address: "tb1pdpd76eytdcdwnm62uuxve403srq0dtcls0uj488wjjy4z4lfm25sdwatf0", Amount: 124_500,
		}},
		VaultID:  4,
		KeyEpoch: 2,
	}
	operator, _ := hex.DecodeString("79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
	guardianPub, _ := hex.DecodeString("c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5")
	_, template.Inputs[0].SpendScript, template.Inputs[0].ControlBlock, _ = utils.BuildDualCustodyTaproot(operator, guardianPub)
	templateData, err := template.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	signature := bytes.Repeat([]byte{0x22}, 64)
	rawTx, err := template.BuildSignedRawTx([][]byte{signature}, [][]byte{signature})
	if err != nil {
		t.Fatal(err)
	}
	archiveBytes, err := proto.Marshal(&pb.FrostVaultMigrationArchive{
		State: &pb.FrostVaultMigrationState{
			MigrationId: "migration-btc-4-2", Chain: "BTC", VaultId: 4, OldEpoch: 1, NewEpoch: 2,
			TemplateHash: template.TemplateHash(), TemplateData: templateData, Status: "SIGNED", IntentHeight: 88,
		},
		GuardianSigs: [][]byte{signature, signature}, InputSigs: [][]byte{signature}, ScriptPubkeys: [][]byte{{0x51, 0x20, 0x01}}, RawTx: rawTx, SignedHeight: 99,
	})
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Transport: explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		var payload []byte
		switch req.URL.Path {
		case "/frost/vault/migration":
			if req.Method != http.MethodGet || req.URL.Query().Get("migration_id") != "migration-btc-4-2" {
				t.Fatalf("unexpected node request: %s %s", req.Method, req.URL.String())
			}
			payload = archiveBytes
		case "/frost/vault/config":
			if req.URL.Query().Get("chain") != "btc" {
				t.Fatalf("unexpected config request: %s", req.URL.String())
			}
			payload, _ = proto.Marshal(&pb.FrostVaultConfig{Chain: "BTC", BtcNetwork: "testnet3"})
		default:
			t.Fatalf("unexpected node request: %s %s", req.Method, req.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/x-protobuf"}},
			Body:       io.NopCloser(bytes.NewReader(payload)),
			Request:    req,
		}, nil
	})}
	srv := &server{
		client: client, timeout: time.Second,
		defaultNodes: []string{"node.example:6000"},
		allowedNodes: map[string]struct{}{"node.example:6000": {}},
	}

	recorder := httptest.NewRecorder()
	srv.handleFrostMigrationDetail(recorder, httptest.NewRequest(http.MethodGet,
		"/api/frost/vault/migration/detail?node=node.example%3A6000&migration_id=migration-btc-4-2", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response frostMigrationDetailResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.TxID == "" || response.RawTxHex == "" || response.RawSize != len(rawTx) || response.BTC == nil {
		t.Fatalf("missing transaction details: %+v", response)
	}
	if len(response.BTC.Inputs) != 1 || response.BTC.Inputs[0].TxID != strings.Repeat("11", 32) || response.BTC.Inputs[0].Vout != 3 || response.BTC.Inputs[0].Amount != "125000" {
		t.Fatalf("unexpected inputs: %+v", response.BTC.Inputs)
	}
	if response.BTC.Network != "testnet3" || len(response.BTC.Outputs) != 1 || response.BTC.Outputs[0].Amount != "124500" || response.BTC.TotalInput != "125000" || response.BTC.TotalOutput != "124500" || response.BTC.Fee != "500" {
		t.Fatalf("unexpected outputs/totals: %+v", response.BTC)
	}
}

func TestFrostMigrationDetailReturnsVerifiedAccountContractCall(t *testing.T) {
	group := curve.NewBN256Group()
	oldPub, _ := evm.CanonicalBN128PublicKey(group.SerializePoint(group.ScalarBaseMult(big.NewInt(5))))
	newPub, _ := evm.CanonicalBN128PublicKey(group.SerializePoint(group.ScalarBaseMult(big.NewInt(7))))
	guardianPub, _ := evm.CanonicalBN128PublicKey(group.SerializePoint(group.ScalarBaseMult(big.NewInt(29))))
	cfg := &pb.FrostVaultConfig{Chain: "eth", EvmChainId: 1, AuthorityProtocol: 3, DepositProtocol: 2, DepositDexChainId: 771, GuardianPubkey: guardianPub, VaultRefs: []string{"", "", "0x1111111111111111111111111111111111111111"}}
	result, _, err := evm.BuildMigrationTemplateV2(evm.MigrationV2Params{Chain: "eth", ChainID: 1, DexChainID: 771, ContractAddr: cfg.VaultRefs[2], VaultID: 2, OldEpoch: 4, NewEpoch: 5, NewGroupPubkey: newPub, GuardianPubkey: guardianPub, FrozenNextBatchSeq: "1"})
	if err != nil {
		t.Fatal(err)
	}
	signature := explorerAccountSignature(t, 5, result.SigHashes[0])
	guardianSignature := explorerAccountSignature(t, 29, result.SigHashes[0])
	archiveBytes, err := proto.Marshal(&pb.FrostVaultMigrationArchive{
		State: &pb.FrostVaultMigrationState{
			MigrationId: "migration-eth-2-5", Chain: "ETH", VaultId: 2, OldEpoch: 4, NewEpoch: 5,
			AuthorityProtocol: 3, FrozenNextBatchSeq: "1", OldGroupPubkey: oldPub, NewGroupPubkey: newPub, TemplateHash: result.TemplateHash,
			TemplateData: result.TemplateData, IntentHash: result.SigHashes[0], Status: "SIGNED",
		},
		Signature: signature, GuardianSigs: [][]byte{guardianSignature}, SignedHeight: 77,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		payload := archiveBytes
		if req.URL.Path == "/frost/vault/config" {
			payload, _ = proto.Marshal(cfg)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/x-protobuf"}}, Body: io.NopCloser(bytes.NewReader(payload)), Request: req}, nil
	})}
	srv := &server{client: client, timeout: time.Second, defaultNodes: []string{"node.example:6000"}, allowedNodes: map[string]struct{}{"node.example:6000": {}}}
	recorder := httptest.NewRecorder()
	srv.handleFrostMigrationDetail(recorder, httptest.NewRequest(http.MethodGet,
		"/api/frost/vault/migration/detail?node=node.example%3A6000&migration_id=migration-eth-2-5", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response frostMigrationDetailResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Account == nil || response.Account.Kind != "evm" || response.Account.ChainID != "1" ||
		response.Account.ContractAddress != "0x1111111111111111111111111111111111111111" ||
		response.Account.NextEpoch != 5 || response.Account.NewPubkeyX != new(big.Int).SetBytes(newPub[:32]).String() || response.Account.NewPubkeyY != new(big.Int).SetBytes(newPub[32:]).String() ||
		response.Account.Method != "withdrawBatchWithHandovers" || response.Account.Calldata != "" {
		t.Fatalf("unexpected account migration detail: %+v", response.Account)
	}
	if response.Account.GuardianRX != new(big.Int).SetBytes(guardianSignature[:32]).String() ||
		response.Account.GuardianRY != new(big.Int).SetBytes(guardianSignature[32:64]).String() ||
		response.Account.GuardianS != new(big.Int).SetBytes(guardianSignature[64:]).String() {
		t.Fatalf("guardian signature missing from verified migration detail: %+v", response.Account)
	}
}

func TestFrostMigrationDetailRejectsRawTransactionOutsideConsensusTemplate(t *testing.T) {
	template := &btc.BTCTemplate{
		Version: 2,
		Inputs:  []btc.TxInput{{TxID: strings.Repeat("22", 32), Amount: 10_000, Sequence: btc.DefaultSequence}},
		Outputs: []btc.TxOutput{{Address: "tb1pdpd76eytdcdwnm62uuxve403srq0dtcls0uj488wjjy4z4lfm25sdwatf0", Amount: 9_500}},
	}
	templateData, _ := template.ToJSON()
	archiveBytes, _ := proto.Marshal(&pb.FrostVaultMigrationArchive{
		State:        &pb.FrostVaultMigrationState{MigrationId: "migration-btc", Chain: "BTC", TemplateHash: template.TemplateHash(), TemplateData: templateData, Status: "SIGNED"},
		GuardianSigs: [][]byte{bytes.Repeat([]byte{0x33}, 64), bytes.Repeat([]byte{0x33}, 64)}, InputSigs: [][]byte{bytes.Repeat([]byte{0x33}, 64)}, RawTx: []byte{1, 2, 3},
	})
	client := &http.Client{Transport: explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(archiveBytes)), Request: req}, nil
	})}
	srv := &server{client: client, timeout: time.Second, defaultNodes: []string{"node.example:6000"}, allowedNodes: map[string]struct{}{"node.example:6000": {}}}
	recorder := httptest.NewRecorder()
	srv.handleFrostMigrationDetail(recorder, httptest.NewRequest(http.MethodGet,
		"/api/frost/vault/migration/detail?node=node.example%3A6000&migration_id=migration-btc", nil))
	if recorder.Code != http.StatusBadGateway || !strings.Contains(recorder.Body.String(), "does not match") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func explorerAccountSignature(t *testing.T, scalar int64, message []byte) []byte {
	t.Helper()
	g := curve.NewBN256Group()
	pub, _ := evm.CanonicalBN128PublicKey(g.SerializePoint(g.ScalarBaseMult(big.NewInt(scalar))))
	btcPub, _ := hex.DecodeString("79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
	request := guardian.Request{Chain: "eth", JobID: "test", Epoch: 1, TemplateHash: make([]byte, 32)}
	resolver := guardian.ResolverFunc(func(_ context.Context, r guardian.Request) (*guardian.Intent, error) {
		return &guardian.Intent{Request: r, SignAlgo: int32(pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128), PublicKey: pub, Messages: [][]byte{message}}, nil
	})
	signer, err := guardian.NewSigner(guardian.KeyFile{BTCPrivateKey: fmt.Sprintf("%064x", 1), AccountPrivateKey: fmt.Sprintf("%064x", scalar)}, btcPub, pub, resolver)
	if err != nil {
		t.Fatal(err)
	}
	signatures, err := signer.Sign(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return signatures[0]
}
