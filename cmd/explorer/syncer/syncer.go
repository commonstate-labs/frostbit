package syncer

import (
	"context"
	"dex/cmd/explorer/indexdb"
	"dex/config"
	"dex/db"
	"dex/keys"
	"dex/pb"
	"dex/utils/diskspace"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

                         
type Syncer struct {
	indexDB                            *indexdb.IndexDB
	node                               string                                
	interval                           time.Duration                
	httpClient                         *http.Client
	nodeDB                             *db.Manager                                                       
	maxBlocksPerSync                   int
	indexRetentionHeights              uint64
	indexPruneMaxHeights               int
	indexDiskPressureFreeBytes         uint64
	indexDiskPressureKeepRecentHeights uint64
	diskUsage                          func(string) (diskspace.Snapshot, error)

	mu                  sync.RWMutex
	running             bool
	cancel              context.CancelFunc
	stopCh              chan struct{}
	doneCh              chan struct{}
	triggerCh           chan uint64
	lastSync            time.Time
	lastIntegrityCheck  time.Time
	lastDiskPressureLog time.Time
	status              string
	onBlockIndexed      func(uint64)
	lastIndexedNotifyAt time.Time
	lastIndexedNotify   uint64
	indexedNotifyEvery  time.Duration
	indexedNotifyNow    func() time.Time
	sourceVersion       uint64

	getLatestHeightOverride func(context.Context, string) (uint64, error)
	fetchBlockOverride      func(context.Context, string, uint64) (*pb.Block, error)
	partialHistoryWindow    uint64
}

const DefaultMaxBlocksPerSync = 20
const defaultIndexedNotifyInterval = 2 * time.Second
const DefaultIndexRetentionHeights uint64 = 500000
const DefaultIndexDiskPressureFreeBytes uint64 = 10 << 30
const DefaultIndexDiskPressureKeepRecentHeights uint64 = 100
const defaultPartialHistoryWindow uint64 = 100
const integrityCheckInterval = time.Minute
const indexdbDefaultPruneMaxHeights = 100

type finalizedBlock struct {
	Block     *pb.Block
	ReadModel *pb.BlockReadModel
}

type blockLoader func(context.Context, uint64) (*finalizedBlock, error)

type localKVReader interface {
	GetKV(string) ([]byte, error)
}

                            
func New(indexDB *indexdb.IndexDB, node string, interval time.Duration) *Syncer {
	if interval <= 0 {
		interval = 5 * time.Second
	}

	                                                     
	transport := &http3.Transport{
		TLSClientConfig: config.ExplorerNodeTLSConfig(),
		QUICConfig: &quic.Config{
			MaxIdleTimeout:  30 * time.Second,
			KeepAlivePeriod: 10 * time.Second,
		},
	}

	return &Syncer{
		indexDB:  indexDB,
		node:     node,
		interval: interval,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		maxBlocksPerSync:                   DefaultMaxBlocksPerSync,
		indexRetentionHeights:              DefaultIndexRetentionHeights,
		indexPruneMaxHeights:               indexdbDefaultPruneMaxHeights,
		indexDiskPressureFreeBytes:         DefaultIndexDiskPressureFreeBytes,
		indexDiskPressureKeepRecentHeights: DefaultIndexDiskPressureKeepRecentHeights,
		diskUsage:                          diskspace.Usage,
		partialHistoryWindow:               defaultPartialHistoryWindow,
		indexedNotifyEvery:                 defaultIndexedNotifyInterval,
		status:                             "stopped",
	}
}

                        
func (s *Syncer) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("syncer already running")
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.running = true
	s.cancel = cancel
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.triggerCh = make(chan uint64, 1)
	s.status = "running"
	s.mu.Unlock()

	                     
	s.indexDB.SetSyncNode(s.node)

	go s.syncLoop(runCtx)
	return nil
}

                       
func (s *Syncer) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	cancel := s.cancel
	s.cancel = nil
	close(s.stopCh)
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	<-s.doneCh
	if closer, ok := s.httpClient.Transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
	if closer, ok := s.httpClient.Transport.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

                             
func (s *Syncer) SetNode(node string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.node != node {
		s.sourceVersion++
	}
	s.node = node
	s.indexDB.SetSyncNode(node)
}

                                                                             
                                                                          
                                                                               
                                         
