package sender

import (
	"bytes"
	"dex/pb"
	"dex/types"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"google.golang.org/protobuf/proto"
)

const (
	maxSyncResponseJSONBytes = int64(256 << 20)
	maxSyncBlockPayloadBytes = pb.MaxTotalSize
	maxSyncResponseBundles   = 64
)

func postProtobufAndRead(t *SendTask, client *http.Client, op string, path string, payload []byte) ([]byte, error) {
	return postProtobufAndReadLimited(t, client, op, path, payload, pb.MaxTotalSize)
}

func postProtobufAndReadLimited(t *SendTask, client *http.Client, op string, path string, payload []byte, maxBytes int64) ([]byte, error) {
	url := fmt.Sprintf("https://%s%s", t.Target, path)
	req, cancel, err := newSendRequest(t, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer cancel()
	req.Header.Set("Content-Type", "application/x-protobuf")
	if t.NodeAddress != "" {
		req.Header.Set("X-Node-Address", t.NodeAddress)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, responseStatusError(resp, op)
	}

	return readProtobufResponseBodyLimited(resp, op, maxBytes)
}

                                                         
func doSendHeightQuery(t *SendTask, client *http.Client) error {
	msg, ok := t.Message.(*heightQueryMessage)
	if !ok {
		return fmt.Errorf("doSendHeightQuery: message is not *heightQueryMessage, got %T", t.Message)
	}

	respBytes, err := postProtobufAndReadLimited(t, client, "doSendHeightQuery", "/heightquery", msg.requestData, maxControlResponseBytes)
	if err != nil {
		return err
	}

	var heightResp pb.HeightResponse
	if err := proto.Unmarshal(respBytes, &heightResp); err != nil {
		return fmt.Errorf("doSendHeightQuery: unmarshal HeightResponse fail: %v", err)
	}

	if msg.onSuccess != nil {
		msg.onSuccess(&heightResp)
	}
	return nil
}

                                                                         
func doSendAcceptedFrontier(t *SendTask, client *http.Client) error {
	msg, ok := t.Message.(*acceptedFrontierMessage)
	if !ok {
		return fmt.Errorf("doSendAcceptedFrontier: message is not *acceptedFrontierMessage, got %T", t.Message)
	}

	respBytes, err := postProtobufAndReadLimited(t, client, "doSendAcceptedFrontier", "/acceptedfrontier", msg.requestData, maxControlResponseBytes)
	if err != nil {
		return err
	}

	var frontierResp pb.AcceptedFrontierResponse
	if err := pb.UnmarshalSanitize(respBytes, &frontierResp); err != nil {
		return fmt.Errorf("doSendAcceptedFrontier: unmarshal response: %w", err)
	}
	if msg.onSuccess != nil {
		if err := msg.onSuccess(&frontierResp); err != nil {
			return err
		}
	}
	return nil
}

                                                      
func doSendSyncRequest(t *SendTask, client *http.Client) error {
	msg, ok := t.Message.(*syncRequestMessage)
	if !ok {
		return fmt.Errorf("doSendSyncRequest: message is not *syncRequestMessage, got %T", t.Message)
	}

	reqPayload, err := json.Marshal(types.SyncBlocksRequest{
		FromHeight: msg.fromHeight,
		ToHeight:   msg.toHeight,
	})
	if err != nil {
		return fmt.Errorf("doSendSyncRequest: marshal request failed: %w", err)
	}

	url := fmt.Sprintf("https://%s/getsyncblocks", t.Target)
	req, cancel, err := newSendRequest(t, http.MethodPost, url, bytes.NewReader(reqPayload))
	if err != nil {
		return err
	}
	defer cancel()
	req.Header.Set("Content-Type", "application/json")
	if t.NodeAddress != "" {
		req.Header.Set("X-Node-Address", t.NodeAddress)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return responseStatusError(resp, "doSendSyncRequest")
	}

	syncResp, err := decodeSyncHTTPResponse(resp, msg.fromHeight, msg.toHeight)
	if err != nil {
		return fmt.Errorf("doSendSyncRequest: %w", err)
	}
	if syncResp.Error != "" {
		return fmt.Errorf("doSendSyncRequest: remote error: %s", syncResp.Error)
	}
	if len(syncResp.Blocks) == 0 {
		return fmt.Errorf("doSendSyncRequest: no blocks fetched for heights %d-%d", msg.fromHeight, msg.toHeight)
	}

	if msg.onSuccessWithBundles != nil {
		msg.onSuccessWithBundles(syncResp)
	}
	return nil
}

func decodeSyncBlocksResponse(body io.Reader, fromHeight, toHeight uint64) (*types.SyncBlocksResponse, error) {
	if toHeight < fromHeight || toHeight-fromHeight >= maxSyncResponseBundles {
		return nil, fmt.Errorf("invalid requested range %d-%d", fromHeight, toHeight)
	}
	limited := &io.LimitedReader{R: body, N: maxSyncResponseJSONBytes + 1}
	decoder := json.NewDecoder(limited)
	var syncResp types.SyncBlocksResponse
	opening, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode json object: %w", err)
	}
	if delim, ok := opening.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("response must be a JSON object")
	}
	expectedCount := int(toHeight - fromHeight + 1)
	seenBlocks := false
	seenError := false
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode response field: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("response field name is not a string")
		}
		switch key {
		case "blocks":
			if seenBlocks {
				return nil, fmt.Errorf("response contains duplicate blocks field")
			}
			seenBlocks = true
			arrayStart, err := decoder.Token()
			if err != nil {
				return nil, fmt.Errorf("decode blocks array: %w", err)
			}
			if delim, ok := arrayStart.(json.Delim); !ok || delim != '[' {
				return nil, fmt.Errorf("blocks field must be an array")
			}
			for decoder.More() {
				if len(syncResp.Blocks) >= expectedCount || len(syncResp.Blocks) >= maxSyncResponseBundles {
					return nil, fmt.Errorf("response contains more than %d bundles", expectedCount)
				}
				var bundle types.SyncBlockBundle
				if err := decoder.Decode(&bundle); err != nil {
					return nil, fmt.Errorf("decode bundle %d: %w", len(syncResp.Blocks), err)
				}
				i := len(syncResp.Blocks)
				expectedHeight := fromHeight + uint64(i)
				if bundle.Height != expectedHeight {
					return nil, fmt.Errorf("bundle %d has height %d, expected %d", i, bundle.Height, expectedHeight)
				}
				if bundle.BlockID == "" || len(bundle.BlockID) > 512 {
					return nil, fmt.Errorf("bundle %d has invalid block ID", i)
				}
				if len(bundle.BlockBytes) == 0 || len(bundle.BlockBytes) > maxSyncBlockPayloadBytes {
					return nil, fmt.Errorf("bundle %d block payload size %d is outside 1..%d", i, len(bundle.BlockBytes), maxSyncBlockPayloadBytes)
				}
				syncResp.Blocks = append(syncResp.Blocks, bundle)
			}
			arrayEnd, err := decoder.Token()
			if err != nil {
				return nil, fmt.Errorf("decode blocks array end: %w", err)
			}
			if delim, ok := arrayEnd.(json.Delim); !ok || delim != ']' {
				return nil, fmt.Errorf("invalid blocks array terminator")
			}
		case "error":
			if seenError {
				return nil, fmt.Errorf("response contains duplicate error field")
			}
			seenError = true
			if err := decoder.Decode(&syncResp.Error); err != nil {
				return nil, fmt.Errorf("decode response error: %w", err)
			}
			if len(syncResp.Error) > 1024 {
				return nil, fmt.Errorf("response error text exceeds 1024 bytes")
			}
		default:
			return nil, fmt.Errorf("response contains unknown field %q", key)
		}
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode json object end: %w", err)
	}
	if delim, ok := closing.(json.Delim); !ok || delim != '}' {
		return nil, fmt.Errorf("invalid response object terminator")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("response contains multiple JSON values")
		}
		return nil, fmt.Errorf("decode trailing response data: %w", err)
	}
	if limited.N == 0 {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxSyncResponseJSONBytes)
	}
	if syncResp.Error != "" {
		return &syncResp, nil
	}
	if len(syncResp.Blocks) != expectedCount {
		return nil, fmt.Errorf("response has %d bundles, expected %d", len(syncResp.Blocks), expectedCount)
	}
	return &syncResp, nil
}

                                                   
func doSendGetBlock(t *SendTask, client *http.Client) error {
	msg, ok := t.Message.(*pullBlockMessage)
	if !ok {
		return fmt.Errorf("doSendGetBlock: message is not *pullBlockMessage, got %T", t.Message)
	}

	respBytes, err := postProtobufAndReadLimited(t, client, "doSendGetBlock", "/getblock", msg.requestData, maxBlockResponseBytes)
	if err != nil {
		return err
	}

	var blockResp pb.GetBlockResponse
	if err := proto.Unmarshal(respBytes, &blockResp); err != nil {
		return fmt.Errorf("doSendGetBlock: unmarshal GetBlockResponse err=%v", err)
	}
	if blockResp.Error != "" {
		return fmt.Errorf("doSendGetBlock: remote error: %s", blockResp.Error)
	}

	if msg.onSuccess != nil && blockResp.Block != nil {
		msg.onSuccess(blockResp.Block)
	}
	return nil
}

                                                                    
func doSendGetBlockByID(t *SendTask, client *http.Client) error {
	msg, ok := t.Message.(*pullBlockByIDMessage)
	if !ok {
		return fmt.Errorf("doSendGetBlockByID: message is not *pullBlockByIDMessage, got %T", t.Message)
	}

	respBytes, err := postProtobufAndReadLimited(t, client, "doSendGetBlockByID", "/getblockbyid", msg.requestData, maxBlockResponseBytes)
	if err != nil {
		return err
	}

	var gbr pb.GetBlockResponse
	if err := proto.Unmarshal(respBytes, &gbr); err != nil {
		return fmt.Errorf("doSendGetBlockByID: bad GetBlockResponse: %w", err)
	}
	if gbr.Block == nil {
		return fmt.Errorf("doSendGetBlockByID: empty block in response")
	}
	if gbr.Block.BlockHash != msg.blockID {
		return fmt.Errorf("doSendGetBlockByID: returned block ID mismatch, want %s, got %s", msg.blockID, gbr.Block.BlockHash)
	}

	if msg.onSuccess != nil {
		msg.onSuccess(gbr.Block)
	}
	return nil
}

                                                   
func doSendBatchGetTxs(t *SendTask, client *http.Client) error {
	msg, ok := t.Message.(*pullBatchTxMessage)
	if !ok {
		return fmt.Errorf("doSendBatchGetTxs: message is not *pullBatchTxMessage, got %T", t.Message)
	}

	respBytes, err := postProtobufAndRead(t, client, "doSendBatchGetTxs", "/batchgetdata", msg.requestData)
	if err != nil {
		return err
	}

	var respMsg pb.BatchGetShortTxResponse
	if err := proto.Unmarshal(respBytes, &respMsg); err != nil {
		return fmt.Errorf("doSendBatchGetTxs: unmarshal BatchGetDataResponse failed: %v", err)
	}

	if msg.onSuccess != nil {
		msg.onSuccess(respMsg.Transactions)
	}
	return nil
}
