package liveness

import (
	"dex/protocol"
	"fmt"
	"reflect"
	"testing"
)

func TestLocalReporterBuffersOnlyHeaderReports(t *testing.T) {
	tr, sv := newTrackerWithStore(defaultTestCfg())
	alice, err := tr.RegisterMinerAtHeight(sv, "alice", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := tr.RegisterMinerAtHeight(sv, "bob", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	storeKeysBefore := len(sv.data)

	reporter := NewLocalReporter(tr)
	reporter.ReportSampled([]string{"alice", "bob", "unknown", "alice"})
	reporter.ReportOffline([]string{"bob", "unknown", "bob"})

	if len(sv.data) != storeKeysBefore {
		t.Fatalf("local reporter should not write store: before=%d after=%d", storeKeysBefore, len(sv.data))
	}
	if got, want := reporter.GetSampledReports(), []uint32{alice, bob}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sampled reports mismatch: got=%v want=%v", got, want)
	}
	if got, want := reporter.GetOfflineReports(), []uint32{bob}; !reflect.DeepEqual(got, want) {
		t.Fatalf("offline reports mismatch: got=%v want=%v", got, want)
	}
	if got := reporter.GetSampledReports(); len(got) != 0 {
		t.Fatalf("sampled reports should drain once, got=%v", got)
	}
	if got := reporter.GetOfflineReports(); len(got) != 0 {
		t.Fatalf("offline reports should drain once, got=%v", got)
	}
}

func TestLocalReporterRotatesAndRetainsOverflow(t *testing.T) {
	tr, sv := newTrackerWithStore(defaultTestCfg())
	addresses := make([]string, protocol.MaxLivenessReportsPerBlock+50)
	for i := range addresses {
		addresses[i] = fmt.Sprintf("miner-%04d", i)
		if _, err := tr.RegisterMinerAtHeight(sv, addresses[i], 1000, 0); err != nil {
			t.Fatal(err)
		}
	}
	reporter := NewLocalReporter(tr)
	reporter.ReportSampled(addresses)
	first, firstOffline := reporter.GetLivenessReports()
	if len(first) != protocol.MaxLivenessReportsPerBlock || len(firstOffline) != 0 {
		t.Fatalf("first batch sizes=(%d,%d) want=(%d,0)", len(first), len(firstOffline), protocol.MaxLivenessReportsPerBlock)
	}
	if first[0] != 0 || first[len(first)-1] != uint32(protocol.MaxLivenessReportsPerBlock-1) {
		t.Fatalf("unexpected first rotation: first=%d last=%d", first[0], first[len(first)-1])
	}

	                                                                          
	                                                                          
	                                                 
	reporter.ReportSampled(addresses[:protocol.MaxLivenessReportsPerBlock])
	second, _ := reporter.GetLivenessReports()
	if len(second) != protocol.MaxLivenessReportsPerBlock {
		t.Fatalf("second report count=%d want=%d", len(second), protocol.MaxLivenessReportsPerBlock)
	}
	selected := make(map[uint32]struct{}, len(second))
	for _, idx := range second {
		selected[idx] = struct{}{}
	}
	for idx := uint32(protocol.MaxLivenessReportsPerBlock); idx < uint32(len(addresses)); idx++ {
		if _, ok := selected[idx]; !ok {
			t.Fatalf("overflow index %d was starved by new low indexes", idx)
		}
	}
	third, _ := reporter.GetLivenessReports()
	if len(third) != 50 || third[0] != 206 || third[len(third)-1] != 255 {
		t.Fatalf("remaining rotated batch=%v", third)
	}
}

func TestLocalReporterDrainsSampledAndOfflineAtomically(t *testing.T) {
	tr, sv := newTrackerWithStore(defaultTestCfg())
	alice, _ := tr.RegisterMinerAtHeight(sv, "alice", 1000, 0)
	bob, _ := tr.RegisterMinerAtHeight(sv, "bob", 1000, 0)
	reporter := NewLocalReporter(tr)

	                                                                         
	                                                             
	reporter.ReportLiveness([]string{"alice"}, []string{"bob"})
	sampled, offline := reporter.GetLivenessReports()
	if !reflect.DeepEqual(sampled, []uint32{alice, bob}) {
		t.Fatalf("atomic sampled=%v", sampled)
	}
	if !reflect.DeepEqual(offline, []uint32{bob}) {
		t.Fatalf("atomic offline=%v", offline)
	}
	set := make(map[uint32]struct{}, len(sampled))
	for _, idx := range sampled {
		set[idx] = struct{}{}
	}
	for _, idx := range offline {
		if _, ok := set[idx]; !ok {
			t.Fatalf("offline index %d missing from sampled %v", idx, sampled)
		}
	}
}