func (s *Syncer) SetOnBlockIndexed(callback func(uint64)) {
	s.mu.Lock()
	s.onBlockIndexed = callback
	s.lastIndexedNotifyAt = time.Time{}
	s.lastIndexedNotify = 0
	s.mu.Unlock()
}

func (s *Syncer) notifyBlockIndexed(height uint64) {
	s.notifyBlockIndexedBounded(height, false)
}

func (s *Syncer) forceNotifyBlockIndexed(height uint64) {
	s.notifyBlockIndexedBounded(height, true)
}

                                                                             
                                                                               
                                                                            
                                             
func (s *Syncer) notifyBlockIndexedBounded(height uint64, force bool) {
	if height == 0 {
		return
	}
	s.mu.Lock()
	callback := s.onBlockIndexed
	if callback == nil || height <= s.lastIndexedNotify {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	if s.indexedNotifyNow != nil {
		now = s.indexedNotifyNow()
	}
	interval := s.indexedNotifyEvery
	if interval <= 0 {
		interval = defaultIndexedNotifyInterval
	}
	if !force && !s.lastIndexedNotifyAt.IsZero() && now.Sub(s.lastIndexedNotifyAt) < interval {
		s.mu.Unlock()
		return
	}
	s.lastIndexedNotify = height
	s.lastIndexedNotifyAt = now
	s.mu.Unlock()
	callback(height)
}

func (s *Syncer) resetBlockIndexedNotification() {
	s.mu.Lock()
	s.lastIndexedNotifyAt = time.Time{}
	s.lastIndexedNotify = 0
	s.mu.Unlock()
}

                                                                  
func (s *Syncer) SetNodeDB(nodeDB *db.Manager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.nodeDB != nodeDB {
		s.sourceVersion++
	}
	s.nodeDB = nodeDB
	                                                     
	if s.indexDB != nil {
		s.indexDB.SetNodeDB(nodeDB)
	}
}

                                                                       
                                                                             
                                                
func (s *Syncer) SetMaxBlocksPerSync(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 {
		n = DefaultMaxBlocksPerSync
	}
	s.maxBlocksPerSync = n
}

                                                                              
                                                                              
                                       
func (s *Syncer) SetIndexRetentionHeights(n uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n == 0 {
		n = DefaultIndexRetentionHeights
	}
	s.indexRetentionHeights = n
}

                                                                              
func (s *Syncer) SetIndexPruneMaxHeights(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 {
		n = indexdbDefaultPruneMaxHeights
	}
	s.indexPruneMaxHeights = n
}

                                                                                
                                                                          
                                                                  
func (s *Syncer) SetIndexDiskPressureFreeBytes(bytes uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if bytes == 0 {
		bytes = DefaultIndexDiskPressureFreeBytes
	}
	s.indexDiskPressureFreeBytes = bytes
}

                                                                             
                                                                              
                                                    
func (s *Syncer) SetIndexDiskPressureKeepRecentHeights(heights uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if heights < DefaultIndexDiskPressureKeepRecentHeights {
		heights = DefaultIndexDiskPressureKeepRecentHeights
	}
	s.indexDiskPressureKeepRecentHeights = heights
}

                                                                              
                                                                             
                                                                                
func (s *Syncer) pruneIndexUnderDiskPressure(currentHeight uint64) (bool, error) {
	s.mu.RLock()
	idb := s.indexDB
	thresholdBytes := s.indexDiskPressureFreeBytes
	keepRecent := s.indexDiskPressureKeepRecentHeights
	maxHeights := s.indexPruneMaxHeights
	usageFn := s.diskUsage
	s.mu.RUnlock()

	if idb == nil || currentHeight == 0 {
		return false, nil
	}
	if thresholdBytes == 0 {
		thresholdBytes = DefaultIndexDiskPressureFreeBytes
	}
	if keepRecent < DefaultIndexDiskPressureKeepRecentHeights {
		keepRecent = DefaultIndexDiskPressureKeepRecentHeights
	}
	if maxHeights <= 0 {
		maxHeights = indexdbDefaultPruneMaxHeights
	}
	if usageFn == nil {
		usageFn = diskspace.Usage
	}

	usage, err := usageFn(idb.Path())
	if err != nil {
		return false, err
	}
	if usage.TotalBytes == 0 {
		return false, fmt.Errorf("disk usage for %q reported zero total bytes", usage.Path)
	}
	                                                                          
	                                                                      
	if !(usage.FreeBytes < thresholdBytes) {
		return false, nil
	}

	if currentHeight > keepRecent {
		if err := idb.PruneToKeepLimited(currentHeight, keepRecent, maxHeights); err != nil {
			return true, err
		}
	}
	s.logIndexDiskPressure(usage, thresholdBytes, keepRecent, maxHeights)
	return true, nil
}

func (s *Syncer) logIndexDiskPressure(usage diskspace.Snapshot, thresholdBytes uint64, keepRecent uint64, maxHeights int) {
	now := time.Now()
	s.mu.Lock()
	if !s.lastDiskPressureLog.IsZero() && now.Sub(s.lastDiskPressureLog) < time.Minute {
		s.mu.Unlock()
		return
	}
	s.lastDiskPressureLog = now
	s.mu.Unlock()
	log.Printf("[syncer] Explorer index disk pressure: path=%s available_bytes=%d threshold_bytes=%d available_gib=%.2f threshold_gib=%.2f keep_recent=%d prune_budget=%d",
		usage.Path, usage.FreeBytes, thresholdBytes, float64(usage.FreeBytes)/(1<<30), float64(thresholdBytes)/(1<<30), keepRecent, maxHeights)
}

                                                                           
                                                                              
                                                               
func (s *Syncer) Trigger(height uint64) {
	s.mu.RLock()
	running := s.running
	ch := s.triggerCh
	s.mu.RUnlock()
	if !running || ch == nil {
		return
	}
	for {
		select {
		case ch <- height:
			return
		default:
			select {
			case old := <-ch:
				if old > height {
					height = old
				}
			default:
			}
		}
	}
}

                                   
func (s *Syncer) GetNode() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.node
}

                               
func (s *Syncer) GetStatus() (string, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status, s.lastSync
}

                        
func (s *Syncer) syncLoop(ctx context.Context) {
	defer close(s.doneCh)

	                                                                     
	s.syncOnce(ctx, 0)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.syncOnce(ctx, 0)
		case height := <-s.triggerCh:
			s.syncOnce(ctx, height)
		}
	}
}

                              
func (s *Syncer) syncOnce(ctx context.Context, targetHeight uint64) {
	s.mu.Lock()
	node := s.node
	nodeDB := s.nodeDB
	sourceVersion := s.sourceVersion
	maxBlocksPerSync := s.maxBlocksPerSync
	indexRetentionHeights := s.indexRetentionHeights
	indexPruneMaxHeights := s.indexPruneMaxHeights
	s.status = "syncing"
	s.mu.Unlock()

	                           
	syncHeight, err := s.indexDB.GetSyncHeight()
	if err != nil {
		s.setStatus(fmt.Sprintf("error getting sync height: %v", err))
		return
	}
	history, err := s.indexDB.GetHistoryMetadata()
	if err != nil {
		s.finishSync(fmt.Sprintf("error reading history metadata: %v", err))
		return
	}
	pressureActive, pressureErr := s.pruneIndexUnderDiskPressure(syncHeight)
	if pressureErr != nil {
		s.finishSync(fmt.Sprintf("error pruning explorer index under disk pressure: %v", pressureErr))
		return
	}
	if pressureActive {
		                                                                         
		                                                                            
		history, err = s.indexDB.GetHistoryMetadata()
		if err != nil {
			s.finishSync(fmt.Sprintf("error reading history metadata after disk-pressure pruning: %v", err))
			return
		}
	}

	                                                                                              
	                                                                                                       
	                                  
	var latestHeight uint64
	useLocalDB := false

	if node != "" {
		latestHeight, err = s.loadLatestHeight(ctx, node)
	}
	if err != nil || node == "" {
		if nodeDB != nil {
			latestHeight, err = nodeDB.GetLatestBlockHeight()
			if err == nil {
				useLocalDB = true
			}
		}
	}
	if err != nil {
		s.setStatus(fmt.Sprintf("error getting latest height: %v", err))
		return
	}
	if !s.isSourceVersionCurrent(sourceVersion) {
		s.finishSync("source changed while reading latest height; retrying on the next sync round")
		return
	}

	loadBlock := s.newBlockLoader(node, nodeDB, useLocalDB)

	                                                                           
	                                                                           
	                                                                           
	                                                                        
	                                              
	if syncHeight > 0 && latestHeight < syncHeight {
		baseHeight := uint64(1)
		if history != nil && history.Partial {
			baseHeight = history.BaseHeight
		}
		checkHeight := latestHeight                                                        
		if checkHeight == 0 || checkHeight < baseHeight {
			s.finishSync(formatHistoryStatus(history, fmt.Sprintf("source height %d is behind sync height %d and has no overlap with retained history starting at %d; keeping existing index", latestHeight, syncHeight, baseHeight)))
			return
		}
		stale, staleErr := s.indexStaleAt(ctx, loadBlock, checkHeight)
		if staleErr != nil {
			s.finishSync(formatHistoryStatus(history, fmt.Sprintf("source height %d is behind sync height %d; unable to verify overlap at height %d: %v; keeping existing index", latestHeight, syncHeight, checkHeight, staleErr)))
			return
		}
		if !stale {
			if !s.isSourceVersionCurrent(sourceVersion) {
				s.finishSync("source changed while verifying the indexed chain; retrying on the next sync round")
				return
			}
			s.finishSync(formatHistoryStatus(history, fmt.Sprintf("source height %d is behind sync height %d but matches retained height %d; keeping existing index", latestHeight, syncHeight, checkHeight)))
			return
		}
		log.Printf("[syncer] Index chain mismatch at overlapping height %d (source tip %d, indexed tip %d), resetting explorer index", checkHeight, latestHeight, syncHeight)
		if err := s.resetIndexForSource(sourceVersion, node); err != nil {
			if errors.Is(err, errSyncSourceChanged) {
				s.finishSync("source changed before stale index reset; retrying on the next sync round")
				return
			}
			s.setStatus(fmt.Sprintf("error resetting stale index: %v", err))
			return
		}
		syncHeight = 0
		history = nil
		pressureActive = false
	}

	if syncHeight > 0 && latestHeight > 0 && s.reserveIntegrityCheck() {
		checkHeight := syncHeight
		if checkHeight > latestHeight {
			checkHeight = latestHeight
		}
		stale, staleErr := s.indexStaleAt(ctx, loadBlock, checkHeight)
		if staleErr != nil {
			log.Printf("[syncer] failed to verify index at height %d: %v", checkHeight, staleErr)
		} else if stale {
			log.Printf("[syncer] Index chain mismatch at height %d, resetting explorer index", checkHeight)
			if err := s.resetIndexForSource(sourceVersion, node); err != nil {
				if errors.Is(err, errSyncSourceChanged) {
					s.finishSync("source changed before stale index reset; retrying on the next sync round")
					return
				}
				s.setStatus(fmt.Sprintf("error resetting stale index: %v", err))
				return
			}
			syncHeight = 0
			history = nil
		}
	}
	if !s.isSourceVersionCurrent(sourceVersion) {
		s.finishSync("source changed while verifying the indexed chain; retrying on the next sync round")
		return
	}
	if targetHeight > 0 && targetHeight <= syncHeight && latestHeight <= syncHeight {
		s.finishSync(formatHistoryStatus(history, fmt.Sprintf("trigger height %d already covered at %d", targetHeight, syncHeight)))
		return
	}
	startHeight := syncHeight + 1
	if syncHeight == 0 && history != nil && history.Partial {
		startHeight = history.BaseHeight
	}
	if history != nil && history.Partial && syncHeight == 0 && startHeight > latestHeight {
		s.finishSync(formatHistoryStatus(history, fmt.Sprintf("blocked: partial history base %d is ahead of source height %d", startHeight, latestHeight)))
		return
	}

	if syncHeight >= latestHeight {
		if !pressureActive && indexRetentionHeights > 0 && syncHeight > 0 {
			if err := s.indexDB.PruneToKeepLimited(syncHeight, indexRetentionHeights, indexPruneMaxHeights); err != nil {
				s.finishSync(fmt.Sprintf("error pruning explorer index at height %d: %v", syncHeight, err))
				return
			}
			history, err = s.indexDB.GetHistoryMetadata()
			if err != nil {
				s.finishSync(fmt.Sprintf("error reading history metadata after retention pruning: %v", err))
				return
			}
		}
		s.finishSync(formatHistoryStatus(history, fmt.Sprintf("up to date at %d", latestHeight)))
		return
	}

	if useLocalDB {
		log.Printf("[syncer] Using local nodeDB (height %d -> %d)", syncHeight, latestHeight)
	}

	var prefetched *finalizedBlock
	if syncHeight == 0 && startHeight <= latestHeight {
		prefetched, err = loadBlock(ctx, startHeight)
		if err == nil && (prefetched == nil || prefetched.Block == nil || prefetched.Block.Header == nil || prefetched.ReadModel == nil) {
			s.finishSync(formatHistoryStatus(history, fmt.Sprintf("error loading first expected block %d: source returned an empty block", startHeight)))
			return
		}
		if err != nil {
			if !errors.Is(err, errBlockNotFound) {
				s.finishSync(formatHistoryStatus(history, fmt.Sprintf("error loading first expected block %d: %v", startHeight, err)))
				return
			}
			empty, emptyErr := s.indexDB.IsHistoryEmpty()
			if emptyErr != nil {
				s.finishSync(formatHistoryStatus(history, fmt.Sprintf("error checking explorer history: %v", emptyErr)))
				return
			}
			if !empty {
				s.finishSync(formatHistoryStatus(history, fmt.Sprintf("blocked at height %d: source block unavailable; existing index data prevents skipping a gap", startHeight)))
				return
			}
			missingHeight := startHeight
			baseHeight, baseBlock, discoverErr := s.discoverPartialHistoryBase(ctx, latestHeight, loadBlock)
			if discoverErr != nil {
				s.finishSync(formatHistoryStatus(history, fmt.Sprintf("blocked at height %d: source block unavailable; partial bootstrap failed: %v", startHeight, discoverErr)))
				return
			}
			reason := fmt.Sprintf("source history unavailable at height %d; bootstrapped from recent retained window ending at %d", missingHeight, latestHeight)
			if err := s.setPartialHistoryBaseForSource(sourceVersion, baseHeight, reason); err != nil {
				if errors.Is(err, errSyncSourceChanged) {
					s.finishSync("source changed during partial-history discovery; retrying on the next sync round")
					return
				}
				s.finishSync(fmt.Sprintf("error saving partial history base %d: %v", baseHeight, err))
				return
			}
			history = &indexdb.HistoryMetadata{Version: indexdb.HistoryMetadataVersion, BaseHeight: baseHeight, Partial: true, Reason: reason}
			startHeight = baseHeight
			prefetched = baseBlock
			log.Printf("[syncer] Explorer is starting explicit partial history at height %d because source block %d is unavailable", baseHeight, missingHeight)
		}
	}

	roundTarget := latestHeight
	if maxBlocksPerSync > 0 {
		limitTarget := startHeight + uint64(maxBlocksPerSync) - 1
		if roundTarget > limitTarget {
			roundTarget = limitTarget
		}
	}

	                        
	synced := 0
	var lastSyncedHeight uint64
	stopStatus := ""
	expectedParentHash := ""
	parentKnown := true
	if syncHeight == 0 && (startHeight == 1 || (history != nil && history.Partial && startHeight == history.BaseHeight)) {
		                                                                         
		                                                                        
		                                               
		parentKnown = false
	} else {
		expectedParentHash, err = s.indexDB.GetBlockHash(startHeight - 1)
		if err != nil {
			s.finishSync(formatHistoryStatus(history, fmt.Sprintf("error reading indexed parent hash at height %d: %v", startHeight-1, err)))
			return
		}
		if expectedParentHash == "" {
			s.finishSync(formatHistoryStatus(history, fmt.Sprintf("error validating block %d: indexed parent hash at height %d is missing", startHeight, startHeight-1)))
			return
		}
	}

	for height := startHeight; height <= roundTarget; height++ {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		default:
		}

		finalized := prefetched
		var blockErr error
		if height != startHeight || finalized == nil {
			finalized, blockErr = loadBlock(ctx, height)
		}

		if blockErr != nil {
			if errors.Is(blockErr, errBlockNotFound) {
				log.Printf("[syncer] Block %d not available yet, keeping sync height at %d", height, syncHeight+uint64(synced))
				stopStatus = fmt.Sprintf("blocked at height %d: source block unavailable; sync remains at %d", height, lastSuccessfulHeight(syncHeight, lastSyncedHeight))
			} else {
				stopStatus = fmt.Sprintf("error syncing block %d: %v", height, blockErr)
			}
			break
		}
		if finalized == nil || finalized.Block == nil || finalized.Block.Header == nil || finalized.ReadModel == nil {
			stopStatus = fmt.Sprintf("error syncing block %d: source returned an empty block", height)
			break
		}
		block := finalized.Block
		if block.Header.Height != height {
			stopStatus = fmt.Sprintf("error syncing block %d: source returned height %d", height, block.Header.Height)
			break
		}
		if block.BlockHash == "" {
			stopStatus = fmt.Sprintf("error validating block %d: source returned an empty block hash", height)
			break
		}
		if parentKnown && block.Header.PrevBlockHash != expectedParentHash {
			stopStatus = fmt.Sprintf("error validating block %d: parent hash mismatch: got %q, want %q", height, block.Header.PrevBlockHash, expectedParentHash)
			break
		}

		if err := s.indexBlockForSource(sourceVersion, block, finalized.ReadModel); err != nil {
			if errors.Is(err, errSyncSourceChanged) {
				stopStatus = "source changed before block commit; retrying on the next sync round"
				break
			}
			stopStatus = fmt.Sprintf("error indexing block %d: %v", height, err)
			break
		}
		lastSyncedHeight = height
		synced++
		expectedParentHash = block.BlockHash
		parentKnown = true
		                                                                     
		                                                                         
		                                                          
		s.notifyBlockIndexed(height)
	}
	if lastSyncedHeight > 0 {
		                                                                          
		                                                                  
		s.forceNotifyBlockIndexed(lastSyncedHeight)
	}

	syncedTo := lastSuccessfulHeight(syncHeight, lastSyncedHeight)
	if !pressureActive && indexRetentionHeights > 0 && syncedTo > 0 {
		if err := s.indexDB.PruneToKeepLimited(syncedTo, indexRetentionHeights, indexPruneMaxHeights); err != nil {
			s.finishSync(formatHistoryStatus(history, fmt.Sprintf("error pruning explorer index at height %d: %v", syncedTo, err)))
			return
		}
		history, err = s.indexDB.GetHistoryMetadata()
		if err != nil {
			s.finishSync(fmt.Sprintf("error reading history metadata after retention pruning: %v", err))
			return
		}
	}

	if stopStatus != "" {
		s.finishSync(formatHistoryStatus(history, stopStatus))
		return
	}

	status := ""
	if syncedTo < latestHeight {
		status = fmt.Sprintf("synced to %d (fetched %d blocks, %d remaining)", syncedTo, synced, latestHeight-syncedTo)
	} else {
		status = fmt.Sprintf("synced to %d (fetched %d blocks)", syncedTo, synced)
	}
	s.finishSync(formatHistoryStatus(history, status))
}

                                                                         
                                                                           
                                                                             
                                
