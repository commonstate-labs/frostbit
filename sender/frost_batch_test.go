package sender

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dex/pb"
	"google.golang.org/protobuf/proto"
)

func TestFrostBatchRetainsEnvelopesAndReportsIndependentStatuses(t *testing.T) {
	batch := &pb.FrostEnvelopeBatch{Envelopes: [][]byte{[]byte("signed-A"), []byte("signed-B")}}
	body, _ := proto.Marshal(batch)
	for _, tc := range []struct {
		statuses []uint32
		want     int
	}{{[]uint32{200, 200}, 0}, {[]uint32{200, 503}, 503}, {[]uint32{422, 200}, 422}, {[]uint32{403, 503}, 503}} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/frostmsgbatch" {
				t.Errorf("wrong route %s", r.URL.Path)
			}
			got, _ := io.ReadAll(r.Body)
			if !bytes.Equal(got, body) {
				t.Error("transport modified original signed envelopes")
			}
			reply, _ := proto.Marshal(&pb.FrostEnvelopeBatchReply{Statuses: tc.statuses})
			w.Write(reply)
		}))
		err := doSendFrostBatch(&SendTask{Target: strings.TrimPrefix(server.URL, "https://"), Message: body}, server.Client())
		server.Close()
		if tc.want == 0 {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var status *httpStatusError
		if !errors.As(err, &status) || status.statusCode != tc.want {
			t.Fatalf("err=%v want %d", err, tc.want)
		}
	}
}
