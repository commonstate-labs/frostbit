package certchain

import (
	"bytes"
	"dex/interfaces"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/utils"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"go.dedis.ch/kyber/v3"
	"google.golang.org/protobuf/proto"
)

                                                 
                                                                                             
                                                                                                        
                                                                                                
type NodeLocalStore interface {
	Get(key string) ([]byte, error)
	EnqueueSet(key, value string)
	Scan(prefix string) (map[string][]byte, error)
}

                                                                                                
type Store = NodeLocalStore

                                                      
type pendingCert struct {
	Height               uint64
	BlockHash            []byte
	StateRoot            []byte
	Diffs                []MinerDiffEntry
	PrevCheckpoint       uint64
	MinerCount           uint32
	ChainID              uint64
	CommitteeVersion     uint32
	CommitteeSize        uint32
	NextValidatorSetRoot []byte
	Digest               []byte                                       
	Signatures           map[uint32][]byte                           
	MinerSet             map[uint32]*MinerSnapshot
	AddrIndex            map[string]uint32
	                                                                                                               
	Certified bool
}

type collectedCertSign struct {
	BlockHeight      uint64
	CheckpointHeight uint64
	FromAddress      string
	BLSSignature     []byte
	TxDigest         []byte
}

                                                                                                         
                                                                             
type CertDigestReadyCallback func(checkpointHeight uint64, digest []byte, signerIndex uint32, isActiveMiner bool)

const (
	certSignRetryIntervalBlocks uint64 = 10
	certSignRetryBatchLimit            = 4
)

                                        
type Tracker struct {
	mu  sync.RWMutex
	cfg Config
	db  NodeLocalStore

	                                                                                    
	prevMinerSet map[uint32]*MinerSnapshot
	addrIndex    map[string]uint32                                               
	                                                                                                      
	genesisMinerSet map[uint32]*MinerSnapshot

	                                                                                                                
	                                                      
	pending         *pendingCert
	pendingByHeight map[uint64]*pendingCert
	collectedSigns  []collectedCertSign

	               
	lastCheckpoint    uint64
	lastAssembledCert uint64

	                                                             
	onDigestReady CertDigestReadyCallback
	                                                                                 
	nextSignRetryHeight uint64
	localAddress        string                                                       
}

                                     
func NewTracker(cfg Config) *Tracker {
	return &Tracker{
		cfg:             cfg,
		prevMinerSet:    make(map[uint32]*MinerSnapshot),
		addrIndex:       make(map[string]uint32),
		genesisMinerSet: make(map[uint32]*MinerSnapshot),
		pendingByHeight: make(map[uint64]*pendingCert),
	}
}

                                                                              
func (t *Tracker) SetOnDigestReady(addr string, cb CertDigestReadyCallback) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.localAddress = addr
	t.onDigestReady = cb
	t.scheduleLocalCertSignsLocked(t.lastCheckpoint, true)
}

                                                                         
func (t *Tracker) SetStore(store NodeLocalStore) {
	t.mu.Lock()
	t.db = store
	t.mu.Unlock()
	t.loadFromDB()
}

                                                         

func (t *Tracker) ReloadFromDB() {
	t.mu.Lock()
	t.prevMinerSet = make(map[uint32]*MinerSnapshot)
	t.addrIndex = make(map[string]uint32)
	t.genesisMinerSet = make(map[uint32]*MinerSnapshot)
	t.pending = nil
	t.pendingByHeight = make(map[uint64]*pendingCert)
	t.collectedSigns = nil
	t.lastCheckpoint = 0
	t.lastAssembledCert = 0
	t.nextSignRetryHeight = 0
	t.mu.Unlock()
	t.loadFromDB()
}

                                                                                                             
func (t *Tracker) OnFinalizedHeight(height uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scheduleLocalCertSignsLocked(height, false)
}

func (t *Tracker) IsCheckpointHeight(height uint64) bool {
	return height > 0 && height%t.cfg.CheckpointInterval == 0
}

func (t *Tracker) GetCheckpointInterval() uint64 {
	return t.cfg.CheckpointInterval
}

func (t *Tracker) GetPrevMinerPubKeys() map[uint32][]byte {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[uint32][]byte, len(t.prevMinerSet))
	for idx, m := range t.prevMinerSet {
		out[idx] = m.PubKey
	}
	return out
}

func (t *Tracker) GetGenesisMinerPubKeys() map[uint32][]byte {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[uint32][]byte, len(t.genesisMinerSet))
	for idx, m := range t.genesisMinerSet {
		if m != nil {
			out[idx] = append([]byte(nil), m.PubKey...)
		}
	}
	return out
}

                                                                                
func (t *Tracker) GetGenesisMinerSet() map[uint32]*MinerSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out, _ := cloneMinerSet(t.genesisMinerSet)
	return out
}

                                                                                                                    
func (t *Tracker) GetMinerPubKeyByIndex(index uint32) ([]byte, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if m, ok := t.prevMinerSet[index]; ok {
		return m.PubKey, true
	}
	return nil, false
}

                                                                             
                                                                      
