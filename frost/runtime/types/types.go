                               
                                          

package types

import (
	"context"
	"math/big"
	"time"

	"dex/pb"
)

                                                                                   
                                                                   
type LocalIdentity interface {
	Sign([]byte) ([]byte, error)
	PublicKeyBytes() []byte
	SignFrostEnvelope(*pb.FrostEnvelope) error
	DecryptECIES([]byte) ([]byte, error)
}

                                                                                                         

                                          
type SigningSessionParams struct {
	RequireGuardian bool
	JobID           string
	Chain           string
	VaultID         uint32
	KeyEpoch        uint64
	SignAlgo        pb.SignAlgo
	Messages        [][]byte
	Threshold       int
	Tweaks          [][]byte
}

                             
type SessionStatus struct {
	SessionID   string
	JobID       string
	State       string
	Progress    float64
	StartedAt   time.Time
	CompletedAt *time.Time
	Error       error
}

                             
type SignedPackage struct {
	GuardianSignatures [][]byte
	SessionID          string
	JobID              string
	Signature          []byte
	Signatures         [][]byte
	RawTx              []byte
	TemplateHash       []byte
}

                                    
type SigningService interface {
	StartSigningSession(ctx context.Context, params *SigningSessionParams) (sessionID string, err error)
	GetSessionStatus(sessionID string) (*SessionStatus, error)
	CancelSession(sessionID string) error
	WaitForCompletion(ctx context.Context, sessionID string, timeout time.Duration) (*SignedPackage, error)
}

                                  
type NodeID string

                                   
type SignerInfo struct {
	ID        NodeID             
	Index     uint32                         
	PublicKey []byte          
	Weight    uint64                      
}

                                     

                                   
type CurvePoint struct {
	X, Y *big.Int
}

                                           

                                       
type NonceInput struct {
	SignerID     int
	HidingNonce  *big.Int                                        
	BindingNonce *big.Int                                        
	HidingPoint  CurvePoint
	BindingPoint CurvePoint
}

                                
type ShareInput struct {
	SignerID int
	Share    *big.Int
}

                                                
type PartialSignParams struct {
	SignerID     int
	HidingNonce  *big.Int
	BindingNonce *big.Int
	SecretShare  *big.Int                            
	Rho          *big.Int                
	Lambda       *big.Int                      
	Challenge    *big.Int             
}

                                             
                                                                            
type ROASTExecutor interface {
	                                                                   
	ComputeGroupCommitment(nonces []NonceInput, msg []byte) (CurvePoint, error)

	                                                    
	ComputeBindingCoefficient(signerID int, msg []byte, nonces []NonceInput) *big.Int

	                                     
	ComputeChallenge(R CurvePoint, groupPubX *big.Int, msg []byte) *big.Int

	                                                       
	ComputeLagrangeCoefficients(signerIDs []int) map[int]*big.Int

	                                                 
	ComputePartialSignature(params PartialSignParams) *big.Int

	                                                              
	AggregateSignatures(R CurvePoint, shares []ShareInput) ([]byte, error)

	                                     
	GenerateNoncePair() (hiding, binding *big.Int, hidingPoint, bindingPoint CurvePoint, err error)

	                                  
	ScalarBaseMult(k *big.Int) CurvePoint

	                                    
	SerializePoint(P CurvePoint) []byte
}

                                         

                                                              
type PolynomialHandle interface {
	                           
	Evaluate(x int) *big.Int
	                                        
	Coefficients() []*big.Int
}

                                         
                                                                          
