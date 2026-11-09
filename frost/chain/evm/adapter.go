                             
                                                                                        
package evm

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"dex/frost/chain"
	"dex/frost/core/curve"
	frostcore "dex/frost/core/frost"
	"dex/pb"

	"golang.org/x/crypto/sha3"
)

const (
	                         
	ChainETH = chain.ChainETH
	                         
	ChainBNB  = chain.ChainBNB
	ChainBASE = chain.ChainBASE
	ChainARB  = chain.ChainARB
	                                                           
	SchnorrSigSize = 64
	                                                                                     
	SchnorrSigWithYSize = 96

	defaultEVMWithdrawMethodSignature = "withdrawBatch((bytes32,uint256,address,uint256,uint256,address[],(bytes32,address,address,uint256,uint256)[]),(uint256,uint256,uint256),(uint256,uint256,uint256))"
	evmWordSize                       = 32
	evmWithdrawMessageSize            = 256                                        
	MaxWithdrawalsPerBatch            = chain.MaxWithdrawalsPerBatch
	withdrawBatchDomain               = "DEX_VAULT_MIXED_WITHDRAW_BATCH_V1"
)

var (
	ErrInvalidTemplateData    = errors.New("invalid template data")
	ErrInvalidSignature       = errors.New("invalid signature")
	ErrInvalidContractAddress = errors.New("invalid contract address")
	ErrInvalidPublicKey       = errors.New("invalid public key")
	ErrInvalidMessage         = errors.New("invalid message")
	ErrInvalidOutput          = errors.New("invalid output")
	ErrInvalidAddress         = errors.New("invalid address")
	ErrInvalidAmount          = errors.New("invalid amount")
)

                               
type EVMTemplate struct {
	AuthorityProtocol uint32      `json:"authority_protocol,omitempty"`
	GuardianPubkey    []byte      `json:"guardian_pubkey"`
	DexChainID        uint64      `json:"dex_chain_id,omitempty"`
	ChainID           uint64      `json:"chain_id"`
	VaultID           uint32      `json:"vault_id"`
	KeyEpoch          uint64      `json:"key_epoch"`
	ContractAddr      string      `json:"contract_addr"`
	MethodID          []byte      `json:"method_id"`
	Payload           []byte      `json:"payload"`
	WithdrawIDs       []string    `json:"withdraw_ids"`
	Outputs           []EVMOutput `json:"outputs"`
	Nonce             uint64      `json:"nonce"`
	BatchSeq          string      `json:"batch_seq,omitempty"`
	RewardTokens      []string    `json:"reward_tokens,omitempty"`
}

                       
type EVMOutput struct {
	WithdrawID  string `json:"withdraw_id"`
	To          string `json:"to"`
	GrossAmount string `json:"gross_amount"`
	Token       string `json:"token"`
	RewardIndex uint32 `json:"reward_index"`
	                                                                           
	                                                                          
	Amount       string `json:"amount,omitempty"`
	BroadcastFee string `json:"broadcast_fee,omitempty"`
}

                               
func (t *EVMTemplate) Serialize() []byte {
	var buf bytes.Buffer

	binary.Write(&buf, binary.BigEndian, t.ChainID)
	binary.Write(&buf, binary.BigEndian, t.VaultID)
	binary.Write(&buf, binary.BigEndian, t.KeyEpoch)

	addrBytes, _ := hex.DecodeString(normalizeHexString(t.ContractAddr))
	buf.Write(padLeft(addrBytes, 20))

	buf.Write(t.MethodID)
	buf.Write(t.Payload)
	binary.Write(&buf, binary.BigEndian, t.Nonce)
	batchSeq, _ := parseBatchSeq(t.BatchSeq)
	buf.Write(abiUintWord(batchSeq))
	binary.Write(&buf, binary.BigEndian, uint32(len(t.RewardTokens)))
	for _, token := range t.RewardTokens {
		tokenBytes, _ := hex.DecodeString(normalizeHexString(token))
		buf.Write(padLeft(tokenBytes, 20))
	}

	binary.Write(&buf, binary.BigEndian, uint32(len(t.Outputs)))
	for _, out := range t.Outputs {
		toBytes, _ := hex.DecodeString(normalizeHexString(out.To))
		buf.Write(padLeft(toBytes, 20))
		gross := out.GrossAmount
		if gross == "" {
			gross = out.Amount
		}
		amountBig, _ := new(big.Int).SetString(gross, 10)
		if amountBig == nil {
			amountBig = big.NewInt(0)
		}
		buf.Write(padLeft(amountBig.Bytes(), 32))
		tokenBytes, _ := hex.DecodeString(normalizeHexString(out.Token))
		buf.Write(padLeft(tokenBytes, 20))
		binary.Write(&buf, binary.BigEndian, out.RewardIndex)
	}

	binary.Write(&buf, binary.BigEndian, uint32(len(t.WithdrawIDs)))
	for _, wid := range t.WithdrawIDs {
		widBytes := []byte(wid)
		binary.Write(&buf, binary.BigEndian, uint32(len(widBytes)))
		buf.Write(widBytes)
	}

	buf.Write(t.GuardianPubkey)
	if t.AuthorityProtocol != 0 {
		binary.Write(&buf, binary.BigEndian, t.AuthorityProtocol)
		binary.Write(&buf, binary.BigEndian, t.DexChainID)
	}
	return buf.Bytes()
}

                                  
func (t *EVMTemplate) TemplateHash() []byte {
	hash := sha256.Sum256(t.Serialize())
	return hash[:]
}

                                                                              
                                                      
