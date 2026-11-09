package handlers

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"dex/frost/security"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

func decodeFrostBatch(body []byte) (*pb.FrostEnvelopeBatch, error) {
	if len(body) > security.FrostTransportBatchBytes {
		return nil, fmt.Errorf("FROST batch exceeds byte budget")
	}
	var batch pb.FrostEnvelopeBatch
	if err := pb.UnmarshalSanitize(body, &batch); err != nil {
		return nil, err
	}
	if len(batch.Envelopes) == 0 || len(batch.Envelopes) > security.FrostTransportBatchItems {
		return nil, fmt.Errorf("invalid FROST batch item count")
	}
	return &batch, nil
}

                                                                             
                                                                            
func (hm *HandlerManager) HandleFrostBatch(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleFrostBatch")
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, security.FrostTransportBatchBytes+1))
	if err != nil {
		http.Error(w, "Failed to read FROST batch", http.StatusBadRequest)
		return
	}
	batch, err := decodeFrostBatch(body)
	if err != nil {
		http.Error(w, "Invalid FROST batch", http.StatusBadRequest)
		return
	}
	reply := &pb.FrostEnvelopeBatchReply{Statuses: make([]uint32, 0, len(batch.Envelopes))}
	for _, raw := range batch.Envelopes {
		if err := r.Context().Err(); err != nil {
			return
		}
		if hm.peerGate != nil {
			ip := extractIP(r)
			if hm.peerGate.enforceLoopback && (ip == "127.0.0.1" || ip == "::1") && r.Header.Get("X-Node-Address") != "" {
				ip = "loopback:" + r.Header.Get("X-Node-Address")
			}
			if !hm.peerGate.verifyAndPromote(ip, "/frostmsg", raw) {
				reply.Statuses = append(reply.Statuses, http.StatusForbidden)
				continue
			}
		}
		child := r.Clone(r.Context())
		child.Body = io.NopCloser(bytes.NewReader(raw))
		child.ContentLength = int64(len(raw))
		result := &frostBatchEntryWriter{header: make(http.Header)}
		hm.HandleFrostMsg(result, child)
		status := result.status
		if status == 0 {
			status = http.StatusOK
		}
		reply.Statuses = append(reply.Statuses, uint32(status))
	}
	encoded, err := proto.Marshal(reply)
	if err != nil {
		http.Error(w, "Failed to encode FROST batch result", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Write(encoded)
}

                                                                               
type frostBatchEntryWriter struct {
	header http.Header
	status int
}

func (w *frostBatchEntryWriter) Header() http.Header { return w.header }
func (w *frostBatchEntryWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}
func (w *frostBatchEntryWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return len(p), nil
}
