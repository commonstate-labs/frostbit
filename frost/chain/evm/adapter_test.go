package evm

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"testing"

	"dex/frost/chain"
	"dex/frost/core/curve"

	"golang.org/x/crypto/sha3"
)

                                       

func TestAdapterIdentity(t *testing.T) {
	cases := []struct {
		name   string
		build  func() *EVMAdapter
		chain  string
		chain_ uint64
	}{
		{"ETH", NewETHAdapter, chain.ChainETH, 1},
		{"BNB", NewBNBAdapter, chain.ChainBNB, 56},
		{"BASE", NewBaseAdapter, chain.ChainBASE, 8453},
		{"ARB", NewARBAdapter, chain.ChainARB, 42161},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := c.build()
			if a.Chain() != c.chain {
				t.Fatalf("Chain()=%s want %s", a.Chain(), c.chain)
			}
			if a.chainID != c.chain_ {
				t.Fatalf("chainID=%d want %d", a.chainID, c.chain_)
			}
			if a.SignAlgo() != chain.SignAlgoETH {
				t.Fatalf("SignAlgo=%v want SignAlgoETH", a.SignAlgo())
			}
		})
	}
}

func TestAdapterNormalizesChainName(t *testing.T) {
	a := NewEVMAdapter("  ETH  ", 1)
	if a.Chain() != "eth" {
		t.Fatalf("Chain()=%q want %q", a.Chain(), "eth")
	}
}

                                 

func TestDefaultMethodIDMatchesKeccak(t *testing.T) {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(defaultEVMWithdrawMethodSignature))
	want := h.Sum(nil)[:4]
	got := defaultEVMMethodID()
	if !bytes.Equal(got, want) {
		t.Fatalf("methodID=%x want %x", got, want)
	}
}

func TestBuildWithdrawTemplateDefaultsMethodIDWhenEmpty(t *testing.T) {
	a := NewETHAdapter()
	res, err := a.BuildWithdrawTemplate(baseParams())
	if err != nil {
		t.Fatalf("BuildWithdrawTemplate err=%v", err)
	}
	tpl, _ := FromJSON(res.TemplateData)
	if !bytes.Equal(tpl.MethodID, defaultEVMMethodID()) {
		t.Fatalf("methodID defaulting missing")
	}
}

func TestBuildWithdrawTemplateRejectsUnknownMethod(t *testing.T) {
	p := baseParams()
	p.MethodID = []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	if _, err := NewETHAdapter().BuildWithdrawTemplate(p); err == nil {
		t.Fatal("unknown method accepted")
	}
}

                                                           

func TestBuildWithdrawTemplateRejectsEmptyContractAddr(t *testing.T) {
	a := NewETHAdapter()
	p := baseParams()
	p.ContractAddr = "   "
	if _, err := a.BuildWithdrawTemplate(p); err != ErrInvalidContractAddress {
		t.Fatalf("err=%v want ErrInvalidContractAddress", err)
	}
}

func TestNormalizeAddressStrict(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		want      string
		wantError bool
	}{
		{
			name: "lowercase",
			raw:  "0xde709f2102306220921060314715629080e2fb77",
			want: "0xde709f2102306220921060314715629080e2fb77",
		},
		{
			name: "valid EIP-55 checksum",
			raw:  "0x52908400098527886E0F7030069857D2E4169EE7",
			want: "0x52908400098527886E0F7030069857D2E4169EE7",
		},
		{
			name: "uppercase with uppercase prefix",
			raw:  "0X52908400098527886E0F7030069857D2E4169EE7",
			want: "0x52908400098527886E0F7030069857D2E4169EE7",
		},
		{name: "missing prefix", raw: "52908400098527886E0F7030069857D2E4169EE7", wantError: true},
		{name: "short", raw: "0x1234", wantError: true},
		{name: "long", raw: "0x" + strings.Repeat("1", 41), wantError: true},
		{name: "non-hex", raw: "0x" + strings.Repeat("g", 40), wantError: true},
		{name: "bad mixed-case checksum", raw: "0x52908400098527886E0F7030069857D2E4169Ee7", wantError: true},
		{name: "zero address", raw: "0x" + strings.Repeat("0", 40), wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeAddress(tt.raw, false)
			if tt.wantError {
				if err != ErrInvalidAddress {
					t.Fatalf("NormalizeAddress(%q) err=%v want ErrInvalidAddress", tt.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeAddress(%q) err=%v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeAddress(%q)=%q want %q", tt.raw, got, tt.want)
			}
		})
	}

	zero := "0x" + strings.Repeat("0", 40)
	if got, err := NormalizeAddress(zero, true); err != nil || got != zero {
		t.Fatalf("allowZero NormalizeAddress()=(%q,%v) want (%q,nil)", got, err, zero)
	}
}

