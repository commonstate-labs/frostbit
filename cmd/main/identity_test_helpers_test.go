package main

import (
	"dex/pb"
	"dex/txverify"
	"dex/utils"
	"testing"
)

func testRuntimeKey(t *testing.T, raw string) *utils.KeyManager {
	t.Helper()
	km := utils.NewKeyManager()
	normalized, err := normalizeSecpPrivKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := km.InitKey(normalized); err != nil {
		t.Fatal(err)
	}
	return km
}
func deriveTestPeerIdentity(raw string) (string, PeerInfo, error) {
	normalized, err := normalizeSecpPrivKey(raw)
	if err != nil {
		return "", PeerInfo{}, err
	}
	km := utils.NewKeyManager()
	if err := km.InitKey(normalized); err != nil {
		return "", PeerInfo{}, err
	}
	peer, err := deriveLocalPeerIdentity(km)
	return normalized, peer, err
}
func signSimTx(tx *pb.AnyTx, privKey string) (string, error) {
	return txverify.SignWithPrivateKey(tx, privKey)
}
