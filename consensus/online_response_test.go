package consensus

import (
	"dex/liveness"
	"dex/types"
	"errors"
	"reflect"
	"testing"
)

type responseIndexResolver map[string]uint32

func (r responseIndexResolver) GetMinerIndex(a string) (uint32, bool) { i, ok := r[a]; return i, ok }

func TestAuthenticatedRealResponsesAloneProduceOnlineReports(t *testing.T) {
	for _, mode := range []string{"vote", "abstention", "send-failure-then-real", "wrong-nonce", "unsigned", "invalid-signature", "unsampled", "expired"} {
		t.Run(mode, func(t *testing.T) {
			ClearNodePublicKeys()
			SetNodePublicKeyResolver(nil)
			defer func() { ClearNodePublicKeys(); SetNodePublicKeyResolver(nil) }()
			key := newTestKeyManager(t)
			SetNodePublicKeyResolver(func(node string) ([]byte, bool) {
				if node == "peer" {
					return key.PublicKeyBytes(), true
				}
				return nil, false
			})
			reporter := liveness.NewLocalReporter(responseIndexResolver{"peer": 7})
			engine := &queryManagerSigEngine{}
			qm := &QueryManager{nodeID: "requester", store: &queryManagerSigStore{blocks: map[string]*types.Block{"block-1": {ID: "block-1"}}}, engine: engine, livenessReporter: reporter}
			nonce := queryManagerTestNonce(51)
			poll := &Poll{requestID: 7, queryKey: "query-key", height: 1, vrfSeed: []byte("seed"), seqID: 2, queryNonce: nonce, sampledPeers: []types.NodeID{"peer"}, sampledSet: map[types.NodeID]struct{}{"peer": {}}}
			qm.activePolls.Store(uint32(7), poll)
			msg := types.Message{From: "peer", RequestID: 7, PreferredID: "block-1", PreferredIDHeight: 1, QueryNonce: nonce}
			if mode == "abstention" || mode == "send-failure-then-real" {
				msg.PreferredID = ""
			}
			digest := queryManagerTestChitDigest(7, "requester", "peer", msg.PreferredID, 1, "", 0, nonce)
			signature, err := key.Sign(digest)
			if err != nil {
				t.Fatal(err)
			}
			msg.ChitSignature = signature
			switch mode {
			case "send-failure-then-real":
				qm.handleQuerySendFailure(7, "peer", errors.New("known local failure"))
				sampled, offline, online := reporter.GetLivenessReportsWithOnline()
				if len(sampled)+len(offline)+len(online) != 0 {
					t.Fatal("synthetic abstention created liveness evidence")
				}
			case "wrong-nonce":
				msg.QueryNonce = queryManagerTestNonce(52)
			case "unsigned":
				msg.ChitSignature = nil
			case "invalid-signature":
				msg.ChitSignature = []byte("invalid")
			case "unsampled":
				poll.sampledSet = nil
			case "expired":
				qm.activePolls.Delete(uint32(7))
			}
			qm.HandleChit(msg)
			sampled, offline, online := reporter.GetLivenessReportsWithOnline()
			valid := mode == "vote" || mode == "abstention" || mode == "send-failure-then-real"
			if valid {
				if !reflect.DeepEqual(sampled, []uint32{7}) || len(offline) != 0 || !reflect.DeepEqual(online, []uint32{7}) {
					t.Fatalf("valid response reports sampled=%v offline=%v online=%v", sampled, offline, online)
				}
				if engine.submitted != 1 {
					t.Fatalf("vote slots settled %d times", engine.submitted)
				}
				                                                                                        
				qm.HandleChit(msg)
				sampled, offline, online = reporter.GetLivenessReportsWithOnline()
			}
			if len(sampled)+len(offline)+len(online) != 0 {
				t.Fatalf("rejected/duplicate response reported online: sampled=%v offline=%v online=%v", sampled, offline, online)
			}
		})
	}
}