func (t *EVMTemplate) WithdrawMessage() ([]byte, error) {
	if err := t.validateGuardianPolicy(); err != nil {
		return nil, err
	}
	if t == nil || len(t.Outputs) == 0 || len(t.Outputs) > MaxWithdrawalsPerBatch || len(t.WithdrawIDs) != len(t.Outputs) {
		return nil, ErrInvalidOutput
	}
	if t.ChainID == 0 {
		return nil, ErrInvalidTemplateData
	}
	batchSeq, err := parseBatchSeq(t.BatchSeq)
	if err != nil {
		return nil, err
	}
	contract, err := NormalizeAddress(t.ContractAddr, false)
	if err != nil {
		return nil, ErrInvalidContractAddress
	}
	contractWord, _ := abiAddressWord(contract)
	if len(t.RewardTokens) == 0 || len(t.RewardTokens) > len(t.Outputs) {
		return nil, ErrInvalidOutput
	}
	rewardWords := make([]byte, 0, evmWordSize+len(t.RewardTokens)*evmWordSize)
	rewardWords = append(rewardWords, abiUintWord(big.NewInt(int64(len(t.RewardTokens))))...)
	var previous []byte
	for _, rawToken := range t.RewardTokens {
		canonical, tokenBytes, err := canonicalAddressBytes(rawToken)
		if err != nil || canonical != rawToken || (previous != nil && bytes.Compare(previous, tokenBytes) >= 0) {
			return nil, ErrInvalidAddress
		}
		previous = append(previous[:0], tokenBytes...)
		word, _ := abiAddressWord(canonical)
		rewardWords = append(rewardWords, word...)
	}
	items := make([]byte, 0, evmWordSize+len(t.Outputs)*5*evmWordSize)
	items = append(items, abiUintWord(big.NewInt(int64(len(t.Outputs))))...)
	for i, out := range t.Outputs {
		wid := strings.TrimSpace(t.WithdrawIDs[i])
		if wid == "" || wid != t.WithdrawIDs[i] || out.WithdrawID != wid || int(out.RewardIndex) >= len(t.RewardTokens) || out.Token != t.RewardTokens[out.RewardIndex] {
			return nil, ErrInvalidOutput
		}
		requestID, err := requestIDWord(wid)
		if err != nil {
			return nil, err
		}
		to, err := NormalizeAccountAddress("eth", out.To, false)
		if err != nil || strings.EqualFold(to, contract) {
			return nil, ErrInvalidAddress
		}
		toWord, _ := abiAddressWord(to)
		gross := out.GrossAmount
		if gross == "" {
			gross = out.Amount
		}
		amount, err := chain.ParseWithdrawAmount(gross)
		if err != nil {
			return nil, ErrInvalidAmount
		}
		fee := CalculateBroadcastFee(amount)
		if fee.Sign() <= 0 || fee.Cmp(amount) >= 0 {
			return nil, ErrInvalidAmount
		}
		tokenWord, _ := abiAddressWord(out.Token)
		items = append(items, requestID...)
		items = append(items, tokenWord...)
		items = append(items, toWord...)
		items = append(items, abiUintWord(amount)...)
		items = append(items, abiUintWord(new(big.Int).SetUint64(uint64(out.RewardIndex)))...)
	}
	                                                                            
	                                                 
	const tupleHeadWords = 7
	itemsOffset := tupleHeadWords*evmWordSize + len(rewardWords)
	encoded := make([]byte, 0, evmWordSize+tupleHeadWords*evmWordSize+len(rewardWords)+len(items))
	encoded = append(encoded, abiUintWord(big.NewInt(evmWordSize))...)
	encoded = append(encoded, keccak256([]byte(withdrawBatchDomain))...)
	encoded = append(encoded, abiUintWord(new(big.Int).SetUint64(t.ChainID))...)
	encoded = append(encoded, contractWord...)
	encoded = append(encoded, abiUintWord(new(big.Int).SetUint64(t.KeyEpoch))...)
	encoded = append(encoded, abiUintWord(batchSeq)...)
	encoded = append(encoded, abiUintWord(big.NewInt(tupleHeadWords*evmWordSize))...)
	encoded = append(encoded, abiUintWord(big.NewInt(int64(itemsOffset)))...)
	encoded = append(encoded, rewardWords...)
	encoded = append(encoded, items...)
	return encoded, nil
}

                                                                               
                                                                                
                                                                            
func (t *EVMTemplate) SigningHash() ([]byte, error) {
	if err := t.validateGuardianPolicy(); err != nil {
		return nil, err
	}
	if t == nil {
		return nil, ErrInvalidTemplateData
	}
	if t.isSolidityWithdrawCall() {
		msg, err := t.WithdrawMessage()
		if err != nil {
			return nil, err
		}
		return keccak256(msg), nil
	}
	if t.isHandoverProof() {
		msg, err := t.HandoverProofMessage()
		if err != nil {
			return nil, err
		}
		return keccak256(msg), nil
	}
	return nil, ErrInvalidTemplateData
}

                                                                  
                                                                           
                                                                            
                 