func TestBuildWithdrawTemplateRejectsInvalidAddresses(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*chain.WithdrawTemplateParams)
		wantErr error
	}{
		{
			name:    "contract missing prefix",
			mutate:  func(p *chain.WithdrawTemplateParams) { p.ContractAddr = strings.Repeat("1", 40) },
			wantErr: ErrInvalidContractAddress,
		},
		{
			name:    "zero contract",
			mutate:  func(p *chain.WithdrawTemplateParams) { p.ContractAddr = "0x" + strings.Repeat("0", 40) },
			wantErr: ErrInvalidContractAddress,
		},
		{
			name:    "destination bad checksum",
			mutate:  func(p *chain.WithdrawTemplateParams) { p.Outputs[0].To = "0x52908400098527886E0F7030069857D2E4169Ee7" },
			wantErr: ErrInvalidAddress,
		},
		{
			name:    "zero destination",
			mutate:  func(p *chain.WithdrawTemplateParams) { p.Outputs[0].To = "0x" + strings.Repeat("0", 40) },
			wantErr: ErrInvalidAddress,
		},
		{
			name:    "invalid token contract",
			mutate:  func(p *chain.WithdrawTemplateParams) { p.Outputs[0].Token = "0x1234" },
			wantErr: ErrInvalidAddress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := baseParams()
			tt.mutate(&p)
			if _, err := NewETHAdapter().BuildWithdrawTemplate(p); err != tt.wantErr {
				t.Fatalf("BuildWithdrawTemplate err=%v want %v", err, tt.wantErr)
			}
		})
	}
}

func TestBuildWithdrawTemplatePreservesUint256Boundaries(t *testing.T) {
	amounts := []string{
		"9223372036854775808",                                                            
		"18446744073709551616",                                         
		"115792089237316195423570985008687907853269984665640564039457584007913129639935",              
	}

	for _, amount := range amounts {
		t.Run(amount, func(t *testing.T) {
			p := baseParams()
			p.Outputs[0].Amount = amount
			result, err := NewETHAdapter().BuildWithdrawTemplate(p)
			if err != nil {
				t.Fatalf("BuildWithdrawTemplate amount=%s err=%v", amount, err)
			}
			tpl, err := FromJSON(result.TemplateData)
			if err != nil {
				t.Fatalf("FromJSON amount=%s err=%v", amount, err)
			}
			if len(tpl.Outputs) != 1 || tpl.Outputs[0].GrossAmount != amount {
				t.Fatalf("amount changed during template build: got %+v want %s", tpl.Outputs, amount)
			}

			message, err := tpl.WithdrawMessage()
			if err != nil {
				t.Fatalf("WithdrawMessage amount=%s err=%v", amount, err)
			}
			amountInt, ok := new(big.Int).SetString(amount, 10)
			if !ok {
				t.Fatalf("invalid test amount %s", amount)
			}
			wantWord := make([]byte, evmWordSize)
			amountInt.FillBytes(wantWord)
			if !bytes.Equal(message[14*evmWordSize:15*evmWordSize], wantWord) {
				t.Fatalf("ABI amount word mismatch\n got: %x\nwant: %x", message[14*evmWordSize:15*evmWordSize], wantWord)
			}
		})
	}
}

