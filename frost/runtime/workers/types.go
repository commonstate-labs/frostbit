                                 
                           

package workers

import (
	"dex/frost/chain"
	"dex/frost/runtime/types"
)

                                                                        
type (
	ChainAdapter           = chain.ChainAdapter
	ChainAdapterFactory    = chain.ChainAdapterFactory
	WithdrawTemplateParams = chain.WithdrawTemplateParams
	WithdrawOutput         = chain.WithdrawOutput
	UTXO                   = chain.UTXO
	TemplateResult         = chain.TemplateResult
)

                                            
type StateReader = types.StateReader
type TxSubmitter = types.TxSubmitter
type LogReporter = types.LogReporter
type LocalShareStore = types.LocalShareStore
type LocalIdentity = types.LocalIdentity
type DkgLocalSessionRef = types.DkgLocalSessionRef
type VaultCommitteeProvider = types.VaultCommitteeProvider
type SignerInfo = types.SignerInfo
type MinerPubKeyProvider = types.MinerPubKeyProvider
type CryptoExecutorFactory = types.CryptoExecutorFactory
type ROASTExecutor = types.ROASTExecutor
type DKGExecutor = types.DKGExecutor
type PolynomialHandle = types.PolynomialHandle
type CurvePoint = types.CurvePoint
type NonceInput = types.NonceInput
type ShareInput = types.ShareInput
type PartialSignParams = types.PartialSignParams
type SigningService = types.SigningService
type SigningSessionParams = types.SigningSessionParams
type SessionStatus = types.SessionStatus
type SignedPackage = types.SignedPackage

                                          
type SignerSetProvider interface {
	TopK(height uint64) ([]SignerInfo, error)
	CurrentEpoch(height uint64) uint64
}