func (t *EVMTemplate) HandoverProofMessage() ([]byte, error) {
	if t.isHandoverProofV2() {
		return t.handoverProofMessageV2()
	}
	return nil, ErrInvalidTemplateData
}

func (t *EVMTemplate) validateGuardianPolicy() error {
	if t == nil || t.AuthorityProtocol != 3 {
		return ErrInvalidTemplateData
	}
	pub, err := CanonicalBN128PublicKey(t.GuardianPubkey)
	if err != nil || !bytes.Equal(pub, t.GuardianPubkey) {
		return ErrInvalidPublicKey
	}
	return nil
}

                           
func (t *EVMTemplate) ToJSON() ([]byte, error) {
	return json.Marshal(t)
}

                                 
func FromJSON(data []byte) (*EVMTemplate, error) {
	var t EVMTemplate
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

                              
type EVMAdapter struct {
	chainName string
	chainID   uint64
}

func DefaultChainID(chainName string) uint64 {
	switch chain.NormalizeChain(chainName) {
	case ChainETH:
		return 1
	case ChainBNB:
		return 56
	case ChainBASE:
		return 8453
	case ChainARB:
		return 42161
	case chain.ChainTRX:
		return 728126428
	default:
		return 0
	}
}

                                     
func NewEVMAdapter(chainName string, chainID uint64) *EVMAdapter {
	normalized := chain.NormalizeChain(chainName)
	if chainID == 0 {
		chainID = DefaultChainID(normalized)
	}
	return &EVMAdapter{chainName: normalized, chainID: chainID}
}

                                     
func NewETHAdapter() *EVMAdapter { return NewEVMAdapter(ChainETH, 1) }

                                     
func NewBNBAdapter() *EVMAdapter { return NewEVMAdapter(ChainBNB, 56) }

func NewBaseAdapter() *EVMAdapter { return NewEVMAdapter(ChainBASE, 8453) }

func NewARBAdapter() *EVMAdapter { return NewEVMAdapter(ChainARB, 42161) }

                        
func (a *EVMAdapter) Chain() string { return a.chainName }

                              
func (a *EVMAdapter) SignAlgo() pb.SignAlgo { return chain.SignAlgoETH }

                                                 
func (a *EVMAdapter) BuildWithdrawTemplate(params chain.WithdrawTemplateParams) (*chain.TemplateResult, error) {
	guardian, guardianErr := CanonicalBN128PublicKey(params.GuardianPubkey)
	if params.AuthorityProtocol != 3 || guardianErr != nil || !bytes.Equal(guardian, params.GuardianPubkey) {
		return nil, ErrInvalidPublicKey
	}
	contractAddr, err := NormalizeAccountAddress(a.chainName, params.ContractAddr, false)
	if err != nil {
		return nil, ErrInvalidContractAddress
	}
	if len(params.Payload) == 0 && (len(params.Outputs) == 0 || len(params.Outputs) > MaxWithdrawalsPerBatch) {
		return nil, ErrInvalidOutput
	}
	if params.Chain != "" && chain.NormalizeChain(params.Chain) != a.chainName {
		return nil, ErrInvalidTemplateData
	}
	if len(params.Payload) == 0 && len(params.WithdrawIDs) != len(params.Outputs) {
		return nil, ErrInvalidOutput
	}

	outputs := make([]EVMOutput, len(params.Outputs))
	tokenSet := make(map[string][]byte, len(params.Outputs))
	for i, out := range params.Outputs {
		if strings.TrimSpace(params.WithdrawIDs[i]) == "" || out.WithdrawID != params.WithdrawIDs[i] {
			return nil, ErrInvalidOutput
		}
		amount, err := chain.ParseWithdrawAmount(out.Amount)
		if err != nil {
			return nil, ErrInvalidAmount
		}
		to, err := NormalizeAccountAddress(a.chainName, out.To, false)
		if err != nil {
			return nil, ErrInvalidAddress
		}
		token := "0x0000000000000000000000000000000000000000"
		if strings.TrimSpace(out.Token) != "" {
			token, err = NormalizeAccountAddress(a.chainName, out.Token, true)
			if err != nil {
				return nil, ErrInvalidAddress
			}
		}
		fee := CalculateBroadcastFee(amount)
		if fee.Sign() <= 0 || fee.Cmp(amount) >= 0 {
			return nil, ErrInvalidAmount
		}
		_, tokenBytes, err := canonicalAddressBytes(token)
		if err != nil {
			return nil, ErrInvalidAddress
		}
		tokenSet[token] = tokenBytes
		outputs[i] = EVMOutput{WithdrawID: params.WithdrawIDs[i], To: to, GrossAmount: amount.String(), Token: token}
	}
	rewardTokens := make([]string, 0, len(tokenSet))
	for token := range tokenSet {
		rewardTokens = append(rewardTokens, token)
	}
	sort.Slice(rewardTokens, func(i, j int) bool { return bytes.Compare(tokenSet[rewardTokens[i]], tokenSet[rewardTokens[j]]) < 0 })
	rewardIndex := make(map[string]uint32, len(rewardTokens))
	for i, token := range rewardTokens {
		rewardIndex[token] = uint32(i)
	}
	for i := range outputs {
		outputs[i].RewardIndex = rewardIndex[outputs[i].Token]
	}
	if len(params.Payload) == 0 {
		if _, err := parseBatchSeq(params.BatchSeq); err != nil {
			return nil, err
		}
	}

	methodID := params.MethodID
	if len(methodID) == 0 {
		methodID = defaultEVMMethodID()
	}
	if len(methodID) > 4 {
		methodID = append([]byte(nil), methodID[:4]...)
	} else {
		methodID = append([]byte(nil), methodID...)
	}

	template := &EVMTemplate{
		ChainID:           a.chainID,
		AuthorityProtocol: params.AuthorityProtocol, DexChainID: params.DexChainID, GuardianPubkey: append([]byte(nil), guardian...),
		VaultID:      params.VaultID,
		KeyEpoch:     params.KeyEpoch,
		ContractAddr: contractAddr,
		MethodID:     methodID,
		Payload:      append([]byte(nil), params.Payload...),
		WithdrawIDs:  append([]string(nil), params.WithdrawIDs...),
		Outputs:      outputs,
		Nonce:        params.Nonce,
		BatchSeq:     params.BatchSeq,
		RewardTokens: rewardTokens,
	}

	templateHash := template.TemplateHash()
	signingHash, err := template.SigningHash()
	if err != nil {
		return nil, err
	}
	templateData, err := template.ToJSON()
	if err != nil {
		return nil, err
	}

	return &chain.TemplateResult{
		TemplateHash: templateHash,
		TemplateData: templateData,
		SigHashes:    [][]byte{signingHash},
	}, nil
}

                                  
func (a *EVMAdapter) TemplateHash(templateData []byte) ([]byte, error) {
	template, err := FromJSON(templateData)
	if err != nil {
		return nil, ErrInvalidTemplateData
	}
	return template.TemplateHash(), nil
}

                                            
func (a *EVMAdapter) PackageSigned(templateData []byte, signatures, guardianSignatures [][]byte) (*chain.SignedPackage, error) {
	t, err := FromJSON(templateData)
	if err != nil || t.AuthorityProtocol != 3 || !t.isSolidityWithdrawCall() || t.ChainID != a.chainID {
		return nil, ErrInvalidTemplateData
	}
	if len(signatures) != 1 || len(guardianSignatures) != 1 {
		return nil, ErrInvalidSignature
	}
	op, err := parseBN128Signature(signatures[0], true)
	if err != nil {
		return nil, err
	}
	guardian, err := parseBN128Signature(guardianSignatures[0], true)
	if err != nil {
		return nil, err
	}
	message, err := t.WithdrawMessage()
	if err != nil {
		return nil, err
	}
	raw := abiEncodeDualWithdrawBatchCall(t.MethodID, message, op, guardian)
	return &chain.SignedPackage{TemplateHash: t.TemplateHash(), TemplateData: templateData, Signatures: signatures, GuardianSignatures: guardianSignatures, RawTx: raw}, nil
}

                                                                                
func (a *EVMAdapter) PackageSignedWithHandover(data, signature, guardianSignature, migrationData, migrationSignature, migrationGuardianSignature []byte) (*chain.SignedPackage, error) {
	t, err := FromJSON(data)
	if err != nil || !t.isSolidityWithdrawCall() {
		return nil, ErrInvalidTemplateData
	}
	m, err := FromJSON(migrationData)
	if err != nil || !m.isHandoverProofV2() {
		return nil, ErrInvalidTemplateData
	}
	if t.ChainID != m.ChainID || t.VaultID != m.VaultID || t.ContractAddr != m.ContractAddr || t.KeyEpoch != m.Nonce || !bytes.Equal(t.GuardianPubkey, m.GuardianPubkey) {
		return nil, ErrInvalidTemplateData
	}
	if _, err = m.HandoverProofMessage(); err != nil {
		return nil, err
	}
	return a.PackageSignedWithHandovers(data, signature, guardianSignature, []SignedHandover{{FromEpoch: m.KeyEpoch, ToEpoch: m.Nonce, NewGroupPubkey: append([]byte(nil), m.Payload[:64]...), FrozenNextBatchSeq: new(big.Int).SetBytes(m.Payload[96:128]).String(), Signature: migrationSignature, GuardianSignature: migrationGuardianSignature}})
}

func abiEncodeDualWithdrawBatchCall(method, batch []byte, signature, guardian *bn128Signature) []byte {
	tuple := batch[32:]
	out := append([]byte(nil), method...)
	out = append(out, abiUintWord(big.NewInt(224))...)
	for _, s := range []*bn128Signature{signature, guardian} {
		out = append(out, abiUintWord(s.Rx)...)
		out = append(out, abiUintWord(s.Ry)...)
		out = append(out, abiUintWord(s.S)...)
	}
	return append(out, tuple...)
}

                               
func (a *EVMAdapter) VerifySignature(groupPubkey []byte, msg []byte, signature []byte) (valid bool, err error) {
	if !isSupportedSignatureSize(len(signature)) {
		return false, ErrInvalidSignature
	}
	if len(groupPubkey) != 64 {
		return false, ErrInvalidPublicKey
	}
	if len(msg) != 32 {
		return false, ErrInvalidMessage
	}

	verifyPubkey, err := CanonicalBN128PublicKey(groupPubkey)
	if err != nil {
		return false, err
	}

	if _, err := parseBN128Signature(signature, false); err != nil {
		return false, ErrInvalidSignature
	}

	defer func() {
		if r := recover(); r != nil {
			valid = false
			err = fmt.Errorf("signature verification panic: %v", r)
		}
	}()
	return frostcore.Verify(pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128, verifyPubkey, msg, signature)
}

                                                                                                        
func SigningHashFromTemplateData(templateData []byte) ([]byte, error) {
	template, err := FromJSON(templateData)
	if err != nil {
		return nil, ErrInvalidTemplateData
	}
	return template.SigningHash()
}

func padLeft(data []byte, size int) []byte {
	if len(data) >= size {
		return data[len(data)-size:]
	}
	result := make([]byte, size)
	copy(result[size-len(data):], data)
	return result
}

func normalizeHexString(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if len(s)%2 == 1 {
		s = "0" + s
	}
	return s
}

var maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

                                                                          
                                                                              
func ParseBatchSeq(raw string) (*big.Int, error) { return parseBatchSeq(raw) }

func parseBatchSeq(raw string) (*big.Int, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || (len(raw) > 1 && raw[0] == '0') {
		return nil, ErrInvalidTemplateData
	}
	v, ok := new(big.Int).SetString(raw, 10)
	if !ok || v.Sign() <= 0 || v.Cmp(maxUint256) >= 0 {
		return nil, ErrInvalidTemplateData
	}
	return v, nil
}

                                                                     
func CalculateBroadcastFee(gross *big.Int) *big.Int {
	if gross == nil || gross.Sign() <= 0 {
		return big.NewInt(0)
	}
	return new(big.Int).Quo(new(big.Int).Add(new(big.Int).Set(gross), big.NewInt(9999)), big.NewInt(10000))
}

func canonicalAddressBytes(raw string) (string, []byte, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "0x0000000000000000000000000000000000000000"
	}
	canonical, err := NormalizeAddress(raw, true)
	if err != nil {
		return "", nil, err
	}
	b, err := hex.DecodeString(strings.TrimPrefix(canonical, "0x"))
	if err != nil || len(b) != 20 {
		return "", nil, ErrInvalidAddress
	}
	return canonical, b, nil
}

