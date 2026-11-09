package consensus

import (
	"crypto/sha256"
	"crypto/subtle"
	"dex/interfaces"
	"dex/pb"
	"dex/types"
	"dex/utils"
	"encoding/binary"
	"fmt"
)

const (
	AcceptedFrontierProtocolVersion uint32 = 3
	AcceptedFrontierNonceSize              = 32
	                                                                            
	                                     
	AcceptedFrontierMaxSpan uint64 = 64
)

const (
	acceptedFrontierMaxSignatureSize = 128
)

var (
	acceptedFrontierRequestDomain  = []byte("DEX_ACCEPTED_FRONTIER_REQUEST_V3")
	acceptedFrontierResponseDomain = []byte("DEX_ACCEPTED_FRONTIER_RESPONSE_V3")
)

                                                                              
                                                                              
                                                                       
func ComputeAcceptedFrontierRequestDigest(query AcceptedFrontierQuery) []byte {
	h := sha256.New()
	writeAcceptedFrontierBytes(h, acceptedFrontierRequestDomain)
	writeAcceptedFrontierUint32(h, AcceptedFrontierProtocolVersion)
	writeAcceptedFrontierUint64(h, pb.GetDefaultChainID())
	writeAcceptedFrontierString(h, string(query.Requester))
	writeAcceptedFrontierBytes(h, query.Nonce)
	writeAcceptedFrontierUint64(h, query.AnchorHeight)
	writeAcceptedFrontierString(h, query.AnchorBlockID)
	writeAcceptedFrontierUint64(h, query.TargetHeight)
	return h.Sum(nil)
}

                                                                           
                                                                          
                                                                          
                
func ComputeAcceptedFrontierDigest(query AcceptedFrontierQuery, vote AcceptedFrontierVote) []byte {
	h := sha256.New()
	writeAcceptedFrontierBytes(h, acceptedFrontierResponseDomain)
	writeAcceptedFrontierUint32(h, AcceptedFrontierProtocolVersion)
	writeAcceptedFrontierUint64(h, pb.GetDefaultChainID())
	writeAcceptedFrontierString(h, string(query.Requester))
	writeAcceptedFrontierString(h, string(vote.Peer))
	writeAcceptedFrontierBytes(h, query.Nonce)
	writeAcceptedFrontierUint64(h, query.AnchorHeight)
	writeAcceptedFrontierString(h, query.AnchorBlockID)
	writeAcceptedFrontierString(h, vote.ObservedAnchorID)
	writeAcceptedFrontierUint64(h, vote.TargetHeight)
	writeAcceptedFrontierString(h, vote.TargetBlockID)
	writeAcceptedFrontierUint64(h, vote.AcceptedHeight)
	writeAcceptedFrontierString(h, vote.Status)
	return h.Sum(nil)
}

                                                                                
func NewSignedAcceptedFrontierRequest(query AcceptedFrontierQuery, signer interfaces.NodeSigner) (*pb.AcceptedFrontierRequest, error) {
	if signer == nil {
		return nil, fmt.Errorf("accepted frontier: requester signer is nil")
	}
	if err := validateAcceptedFrontierQuery(query); err != nil {
		return nil, err
	}
	signature, err := signer.Sign(ComputeAcceptedFrontierRequestDigest(query))
	if err != nil {
		return nil, fmt.Errorf("accepted frontier: sign request: %w", err)
	}
	if len(signature) == 0 || len(signature) > acceptedFrontierMaxSignatureSize {
		return nil, fmt.Errorf("accepted frontier: invalid requester signature length %d", len(signature))
	}
	publicKey := append([]byte(nil), signer.PublicKeyBytes()...)
	derivedAddress, err := utils.AddressFromSecp256k1PubKey(publicKey)
	if err != nil || derivedAddress != string(query.Requester) {
		return nil, fmt.Errorf("accepted frontier: requester public key does not match identity")
	}
	return &pb.AcceptedFrontierRequest{
		Version:                AcceptedFrontierProtocolVersion,
		ChainId:                pb.GetDefaultChainID(),
		RequesterId:            string(query.Requester),
		Nonce:                  append([]byte(nil), query.Nonce...),
		AnchorHeight:           query.AnchorHeight,
		AnchorBlockId:          query.AnchorBlockID,
		TargetHeight:           query.TargetHeight,
		Signature:              signature,
		RequesterSecpPublicKey: publicKey,
	}, nil
}

                                                                       
                                                                              
                                                                              
                                                                    
