                             
                               

package handlers

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"dex/consensus"
	"dex/frost/runtime/roast"
	roastsession "dex/frost/runtime/session"
	"dex/frost/security"
	"dex/pb"
	"dex/types"

	"github.com/btcsuite/btcd/btcec/v2"
	"google.golang.org/protobuf/proto"
)

const (
	frostSeqReplayWindow      = uint64(1000)
	frostSeqJumpWarnThreshold = 4 * security.FrostSequenceReservationSize
)

                                         
type FrostMsgVerifier struct {
	                                                
	GetPublicKey func(address string) (*ecdsa.PublicKey, error)
	                           
	ReplayGuard *security.SeqReplayGuard
	                                                                
	EnableSigVerify bool
	                                                             
	EnableReplayGuard bool
}

var (
	                               
	frostMsgVerifier     *FrostMsgVerifier
	frostMsgVerifierOnce sync.Once
)

                                                            
func GetFrostMsgVerifier() *FrostMsgVerifier {
	frostMsgVerifierOnce.Do(func() {
		frostMsgVerifier = &FrostMsgVerifier{
			GetPublicKey:      defaultFrostMsgPublicKey,
			ReplayGuard:       security.NewSeqReplayGuard(frostSeqReplayWindow),
			EnableSigVerify:   true,
			EnableReplayGuard: true,
		}
	})
	return frostMsgVerifier
}

                                                                     
func SetFrostMsgVerifier(v *FrostMsgVerifier) {
	frostMsgVerifier = v
}

                                         
                 
func (hm *HandlerManager) HandleFrostMsg(w http.ResponseWriter, r *http.Request) {
	hm.Stats.RecordAPICall("HandleFrostMsg")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}

	                                
	var envelope pb.FrostEnvelope
	if err := pb.UnmarshalSanitize(bodyBytes, &envelope); err != nil {
		http.Error(w, "Invalid FrostEnvelope proto", http.StatusBadRequest)
		return
	}

	                        
	senderAddress := envelope.From
	if senderAddress == "" {
		http.Error(w, "Missing sender address", http.StatusBadRequest)
		return
	}
	                                                                           
	                                                                            
	                                                                           
	if hm.address != "" && envelope.To != hm.address {
		hm.Logger.Warn("[HandleFrostMsg] Message addressed to %q received by %q from %s", envelope.To, hm.address, senderAddress)
		http.Error(w, "Message addressed to another node", http.StatusBadRequest)
		return
	}

	                  
	verifier := GetFrostMsgVerifier()

	                  
	if verifier.EnableSigVerify {
		if len(envelope.Sig) == 0 {
			hm.Logger.Warn("[HandleFrostMsg] Missing signature from %s", senderAddress)
			http.Error(w, "Missing signature", http.StatusUnauthorized)
			return
		}

		if verifier.GetPublicKey == nil {
			hm.Logger.Warn("[HandleFrostMsg] No public key lookup function configured")
			http.Error(w, "Signature verification not configured", http.StatusInternalServerError)
			return
		}

		pubKey, err := verifier.GetPublicKey(senderAddress)
		if err != nil {
			hm.Logger.Warn("[HandleFrostMsg] Failed to get public key for %s: %v", senderAddress, err)
			http.Error(w, "Unknown sender", http.StatusUnauthorized)
			return
		}

		if !security.VerifyFrostEnvelope(pubKey, &envelope) {
			hm.Logger.Warn("[HandleFrostMsg] Invalid signature from %s", senderAddress)
			http.Error(w, "Invalid signature", http.StatusUnauthorized)
			return
		}
	}

	                     
	var replayGuard *security.SeqReplayGuard
	var replayReservation *security.SeqReplayReservation
	replayCommittedRequest := false
	if verifier.EnableReplayGuard && verifier.ReplayGuard != nil {
		replayKey := frostReplayKey(senderAddress, envelope.To, hm.address)
		digest, err := frostEnvelopeReplayDigest(&envelope)
		if err != nil {
			hm.Logger.Warn("[HandleFrostMsg] Failed to build replay digest from %s: %v", senderAddress, err)
			http.Error(w, "Failed to validate message", http.StatusInternalServerError)
			return
		}
		reservation, decision, previousMax := verifier.ReplayGuard.Reserve(replayKey, envelope.Seq, digest)
		switch decision {
		case security.SeqReplayAccepted:
			replayGuard = verifier.ReplayGuard
			replayReservation = reservation
		case security.SeqReplayInFlight:
			hm.Logger.Debug("[HandleFrostMsg] Duplicate request still in flight from %s, seq=%d", senderAddress, envelope.Seq)
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Message still processing", http.StatusServiceUnavailable)
			return
		case security.SeqReplayAlreadyCommitted:
			if envelope.Kind != pb.FrostEnvelopeKind_FROST_ENVELOPE_KIND_ROAST_REQUEST {
				hm.Logger.Debug("[HandleFrostMsg] Idempotent response replay acknowledged from %s, seq=%d", senderAddress, envelope.Seq)
				w.WriteHeader(http.StatusOK)
				return
			}
			                                                                     
			                                                                      
			                                                                   
			replayCommittedRequest = true
			hm.Logger.Debug("[HandleFrostMsg] Replaying committed request for cached response from %s, seq=%d", senderAddress, envelope.Seq)
		case security.SeqReplayPermanentlyRejected:
			hm.Logger.Debug("[HandleFrostMsg] Permanently rejected duplicate from %s, seq=%d", senderAddress, envelope.Seq)
			http.Error(w, "Message permanently rejected", http.StatusUnprocessableEntity)
			return
		case security.SeqReplayConflict:
			hm.Logger.Warn("[HandleFrostMsg] Conflicting payload for sequence from %s, seq=%d", senderAddress, envelope.Seq)
			http.Error(w, "Sequence payload conflict", http.StatusConflict)
			return
		case security.SeqReplayStale:
			hm.Logger.Warn("[HandleFrostMsg] Stale or invalid sequence from %s, seq=%d", senderAddress, envelope.Seq)
			http.Error(w, "Replay detected", http.StatusConflict)
			return
		case security.SeqReplayBusy:
			hm.Logger.Warn("[HandleFrostMsg] Too many pending messages from %s", senderAddress)
			w.Header().Set("Retry-After", "1")
			http.Error(w, "Too many messages still processing", http.StatusServiceUnavailable)
			return
		default:
			hm.Logger.Warn("[HandleFrostMsg] Unknown replay decision from %s, seq=%d", senderAddress, envelope.Seq)
			http.Error(w, "Failed to validate message", http.StatusInternalServerError)
			return
		}
		if envelope.Seq > previousMax && envelope.Seq-previousMax > frostSeqJumpWarnThreshold {
			hm.Logger.Warn(
				"[HandleFrostMsg] Large seq jump accepted from %s, previous=%d, seq=%d",
				senderAddress,
				previousMax,
				envelope.Seq,
			)
		}
	}

	                                 
	msg := types.Message{
		Type:         types.MsgFrost,
		From:         types.NodeID(senderAddress),
		FrostPayload: bodyBytes,
	}

	                                               
	if hm.frostMsgHandler != nil {
		if err := hm.frostMsgHandler(msg); err != nil {
			retryable := isRetryableFrostProcessingError(err)
			if replayCommittedRequest {
				                                                             
				                                                                      
				                                                                     
				hm.Logger.Warn("[HandleFrostMsg] Failed to replay cached frost response: %v", err)
				if retryable {
					w.Header().Set("Retry-After", "1")
					http.Error(w, "Failed to replay response", http.StatusServiceUnavailable)
				} else {
					w.WriteHeader(http.StatusOK)
				}
				return
			}
			hm.Logger.Warn("[HandleFrostMsg] Failed to handle frost message: %v", err)
			if retryable {
				if replayGuard != nil && replayReservation != nil {
					replayGuard.Release(replayReservation)
				}
				w.Header().Set("Retry-After", "1")
				http.Error(w, "Failed to process message", http.StatusServiceUnavailable)
				return
			}
			if replayGuard != nil && replayReservation != nil && !replayGuard.Reject(replayReservation) {
				hm.Logger.Error("[HandleFrostMsg] Replay reservation disappeared after permanent rejection from %s, seq=%d", senderAddress, envelope.Seq)
			}
			http.Error(w, "Invalid frost message", http.StatusUnprocessableEntity)
			return
		}
	}

	if replayGuard != nil && replayReservation != nil && !replayGuard.Commit(replayReservation) {
		                                                                          
		                                                                       
		                                                                      
		hm.Logger.Error("[HandleFrostMsg] Replay reservation disappeared after successful handling from %s, seq=%d", senderAddress, envelope.Seq)
	}

	w.WriteHeader(http.StatusOK)
}

