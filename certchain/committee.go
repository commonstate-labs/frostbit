package certchain

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"

	"dex/interfaces"
	"dex/pb"
	"dex/utils"
)

const (
	                                                                       
	                                                        
	CommitteeVersionTopStakeV2 uint32 = 2
	                                                                            
	                                                                            
	CommitteeVersionTopStakeV1 = CommitteeVersionTopStakeV2
	MaxCommitteeSize           = 100
)

const validatorSetRootDomain = "dex/cert-validator-set/v2"

                                                                               
type MinerSnapshot struct {
	Index           uint32
	Address         string
	PubKey          []byte
	EffectiveWeight uint64
}

func minerSetFromInterfaces(input []interfaces.CertMinerSnapshot) (map[uint32]*MinerSnapshot, error) {
	out := make(map[uint32]*MinerSnapshot, len(input))
	for i := range input {
		m := input[i]
		if _, exists := out[m.Index]; exists {
			return nil, fmt.Errorf("duplicate active miner index %d", m.Index)
		}
		out[m.Index] = &MinerSnapshot{
			Index:           m.Index,
			Address:         m.Address,
			PubKey:          append([]byte(nil), m.PublicKey...),
			EffectiveWeight: m.EffectiveWeight,
		}
	}
	if err := validateMinerSetStructure(out); err != nil {
		return nil, err
	}
	return out, nil
}

func cloneMinerSet(source map[uint32]*MinerSnapshot) (map[uint32]*MinerSnapshot, map[string]uint32) {
	miners := make(map[uint32]*MinerSnapshot, len(source))
	addrIndex := make(map[string]uint32, len(source))
	for idx, m := range source {
		if m == nil {
			continue
		}
		cp := &MinerSnapshot{
			Index:           m.Index,
			Address:         m.Address,
			PubKey:          append([]byte(nil), m.PubKey...),
			EffectiveWeight: m.EffectiveWeight,
		}
		miners[idx] = cp
		addrIndex[m.Address] = idx
	}
	return miners, addrIndex
}

func validateMinerSetStructure(miners map[uint32]*MinerSnapshot) error {
	addresses := make(map[string]uint32, len(miners))
	publicKeys := make(map[string]uint32, len(miners))
	for idx, m := range miners {
		if m == nil {
			return fmt.Errorf("nil active miner at index %d", idx)
		}
		if m.Index != idx {
			return fmt.Errorf("active miner key/index mismatch: key=%d record=%d", idx, m.Index)
		}
		if m.Address == "" {
			return fmt.Errorf("active miner %d has empty address", idx)
		}
		if prior, exists := addresses[m.Address]; exists {
			return fmt.Errorf("duplicate active miner address %q at indexes %d and %d", m.Address, prior, idx)
		}
		addresses[m.Address] = idx
		if m.EffectiveWeight == 0 {
			return fmt.Errorf("active miner %d has zero effective weight", idx)
		}
		if len(m.PubKey) == 0 {
			return fmt.Errorf("active miner %d has empty BLS public key", idx)
		}
		keyID := string(m.PubKey)
		if prior, exists := publicKeys[keyID]; exists {
			return fmt.Errorf("duplicate active miner BLS public key at indexes %d and %d", prior, idx)
		}
		publicKeys[keyID] = idx
	}
	return nil
}

func validateMinerPublicKey(index uint32, publicKey []byte) error {
	if len(publicKey) == 0 {
		return fmt.Errorf("active miner %d has empty BLS public key", index)
	}
	if _, err := utils.DeserializePublicKey(publicKey); err != nil {
		return fmt.Errorf("active miner %d has invalid BLS public key: %w", index, err)
	}
	return nil
}

                                                                             
                                                                             
                                                                           
func validateMinerSet(miners map[uint32]*MinerSnapshot) error {
	if err := validateMinerSetStructure(miners); err != nil {
		return err
	}
	for index, miner := range miners {
		if err := validateMinerPublicKey(index, miner.PubKey); err != nil {
			return err
		}
	}
	return nil
}

                                                                                
                                                                        
func SelectTopStakeCommittee(miners map[uint32]*MinerSnapshot) (map[uint32]*MinerSnapshot, map[string]uint32, error) {
	if err := validateMinerSetStructure(miners); err != nil {
		return nil, nil, err
	}
	candidates := make([]*MinerSnapshot, 0, len(miners))
	for _, m := range miners {
		candidates = append(candidates, m)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].EffectiveWeight != candidates[j].EffectiveWeight {
			return candidates[i].EffectiveWeight > candidates[j].EffectiveWeight
		}
		return candidates[i].Index < candidates[j].Index
	})
	if len(candidates) > MaxCommitteeSize {
		candidates = candidates[:MaxCommitteeSize]
	}
	committee := make(map[uint32]*MinerSnapshot, len(candidates))
	addrIndex := make(map[string]uint32, len(candidates))
	for _, m := range candidates {
		if err := validateMinerPublicKey(m.Index, m.PubKey); err != nil {
			return nil, nil, err
		}
		cp := &MinerSnapshot{
			Index:           m.Index,
			Address:         m.Address,
			PubKey:          append([]byte(nil), m.PubKey...),
			EffectiveWeight: m.EffectiveWeight,
		}
		committee[cp.Index] = cp
		addrIndex[cp.Address] = cp.Index
	}
	return committee, addrIndex, nil
}