func TestBuildWithdrawTemplateRejectsAmountAboveUint256(t *testing.T) {
	p := baseParams()
	p.Outputs[0].Amount = "115792089237316195423570985008687907853269984665640564039457584007913129639936"          
	if _, err := NewETHAdapter().BuildWithdrawTemplate(p); err != ErrInvalidAmount {
		t.Fatalf("BuildWithdrawTemplate err=%v want ErrInvalidAmount", err)
	}
}

func TestBuildWithdrawTemplateAcceptsMultipleWithdrawOutputs(t *testing.T) {
	p := baseParams()
	p.WithdrawIDs = append(p.WithdrawIDs, "w_0002")
	p.Outputs = append(p.Outputs, chain.WithdrawOutput{
		WithdrawID:   "w_0002",
		To:           "0x00000000000000000000000000000000000000cc",
		Amount:       "1",
		BroadcastFee: "1",
	})
	                                    
	p.Outputs[1].Amount = "2"
	if _, err := NewETHAdapter().BuildWithdrawTemplate(p); err != nil {
		t.Fatalf("BuildWithdrawTemplate err=%v", err)
	}
}

                                                           

func TestTemplateHashDeterministic(t *testing.T) {
	a := NewETHAdapter()
	r1, _ := a.BuildWithdrawTemplate(baseParams())
	r2, _ := a.BuildWithdrawTemplate(baseParams())
	if !bytes.Equal(r1.TemplateHash, r2.TemplateHash) {
		t.Fatalf("non-deterministic hash")
	}
	tpl, _ := FromJSON(r1.TemplateData)
	signingHash, err := tpl.SigningHash()
	if err != nil {
		t.Fatalf("SigningHash err=%v", err)
	}
	if len(r1.SigHashes) != 1 || !bytes.Equal(r1.SigHashes[0], signingHash) {
		t.Fatalf("SigHashes must be [SigningHash]")
	}
}

func TestBuildWithdrawTemplateSignsSolidityWithdrawMessage(t *testing.T) {
	r, err := NewETHAdapter().BuildWithdrawTemplate(baseParams())
	if err != nil {
		t.Fatalf("BuildWithdrawTemplate err=%v", err)
	}
	tpl, err := FromJSON(r.TemplateData)
	if err != nil {
		t.Fatalf("FromJSON err=%v", err)
	}
	msg, err := tpl.WithdrawMessage()
	if err != nil {
		t.Fatalf("WithdrawMessage err=%v", err)
	}
	wantHash := keccak256(msg)
	if !bytes.Equal(r.SigHashes[0], wantHash) {
		t.Fatalf("signing hash mismatch\n got: %x\nwant: %x", r.SigHashes[0], wantHash)
	}
	if len(msg) != 16*evmWordSize || !bytes.Equal(msg[32:64], keccak256([]byte("DEX_VAULT_MIXED_WITHDRAW_BATCH_V1"))) ||
		new(big.Int).SetBytes(msg[160:192]).Cmp(big.NewInt(1)) != 0 || new(big.Int).SetBytes(msg[192:224]).Cmp(big.NewInt(7*evmWordSize)) != 0 {
		t.Fatalf("withdraw message mixed-batch head mismatch: %x", msg)
	}
}

                                                                                                             
func TestTemplateHashChangesWithETH_vs_BNB(t *testing.T) {
	ethHash, _ := NewETHAdapter().BuildWithdrawTemplate(baseParams())
	bnbParams := baseParams()
	bnbParams.Chain = chain.ChainBNB
	bnbHash, err := NewBNBAdapter().BuildWithdrawTemplate(bnbParams)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(ethHash.TemplateHash, bnbHash.TemplateHash) {
		t.Fatalf("ETH and BNB template hash must differ (chainID binding)")
	}
}

                                                                           
func TestTemplateHashSensitivity(t *testing.T) {
	base, _ := NewETHAdapter().BuildWithdrawTemplate(baseParams())

	mut := []struct {
		name string
		f    func(p *chain.WithdrawTemplateParams)
	}{
		{"VaultID", func(p *chain.WithdrawTemplateParams) { p.VaultID = 99 }},
		{"KeyEpoch", func(p *chain.WithdrawTemplateParams) { p.KeyEpoch = 99 }},
		{"ContractAddr", func(p *chain.WithdrawTemplateParams) { p.ContractAddr = "0x00000000000000000000000000000000000000cc" }},
		{"GuardianPubkey", func(p *chain.WithdrawTemplateParams) { p.GuardianPubkey = formalMultigenerationPub(31) }},
		{"Nonce", func(p *chain.WithdrawTemplateParams) { p.Nonce = 77 }},
		{"Output.To", func(p *chain.WithdrawTemplateParams) { p.Outputs[0].To = "0x0000000000000000000000000000000000000099" }},
		{"Output.Amount", func(p *chain.WithdrawTemplateParams) { p.Outputs[0].Amount = "4242" }},
		{"BatchSeq", func(p *chain.WithdrawTemplateParams) { p.BatchSeq = "2" }},
		{"Output.Token", func(p *chain.WithdrawTemplateParams) {
			p.Outputs[0].Token = "0x00000000000000000000000000000000000000cc"
		}},
		{"WithdrawID", func(p *chain.WithdrawTemplateParams) {
			p.WithdrawIDs[0] = "altered"
			p.Outputs[0].WithdrawID = "altered"
		}},
	}
	for _, m := range mut {
		t.Run(m.name, func(t *testing.T) {
			p := baseParams()
			m.f(&p)
			alt, err := NewETHAdapter().BuildWithdrawTemplate(p)
			if err != nil {
				t.Fatalf("BuildWithdrawTemplate err=%v", err)
			}
			if bytes.Equal(alt.TemplateHash, base.TemplateHash) {
				t.Fatalf("hash must change when %s changes", m.name)
			}
		})
	}
}

