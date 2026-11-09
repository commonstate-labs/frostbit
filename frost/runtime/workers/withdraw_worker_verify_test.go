package workers

import (
	"testing"

	"dex/pb"
)

func TestVerifySignatureWithRecoverHandlesInvalidInput(t *testing.T) {
	msg := make([]byte, 32)
	sig := make([]byte, 64)

	valid, err, panicVal := verifySignatureWithRecover(
		pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340,
		[]byte{0x01},
		msg,
		sig,
	)
	if valid {
		t.Fatal("valid should be false for invalid input")
	}
	if err == nil && panicVal == nil {
		t.Fatal("expected regular error or captured panic")
	}
}

func TestVerifySignatureWithRecoverNoPanicForRegularError(t *testing.T) {
	valid, err, panicVal := verifySignatureWithRecover(
		pb.SignAlgo_SIGN_ALGO_ECDSA_SECP256K1,
		[]byte{0x01},
		[]byte{0x02},
		[]byte{0x03},
	)
	if panicVal != nil {
		t.Fatalf("unexpected panic: %v", panicVal)
	}
	if err == nil {
		t.Fatal("expected regular verification error")
	}
	if valid {
		t.Fatal("valid should be false for unsupported algo")
	}
}
