package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestWalletSubmitWaitsForAdmissionAndReturnsNodeError(t *testing.T) {
	txBytes, err := proto.Marshal(&pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{TxId: "tx-conflict"}, To: "recipient", TokenAddress: "FB", Amount: "1",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/tx" {
			t.Fatalf("node path = %q", req.URL.Path)
		}
		if got := req.Header.Get("X-DEX-Wait-Admission"); got != "1" {
			t.Fatalf("wait admission header = %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     "500 Internal Server Error",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("Failed to submit tx: txpool admission rejected: account nonce already pending\n")),
			Request:    req,
		}, nil
	})}
	srv := &server{
		client:       client,
		timeout:      time.Second,
		defaultNodes: []string{"node.example:19000"},
		allowedNodes: map[string]struct{}{"node.example:19000": {}},
	}
	body, err := json.Marshal(walletSubmitTxRequest{Node: "node.example:19000", Tx: base64.StdEncoding.EncodeToString(txBytes)})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	srv.handleWalletSubmitTx(recorder, httptest.NewRequest(http.MethodPost, "/api/wallet/submittx", bytes.NewReader(body)))

	var response walletSubmitTxResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Ok || !strings.Contains(response.Error, "account nonce already pending") {
		t.Fatalf("response = %+v", response)
	}
}

func TestWalletNonceRequireRemoteUsesConfiguredNode(t *testing.T) {
	client := &http.Client{Transport: explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "node.example:19000" || req.URL.Path != "/getaccount" {
			t.Fatalf("node URL = %s", req.URL.String())
		}
		encoded, err := proto.Marshal(&pb.GetAccountResponse{Account: &pb.Account{Address: "fbc1account", Nonce: 9}})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(encoded)),
			Request:    req,
		}, nil
	})}
	srv := &server{
		client:       client,
		timeout:      time.Second,
		chainID:      7,
		defaultNodes: []string{"node.example:19000"},
		allowedNodes: map[string]struct{}{"node.example:19000": {}},
	}
	body, err := json.Marshal(walletNonceRequest{Address: "fbc1account", RequireRemote: true})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	srv.handleWalletNonce(recorder, httptest.NewRequest(http.MethodPost, "/api/wallet/nonce", bytes.NewReader(body)))

	var response walletNonceResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error != "" || response.Nonce != 9 || response.ChainID != 7 {
		t.Fatalf("response = %+v", response)
	}
}
