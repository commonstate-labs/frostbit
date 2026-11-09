                                
                                                 

package roast

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"sync"
	"time"

	"dex/frost/core/curve"
	"dex/frost/core/dkg"
	"dex/frost/core/roast"
	roastsession "dex/frost/runtime/session"
	"dex/logs"
	"dex/pb"
)

                                     

var (
	                                 
	ErrSigningTimeout = errors.New("signing timeout")
	                                
	ErrSigningFailed = errors.New("signing failed")
	                                
	ErrNoShare = errors.New("no share available")
)

                                      

                             
type SignerService struct {
	mu sync.RWMutex

	nodeID      NodeID
	coordinator *Coordinator
	participant *Participant

	                     
	shares map[string]*big.Int                                  

	                     
	pubShares map[string]curve.Point                                                  

	                  
	groupPubKeys map[string]curve.Point                                        

	         
	signTimeout time.Duration
	group       curve.Group
}

                                      
func NewSignerService(nodeID NodeID, coordinator *Coordinator, participant *Participant) *SignerService {
	return &SignerService{
		nodeID:       nodeID,
		coordinator:  coordinator,
		participant:  participant,
		shares:       make(map[string]*big.Int),
		pubShares:    make(map[string]curve.Point),
		groupPubKeys: make(map[string]curve.Point),
		signTimeout:  30 * time.Second,
		group:        curve.NewSecp256k1Group(),
	}
}

                                    
func (s *SignerService) SetShare(chain string, vaultID uint32, epoch uint64, share *big.Int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := shareKeyStr(chain, vaultID, epoch)
	s.shares[key] = share

	                        
	if s.participant != nil {
		s.participant.SetShare(chain, vaultID, epoch, share.Bytes())
	}
}

                                       
func (s *SignerService) SetPublicKeyShare(chain string, vaultID uint32, signerID int, epoch uint64, pubShare curve.Point) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := pubShareKeyStr(chain, vaultID, signerID, epoch)
	s.pubShares[key] = pubShare
}

                                    
func (s *SignerService) SetGroupPublicKey(chain string, vaultID uint32, epoch uint64, groupPub curve.Point) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := shareKeyStr(chain, vaultID, epoch)
	s.groupPubKeys[key] = groupPub
}

                                    
func (s *SignerService) GetShare(chain string, vaultID uint32, epoch uint64) *big.Int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := shareKeyStr(chain, vaultID, epoch)
	return s.shares[key]
}

                               
func shareKeyStr(chain string, vaultID uint32, epoch uint64) string {
	return chain + "_" + itoa(int(vaultID)) + "_" + itoa(int(epoch))
}

                                     
func pubShareKeyStr(chain string, vaultID uint32, signerID int, epoch uint64) string {
	return chain + "_" + itoa(int(vaultID)) + "_" + itoa(signerID) + "_" + itoa(int(epoch))
}

