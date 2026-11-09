package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dex/nodekey"
)

type importOperationCounts struct {
	source, encrypt, decrypt, tty, fd int
	buffers                           [][]byte
	reports                           []string
}

func importTestOperations(t *testing.T, passwords ...string) (runtimeKeystoreOperations, *importOperationCounts) {
	t.Helper()
	ops := defaultRuntimeKeystoreOperations()
	counts := &importOperationCounts{}
	originalSource, originalEncrypt, originalDecrypt := ops.readPrivateKey, ops.encryptFile, ops.readEncryptedFile
	originalImportSource, originalVerifiedFile := ops.readImportSource, ops.readVerifiedFile
	ops.readPrivateKey = func(path string) ([]byte, error) {
		counts.source++
		raw, err := originalSource(path)
		counts.buffers = append(counts.buffers, raw)
		return raw, err
	}
	ops.readImportSource = func(path string) ([]byte, *nodekey.ImportSource, error) {
		counts.source++
		raw, proof, err := originalImportSource(path)
		counts.buffers = append(counts.buffers, raw)
		return raw, proof, err
	}
	ops.encryptFile = func(path string, key, password []byte) error {
		counts.encrypt++
		return originalEncrypt(path, key, password)
	}
	ops.readEncryptedFile = func(path string, password []byte) ([]byte, error) {
		counts.decrypt++
		raw, err := originalDecrypt(path, password)
		counts.buffers = append(counts.buffers, raw)
		return raw, err
	}
	ops.readVerifiedFile = func(path string, password []byte) ([]byte, *nodekey.VerifiedKeystore, error) {
		counts.decrypt++
		raw, proof, err := originalVerifiedFile(path, password)
		counts.buffers = append(counts.buffers, raw)
		return raw, proof, err
	}
	ops.readPassphraseTTY = func(prompt string) ([]byte, error) {
		counts.tty++
		if len(passwords) == 0 {
			return nil, errors.New("fixture has no terminal input")
		}
		raw := []byte(passwords[0])
		passwords = passwords[1:]
		counts.buffers = append(counts.buffers, raw)
		return raw, nil
	}
	ops.readPassphraseFD = func(int) ([]byte, error) { counts.fd++; return nil, errors.New("unexpected fixture FD") }
	ops.report = func(format string, args ...any) {
		counts.reports = append(counts.reports, fmt.Sprintf(format, args...))
	}
	return ops, counts
}

func assertImportBuffersCleared(t *testing.T, counts *importOperationCounts) {
	t.Helper()
	for _, buffer := range counts.buffers {
		if !bytes.Equal(buffer, make([]byte, len(buffer))) {
			t.Fatal("startup retained a caller-owned secret buffer")
		}
	}
}

func importTestOptions(t *testing.T) (runtimeOptions, []byte) {
	t.Helper()
	dir := t.TempDir()
	source := []byte(testPrivateKeyForIndex(7) + "\n")
	path := filepath.Join(dir, "original.key")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	opts := emptySecretOptions()
	opts.KeystorePath = filepath.Join(dir, "miner.json")
	opts.KeystoreImportFrom = path
	opts.ExpectedAddress = testRuntimeKey(t, testPrivateKeyForIndex(7)).GetAddress()
	return opts, source
}

func TestRuntimeKeystoreImportAndRepeatUsesOnlyCiphertext(t *testing.T) {
	opts, source := importTestOptions(t)
	const password = "fixture-import-password-123"
	ops, counts := importTestOperations(t, password, password)
	identity, err := loadRuntimeKeystoreIdentity(&opts, ops)
	if err != nil {
		t.Fatal(err)
	}
	if identity == nil || identity.GetAddress() != opts.ExpectedAddress {
		t.Fatal("imported identity changed")
	}
	if counts.source != 1 || counts.encrypt != 1 || counts.decrypt != 1 || counts.tty != 2 || counts.fd != 0 {
		t.Fatalf("first import operation counts=%+v", counts)
	}
	if len(counts.reports) != 1 || !strings.Contains(counts.reports[0], identity.GetAddress()) || !strings.Contains(counts.reports[0], "retained") {
		t.Fatal("missing public import/source-retention status")
	}
	original, err := os.ReadFile(opts.KeystoreImportFrom)
	if err != nil || !bytes.Equal(original, source) {
		t.Fatal("import source changed")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(opts.KeystorePath)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("new ciphertext permissions are not 0600")
		}
	}
	before, err := os.ReadFile(opts.KeystorePath)
	if err != nil {
		t.Fatal(err)
	}
	assertImportBuffersCleared(t, counts)
	if err := os.Remove(opts.KeystoreImportFrom); err != nil {
		t.Fatal(err)
	}
	                                                                         
	ops, counts = importTestOperations(t, password)
	repeated, err := loadRuntimeKeystoreIdentity(&opts, ops)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.GetAddress() != identity.GetAddress() || !bytes.Equal(repeated.PublicKeyBytes(), identity.PublicKeyBytes()) {
		t.Fatal("repeat startup changed identity")
	}
	if counts.source != 0 || counts.encrypt != 0 || counts.decrypt != 1 || counts.tty != 1 {
		t.Fatalf("repeat read its import source or ran extra KDFs: %+v", counts)
	}
	after, err := os.ReadFile(opts.KeystorePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("repeat startup changed ciphertext")
	}
	assertImportBuffersCleared(t, counts)
}

