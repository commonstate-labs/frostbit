package workers

import (
	"dex/lifecycle"
	"dex/logs"
	"dex/pb"
	"math/big"
	"sync"
	"time"
)

                                                             
                                                                               
                                                          
type TransitionWorker struct {
	mu sync.RWMutex

	stateReader     StateReader
	txSubmitter     TxSubmitter
	pubKeyProvider  MinerPubKeyProvider
	cryptoFactory   CryptoExecutorFactory
	vaultProvider   VaultCommitteeProvider
	signerProvider  SignerSetProvider
	adapterFactory  ChainAdapterFactory
	signingService  SigningService
	localShareStore LocalShareStore
	localIdentity   LocalIdentity
	localAddress    string
	Logger          logs.Logger

	                   
	sessions map[string]*DKGSession                        

	         
	commitTimeout time.Duration
	shareTimeout  time.Duration
	signTimeout   time.Duration

	plannedMigrations    map[string]bool
	migrationLastAttempt map[string]uint64                                                                                      
	migrationSignedTx    map[string]*pb.FrostVaultTransitionSignedTx                                       
	migrationSignedOrder []string
	migrationChildActive map[string]bool
	migrationChildCursor map[string]string
	warnedExpired        map[string]bool                                                 
	complaintScans       map[string]bool                                                   
	complaintPending     map[string]bool                                                        

	                  
	lastIssuedNonce uint64
	issuedNonce     bool

	emergency *lifecycle.EmergencyStop
}

                                                                                        
                                                                    
func (w *TransitionWorker) SetEmergencyStop(e *lifecycle.EmergencyStop) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.emergency = e
	w.mu.Unlock()
}

                                              
type DKGSession struct {
	Chain     string
	VaultID   uint32
	EpochID   uint64
	SessionID string
	SignAlgo  pb.SignAlgo

	Polynomial  PolynomialHandle                         
	LocalShare  *big.Int                        
	LocalShares map[int][]byte                                 
	EncRands    map[int][]byte                                 

	LocalShareBytes []byte
	GroupPubkey     []byte
	Commitments     map[string][]byte                              
	ReceivedShrs    map[string][]byte                        

	MyIndex          int
	Committee        []string
	CommitteePubKeys map[string][]byte
	Threshold        int

	Phase           string                                                
	TriggerHeight   uint64
	CommitDeadline  uint64
	SharingDeadline uint64
	DisputeDeadline uint64
	CreatedAt       time.Time
}

                                                 
func NewTransitionWorker(
	stateReader StateReader,
	txSubmitter TxSubmitter,
	pubKeyProvider MinerPubKeyProvider,
	cryptoFactory CryptoExecutorFactory,
	vaultProvider VaultCommitteeProvider,
	signerProvider SignerSetProvider,
	adapterFactory ChainAdapterFactory,
	signingService SigningService,
	localShareStore LocalShareStore,
	localIdentity LocalIdentity,
	localAddress string,
	logger logs.Logger,
) *TransitionWorker {
	return &TransitionWorker{
		stateReader:          stateReader,
		txSubmitter:          txSubmitter,
		pubKeyProvider:       pubKeyProvider,
		cryptoFactory:        cryptoFactory,
		vaultProvider:        vaultProvider,
		signerProvider:       signerProvider,
		adapterFactory:       adapterFactory,
		signingService:       signingService,
		localShareStore:      localShareStore,
		localIdentity:        localIdentity,
		localAddress:         localAddress,
		Logger:               logger,
		sessions:             make(map[string]*DKGSession),
		commitTimeout:        30 * time.Second,
		shareTimeout:         30 * time.Second,
		signTimeout:          60 * time.Second,
		plannedMigrations:    make(map[string]bool),
		migrationLastAttempt: make(map[string]uint64),
		migrationSignedTx:    make(map[string]*pb.FrostVaultTransitionSignedTx),
		warnedExpired:        make(map[string]bool),
		complaintScans:       make(map[string]bool),
		complaintPending:     make(map[string]bool),
		lastIssuedNonce:      0,
	}
}
