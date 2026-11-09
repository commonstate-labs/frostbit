package certchain

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"

	"dex/pb"
)

                                                                            
                                                                            
const certDigestDomain = "dex/cert/v3"

                                                                           
type CertDigestInput struct {
	ChainID              uint64
	Height               uint64
	BlockHash            []byte
	StateRoot            []byte
	PrevCheckpoint       uint64
	MinerCount           uint32
	CommitteeVersion     uint32
	CommitteeSize        uint32
	NextValidatorSetRoot []byte
	Diffs                []MinerDiffEntry
}

                                                                   
func ComputeCertDigest(in CertDigestInput) []byte {
	h := sha256.New()
	h.Write([]byte(certDigestDomain))
	var u8 [8]byte
	var u4 [4]byte
	binary.BigEndian.PutUint64(u8[:], in.ChainID)
	h.Write(u8[:])
	binary.BigEndian.PutUint64(u8[:], in.Height)
	h.Write(u8[:])
	binary.BigEndian.PutUint64(u8[:], in.PrevCheckpoint)
	h.Write(u8[:])
	binary.BigEndian.PutUint32(u4[:], in.MinerCount)
	h.Write(u4[:])
	binary.BigEndian.PutUint32(u4[:], in.CommitteeVersion)
	h.Write(u4[:])
	binary.BigEndian.PutUint32(u4[:], in.CommitteeSize)
	h.Write(u4[:])
	writeLenBytes(h, in.BlockHash)
	writeLenBytes(h, in.StateRoot)
	writeLenBytes(h, in.NextValidatorSetRoot)
	writeDiffs(h, in.Diffs)
	return h.Sum(nil)
}

                                                                      
func CertDigestFromCert(cert *pb.CheckpointCertificate) []byte {
	if cert == nil {
		return nil
	}
	diffs := make([]MinerDiffEntry, len(cert.Diffs))
	for i, d := range cert.Diffs {
		if d == nil {
			continue
		}
		diffs[i] = MinerDiffEntry{
			Index:           d.Index,
			Joined:          d.Joined,
			Address:         d.Address,
			PubKey:          append([]byte(nil), d.PubKey...),
			EffectiveWeight: d.EffectiveWeight,
		}
	}
	return ComputeCertDigest(CertDigestInput{
		ChainID:              cert.ChainId,
		Height:               cert.Height,
		BlockHash:            cert.BlockHash,
		StateRoot:            cert.StateRoot,
		PrevCheckpoint:       cert.PrevCheckpoint,
		MinerCount:           cert.MinerCount,
		CommitteeVersion:     cert.CommitteeVersion,
		CommitteeSize:        cert.CommitteeSize,
		NextValidatorSetRoot: cert.NextValidatorSetRoot,
		Diffs:                diffs,
	})
}

func writeLenBytes(h interface{ Write([]byte) (int, error) }, b []byte) {
	var u4 [4]byte
	binary.BigEndian.PutUint32(u4[:], uint32(len(b)))
	h.Write(u4[:])
	h.Write(b)
}

                                                                               
func writeDiffs(h interface{ Write([]byte) (int, error) }, diffs []MinerDiffEntry) {
	sorted := append([]MinerDiffEntry(nil), diffs...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Index != sorted[j].Index {
			return sorted[i].Index < sorted[j].Index
		}
		if sorted[i].Joined != sorted[j].Joined {
			return !sorted[i].Joined
		}
		return sorted[i].Address < sorted[j].Address
	})
	var u8 [8]byte
	var u4 [4]byte
	binary.BigEndian.PutUint32(u4[:], uint32(len(sorted)))
	h.Write(u4[:])
	for _, d := range sorted {
		binary.BigEndian.PutUint32(u4[:], d.Index)
		h.Write(u4[:])
		if d.Joined {
			h.Write([]byte{1})
		} else {
			h.Write([]byte{0})
		}
		writeLenBytes(h, []byte(d.Address))
		writeLenBytes(h, d.PubKey)
		binary.BigEndian.PutUint64(u8[:], d.EffectiveWeight)
		h.Write(u8[:])
	}
}

                                                                           
type MinerDiffEntry struct {
	Index           uint32
	Joined          bool
	Address         string
	PubKey          []byte
	EffectiveWeight uint64
}