func (t *EVMTemplate) isSolidityWithdrawCall() bool {
	if t == nil || len(t.Payload) != 0 || len(t.MethodID) != 4 {
		return false
	}
	return bytes.Equal(t.MethodID, defaultEVMMethodID())
}

func (t *EVMTemplate) isHandoverProof() bool { return t.isHandoverProofV2() }

func keccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

func abiAddressWord(raw string) ([]byte, error) {
	if strings.TrimSpace(raw) == "" {
		return make([]byte, evmWordSize), nil
	}
	canonical, err := NormalizeAddress(raw, true)
	if err != nil {
		return nil, ErrInvalidAddress
	}
	addr, err := hex.DecodeString(strings.TrimPrefix(canonical, "0x"))
	if err != nil {
		return nil, ErrInvalidAddress
	}
	word := make([]byte, evmWordSize)
	copy(word[evmWordSize-len(addr):], addr)
	return word, nil
}

func requestIDWord(raw string) ([]byte, error) {
	s := strings.TrimSpace(raw)
	if s == "" || s != raw {
		return nil, ErrInvalidOutput
	}
	return keccak256([]byte(s)), nil
}

                                                                       
                                                                            
                                                       
func NormalizeAddress(raw string, allowZero bool) (string, error) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return "", ErrInvalidAddress
	}
	hexPart := s[2:]
	if len(hexPart) != 40 {
		return "", ErrInvalidAddress
	}
	decoded, err := hex.DecodeString(hexPart)
	if err != nil {
		return "", ErrInvalidAddress
	}
	allZero := true
	for _, b := range decoded {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero && !allowZero {
		return "", ErrInvalidAddress
	}

	lower := strings.ToLower(hexPart)
	upper := strings.ToUpper(hexPart)
	canonical := checksumAddress(decoded)
	if hexPart != lower && hexPart != upper && s != canonical {
		return "", ErrInvalidAddress
	}
	return canonical, nil
}

