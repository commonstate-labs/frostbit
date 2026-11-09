package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func TestPublicSummaryIgnoresIncludeFrostAndNeverFetchesDiagnostics(t *testing.T) {
	var mu sync.Mutex
	paths := make([]string, 0, 2)
	client := &http.Client{Transport: explorerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		paths = append(paths, req.URL.Path)
		mu.Unlock()
		var message proto.Message
		switch req.URL.Path {
		case "/status":
			message = &pb.StatusResponse{Status: "running"}
		case "/heightquery":
			message = &pb.HeightResponse{CurrentHeight: 10, LastAcceptedHeight: 9}
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("not found")),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}
		body, _ := proto.Marshal(message)
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: int64(len(body)),
			Body:          io.NopCloser(bytes.NewReader(body)),
			Header:        http.Header{"Content-Type": []string{"application/x-protobuf"}},
			Request:       req,
		}, nil
	})}
	srv := &server{
		gateway:      newNodeGateway(client, []string{"node-a.example:6000"}, 2),
		timeout:      time.Second,
		defaultNodes: []string{"node-a.example:6000"},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/summary?nodes=node-a.example%3A6000&include_frost=true", nil)
	srv.handleSummary(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, sensitive := range []string{"frost_metrics", "resources", "block_timing", "logs", "is_paused"} {
		if strings.Contains(recorder.Body.String(), `"`+sensitive+`"`) {
			t.Fatalf("public summary leaked %q: %s", sensitive, recorder.Body.String())
		}
	}
	hubPayload := (&wsHub{srv: srv}).fetchSummaryData(context.Background(), []byte(`{"nodes":["node-a.example:6000"],"include_frost":true}`))
	for _, sensitive := range []string{"frost_metrics", "resources", "block_timing", "logs", "is_paused"} {
		if strings.Contains(string(hubPayload.data), `"`+sensitive+`"`) {
			t.Fatalf("public WebSocket summary leaked %q: %s", sensitive, hubPayload.data)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 4 {
		t.Fatalf("public REST/WS summary paths=%v, want status and height only for each request", paths)
	}
	for _, path := range paths {
		switch path {
		case "/status", "/heightquery":
		default:
			t.Fatalf("public summary fetched diagnostic path %q", path)
		}
	}
}

func TestBoundedExplorerLocalLogsMatchesNodeWireLimits(t *testing.T) {
	lines := make([]*pb.LogLine, 0, 1200)
	for index := 0; index < 1200; index++ {
		lines = append(lines, &pb.LogLine{
			Timestamp: strings.Repeat("t", 100),
			Level:     strings.Repeat("L", 30),
			Message:   strings.Repeat("界", 4000) + string([]byte{0xff}),
		})
	}
	bounded := boundedExplorerLocalLogs(lines)
	if len(bounded) == 0 || len(bounded) > explorerLocalLogMaxLines {
		t.Fatalf("bounded local log count=%d", len(bounded))
	}
	response := &pb.LogsResponse{Logs: bounded}
	if size := proto.Size(response); size > explorerLocalLogMaxResponse {
		t.Fatalf("bounded local log response=%d bytes, max=%d", size, explorerLocalLogMaxResponse)
	}
	for _, line := range bounded {
		if len(line.Timestamp) > explorerLocalLogMaxTimestamp || len(line.Level) > explorerLocalLogMaxLevel || len(line.Message) > explorerLocalLogMaxMessage || !utf8.ValidString(line.Message) {
			t.Fatalf("unbounded or invalid line: timestamp=%d level=%d message=%d valid=%v", len(line.Timestamp), len(line.Level), len(line.Message), utf8.ValidString(line.Message))
		}
	}
}
