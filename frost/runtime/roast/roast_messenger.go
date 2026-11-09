                                         
                                                                                 

package roast

import (
	"dex/frost/runtime/types"
	"dex/pb"
)

                                                    
type Envelope struct {
	SessionID string
	Kind      string
	From      NodeID
	Chain     string
	VaultID   uint32
	SignAlgo  pb.SignAlgo
	Epoch     uint64
	Round     uint32
	Payload   []byte
	Tweaks    [][]byte                                               
}

                                     
type P2P interface {
	Send(to NodeID, msg *FrostEnvelope) error
	Broadcast(peers []NodeID, msg *FrostEnvelope) error
	SamplePeers(n int, role string) []NodeID
}

                                                                        
type FrostEnvelope struct {
	SessionID string
	Kind      string
	From      NodeID
	Chain     string
	VaultID   uint32
	SignAlgo  int32
	Epoch     uint64
	Round     uint32
	Payload   []byte
	Sig       []byte
	Tweaks    [][]byte
}

                                                                    
type P2PMessenger struct {
	p2p P2P
}

                                                                   
func NewP2PMessenger(p2p P2P) *P2PMessenger {
	return &P2PMessenger{p2p: p2p}
}

                                               
func (m *P2PMessenger) Send(to NodeID, msg *Envelope) error {
	if m == nil || m.p2p == nil || msg == nil {
		return nil
	}
	return m.p2p.Send(to, toFrostEnvelope(msg))
}

                                                     
func (m *P2PMessenger) Broadcast(peers []NodeID, msg *Envelope) error {
	if m == nil || m.p2p == nil || msg == nil {
		return nil
	}
	return m.p2p.Broadcast(peers, toFrostEnvelope(msg))
}

                                                               
func FromFrostEnvelope(env *FrostEnvelope) *Envelope {
	if env == nil {
		return nil
	}
	return &Envelope{
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
}

func toFrostEnvelope(msg *Envelope) *FrostEnvelope {
	if msg == nil {
		return nil
	}
	return &FrostEnvelope{
		SessionID: msg.SessionID,
		Kind:      msg.Kind,
		From:      msg.From,
		Chain:     msg.Chain,
		VaultID:   msg.VaultID,
		SignAlgo:  int32(msg.SignAlgo),
		Epoch:     msg.Epoch,
		Round:     msg.Round,
		Payload:   msg.Payload,
		Tweaks:    msg.Tweaks,
	}
}

                                                                        
func toTypesRoastEnvelope(msg *Envelope) *types.RoastEnvelope {
	if msg == nil {
		return nil
	}
	return &types.RoastEnvelope{
		SessionID: msg.SessionID,
		Kind:      msg.Kind,
		From:      msg.From,
		Chain:     msg.Chain,
		VaultID:   msg.VaultID,
		SignAlgo:  int32(msg.SignAlgo),
		Epoch:     msg.Epoch,
		Round:     msg.Round,
		Payload:   msg.Payload,
		Tweaks:    msg.Tweaks,
	}
}
