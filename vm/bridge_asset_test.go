package vm

import (
	bridgeasset "dex/frost/asset"
	"testing"
)

func testBridgeAsset(t *testing.T, chainName, contract string) bridgeasset.Identity {
	t.Helper()
	identity, err := bridgeasset.Resolve(chainName, contract)
	if err != nil {
		t.Fatalf("resolve test bridge asset: %v", err)
	}
	return identity
}
