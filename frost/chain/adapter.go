package chain

import (
	"errors"
	"strings"

	"dex/pb"
)

                                                            

const (
	ChainBTC  = "btc"            
	ChainETH  = "eth"             
	ChainBNB  = "bnb"                    
	ChainTRX  = "trx"         
	ChainBASE = "base"        
	ChainARB  = "arb"                 

	                                                                     
	                                          
	MaxWithdrawalsPerBatch = 64

	                                                                           
	                                                                     
	MaxBTCWithdrawalsPerBatch = 256

	                                                                          
	                                                                      
	AccountWithdrawBatchTimeoutBlocks uint64 = 100
	BTCWithdrawBatchTimeoutBlocks            = AccountWithdrawBatchTimeoutBlocks
)

                                     
var SupportedChains = []string{ChainBTC, ChainETH, ChainBNB, ChainTRX, ChainBASE, ChainARB}

                                      
var ErrUnsupportedChain = errors.New("unsupported chain")

                                                      
                                                         
func NormalizeChain(chain string) string {
	normalized := strings.ToLower(strings.TrimSpace(chain))
	switch normalized {
	case "bsc":
		return ChainBNB
	case "arbitrum", "arbitrum-one", "arbitrum_one":
		return ChainARB
	default:
		return normalized
	}
}

                                              
func IsValidChain(chain string) bool {
	normalized := NormalizeChain(chain)
	for _, c := range SupportedChains {
		if c == normalized {
			return true
		}
	}
	return false
}

                                                
                                       
const (
	SignAlgoBTC = pb.SignAlgo_SIGN_ALGO_SCHNORR_SECP256K1_BIP340       
	SignAlgoETH = pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128                  
	SignAlgoTRX = pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128                                                        
)

                              
type WithdrawOutput struct {
	WithdrawID string                   
	To         string                
	Amount     string                                       
	Token      string                                                          
	                                                                    
	                                                                           
	BroadcastFee string
}

                                   
type UTXO struct {
	TxID          string                      
	Vout          uint32                
	Amount        uint64                       
	ScriptPubKey  []byte                
	ConfirmHeight uint64                                        
}

                                            
                                                                                                         
type WithdrawTemplateParams struct {
	GuardianPubkey  []byte                                                             
	BTCInputPubkeys [][]byte                                                                               
	Chain           string                       
	Asset           string                          
	VaultID         uint32                      
	KeyEpoch        uint64                          
	WithdrawIDs     []string                                                               
	Outputs         []WithdrawOutput                                                      
	             
	Inputs        []UTXO                                                               
	ChangeAddress string                
	Fee           uint64                                                                       
	ChangeAmount  uint64                                                                        
	                  
	ContractAddr      string                
	MethodID          []byte                
	Payload           []byte                       
	Nonce             uint64                                                                
	AuthorityProtocol uint32
	DexChainID        uint64
	BatchSeq          string                                                       
}

                                    
type TemplateResult struct {
	TemplateHash []byte                                        
	TemplateData []byte                                        
	             
	SigHashes [][]byte                                
}

                          
type SignedPackage struct {
	GuardianSignatures [][]byte
	TemplateHash       []byte                  
	TemplateData       []byte                  
	Signatures         [][]byte                                                    
	RawTx              []byte                                          
}

                                  
                                                                    
type ChainAdapter interface {
	                        
	Chain() string

	                                                                           
	SignAlgo() pb.SignAlgo

	                                                 
	                                                                        
	BuildWithdrawTemplate(params WithdrawTemplateParams) (*TemplateResult, error)

	                                                                    
	                                                
	TemplateHash(templateData []byte) ([]byte, error)

	                                            
	                                                      
	PackageSigned(templateData []byte, signatures [][]byte, guardianSignatures [][]byte) (*SignedPackage, error)

	                                                                        
}

                                         
type ChainAdapterFactory interface {
	                                      
	Adapter(chain string) (ChainAdapter, error)

	                                     
	RegisterAdapter(adapter ChainAdapter)
}

                                                    
type DefaultAdapterFactory struct {
	adapters map[string]ChainAdapter
}

                                                       
func NewDefaultAdapterFactory() *DefaultAdapterFactory {
	return &DefaultAdapterFactory{
		adapters: make(map[string]ChainAdapter),
	}
}

                                      
func (f *DefaultAdapterFactory) Adapter(chain string) (ChainAdapter, error) {
	                  
	normalized := NormalizeChain(chain)
	adapter, ok := f.adapters[normalized]
	if !ok {
		return nil, ErrUnsupportedChain
	}
	return adapter, nil
}

                                     
func (f *DefaultAdapterFactory) RegisterAdapter(adapter ChainAdapter) {
	f.adapters[adapter.Chain()] = adapter
}
