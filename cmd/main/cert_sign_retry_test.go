package main

import (
	"bytes"
	"testing"

	"dex/utils"
)

func TestBuildCertSignTxRetryKeepsCanonicalTxID(t *testing.T) {
	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	digest := []byte("checkpoint-digest")
	blsSignature := []byte("deterministic-bls-signature")
	first, err := buildCertSignTx(keyManager.GetAddress(), 100, 7, digest, blsSignature, keyManager)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildCertSignTx(keyManager.GetAddress(), 100, 7, digest, blsSignature, keyManager)
	if err != nil {
		t.Fatal(err)
	}
	if first.GetTxId() != second.GetTxId() {
		t.Fatalf("retry changed txID: %s != %s", first.GetTxId(), second.GetTxId())
	}
	if first.GetBase().Nonce != 100 {
		t.Fatalf("cert retry nonce=%d want checkpoint height 100", first.GetBase().Nonce)
	}
	if bytes.Equal(first.GetBase().Signature, second.GetBase().Signature) {
		t.Log("ECDSA signatures happened to match; canonical txID stability is still verified")
	}
}
