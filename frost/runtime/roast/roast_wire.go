                                    
                                                                          

package roast

import (
	"encoding/json"
	"errors"
	"fmt"

	"dex/pb"
)

var (
	ErrInvalidPBEnvelope    = errors.New("invalid pb.FrostEnvelope")
	ErrEmptyRoastPayload    = errors.New("empty roast payload")
	ErrInvalidRoastKind     = errors.New("invalid roast kind")
	ErrRoastSessionMismatch = errors.New("roast session does not match pb envelope job")
	ErrRoastFromMismatch    = errors.New("roast sender does not match pb envelope sender")
	ErrRoastKindMismatch    = errors.New("roast kind does not match pb envelope kind")
	ErrUnknownRoastEncode   = errors.New("unable to encode roast envelope")
)

                                                                   
func EnvelopeFromPB(env *pb.FrostEnvelope) (*Envelope, error) {
	if env == nil {
		return nil, ErrInvalidPBEnvelope
	}
	if len(env.Payload) == 0 {
		return nil, ErrEmptyRoastPayload
	}

	var msg Envelope
	if err := json.Unmarshal(env.Payload, &msg); err != nil {
		return nil, err
	}

	if msg.SessionID == "" {
		msg.SessionID = env.JobId
	} else if msg.SessionID != env.JobId {
		return nil, fmt.Errorf("%w: inner session_id=%q outer job_id=%q", ErrRoastSessionMismatch, msg.SessionID, env.JobId)
	}
	if msg.From == "" {
		msg.From = NodeID(env.From)
	} else if string(msg.From) != env.From {
		return nil, fmt.Errorf("%w: inner from=%q outer from=%q", ErrRoastFromMismatch, msg.From, env.From)
	}
	if msg.Kind == "" {
		kind := roastKindFromPB(env.Kind)
		if kind == "" {
			return nil, ErrInvalidRoastKind
		}
		msg.Kind = kind
	} else {
		expected := pbKindFromRoast(msg.Kind)
		if expected == pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_UNSPECIFIED {
			return nil, ErrInvalidRoastKind
		}
		if env.Kind != expected {
			return nil, fmt.Errorf("%w: inner kind=%q outer kind=%s", ErrRoastKindMismatch, msg.Kind, env.Kind)
		}
	}

	return &msg, nil
}

                                                                        
func PBEnvelopeFromRoast(msg *Envelope) (*pb.FrostEnvelope, error) {
	if msg == nil {
		return nil, ErrUnknownRoastEncode
	}
	kind := pbKindFromRoast(msg.Kind)
	if kind == pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_UNSPECIFIED {
		return nil, ErrInvalidRoastKind
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	return &pb.FrostEnvelope{
		From:    string(msg.From),
		Kind:    kind,
		Payload: payload,
		JobId:   msg.SessionID,
	}, nil
}

func pbKindFromRoast(kind string) pb.FrostEnvelopeKind {
	switch kind {
	case "NonceRequest", "SignRequest":
		return pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_REQUEST
	case "NonceCommit", "SigShare":
		return pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_RESPONSE
	default:
		return pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_UNSPECIFIED
	}
}

func roastKindFromPB(kind pb.FrostEnvelopeKind) string {
	switch kind {
	case pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_REQUEST:
		return "NonceRequest"
	case pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_RESPONSE:
		return "NonceCommit"
	default:
		return ""
	}
}
