package workers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

func TestShareFingerprintForLogDoesNotExposeSecretPrefix(t *testing.T) {
	secret := []byte{
		0xde, 0xad, 0xbe, 0xef, 0xca, 0xfe, 0xba, 0xbe,
		0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
	}
	sum := sha256.Sum256(secret)
	want := fmt.Sprintf("len=%d,sha256_8=%x", len(secret), sum[:8])
	got := shareFingerprintForLog(secret)
	if got != want {
		t.Fatalf("fingerprint=%q want=%q", got, want)
	}
	if strings.Contains(got, hex.EncodeToString(secret[:8])) {
		t.Fatalf("fingerprint exposes raw secret prefix: %q", got)
	}
}

func TestShareFingerprintForLogEmpty(t *testing.T) {
	if got := shareFingerprintForLog(nil); got != "len=0" {
		t.Fatalf("empty fingerprint=%q want len=0", got)
	}
}
