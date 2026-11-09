                             
                                                                                                                      

package btc

import (
	"crypto/sha256"
	"errors"
	"fmt"

	"dex/frost/chain"
	"dex/pb"
	"dex/utils"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

                                  

const (
	                                                  
	TaprootWitnessVersion = 0x01
	                                                              
	SchnorrSigSize = 64
)

                                           

var (
	                                                        
	ErrInvalidTemplateData = errors.New("invalid template data")
	                                             
	ErrInvalidSignature = errors.New("invalid signature")
	                                                            
	ErrSignatureCountMismatch = errors.New("signature count mismatch with inputs")
)

                                   

                                     
type BTCAdapter struct {
	                                                     
	network string
}

                                            
func NewBTCAdapter(network string) *BTCAdapter {
	if network == "" {
		network = "mainnet"
	}
	return &BTCAdapter{
		network: network,
	}
}

                               
func (a *BTCAdapter) Chain() string {
	return chain.ChainBTC
}

                              
func (a *BTCAdapter) SignAlgo() pb.SignAlgo {
	return chain.SignAlgoBTC
}

                                                 
func (a *BTCAdapter) BuildWithdrawTemplate(params chain.WithdrawTemplateParams) (*chain.TemplateResult, error) {
	                    
	template, err := NewBTCTemplate(params)
	if err != nil {
		return nil, err
	}

	                     
	templateHash := template.TemplateHash()

	                                 
	templateData, err := template.ToJSON()
	if err != nil {
		return nil, err
	}

	                                                                        
	scriptPubKeyByInput := make(map[string][]byte, len(params.Inputs))
	for _, utxo := range params.Inputs {
		scriptPubKeyByInput[fmt.Sprintf("%s:%d", utxo.TxID, utxo.Vout)] = utxo.ScriptPubKey
	}
	scriptPubKeys := make([][]byte, len(template.Inputs))
	for i, input := range template.Inputs {
		scriptPubKeys[i] = scriptPubKeyByInput[fmt.Sprintf("%s:%d", input.TxID, input.Vout)]
	}

	                         
	sighashes, err := template.ComputeTaprootSighash(scriptPubKeys, SighashDefault)
	if err != nil {
		return nil, err
	}

	return &chain.TemplateResult{
		TemplateHash: templateHash,
		TemplateData: templateData,
		SigHashes:    sighashes,
	}, nil
}

                                  
func (a *BTCAdapter) TemplateHash(templateData []byte) ([]byte, error) {
	template, err := FromJSON(templateData)
	if err != nil {
		return nil, ErrInvalidTemplateData
	}
	return template.TemplateHash(), nil
}

                                                         
func (a *BTCAdapter) PackageSigned(templateData []byte, signatures, guardianSigs [][]byte) (*chain.SignedPackage, error) {
	template, err := FromJSON(templateData)
	if err != nil {
		return nil, ErrInvalidTemplateData
	}

	                     
	if len(signatures) != len(template.Inputs) {
		return nil, ErrSignatureCountMismatch
	}

	                     
	for _, sig := range signatures {
		if len(sig) != SchnorrSigSize {
			return nil, ErrInvalidSignature
		}
	}

	                     
	rawTx, err := a.buildRawTransaction(template, signatures, guardianSigs)
	if err != nil {
		return nil, err
	}

	return &chain.SignedPackage{
		TemplateHash:       template.TemplateHash(),
		TemplateData:       templateData,
		Signatures:         signatures,
		GuardianSignatures: guardianSigs,
		RawTx:              rawTx,
	}, nil
}

                                                  
func (a *BTCAdapter) buildRawTransaction(template *BTCTemplate, signatures, guardianSigs [][]byte) ([]byte, error) {
	                                                                        
	                                                                           
	                               
	for i, out := range template.Outputs {
		if _, err := AddressScriptForNetwork(out.Address, a.network); err != nil {
			return nil, fmt.Errorf("invalid output[%d] address for %s: %w", i, a.network, err)
		}
	}
	return template.BuildSignedRawTx(signatures, guardianSigs)
}

                                                
func (a *BTCAdapter) VerifySignature(groupPubkey []byte, msg []byte, signature []byte) (bool, error) {
	if len(signature) != SchnorrSigSize {
		return false, ErrInvalidSignature
	}
	xOnlyPubkey, err := utils.NormalizeSecp256k1XOnlyPubKey(groupPubkey)
	if err != nil {
		return false, err
	}
	if len(msg) != 32 {
		return false, errors.New("invalid message length")
	}

	                                                
	pubKey, err := schnorr.ParsePubKey(xOnlyPubkey)
	if err != nil {
		return false, fmt.Errorf("parse pubkey: %w", err)
	}

	sig, err := schnorr.ParseSignature(signature)
	if err != nil {
		return false, fmt.Errorf("parse signature: %w", err)
	}

	return sig.Verify(msg, pubKey), nil
}

                                     

                              
func ComputeTxID(rawTx []byte) []byte {
	                                                                                                       
	                               
	hash1 := sha256.Sum256(rawTx)
	hash2 := sha256.Sum256(hash1[:])
	return hash2[:]
}
