package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"dex/config"
	"dex/consensus"
	"dex/pb"
	"dex/sender"
)

func TestLiveBlockServeRequiresActualRecentCacheAndPrivilegedSource(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Network.AdvertiseHost = "127.0.0.1"
	hm := &HandlerManager{cfg: cfg, senderManager: &sender.SenderManager{}}
	r := httptest.NewRequest(http.MethodPost, "/getblockbyid", nil)
	r.RemoteAddr = "127.0.0.1:19000"
	history, ok := hm.acquireArchiveDataServe(r)
	if !ok {
		t.Fatal("cannot occupy history slot")
	}
	defer history()
	for _, test := range []struct {
		name  string
		block *pb.Block
		want  bool
	}{
		{"cache miss", nil, false},
		{"no header", &pb.Block{}, false},
		{"finalized", &pb.Block{Header: &pb.BlockHeader{Height: 100}}, false},
		{"current candidate", &pb.Block{Header: &pb.BlockHeader{Height: 101}}, true},
		{"oversize cache", &pb.Block{Header: &pb.BlockHeader{Height: 101}, ShortTxs: make([]byte, pb.MaxTotalSize+1)}, false},
		{"future window edge", &pb.Block{Header: &pb.BlockHeader{Height: 100 + consensus.MaxFutureBlockCacheWindow}}, true},
		{"far future", &pb.Block{Header: &pb.BlockHeader{Height: 101 + consensus.MaxFutureBlockCacheWindow}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			release, ok := hm.acquireBlockByIDServe(r, test.block, 100)
			if ok {
				defer release()
			}
			if ok != test.want {
				t.Fatalf("admitted=%v, want %v", ok, test.want)
			}
		})
	}

	ordinary, _, _ := newArchiveServicePeerHandler(t)
	r.RemoteAddr = "1.1.1.1:12345"
	release, ok := ordinary.acquireArchiveDataServe(r)
	if !ok {
		t.Fatal("cannot occupy ordinary source slot")
	}
	defer release()
	if _, ok := ordinary.acquireBlockByIDServe(r, &pb.Block{Header: &pb.BlockHeader{Height: 101}}, 100); ok {
		t.Fatal("ordinary caller borrowed miner live capacity")
	}
	r.Header.Set("X-Node-Address", "self-declared-miner")
	if _, ok := ordinary.acquireBlockByIDServe(r, &pb.Block{Header: &pb.BlockHeader{Height: 101}}, 100); ok {
		t.Fatal("self-declared miner borrowed live capacity")
	}
}
