                                          
                                                                             

package roast

import (
	"errors"
)

var (
	ErrInvalidRoastMessage = errors.New("invalid roast message")
	ErrUnknownRoastKind    = errors.New("unknown roast message kind")
)

                                                                           
type Dispatcher struct {
	coordinator *Coordinator
	participant *Participant
}

                                                                       
func NewDispatcher(coordinator *Coordinator, participant *Participant) *Dispatcher {
	return &Dispatcher{
		coordinator: coordinator,
		participant: participant,
	}
}

                                                            
func (d *Dispatcher) Handle(msg *Envelope) error {
	if msg == nil {
		return ErrInvalidRoastMessage
	}

	switch msg.Kind {
	case "NonceRequest":
		if d.participant == nil {
			return errors.New("participant not available")
		}
		return d.participant.HandleRoastNonceRequest(msg)
	case "SignRequest":
		if d.participant == nil {
			return errors.New("participant not available")
		}
		return d.participant.HandleRoastSignRequest(msg)
	case "NonceCommit":
		if d.coordinator == nil {
			return errors.New("coordinator not available")
		}
		return d.coordinator.HandleRoastNonceCommit(msg)
	case "SigShare":
		if d.coordinator == nil {
			return errors.New("coordinator not available")
		}
		return d.coordinator.HandleRoastSigShare(msg)
	default:
		return ErrUnknownRoastKind
	}
}

                                                                     
func (d *Dispatcher) HandleFrostEnvelope(env *FrostEnvelope) error {
	return d.Handle(FromFrostEnvelope(env))
}