func checksumAddress(address []byte) string {
	lower := hex.EncodeToString(address)
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write([]byte(lower))
	digest := h.Sum(nil)
	out := []byte(lower)
	for i := range out {
		if out[i] < 'a' || out[i] > 'f' {
			continue
		}
		nibble := digest[i/2] >> 4
		if i%2 == 1 {
			nibble = digest[i/2] & 0x0f
		}
		if nibble >= 8 {
			out[i] -= 'a' - 'A'
		}
	}
	return "0x" + string(out)
}

func abiUintWord(v *big.Int) []byte {
	word := make([]byte, evmWordSize)
	if v != nil {
		v.FillBytes(word)
	}
	return word
}

type bn128Signature struct {
	Rx *big.Int
	Ry *big.Int
	S  *big.Int
}

func parseBN128Signature(signature []byte, requireY bool) (*bn128Signature, error) {
	if !isSupportedSignatureSize(len(signature)) || (requireY && len(signature) != SchnorrSigWithYSize) {
		return nil, ErrInvalidSignature
	}
	grp := curve.NewBN256Group()
	sig := &bn128Signature{Rx: new(big.Int).SetBytes(signature[:32])}
	if sig.Rx.Cmp(grp.Modulus()) >= 0 {
		return nil, ErrInvalidSignature
	}
	switch len(signature) {
	case SchnorrSigSize:
		sig.Ry = recoverBN128YForAdapter(grp, sig.Rx)
		sig.S = new(big.Int).SetBytes(signature[32:])
	case SchnorrSigWithYSize:
		sig.Ry = new(big.Int).SetBytes(signature[32:64])
		sig.S = new(big.Int).SetBytes(signature[64:])
	}
	if sig.Ry == nil || sig.Ry.Cmp(grp.Modulus()) >= 0 || !isBN128PointOnCurve(grp, sig.Rx, sig.Ry) {
		return nil, ErrInvalidSignature
	}
	if sig.S.Sign() == 0 || sig.S.Cmp(grp.Order()) >= 0 {
		return nil, ErrInvalidSignature
	}
	return sig, nil
}

