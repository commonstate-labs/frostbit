                                                                             
                                                               
  
                                                                             
                                                                           
                                                                  
package servicepeer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"dex/utils"

	"github.com/btcsuite/btcd/btcec/v2"
)

const (
	                                                                        
	                                       
	ProtocolVersion uint32 = 1

	PublicKeySize = 33
	SignatureSize = 64
	NonceSize     = 32
)

var (
	recordDomain = []byte("DEX_SERVICE_PEER_RECORD_V1")

	ErrInvalidEndpoint    = errors.New("servicepeer: invalid public IPv4 endpoint")
	ErrInvalidRecord      = errors.New("servicepeer: invalid signed record")
	ErrWrongChain         = errors.New("servicepeer: wrong chain")
	ErrInvalidSignature   = errors.New("servicepeer: invalid signature")
	ErrIdentityMismatch   = errors.New("servicepeer: public key does not match node ID")
	ErrUnsupportedVersion = errors.New("servicepeer: unsupported protocol version")
)

                                                                   
                                                                      
type Signer interface {
	Sign(digest []byte) ([]byte, error)
	PublicKeyBytes() []byte
}

                                                                             
                                                                             
                                                                           
                                                                          
type Record struct {
	Version          uint32 `json:"version"`
	ChainID          uint64 `json:"chain_id"`
	NodeID           string `json:"node_id"`
	SecpPublicKey    []byte `json:"secp_public_key"`
	Endpoint         string `json:"endpoint"`
	ArchiveFrom      uint64 `json:"archive_from"`
	ArchiveTo        uint64 `json:"archive_to"`
	CheckpointHeight uint64 `json:"checkpoint_height"`
	Sequence         uint64 `json:"sequence"`
	Signature        []byte `json:"signature"`
}

                                                                               
                                                                          
                                                                             
          
func NewSignedRecord(template Record, signer Signer) (Record, error) {
	if signer == nil {
		return Record{}, fmt.Errorf("%w: nil signer", ErrInvalidRecord)
	}
	pub, nodeID, err := signerIdentity(signer)
	if err != nil {
		return Record{}, err
	}
	endpoint, err := CanonicalPublicEndpoint(template.Endpoint)
	if err != nil {
		return Record{}, err
	}
	record := Record{
		Version:          ProtocolVersion,
		ChainID:          template.ChainID,
		NodeID:           nodeID,
		SecpPublicKey:    pub,
		Endpoint:         endpoint,
		ArchiveFrom:      template.ArchiveFrom,
		ArchiveTo:        template.ArchiveTo,
		CheckpointHeight: template.CheckpointHeight,
		Sequence:         template.Sequence,
	}
	if err := validateUnsignedRecord(record, record.ChainID); err != nil {
		return Record{}, err
	}
	record.Signature, err = signer.Sign(RecordDigest(record))
	if err != nil {
		return Record{}, fmt.Errorf("%w: sign record: %v", ErrInvalidSignature, err)
	}
	if len(record.Signature) != SignatureSize {
		return Record{}, fmt.Errorf("%w: signature length %d", ErrInvalidSignature, len(record.Signature))
	}
	return cloneRecord(record), nil
}

                                                                           
                                                                            
                                                                                  
                                                    
func VerifyRecord(record Record, expectedChainID uint64) error {
	if err := validateUnsignedRecord(record, expectedChainID); err != nil {
		return err
	}
	if len(record.Signature) != SignatureSize || !utils.VerifyECDSASignature(record.SecpPublicKey, RecordDigest(record), record.Signature) {
		return ErrInvalidSignature
	}
	return nil
}

func validateUnsignedRecord(record Record, expectedChainID uint64) error {
	if record.Version != ProtocolVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedVersion, record.Version)
	}
	if expectedChainID == 0 || record.ChainID != expectedChainID {
		return fmt.Errorf("%w: got %d want %d", ErrWrongChain, record.ChainID, expectedChainID)
	}
	if record.Sequence == 0 {
		return fmt.Errorf("%w: sequence must be positive", ErrInvalidRecord)
	}
	if err := verifyIdentity(record.NodeID, record.SecpPublicKey); err != nil {
		return err
	}
	canonical, err := CanonicalPublicEndpoint(record.Endpoint)
	if err != nil {
		return err
	}
	if canonical != record.Endpoint {
		return fmt.Errorf("%w: endpoint is not canonical", ErrInvalidEndpoint)
	}
	if record.ArchiveTo == 0 {
		if record.ArchiveFrom != 0 {
			return fmt.Errorf("%w: archive_from requires archive_to", ErrInvalidRecord)
		}
	} else {
		if record.ArchiveFrom == 0 {
			return fmt.Errorf("%w: non-empty stored-block range must start above genesis", ErrInvalidRecord)
		}
		if record.ArchiveFrom > record.ArchiveTo {
			return fmt.Errorf("%w: stored-block range %d..%d is reversed", ErrInvalidRecord, record.ArchiveFrom, record.ArchiveTo)
		}
	}
	return nil
}

                                                                           
                                                                          