func quorumThresholdForMinerCount(n int, quorum float64) int {
	if n == 0 {
		return 1
	}
	q := int(float64(n)*quorum) + 1
	if q > n {
		q = n
	}
	return q
}

func (t *Tracker) refreshLatestPendingLocked() {
	t.pending = nil
	for _, p := range t.pendingByHeight {
		if p == nil {
			continue
		}
		if t.pending == nil || p.Height > t.pending.Height {
			t.pending = p
		}
	}
}

                                                                                        
func (t *Tracker) scheduleLocalCertSignsLocked(height uint64, force bool) {
	if t.onDigestReady == nil || t.localAddress == "" || (!force && height < t.nextSignRetryHeight) {
		return
	}
	t.nextSignRetryHeight = height + certSignRetryIntervalBlocks
	heights := make([]uint64, 0, len(t.pendingByHeight))
	for h := range t.pendingByHeight {
		heights = append(heights, h)
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	scheduled := 0
	for _, h := range heights {
		pending := t.pendingByHeight[h]
		if pending == nil || pending.Certified {
			continue
		}
		signerIndex, ok := pending.AddrIndex[t.localAddress]
		if !ok {
			continue
		}
		if _, signed := pending.Signatures[signerIndex]; signed {
			continue
		}
		digest := append([]byte(nil), pending.Digest...)
		cb := t.onDigestReady
		go cb(pending.Height, digest, signerIndex, true)
		scheduled++
		if scheduled == certSignRetryBatchLimit {
			break
		}
	}
}

                                                                               
func (t *Tracker) GetPendingMinerByAddress(height uint64, address string) ([]byte, uint32, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	pending := t.pendingByHeight[height]
	if pending == nil || pending.Certified {
		return nil, 0, false
	}
	index, ok := pending.AddrIndex[address]
	if !ok || pending.MinerSet[index] == nil {
		return nil, 0, false
	}
	return append([]byte(nil), pending.MinerSet[index].PubKey...), index, true
}

func (t *Tracker) GetPendingCertDigest(height uint64) ([]byte, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	pending := t.pendingByHeight[height]
	if pending == nil || pending.Certified || len(pending.Digest) == 0 {
		return nil, false
	}
	return append([]byte(nil), pending.Digest...), true
}

                                                                                 
func (t *Tracker) OnCheckpointExecuted(height uint64, blockHash, stateRoot []byte, activeMiners []interfaces.CertMinerSnapshot) {
	nextSet, err := minerSetFromInterfaces(activeMiners)
	if err != nil {
		logs.Error("[CertChain] checkpoint %d rejected invalid Active set: %v", height, err)
		return
	}
	chainID := pb.GetDefaultChainID()
	nextRoot, err := ValidatorSetRoot(chainID, nextSet)
	if err != nil {
		logs.Error("[CertChain] checkpoint %d failed to commit Active set: %v", height, err)
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.tryAssembleReadyLocked()
	if len(t.prevMinerSet) == 0 {
		logs.Error("[CertChain] checkpoint %d has no previous Active-set trust anchor", height)
		return
	}

	previousFullSet, _ := cloneMinerSet(t.prevMinerSet)
	committee, committeeAddrIndex, err := SelectTopStakeCommittee(previousFullSet)
	if err != nil || len(committee) == 0 {
		logs.Error("[CertChain] checkpoint %d cannot derive signing committee: %v", height, err)
		return
	}
	diffs := DiffMinerSets(previousFullSet, nextSet)
	if err := validateMinerDiffPublicKeys(diffs); err != nil {
		logs.Error("[CertChain] checkpoint %d rejected invalid changed BLS key: %v", height, err)
		return
	}
	prevCP := uint64(0)
	if t.cfg.CheckpointInterval > 0 && height >= t.cfg.CheckpointInterval {
		prevCP = height - t.cfg.CheckpointInterval
	}
	minerCount := uint32(len(previousFullSet))
	committeeSize := uint32(len(committee))
	digest := ComputeCertDigest(CertDigestInput{
		ChainID:              chainID,
		Height:               height,
		BlockHash:            blockHash,
		StateRoot:            stateRoot,
		PrevCheckpoint:       prevCP,
		MinerCount:           minerCount,
		CommitteeVersion:     CommitteeVersionTopStakeV1,
		CommitteeSize:        committeeSize,
		NextValidatorSetRoot: nextRoot,
		Diffs:                diffs,
	})

	t.pending = &pendingCert{
		ChainID:              chainID,
		Height:               height,
		BlockHash:            append([]byte(nil), blockHash...),
		StateRoot:            append([]byte(nil), stateRoot...),
		Diffs:                diffs,
		PrevCheckpoint:       prevCP,
		MinerCount:           minerCount,
		CommitteeVersion:     CommitteeVersionTopStakeV1,
		CommitteeSize:        committeeSize,
		NextValidatorSetRoot: append([]byte(nil), nextRoot...),
		Digest:               digest,
		Signatures:           make(map[uint32][]byte),
		MinerSet:             committee,
		AddrIndex:            committeeAddrIndex,
	}
	t.pendingByHeight[height] = t.pending
	t.lastCheckpoint = height
	t.persistGlobal()
	t.persistPending(t.pending)

	                                                                             
	t.persistMinerSetSnapshot(height, previousFullSet)
	t.prevMinerSet, t.addrIndex = cloneMinerSet(nextSet)

	logs.Info("[CertChain] Checkpoint %d executed: Active=%d committee=%d quorum=%d nextRoot=%x digest=%x...",
		height, len(previousFullSet), len(committee), quorumThresholdForMinerCount(len(committee), t.cfg.Quorum), nextRoot[:8], digest[:8])
	if t.onDigestReady != nil && t.localAddress != "" {
		t.scheduleLocalCertSignsLocked(height, true)
	}
}

func (t *Tracker) CollectCertSignTx(blockHeight uint64, checkpointHeight uint64, fromAddress string, blsSig []byte, txDigest []byte) {
	if checkpointHeight == 0 || fromAddress == "" || len(blsSig) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.collectedSigns = append(t.collectedSigns, collectedCertSign{
		BlockHeight:      blockHeight,
		CheckpointHeight: checkpointHeight,
		FromAddress:      fromAddress,
		BLSSignature:     append([]byte(nil), blsSig...),
		TxDigest:         append([]byte(nil), txDigest...),
	})
}

                                                                                                      
func (t *Tracker) ProcessCollectedCertSigns(fromHeight uint64, toHeight uint64) {
	if fromHeight > toHeight {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.collectedSigns) == 0 {
		return
	}

	remaining := t.collectedSigns[:0]
	for _, item := range t.collectedSigns {
		if item.BlockHeight < fromHeight {
			continue
		}
		if item.BlockHeight > toHeight {
			remaining = append(remaining, item)
			continue
		}
		if item.CheckpointHeight > t.lastCheckpoint {
			remaining = append(remaining, item)
			continue
		}
		if _, ok := t.pendingByHeight[item.CheckpointHeight]; !ok {
			logs.Debug("[CertChain] Replay buffered CertSignTx skipped: block=%d cp=%d no pending checkpoint",
				item.BlockHeight, item.CheckpointHeight)
			continue
		}
		if err := t.processCertSignLocked(item.CheckpointHeight, item.FromAddress, item.BLSSignature, item.TxDigest); err != nil {
			if strings.Contains(err.Error(), "digest mismatch") {
				logs.Warn("[CertChain] Replay buffered CertSignTx DIGEST MISMATCH: block=%d cp=%d from=%s err=%v",
					item.BlockHeight, item.CheckpointHeight, item.FromAddress, err)
			} else {
				logs.Debug("[CertChain] Replay buffered CertSignTx failed: block=%d cp=%d from=%s err=%v",
					item.BlockHeight, item.CheckpointHeight, item.FromAddress, err)
			}
		}
	}
	t.collectedSigns = append([]collectedCertSign(nil), remaining...)
	t.tryAssembleReadyLocked()
}

                                                                    
                                                                                                         
                                                                                       
func (t *Tracker) ProcessCertSign(checkpointHeight uint64, fromAddress string, blsSig []byte, txDigest []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.processCertSignLocked(checkpointHeight, fromAddress, blsSig, txDigest)
}

func (t *Tracker) processCertSignLocked(checkpointHeight uint64, fromAddress string, blsSig []byte, txDigest []byte) error {
	                    
	pending := t.pendingByHeight[checkpointHeight]
	if pending == nil {
		return fmt.Errorf("no pending cert for height %d", checkpointHeight)
	}
	if pending.Certified {
		return nil
	}

	                                                                          
	idx, ok := pending.AddrIndex[fromAddress]
	if !ok {
		return fmt.Errorf("address %s not in prev miner set", fromAddress)
	}
	miner := pending.MinerSet[idx]
	if miner == nil {
		return fmt.Errorf("miner index %d missing in pending miner set", idx)
	}

	            
	if _, exists := pending.Signatures[miner.Index]; exists {
		return nil                      
	}

	                                                                       
	if len(pending.Digest) == 0 {
		return fmt.Errorf("pending digest is empty for checkpoint %d", checkpointHeight)
	}
	if len(txDigest) > 0 && !bytes.Equal(txDigest, pending.Digest) {
		return fmt.Errorf("digest mismatch for cp=%d signer=%s: tx=%x.. pending=%x..",
			checkpointHeight, fromAddress, txDigest[:min(8, len(txDigest))], pending.Digest[:min(8, len(pending.Digest))])
	}

	                                                                         
	pubKey, err := utils.DeserializePublicKey(miner.PubKey)
	if err != nil {
		return fmt.Errorf("deserialize pubkey for %s (index %d): %w", fromAddress, miner.Index, err)
	}
	if err := utils.BLSVerifySignature(pubKey, string(pending.Digest), blsSig); err != nil {
		return fmt.Errorf("BLS verify failed for %s (index %d): %w", fromAddress, miner.Index, err)
	}

	                  
	pending.Signatures[miner.Index] = blsSig
	t.persistSignature(checkpointHeight, miner.Index, blsSig)

	threshold := quorumThresholdForMinerCount(len(pending.MinerSet), t.cfg.Quorum)
	logs.Debug("[CertChain] Cert sign accepted: checkpoint=%d signer=%s(idx=%d) total=%d/%d (quorum=%d)",
		checkpointHeight, fromAddress, miner.Index, len(pending.Signatures), len(pending.MinerSet), threshold)
	if len(pending.Signatures) >= threshold {
		t.tryAssembleReadyLocked()
	}

	return nil
}

                                                                            
func (t *Tracker) GetPrevMinerByAddress(addr string) ([]byte, uint32, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	idx, ok := t.addrIndex[addr]
	if !ok {
		return nil, 0, false
	}
	m := t.prevMinerSet[idx]
	return m.PubKey, m.Index, true
}

                                                                           
func (t *Tracker) GetCertificate(height uint64) ([]byte, bool) {
	if t.db == nil {
		return nil, false
	}
	data, err := t.db.Get(keys.KeyCertCertificate(height))
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

                                                                              
                                                                             
func (t *Tracker) UpdateMinerSet(miners []MinerSnapshot) error {
	input := make(map[uint32]*MinerSnapshot, len(miners))
	for i := range miners {
		m := miners[i]
		if _, exists := input[m.Index]; exists {
			return fmt.Errorf("duplicate miner index %d during bootstrap", m.Index)
		}
		input[m.Index] = &MinerSnapshot{
			Index:           m.Index,
			Address:         m.Address,
			PubKey:          append([]byte(nil), m.PubKey...),
			EffectiveWeight: m.EffectiveWeight,
		}
	}
	if err := validateMinerSet(input); err != nil {
		return fmt.Errorf("invalid bootstrap Active set: %w", err)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lastCheckpoint != 0 || len(t.genesisMinerSet) != 0 {
		return fmt.Errorf("genesis certificate Active set is already initialized")
	}
	t.prevMinerSet, t.addrIndex = cloneMinerSet(input)
	t.genesisMinerSet, _ = cloneMinerSet(t.prevMinerSet)
	t.persistMinerSetSnapshot(0, t.genesisMinerSet)
	return nil
}

                                   

func (t *Tracker) canAssembleLocked(pending *pendingCert) bool {
	if pending == nil || pending.Certified || t.cfg.CheckpointInterval == 0 ||
		pending.Height != pending.PrevCheckpoint+t.cfg.CheckpointInterval {
		return false
	}
	if pending.PrevCheckpoint == 0 || pending.PrevCheckpoint == t.lastAssembledCert {
		return true
	}
	if prev := t.pendingByHeight[pending.PrevCheckpoint]; prev != nil && prev.Certified {
		return true
	}
	raw, ok := t.GetCertificate(pending.PrevCheckpoint)
	if !ok {
		return false
	}
	cert, err := ParseCertificate(raw)
	return err == nil && cert.Height == pending.PrevCheckpoint
}

func (t *Tracker) tryAssembleReadyLocked() {
	heights := make([]uint64, 0, len(t.pendingByHeight))
	for h := range t.pendingByHeight {
		heights = append(heights, h)
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	for _, h := range heights {
		if pending := t.pendingByHeight[h]; pending != nil {
			t.tryAssembleLocked(pending)
		}
	}
}

func (t *Tracker) tryAssembleLocked(pending *pendingCert) {
	if !t.canAssembleLocked(pending) || len(pending.Signatures) < quorumThresholdForMinerCount(len(pending.MinerSet), t.cfg.Quorum) {
		return
	}

	                               
	allIndexes := make([]uint32, 0, len(pending.Signatures))
	for idx := range pending.Signatures {
		allIndexes = append(allIndexes, idx)
	}
	sort.Slice(allIndexes, func(i, j int) bool { return allIndexes[i] < allIndexes[j] })

	                                       
	maxIdx := allIndexes[len(allIndexes)-1]
	bitmapLen := int(maxIdx/8) + 1
	bitmap := make([]byte, bitmapLen)
	sigs := make([][]byte, 0, len(allIndexes))
	pubKeys := make([]kyber.Point, 0, len(allIndexes))

	for _, idx := range allIndexes {
		bitmap[idx/8] |= 1 << (idx % 8)
		sigs = append(sigs, pending.Signatures[idx])
		if miner, ok := pending.MinerSet[idx]; ok {
			if pk, err := utils.DeserializePublicKey(miner.PubKey); err == nil {
				pubKeys = append(pubKeys, pk)
			}
		}
	}

	             
	aggSig, err := utils.AggregateBLS(sigs)
	if err != nil {
		logs.Error("[CertChain] Failed to aggregate BLS signatures: %v", err)
		return
	}

	                     
	if err := utils.BLSAggregateVerify(pubKeys, pending.Digest, aggSig); err != nil {
		logs.Error("[CertChain] Aggregate signature verification failed: %v", err)
		return
	}

	                                       
	pbDiffs := minerDiffsToPB(pending.Diffs)
	cert := &pb.CheckpointCertificate{
		Height:               pending.Height,
		BlockHash:            pending.BlockHash,
		StateRoot:            pending.StateRoot,
		Diffs:                pbDiffs,
		Bitmap:               bitmap,
		AggSig:               aggSig,
		PrevCheckpoint:       pending.PrevCheckpoint,
		MinerCount:           pending.MinerCount,
		SignerCount:          int32(len(allIndexes)),
		ChainId:              pending.ChainID,
		CommitteeVersion:     pending.CommitteeVersion,
		CommitteeSize:        pending.CommitteeSize,
		NextValidatorSetRoot: append([]byte(nil), pending.NextValidatorSetRoot...),
	}
	data, _ := proto.Marshal(cert)

	                  
	if t.db != nil {
		t.db.EnqueueSet(keys.KeyCertCertificate(pending.Height), string(data))
	}

	if pending.Height > t.lastAssembledCert {
		t.lastAssembledCert = pending.Height
	}
	t.persistGlobal()

	logs.Info("[CertChain] ✅ Certificate assembled for height %d (%d/%d signers, aggSig=%d bytes)",
		pending.Height, len(allIndexes), len(pending.MinerSet), len(aggSig))

	delete(t.pendingByHeight, pending.Height)
	for height, candidate := range t.pendingByHeight {
		if candidate != nil && candidate.Certified && height <= pending.PrevCheckpoint {
			delete(t.pendingByHeight, height)
		}
	}
	if t.pending != nil && t.pending.Height == pending.Height {
		t.refreshLatestPendingLocked()
	}
}

                                

func (t *Tracker) persistSignature(cpHeight uint64, signerIndex uint32, sig []byte) {
	if t.db == nil {
		return
	}
	t.db.EnqueueSet(keys.KeyCertSignature(cpHeight, signerIndex), string(sig))
}

func (t *Tracker) persistMinerSetSnapshot(cpHeight uint64, miners map[uint32]*MinerSnapshot) {
	if t.db == nil {
		return
	}
	indexes := make([]uint32, 0, len(miners))
	for idx := range miners {
		indexes = append(indexes, idx)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })

	list := &pb.CertMinerSnapshotList{Miners: make([]*pb.CertMinerSnapshot, 0, len(indexes))}
	for _, idx := range indexes {
		m := miners[idx]
		if m == nil {
			continue
		}
		list.Miners = append(list.Miners, &pb.CertMinerSnapshot{
			Index:           m.Index,
			Address:         m.Address,
			PubKey:          m.PubKey,
			EffectiveWeight: m.EffectiveWeight,
		})
	}
	data, _ := proto.Marshal(list)
	t.db.EnqueueSet(keys.KeyCertMinerSet(cpHeight), string(data))
}

func (t *Tracker) loadMinerSetSnapshot(cpHeight uint64) (map[uint32]*MinerSnapshot, map[string]uint32) {
	miners := make(map[uint32]*MinerSnapshot)
	addrIndex := make(map[string]uint32)
	if t.db == nil {
		return miners, addrIndex
	}
	data, err := t.db.Get(keys.KeyCertMinerSet(cpHeight))
	if err != nil || len(data) == 0 {
		return miners, addrIndex
	}
	var list pb.CertMinerSnapshotList
	if proto.Unmarshal(data, &list) != nil {
		return miners, addrIndex
	}
	for _, m := range list.Miners {
		if m == nil {
			continue
		}
		miners[m.Index] = &MinerSnapshot{
			Index:           m.Index,
			Address:         m.Address,
			PubKey:          append([]byte(nil), m.PubKey...),
			EffectiveWeight: m.EffectiveWeight,
		}
		addrIndex[m.Address] = m.Index
	}
	if err := validateMinerSet(miners); err != nil {
		logs.Warn("[CertChain] ignored invalid miner-set snapshot height=%d: %v", cpHeight, err)
		return map[uint32]*MinerSnapshot{}, map[string]uint32{}
	}
	return miners, addrIndex
}

func (t *Tracker) loadCheckpointRecord(height uint64) *pb.CheckpointCertificate {
	if t.db == nil || height == 0 {
		return nil
	}
	for _, key := range []string{keys.KeyCertCertificate(height), keys.KeyCertPendingCheckpoint(height), keys.KeyCertPending()} {
		data, err := t.db.Get(key)
		if err != nil || len(data) == 0 {
			continue
		}
		var record pb.CheckpointCertificate
		if proto.Unmarshal(data, &record) == nil && record.Height == height {
			return &record
		}
	}
	return nil
}

func (t *Tracker) persistPending(pending *pendingCert) {
	if t.db == nil || pending == nil {
		return
	}
	pbDiffs := minerDiffsToPB(pending.Diffs)
	meta := &pb.CheckpointCertificate{
		Height:               pending.Height,
		BlockHash:            pending.BlockHash,
		StateRoot:            pending.StateRoot,
		Diffs:                pbDiffs,
		PrevCheckpoint:       pending.PrevCheckpoint,
		MinerCount:           pending.MinerCount,
		ChainId:              pending.ChainID,
		CommitteeVersion:     pending.CommitteeVersion,
		CommitteeSize:        pending.CommitteeSize,
		NextValidatorSetRoot: append([]byte(nil), pending.NextValidatorSetRoot...),
	}
	data, _ := proto.Marshal(meta)
	t.db.EnqueueSet(keys.KeyCertPendingCheckpoint(pending.Height), string(data))
	if pending.Height == t.lastCheckpoint {
		t.db.EnqueueSet(keys.KeyCertPending(), string(data))
	}
}

                                                                
                                                                                                                      
func (t *Tracker) SyncGlobalState(lastCheckpoint, lastAssembledCert uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	updated := false
	if lastCheckpoint > t.lastCheckpoint {
		t.lastCheckpoint = lastCheckpoint
		updated = true
	}
	if lastAssembledCert > t.lastAssembledCert {
		t.lastAssembledCert = lastAssembledCert
		updated = true
	}
	if updated {
		t.persistGlobal()
		logs.Info("[CertChain] Tracker state synced: lastCheckpoint=%d, lastAssembledCert=%d", t.lastCheckpoint, t.lastAssembledCert)
	}
}

                                                                              
func (t *Tracker) SyncPendingFromCertificate(cert *pb.CheckpointCertificate) {
	if cert == nil || cert.Height == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	fullSignerSet, _ := cloneMinerSet(t.prevMinerSet)
	committee, addrIndex, err := SelectTopStakeCommittee(fullSignerSet)
	if err != nil || len(committee) == 0 {
		logs.Error("[CertChain] cannot restore certified pending height=%d: derive committee: %v", cert.Height, err)
		return
	}
	diffs, err := minerDiffsFromPB(cert.Diffs)
	if err != nil {
		logs.Error("[CertChain] cannot restore certified pending height=%d: %v", cert.Height, err)
		return
	}
	nextSet, _ := cloneMinerSet(fullSignerSet)
	if err := applyMinerDiffs(nextSet, diffs); err != nil {
		logs.Error("[CertChain] cannot restore certified pending height=%d: %v", cert.Height, err)
		return
	}
	nextRoot, err := ValidatorSetRoot(cert.ChainId, nextSet)
	if err != nil || !bytes.Equal(nextRoot, cert.NextValidatorSetRoot) {
		logs.Error("[CertChain] cannot restore certified pending height=%d: next Active-set root mismatch", cert.Height)
		return
	}
	t.pending = &pendingCert{
		ChainID:              cert.ChainId,
		Height:               cert.Height,
		BlockHash:            append([]byte(nil), cert.BlockHash...),
		StateRoot:            append([]byte(nil), cert.StateRoot...),
		Diffs:                diffs,
		PrevCheckpoint:       cert.PrevCheckpoint,
		MinerCount:           cert.MinerCount,
		CommitteeVersion:     cert.CommitteeVersion,
		CommitteeSize:        cert.CommitteeSize,
		NextValidatorSetRoot: append([]byte(nil), cert.NextValidatorSetRoot...),
		Digest:               CertDigestFromCert(cert),
		Signatures:           make(map[uint32][]byte),
		MinerSet:             committee,
		AddrIndex:            addrIndex,
		Certified:            true,
	}
	t.pendingByHeight = map[uint64]*pendingCert{cert.Height: t.pending}
	t.collectedSigns = nil
	t.lastCheckpoint = cert.Height
	t.lastAssembledCert = cert.PrevCheckpoint
	t.prevMinerSet, t.addrIndex = cloneMinerSet(nextSet)
	t.persistGlobal()
	t.persistPending(t.pending)
	t.persistMinerSetSnapshot(cert.Height, fullSignerSet)
}

func (t *Tracker) SyncPendingFromCertificateChain(certs []*pb.CheckpointCertificate) error {
	if len(certs) == 0 {
		return fmt.Errorf("empty verified certificate chain")
	}
	t.mu.RLock()
	genesis, _ := cloneMinerSet(t.genesisMinerSet)
	interval := t.cfg.CheckpointInterval
	quorum := t.cfg.Quorum
	t.mu.RUnlock()
	if len(genesis) == 0 {
		return fmt.Errorf("genesis miner set unavailable")
	}
	if _, _, err := VerifyCertChain(genesis, certs, quorum, interval); err != nil {
		return fmt.Errorf("certificate chain revalidation failed: %w", err)
	}

	current, _ := cloneMinerSet(genesis)
	var signerFullSet map[uint32]*MinerSnapshot
	var signerCommittee map[uint32]*MinerSnapshot
	var signerAddrIndex map[string]uint32
	for _, cert := range certs {
		signerFullSet, _ = cloneMinerSet(current)
		var err error
		signerCommittee, signerAddrIndex, err = SelectTopStakeCommittee(signerFullSet)
		if err != nil {
			return fmt.Errorf("derive committee at height %d: %w", cert.Height, err)
		}
		if err := applyCertificateDiffs(current, cert.Diffs); err != nil {
			return fmt.Errorf("replay Active set at height %d: %w", cert.Height, err)
		}
	}
	last := certs[len(certs)-1]
	diffs, err := minerDiffsFromPB(last.Diffs)
	if err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.prevMinerSet, t.addrIndex = cloneMinerSet(current)
	t.pending = &pendingCert{
		ChainID:              last.ChainId,
		Height:               last.Height,
		BlockHash:            append([]byte(nil), last.BlockHash...),
		StateRoot:            append([]byte(nil), last.StateRoot...),
		Diffs:                diffs,
		PrevCheckpoint:       last.PrevCheckpoint,
		MinerCount:           last.MinerCount,
		CommitteeVersion:     last.CommitteeVersion,
		CommitteeSize:        last.CommitteeSize,
		NextValidatorSetRoot: append([]byte(nil), last.NextValidatorSetRoot...),
		Digest:               CertDigestFromCert(last),
		Signatures:           make(map[uint32][]byte),
		MinerSet:             signerCommittee,
		AddrIndex:            signerAddrIndex,
		Certified:            true,
	}
	t.pendingByHeight = map[uint64]*pendingCert{last.Height: t.pending}
	t.collectedSigns = nil
	t.lastCheckpoint = last.Height
	t.lastAssembledCert = last.PrevCheckpoint
	t.persistGlobal()
	t.persistPending(t.pending)
	t.persistMinerSetSnapshot(last.Height, signerFullSet)
	logs.Info("[CertChain] Tracker rebuilt from %d verified certs: checkpoint=%d nextActive=%d committee=%d",
		len(certs), last.Height, len(t.prevMinerSet), len(signerCommittee))
	return nil
}

func (t *Tracker) persistGlobal() {
	if t.db == nil {
		return
	}
	g := &pb.CertGlobalState{LastCheckpoint: t.lastCheckpoint, LastAssembledCert: t.lastAssembledCert}
	data, _ := proto.Marshal(g)
	t.db.EnqueueSet(keys.KeyCertGlobal(), string(data))
}

func (t *Tracker) restorePendingV2FromMetaLocked(meta *pb.CheckpointCertificate) *pendingCert {
	if meta == nil || meta.Height == 0 || meta.ChainId != pb.GetDefaultChainID() || meta.CommitteeVersion != CommitteeVersionTopStakeV1 {
		return nil
	}
	certified := false
	if _, ok := t.GetCertificate(meta.Height); ok {
		if t.lastAssembledCert >= meta.Height {
			return nil
		}
		certified = true
	}
	fullSignerSet, _ := t.loadMinerSetSnapshot(meta.Height)
	if len(fullSignerSet) == 0 || meta.MinerCount != uint32(len(fullSignerSet)) {
		logs.Warn("[CertChain] ignored pending checkpoint %d: missing or mismatched full signer set", meta.Height)
		return nil
	}
	committee, addrIndex, err := SelectTopStakeCommittee(fullSignerSet)
	if err != nil || meta.CommitteeSize != uint32(len(committee)) {
		logs.Warn("[CertChain] ignored pending checkpoint %d: invalid committee metadata", meta.Height)
		return nil
	}
	diffs, err := minerDiffsFromPB(meta.Diffs)
	if err != nil {
		logs.Warn("[CertChain] ignored pending checkpoint %d: %v", meta.Height, err)
		return nil
	}
	nextSet, _ := cloneMinerSet(fullSignerSet)
	if err := applyMinerDiffs(nextSet, diffs); err != nil {
		logs.Warn("[CertChain] ignored pending checkpoint %d: bad Active diff: %v", meta.Height, err)
		return nil
	}
	nextRoot, err := ValidatorSetRoot(meta.ChainId, nextSet)
	if err != nil || !bytes.Equal(nextRoot, meta.NextValidatorSetRoot) {
		logs.Warn("[CertChain] ignored pending checkpoint %d: next Active-set root mismatch", meta.Height)
		return nil
	}
	pending := &pendingCert{
		ChainID:              meta.ChainId,
		Height:               meta.Height,
		BlockHash:            append([]byte(nil), meta.BlockHash...),
		StateRoot:            append([]byte(nil), meta.StateRoot...),
		Diffs:                diffs,
		PrevCheckpoint:       meta.PrevCheckpoint,
		MinerCount:           meta.MinerCount,
		CommitteeVersion:     meta.CommitteeVersion,
		CommitteeSize:        meta.CommitteeSize,
		NextValidatorSetRoot: append([]byte(nil), meta.NextValidatorSetRoot...),
		Digest:               CertDigestFromCert(meta),
		Signatures:           map[uint32][]byte{},
		MinerSet:             committee,
		AddrIndex:            addrIndex,
		Certified:            certified,
	}
	if sigs, scanErr := t.db.Scan(keys.KeyCertSignaturePrefix(meta.Height)); scanErr == nil {
		for k, v := range sigs {
			parts := strings.Split(k, "_")
			idx, parseErr := strconv.ParseUint(parts[len(parts)-1], 10, 32)
			if parseErr != nil {
				continue
			}
			miner := pending.MinerSet[uint32(idx)]
			if miner == nil {
				continue
			}
			pubKey, keyErr := utils.DeserializePublicKey(miner.PubKey)
			if keyErr == nil && utils.BLSVerifySignature(pubKey, string(pending.Digest), v) == nil {
				pending.Signatures[uint32(idx)] = append([]byte(nil), v...)
			}
		}
	}
	return pending
}

func (t *Tracker) loadFromDB() {
	if t.db == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	                                                            
	t.genesisMinerSet, _ = t.loadMinerSetSnapshot(0)
	if len(t.genesisMinerSet) == 0 && t.cfg.CheckpointInterval > 0 {
		                                                                                                   
		                                                                           
		t.genesisMinerSet, _ = t.loadMinerSetSnapshot(t.cfg.CheckpointInterval)
		if len(t.genesisMinerSet) > 0 {
			t.persistMinerSetSnapshot(0, t.genesisMinerSet)
		}
	}

	                  
	if data, err := t.db.Get(keys.KeyCertGlobal()); err == nil && len(data) > 0 {
		var g pb.CertGlobalState
		if proto.Unmarshal(data, &g) == nil {
			t.lastCheckpoint = g.LastCheckpoint
			t.lastAssembledCert = g.LastAssembledCert
		}
	}
	if t.lastCheckpoint == 0 && len(t.genesisMinerSet) > 0 {
		                                                                        
		                                                            
		t.prevMinerSet, t.addrIndex = cloneMinerSet(t.genesisMinerSet)
	}

	                                                             
	if t.lastCheckpoint > 0 {
		t.prevMinerSet, t.addrIndex = t.loadMinerSetSnapshot(t.lastCheckpoint)
		                                                                                                                       
		                                                                                    
		if record := t.loadCheckpointRecord(t.lastCheckpoint); record != nil {
			if err := applyCertificateDiffs(t.prevMinerSet, record.Diffs); err != nil {
				logs.Error("[CertChain] failed to replay checkpoint %d Active set: %v", t.lastCheckpoint, err)
				t.prevMinerSet = make(map[uint32]*MinerSnapshot)
				t.addrIndex = make(map[string]uint32)
			} else {
				_, t.addrIndex = cloneMinerSet(t.prevMinerSet)
			}
		}
	}

	                                                                                                             
	t.pendingByHeight = make(map[uint64]*pendingCert)
	seenPending := make(map[uint64]struct{})
	if pendingMetas, err := t.db.Scan(keys.KeyCertPendingPrefix()); err == nil {
		keysSorted := make([]string, 0, len(pendingMetas))
		for k := range pendingMetas {
			keysSorted = append(keysSorted, k)
		}
		sort.Strings(keysSorted)
		for _, k := range keysSorted {
			var meta pb.CheckpointCertificate
			if proto.Unmarshal(pendingMetas[k], &meta) != nil || meta.Height == 0 {
				continue
			}
			if _, seen := seenPending[meta.Height]; seen {
				continue
			}
			if pending := t.restorePendingV2FromMetaLocked(&meta); pending != nil {
				t.pendingByHeight[pending.Height] = pending
				seenPending[pending.Height] = struct{}{}
				logs.Info("[CertChain] Restored pending checkpoint %d with %d signatures", pending.Height, len(pending.Signatures))
			}
		}
	}
	if metaData, err := t.db.Get(keys.KeyCertPending()); err == nil && len(metaData) > 0 {
		var meta pb.CheckpointCertificate
		if proto.Unmarshal(metaData, &meta) == nil && meta.Height > 0 {
			if _, seen := seenPending[meta.Height]; !seen {
				if pending := t.restorePendingV2FromMetaLocked(&meta); pending != nil {
					t.pendingByHeight[pending.Height] = pending
					seenPending[pending.Height] = struct{}{}
					logs.Info("[CertChain] Restored pending checkpoint %d with %d signatures", pending.Height, len(pending.Signatures))
				}
			}
		}
	}
	t.refreshLatestPendingLocked()
	t.tryAssembleReadyLocked()

	if t.lastCheckpoint > 0 {
		logs.Info("[CertChain] Restored from DB: lastCheckpoint=%d, lastAssembledCert=%d, prevMinerSet=%d",
			t.lastCheckpoint, t.lastAssembledCert, len(t.prevMinerSet))
	}
	t.scheduleLocalCertSignsLocked(t.lastCheckpoint, true)
}