func minerSnapshotEqual(a, b *MinerSnapshot) bool {
	return a != nil && b != nil &&
		a.Index == b.Index &&
		a.Address == b.Address &&
		a.EffectiveWeight == b.EffectiveWeight &&
		bytes.Equal(a.PubKey, b.PubKey)
}

                                                                               
                                                                            
                                
func DiffMinerSets(old, next map[uint32]*MinerSnapshot) []MinerDiffEntry {
	indexes := make([]uint32, 0, len(old)+len(next))
	seen := make(map[uint32]struct{}, len(old)+len(next))
	for idx := range old {
		seen[idx] = struct{}{}
		indexes = append(indexes, idx)
	}
	for idx := range next {
		if _, exists := seen[idx]; !exists {
			indexes = append(indexes, idx)
		}
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	diffs := make([]MinerDiffEntry, 0, len(indexes))
	for _, idx := range indexes {
		before, hadBefore := old[idx]
		after, hasAfter := next[idx]
		switch {
		case hadBefore && !hasAfter:
			diffs = append(diffs, MinerDiffEntry{Index: idx, Joined: false})
		case hasAfter && (!hadBefore || !minerSnapshotEqual(before, after)):
			diffs = append(diffs, MinerDiffEntry{
				Index:           after.Index,
				Joined:          true,
				Address:         after.Address,
				PubKey:          append([]byte(nil), after.PubKey...),
				EffectiveWeight: after.EffectiveWeight,
			})
		}
	}
	return diffs
}

func minerDiffsFromPB(input []*pb.CertMinerDiffEntry) ([]MinerDiffEntry, error) {
	out := make([]MinerDiffEntry, len(input))
	seen := make(map[uint32]struct{}, len(input))
	for i, d := range input {
		if d == nil {
			return nil, fmt.Errorf("nil miner diff %d", i)
		}
		if _, exists := seen[d.Index]; exists {
			return nil, fmt.Errorf("duplicate miner diff index %d", d.Index)
		}
		seen[d.Index] = struct{}{}
		out[i] = MinerDiffEntry{
			Index:           d.Index,
			Joined:          d.Joined,
			Address:         d.Address,
			PubKey:          append([]byte(nil), d.PubKey...),
			EffectiveWeight: d.EffectiveWeight,
		}
	}
	return out, nil
}

func minerDiffsToPB(input []MinerDiffEntry) []*pb.CertMinerDiffEntry {
	out := make([]*pb.CertMinerDiffEntry, len(input))
	for i, d := range input {
		out[i] = &pb.CertMinerDiffEntry{
			Index:           d.Index,
			Joined:          d.Joined,
			Address:         d.Address,
			PubKey:          append([]byte(nil), d.PubKey...),
			EffectiveWeight: d.EffectiveWeight,
		}
	}
	return out
}

func validateMinerDiffPublicKeys(diffs []MinerDiffEntry) error {
	for _, diff := range diffs {
		if !diff.Joined {
			continue
		}
		if err := validateMinerPublicKey(diff.Index, diff.PubKey); err != nil {
			return err
		}
	}
	return nil
}

                                                                            
func applyMinerDiffs(miners map[uint32]*MinerSnapshot, diffs []MinerDiffEntry) error {
	next, _ := cloneMinerSet(miners)
	seen := make(map[uint32]struct{}, len(diffs))
	for _, d := range diffs {
		if _, exists := seen[d.Index]; exists {
			return fmt.Errorf("duplicate miner diff index %d", d.Index)
		}
		seen[d.Index] = struct{}{}
		if d.Joined {
			next[d.Index] = &MinerSnapshot{
				Index:           d.Index,
				Address:         d.Address,
				PubKey:          append([]byte(nil), d.PubKey...),
				EffectiveWeight: d.EffectiveWeight,
			}
			continue
		}
		if _, exists := next[d.Index]; !exists {
			return fmt.Errorf("remove unknown active miner index %d", d.Index)
		}
		delete(next, d.Index)
	}
	if err := validateMinerSetStructure(next); err != nil {
		return err
	}
	if err := validateMinerDiffPublicKeys(diffs); err != nil {
		return err
	}
	for idx := range miners {
		delete(miners, idx)
	}
	for idx, m := range next {
		miners[idx] = m
	}
	return nil
}

func applyCertificateDiffs(miners map[uint32]*MinerSnapshot, diffs []*pb.CertMinerDiffEntry) error {
	parsed, err := minerDiffsFromPB(diffs)
	if err != nil {
		return err
	}
	return applyMinerDiffs(miners, parsed)
}

                                                                                
                                     
func ValidatorSetRoot(chainID uint64, miners map[uint32]*MinerSnapshot) ([]byte, error) {
	if err := validateMinerSetStructure(miners); err != nil {
		return nil, err
	}
	indexes := make([]uint32, 0, len(miners))
	for idx := range miners {
		indexes = append(indexes, idx)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	h := sha256.New()
	h.Write([]byte(validatorSetRootDomain))
	var u8 [8]byte
	var u4 [4]byte
	binary.BigEndian.PutUint64(u8[:], chainID)
	h.Write(u8[:])
	binary.BigEndian.PutUint32(u4[:], uint32(len(indexes)))
	h.Write(u4[:])
	for _, idx := range indexes {
		m := miners[idx]
		binary.BigEndian.PutUint32(u4[:], m.Index)
		h.Write(u4[:])
		writeLenBytes(h, []byte(m.Address))
		writeLenBytes(h, m.PubKey)
		binary.BigEndian.PutUint64(u8[:], m.EffectiveWeight)
		h.Write(u8[:])
	}
	return h.Sum(nil), nil
}

                                                                             
                                                    
func ValidatorSetRootFromInterfaces(chainID uint64, miners []interfaces.CertMinerSnapshot) ([]byte, error) {
	set, err := minerSetFromInterfaces(miners)
	if err != nil {
		return nil, err
	}
	return ValidatorSetRoot(chainID, set)
}