func TestWithdrawBatchBounds63_64_65(t *testing.T) {
	for _, count := range []int{63, 64, 65} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			p := baseParams()
			p.WithdrawIDs = nil
			p.Outputs = nil
			for i := 0; i < count; i++ {
				id := fmt.Sprintf("withdraw-%03d", i)
				p.WithdrawIDs = append(p.WithdrawIDs, id)
				p.Outputs = append(p.Outputs, chain.WithdrawOutput{WithdrawID: id, To: fmt.Sprintf("0x%040x", i+1), Amount: "100", BroadcastFee: "1"})
			}
			_, err := NewETHAdapter().BuildWithdrawTemplate(p)
			if count <= MaxWithdrawalsPerBatch && err != nil {
				t.Fatalf("count=%d rejected: %v", count, err)
			}
			if count > MaxWithdrawalsPerBatch && err != ErrInvalidOutput {
				t.Fatalf("count=%d err=%v want ErrInvalidOutput", count, err)
			}
		})
	}
}

func TestBuildWithdrawTemplateBindsOutputToken(t *testing.T) {
	p := baseParams()
	p.Outputs[0].Token = "0x00000000000000000000000000000000000000cc"
	res, err := NewETHAdapter().BuildWithdrawTemplate(p)
	if err != nil {
		t.Fatalf("BuildWithdrawTemplate err=%v", err)
	}
	tpl, err := FromJSON(res.TemplateData)
	if err != nil {
		t.Fatalf("FromJSON err=%v", err)
	}
	wantToken, err := NormalizeAddress(p.Outputs[0].Token, false)
	if err != nil {
		t.Fatalf("NormalizeAddress token err=%v", err)
	}
	if len(tpl.Outputs) != 1 || tpl.Outputs[0].Token != wantToken {
		t.Fatalf("output token not preserved: %+v", tpl.Outputs)
	}
}

                                                                                                          
func TestTemplateHashRecomputeMatches(t *testing.T) {
	a := NewETHAdapter()
	r, _ := a.BuildWithdrawTemplate(baseParams())
	rehash, err := a.TemplateHash(r.TemplateData)
	if err != nil {
		t.Fatalf("recompute err=%v", err)
	}
	if !bytes.Equal(rehash, r.TemplateHash) {
		t.Fatalf("rehash mismatch\n have %x\n want %x", rehash, r.TemplateHash)
	}
}

