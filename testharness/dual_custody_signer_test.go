package testharness

import (
	"context"
	"fmt"
	"testing"

	"dex/frost/guardian"
	frostruntime "dex/frost/runtime"
	"dex/frost/runtime/types"
)

func batchGuardianSigner(t *testing.T, reader types.ChainStateReader) *guardian.Signer {
	t.Helper()
	signer, err := guardian.NewSigner(guardian.KeyFile{
		BTCPrivateKey: fmt.Sprintf("%064x", 31), AccountPrivateKey: fmt.Sprintf("%064x", 31),
	}, harnessGuardianPubkey("btc"), harnessGuardianPubkey("eth"), guardian.ResolverFunc(func(_ context.Context, request guardian.Request) (*guardian.Intent, error) {
		return frostruntime.ResolveGuardianIntent(reader, request)
	}))
	if err != nil {
		t.Fatal(err)
	}
	return signer
}