func (s *Syncer) reserveIntegrityCheck() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if !s.lastIntegrityCheck.IsZero() && now.Sub(s.lastIntegrityCheck) < integrityCheckInterval {
		return false
	}
	s.lastIntegrityCheck = now
	return true
}

func (s *Syncer) indexStaleAt(ctx context.Context, loadBlock blockLoader, height uint64) (bool, error) {
	indexedHash, err := s.indexDB.GetBlockHash(height)
	if err != nil {
		return false, err
	}
	if indexedHash == "" {
		return false, fmt.Errorf("indexed block hash is missing at height %d", height)
	}

	finalized, err := loadBlock(ctx, height)
	if err != nil {
		return false, err
	}
	if finalized == nil || finalized.Block == nil || finalized.Block.Header == nil || finalized.Block.BlockHash == "" {
		return false, fmt.Errorf("source returned an empty block at height %d", height)
	}
	if finalized.Block.Header.Height != height {
		return false, fmt.Errorf("source returned height %d for requested height %d", finalized.Block.Header.Height, height)
	}
	return indexedHash != finalized.Block.BlockHash, nil
}

var errSyncSourceChanged = errors.New("sync source changed")

func (s *Syncer) isSourceVersionCurrent(version uint64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sourceVersion == version
}

                                                                        
                                                                            
                                                                             
func (s *Syncer) resetIndexForSource(version uint64, node string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sourceVersion != version {
		return errSyncSourceChanged
	}
	if err := s.indexDB.Reset(); err != nil {
		return err
	}
	if err := s.indexDB.SetSyncNode(node); err != nil {
		return fmt.Errorf("restore sync node after index reset: %w", err)
	}
	s.lastIndexedNotifyAt = time.Time{}
	s.lastIndexedNotify = 0
	return nil
}