func TestRuntimeKeystoreImportValidationBeforeCreatingTarget(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*runtimeOptions)
		passwords []string
		want      string
		prompts   int
	}{
		{"wrong expected address", func(o *runtimeOptions) { o.ExpectedAddress = testRuntimeKey(t, "01").GetAddress() }, nil, "not --expected-address", 0},
		{"confirmation mismatch", func(*runtimeOptions) {}, []string{"fixture-password-123", "different-password-123"}, "do not match", 2},
		{"short new password", func(*runtimeOptions) {}, []string{"short", "short"}, "at least 12", 2},
		{"source absent", func(o *runtimeOptions) { o.KeystoreImportFrom += ".missing" }, nil, "import source", 0},
		{"missing import option", func(o *runtimeOptions) { o.KeystoreImportFrom = "" }, nil, "--keystore-import-from", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, _ := importTestOptions(t)
			tc.configure(&opts)
			ops, counts := importTestOperations(t, tc.passwords...)
			identity, err := loadRuntimeKeystoreIdentity(&opts, ops)
			if err == nil || identity != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("identity=%v error=%v want %q", identity != nil, err, tc.want)
			}
			if counts.tty != tc.prompts || counts.decrypt != 0 {
				t.Fatal("invalid import progressed too far")
			}
			if _, err := os.Lstat(opts.KeystorePath); !os.IsNotExist(err) {
				t.Fatal("invalid import created a target")
			}
			assertImportBuffersCleared(t, counts)
		})
	}
}

func TestRuntimeKeystoreExistingInvalidTargetNeverImports(t *testing.T) {
	for _, kind := range []string{"json", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			opts, _ := importTestOptions(t)
			switch kind {
			case "json":
				if err := os.WriteFile(opts.KeystorePath, []byte("{broken-json"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(opts.KeystorePath, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(opts.KeystoreImportFrom, opts.KeystorePath); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			before, err := os.Lstat(opts.KeystorePath)
			if err != nil {
				t.Fatal(err)
			}
			ops, counts := importTestOperations(t, "fixture-password-123")
			identity, err := loadRuntimeKeystoreIdentity(&opts, ops)
			if err == nil || identity != nil {
				t.Fatal("invalid existing target unlocked")
			}
			if counts.source != 0 || counts.encrypt != 0 || counts.decrypt != 1 {
				t.Fatal("invalid existing target triggered import")
			}
			after, err := os.Lstat(opts.KeystorePath)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("invalid target replaced")
			}
			assertImportBuffersCleared(t, counts)
		})
	}
}

func TestRuntimeKeystoreImportRefusesPathErrorsAndCompetingWriter(t *testing.T) {
	t.Run("stat permission error", func(t *testing.T) {
		opts, _ := importTestOptions(t)
		ops, counts := importTestOperations(t)
		ops.lstat = func(string) (os.FileInfo, error) { return nil, os.ErrPermission }
		if identity, err := loadRuntimeKeystoreIdentity(&opts, ops); err == nil || identity != nil {
			t.Fatal("path permission error admitted import")
		}
		if counts.source != 0 || counts.encrypt != 0 || counts.decrypt != 0 || counts.tty != 0 {
			t.Fatal("path error consumed a source or secret")
		}
	})
	t.Run("exclusive create race", func(t *testing.T) {
		opts, _ := importTestOptions(t)
		ops, counts := importTestOperations(t, "fixture-password-123", "fixture-password-123")
		const competitor = "competing-writer-ciphertext"
		ops.encryptFile = func(path string, _ []byte, _ []byte) error {
			counts.encrypt++
			if err := os.WriteFile(path, []byte(competitor), 0600); err != nil {
				return err
			}
			return os.ErrExist
		}
		if identity, err := loadRuntimeKeystoreIdentity(&opts, ops); err == nil || identity != nil {
			t.Fatal("competing writer admitted startup")
		}
		if counts.decrypt != 0 {
			t.Fatal("race fell back to decrypting a competing target")
		}
		got, err := os.ReadFile(opts.KeystorePath)
		if err != nil || string(got) != competitor {
			t.Fatal("competing target changed")
		}
		assertImportBuffersCleared(t, counts)
	})
}

func TestRuntimeKeystoreImportReadbackFailureStopsStartup(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		t.Run(fmt.Sprint(mismatch), func(t *testing.T) {
			opts, _ := importTestOptions(t)
			ops, counts := importTestOperations(t, "fixture-password-123", "fixture-password-123")
			ops.encryptFile = func(path string, _ []byte, _ []byte) error {
				counts.encrypt++
				return os.WriteFile(path, []byte("written-ciphertext"), 0600)
			}
			ops.readEncryptedFile = func(string, []byte) ([]byte, error) {
				counts.decrypt++
				if !mismatch {
					return nil, errors.New("fixture readback failure")
				}
				raw := make([]byte, 32)
				raw[31] = 1
				counts.buffers = append(counts.buffers, raw)
				return raw, nil
			}
			if identity, err := loadRuntimeKeystoreIdentity(&opts, ops); err == nil || identity != nil {
				t.Fatal("unverified ciphertext admitted startup")
			}
			if _, err := os.Stat(opts.KeystoreImportFrom); err != nil {
				t.Fatal("readback failure deleted source")
			}
			if _, err := os.Stat(opts.KeystorePath); err != nil {
				t.Fatal("readback failure removed written target")
			}
			if len(counts.reports) != 0 {
				t.Fatal("failed import reported success")
			}
			assertImportBuffersCleared(t, counts)
		})
	}
}

