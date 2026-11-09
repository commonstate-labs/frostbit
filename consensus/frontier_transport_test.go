package consensus

import (
	"dex/logs"
	"dex/types"
	"testing"
)

func TestSendAcceptedFrontierQueryHonorsSimulatedPacketLoss(t *testing.T) {
	transport := &RealTransport{
		nodeID:         types.NodeID("local"),
		packetLossRate: 1,
		Logger:         logs.NewNodeLogger("frontier-loss-test", 10),
	}
	called := false
	if err := transport.SendAcceptedFrontierQuery("peer", AcceptedFrontierQuery{}, func(AcceptedFrontierVote) {
		called = true
	}); err != nil {
		t.Fatalf("simulated packet drop returned error: %v", err)
	}
	if called {
		t.Fatal("callback ran for a simulated dropped frontier request")
	}
}