func (s *Syncer) setPartialHistoryBaseForSource(version, baseHeight uint64, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sourceVersion != version {
		return errSyncSourceChanged
	}
	return s.indexDB.SetPartialHistoryBase(baseHeight, reason)
}

func (s *Syncer) indexBlockForSource(version uint64, block *pb.Block, readModel *pb.BlockReadModel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sourceVersion != version {
		return errSyncSourceChanged
	}
	return s.indexDB.IndexBlockWithReadModel(block, readModel)
}

func (s *Syncer) loadLatestHeight(ctx context.Context, node string) (uint64, error) {
	if s.getLatestHeightOverride != nil {
		return s.getLatestHeightOverride(ctx, node)
	}
	return s.getLatestHeight(ctx, node)
}

func (s *Syncer) loadRemoteBlock(ctx context.Context, node string, height uint64) (*finalizedBlock, error) {
	if s.fetchBlockOverride != nil {
		block, err := s.fetchBlockOverride(ctx, node, height)
		if err != nil {
			return nil, err
		}
		                                                                          
		                                                                 
		model, err := emptyReadModelForBlock(block)
		if err != nil {
			return nil, err
		}
		return &finalizedBlock{Block: block, ReadModel: model}, nil
	}
	return s.fetchFinalizedBlock(ctx, node, height)
}