func TestRuntimeKeystoreImportFromRequiresKeystore(t *testing.T) {
	opts := emptySecretOptions()
	opts.KeystoreImportFrom = "source.key"
	if err := validateRuntimeSecretOptions(&opts); err == nil || !strings.Contains(err.Error(), "--keystore-import-from requires --keystore") {
		t.Fatalf("unbound import option: %v", err)
	}
}

func TestRuntimeKeystoreDeleteImportSourceRequiresBothPaths(t *testing.T) {
	for _, paths := range []struct{ keystore, source string }{{"", "source.key"}, {"miner.json", ""}} {
		opts := emptySecretOptions()
		opts.KeystorePath, opts.KeystoreImportFrom = paths.keystore, paths.source
		opts.DeleteKeystoreImportSource = true
		err := validateRuntimeSecretOptions(&opts)
		if err == nil || !strings.Contains(err.Error(), "--delete-keystore-import-source requires --keystore and --keystore-import-from") {
			t.Fatalf("unbound delete option: %v", err)
		}
	}
	if !nodekey.ImportCleanupSupported {
		opts := emptySecretOptions()
		opts.KeystorePath, opts.KeystoreImportFrom = "miner.json", "source.key"
		opts.DeleteKeystoreImportSource = true
		if err := validateRuntimeSecretOptions(&opts); err == nil || !strings.Contains(err.Error(), "only on Linux") {
			t.Fatalf("unsupported delete option: %v", err)
		}
	}
}

func BenchmarkRuntimeKeystoreImportStartup(b *testing.B) {
	const password = "fixture-startup-benchmark-password"
	for _, first := range []bool{true, false} {
		name := "existing-unlock"
		if first {
			name = "first-import-and-readback"
		}
		b.Run(name, func(b *testing.B) {
			dir := b.TempDir()
			source := filepath.Join(dir, "source.key")
			if err := os.WriteFile(source, []byte(testPrivateKeyForIndex(7)), 0600); err != nil {
				b.Fatal(err)
			}
			                                                                                    
			_, peer, err := deriveTestPeerIdentity(testPrivateKeyForIndex(7))
			if err != nil {
				b.Fatal(err)
			}
			ops := defaultRuntimeKeystoreOperations()
			ops.readPassphraseTTY = func(string) ([]byte, error) { return []byte(password), nil }
			ops.report = func(string, ...any) {}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				opts := emptySecretOptions()
				opts.KeystorePath = filepath.Join(dir, fmt.Sprintf("miner-%d.json", i))
				opts.KeystoreImportFrom = source
				opts.ExpectedAddress = peer.Address
				if !first {
					if _, err := loadRuntimeKeystoreIdentity(&opts, ops); err != nil {
						b.Fatal(err)
					}
					opts.KeystoreImportFrom = source + ".missing"
				}
				b.StartTimer()
				if _, err := loadRuntimeKeystoreIdentity(&opts, ops); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
