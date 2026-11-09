package adapters

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"dex/frost/runtime"
	"dex/frost/runtime/types"
	"dex/frost/security"
	iface "dex/interfaces"
	"dex/keys"
	"dex/pb"
	"dex/sender"
)

const (
	roastSequenceRecordVersion = byte(1)
	roastSequenceRecordSize    = 1 + 8
	roastSequenceRangeSize     = security.FrostSequenceReservationSize
)

                                                                            
                                                                              
                                                                       
var roastSequenceReservationMu sync.Mutex

type roastSequenceRange struct {
	next uint64
	end  uint64
}

                                                               
type SenderRoastMessenger struct {
	sender    *sender.SenderManager
	identity  types.LocalIdentity
	seqStore  iface.NodeLocalWriter
	seqMu     sync.Mutex
	seqRanges map[string]roastSequenceRange
	batcher   *roastEnvelopeBatcher
}

                                                                            
                                                                        
                                                        
func NewSenderRoastMessenger(senderMgr *sender.SenderManager, identity types.LocalIdentity, seqStore iface.NodeLocalWriter) *SenderRoastMessenger {
	m := &SenderRoastMessenger{
		sender:    senderMgr,
		identity:  identity,
		seqStore:  seqStore,
		seqRanges: make(map[string]roastSequenceRange),
	}
	if senderMgr != nil {
		m.batcher = newRoastEnvelopeBatcher(senderMgr)
	}
	return m
}

                                               
func (m *SenderRoastMessenger) Send(to runtime.NodeID, msg *runtime.RoastEnvelope) error {
	if m == nil || m.sender == nil || msg == nil {
		return nil
	}
	env, err := m.prepareEnvelope(to, msg)
	if err != nil {
		return err
	}
	if m.batcher != nil {
		return m.batcher.send(string(to), env)
	}
	return m.sender.SendFrostToAddress(string(to), env)
}

                                                     
func (m *SenderRoastMessenger) Broadcast(peers []runtime.NodeID, msg *runtime.RoastEnvelope) error {
	if m == nil || m.sender == nil || msg == nil {
		return nil
	}
	for _, peer := range peers {
		env, err := m.prepareEnvelope(peer, msg)
		if err != nil {
			return err
		}
		if err := m.sender.SendFrostToAddress(string(peer), env); err != nil {
			return err
		}
	}
	return nil
}

func (m *SenderRoastMessenger) prepareEnvelope(to runtime.NodeID, msg *runtime.RoastEnvelope) (*pb.FrostEnvelope, error) {
	env, err := runtime.PBEnvelopeFromRoast(msg)
	if err != nil {
		return nil, err
	}
	if env == nil {
		return nil, fmt.Errorf("empty frost envelope")
	}
	if to != "" {
		env.To = string(to)
	}
	if m.identity == nil {
		return nil, fmt.Errorf("frost envelope signer not configured")
	}
	seq, err := m.nextSeq(runtime.NodeID(env.From), runtime.NodeID(env.To))
	if err != nil {
		return nil, err
	}
	env.Seq = seq
	if err := m.identity.SignFrostEnvelope(env); err != nil {
		return nil, fmt.Errorf("sign frost envelope: %w", err)
	}
	return env, nil
}

func (m *SenderRoastMessenger) nextSeq(from, to runtime.NodeID) (uint64, error) {
	if m == nil || m.seqStore == nil {
		return 0, errors.New("frost sequence store not configured")
	}
	if from == "" {
		return 0, errors.New("frost envelope sender is empty")
	}
	if to == "" {
		return 0, errors.New("frost envelope receiver is empty")
	}

	m.seqMu.Lock()
	defer m.seqMu.Unlock()

	key := roastSequenceKey(from, to)
	reserved := m.seqRanges[key]
	if reserved.next == 0 || reserved.next > reserved.end {
		var err error
		reserved, err = m.reserveRoastSequenceRange(key, to)
		if err != nil {
			return 0, err
		}
	}

	sequence := reserved.next
	if sequence == reserved.end {
		                                                                         
		reserved.next = 0
	} else {
		reserved.next++
	}
	m.seqRanges[key] = reserved
	return sequence, nil
}

func (m *SenderRoastMessenger) reserveRoastSequenceRange(key string, to runtime.NodeID) (roastSequenceRange, error) {
	roastSequenceReservationMu.Lock()
	defer roastSequenceReservationMu.Unlock()

	currentEnd, err := loadRoastSequence(m.seqStore, key)
	if err != nil {
		return roastSequenceRange{}, err
	}
	if currentEnd > ^uint64(0)-roastSequenceRangeSize {
		return roastSequenceRange{}, fmt.Errorf("outbound frost sequence exhausted for receiver %s", to)
	}
	newEnd := currentEnd + roastSequenceRangeSize
	record := make([]byte, roastSequenceRecordSize)
	record[0] = roastSequenceRecordVersion
	binary.BigEndian.PutUint64(record[1:], newEnd)
	m.seqStore.EnqueueSet(key, string(record))
	if err := flushRoastSequence(m.seqStore); err != nil {
		return roastSequenceRange{}, fmt.Errorf("reserve outbound frost sequence range for receiver %s: %w", to, err)
	}
	return roastSequenceRange{next: currentEnd + 1, end: newEnd}, nil
}

func loadRoastSequence(store iface.NodeLocalWriter, key string) (uint64, error) {
	record, err := store.Get(key)
	if err != nil {
		if isLocalShareNotFound(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("load outbound frost sequence: %w", err)
	}
	if len(record) != roastSequenceRecordSize || record[0] != roastSequenceRecordVersion {
		return 0, errors.New("invalid outbound frost sequence record")
	}
	return binary.BigEndian.Uint64(record[1:]), nil
}

func roastSequenceKey(from, to runtime.NodeID) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte(from))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(to))
	return keys.KeyFrostLocalRoastSequence(hex.EncodeToString(digest.Sum(nil)))
}

type roastSequenceHotFlusher interface {
	ForceFlushHot() error
}

func flushRoastSequence(store iface.NodeLocalWriter) error {
	if hot, ok := store.(roastSequenceHotFlusher); ok {
		return hot.ForceFlushHot()
	}
	return store.ForceFlush()
}
