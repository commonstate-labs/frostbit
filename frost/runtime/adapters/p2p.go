                                
                                            
package adapters

import (
	"dex/frost/runtime"
	"dex/pb"
	"dex/types"
	"fmt"

	"google.golang.org/protobuf/proto"
)

                                                    
type Transport interface {
	Send(to types.NodeID, msg types.Message) error
	Broadcast(msg types.Message, peers []types.NodeID)
	SamplePeers(exclude types.NodeID, count int) []types.NodeID
}

                                               
type TransportP2P struct {
	transport Transport
	localID   runtime.NodeID
}

                                            
func NewTransportP2P(transport Transport, localID runtime.NodeID) *TransportP2P {
	return &TransportP2P{
		transport: transport,
		localID:   localID,
	}
}

                                   
func (p *TransportP2P) Send(to runtime.NodeID, msg *runtime.FrostEnvelope) error {
	if p.transport == nil {
		return fmt.Errorf("transport not available")
	}

	                                                 
	transportMsg, err := p.toTransportMessage(msg)
	if err != nil {
		return fmt.Errorf("convert to transport message: %w", err)
	}

	return p.transport.Send(types.NodeID(to), transportMsg)
}

                                        
func (p *TransportP2P) Broadcast(peers []runtime.NodeID, msg *runtime.FrostEnvelope) error {
	if p.transport == nil {
		return fmt.Errorf("transport not available")
	}

	                                                 
	transportMsg, err := p.toTransportMessage(msg)
	if err != nil {
		return fmt.Errorf("convert to transport message: %w", err)
	}

	               
	transportPeers := make([]types.NodeID, len(peers))
	for i, peer := range peers {
		transportPeers[i] = types.NodeID(peer)
	}

	p.transport.Broadcast(transportMsg, transportPeers)
	return nil
}

                           
func (p *TransportP2P) SamplePeers(n int, role string) []runtime.NodeID {
	if p.transport == nil {
		return nil
	}

	transportPeers := p.transport.SamplePeers(types.NodeID(p.localID), n)
	result := make([]runtime.NodeID, len(transportPeers))
	for i, peer := range transportPeers {
		result[i] = runtime.NodeID(peer)
	}
	return result
}

                                                                    
func (p *TransportP2P) toTransportMessage(env *runtime.FrostEnvelope) (types.Message, error) {
	var (
		pbEnv *pb.FrostEnvelope
		err   error
	)

	if isRoastEnvelopeKind(env.Kind) {
		pbEnv, err = runtime.PBEnvelopeFromRoast(&runtime.RoastEnvelope{
			SessionID: env.SessionID,
			Kind:      env.Kind,
			From:      env.From,
			Chain:     env.Chain,
			VaultID:   env.VaultID,
			SignAlgo:  env.SignAlgo,
			Epoch:     env.Epoch,
			Round:     env.Round,
			Payload:   env.Payload,
			Tweaks:    env.Tweaks,
		})
		if err != nil {
			return types.Message{}, fmt.Errorf("encode roast envelope: %w", err)
		}
	} else {
		                          
		pbEnv = &pb.FrostEnvelope{
			From:    string(env.From),
			Kind:    p.toPBEnvelopeKind(env.Kind),
			Payload: env.Payload,
			Sig:     env.Sig,
			JobId:   env.SessionID,
		}
	}

	            
	data, err := proto.Marshal(pbEnv)
	if err != nil {
		return types.Message{}, fmt.Errorf("marshal envelope: %w", err)
	}

	                       
	msg := types.Message{
		Type:         types.MsgFrost,
		From:         types.NodeID(env.From),
		FrostPayload: data,
	}
	return msg, nil
}

                                      
func (p *TransportP2P) toPBEnvelopeKind(kind string) pb.FrostEnvelopeKind {
	switch kind {
	case "NonceCommit", "NonceRequest":
		return pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_REQUEST
	case "SigShare", "SignRequest":
		return pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_RESPONSE
	default:
		return pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_UNSPECIFIED
	}
}

func isRoastEnvelopeKind(kind string) bool {
	switch kind {
	case "NonceRequest", "SignRequest", "NonceCommit", "SigShare":
		return true
	default:
		return false
	}
}

                                             
var _ runtime.P2P = (*TransportP2P)(nil)
