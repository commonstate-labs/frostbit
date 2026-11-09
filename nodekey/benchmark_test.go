package nodekey

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// 两个基准只比较单身份启动加载成本；均不接触 StateDB，也不包含签名热路径。
func BenchmarkReadPrivateKeyFile(b *testing.B) {
	path := filepath.Join(b.TempDir(), "test-only-original.hex")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(dummyKey())), 0o600); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key, err := ReadPrivateKeyFile(path)
		if err != nil {
			b.Fatal(err)
		}
		Wipe(key)
	}
}

func BenchmarkReadEncryptedFile(b *testing.B) {
	path := filepath.Join(b.TempDir(), "test-only-encrypted.json")
	password := []byte("test-only random long passphrase")
	if err := EncryptFile(path, dummyKey(), password); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key, err := ReadEncryptedFile(path, password)
		if err != nil {
			b.Fatal(err)
		}
		Wipe(key)
	}
}
