                        
                               

package sender

import (
	"fmt"
	"net/http"

	"dex/logs"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                  
type frostMessage struct {
	envelope *pb.FrostEnvelope
}

                                                                           
                                                                               
                                                                                
const frostSendMaxRetries = 7

                                  
func doSendFrost(t *SendTask, client *http.Client) error {
	fm, ok := t.Message.(*frostMessage)
	if !ok {
		return fmt.Errorf("doSendFrost: message is not *frostMessage")
	}

	data, err := proto.Marshal(fm.envelope)
	if err != nil {
		return fmt.Errorf("doSendFrost: marshal failed: %w", err)
	}

	return postProtobufNoReply(t, client, "doSendFrost", "/frostmsg", data)
}

                                               
func (sm *SenderManager) SendFrost(targetIP string, envelope *pb.FrostEnvelope) error {
	if targetIP == "" {
		return fmt.Errorf("SendFrost: empty target IP")
	}

	msg := &frostMessage{envelope: envelope}

	task := &SendTask{
		Target:     targetIP,
		Message:    msg,
		RetryCount: 0,
		MaxRetries: frostSendMaxRetries,
		SendFunc:   doSendFrost,
		Priority:   PriorityControl,                                        
	}

	if !sm.SendQueue.Enqueue(task) {
		return fmt.Errorf("SendFrost: send queue rejected task for %s", targetIP)
	}
	logs.Debug("[SendFrost] enqueued to %s, kind=%v, job=%s", targetIP, envelope.Kind, envelope.JobId)
	return nil
}

                                                                                  
func (sm *SenderManager) SendFrostToAddress(targetAddress string, envelope *pb.FrostEnvelope) error {
	ip, err := sm.AddressToIP(targetAddress)
	if err != nil {
		return fmt.Errorf("SendFrostToAddress: failed to get IP for %s: %w", targetAddress, err)
	}

	return sm.SendFrost(ip, envelope)
}

                                                    
func (sm *SenderManager) BroadcastFrost(targetIPs []string, envelope *pb.FrostEnvelope) {
	for _, ip := range targetIPs {
		if err := sm.SendFrost(ip, envelope); err != nil {
			logs.Debug("[BroadcastFrost] failed to send to %s: %v", ip, err)
		}
	}
}