func (s *Syncer) newBlockLoader(node string, nodeDB *db.Manager, useLocalDB bool) blockLoader {
	return func(ctx context.Context, height uint64) (*finalizedBlock, error) {
		if useLocalDB && nodeDB != nil {
			return loadLocalBlock(nodeDB, height)
		}
		if node != "" {
			return s.loadRemoteBlock(ctx, node, height)
		}
		return nil, fmt.Errorf("no block source configured")
	}
}

func loadLocalBlock(nodeDB localKVReader, height uint64) (*finalizedBlock, error) {
	rawIDs, err := nodeDB.GetKV(keys.KeyHeightBlocks(height))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return nil, fmt.Errorf("%w: height %d", errBlockNotFound, height)
		}
		return nil, fmt.Errorf("read block height index %d: %w", height, err)
	}
	var blockIDs []string
	if err := json.Unmarshal(rawIDs, &blockIDs); err != nil {
		return nil, fmt.Errorf("decode block height index %d: %w", height, err)
	}
	if len(blockIDs) == 0 {
		return nil, fmt.Errorf("block height index %d is empty", height)
	}
	var missing []string
	for _, blockID := range blockIDs {
		rawBlock, err := nodeDB.GetKV(keys.KeyBlockData(blockID))
		if err != nil {
			if errors.Is(err, pebble.ErrNotFound) {
				missing = append(missing, blockID)
				continue
			}
			return nil, fmt.Errorf("read block %s at height %d: %w", blockID, height, err)
		}
		var block pb.Block
		if err := db.ProtoUnmarshal(rawBlock, &block); err != nil {
			return nil, fmt.Errorf("decode block %s at height %d: %w", blockID, height, err)
		}
		rawModel, modelErr := nodeDB.GetKV(keys.KeyBlockReadModel(block.BlockHash))
		if modelErr != nil {
			return nil, fmt.Errorf("read block read model %s at height %d: %w", block.BlockHash, height, modelErr)
		}
		var model pb.BlockReadModel
		if err := db.ProtoUnmarshal(rawModel, &model); err != nil {
			return nil, fmt.Errorf("decode block read model %s at height %d: %w", block.BlockHash, height, err)
		}
		if err := pb.VerifyBlockReadModel(&model, height, block.BlockHash); err != nil {
			return nil, fmt.Errorf("verify local block read model %s at height %d: %w", block.BlockHash, height, err)
		}
		if model.ParentId != block.Header.PrevBlockHash {
			return nil, fmt.Errorf("local block read model parent mismatch at height %d", height)
		}
		return &finalizedBlock{Block: &block, ReadModel: &model}, nil
	}
	return nil, fmt.Errorf("block height index %d references only missing blocks: %v", height, missing)
}

                                                                            
                                                                          
                                                                            
                                                                 
