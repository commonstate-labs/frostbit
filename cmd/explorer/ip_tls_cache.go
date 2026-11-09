package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/mholt/acmez/v3/acme"
)

type ipTLSAccountFile struct {
	Directory     string       `json:"directory"`
	Account       acme.Account `json:"account"`
	PrivateKeyPEM string       `json:"private_key_pem"`
}

func prepareIPTLSCache(directory string) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("create IP TLS cache: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("IP TLS cache must be a real directory")
	}
	return os.Chmod(directory, 0700)
}

func readIPTLSPrivateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > ipTLSMaxCacheBytes {
		return nil, errors.New("IP TLS cache file is not regular or exceeds limit")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("IP TLS private cache permissions must be 0600 or stricter")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, ipTLSMaxCacheBytes+1))
	if err == nil && len(data) > ipTLSMaxCacheBytes {
		return nil, errors.New("IP TLS cache grew beyond size limit")
	}
	return data, err
}

func atomicIPTLSPrivateFile(path string, data []byte) error {
	if len(data) > ipTLSMaxCacheBytes {
		return errors.New("IP TLS cache exceeds limit")
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("IP TLS destination is not a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".ip-tls-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (m *ipTLSManager) parseCertificate(bundle []byte) (*ipTLSCertificate, error) {
	certificate, err := tls.X509KeyPair(bundle, bundle)
	if err != nil {
		return nil, err
	}
	if len(certificate.Certificate) == 0 {
		return nil, errors.New("IP TLS bundle contains no certificate")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, err
	}
	intermediates := x509.NewCertPool()
	for _, der := range certificate.Certificate[1:] {
		parsed, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, err
		}
		intermediates.AddCert(parsed)
	}
	now := m.opts.Now()
	if _, err = leaf.Verify(x509.VerifyOptions{DNSName: m.ip, Roots: m.opts.Roots, Intermediates: intermediates, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return nil, fmt.Errorf("validate IP TLS chain and IP SAN: %w", err)
	}
	if !leaf.NotAfter.After(leaf.NotBefore) {
		return nil, errors.New("IP TLS certificate has invalid lifetime")
	}
	certificate.Leaf = leaf
	renewAt := leaf.NotAfter.Add(-leaf.NotAfter.Sub(leaf.NotBefore) / 3)
	return &ipTLSCertificate{certificate: certificate, renewAt: renewAt}, nil
}

func (m *ipTLSManager) loadCertificate() (*ipTLSCertificate, error) {
	bundle, err := readIPTLSPrivateFile(filepath.Join(m.opts.CacheDir, "certificate.pem"))
	if err != nil {
		return nil, err
	}
	return m.parseCertificate(bundle)
}

func (m *ipTLSManager) saveAccount(account acme.Account) error {
	key, err := x509.MarshalPKCS8PrivateKey(account.PrivateKey)
	if err != nil {
		return err
	}
	record := ipTLSAccountFile{Directory: m.opts.DirectoryURL, Account: account, PrivateKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return atomicIPTLSPrivateFile(filepath.Join(m.opts.CacheDir, "account.json"), data)
}

func (m *ipTLSManager) loadAccount() (acme.Account, error) {
	data, err := readIPTLSPrivateFile(filepath.Join(m.opts.CacheDir, "account.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return acme.Account{}, err
	}
	if err == nil {
		var record ipTLSAccountFile
		if err = json.Unmarshal(data, &record); err != nil {
			return acme.Account{}, err
		}
		if record.Directory != m.opts.DirectoryURL {
			return acme.Account{}, errors.New("IP TLS account cache belongs to a different CA directory")
		}
		block, _ := pem.Decode([]byte(record.PrivateKeyPEM))
		if block == nil {
			return acme.Account{}, errors.New("IP TLS account has no private key")
		}
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return acme.Account{}, err
		}
		private, ok := key.(*ecdsa.PrivateKey)
		if !ok || private.Curve != elliptic.P256() {
			return acme.Account{}, errors.New("IP TLS account must use P-256")
		}
		record.Account.PrivateKey = private
		return record.Account, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return acme.Account{}, err
	}
	account := acme.Account{PrivateKey: key, TermsOfServiceAgreed: true}
	                                                                             
	                                                                     
	if err = m.saveAccount(account); err != nil {
		return acme.Account{}, err
	}
	return account, nil
}
