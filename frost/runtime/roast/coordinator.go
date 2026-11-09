                                     
                                                                                 
                           
                                                                       
                                                                          
                                                        
                                                                             
                                                               

package roast

import (
	"errors"
	"sync"
	"time"

	roastsession "dex/frost/runtime/session"
	"dex/logs"
)

                                     

var (
	ErrCoordinatorNotLeader     = errors.New("not current coordinator")
	ErrSessionNotFound          = errors.New("session not found")
	ErrSessionAlreadyExists     = errors.New("session already exists")
	ErrInsufficientParticipants = errors.New("insufficient participants")
	ErrInvalidRoastPayload      = errors.New("invalid roast payload")
	ErrParticipantNotFound      = errors.New("participant not found")
	ErrInsufficientNonces       = errors.New("insufficient nonces for aggregation")
	ErrInsufficientShares       = errors.New("insufficient shares for aggregation")
)

                               

type CoordinatorConfig struct {
	NonceCollectTimeout    time.Duration
	ShareCollectTimeout    time.Duration
	AggregatorRotateBlocks uint64
	MaxRetries             int
}

                                                                            
                                                                            
                                                             
const DefaultSigningMaxRetries = 5

func DefaultCoordinatorConfig() *CoordinatorConfig {
	return &CoordinatorConfig{
		NonceCollectTimeout:    10 * time.Second,
		ShareCollectTimeout:    10 * time.Second,
		AggregatorRotateBlocks: 10,
		MaxRetries:             DefaultSigningMaxRetries,
	}
}

                                    

                                                                                        
type Coordinator struct {
	mu sync.RWMutex

	config *CoordinatorConfig
	nodeID NodeID

	sessions map[string]*roastsession.Session                    

	messenger     RoastMessenger
	vaultProvider VaultCommitteeProvider
	cryptoFactory CryptoExecutorFactory
	sessionStore  *roastsession.SessionStore
	Logger        logs.Logger

	currentHeight uint64
}

func NewCoordinator(nodeID NodeID, messenger RoastMessenger, vaultProvider VaultCommitteeProvider, cryptoFactory CryptoExecutorFactory, logger logs.Logger) *Coordinator {
	return &Coordinator{
		config:        DefaultCoordinatorConfig(),
		nodeID:        nodeID,
		sessions:      make(map[string]*roastsession.Session),
		messenger:     messenger,
		vaultProvider: vaultProvider,
		cryptoFactory: cryptoFactory,
		Logger:        logger,
	}
}

                                                                      
func (c *Coordinator) UpdateHeight(height uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.currentHeight = height
}
