package consensus

import (
	"dex/interfaces"
	"dex/types"
	"testing"
	"time"
)

type participationGateTracker struct {
	interfaces.LivenessTracker
	active            bool
	activeOrProbation bool
}

func (t *participationGateTracker) IsActive(string) bool { return t.active }

func (t *participationGateTracker) IsActiveOrProbation(string) bool {
	return t.activeOrProbation
}

                                                                            
                                                                       
                                                                            
                                                                    
func verifiedParticipantTrackerForTest() interfaces.LivenessTracker {
	return &participationGateTracker{active: true, activeOrProbation: true}
}

func TestObserverCannotInitiatePreferencePoll(t *testing.T) {
	qm := &QueryManager{
		nodeID:          types.NodeID("observer"),
		livenessTracker: &participationGateTracker{},
		                                                                         
		                                               
	}
	qm.issueQueryIfReady()
}

func TestPreferencePollFailsClosedWithoutVerifiedLiveness(t *testing.T) {
	qm := &QueryManager{
		nodeID: types.NodeID("unverified-node"),
		                                                                 
		                                                                          
	}
	qm.issueQueryIfReady()
}

func TestObserverCannotSendChit(t *testing.T) {
	handler := &MessageHandler{
		nodeID:          types.NodeID("observer"),
		livenessTracker: &participationGateTracker{},
		                                                                      
		                                                              
	}
	handler.sendChits("requester", 1, 1, nil, 0, nil)
}

func TestChitFailsClosedWithoutVerifiedLiveness(t *testing.T) {
	handler := &MessageHandler{
		nodeID: types.NodeID("unverified-node"),
		                                                                            
		                                                                         
	}
	handler.sendChits("requester", 1, 1, nil, 0, nil)
}

func TestProbationMinerCannotSendConsensusChit(t *testing.T) {
	transport := &captureTransport{}
	handler := &MessageHandler{
		nodeID:          types.NodeID("probation-miner"),
		livenessTracker: &participationGateTracker{active: false, activeOrProbation: true},
		store: &queryManagerSigStore{
			acceptedID:     "genesis",
			acceptedHeight: 0,
			finalized: map[uint64]*types.Block{
				0: {ID: "genesis"},
			},
		},
		engine:    &queryManagerSigEngine{},
		transport: transport,
	}
	handler.sendChits("requester", 1, 0, nil, 0, nil)
	if transport.sent.Type != "" || transport.sentTo != "" {
		t.Fatalf("probation miner sent a consensus Chit: to=%s type=%v", transport.sentTo, transport.sent.Type)
	}
}

func TestProbationMinerCanAnswerReachabilityProbeOnly(t *testing.T) {
	transport := &captureTransport{}
	nonce := queryManagerTestNonce(9)
	handler := &MessageHandler{
		nodeID:          types.NodeID("probation-miner"),
		livenessTracker: &participationGateTracker{active: false, activeOrProbation: true},
		store: &queryManagerSigStore{
			acceptedID:     "genesis",
			acceptedHeight: 0,
		},
		transport: transport,
		signer:    newTestKeyManager(t),
	}
	handler.sendProbationProbeChit("requester", 1, 1, []byte("seed"), 2, nonce)
	if transport.sent.Type != types.MsgChits || transport.sentTo != "requester" || transport.sent.PreferredID != "" || len(transport.sent.ChitSignature) == 0 {
		t.Fatalf("probation probe response invalid: to=%s msg=%+v", transport.sentTo, transport.sent)
	}
}

func TestRealBlockProposerFailsClosedWithoutVerifiedLiveness(t *testing.T) {
	proposer := &RealBlockProposer{}
	if proposer.ShouldPropose(
		"observer",
		0,
		0,
		0,
		1,
		time.Now(),
		"genesis",
	) {
		t.Fatal("proposer without a verified liveness registry was authorized")
	}
}