func TestTemplateHashRejectsInvalidJSON(t *testing.T) {
	if _, err := NewETHAdapter().TemplateHash([]byte("{not json")); err != ErrInvalidTemplateData {
		t.Fatalf("err=%v want ErrInvalidTemplateData", err)
	}
}

                                                  

                                                                                   
func TestSerializeAddrCaseInsensitive(t *testing.T) {
	p1 := baseParams()
	p2 := baseParams()
	p2.ContractAddr = "0X" + strings.ToUpper(strings.TrimPrefix(p1.ContractAddr, "0x"))
	p2.Outputs[0].To = "0X" + strings.ToUpper(strings.TrimPrefix(p1.Outputs[0].To, "0x"))

	r1, _ := NewETHAdapter().BuildWithdrawTemplate(p1)
	r2, _ := NewETHAdapter().BuildWithdrawTemplate(p2)
	if !bytes.Equal(r1.TemplateHash, r2.TemplateHash) {
		t.Fatalf("hash must be case/prefix-insensitive for hex addrs")
	}
}

                                                                                                            
func TestSerializeLayout(t *testing.T) {
	tpl := &EVMTemplate{AuthorityProtocol: 3, GuardianPubkey: adapterGuardianPubkeyForTest(), DexChainID: 51025,
		ChainID:      1,
		VaultID:      7,
		KeyEpoch:     3,
		ContractAddr: "0x" + strings.Repeat("aa", 20),
		MethodID:     []byte{0x11, 0x22, 0x33, 0x44},
		Payload:      []byte{0xde, 0xad},
		WithdrawIDs:  []string{"w1"},
		Outputs: []EVMOutput{
			{WithdrawID: "w1", To: "0x" + strings.Repeat("bb", 20), GrossAmount: "1000", Token: "0x" + strings.Repeat("00", 20), RewardIndex: 0},
		},
		Nonce: 42, BatchSeq: "1", RewardTokens: []string{"0x" + strings.Repeat("00", 20)},
	}

	var want bytes.Buffer
	binary.Write(&want, binary.BigEndian, tpl.ChainID)
	binary.Write(&want, binary.BigEndian, tpl.VaultID)
	binary.Write(&want, binary.BigEndian, tpl.KeyEpoch)
	addr, _ := hex.DecodeString(strings.Repeat("aa", 20))
	want.Write(addr)
	want.Write(tpl.MethodID)
	want.Write(tpl.Payload)
	binary.Write(&want, binary.BigEndian, tpl.Nonce)
	want.Write(abiUintWord(big.NewInt(1)))
	binary.Write(&want, binary.BigEndian, uint32(1))
	want.Write(make([]byte, 20))
	binary.Write(&want, binary.BigEndian, uint32(1))               
	to, _ := hex.DecodeString(strings.Repeat("bb", 20))
	want.Write(to)
	amount := make([]byte, 32)
	amount[31] = 0xe8
	amount[30] = 0x03
	want.Write(amount)
	want.Write(make([]byte, 20))                        
	binary.Write(&want, binary.BigEndian, uint32(0))
	binary.Write(&want, binary.BigEndian, uint32(1))
	binary.Write(&want, binary.BigEndian, uint32(2))
	want.WriteString("w1")
	want.Write(tpl.GuardianPubkey)
	binary.Write(&want, binary.BigEndian, tpl.AuthorityProtocol)
	binary.Write(&want, binary.BigEndian, tpl.DexChainID)

	got := tpl.Serialize()
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("Serialize layout drift\n got: %x\nwant: %x", got, want.Bytes())
	}
}

                                      

