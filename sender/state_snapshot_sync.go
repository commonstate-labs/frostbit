package sender

import (
	"bytes"
	"dex/pb"
	"dex/types"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	pathutil "path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
)

const (
	checkpointChunkSizeLimit    = int64(10 << 20)
	checkpointChunkMinSize      = int64(1 << 20)
	checkpointChunkHTTPTimeout  = 10 * time.Second
	checkpointChunkMaxAttempts  = 4
	checkpointChunkRetryBackoff = 300 * time.Millisecond
)

func (sm *SenderManager) FetchStateSnapshotShards(targetAddress string, targetHeight uint64) (*types.StateSnapshotShardsResponse, error) {
	ip, err := sm.AddressToIP(targetAddress)
	if err != nil {
		return nil, err
	}

	req := types.StateSnapshotShardsRequest{TargetHeight: targetHeight}
	var resp types.StateSnapshotShardsResponse
	if err := sm.postJSON(ip, "/statedb/snapshot/shards", req, &resp); err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("remote error: %s", resp.Error)
	}
	return &resp, nil
}

func (sm *SenderManager) FetchStateCheckpointFiles(targetAddress string, targetHeight uint64) (string, func(), error) {
	ip, err := sm.AddressToIP(targetAddress)
	if err != nil {
		return "", nil, err
	}

	var manifest types.StateCheckpointFilesResponse
	if err := sm.postJSON(ip, "/statedb/checkpoint/files", types.StateCheckpointFilesRequest{TargetHeight: targetHeight}, &manifest); err != nil {
		return "", nil, err
	}
	if manifest.Error != "" {
		return "", nil, fmt.Errorf("remote error: %s", manifest.Error)
	}
	if manifest.SnapshotHeight != targetHeight {
		return "", nil, fmt.Errorf("checkpoint height mismatch: got=%d want=%d", manifest.SnapshotHeight, targetHeight)
	}
	if manifest.ChunkSize <= 0 || manifest.ChunkSize > checkpointChunkSizeLimit {
		return "", nil, fmt.Errorf("invalid checkpoint chunk size %d", manifest.ChunkSize)
	}
	if len(manifest.Files) == 0 {
		return "", nil, fmt.Errorf("empty checkpoint file manifest at height %d", targetHeight)
	}

	var totalBytes int64
	for _, file := range manifest.Files {
		if file.Size < 0 {
			return "", nil, fmt.Errorf("invalid checkpoint file size %d for %s", file.Size, file.Name)
		}
		totalBytes += file.Size
	}
	if manifest.TotalBytes != 0 && manifest.TotalBytes != totalBytes {
		return "", nil, fmt.Errorf("checkpoint total size mismatch: manifest=%d files=%d", manifest.TotalBytes, totalBytes)
	}

	tmpDir, err := os.MkdirTemp("", "dex-state-checkpoint-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	var downloaded int64
	lastProgress := time.Time{}
	chunkLimit := manifest.ChunkSize

	for _, file := range manifest.Files {
		localPath, err := checkpointDownloadPath(tmpDir, file.Name)
		if err != nil {
			cleanup()
			return "", nil, err
		}
		if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
			cleanup()
			return "", nil, err
		}
		out, err := os.OpenFile(localPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			cleanup()
			return "", nil, err
		}
		for offset := int64(0); offset < file.Size; {
			limit := chunkLimit
			if remain := file.Size - offset; remain < limit {
				limit = remain
			}
			chunk, usedLimit, err := sm.fetchStateCheckpointChunkWithRetry(ip, targetHeight, file.Name, offset, limit)
			if err != nil {
				_ = out.Close()
				cleanup()
				return "", nil, err
			}
			n := int64(len(chunk))
			if n != usedLimit {
				_ = out.Close()
				cleanup()
				return "", nil, fmt.Errorf("checkpoint chunk short read file=%s offset=%d got=%d want=%d", file.Name, offset, n, usedLimit)
			}
			if _, err := out.Write(chunk); err != nil {
				_ = out.Close()
				cleanup()
				return "", nil, err
			}
			downloaded += n
			offset += usedLimit
			if usedLimit < limit {
				chunkLimit = usedLimit
			}
			if sm.Logger != nil && (time.Since(lastProgress) >= 2*time.Second || downloaded == totalBytes) {
				sm.logCheckpointDownloadProgress(ip, targetHeight, downloaded, totalBytes)
				lastProgress = time.Now()
			}
		}
		if err := out.Close(); err != nil {
			cleanup()
			return "", nil, err
		}
		if info, err := os.Stat(localPath); err != nil {
			cleanup()
			return "", nil, err
		} else if info.Size() != file.Size {
			cleanup()
			return "", nil, fmt.Errorf("checkpoint file size mismatch file=%s got=%d want=%d", file.Name, info.Size(), file.Size)
		}
	}
	if downloaded != totalBytes {
		cleanup()
		return "", nil, fmt.Errorf("checkpoint download size mismatch got=%d want=%d", downloaded, totalBytes)
	}
	return tmpDir, cleanup, nil
}