func (s *Syncer) discoverPartialHistoryBase(ctx context.Context, latestHeight uint64, loadBlock blockLoader) (uint64, *finalizedBlock, error) {
	if latestHeight < 2 {
		return 0, nil, fmt.Errorf("source tip %d has no retained non-genesis block", latestHeight)
	}
	window := s.partialHistoryWindow
	if window == 0 {
		window = defaultPartialHistoryWindow
	}
	lower := uint64(2)
	if latestHeight >= window {
		lower = latestHeight - window + 1
		if lower < 2 {
			lower = 2
		}
	}

	probed := make(map[uint64]*finalizedBlock)
	probe := func(height uint64) (*finalizedBlock, error) {
		if block, ok := probed[height]; ok {
			return block, nil
		}
		block, err := loadBlock(ctx, height)
		if err != nil {
			return nil, err
		}
		if block == nil || block.Block == nil || block.Block.Header == nil || block.ReadModel == nil {
			return nil, fmt.Errorf("source returned an empty block at height %d", height)
		}
		if block.Block.Header.Height != height {
			return nil, fmt.Errorf("source returned height %d for requested height %d", block.Block.Header.Height, height)
		}
		probed[height] = block
		return block, nil
	}
	if _, err := probe(latestHeight); err != nil {
		return 0, nil, fmt.Errorf("latest source block %d unavailable: %w", latestHeight, err)
	}
	if block, err := probe(lower); err == nil {
		return lower, block, nil
	} else if !errors.Is(err, errBlockNotFound) {
		return 0, nil, fmt.Errorf("probe block %d: %w", lower, err)
	}

	left, right := lower+1, latestHeight
	for left < right {
		mid := left + (right-left)/2
		if _, err := probe(mid); err == nil {
			right = mid
		} else if errors.Is(err, errBlockNotFound) {
			left = mid + 1
		} else {
			return 0, nil, fmt.Errorf("probe block %d: %w", mid, err)
		}
	}
	block, err := probe(left)
	if err != nil {
		return 0, nil, fmt.Errorf("first retained block %d unavailable: %w", left, err)
	}
	return left, block, nil
}

func emptyReadModelForBlock(block *pb.Block) (*pb.BlockReadModel, error) {
	if block == nil || block.Header == nil || block.BlockHash == "" {
		return nil, fmt.Errorf("cannot synthesize read model for empty block")
	}
	model := &pb.BlockReadModel{
		SchemaVersion: pb.BlockReadModelSchemaVersion,
		Height:        block.Header.Height,
		BlockId:       block.BlockHash,
		ParentId:      block.Header.PrevBlockHash,
	}
	if err := pb.SealBlockReadModel(model); err != nil {
		return nil, err
	}
	return model, nil
}

func lastSuccessfulHeight(syncHeight, lastSyncedHeight uint64) uint64 {
	if lastSyncedHeight > 0 {
		return lastSyncedHeight
	}
	return syncHeight
}

func formatHistoryStatus(history *indexdb.HistoryMetadata, status string) string {
	if history != nil && history.Partial {
		return fmt.Sprintf("partial history from height %d; %s", history.BaseHeight, status)
	}
	return status
}

func (s *Syncer) finishSync(status string) {
	s.mu.Lock()
	s.lastSync = time.Now()
	s.status = status
	s.mu.Unlock()
}