func TestPackageSignedHappyPath(t *testing.T) {
	a := NewETHAdapter()
	r, _ := a.BuildWithdrawTemplate(baseParams())
	sig := validBN128SigWithYForAdapterTest(t)

	pkg, err := a.PackageSigned(r.TemplateData, [][]byte{sig}, [][]byte{validBN128SigWithYForAdapterTest(t)})
	if err != nil {
		t.Fatalf("PackageSigned err=%v", err)
	}
	if !bytes.Equal(pkg.TemplateHash, r.TemplateHash) {
		t.Fatalf("TemplateHash mismatch")
	}
	if !bytes.Equal(pkg.TemplateData, r.TemplateData) {
		t.Fatalf("TemplateData must be passed through")
	}
	if len(pkg.Signatures) != 1 || !bytes.Equal(pkg.Signatures[0], sig) {
		t.Fatalf("Signatures mismatch")
	}
	tpl, _ := FromJSON(r.TemplateData)
	msg, err := tpl.WithdrawMessage()
	if err != nil {
		t.Fatalf("WithdrawMessage err=%v", err)
	}
	parsed, err := parseBN128Signature(sig, true)
	if err != nil {
		t.Fatalf("parse signature err=%v", err)
	}
	guardianParsed, _ := parseBN128Signature(pkg.GuardianSignatures[0], true)
	want := abiEncodeDualWithdrawBatchCall(tpl.MethodID, msg, parsed, guardianParsed)
	if !bytes.Equal(pkg.RawTx, want) {
		t.Fatalf("RawTx layout drift\n got: %x\nwant: %x", pkg.RawTx, want)
	}
	if len(pkg.RawTx) != 4+22*32 {
		t.Fatalf("calldata length=%d want=%d", len(pkg.RawTx), 4+22*32)
	}
}

func TestPackageSignedRejectsCompressedWithdrawSignature(t *testing.T) {
	a := NewETHAdapter()
	r, _ := a.BuildWithdrawTemplate(baseParams())
	_, sig := signBN128ForAdapterTest(t, hash32ForTest([]byte("compressed sig")))
	if _, err := a.PackageSigned(r.TemplateData, [][]byte{sig}, [][]byte{validBN128SigWithYForAdapterTest(t)}); err != ErrInvalidSignature {
		t.Fatalf("err=%v want ErrInvalidSignature", err)
	}
}

func TestPackageSignedRejectsWrongSigCount(t *testing.T) {
	a := NewETHAdapter()
	r, _ := a.BuildWithdrawTemplate(baseParams())
	sig := bytes.Repeat([]byte{0x01}, SchnorrSigSize)

	if _, err := a.PackageSigned(r.TemplateData, nil, [][]byte{validBN128SigWithYForAdapterTest(t)}); err == nil {
		t.Fatal("want error for 0 signatures")
	}
	if _, err := a.PackageSigned(r.TemplateData, [][]byte{sig, sig}, [][]byte{validBN128SigWithYForAdapterTest(t)}); err == nil {
		t.Fatal("want error for 2 signatures")
	}
}

func TestPackageSignedRejectsWrongSigSize(t *testing.T) {
	a := NewETHAdapter()
	r, _ := a.BuildWithdrawTemplate(baseParams())

	if _, err := a.PackageSigned(r.TemplateData, [][]byte{bytes.Repeat([]byte{1}, 63)}, [][]byte{validBN128SigWithYForAdapterTest(t)}); err != ErrInvalidSignature {
		t.Fatalf("err=%v want ErrInvalidSignature", err)
	}
	if _, err := a.PackageSigned(r.TemplateData, [][]byte{bytes.Repeat([]byte{1}, 65)}, [][]byte{validBN128SigWithYForAdapterTest(t)}); err != ErrInvalidSignature {
		t.Fatalf("err=%v want ErrInvalidSignature", err)
	}
}

func TestPackageSignedRejectsInvalidTemplateData(t *testing.T) {
	a := NewETHAdapter()
	sig := bytes.Repeat([]byte{1}, SchnorrSigSize)
	if _, err := a.PackageSigned([]byte("{bad"), [][]byte{sig}, [][]byte{validBN128SigWithYForAdapterTest(t)}); err != ErrInvalidTemplateData {
		t.Fatalf("err=%v want ErrInvalidTemplateData", err)
	}
}

                                        

func TestVerifySignatureBN128HappyPath(t *testing.T) {
	a := NewETHAdapter()
	msg := hash32ForTest([]byte("evm adapter bn128 verify"))
	pub, sig := signBN128ForAdapterTest(t, msg)

	valid, err := a.VerifySignature(pub, msg, sig)
	if err != nil {
		t.Fatalf("VerifySignature err=%v", err)
	}
	if !valid {
		t.Fatal("expected signature to be valid")
	}
}