func VerifyAcceptedFrontierRequest(req *pb.AcceptedFrontierRequest) (AcceptedFrontierQuery, error) {
	var query AcceptedFrontierQuery
	if req == nil {
		return query, fmt.Errorf("accepted frontier: nil request")
	}
	if req.Version != AcceptedFrontierProtocolVersion {
		return query, fmt.Errorf("accepted frontier: unsupported version %d", req.Version)
	}
	if req.ChainId != pb.GetDefaultChainID() {
		return query, fmt.Errorf("accepted frontier: chain ID %d does not match %d", req.ChainId, pb.GetDefaultChainID())
	}
	query = AcceptedFrontierQuery{
		Requester:     types.NodeID(req.RequesterId),
		Nonce:         append([]byte(nil), req.Nonce...),
		AnchorHeight:  req.AnchorHeight,
		AnchorBlockID: req.AnchorBlockId,
		TargetHeight:  req.TargetHeight,
	}
	if err := validateAcceptedFrontierQuery(query); err != nil {
		return AcceptedFrontierQuery{}, err
	}
	if len(req.RequesterSecpPublicKey) != 33 {
		return AcceptedFrontierQuery{}, fmt.Errorf("accepted frontier: requester public key must be compressed secp256k1")
	}
	derivedAddress, err := utils.AddressFromSecp256k1PubKey(req.RequesterSecpPublicKey)
	if err != nil || derivedAddress != req.RequesterId {
		return AcceptedFrontierQuery{}, fmt.Errorf("accepted frontier: requester public key does not match identity")
	}
	if len(req.Signature) == 0 || len(req.Signature) > acceptedFrontierMaxSignatureSize || !utils.VerifyECDSASignature(req.RequesterSecpPublicKey, ComputeAcceptedFrontierRequestDigest(query), req.Signature) {
		return AcceptedFrontierQuery{}, fmt.Errorf("accepted frontier: invalid requester signature")
	}
	return query, nil
}

                                                                        
func NewSignedAcceptedFrontierResponse(query AcceptedFrontierQuery, vote AcceptedFrontierVote, signer interfaces.NodeSigner) (*pb.AcceptedFrontierResponse, error) {
	if signer == nil {
		return nil, fmt.Errorf("accepted frontier: responder signer is nil")
	}
	if err := validateAcceptedFrontierVote(query, vote); err != nil {
		return nil, err
	}
	status, err := acceptedFrontierStatusToProto(vote.Status)
	if err != nil {
		return nil, err
	}
	signature, err := signer.Sign(ComputeAcceptedFrontierDigest(query, vote))
	if err != nil {
		return nil, fmt.Errorf("accepted frontier: sign response: %w", err)
	}
	if len(signature) == 0 || len(signature) > acceptedFrontierMaxSignatureSize {
		return nil, fmt.Errorf("accepted frontier: invalid responder signature length %d", len(signature))
	}
	return &pb.AcceptedFrontierResponse{
		Version:          AcceptedFrontierProtocolVersion,
		ChainId:          pb.GetDefaultChainID(),
		RequestDigest:    ComputeAcceptedFrontierRequestDigest(query),
		ResponderId:      string(vote.Peer),
		Status:           status,
		AcceptedHeight:   vote.AcceptedHeight,
		ObservedAnchorId: vote.ObservedAnchorID,
		TargetHeight:     vote.TargetHeight,
		TargetBlockId:    vote.TargetBlockID,
		Signature:        signature,
	}, nil
}

                                                                           
                                                                               
