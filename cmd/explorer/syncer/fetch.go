package syncer

import (
	"context"
	"dex/config"
	"dex/pb"
	"errors"
	"fmt"
	"io"
	"net/http"

	"google.golang.org/protobuf/proto"
)

var errBlockNotFound = errors.New("block_not_found")

const (
	maxSyncHeightResponseBytes = 64 << 10
	maxSyncBlockResponseBytes  = 128 << 20
	maxSyncErrorResponseBytes  = 4 << 10
)

                         
func (s *Syncer) setStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

                                                 
func (s *Syncer) getLatestHeight(ctx context.Context, node string) (uint64, error) {
	                                         
	url := fmt.Sprintf("%s/heightquery", node)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}
	setExplorerServiceHeaders(req.Header)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("heightquery request failed: %d", resp.StatusCode)
	}

	body, err := readSyncResponse(resp.Body, maxSyncHeightResponseBytes)
	if err != nil {
		return 0, err
	}

	                         
	var heightResp pb.HeightResponse
	if err := proto.Unmarshal(body, &heightResp); err != nil {
		return 0, fmt.Errorf("failed to unmarshal height response: %v", err)
	}

	                                                    
	return heightResp.LastAcceptedHeight, nil
}

                               
func (s *Syncer) syncBlock(ctx context.Context, node string, height uint64) error {
	finalized, err := s.fetchFinalizedBlock(ctx, node, height)
	if err != nil {
		return err
	}
	if err := s.indexDB.IndexBlockWithReadModel(finalized.Block, finalized.ReadModel); err != nil {
		return err
	}
	s.notifyBlockIndexed(finalized.Block.Header.Height)
	return nil
}

                                   
func (s *Syncer) fetchBlock(ctx context.Context, node string, height uint64) (*pb.Block, error) {
	finalized, err := s.fetchFinalizedBlock(ctx, node, height)
	if err != nil {
		return nil, err
	}
	return finalized.Block, nil
}

func (s *Syncer) fetchFinalizedBlock(ctx context.Context, node string, height uint64) (*finalizedBlock, error) {
	url := fmt.Sprintf("%s/getblock", node)

	                         
	reqProto := &pb.GetBlockRequest{Height: height, IncludeReadModel: true}
	reqBody, err := proto.Marshal(reqProto)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Node-Address", "explorer-syncer")
	setExplorerServiceHeaders(req.Header)
	req.Body = io.NopCloser(bytesReader(reqBody))

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := readSyncResponse(resp.Body, maxSyncErrorResponseBytes)
		if resp.StatusCode == http.StatusNotFound {
			return nil, errBlockNotFound
		}
		return nil, fmt.Errorf("getblock failed: %d - %s", resp.StatusCode, string(body))
	}

	body, err := readSyncResponse(resp.Body, maxSyncBlockResponseBytes)
	if err != nil {
		return nil, err
	}

	var blockResp pb.GetBlockResponse
	if err := proto.Unmarshal(body, &blockResp); err != nil {
		return nil, err
	}

	if blockResp.Error != "" {
		return nil, fmt.Errorf("getblock error: %s", blockResp.Error)
	}

	if blockResp.Block == nil || blockResp.Block.Header == nil {
		return nil, fmt.Errorf("getblock returned an empty block at height %d", height)
	}
	if blockResp.ReadModel == nil {
		return nil, fmt.Errorf("getblock returned no read model at height %d", height)
	}
	if err := pb.VerifyBlockReadModel(blockResp.ReadModel, blockResp.Block.Header.Height, blockResp.Block.BlockHash); err != nil {
		return nil, fmt.Errorf("verify block read model at height %d: %w", height, err)
	}
	if blockResp.ReadModel.ParentId != blockResp.Block.Header.PrevBlockHash {
		return nil, fmt.Errorf("block read model parent %q does not match block parent %q at height %d", blockResp.ReadModel.ParentId, blockResp.Block.Header.PrevBlockHash, height)
	}
	return &finalizedBlock{Block: blockResp.Block, ReadModel: blockResp.ReadModel}, nil
}

func setExplorerServiceHeaders(header http.Header) {
	header.Set(config.ExplorerServiceHeader, "explorer")
	if token := config.ExplorerServiceToken(); token != "" {
		header.Set(config.ExplorerServiceTokenHeader, token)
	}
}

func readSyncResponse(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("sync response exceeds %d-byte budget", limit)
	}
	return data, nil
}

type bytesReaderWrapper struct {
	*bytesBuffer
}

type bytesBuffer struct {
	data []byte
	pos  int
}

func bytesReader(data []byte) io.Reader {
	return &bytesBuffer{data: data}
}

func (b *bytesBuffer) Read(p []byte) (n int, err error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n = copy(p, b.data[b.pos:])
	b.pos += n
	return n, nil
}