func RecordDigest(record Record) []byte {
	h := sha256.New()
	writeBytes(h, recordDomain)
	writeUint32(h, record.Version)
	writeUint64(h, record.ChainID)
	writeString(h, record.NodeID)
	writeBytes(h, record.SecpPublicKey)
	writeString(h, record.Endpoint)
	writeUint64(h, record.ArchiveFrom)
	writeUint64(h, record.ArchiveTo)
	writeUint64(h, record.CheckpointHeight)
	writeUint64(h, record.Sequence)
	return h.Sum(nil)
}

                                                                         
                                                                                
                                                                               
                                                                              
func CanonicalPublicEndpoint(endpoint string) (string, error) {
	if endpoint == "" || strings.TrimSpace(endpoint) != endpoint || strings.Contains(endpoint, "://") {
		return "", ErrInvalidEndpoint
	}
	ap, err := netip.ParseAddrPort(endpoint)
	if err != nil || !ap.Addr().Is4() || ap.Port() == 0 || !isPublicIPv4(ap.Addr()) {
		return "", ErrInvalidEndpoint
	}
	return netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()).String(), nil
}

                                                                          
func PublicEndpointIP(endpoint string) (netip.Addr, error) {
	canonical, err := CanonicalPublicEndpoint(endpoint)
	if err != nil {
		return netip.Addr{}, err
	}
	ap, _ := netip.ParseAddrPort(canonical)
	return ap.Addr(), nil
}

                                                                              
                                                                 
func PublicEndpointPort(endpoint string) (uint16, error) {
	canonical, err := CanonicalPublicEndpoint(endpoint)
	if err != nil {
		return 0, err
	}
	_, port, _ := strings.Cut(canonical, ":")
	parsed, _ := strconv.ParseUint(port, 10, 16)
	return uint16(parsed), nil
}

func signerIdentity(signer Signer) ([]byte, string, error) {
	if signer == nil {
		return nil, "", fmt.Errorf("%w: nil signer", ErrIdentityMismatch)
	}
	pub := append([]byte(nil), signer.PublicKeyBytes()...)
	if len(pub) != PublicKeySize {
		return nil, "", fmt.Errorf("%w: public key length %d", ErrIdentityMismatch, len(pub))
	}
	parsed, err := btcec.ParsePubKey(pub)
	if err != nil || !bytes.Equal(parsed.SerializeCompressed(), pub) {
		return nil, "", fmt.Errorf("%w: public key is not canonical compressed secp256k1", ErrIdentityMismatch)
	}
	nodeID, err := utils.AddressFromSecp256k1PubKey(pub)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrIdentityMismatch, err)
	}
	return pub, nodeID, nil
}

func verifyIdentity(nodeID string, publicKey []byte) error {
	if len(publicKey) != PublicKeySize {
		return fmt.Errorf("%w: public key length %d", ErrIdentityMismatch, len(publicKey))
	}
	parsed, err := btcec.ParsePubKey(publicKey)
	if err != nil || !bytes.Equal(parsed.SerializeCompressed(), publicKey) {
		return fmt.Errorf("%w: public key is not canonical compressed secp256k1", ErrIdentityMismatch)
	}
	derived, err := utils.AddressFromSecp256k1PubKey(publicKey)
	if err != nil || derived != nodeID {
		return ErrIdentityMismatch
	}
	return nil
}

func isPublicIPv4(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.Is4() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	                                                                        
	                                                                            
	                                                                
	for _, prefix := range nonPublicIPv4Prefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

var nonPublicIPv4Prefixes = mustPrefixes(
	"0.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.88.99.0/24",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
)

func mustPrefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}

func cloneRecord(record Record) Record {
	record.SecpPublicKey = append([]byte(nil), record.SecpPublicKey...)
	record.Signature = append([]byte(nil), record.Signature...)
	return record
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func writeUint32(h hashWriter, value uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], value)
	_, _ = h.Write(buf[:])
}

func writeUint64(h hashWriter, value uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	_, _ = h.Write(buf[:])
}

func writeBytes(h hashWriter, value []byte) {
	writeUint64(h, uint64(len(value)))
	_, _ = h.Write(value)
}

func writeString(h hashWriter, value string) {
	writeBytes(h, []byte(value))
}