func (sm *SenderManager) fetchStateCheckpointChunkWithRetry(targetIP string, targetHeight uint64, name string, offset, limit int64) ([]byte, int64, error) {
	var lastErr error
	currentLimit := limit
	minLimit := checkpointChunkMinSize
	if limit < minLimit {
		minLimit = limit
	}
	for attempt := 1; attempt <= checkpointChunkMaxAttempts; attempt++ {
		chunk, err := sm.fetchStateCheckpointChunk(targetIP, targetHeight, name, offset, currentLimit)
		if err == nil {
			if attempt > 1 && sm.Logger != nil {
				sm.Logger.Info("[StateCheckpointSync] chunk recovered peer=%s height=%d file=%s offset=%d size=%d attempt=%d", targetIP, targetHeight, name, offset, currentLimit, attempt)
			}
			return chunk, currentLimit, nil
		}
		lastErr = err
		if sm.Logger != nil {
			sm.Logger.Warn("[StateCheckpointSync] chunk download failed peer=%s height=%d file=%s offset=%d size=%d attempt=%d/%d err=%v", targetIP, targetHeight, name, offset, currentLimit, attempt, checkpointChunkMaxAttempts, err)
		}
		if attempt < checkpointChunkMaxAttempts {
			if nextLimit := nextCheckpointChunkLimit(currentLimit, minLimit); nextLimit < currentLimit {
				if sm.Logger != nil {
					sm.Logger.Warn("[StateCheckpointSync] shrinking chunk peer=%s height=%d file=%s offset=%d size=%d->%d", targetIP, targetHeight, name, offset, currentLimit, nextLimit)
				}
				currentLimit = nextLimit
			}
			time.Sleep(checkpointChunkRetryBackoff * time.Duration(attempt))
		}
	}
	return nil, 0, lastErr
}

func nextCheckpointChunkLimit(current, minLimit int64) int64 {
	if current <= minLimit {
		return current
	}
	next := current / 2
	if next < minLimit {
		return minLimit
	}
	return next
}

func (sm *SenderManager) fetchStateCheckpointChunk(targetIP string, targetHeight uint64, name string, offset, limit int64) ([]byte, error) {
	if limit <= 0 || limit > checkpointChunkSizeLimit {
		return nil, fmt.Errorf("invalid checkpoint chunk limit %d", limit)
	}
	payload, err := json.Marshal(types.StateCheckpointChunkRequest{
		TargetHeight: targetHeight,
		Name:         name,
		Offset:       offset,
		Limit:        limit,
	})
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("https://%s/statedb/checkpoint/chunk", targetIP)
	req, cancel, err := newSendRequest(&SendTask{Timeout: checkpointChunkHTTPTimeout}, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer cancel()
	req.Header.Set("Content-Type", "application/json")
	if sm.address != "" {
		req.Header.Set("X-Node-Address", sm.address)
	}

	resp, err := sm.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseStatusError(resp, "checkpoint chunk")
	}
	chunk, err := readProtobufResponseBodyLimited(resp, "checkpoint chunk", limit)
	if err != nil {
		return nil, fmt.Errorf("checkpoint chunk read file=%s offset=%d: %w", name, offset, err)
	}
	if int64(len(chunk)) != limit {
		return nil, fmt.Errorf("checkpoint chunk short read file=%s offset=%d got=%d want=%d", name, offset, len(chunk), limit)
	}
	return chunk, nil
}

