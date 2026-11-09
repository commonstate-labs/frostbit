package sender

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"dex/frost/security"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

                                                                             
                                                                               
func (sm *SenderManager) SendFrostBatchToAddress(ctx context.Context, address string, envelopes []*pb.FrostEnvelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(envelopes) == 0 || len(envelopes) > security.FrostTransportBatchItems {
		return fmt.Errorf("invalid FROST batch count")
	}
	batch := &pb.FrostEnvelopeBatch{Envelopes: make([][]byte, len(envelopes))}
	for i, envelope := range envelopes {
		if envelope == nil || envelope.To != address {
			return fmt.Errorf("invalid FROST batch recipient at %d", i)
		}
		raw, err := proto.Marshal(envelope)
		if err != nil {
			return err
		}
		batch.Envelopes[i] = raw
	}
	if proto.Size(batch) > security.FrostTransportBatchBytes {
		return fmt.Errorf("FROST batch exceeds byte budget")
	}
	payload, err := proto.Marshal(batch)
	if err != nil {
		return err
	}
	ip, err := sm.AddressToIP(address)
	if err != nil {
		return err
	}
	timeout := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
		if timeout <= 0 {
			return context.DeadlineExceeded
		}
	}
	task := &SendTask{
		Target: ip, NodeAddress: sm.address, Message: payload,
		Priority: PriorityControl, MaxRetries: frostSendMaxRetries, SendFunc: doSendFrostBatch,
		Timeout: timeout,
	}
	if !sm.SendQueue.Enqueue(task) {
		return fmt.Errorf("FROST batch send queue is full")
	}
	                                                                         
	                                                                          
	                                                                        
	                                                                              
	return nil
}

func doSendFrostBatch(task *SendTask, client *http.Client) error {
	payload, ok := task.Message.([]byte)
	if !ok {
		return fmt.Errorf("invalid FROST batch task")
	}
	var sent pb.FrostEnvelopeBatch
	if err := proto.Unmarshal(payload, &sent); err != nil {
		return err
	}
	body, err := postProtobufAndReadLimited(task, client, "FROST batch", "/frostmsgbatch", payload, 4096)
	if err != nil {
		return err
	}
	var reply pb.FrostEnvelopeBatchReply
	if err := pb.UnmarshalSanitize(body, &reply); err != nil {
		return err
	}
	if len(reply.Statuses) != len(sent.Envelopes) {
		return fmt.Errorf("incomplete FROST batch result")
	}
	var rejected int
	for _, code := range reply.Statuses {
		if code == http.StatusTooManyRequests || code >= 500 {
			return &httpStatusError{statusCode: int(code)}
		}
		if code != http.StatusOK {
			rejected = int(code)
		}
	}
	if rejected != 0 {
		return &httpStatusError{statusCode: rejected}
	}
	return nil
}