func isSupportedSignatureSize(size int) bool {
	return size == SchnorrSigSize || size == SchnorrSigWithYSize
}

func isBN128PointOnCurve(grp *curve.BN256Group, x, y *big.Int) bool {
	if grp == nil || x == nil || y == nil {
		return false
	}
	p := grp.Modulus()
	if x.Sign() < 0 || y.Sign() < 0 || x.Cmp(p) >= 0 || y.Cmp(p) >= 0 || (x.Sign() == 0 && y.Sign() == 0) {
		return false
	}
	y2 := new(big.Int).Mul(y, y)
	y2.Mod(y2, p)
	x3 := new(big.Int).Mul(x, x)
	x3.Mod(x3, p)
	x3.Mul(x3, x)
	x3.Mod(x3, p)
	x3.Add(x3, big.NewInt(3))
	x3.Mod(x3, p)
	return y2.Cmp(x3) == 0
}

func defaultEVMMethodID() []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(defaultEVMWithdrawMethodSignature))
	sum := h.Sum(nil)
	out := make([]byte, 4)
	copy(out, sum[:4])
	return out
}

func recoverBN128YForAdapter(grp *curve.BN256Group, x *big.Int) *big.Int {
	p := grp.Modulus()
	x3 := new(big.Int).Mul(x, x)
	x3.Mod(x3, p)
	x3.Mul(x3, x)
	x3.Mod(x3, p)
	y2 := new(big.Int).Add(x3, big.NewInt(3))
	y2.Mod(y2, p)
	exp := new(big.Int).Add(p, big.NewInt(1))
	exp.Rsh(exp, 2)
	y := new(big.Int).Exp(y2, exp, p)
	check := new(big.Int).Mul(y, y)
	check.Mod(check, p)
	if check.Cmp(y2) != 0 {
		return nil
	}
	return y
}

func canonicalBN128PubkeyForAdapter(grp *curve.BN256Group, point curve.Point) []byte {
	p := grp.Modulus()
	y := new(big.Int).Set(point.Y)
	halfP := new(big.Int).Rsh(new(big.Int).Set(p), 1)
	if y.Cmp(halfP) > 0 {
		y.Sub(p, y)
	}
	out := make([]byte, 64)
	point.X.FillBytes(out[:32])
	y.FillBytes(out[32:])
	return out
}

                                                                           
                                                                           
                                                                              
func CanonicalBN128PublicKey(raw []byte) ([]byte, error) {
	if len(raw) != 64 {
		return nil, ErrInvalidPublicKey
	}
	grp := curve.NewBN256Group()
	p := curve.Point{X: new(big.Int).SetBytes(raw[:32]), Y: new(big.Int).SetBytes(raw[32:])}
	if !isBN128PointOnCurve(grp, p.X, p.Y) {
		return nil, ErrInvalidPublicKey
	}
	return canonicalBN128PubkeyForAdapter(grp, p), nil
}