func VerifyAcceptedFrontierResponse(query AcceptedFrontierQuery, expectedPeer types.NodeID, resp *pb.AcceptedFrontierResponse) (AcceptedFrontierVote, error) {
	var vote AcceptedFrontierVote
	if resp == nil {
		return vote, fmt.Errorf("accepted frontier: nil response")
	}
	if resp.Version != AcceptedFrontierProtocolVersion {
		return vote, fmt.Errorf("accepted frontier: unsupported response version %d", resp.Version)
	}
	if resp.ChainId != pb.GetDefaultChainID() {
		return vote, fmt.Errorf("accepted frontier: response chain ID %d does not match %d", resp.ChainId, pb.GetDefaultChainID())
	}
	if resp.ResponderId == "" || types.NodeID(resp.ResponderId) != expectedPeer {
		return vote, fmt.Errorf("accepted frontier: responder %q does not match sampled peer %q", resp.ResponderId, expectedPeer)
	}
	expectedRequestDigest := ComputeAcceptedFrontierRequestDigest(query)
	if len(resp.RequestDigest) != len(expectedRequestDigest) || subtle.ConstantTimeCompare(resp.RequestDigest, expectedRequestDigest) != 1 {
		return vote, fmt.Errorf("accepted frontier: response request digest mismatch")
	}
	status, err := acceptedFrontierStatusFromProto(resp.Status)
	if err != nil {
		return vote, err
	}
	vote = AcceptedFrontierVote{
		Peer:             expectedPeer,
		RequestDigest:    append([]byte(nil), resp.RequestDigest...),
		Status:           status,
		AcceptedHeight:   resp.AcceptedHeight,
		ObservedAnchorID: resp.ObservedAnchorId,
		TargetHeight:     resp.TargetHeight,
		TargetBlockID:    resp.TargetBlockId,
		Signature:        append([]byte(nil), resp.Signature...),
	}
	if err := validateAcceptedFrontierVote(query, vote); err != nil {
		return AcceptedFrontierVote{}, err
	}
	if len(vote.Signature) == 0 || len(vote.Signature) > acceptedFrontierMaxSignatureSize || !VerifyChitSignature(string(expectedPeer), ComputeAcceptedFrontierDigest(query, vote), vote.Signature) {
		return AcceptedFrontierVote{}, fmt.Errorf("accepted frontier: invalid responder signature")
	}
	return vote, nil
}

func validateAcceptedFrontierQuery(query AcceptedFrontierQuery) error {
	if query.Requester == "" || len(query.Requester) > 256 {
		return fmt.Errorf("accepted frontier: invalid requester")
	}
	if len(query.Nonce) != AcceptedFrontierNonceSize {
		return fmt.Errorf("accepted frontier: nonce must be %d bytes", AcceptedFrontierNonceSize)
	}
	if query.AnchorBlockID == "" || len(query.AnchorBlockID) > 512 {
		return fmt.Errorf("accepted frontier: invalid anchor block ID")
	}
	if query.TargetHeight <= query.AnchorHeight {
		return fmt.Errorf("accepted frontier: target height %d must follow anchor %d", query.TargetHeight, query.AnchorHeight)
	}
	if query.TargetHeight-query.AnchorHeight > AcceptedFrontierMaxSpan {
		return fmt.Errorf("accepted frontier: span %d exceeds maximum %d", query.TargetHeight-query.AnchorHeight, AcceptedFrontierMaxSpan)
	}
	return nil
}

