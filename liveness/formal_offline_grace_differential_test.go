package liveness

import (
	"dex/formal/itftrace"
	"dex/keys"
	"dex/protocol"
	"encoding/json"
	"os"
	"testing"
)

                                                                                    
func TestFormalOfflineGraceITFDifferential(t *testing.T) {
	cases := []struct {
		name, env string
		kinds     []int
	}{
		{"unknown-preserves-grace", "DEX_FORMAL_ITF_OFFLINE_GRACE", []int{1, 0, 1}},
		{"positive-resets-synthetic-does-not", "DEX_FORMAL_ITF_OFFLINE_RESET", []int{1, 4, 1, 3, 1}},
	}
	if os.Getenv(cases[0].env) == "" {
		t.Skip("由 formal/formal_liveness_vault_term.ps1 生成独立 Quint ITF")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trace, err := itftrace.Load(os.Getenv(tc.env))
			if err != nil {
				t.Fatal(err)
			}
			if len(trace.States) != len(tc.kinds)+1 {
				t.Fatalf("轨迹截断：states=%d want=%d", len(trace.States), len(tc.kinds)+1)
			}
			cfg := formalOfflineGraceConfig(2)
			tr, store := newTrackerWithStore(cfg)
			if _, err := tr.RegisterMinerAtHeight(store, "victim", 1000, 0); err != nil {
				t.Fatal(err)
			}
			formalAssertOfflineGraceRelation(t, tr, store, trace.States[0])
			for i, kind := range tc.kinds {
				h := uint64(i + 1)
				var sampled, offline, online []uint32
				switch kind {
				case 1:
					sampled, offline = []uint32{0}, []uint32{0}
				case 4:
					                                                                                 
					sampled, online = []uint32{0}, []uint32{0}
				case 3:
					                                                                       
					sampled = []uint32{0}
				}
				_, slashed := tr.ProcessBlockWithOnlineReporter(store, h, "reporter", sampled, offline, online)
				if len(slashed) != 0 {
					t.Fatal("离线宽限不得罚没")
				}
				formalAssertOfflineGraceRelation(t, tr, store, trace.States[i+1])
				                                                                     
				tr = NewTracker(cfg)
				tr.SetStore(store)
				formalAssertOfflineGraceRelation(t, tr, store, trace.States[i+1])
			}
		})
	}
}

func formalOfflineGraceConfig(grace uint64) Config {
	cfg := defaultTestCfg()
	cfg.OfflineGraceBlocks = grace
	cfg.MinSamples, cfg.MinReporters = 1, 1
	cfg.WindowFloor, cfg.WindowCeil = 1, 1
	cfg.Threshold = 0.8
	return cfg
}

func formalAssertOfflineGraceRelation(t *testing.T, tr *Tracker, store *mockStore, expected itftrace.State) {
	t.Helper()
	since, err := expected.Int("since")
	if err != nil {
		t.Fatal(err)
	}
	kicked, err := expected.Bool("kicked")
	if err != nil {
		t.Fatal(err)
	}
	current := tr.GetMinerByAddress("victim")
	if current == nil || current.OfflineSinceHeight != uint64(since) || current.Kicked != kicked {
		t.Fatalf("模型/生产状态不一致：want since=%d kicked=%v got=%+v", since, kicked, current)
	}
	if current.Index != 0 || current.Stake != 1000 || current.TotalSlashed != 0 {
		t.Fatalf("宽限期不应改变身份/本金：%+v", current)
	}
	var persisted MinerInfo
	if err := json.Unmarshal(store.data[keys.KeyLivenessMiner(0)], &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.OfflineSinceHeight != current.OfflineSinceHeight || persisted.Kicked != current.Kicked || persisted.State != current.State {
		t.Fatalf("持久状态与运行态不一致：persisted=%+v current=%+v", persisted, current)
	}
}

func TestFormalOfflineGraceProductionBoundaries(t *testing.T) {
	cfg := formalOfflineGraceConfig(0)                                                                   
	tr, store := newTrackerWithStore(cfg)
	_, err := tr.RegisterMinerAtHeight(store, "victim", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	bad := func(height uint64) {
		t.Helper()
		tr.ProcessBlockWithOnlineReporter(store, height, "reporter", []uint32{0}, []uint32{0}, nil)
	}
	bad(1)
	if m := tr.GetMinerByAddress("victim"); m.OfflineSinceHeight != 1 || m.Kicked {
		t.Fatalf("首次充分离线应启动宽限：%+v", m)
	}
	bad(protocol.OfflineGraceBlocks)
	if m := tr.GetMinerByAddress("victim"); m.Kicked {
		t.Fatalf("G-1 提前踢出：%+v", m)
	}
	bad(protocol.OfflineGraceBlocks + 1)
	if m := tr.GetMinerByAddress("victim"); !m.Kicked || m.Stake != 1000 || m.TotalSlashed != 0 {
		t.Fatalf("完整G且仍有充分证据应踢出、不罚没：%+v", m)
	}
}

func TestFormalOfflineGraceUnknownAndProposerPositive(t *testing.T) {
	for _, positive := range []bool{false, true} {
		cfg := formalOfflineGraceConfig(2)
		tr, store := newTrackerWithStore(cfg)
		if _, err := tr.RegisterMinerAtHeight(store, "victim", 1000, 0); err != nil {
			t.Fatal(err)
		}
		tr.ProcessBlockWithOnlineReporter(store, 1, "reporter", []uint32{0}, []uint32{0}, nil)
		reporter := "reporter"
		if positive {
			reporter = "victim"                                                      
		}
		tr.ProcessBlockWithOnlineReporter(store, 3, reporter, nil, nil, nil)
		m := tr.GetMinerByAddress("victim")
		wantSince := uint64(1)
		if positive {
			wantSince = 0
		}
		if m.Kicked || m.OfflineSinceHeight != wantSince {
			t.Fatalf("过期/正证据判定错误 positive=%v miner=%+v", positive, m)
		}
	}
}
