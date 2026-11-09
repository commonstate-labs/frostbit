package pb

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"dex/protocol"

	"google.golang.org/protobuf/proto"
)

func TestOnlineReportsProtobufRoundTrip(t *testing.T) {
	want := &Block{BlockHash: "online-round-trip", Header: &BlockHeader{
		Height: 7, SampledReports: []uint32{1, 2, 3}, OfflineReports: []uint32{2}, OnlineReports: []uint32{1, 3},
	}}
	field := want.Header.ProtoReflect().Descriptor().Fields().ByName("online_reports")
	if field == nil || field.Number() != 13 {
		t.Fatalf("online_reports must retain wire field 13: %v", field)
	}
	raw, err := proto.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Block
	if err := UnmarshalSanitize(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(&got, want) || !reflect.DeepEqual(got.Header.OnlineReports, []uint32{1, 3}) {
		t.Fatalf("online reports lost during protobuf round trip: %v", &got)
	}
}

func TestLivenessReportsSanitizeProtocolBoundaries(t *testing.T) {
	for _, n := range []int{protocol.MaxLivenessReportsPerBlock - 1, protocol.MaxLivenessReportsPerBlock, protocol.MaxLivenessReportsPerBlock + 1} {
		for _, side := range []string{"sampled", "offline", "online"} {
			t.Run(fmt.Sprintf("%s/%d", side, n), func(t *testing.T) {
				indices := make([]uint32, n)
				for i := range indices {
					indices[i] = uint32(i)
				}
				header := &BlockHeader{}
				switch side {
				case "sampled":
					header.SampledReports = indices
				case "offline":
					header.OfflineReports = indices
				case "online":
					header.OnlineReports = indices
				}
				err := Sanitize(&Block{Header: header})
				if n <= protocol.MaxLivenessReportsPerBlock && err != nil {
					t.Fatalf("bounded list rejected: %v", err)
				}
				if n > protocol.MaxLivenessReportsPerBlock && !errors.Is(err, ErrDataBloat) {
					t.Fatalf("oversized list accepted: %v", err)
				}
			})
		}
	}
}
