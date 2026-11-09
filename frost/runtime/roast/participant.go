                                     
                                                                                                   
                           
                                                           
                                                                                       
                                                                                           
                                                        

package roast

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"dex/frost/runtime/session"
	"dex/logs"
	"dex/pb"
)

                                     

var (
	ErrParticipantNotInCommittee = errors.New("participant not in committee")
	ErrInvalidNonceRequest       = errors.New("invalid nonce request")
	ErrInvalidSignRequest        = errors.New("invalid sign request")
	ErrNonceNotFound             = errors.New("nonce not found")
	ErrParticipantRetryLimit     = errors.New("participant signing retry limit exceeded")
	ErrStaleRoastRound           = errors.New("stale ROAST round")
	ErrFutureRoastRound          = errors.New("future ROAST round")
	                                                                            
	                                                                            
	                                                                          
	ErrRoastStateNotReady = errors.New("ROAST state not ready")
	                                                                           
	                                                                           
	                                                             
	ErrLocalShareNotReady = errors.New("local FROST share not ready")
	                                                                        
	                                                                   
	                                                                    
	ErrRoastResponseSendFailed = errors.New("ROAST response send failed")
	                                                                          
	                                                                           
	                                                                            
	ErrSigningIntentNotReady = errors.New("finalized signing intent not ready")
)

                               

type ParticipantConfig struct {
	                                 
	ShareStorePath string
	                                                                          
	                                                                   
	MaxRetries int
}

                                                                                     
type SigningRequestValidator func(env *Envelope, messages [][]byte) error

func DefaultParticipantConfig() *ParticipantConfig {
	return &ParticipantConfig{
		ShareStorePath: "./frost_shares",
		MaxRetries:     DefaultSigningMaxRetries,
	}
}

                                    

                                                                                           
type Participant struct {
	mu sync.RWMutex

	config *ParticipantConfig
	nodeID NodeID

	sessions map[string]*ParticipantSession                    

	messenger        RoastMessenger
	vaultProvider    VaultCommitteeProvider
	cryptoFactory    CryptoExecutorFactory
	sessionStore     *session.SessionStore
	shareStore       LocalShareStore
	requestValidator SigningRequestValidator

	currentHeight uint64

	                                                           
	shares map[string][]byte

	Logger logs.Logger
}

                                                                                                 
func (p *Participant) SetSigningRequestValidator(validator SigningRequestValidator) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requestValidator = validator
}

func (p *Participant) validateSigningRequest(env *Envelope, messages [][]byte) error {
	p.mu.RLock()
	validator := p.requestValidator
	p.mu.RUnlock()
	if validator == nil {
		return nil
	}
	return validator(env, messages)
}

func sameMessages(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

                                                 
type ParticipantSession struct {
	mu sync.RWMutex

	JobID    string
	VaultID  uint32
	Chain    string
	KeyEpoch uint64
	SignAlgo pb.SignAlgo

	Messages [][]byte

	MyIndex int                               
	MyShare []byte                      

	HidingNonces  [][]byte                             
	BindingNonces [][]byte                               
	HidingPoints  [][]byte                 
	BindingPoints [][]byte                   

	AggregatedNonces []byte                           
	GeneratedShares  [][]byte                                                                       

	Tweaks [][]byte                                              

	State       ParticipantSessionState
	CreatedAt   time.Time
	CompletedAt time.Time
	RetryCount  int
	Round       uint32
}

                                                
type ParticipantSessionState int

const (
	ParticipantStateInit ParticipantSessionState = iota
	ParticipantStateNonceGenerated
	ParticipantStateShareGenerated
	ParticipantStateComplete
	ParticipantStateFailed
)

func (s ParticipantSessionState) String() string {
	switch s {
	case ParticipantStateInit:
		return "INIT"
	case ParticipantStateNonceGenerated:
		return "NONCE_GENERATED"
	case ParticipantStateShareGenerated:
		return "SHARE_GENERATED"
	case ParticipantStateComplete:
		return "COMPLETE"
	case ParticipantStateFailed:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

                                                                                                                 
func NewParticipant(nodeID NodeID, messenger RoastMessenger, vaultProvider VaultCommitteeProvider, cryptoFactory CryptoExecutorFactory, sessionStore *session.SessionStore, shareStore LocalShareStore, logger logs.Logger) *Participant {
	if sessionStore == nil {
		sessionStore = session.NewSessionStore(nil)
	}
	return &Participant{
		config:        DefaultParticipantConfig(),
		nodeID:        nodeID,
		sessions:      make(map[string]*ParticipantSession),
		messenger:     messenger,
		vaultProvider: vaultProvider,
		cryptoFactory: cryptoFactory,
		sessionStore:  sessionStore,
		shareStore:    shareStore,
		Logger:        logger,
		currentHeight: 0,
		shares:        make(map[string][]byte),
	}
}

                                        
func (p *Participant) UpdateHeight(height uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.currentHeight = height
}

                          
func (p *Participant) GetSession(jobID string) *ParticipantSession {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sessions[jobID]
}

                                                  
func (p *Participant) CloseSession(jobID string) {
	p.mu.Lock()
	sess, exists := p.sessions[jobID]
	if exists {
		                                                                 
		                                                                   
		                                                             
		delete(p.sessions, jobID)
	}
	p.mu.Unlock()
	if !exists {
		return
	}

	sess.mu.Lock()
	hiding := cloneMessages(sess.HidingNonces)
	binding := cloneMessages(sess.BindingNonces)
	sess.HidingNonces = nil
	sess.BindingNonces = nil
	sess.HidingPoints = nil
	sess.BindingPoints = nil
	sess.AggregatedNonces = nil
	sess.GeneratedShares = nil
	sess.mu.Unlock()

	for _, nonce := range hiding {
		_ = p.sessionStore.ReleaseNonce(nonce)
	}
	for _, nonce := range binding {
		_ = p.sessionStore.ReleaseNonce(nonce)
	}
}

                                        

func shareKey(chain string, vaultID uint32, epoch uint64) string {
	return fmt.Sprintf("%s_%d_%d", chain, vaultID, epoch)
}

func cloneBytes(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

func cloneMessages(src [][]byte) [][]byte {
	if len(src) == 0 {
		return nil
	}
	dst := make([][]byte, len(src))
	for i := range src {
		dst[i] = cloneBytes(src[i])
	}
	return dst
}

func shareFingerprintForLog(share []byte) string {
	if len(share) == 0 {
		return "len=0"
	}
	sum := sha256.Sum256(share)
	return fmt.Sprintf("len=%d,sha256_8=%x", len(share), sum[:8])
}

func committeeSummaryForLog(committee []SignerInfo) string {
	if len(committee) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(committee))
	for idx, member := range committee {
		parts = append(parts, fmt.Sprintf("%d:%s", idx, member.ID))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func firstMessageForLog(messages [][]byte) string {
	if len(messages) == 0 || len(messages[0]) == 0 {
		return ""
	}
	return hex.EncodeToString(messages[0])
}

func bigIntHexForLog(v *big.Int) string {
	if v == nil {
		return "nil"
	}
	return v.Text(16)
}

func bigIntFillBytes32(v *big.Int) []byte {
	b := make([]byte, 32)
	if v != nil {
		v.FillBytes(b)
	}
	return b
}

func lagrangeMapForLog(signerIDs []int, lambdas map[int]*big.Int) string {
	if len(signerIDs) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(signerIDs))
	for _, signerID := range signerIDs {
		parts = append(parts, fmt.Sprintf("%d:%s", signerID, bigIntHexForLog(lambdas[signerID])))
	}
	return "[" + strings.Join(parts, ",") + "]"
}
