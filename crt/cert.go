package crt

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"dex/logs"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"os"
	"time"

	"github.com/btcsuite/btcd/btcutil/bech32"
	"golang.org/x/crypto/ripemd160"
)

                                                                        
func convertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	acc := 0
	bits := uint(0)
	maxv := (1 << toBits) - 1
	output := []byte{}

	for _, value := range data {
		acc = (acc << fromBits) | int(value)
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			output = append(output, byte((acc>>bits)&maxv))
		}
	}

	if pad && bits > 0 {
		output = append(output, byte((acc<<(toBits-bits))&maxv))
	} else if !pad && bits >= fromBits {
		return nil, fmt.Errorf("excess padding")
	} else if !pad && ((acc<<(toBits-bits))&maxv) != 0 {
		return nil, fmt.Errorf("non-zero padding")
	}

	return output, nil
}

                                                                                          
func GenerateBitcoinAddress(pubKey *ecdsa.PublicKey) (string, error) {
	                                 
	pubKeyBytes := elliptic.Marshal(pubKey.Curve, pubKey.X, pubKey.Y)

	                 
	sha256Hash := sha256.Sum256(pubKeyBytes)

	                    
	ripemdHasher := ripemd160.New()
	_, err := ripemdHasher.Write(sha256Hash[:])
	if err != nil {
		return "", err
	}
	ripemdHash := ripemdHasher.Sum(nil)

	if len(ripemdHash) != 20 {
		return "", fmt.Errorf("invalid RIPEMD-160 hash length: %d", len(ripemdHash))
	}

	                                                             
	                                             
	version := byte(0x00)                              

	                                                    
	converted, err := convertBits(ripemdHash, 8, 5, false)
	if err != nil {
		return "", err
	}

	                                                        
	                                                                                  
	data := append([]byte{version}, converted...)

	                                                 
	address, err := bech32.Encode("bc", data)
	if err != nil {
		return "", err
	}

	return address, nil
}

func generateSelfSignedCert(certPath, keyPath string) error {
	                      
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	                                    
	bitcoinAddress, err := GenerateBitcoinAddress(&privateKey.PublicKey)
	if err != nil {
		return err
	}

	                     
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			Organization: []string{bitcoinAddress},                                    
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:  x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}

	                  
	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return err
	}

	               
	certFile, err := os.Create(certPath)
	if err != nil {
		return err
	}
	defer certFile.Close()
	if err := pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certBytes}); err != nil {
		return err
	}

	               
	keyFile, err := os.Create(keyPath)
	if err != nil {
		return err
	}
	defer keyFile.Close()
	privBytes, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return err
	}
	if err := pem.Encode(keyFile, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes}); err != nil {
		return err
	}

	logs.Debug("Certificate and key generated:\nCertificate: %s\nPrivate Key: %s\n", certPath, keyPath)
	logs.Debug("Bitcoin address used in certificate: %s\n", bitcoinAddress)
	return nil
}

func Test() {
	certPath := "server.crt"
	keyPath := "server.key"

	if err := generateSelfSignedCert(certPath, keyPath); err != nil {
		logs.Error("Failed to generate certificate: %v", err)
	}

	log.Println("Certificate generation completed!")
}
