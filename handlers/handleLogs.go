package handlers

import (
	"dex/logs"
	"dex/pb"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
)

const (
	defaultDebugLogLines      = 200
	maxDebugLogLines          = 1000
	maxDebugLogRequestBytes   = 64 << 10
	maxDebugLogMessageBytes   = 8 << 10
	maxDebugLogTimestampBytes = 64
	maxDebugLogLevelBytes     = 16
	maxDebugLogResponseBytes  = 1 << 20
)

                                      
func (hm *HandlerManager) HandleLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if hm.Stats != nil {
		hm.Stats.RecordAPICall("HandleLogs")
	}

	maxLines := int32(defaultDebugLogLines)
	if r.Method == http.MethodPost {
		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, maxDebugLogRequestBytes+1))
		if err != nil {
			http.Error(w, "failed to read request", http.StatusBadRequest)
			return
		}
		if len(bodyBytes) > maxDebugLogRequestBytes {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		var req pb.LogsRequest
		if err := pb.UnmarshalSanitize(bodyBytes, &req); err != nil {
			http.Error(w, "invalid protobuf request", http.StatusBadRequest)
			return
		}
		if req.MaxLines < 0 {
			http.Error(w, "max_lines must not be negative", http.StatusBadRequest)
			return
		}
		if req.MaxLines > 0 {
			maxLines = req.MaxLines
		}
	}
	if maxLines > maxDebugLogLines {
		maxLines = maxDebugLogLines
	}

	logLines := logs.GetLogsForNode(hm.address)
	data, err := marshalBoundedLogs(logLines, int(maxLines))
	if err != nil {
		http.Error(w, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(data)
}

func marshalBoundedLogs(lines []*pb.LogLine, maxLines int) ([]byte, error) {
	if maxLines <= 0 || maxLines > maxDebugLogLines {
		maxLines = maxDebugLogLines
	}
	start := len(lines) - maxLines
	if start < 0 {
		start = 0
	}

	                                                                           
	                                                                          
	selected := make([]*pb.LogLine, 0, len(lines)-start)
	encodedSize := 0
	for i := len(lines) - 1; i >= start; i-- {
		if lines[i] == nil {
			continue
		}
		line := &pb.LogLine{
			Timestamp: truncateUTF8(lines[i].Timestamp, maxDebugLogTimestampBytes),
			Level:     truncateUTF8(lines[i].Level, maxDebugLogLevelBytes),
			Message:   truncateUTF8(lines[i].Message, maxDebugLogMessageBytes),
		}
		lineSize := proto.Size(line)
		entrySize := 1 + protobufUvarintSize(uint64(lineSize)) + lineSize
		if encodedSize+entrySize > maxDebugLogResponseBytes {
			continue
		}
		selected = append(selected, line)
		encodedSize += entrySize
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return proto.Marshal(&pb.LogsResponse{Logs: selected})
}

func protobufUvarintSize(value uint64) int {
	size := 1
	for value >= 1<<7 {
		value >>= 7
		size++
	}
	return size
}

func truncateUTF8(value string, maxBytes int) string {
	value = strings.ToValidUTF8(value, "")
	if maxBytes <= 0 {
		return ""
	}
	if len(value) <= maxBytes {
		return value
	}
	const suffix = "…"
	if maxBytes < len(suffix) {
		cut := maxBytes
		for cut > 0 && !utf8.RuneStart(value[cut]) {
			cut--
		}
		return value[:cut]
	}
	cut := maxBytes - len(suffix)
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + suffix
}
