package consensus

import (
	"context"
	"dex/interfaces"
	"dex/types"
	"fmt"
	"math/rand"
	"time"
)

type SimulatedTransport struct {
	nodeID         types.NodeID
	ctrlInbox      chan types.Message
	dataInbox      chan types.Message
	immInbox       chan types.Message
	network        *NetworkManager
	ctx            context.Context
	networkLatency time.Duration
	packetLossRate float64             
}

func NewSimulatedTransport(nodeID types.NodeID, network *NetworkManager, ctx context.Context, latency time.Duration) interfaces.Transport {
	return &SimulatedTransport{
		nodeID:         nodeID,
		ctrlInbox:      make(chan types.Message, 20000),
		dataInbox:      make(chan types.Message, 5000),
		immInbox:       make(chan types.Message, 1000),                
		network:        network,
		ctx:            ctx,
		networkLatency: latency,
		packetLossRate: network.config.Network.PacketLossRate,                   
	}
}

func (t *SimulatedTransport) Send(to types.NodeID, msg types.Message) error {
	                     
	if rand.Float64() < t.packetLossRate {
		                                             
		                                       
		                                                                                   
		return nil                                                               
	}
	go func() {
		delay := t.networkLatency
		delay += time.Duration(rand.Intn(int(delay / 2)))
		time.Sleep(delay)

		receiver, ok := t.network.GetTransport(to).(*SimulatedTransport)
		if !ok || receiver == nil {
			return
		}

		targetInbox := receiver.ctrlInbox
		if msg.Type == types.MsgGet {
			targetInbox = receiver.immInbox                          
		} else if msg.Type == types.MsgPut {
			targetInbox = receiver.dataInbox                          
		}

		select {
		case targetInbox <- msg:
		case <-time.After(100 * time.Millisecond):
		case <-t.ctx.Done():
		}
	}()
	return nil
}

func (t *SimulatedTransport) Receive() <-chan types.Message {
	return t.ctrlInbox
}

func (t *SimulatedTransport) ReceiveData() <-chan types.Message {
	return t.dataInbox
}

func (t *SimulatedTransport) ReceiveImmediate() <-chan types.Message {
	return t.immInbox
}

func (t *SimulatedTransport) Broadcast(msg types.Message, peers []types.NodeID) {
	for _, peer := range peers {
		t.Send(peer, msg)
	}
}

func (t *SimulatedTransport) SamplePeers(exclude types.NodeID, count int) []types.NodeID {
	return t.network.SamplePeers(exclude, count)
}

func (t *SimulatedTransport) GetAllPeers(exclude types.NodeID) []types.NodeID {
	return t.network.GetAllPeers(exclude)
}

                                                                              
                                                                               
                                                              
func (t *SimulatedTransport) SendAcceptedFrontierQuery(peer types.NodeID, query AcceptedFrontierQuery, callback func(AcceptedFrontierVote)) error {
	if t == nil || t.network == nil {
		return fmt.Errorf("accepted frontier: simulated network is unavailable")
	}
	t.network.mu.RLock()
	requester := t.network.nodes[t.nodeID]
	responder := t.network.nodes[peer]
	t.network.mu.RUnlock()
	if requester == nil || requester.messageHandler == nil || requester.messageHandler.signer == nil {
		return fmt.Errorf("accepted frontier: simulated requester %s has no signer", t.nodeID)
	}
	if responder == nil || responder.messageHandler == nil || responder.messageHandler.signer == nil {
		return fmt.Errorf("accepted frontier: simulated responder %s is unavailable", peer)
	}
	if query.Requester != t.nodeID {
		return fmt.Errorf("accepted frontier: requester %s does not match simulated node %s", query.Requester, t.nodeID)
	}
	req, err := NewSignedAcceptedFrontierRequest(query, requester.messageHandler.signer)
	if err != nil {
		return err
	}
	verifiedQuery, err := VerifyAcceptedFrontierRequest(req)
	if err != nil {
		return err
	}
	if rand.Float64() < t.packetLossRate {
		return nil
	}

	go func() {
		delay := t.networkLatency
		if jitter := delay / 2; jitter > 0 {
			delay += time.Duration(rand.Int63n(int64(jitter)))
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		var done <-chan struct{}
		if t.ctx != nil {
			done = t.ctx.Done()
		}
		select {
		case <-timer.C:
		case <-done:
			return
		}
		if responder.IsByzantine {
			return
		}

		_, acceptedHeight := responder.store.GetLastAccepted()
		vote := AcceptedFrontierVote{
			Peer:         peer,
			Status:       AcceptedFrontierStatusUnavailable,
			TargetHeight: verifiedQuery.TargetHeight,
		}
		if anchor, ok := responder.store.GetFinalizedAtHeight(verifiedQuery.AnchorHeight); ok && anchor != nil {
			vote.ObservedAnchorID = anchor.ID
			switch {
			case anchor.ID != verifiedQuery.AnchorBlockID:
				vote.Status = AcceptedFrontierStatusAnchorMismatch
			case acceptedHeight < verifiedQuery.TargetHeight:
				vote.Status = AcceptedFrontierStatusBelowTarget
				vote.AcceptedHeight = acceptedHeight
			default:
				if target, exists := responder.store.GetFinalizedAtHeight(verifiedQuery.TargetHeight); exists && target != nil {
					vote.Status = AcceptedFrontierStatusOK
					vote.AcceptedHeight = acceptedHeight
					vote.TargetBlockID = target.ID
				}
			}
		}
		resp, signErr := NewSignedAcceptedFrontierResponse(verifiedQuery, vote, responder.messageHandler.signer)
		if signErr != nil {
			return
		}
		verifiedVote, verifyErr := VerifyAcceptedFrontierResponse(verifiedQuery, peer, resp)
		if verifyErr == nil && callback != nil {
			callback(verifiedVote)
		}
	}()
	return nil
}