func TestVerifySignatureBN128AcceptsSignatureWithY(t *testing.T) {
	a := NewETHAdapter()
	msg := hash32ForTest([]byte("evm adapter bn128 verify with y"))
	pub, sig := signBN128WithYForAdapterTest(t, msg)

	valid, err := a.VerifySignature(pub, msg, sig)
	if err != nil {
		t.Fatalf("VerifySignature err=%v", err)
	}
	if !valid {
		t.Fatal("expected signature with R.y to be valid")
	}
}

func TestVerifySignatureBN128RejectsWrongMessage(t *testing.T) {
	a := NewETHAdapter()
	msg := hash32ForTest([]byte("evm adapter bn128 verify"))
	pub, sig := signBN128ForAdapterTest(t, msg)

	valid, err := a.VerifySignature(pub, hash32ForTest([]byte("wrong message")), sig)
	if err != nil {
		t.Fatalf("VerifySignature err=%v", err)
	}
	if valid {
		t.Fatal("expected signature to be invalid for wrong message")
	}
}

func TestVerifySignatureBN128CanonicalizesNonCanonicalPubkey(t *testing.T) {
	a := NewETHAdapter()
	grp := curve.NewBN256Group()
	priv, pub := nonCanonicalBN128KeyForTest(t, grp)
	effectivePriv := new(big.Int).Neg(priv)
	effectivePriv.Mod(effectivePriv, grp.Order())
	msg := hash32ForTest([]byte("evm adapter canonical bn128 verify"))
	sig := signBN128WithSecretForAdapterTest(t, effectivePriv, msg)

	valid, err := a.VerifySignature(pub, msg, sig)
	if err != nil {
		t.Fatalf("VerifySignature err=%v", err)
	}
	if !valid {
		t.Fatal("expected signature to be valid after pubkey canonicalization")
	}
}

func TestVerifySignatureBN128RejectsInvalidInputs(t *testing.T) {
	a := NewETHAdapter()
	msg := hash32ForTest([]byte("evm adapter bn128 verify"))
	pub, sig := signBN128ForAdapterTest(t, msg)
	grp := curve.NewBN256Group()

	if _, err := a.VerifySignature(pub, []byte("short"), sig); err != ErrInvalidMessage {
		t.Fatalf("short msg err=%v want ErrInvalidMessage", err)
	}
	if _, err := a.VerifySignature(make([]byte, 64), msg, sig); err != ErrInvalidPublicKey {
		t.Fatalf("zero pubkey err=%v want ErrInvalidPublicKey", err)
	}
	if _, err := a.VerifySignature(pub[:63], msg, sig); err != ErrInvalidPublicKey {
		t.Fatalf("short pubkey err=%v want ErrInvalidPublicKey", err)
	}
	if _, err := a.VerifySignature(pub, msg, sig[:63]); err != ErrInvalidSignature {
		t.Fatalf("short sig err=%v want ErrInvalidSignature", err)
	}

	badS := append([]byte(nil), sig...)
	grp.Order().FillBytes(badS[32:])
	if _, err := a.VerifySignature(pub, msg, badS); err != ErrInvalidSignature {
		t.Fatalf("out-of-range s err=%v want ErrInvalidSignature", err)
	}

	badR := append([]byte(nil), sig...)
	grp.Modulus().FillBytes(badR[:32])
	if _, err := a.VerifySignature(pub, msg, badR); err != ErrInvalidSignature {
		t.Fatalf("out-of-range R.x err=%v want ErrInvalidSignature", err)
	}
}

                                        

func TestJSONRoundTripPreservesHash(t *testing.T) {
	a := NewETHAdapter()
	r, _ := a.BuildWithdrawTemplate(baseParams())

	tpl, err := FromJSON(r.TemplateData)
	if err != nil {
		t.Fatalf("FromJSON err=%v", err)
	}
	if !bytes.Equal(tpl.TemplateHash(), r.TemplateHash) {
		t.Fatalf("round-trip hash mismatch")
	}
	                                           
	js, err := tpl.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON err=%v", err)
	}
	var any map[string]interface{}
	if err := json.Unmarshal(js, &any); err != nil {
		t.Fatalf("ToJSON produced invalid JSON: %v", err)
	}
}

                                