func itoa(i int) string {
	return string(rune('0' + i%10))
}

                          
type SignParams struct {
	JobID     string
	Chain     string
	VaultID   uint32
	KeyEpoch  uint64
	SignAlgo  pb.SignAlgo
	Messages  [][]byte                                         
	Threshold int
}

                          
type SignResult struct {
	JobID      string
	Signatures [][]byte                                                       
	Success    bool
	Error      error
}

                                            
func (s *SignerService) Sign(ctx context.Context, params *SignParams) (*SignResult, error) {
	                           
	share := s.GetShare(params.Chain, params.VaultID, params.KeyEpoch)
	if share == nil {
		return nil, ErrNoShare
	}

	                        
	startParams := &StartSessionParams{
		JobID:     params.JobID,
		VaultID:   params.VaultID,
		Chain:     params.Chain,
		KeyEpoch:  params.KeyEpoch,
		SignAlgo:  params.SignAlgo,
		Messages:  params.Messages,
		Threshold: params.Threshold,
	}

	if err := s.coordinator.StartSession(ctx, startParams); err != nil {
		return nil, err
	}

	                     
	result := s.waitForSignature(ctx, params.JobID)
	return result, result.Error
}

                                      
func (s *SignerService) waitForSignature(ctx context.Context, jobID string) *SignResult {
	deadline := time.After(s.signTimeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return &SignResult{JobID: jobID, Success: false, Error: ctx.Err()}
		case <-deadline:
			return &SignResult{JobID: jobID, Success: false, Error: ErrSigningTimeout}
		case <-ticker.C:
			sess := s.coordinator.GetSession(jobID)
			if sess == nil {
				continue
			}
			state := sess.GetState()
			sigs := sess.GetFinalSignatures()

			if state == roastsession.SignSessionStateComplete {
				return &SignResult{JobID: jobID, Signatures: sigs, Success: true}
			}
			if state == roastsession.SignSessionStateFailed {
				return &SignResult{JobID: jobID, Success: false, Error: ErrSigningFailed}
			}
		}
	}
}

                                 
func (s *SignerService) GenerateNonce() (*roast.SignerNonce, error) {
	                     
	hidingBytes := make([]byte, 32)
	bindingBytes := make([]byte, 32)
	if _, err := rand.Read(hidingBytes); err != nil {
		return nil, err
	}
	if _, err := rand.Read(bindingBytes); err != nil {
		return nil, err
	}

	hiding := new(big.Int).SetBytes(hidingBytes)
	hiding.Mod(hiding, s.group.Order())

	binding := new(big.Int).SetBytes(bindingBytes)
	binding.Mod(binding, s.group.Order())

	                  
	hidingPoint := s.group.ScalarBaseMult(hiding)
	bindingPoint := s.group.ScalarBaseMult(binding)

	return &roast.SignerNonce{
		HidingNonce:  hiding,
		BindingNonce: binding,
		HidingPoint:  hidingPoint,
		BindingPoint: bindingPoint,
	}, nil
}

                                       
func (s *SignerService) ComputePartialSig(
	signerID int,
	nonce *roast.SignerNonce,
	allNonces []roast.SignerNonce,
	msg []byte,
	share *big.Int,
	groupPubX *big.Int,
	signerIDs []int,
) (*big.Int, error) {
	                    
	R, err := roast.ComputeGroupCommitment(allNonces, msg, s.group)
	if err != nil {
		return nil, err
	}

	                          
	rho := roast.ComputeBindingCoefficient(signerID, msg, allNonces, s.group)

	                    
	e := roast.ComputeChallenge(R, groupPubX, msg, s.group)

	                           
	lambdas := roast.ComputeLagrangeCoefficientsForSet(signerIDs, s.group.Order())
	lambda := lambdas[signerID]

	                     
	z := roast.ComputePartialSignature(
		signerID,
		nonce.HidingNonce,
		nonce.BindingNonce,
		share,
		rho,
		lambda,
		e,
		s.group,
	)

	return z, nil
}

                                      
func (s *SignerService) VerifyPartialSig(
	signerID int,
	share *big.Int,
	nonce roast.SignerNonce,
	allNonces []roast.SignerNonce,
	msg []byte,
	pubKeyShare curve.Point,
	groupPubX *big.Int,
	signerIDs []int,
) bool {
	                     
	rho := roast.ComputeBindingCoefficient(signerID, msg, allNonces, s.group)

	                    
	R, err := roast.ComputeGroupCommitment(allNonces, msg, s.group)
	if err != nil {
		return false
	}

	                  
	e := roast.ComputeChallenge(R, groupPubX, msg, s.group)

	                           
	lambdas := roast.ComputeLagrangeCoefficientsForSet(signerIDs, s.group.Order())
	lambda := lambdas[signerID]

	return roast.VerifyPartialSignature(signerID, share, nonce, rho, lambda, e, pubKeyShare, s.group)
}

                                   
func (s *SignerService) AggregateSignatures(
	allNonces []roast.SignerNonce,
	shares []roast.SignerShare,
	msg []byte,
) ([]byte, error) {
	                    
	R, err := roast.ComputeGroupCommitment(allNonces, msg, s.group)
	if err != nil {
		return nil, err
	}

	               
	return roast.AggregateSignatures(R, shares, s.group)
}

                                     

                                                      
func ComputeLagrangeCoefficient(signerID int, signerIDs []int, order *big.Int) *big.Int {
	ids := make([]*big.Int, len(signerIDs))
	for i, id := range signerIDs {
		ids[i] = big.NewInt(int64(id))
	}

	lambdas := dkg.ComputeLagrangeCoefficients(ids, order)

	for i, id := range signerIDs {
		if id == signerID {
			return lambdas[i]
		}
	}
	return big.NewInt(0)
}

                                   
func init() {
	_ = logs.Debug
}
