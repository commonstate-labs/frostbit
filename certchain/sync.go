package certchain

import (
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
)

                                                          
type DownloadAndVerifyResult struct {
	VerifiedHeight uint64
	StateRoot      []byte
	RawCerts       [][]byte                                                                 
	Certs          []*pb.CheckpointCertificate                      
}

                                                                                   
                                         
                                                           
type CertFetcher func(fromHeight, toHeight, interval uint64) ([][]byte, error)

type parsedCert struct {
	raw  []byte
	cert *pb.CheckpointCertificate
}

                                                                                                      
                                                                                              
                                                     
                                      
                              
func DownloadAndVerify(
	fetchers []CertFetcher,
	genesisMinerSet map[uint32]*MinerSnapshot,
	maxHeight, interval uint64,
	quorum float64,
) (*DownloadAndVerifyResult, error) {
	if len(fetchers) == 0 {
		return nil, fmt.Errorf("no fetchers provided")
	}
	if len(genesisMinerSet) == 0 {
		return nil, fmt.Errorf("empty genesis miner set")
	}
	if interval == 0 {
		return nil, fmt.Errorf("checkpoint interval must be positive")
	}

	for i, fetch := range fetchers {
		            
		rawCerts, err := fetch(0, maxHeight, interval)
		if err != nil || len(rawCerts) == 0 {
			logs.Warn("[CertChain] Fetcher %d failed: %v, trying next", i, err)
			continue
		}

		            
		parsed := make([]parsedCert, 0, len(rawCerts))
		parseFailed := false
		for _, raw := range rawCerts {
			cert, err := ParseCertificate(raw)
			if err != nil {
				parseFailed = true
				break
			}
			parsed = append(parsed, parsedCert{raw: raw, cert: cert})
		}
		if parseFailed || len(parsed) == 0 {
			logs.Warn("[CertChain] Fetcher %d: incomplete or malformed certificate response, trying next", i)
			continue
		}
		sort.Slice(parsed, func(a, b int) bool { return parsed[a].cert.Height < parsed[b].cert.Height })
		if err := validateCompleteCertChain(parsed, maxHeight, interval); err != nil {
			logs.Warn("[CertChain] Fetcher %d: incomplete certificate chain: %v, trying next", i, err)
			continue
		}
		certs := make([]*pb.CheckpointCertificate, 0, len(parsed))
		verifiedRaw := make([][]byte, 0, len(parsed))
		for _, item := range parsed {
			certs = append(certs, item.cert)
			verifiedRaw = append(verifiedRaw, item.raw)
		}

		            
		verifiedHeight, stateRoot, err := VerifyCertChain(genesisMinerSet, certs, quorum, interval)
		if err != nil {
			logs.Warn("[CertChain] Fetcher %d: verification FAILED: %v — trying next", i, err)
			continue
		}

		result := &DownloadAndVerifyResult{
			VerifiedHeight: verifiedHeight,
			StateRoot:      stateRoot,
			RawCerts:       verifiedRaw,
			Certs:          certs,
		}
		if result.VerifiedHeight == maxHeight {
			logs.Info("[CertChain] Verified complete chain of %d certs via fetcher %d, max height=%d", len(result.Certs), i, result.VerifiedHeight)
		} else {
			logs.Info("[CertChain] Verified complete prefix of %d certs via fetcher %d, max height=%d", len(result.Certs), i, result.VerifiedHeight)
		}
		return result, nil
	}

	return nil, fmt.Errorf("all %d fetchers exhausted", len(fetchers))
}

                                                                                                              
                                                                                                                              
func validateCompleteCertChain(items []parsedCert, maxHeight, interval uint64) error {
	if len(items) == 0 {
		return fmt.Errorf("empty chain")
	}
	var prev uint64
	for i, item := range items {
		if item.cert == nil {
			return fmt.Errorf("nil cert at index %d", i)
		}
		h := item.cert.Height
		if h == 0 || h > maxHeight || h%interval != 0 {
			return fmt.Errorf("height %d outside aligned range 1-%d", h, maxHeight)
		}
		if i == 0 {
			if h != interval || item.cert.PrevCheckpoint != 0 {
				return fmt.Errorf("first cert height=%d prev_checkpoint=%d, want height=%d prev_checkpoint=0", h, item.cert.PrevCheckpoint, interval)
			}
			prev = h
			continue
		}
		if h-prev != interval || item.cert.PrevCheckpoint != prev {
			return fmt.Errorf("gap before height=%d: previous=%d prev_checkpoint=%d", h, prev, item.cert.PrevCheckpoint)
		}
		prev = h
	}
	return nil
}

                                                             
func PersistCerts(db Store, result *DownloadAndVerifyResult) {
	if db == nil || result == nil {
		return
	}
	for i, cert := range result.Certs {
		if i < len(result.RawCerts) {
			db.EnqueueSet(keys.KeyCertCertificate(cert.Height), string(result.RawCerts[i]))
		}
	}
	logs.Info("[CertChain] Persisted %d certificates to local DB", len(result.Certs))
}

                                                                                                 
                                                                                                   
func FindLocalCertifiedHeight(tracker interface {
	GetCertificate(height uint64) ([]byte, bool)
}, maxHeight, minHeight, interval uint64) (height uint64, stateRoot []byte, found bool) {
	if tracker == nil || interval == 0 {
		return 0, nil, false
	}

	                                                                                        
	ceiling := (maxHeight / interval) * interval
	floor := (minHeight / interval) * interval
	if ceiling == 0 {
		return 0, nil, false
	}

	var prev uint64
	for h := interval; h <= ceiling; h += interval {
		certData, ok := tracker.GetCertificate(h)
		if !ok || len(certData) == 0 {
			break
		}
		cert, err := ParseCertificate(certData)
		if err != nil || cert.Height != h || cert.PrevCheckpoint != prev {
			break
		}
		height, stateRoot, prev = h, append([]byte(nil), cert.StateRoot...), h
		if h > ^uint64(0)-interval {
			break
		}
	}
	if height == 0 || height < floor {
		return 0, nil, false
	}
	return height, stateRoot, true
}

                                                   
func ParseCertificate(data []byte) (*pb.CheckpointCertificate, error) {
	var cert pb.CheckpointCertificate
	if err := proto.Unmarshal(data, &cert); err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	return &cert, nil
}