func TestAuthenticatedProbeReplyReportsOnlineWithoutEnteringConsensus(t *testing.T) {
	ClearNodePublicKeys()
	SetNodePublicKeyResolver(nil)
	defer func() { ClearNodePublicKeys(); SetNodePublicKeyResolver(nil) }()
	key := newTestKeyManager(t)
	SetNodePublicKeyResolver(func(node string) ([]byte, bool) {
		if node == "probation" {
			return key.PublicKeyBytes(), true
		}
		return nil, false
	})
	reporter := liveness.NewLocalReporter(responseIndexResolver{"probation": 9})
	engine := &queryManagerSigEngine{}
	qm := &QueryManager{nodeID: "requester", engine: engine, livenessReporter: reporter}
	nonce := queryManagerTestNonce(61)
	session := &probationProbeSession{requestID: 77, height: 4, vrfSeed: []byte("seed"), seqID: 2, queryNonce: nonce, targets: map[types.NodeID]struct{}{"probation": {}}}
	qm.probeSessions.Store(uint32(77), session)
	signature, err := key.Sign(queryManagerTestChitDigest(77, "requester", "probation", "", 4, "accepted", 3, nonce))
	if err != nil {
		t.Fatal(err)
	}
	msg := types.Message{From: "probation", RequestID: 77, PreferredIDHeight: 4, AcceptedID: "accepted", AcceptedHeight: 3, QueryNonce: nonce, ChitSignature: signature}
	bad := msg
	bad.QueryNonce = queryManagerTestNonce(62)
	qm.HandleChit(bad)
	_, _, online := reporter.GetLivenessReportsWithOnline()
	if len(online) != 0 {
		t.Fatal("wrong nonce probe proved online")
	}
	                                                                           
	                                                                         
	session.failed.Store(types.NodeID("probation"), struct{}{})
	qm.HandleChit(msg)
	sampled, offline, online := reporter.GetLivenessReportsWithOnline()
	if !reflect.DeepEqual(sampled, []uint32{9}) || len(offline) != 0 || !reflect.DeepEqual(online, []uint32{9}) || engine.submitted != 0 {
		t.Fatalf("probe sampled=%v offline=%v online=%v consensus=%d", sampled, offline, online, engine.submitted)
	}
	qm.probeSessions.Delete(uint32(77))
	qm.HandleChit(msg)
	_, _, online = reporter.GetLivenessReportsWithOnline()
	if len(online) != 0 {
		t.Fatal("expired probe revived online evidence")
	}
}

func TestOnlineResponseCannotRevivePollExpiredDuringVerification(t *testing.T) {
	for _, probe := range []bool{false, true} {
		t.Run(map[bool]string{false: "poll", true: "probe"}[probe], func(t *testing.T) {
			ClearNodePublicKeys()
			SetNodePublicKeyResolver(nil)
			defer func() { ClearNodePublicKeys(); SetNodePublicKeyResolver(nil) }()
			key := newTestKeyManager(t)
			reporter := liveness.NewLocalReporter(responseIndexResolver{"peer": 7})
			engine := &queryManagerSigEngine{}
			qm := &QueryManager{nodeID: "requester", engine: engine, livenessReporter: reporter}
			nonce := queryManagerTestNonce(81)
			if probe {
				qm.probeSessions.Store(uint32(7), &probationProbeSession{requestID: 7, height: 1, vrfSeed: []byte("seed"), seqID: 2, queryNonce: nonce, targets: map[types.NodeID]struct{}{"peer": {}}})
			} else {
				qm.activePolls.Store(uint32(7), &Poll{requestID: 7, height: 1, vrfSeed: []byte("seed"), seqID: 2, queryNonce: nonce, sampledSet: map[types.NodeID]struct{}{"peer": {}}})
			}
			SetNodePublicKeyResolver(func(node string) ([]byte, bool) {
				                                                                         
				                                                                         
				if probe {
					qm.probeSessions.Delete(uint32(7))
				} else {
					qm.activePolls.Delete(uint32(7))
				}
				return key.PublicKeyBytes(), node == "peer"
			})
			signature, err := key.Sign(queryManagerTestChitDigest(7, "requester", "peer", "", 1, "", 0, nonce))
			if err != nil {
				t.Fatal(err)
			}
			qm.HandleChit(types.Message{From: "peer", RequestID: 7, PreferredIDHeight: 1, QueryNonce: nonce, ChitSignature: signature})
			sampled, offline, online := reporter.GetLivenessReportsWithOnline()
			if len(sampled)+len(offline)+len(online) != 0 || engine.submitted != 0 {
				t.Fatal("expired session revived after signature verification")
			}
		})
	}
}
