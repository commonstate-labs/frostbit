package evm

import (
	"bytes"
	"dex/frost/chain"
	"dex/pb"
	"fmt"
	"math/big"
)

const MaxHandoversPerCall = 8
const AuthorityHandoverDomainV2 = "DEX_VAULT_AUTHORITY_HANDOVER_V3"
const withdrawWithHandoversSignature = "withdrawBatchWithHandovers((bytes32,uint256,address,uint256,uint256,address[],(bytes32,address,address,uint256,uint256)[]),(uint256,uint256,uint256),(uint256,uint256,uint256),(uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256,uint256)[])"

type MigrationV2Params struct {
	Chain               string
	ChainID, DexChainID uint64
	ContractAddr        string
	VaultID             uint32
	OldEpoch, NewEpoch  uint64
	NewGroupPubkey      []byte
	GuardianPubkey      []byte
	FrozenNextBatchSeq  string
}

type SignedHandover struct {
	FromEpoch, ToEpoch uint64
	NewGroupPubkey     []byte
	FrozenNextBatchSeq string
	Signature          []byte
	GuardianSignature  []byte
}

                                                                             
                                                                                 
func BuildMigrationTemplateV2(p MigrationV2Params) (*chain.TemplateResult, *EVMTemplate, error) {
	if p.ChainID == 0 || p.DexChainID == 0 || p.OldEpoch == 0 || p.OldEpoch == ^uint64(0) || p.NewEpoch != p.OldEpoch+1 {
		return nil, nil, ErrInvalidTemplateData
	}
	pub, err := CanonicalBN128PublicKey(p.NewGroupPubkey)
	if err != nil {
		return nil, nil, err
	}
	guardian, err := CanonicalBN128PublicKey(p.GuardianPubkey)
	if err != nil || len(p.GuardianPubkey) != 64 || bytes.Equal(pub, guardian) {
		return nil, nil, ErrInvalidPublicKey
	}
	cutoff, err := ParseBatchSeq(p.FrozenNextBatchSeq)
	if err != nil {
		return nil, nil, err
	}
	payload := append([]byte(nil), pub...)
	payload = append(payload, abiUintWord(new(big.Int).SetUint64(p.NewEpoch))...)
	payload = append(payload, abiUintWord(cutoff)...)
	payload = append(payload, abiUintWord(new(big.Int).SetUint64(p.DexChainID))...)
	result, err := NewEVMAdapter(p.Chain, p.ChainID).BuildWithdrawTemplate(chain.WithdrawTemplateParams{
		Chain: chain.NormalizeChain(p.Chain), VaultID: p.VaultID, KeyEpoch: p.OldEpoch, ContractAddr: p.ContractAddr,
		MethodID: handoverProofMethodIDV2(), Payload: payload, Nonce: p.NewEpoch, AuthorityProtocol: 3, DexChainID: p.DexChainID, GuardianPubkey: p.GuardianPubkey,
	})
	if err != nil {
		return nil, nil, err
	}
	template, err := FromJSON(result.TemplateData)
	return result, template, err
}
func handoverProofMethodIDV2() []byte { return keccak256([]byte(AuthorityHandoverDomainV2))[:4] }
func WithdrawBatchWithHandoversMethodID() []byte {
	return keccak256([]byte(withdrawWithHandoversSignature))[:4]
}
func (t *EVMTemplate) isHandoverProofV2() bool {
	return t != nil && t.AuthorityProtocol == 3 && len(t.Outputs) == 0 && len(t.Payload) == 160 && bytes.Equal(t.MethodID, handoverProofMethodIDV2())
}
func (t *EVMTemplate) handoverProofMessageV2() ([]byte, error) {
	if !t.isHandoverProofV2() || t.ChainID == 0 || t.KeyEpoch == 0 || t.KeyEpoch == ^uint64(0) || t.Nonce != t.KeyEpoch+1 {
		return nil, ErrInvalidTemplateData
	}
	pub, err := CanonicalBN128PublicKey(t.Payload[:64])
	if err != nil || !bytes.Equal(pub, t.Payload[:64]) || bytes.Equal(pub, t.GuardianPubkey) {
		return nil, ErrInvalidPublicKey
	}
	next := new(big.Int).SetBytes(t.Payload[64:96])
	dex := new(big.Int).SetBytes(t.Payload[128:160])
	if !next.IsUint64() || next.Uint64() != t.Nonce || !dex.IsUint64() || dex.Sign() == 0 || new(big.Int).SetBytes(t.Payload[96:128]).Sign() == 0 {
		return nil, ErrInvalidTemplateData
	}
	addr, err := abiAddressWord(t.ContractAddr)
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), keccak256([]byte(AuthorityHandoverDomainV2))...)
	out = append(out, t.Payload[128:160]...)
	out = append(out, abiUintWord(new(big.Int).SetUint64(t.ChainID))...)
	out = append(out, addr...)
	out = append(out, abiUintWord(new(big.Int).SetUint64(t.KeyEpoch))...)
	out = append(out, t.Payload[64:96]...)
	out = append(out, t.Payload[:64]...)
	out = append(out, t.Payload[96:128]...)
	return out, nil
}
func (a *EVMAdapter) PackageSignedWithHandovers(data, withdrawalSignature, guardianSignature []byte, certs []SignedHandover) (*chain.SignedPackage, error) {
	t, err := FromJSON(data)
	if err != nil || !t.isSolidityWithdrawCall() || t.ChainID != a.chainID || len(certs) > MaxHandoversPerCall {
		return nil, ErrInvalidTemplateData
	}
	sig, err := parseBN128Signature(withdrawalSignature, true)
	if err != nil {
		return nil, err
	}
	guardian, err := parseBN128Signature(guardianSignature, true)
	if err != nil {
		return nil, err
	}
	batch, err := t.WithdrawMessage()
	if err != nil {
		return nil, err
	}
	encoded := abiUintWord(new(big.Int).SetUint64(uint64(len(certs))))
	signatures := [][]byte{append([]byte(nil), withdrawalSignature...)}
	guardianSignatures := [][]byte{append([]byte(nil), guardianSignature...)}
	for i, c := range certs {
		if c.FromEpoch == 0 || c.FromEpoch == ^uint64(0) || c.ToEpoch != c.FromEpoch+1 || (i > 0 && certs[i-1].ToEpoch != c.FromEpoch) {
			return nil, ErrInvalidTemplateData
		}
		pub, e := CanonicalBN128PublicKey(c.NewGroupPubkey)
		if e != nil || !bytes.Equal(pub, c.NewGroupPubkey) || bytes.Equal(pub, t.GuardianPubkey) {
			return nil, ErrInvalidPublicKey
		}
		cutoff, e := ParseBatchSeq(c.FrozenNextBatchSeq)
		if e != nil {
			return nil, e
		}
		s, e := parseBN128Signature(c.Signature, true)
		if e != nil {
			return nil, e
		}
		g, e := parseBN128Signature(c.GuardianSignature, true)
		if e != nil {
			return nil, e
		}
		encoded = append(encoded, abiUintWord(new(big.Int).SetUint64(c.FromEpoch))...)
		encoded = append(encoded, abiUintWord(new(big.Int).SetUint64(c.ToEpoch))...)
		encoded = append(encoded, pub...)
		encoded = append(encoded, abiUintWord(cutoff)...)
		encoded = append(encoded, abiUintWord(s.Rx)...)
		encoded = append(encoded, abiUintWord(s.Ry)...)
		encoded = append(encoded, abiUintWord(s.S)...)
		encoded = append(encoded, abiUintWord(g.Rx)...)
		encoded = append(encoded, abiUintWord(g.Ry)...)
		encoded = append(encoded, abiUintWord(g.S)...)
		signatures = append(signatures, append([]byte(nil), c.Signature...))
		guardianSignatures = append(guardianSignatures, append([]byte(nil), c.GuardianSignature...))
	}
	if len(certs) > 0 && certs[len(certs)-1].ToEpoch != t.KeyEpoch {
		return nil, ErrInvalidTemplateData
	}
	tuple := batch[32:]
	raw := append([]byte(nil), WithdrawBatchWithHandoversMethodID()...)
	raw = append(raw, abiUintWord(big.NewInt(256))...)
	raw = append(raw, abiUintWord(sig.Rx)...)
	raw = append(raw, abiUintWord(sig.Ry)...)
	raw = append(raw, abiUintWord(sig.S)...)
	raw = append(raw, abiUintWord(guardian.Rx)...)
	raw = append(raw, abiUintWord(guardian.Ry)...)
	raw = append(raw, abiUintWord(guardian.S)...)
	raw = append(raw, abiUintWord(big.NewInt(int64(256+len(tuple))))...)
	raw = append(raw, tuple...)
	raw = append(raw, encoded...)
	return &chain.SignedPackage{TemplateHash: t.TemplateHash(), TemplateData: data, Signatures: signatures, GuardianSignatures: guardianSignatures, RawTx: raw}, nil
}

                                                                            
                                                                              
                                                                               
func DecodeSignedHandovers(raw []byte) ([]SignedHandover, []byte, []byte, error) {
	bad := func() ([]SignedHandover, []byte, []byte, error) {
		return nil, nil, nil, fmt.Errorf("invalid dual-authority handover calldata")
	}
	if len(raw) < 4+288 || !bytes.Equal(raw[:4], WithdrawBatchWithHandoversMethodID()) {
		return bad()
	}
	words := raw[4:]
	offset := new(big.Int).SetBytes(words[224:256])
	if new(big.Int).SetBytes(words[:32]).Cmp(big.NewInt(256)) != 0 || !offset.IsInt64() || offset.Int64() < 256 || offset.Int64() > int64(len(words)-32) || offset.Int64()%32 != 0 {
		return bad()
	}
	pos := int(offset.Int64())
	n := new(big.Int).SetBytes(words[pos : pos+32])
	if !n.IsUint64() || n.Uint64() > MaxHandoversPerCall || len(words) != pos+32+int(n.Uint64())*352 {
		return bad()
	}
	sig := append([]byte(nil), words[32:128]...)
	guardian := append([]byte(nil), words[128:224]...)
	if _, err := parseBN128Signature(sig, true); err != nil {
		return bad()
	}
	if _, err := parseBN128Signature(guardian, true); err != nil {
		return bad()
	}
	certs := make([]SignedHandover, int(n.Uint64()))
	for i := range certs {
		b := words[pos+32+i*352 : pos+32+(i+1)*352]
		from, to := new(big.Int).SetBytes(b[:32]), new(big.Int).SetBytes(b[32:64])
		if !from.IsUint64() || !to.IsUint64() || from.Sign() == 0 || from.Uint64() == ^uint64(0) || to.Uint64() != from.Uint64()+1 || (i > 0 && certs[i-1].ToEpoch != from.Uint64()) {
			return bad()
		}
		pub, e := CanonicalBN128PublicKey(b[64:128])
		if e != nil || !bytes.Equal(pub, b[64:128]) || new(big.Int).SetBytes(b[128:160]).Sign() == 0 {
			return bad()
		}
		op := append([]byte(nil), b[160:256]...)
		g := append([]byte(nil), b[256:352]...)
		if _, e = parseBN128Signature(op, true); e != nil {
			return bad()
		}
		if _, e = parseBN128Signature(g, true); e != nil {
			return bad()
		}
		certs[i] = SignedHandover{FromEpoch: from.Uint64(), ToEpoch: to.Uint64(), NewGroupPubkey: pub, FrozenNextBatchSeq: new(big.Int).SetBytes(b[128:160]).String(), Signature: op, GuardianSignature: g}
	}
	return certs, sig, guardian, nil
}

                                                                              
func CanonicalMigrationTemplate(cfg *pb.FrostVaultConfig, m *pb.FrostVaultMigrationState) (*chain.TemplateResult, *EVMTemplate, error) {
	if cfg == nil || m == nil || int(m.VaultId) >= len(cfg.VaultRefs) || cfg.AuthorityProtocol != 3 || m.AuthorityProtocol != 3 {
		return nil, nil, ErrInvalidTemplateData
	}
	old, err := CanonicalBN128PublicKey(m.OldGroupPubkey)
	if err != nil || bytes.Equal(old, cfg.GuardianPubkey) {
		return nil, nil, ErrInvalidPublicKey
	}
	return BuildMigrationTemplateV2(MigrationV2Params{Chain: m.Chain, ChainID: cfg.EvmChainId, DexChainID: cfg.DepositDexChainId, ContractAddr: cfg.VaultRefs[m.VaultId], VaultID: m.VaultId, OldEpoch: m.OldEpoch, NewEpoch: m.NewEpoch, NewGroupPubkey: m.NewGroupPubkey, FrozenNextBatchSeq: m.FrozenNextBatchSeq, GuardianPubkey: cfg.GuardianPubkey})
}
