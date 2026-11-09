package sender

import (
	"compress/gzip"
	"dex/pb"
	"dex/types"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
)

const (
	maxControlResponseBytes  = int64(16 << 10)
	maxErrorResponseBytes    = int64(16 << 10)
	maxMetadataResponseBytes = int64(16 << 20)
	                                                                        
	                                                        
	maxBlockResponseBytes = int64(pb.MaxTotalSize) + maxControlResponseBytes
)

                                                                            
                                                                              
type responseBudgetReader struct {
	r         io.Reader
	remaining int64
	err       error
}

func (r *responseBudgetReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		var extra [1]byte
		n, err := r.r.Read(extra[:])
		if n > 0 {
			return 0, r.err
		}
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.r.Read(p)
	r.remaining -= int64(n)
	return n, err
}

                                                                           
                                                                         
func boundedResponseReader(resp *http.Response, op string, maxBytes int64) (io.Reader, func(), error) {
	if maxBytes <= 0 || maxBytes > math.MaxInt64/2 {
		return nil, nil, fmt.Errorf("%s: invalid response limit %d", op, maxBytes)
	}
	                                                                          
	                                                                           
	wireLimit := maxBytes + maxBytes/1000 + (64 << 10)
	if resp.ContentLength > wireLimit {
		return nil, nil, fmt.Errorf("%s: response body exceeds wire limit %d bytes", op, wireLimit)
	}
	var reader io.Reader = &responseBudgetReader{r: resp.Body, remaining: wireLimit,
		err: fmt.Errorf("%s: response body exceeds wire limit %d bytes", op, wireLimit)}
	closeReader := func() {}
	switch strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case "gzip":
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: cannot create gzip reader: %w", op, err)
		}
		reader = gz
		closeReader = func() { _ = gz.Close() }
	default:
		return nil, nil, fmt.Errorf("%s: unsupported response encoding", op)
	}
	return &responseBudgetReader{r: reader, remaining: maxBytes,
		err: fmt.Errorf("%s: response body exceeds %d bytes", op, maxBytes)}, closeReader, nil
}

func readProtobufResponseBody(resp *http.Response, op string) ([]byte, error) {
	return readProtobufResponseBodyLimited(resp, op, pb.MaxTotalSize)
}

                                                                       
func readProtobufResponseBodyLimited(resp *http.Response, op string, maxBytes int64) ([]byte, error) {
	reader, closeReader, err := boundedResponseReader(resp, op, maxBytes)
	if err != nil {
		return nil, responseReadError(resp, err)
	}
	defer closeReader()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("%s: read body error: %w", op, responseReadError(resp, err))
	}
	return data, nil
}

                                                                            
                                                                               
func responseReadError(resp *http.Response, err error) error {
	if err != nil && resp.Request != nil && resp.Request.Context().Err() != nil {
		return resp.Request.Context().Err()
	}
	return err
}

func responseErrorText(resp *http.Response, op string) string {
	body, err := readProtobufResponseBodyLimited(resp, op, maxErrorResponseBytes)
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(string(body))
}

func responseStatusError(resp *http.Response, op string) error {
	return &httpStatusError{op: op, statusCode: resp.StatusCode, body: responseErrorText(resp, op)}
}

func decodeSyncHTTPResponse(resp *http.Response, from, to uint64) (*types.SyncBlocksResponse, error) {
	reader, closeReader, err := boundedResponseReader(resp, "sync response", maxSyncResponseJSONBytes)
	if err != nil {
		return nil, responseReadError(resp, err)
	}
	defer closeReader()
	result, err := decodeSyncBlocksResponse(reader, from, to)
	return result, responseReadError(resp, err)
}
