package main

import (
	"errors"
	"testing"

	"dex/pb"
	"dex/protocol"
)

type protocolStartupReader struct {
	height uint64
	data   map[string]string
}

func (r protocolStartupReader) Read(key string) (string, error) {
	value, ok := r.data[key]
	if !ok {
		return "", errors.New("not found")
	}
	return value, nil
}

func (r protocolStartupReader) GetFinalizedStateHeight() (uint64, bool, error) {
	return r.height, r.height > 0, nil
}

func TestFresh51025StartupHasNoPendingMigration(t *testing.T) {
	reader := protocolStartupReader{data: map[string]string{}}
	for _, height := range []uint64{1, 42584, 42600, 1000000} {
		reader.height = height
		status, err := validatePersistedProtocolUpgradeState(reader, 51025, reader.height)
		if err != nil || status.Version != protocol.LegacyVersion || status.Pending != nil {
			t.Fatalf("height=%d status=%+v err=%v", height, status, err)
		}
		if ok, err := pendingLegacyBTCFeeCompatibility(reader, 51025); err != nil || ok {
			t.Fatalf("height=%d legacy compatibility=%v err=%v", height, ok, err)
		}
	}
}

func TestFrostVaultConfigLegacyExceptionIsNarrow(t *testing.T) {
	expected := &pb.FrostVaultConfig{Chain: "btc", VaultCount: 1, BtcNetwork: "mainnet", BtcFeeSatsPerVbyte: protocol.BTCFeeSatsPerVByteV2}
	legacy := &pb.FrostVaultConfig{Chain: "btc", VaultCount: 1, BtcNetwork: "mainnet"}
	if !frostVaultConfigMatchesProtocol(legacy, expected, true) {
		t.Fatal("pending exact legacy BTC fee was rejected")
	}
	if frostVaultConfigMatchesProtocol(legacy, expected, false) {
		t.Fatal("legacy BTC fee was accepted without a pending migration")
	}
	legacy.VaultCount = 2
	if frostVaultConfigMatchesProtocol(legacy, expected, true) {
		t.Fatal("compatibility exception masked an unrelated config mismatch")
	}
}