func validateAcceptedFrontierVote(query AcceptedFrontierQuery, vote AcceptedFrontierVote) error {
	if vote.Peer == "" || len(vote.Peer) > 256 {
		return fmt.Errorf("accepted frontier: invalid responder")
	}
	if vote.TargetHeight != query.TargetHeight {
		return fmt.Errorf("accepted frontier: response target %d does not match request %d", vote.TargetHeight, query.TargetHeight)
	}
	if len(vote.ObservedAnchorID) > 512 || len(vote.TargetBlockID) > 512 {
		return fmt.Errorf("accepted frontier: block ID exceeds maximum length")
	}
	switch vote.Status {
	case AcceptedFrontierStatusOK:
		if vote.AcceptedHeight < query.TargetHeight || vote.ObservedAnchorID != query.AnchorBlockID || vote.TargetBlockID == "" {
			return fmt.Errorf("accepted frontier: inconsistent ok response")
		}
	case AcceptedFrontierStatusAnchorMismatch:
		if vote.AcceptedHeight != 0 || vote.ObservedAnchorID == "" || vote.ObservedAnchorID == query.AnchorBlockID || vote.TargetBlockID != "" {
			return fmt.Errorf("accepted frontier: inconsistent anchor mismatch response")
		}
	case AcceptedFrontierStatusBelowTarget:
		if vote.AcceptedHeight < query.AnchorHeight || vote.AcceptedHeight >= query.TargetHeight || vote.ObservedAnchorID != query.AnchorBlockID || vote.TargetBlockID != "" {
			return fmt.Errorf("accepted frontier: inconsistent below-target response")
		}
	case AcceptedFrontierStatusUnavailable:
		if vote.AcceptedHeight != 0 || vote.TargetBlockID != "" || (vote.ObservedAnchorID != "" && vote.ObservedAnchorID != query.AnchorBlockID) {
			return fmt.Errorf("accepted frontier: inconsistent unavailable response")
		}
	default:
		return fmt.Errorf("accepted frontier: unknown status %q", vote.Status)
	}
	return nil
}

func acceptedFrontierStatusToProto(status string) (pb.AcceptedFrontierStatus, error) {
	switch status {
	case AcceptedFrontierStatusOK:
		return pb.AcceptedFrontierStatus_ACCEPTED_FRONTIER_STATUS_OK, nil
	case AcceptedFrontierStatusBelowTarget:
		return pb.AcceptedFrontierStatus_ACCEPTED_FRONTIER_STATUS_BELOW_TARGET, nil
	case AcceptedFrontierStatusAnchorMismatch:
		return pb.AcceptedFrontierStatus_ACCEPTED_FRONTIER_STATUS_ANCHOR_MISMATCH, nil
	case AcceptedFrontierStatusUnavailable:
		return pb.AcceptedFrontierStatus_ACCEPTED_FRONTIER_STATUS_UNAVAILABLE, nil
	default:
		return pb.AcceptedFrontierStatus_ACCEPTED_FRONTIER_STATUS_UNSPECIFIED, fmt.Errorf("accepted frontier: unknown status %q", status)
	}
}

func acceptedFrontierStatusFromProto(status pb.AcceptedFrontierStatus) (string, error) {
	switch status {
	case pb.AcceptedFrontierStatus_ACCEPTED_FRONTIER_STATUS_OK:
		return AcceptedFrontierStatusOK, nil
	case pb.AcceptedFrontierStatus_ACCEPTED_FRONTIER_STATUS_BELOW_TARGET:
		return AcceptedFrontierStatusBelowTarget, nil
	case pb.AcceptedFrontierStatus_ACCEPTED_FRONTIER_STATUS_ANCHOR_MISMATCH:
		return AcceptedFrontierStatusAnchorMismatch, nil
	case pb.AcceptedFrontierStatus_ACCEPTED_FRONTIER_STATUS_UNAVAILABLE:
		return AcceptedFrontierStatusUnavailable, nil
	default:
		return "", fmt.Errorf("accepted frontier: unknown protobuf status %d", status)
	}
}

type acceptedFrontierHashWriter interface {
	Write([]byte) (int, error)
}

func writeAcceptedFrontierUint32(h acceptedFrontierHashWriter, value uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], value)
	_, _ = h.Write(buf[:])
}

func writeAcceptedFrontierUint64(h acceptedFrontierHashWriter, value uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	_, _ = h.Write(buf[:])
}

func writeAcceptedFrontierBytes(h acceptedFrontierHashWriter, value []byte) {
	writeAcceptedFrontierUint64(h, uint64(len(value)))
	_, _ = h.Write(value)
}

func writeAcceptedFrontierString(h acceptedFrontierHashWriter, value string) {
	writeAcceptedFrontierBytes(h, []byte(value))
}