func (sm *SenderManager) logCheckpointDownloadProgress(targetIP string, height uint64, downloaded, total int64) {
	if sm == nil || sm.Logger == nil {
		return
	}
	pct := float64(100)
	if total > 0 {
		pct = float64(downloaded) * 100 / float64(total)
	}
	sm.Logger.Info("[StateCheckpointSync] download height=%d peer=%s progress=%d/%d bytes %.1f%%", height, targetIP, downloaded, total, pct)
}

func checkpointDownloadPath(root, name string) (string, error) {
	if name == "" || strings.Contains(name, "\x00") || strings.Contains(name, "\\") {
		return "", fmt.Errorf("invalid checkpoint file name: %q", name)
	}
	clean := pathutil.Clean(name)
	if clean == "." || clean == ".." || pathutil.IsAbs(clean) || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid checkpoint file path: %q", name)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(rootAbs, filepath.FromSlash(clean))
	rel, err := filepath.Rel(rootAbs, dst)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("checkpoint file escapes target: %s", name)
	}
	return dst, nil
}

func (sm *SenderManager) postJSON(targetIP, path string, reqObj interface{}, respObj interface{}) error {
	payload, err := json.Marshal(reqObj)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://%s%s", targetIP, path)
	req, cancel, err := newSendRequest(nil, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer cancel()
	req.Header.Set("Content-Type", "application/json")
	if sm.address != "" {
		req.Header.Set("X-Node-Address", sm.address)
	}

	resp, err := sm.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return responseStatusError(resp, path)
	}

	body, err := readProtobufResponseBodyLimited(resp, path, maxMetadataResponseBytes)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return fmt.Errorf("empty response body")
	}

	if err := json.Unmarshal(body, respObj); err != nil {
		return fmt.Errorf("decode json failed: %w", err)
	}
	return nil
}

func (sm *SenderManager) FetchBlockStateRoot(targetIP string, height uint64) ([]byte, error) {
	block, err := sm.FetchFinalizedBlock(targetIP, height)
	if err != nil {
		return nil, err
	}
	return block.Header.StateRoot, nil
}

func (sm *SenderManager) FetchFinalizedBlock(targetIP string, height uint64) (*pb.Block, error) {
	payload, err := proto.Marshal(&pb.GetBlockRequest{Height: height})
	if err != nil {
		return nil, err
	}
	body, err := postProtobufAndReadLimited(&SendTask{Target: targetIP, NodeAddress: sm.address},
		sm.httpClient, "finalized block", "/getblock", payload, maxBlockResponseBytes)
	if err != nil {
		return nil, err
	}
	var blockResp pb.GetBlockResponse
	if err := proto.Unmarshal(body, &blockResp); err != nil {
		return nil, err
	}
	if blockResp.Error != "" {
		return nil, fmt.Errorf("remote: %s", blockResp.Error)
	}
	if blockResp.Block == nil || blockResp.Block.Header == nil {
		return nil, fmt.Errorf("block not found at height %d", height)
	}
	return blockResp.Block, nil
}