func isRetryableFrostProcessingError(err error) bool {
	return errors.Is(err, roast.ErrSessionNotFound) ||
		errors.Is(err, roast.ErrFutureRoastRound) ||
		errors.Is(err, roast.ErrRoastStateNotReady) ||
		errors.Is(err, roast.ErrLocalShareNotReady) ||
		errors.Is(err, roast.ErrRoastResponseSendFailed) ||
		errors.Is(err, roast.ErrSigningIntentNotReady) ||
		errors.Is(err, roastsession.ErrSessionNotFound)
}

func frostEnvelopeReplayDigest(envelope *pb.FrostEnvelope) ([sha256.Size]byte, error) {
	if envelope == nil {
		return [sha256.Size]byte{}, fmt.Errorf("nil FrostEnvelope")
	}
	canonical := proto.Clone(envelope).(*pb.FrostEnvelope)
	canonical.Sig = nil
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(canonical)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(data), nil
}

func defaultFrostMsgPublicKey(address string) (*ecdsa.PublicKey, error) {
	pubBytes, ok := consensus.ResolveNodePublicKey(address)
	if !ok || len(pubBytes) == 0 {
		return nil, fmt.Errorf("public key not found")
	}
	pubKey, err := btcec.ParsePubKey(pubBytes)
	if err != nil {
		return nil, err
	}
	return pubKey.ToECDSA(), nil
}

func frostReplayKey(sender, to, local string) string {
	if to != "" {
		return sender + "->" + to
	}
	if local != "" {
		return sender + "->" + local
	}
	return sender
}
