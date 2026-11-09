package sender

import (
	"bytes"
	"context"
	"dex/logs"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

func newSendRequest(t *SendTask, method, url string, body io.Reader) (*http.Request, func(), error) {
	ctx := context.Background()
	timeout := 30 * time.Second
	if t != nil && t.Timeout > 0 {
		timeout = t.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	                                                                       
	                                                            
	req.Header.Set("Accept-Encoding", "gzip")
	return req, cancel, nil
}

func postProtobufNoReply(t *SendTask, client *http.Client, op string, path string, payload []byte) (sendErr error) {
	url := fmt.Sprintf("https://%s%s", t.Target, path)
	req, cancel, err := newSendRequest(t, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	defer cancel()
	req, trace := attachPushHTTPTrace(t, req)
	if trace != nil {
		defer func() { trace.finish(t, len(payload), sendErr) }()
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	if t.NodeAddress != "" {
		req.Header.Set("X-Node-Address", t.NodeAddress)
	}

	resp, err := client.Do(req)
	trace.gotResponse(resp)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return responseStatusError(resp, op)
	}

	_, err = readProtobufResponseBodyLimited(resp, op, maxControlResponseBytes)
	return err
}

                                        
func doSendTx(t *SendTask, client *http.Client) error {
	payload, ok := t.Message.([]byte)
	if !ok {
		return fmt.Errorf("doSendTx: message is not []byte, got %T", t.Message)
	}

	return postProtobufNoReply(t, client, "doSendTx", "/tx", payload)
}

type txBatchMessage struct {
	requestData []byte
	count       int
}

                                                                                           
func doSendTxBatch(t *SendTask, client *http.Client) error {
	msg, ok := t.Message.(*txBatchMessage)
	if !ok {
		return fmt.Errorf("doSendTxBatch: message is not *txBatchMessage, got %T", t.Message)
	}
	return postProtobufNoReply(t, client, "doSendTxBatch", "/txbatch", msg.requestData)
}

                                            
func doSendBlock(t *SendTask, client *http.Client) error {
	msg, ok := t.Message.(*blockMessage)
	if !ok {
		return fmt.Errorf("doSendBlock: message is not *blockMessage, got %T", t.Message)
	}
	return postProtobufNoReply(t, client, "doSendBlock", "/put", msg.requestData)
}

                                               
func doSendChits(t *SendTask, client *http.Client) error {
	msg, ok := t.Message.(*chitsMessage)
	if !ok {
		return fmt.Errorf("doSendChits: message is not *chitsMessage, got %T", t.Message)
	}
	return postProtobufNoReply(t, client, "doSendChits", "/chits", msg.requestData)
}

                                                     
func doSendPushQuery(t *SendTask, client *http.Client) error {
	msg, ok := t.Message.(*pushQueryMsg)
	if !ok {
		return fmt.Errorf("doSendPushQuery: message is not *pushQueryMsg, got %T", t.Message)
	}
	                                                                  
	return postProtobufNoReply(t, client, "doSendPushQuery", "/pushquery", msg.requestData)
}

                                                     
func doSendPullQuery(t *SendTask, client *http.Client) error {
	msg, ok := t.Message.(*pullQueryMsg)
	if !ok {
		return fmt.Errorf("doSendPullQuery: message is not *pullQueryMsg, got %T", t.Message)
	}
	                                                                  
	return postProtobufNoReply(t, client, "doSendPullQuery", "/pullquery", msg.requestData)
}

                                                             
func doSendToOnePeer(t *SendTask, client *http.Client) error {
	msgBytes, ok := t.Message.([]byte)
	if !ok {
		return fmt.Errorf("doSendToOnePeer: message is not []byte, got %T", t.Message)
	}
	if t.Target == "" {
		return errors.New("doSendToOnePeer: empty target ip")
	}

	if err := postProtobufNoReply(t, client, "doSendToOnePeer", "/gossipAnyMsg", msgBytes); err != nil {
		return err
	}

	logs.Trace("[Gossip] success to %s, messageLen=%d", t.Target, len(msgBytes))
	return nil
}