func (sm *SenderManager) getResponseBytes(url string, limit int64) ([]byte, error) {
	req, cancel, err := newSendRequest(nil, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer cancel()
	if sm.address != "" {
		req.Header.Set("X-Node-Address", sm.address)
	}
	resp, err := sm.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseStatusError(resp, "certificate")
	}
	return readProtobufResponseBodyLimited(resp, "certificate", limit)
}

                                                                                 
                                                                                     
                                                                        
func (sm *SenderManager) FetchCertChain(targetIP string, fromHeight, toHeight, interval uint64) ([][]byte, error) {
	if interval == 0 {
		interval = 1000
	}
	if certs, err := sm.fetchCertChainBulk(targetIP, fromHeight, toHeight, interval); err == nil && len(certs) > 0 {
		return certs, nil
	}
	var certs [][]byte
	for h := fromHeight; h <= toHeight; {
		if h > 0 {
			url := fmt.Sprintf("https://%s/cert/get?height=%d", targetIP, h)
			body, err := sm.getResponseBytes(url, pb.MaxTotalSize)
			if err == nil && len(body) > 0 {
				certs = append(certs, body)
			}
		}
		if toHeight-h < interval {
			break
		}
		h += interval
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("no certificates fetched from %s (range %d-%d)", targetIP, fromHeight, toHeight)
	}
	return certs, nil
}

type certChainEntry struct {
	Height uint64 `json:"height"`
	Data   []byte `json:"data"`
}

type certChainResponse struct {
	Certs    []certChainEntry `json:"certs"`
	Count    int              `json:"count"`
	HasMore  bool             `json:"has_more"`
	NextFrom uint64           `json:"next_from"`
}

func (sm *SenderManager) fetchCertChainBulk(targetIP string, fromHeight, toHeight, interval uint64) ([][]byte, error) {
	cursor := fromHeight
	var entries []certChainEntry
	for {
		                                                                         
		                                                                            
		url := fmt.Sprintf("https://%s/cert/chain?from=%d&to=%d&limit=4", targetIP, cursor, toHeight)
		body, err := sm.getResponseBytes(url, maxMetadataResponseBytes)
		if err != nil {
			return nil, err
		}
		var chain certChainResponse
		if err := json.Unmarshal(body, &chain); err != nil {
			return nil, err
		}
		if chain.Count != len(chain.Certs) {
			return nil, fmt.Errorf("certificate chain page from=%d count mismatch: declared=%d actual=%d", cursor, chain.Count, len(chain.Certs))
		}
		sort.Slice(chain.Certs, func(i, j int) bool { return chain.Certs[i].Height < chain.Certs[j].Height })
		                                                                     
		                                                                        
		for i, cert := range chain.Certs {
			if cert.Height < cursor || cert.Height > toHeight || (i > 0 && cert.Height == chain.Certs[i-1].Height) {
				return nil, fmt.Errorf("certificate chain page contains duplicate or out-of-range height %d", cert.Height)
			}
			if len(cert.Data) > pb.MaxTotalSize {
				return nil, fmt.Errorf("certificate at height %d exceeds %d bytes", cert.Height, pb.MaxTotalSize)
			}
		}
		entries = append(entries, chain.Certs...)
		if !chain.HasMore {
			break
		}
		if len(chain.Certs) == 0 {
			return nil, fmt.Errorf("certificate chain page from=%d is empty but has_more=true", cursor)
		}
		lastHeight := chain.Certs[len(chain.Certs)-1].Height
		if lastHeight == ^uint64(0) {
			return nil, fmt.Errorf("certificate chain cursor overflows after height %d", lastHeight)
		}
		wantNext := lastHeight + 1
		if chain.NextFrom != wantNext || chain.NextFrom <= cursor || chain.NextFrom > toHeight {
			return nil, fmt.Errorf("invalid certificate chain cursor after height %d: got=%d want=%d range=%d-%d", lastHeight, chain.NextFrom, wantNext, cursor, toHeight)
		}
		cursor = chain.NextFrom
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Height < entries[j].Height })
	certs := make([][]byte, 0, len(entries))
	for _, cert := range entries {
		if cert.Height == 0 || cert.Height < fromHeight || cert.Height > toHeight || (interval > 0 && cert.Height%interval != 0) || len(cert.Data) == 0 {
			continue
		}
		certs = append(certs, cert.Data)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("no certificates fetched from bulk %s (range %d-%d)", targetIP, fromHeight, toHeight)
	}
	return certs, nil
}