type DKGExecutor interface {
	                                                   
	GeneratePolynomial(threshold int) (PolynomialHandle, error)

	                                                                
	ComputeCommitments(poly PolynomialHandle) [][]byte

	                                                        
	EvaluateShare(poly PolynomialHandle, receiverIndex int) []byte

	                                               
	AggregateShares(shares [][]byte) []byte

	                                                            
	ComputeGroupPubkey(commitments [][]byte) []byte

	                                                 
	VerifyShare(share []byte, commitments [][]byte, senderIndex, receiverIndex int) bool

	                                                                       
	SchnorrSign(privateKey *big.Int, msgHash []byte) ([]byte, error)

	                                  
	ScalarBaseMult(k *big.Int) CurvePoint

	                                    
	SerializePoint(P CurvePoint) []byte
}

                                     

                                                                
type CryptoExecutorFactory interface {
	                                                            
	NewROASTExecutor(signAlgo int32) (ROASTExecutor, error)

	                                                        
	NewDKGExecutor(signAlgo int32) (DKGExecutor, error)
}

                                           

                                                                        
type VaultCommitteeProvider interface {
	                                                                  
	VaultCommittee(chain string, vaultID uint32, epoch uint64) ([]SignerInfo, error)
	                                                       
	VaultCurrentEpoch(chain string, vaultID uint32) uint64
	                                                       
	VaultGroupPubkey(chain string, vaultID uint32, epoch uint64) ([]byte, error)
	                                     
	CalculateThreshold(chain string, vaultID uint32) (int, error)
}

                                               

                                   
type RoastEnvelope struct {
	SessionID string
	Kind      string
	From      NodeID
	Chain     string
	VaultID   uint32
	SignAlgo  int32
	Epoch     uint64
	Round     uint32
	Payload   []byte
	Tweaks    [][]byte
}

                                          
type RoastMessenger interface {
	Send(to NodeID, msg *RoastEnvelope) error
	Broadcast(peers []NodeID, msg *RoastEnvelope) error
}

                                              

                                                                                           
type ChainStateReader interface {
	Get(key string) ([]byte, bool, error)
	Scan(prefix string, fn func(k string, v []byte) bool) error
}

                                                                 
type StateReader = ChainStateReader

                                                                    
type TxSubmitter interface {
	Submit(tx any) (txID string, err error)
	SubmitDkgCommitTx(ctx context.Context, tx *pb.FrostVaultDkgCommitTx) error
	SubmitDkgShareTx(ctx context.Context, tx *pb.FrostVaultDkgShareTx) error
	SubmitDkgComplaintTx(ctx context.Context, tx *pb.FrostVaultDkgComplaintTx) error
	SubmitDkgRevealTx(ctx context.Context, tx *pb.FrostVaultDkgRevealTx) error
	SubmitDkgValidationSignedTx(ctx context.Context, tx *pb.FrostVaultDkgValidationSignedTx) error
	SubmitTransitionSignedTx(ctx context.Context, tx *pb.FrostVaultTransitionSignedTx) error
	SubmitWithdrawClaimTx(ctx context.Context, tx *pb.FrostWithdrawClaimTx) error
	SubmitWithdrawSignedTx(ctx context.Context, tx *pb.FrostWithdrawSignedTx) error
}

                                                                                       
type LogReporter interface {
	ReportWithdrawPlanningLog(ctx context.Context, log *pb.FrostWithdrawPlanningLogTx) error
}

                                                                                        
type DkgLocalSessionRef struct {
	Chain     string
	VaultID   uint32
	Epoch     uint64
	SessionID string
}

type LocalShareStore interface {
	SaveLocalShare(chain string, vaultID uint32, epoch uint64, share []byte) error
	LoadLocalShare(chain string, vaultID uint32, epoch uint64) ([]byte, error)
	SavePendingLocalShare(chain string, vaultID uint32, epoch uint64, sessionID string, share []byte) error
	LoadPendingLocalShare(chain string, vaultID uint32, epoch uint64, sessionID string) ([]byte, error)
	PromotePendingLocalShare(chain string, vaultID uint32, epoch uint64, sessionID string) error
	HasDkgLocalSession(chain string, vaultID uint32, epoch uint64, sessionID string) (bool, error)
	ListDkgLocalSessions() ([]DkgLocalSessionRef, error)
	DeleteDkgSessionArtifacts(chain string, vaultID uint32, epoch uint64, sessionID string) error

	SaveDkgRevealMaterial(chain string, vaultID uint32, epoch uint64, sessionID string, receiverIndex int, share, encRand []byte) error
	LoadDkgRevealMaterial(chain string, vaultID uint32, epoch uint64, sessionID string, receiverIndex int) (share, encRand []byte, err error)
}

                                            
type MinerPubKeyProvider interface {
	                                                    
	                        
	                                                                
	GetMinerSigningPubKey(minerID string, signAlgo pb.SignAlgo) ([]byte, error)
}
