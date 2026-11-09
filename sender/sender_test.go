package sender

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"log"
	mrand "math/rand"
	"os"
	"testing"
	"time"
)

                                        
type TxData struct {
	Message      string `json:"message"`
	PublicKeyPEM string `json:"public_key_pem"`
	SignatureHex string `json:"signature_hex"`
}

                                                                              
func TestGenerateTxData(t *testing.T) {
	t.Parallel()

	const total = 10000
	outFile := "tx_data.jsonl"

	file, err := os.Create(outFile)
	if err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}
	defer file.Close()

	                                            
	mrand.Seed(time.Now().UnixNano())

	for i := 0; i < total; i++ {
		                     
		msg := randomMessage(64)                              

		                           
		privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("Failed to generate key: %v", err)
		}
		pubKey := &privKey.PublicKey

		                        
		sig, err := SignMessage(privKey, msg)
		if err != nil {
			t.Fatalf("Failed to sign message: %v", err)
		}

		                
		pubPEM := PublicKeyToPEM(pubKey)

		data := TxData{
			Message:      msg,
			PublicKeyPEM: pubPEM,
			SignatureHex: sig,
		}

		line, err := json.Marshal(data)
		if err != nil {
			t.Fatalf("Failed to marshal JSON: %v", err)
		}
		_, err = file.Write(append(line, '\n'))
		if err != nil {
			t.Fatalf("Failed to write line: %v", err)
		}
	}
	log.Printf("Successfully wrote %d tx data lines to %s\n", total, outFile)
}

func randomMessage(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[mrand.Intn(len(charset))]
	}
	return string(b)
}
