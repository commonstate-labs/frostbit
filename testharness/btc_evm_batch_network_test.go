package testharness

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	frostruntime "dex/frost/runtime"
	"dex/frost/runtime/roast"
	"dex/frost/runtime/types"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

type batchRoastDelivery struct {
	to       types.NodeID
	envelope *types.RoastEnvelope
}

                                                                           
                                                                            
type batchRoastNetwork struct {
	mu            sync.Mutex
	nodes         map[types.NodeID]*frostruntime.Manager
	queue         chan batchRoastDelivery
	gate          chan struct{}
	bothStarted   chan struct{}
	started       map[string]string
	shares        map[string]map[types.NodeID]bool
	sessionChains map[string]string
	sessionShares map[string]map[types.NodeID]bool
	errors        chan error
	wg            sync.WaitGroup
}

func newBatchRoastNetwork() *batchRoastNetwork {
	return &batchRoastNetwork{nodes: make(map[types.NodeID]*frostruntime.Manager), queue: make(chan batchRoastDelivery, 128), gate: make(chan struct{}), bothStarted: make(chan struct{}), started: make(map[string]string), shares: make(map[string]map[types.NodeID]bool), sessionChains: make(map[string]string), sessionShares: make(map[string]map[types.NodeID]bool), errors: make(chan error, 128)}
}
func (n *batchRoastNetwork) Send(to types.NodeID, msg *types.RoastEnvelope) error {
	wire, err := frostruntime.PBEnvelopeFromRoast(msg)
	if err != nil {
		return err
	}
	decoded, err := roast.EnvelopeFromPB(proto.Clone(wire).(*pb.FrostEnvelope))
	if err != nil {
		return err
	}
	copyMsg := &types.RoastEnvelope{SessionID: decoded.SessionID, Kind: decoded.Kind, From: decoded.From, Chain: decoded.Chain, VaultID: decoded.VaultID, SignAlgo: int32(decoded.SignAlgo), Epoch: decoded.Epoch, Round: decoded.Round, Payload: decoded.Payload, Tweaks: decoded.Tweaks}
	n.mu.Lock()
	if msg.Kind == "NonceRequest" {
		n.sessionChains[msg.SessionID] = msg.Chain
		_, existed := n.started[msg.Chain]
		n.started[msg.Chain] = msg.SessionID
		if !existed && len(n.started) == 2 {
			close(n.bothStarted)
		}
	}
	if msg.Kind == "SigShare" {
		if n.sessionShares[msg.SessionID] == nil {
			n.sessionShares[msg.SessionID] = make(map[types.NodeID]bool)
		}
		n.sessionShares[msg.SessionID][msg.From] = true
		if n.shares[msg.Chain] == nil {
			n.shares[msg.Chain] = make(map[types.NodeID]bool)
		}
		n.shares[msg.Chain][msg.From] = true
	}
	n.mu.Unlock()
	select {
	case n.queue <- batchRoastDelivery{to, copyMsg}:
		return nil
	default:
		return fmt.Errorf("ROAST queue overflow")
	}
}
func (n *batchRoastNetwork) Broadcast(peers []types.NodeID, msg *types.RoastEnvelope) error {
	for _, peer := range peers {
		if err := n.Send(peer, msg); err != nil {
			return err
		}
	}
	return nil
}
func (n *batchRoastNetwork) run(ctx context.Context) {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		select {
		case <-n.gate:
		case <-ctx.Done():
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case delivery := <-n.queue:
				env := delivery.envelope
				manager := n.nodes[delivery.to]
				message := &roast.Envelope{
					SessionID: env.SessionID,
					Kind:      env.Kind,
					From:      env.From,
					Chain:     env.Chain,
					VaultID:   env.VaultID,
					SignAlgo:  pb.SignAlgo(env.SignAlgo),
					Epoch:     env.Epoch,
					Round:     env.Round,
					Payload:   env.Payload,
					Tweaks:    env.Tweaks,
				}
				err := manager.HandleRoastEnvelope(message)
				// Model the production sender's retry when a share arrives before the coordinator is ready.
				for retry := 0; retry < 20 && env.Kind == "SigShare" && errors.Is(err, roast.ErrRoastStateNotReady); retry++ {
					select {
					case <-time.After(10 * time.Millisecond):
					case <-ctx.Done():
						return
					}
					err = manager.HandleRoastEnvelope(message)
				}
				if err != nil {
					select {
					case n.errors <- fmt.Errorf("%s %s %s: %w", env.Chain, env.Kind, delivery.to, err):
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
}

type batchVaultProvider struct {
	committee []types.SignerInfo
	pubkeys   map[string][]byte
}

func (p *batchVaultProvider) VaultCommittee(string, uint32, uint64) ([]types.SignerInfo, error) {
	return append([]types.SignerInfo(nil), p.committee...), nil
}
func (p *batchVaultProvider) VaultCurrentEpoch(string, uint32) uint64 { return 1 }
func (p *batchVaultProvider) VaultGroupPubkey(chain string, _ uint32, _ uint64) ([]byte, error) {
	return append([]byte(nil), p.pubkeys[chain]...), nil
}
func (p *batchVaultProvider) CalculateThreshold(string, uint32) (int, error) { return 3, nil }
