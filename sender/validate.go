package sender

import (
	"crypto/ecdsa"
	"crypto/x509"
	crt "dex/crt"
	"dex/logs"
	"encoding/pem"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	_ "github.com/btcsuite/btcd/txscript"
)

func test() {
	                                   
	certPEM := `
-----BEGIN CERTIFICATE-----
MIIBqzCCAVGgAwIBAgIIGA+Dizzc51AwCgYIKoZIzj0EAwIwNTEzMDEGA1UEChMq
YmMxcXg1cDI4YXh4YWd3ZG5xOGUzZ3lqanNscmt2NnMzNGxzMmdycmhsMB4XDTI0
MTIwOTEyNTY1NVoXDTI1MTIwOTEyNTY1NVowNTEzMDEGA1UEChMqYmMxcXg1cDI4
YXh4YWd3ZG5xOGUzZ3lqanNscmt2NnMzNGxzMmdycmhsMFkwEwYHKoZIzj0CAQYI
KoZIzj0DAQcDQgAEfknoCSZdayWjt3okKDDNRuwEkoDI3ck/jrpt/1eBh/mQvgfv
Y4H98yS9UJJ2qyjwRv4mf/eCjynaVqCc3uX9uKNLMEkwDgYDVR0PAQH/BAQDAgWg
MBMGA1UdJQQMMAoGCCsGAQUFBwMBMAwGA1UdEwEB/wQCMAAwFAYDVR0RBA0wC4IJ
bG9jYWxob3N0MAoGCCqGSM49BAMCA0gAMEUCIBj8lF97FBjYg1LHRyYzlNA705ME
K/OVNewOFXgmcXmtAiEA5/VJ3qy+5J2+oyZXU27WRTy5iM1Eantv6pTJ2KLL9cw=
-----END CERTIFICATE-----
`

	               
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		logs.Error("Failed to decode PEM block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		logs.Error("Failed to parse certificate: %v", err)
	}

	                                          
	if len(cert.Subject.Organization) == 0 {
		logs.Error("Certificate has no organization field")
	}
	btcAddress := cert.Subject.Organization[0]

	                           
	address, err := btcutil.DecodeAddress(btcAddress, &chaincfg.MainNetParams)
	if err != nil {
		logs.Error("Invalid Bitcoin address: %v", err)
	}

	                           
	pubKey, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		logs.Error("Public key is not of type ECDSA")
	}

	                        
	generatedAddress, err := crt.GenerateBitcoinAddress(pubKey)
	if err != nil {
		logs.Error("Failed to create Bitcoin address from public key: %v", err)
	}

	                           
	if address.String() == generatedAddress {
		logs.Info("Address matches the public key!")
	} else {
		logs.Warn("Address does not match the public key! %s", generatedAddress)
	}
}