func baseParams() chain.WithdrawTemplateParams {
	return chain.WithdrawTemplateParams{
		AuthorityProtocol: 3, GuardianPubkey: adapterGuardianPubkeyForTest(), DexChainID: 51025,
		Chain:        chain.ChainETH,
		Asset:        "USDT",
		VaultID:      0,
		KeyEpoch:     1,
		BatchSeq:     "1",
		WithdrawIDs:  []string{"w_0001"},
		ContractAddr: "0x00000000000000000000000000000000000000aa",
		MethodID:     nil,                   
		Payload:      nil,
		Nonce:        5,
		Outputs: []chain.WithdrawOutput{
			{WithdrawID: "w_0001", To: "0x00000000000000000000000000000000000000bb", Amount: "1000", BroadcastFee: "10"},
		},
	}
}

func signBN128ForAdapterTest(t *testing.T, msg []byte) ([]byte, []byte) {
	t.Helper()
	grp := curve.NewBN256Group()
	priv := big.NewInt(1)
	pub := grp.SerializePoint(grp.ScalarBaseMult(priv))
	return pub, signBN128WithSecretForAdapterTest(t, priv, msg)
}

func signBN128WithYForAdapterTest(t *testing.T, msg []byte) ([]byte, []byte) {
	t.Helper()
	grp := curve.NewBN256Group()
	priv := big.NewInt(1)
	pub := grp.SerializePoint(grp.ScalarBaseMult(priv))
	nonce := big.NewInt(987654321)
	P := grp.ScalarBaseMult(priv)
	R := grp.ScalarBaseMult(nonce)
	e := bn128ChallengeForAdapterTest(R.X, R.Y, P.X, P.Y, msg, grp.Order())
	s := new(big.Int).Mul(e, priv)
	s.Add(s, nonce)
	s.Mod(s, grp.Order())
	sig := make([]byte, SchnorrSigWithYSize)
	R.X.FillBytes(sig[:32])
	R.Y.FillBytes(sig[32:64])
	s.FillBytes(sig[64:])
	return pub, sig
}

func validBN128SigWithYForAdapterTest(t *testing.T) []byte {
	t.Helper()
	_, sig := signBN128WithYForAdapterTest(t, hash32ForTest([]byte("package signed")))
	return sig
}

func signBN128WithSecretForAdapterTest(t *testing.T, priv *big.Int, msg []byte) []byte {
	t.Helper()
	grp := curve.NewBN256Group()
	nonce := big.NewInt(987654321)
	P := grp.ScalarBaseMult(priv)
	R := grp.ScalarBaseMult(nonce)
	e := bn128ChallengeForAdapterTest(R.X, R.Y, P.X, P.Y, msg, grp.Order())
	s := new(big.Int).Mul(e, priv)
	s.Add(s, nonce)
	s.Mod(s, grp.Order())

	sig := make([]byte, 64)
	R.X.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return sig
}

func bn128ChallengeForAdapterTest(Rx, Ry, Px, Py *big.Int, msg []byte, order *big.Int) *big.Int {
	h := sha3.NewLegacyKeccak256()
	writePadded := func(n *big.Int) {
		out := make([]byte, 32)
		n.FillBytes(out)
		h.Write(out)
	}
	writePadded(Rx)
	writePadded(Ry)
	writePadded(Px)
	writePadded(Py)
	h.Write(msg)
	e := new(big.Int).SetBytes(h.Sum(nil))
	return e.Mod(e, order)
}

func hash32ForTest(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

func nonCanonicalBN128KeyForTest(t *testing.T, grp *curve.BN256Group) (*big.Int, []byte) {
	t.Helper()
	halfP := new(big.Int).Rsh(grp.Modulus(), 1)
	for i := int64(1); i < 10000; i++ {
		priv := big.NewInt(i)
		P := grp.ScalarBaseMult(priv)
		if P.Y.Cmp(halfP) > 0 {
			return priv, grp.SerializePoint(P)
		}
	}
	t.Fatal("failed to find non-canonical BN128 key")
	return nil, nil
}
