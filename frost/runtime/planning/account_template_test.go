package planning

import (
	"math/big"
	"testing"

	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

func canonicalAccountTemplateFixture(t *testing.T, state *pb.FrostWithdrawState, vaultID uint32) []byte {
	t.Helper()
	gross, ok := new(big.Int).SetString(state.Amount, 10)
	if !ok || gross.Sign() <= 0 {
		t.Fatalf("invalid fixture gross %q", state.Amount)
	}
	fee := new(big.Int).Quo(new(big.Int).Add(new(big.Int).Set(gross), big.NewInt(9999)), big.NewInt(10000))
	state.VaultId = vaultID
	state.LiveSeq = state.Seq
	state.GrossAmount = gross.String()
	state.FeeAmount = fee.String()
	state.NetAmount = new(big.Int).Sub(gross, fee).String()
	state.FeeVersion = 1
	data, err := proto.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func accountTemplateToken(t *testing.T, address, nativeChain, nativeContract string) []byte {
	t.Helper()
	data, err := proto.Marshal(&pb.Token{Address: address, Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: nativeChain, NativeContract: nativeContract})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type testStateReader struct {
	kvs        map[string][]byte
	getCalls   int
	batchCalls int
	batchKeys  int
}

func (r *testStateReader) Get(key string) ([]byte, bool, error) {
	r.getCalls++
	v, ok := r.kvs[key]
	return v, ok, nil
}

func (r *testStateReader) Scan(prefix string, fn func(k string, v []byte) bool) error {
	for k, v := range r.kvs {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			if !fn(k, v) {
				break
			}
		}
	}
	return nil
}

func (r *testStateReader) GetMany(keys []string) (map[string][]byte, error) {
	r.batchCalls++
	r.batchKeys += len(keys)
	out := make(map[string][]byte, len(keys))
	for _, k := range keys {
		if v, ok := r.kvs[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func TestBuildAccountTemplateParams_DeterministicFields(t *testing.T) {
	w1 := &pb.FrostWithdrawState{
		WithdrawId: "w1",
		Seq:        1,
		Chain:      "eth",
		Asset:      "native",
		To:         "0x1111111111111111111111111111111111111111",
		Amount:     "10",
		Status:     "QUEUED",
	}
	w2 := &pb.FrostWithdrawState{
		WithdrawId: "w2",
		Seq:        2,
		Chain:      "eth",
		Asset:      "native",
		To:         "0x2222222222222222222222222222222222222222",
		Amount:     "20",
		Status:     "QUEUED",
	}
	cfg := &pb.FrostVaultConfig{AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: accountPlanningGuardianKey(),
		Chain:     "eth",
		VaultRefs: []string{"0x00000000000000000000000000000000000000aa"},
	}
	w1Data := canonicalAccountTemplateFixture(t, w1, 0)
	w2Data := canonicalAccountTemplateFixture(t, w2, 0)
	cfgData, _ := proto.Marshal(cfg)

	reader := &testStateReader{
		kvs: map[string][]byte{
			keys.KeyFrostWithdraw("w1"):        w1Data,
			keys.KeyFrostWithdraw("w2"):        w2Data,
			keys.KeyFrostVaultConfig("eth", 0): cfgData,
			keys.KeyToken("native"):            accountTemplateToken(t, "native", "eth", ""),
		},
	}

	params, firstSeq, err := BuildAccountTemplateParams(reader, "eth", "native", 0, 9, []string{"w2", "w1"}, nil)
	if err != nil {
		t.Fatalf("BuildAccountTemplateParams failed: %v", err)
	}

	if firstSeq != 1 {
		t.Fatalf("firstSeq mismatch: got=%d want=1", firstSeq)
	}
	if params.Nonce != 1 {
		t.Fatalf("nonce mismatch: got=%d want=1", params.Nonce)
	}
	if params.ContractAddr != "0x00000000000000000000000000000000000000aa" {
		t.Fatalf("contract addr mismatch: %s", params.ContractAddr)
	}
	if len(params.MethodID) != 4 {
		t.Fatalf("method id length mismatch: got=%d want=4", len(params.MethodID))
	}
	if len(params.WithdrawIDs) != 2 || params.WithdrawIDs[0] != "w1" || params.WithdrawIDs[1] != "w2" {
		t.Fatalf("withdraw order mismatch: %+v", params.WithdrawIDs)
	}
	if len(params.Outputs) != 2 || params.Outputs[0].Amount != "10" || params.Outputs[1].Amount != "20" {
		t.Fatalf("outputs mismatch: %+v", params.Outputs)
	}
}

func TestBuildAccountTemplateParams_RejectsAmountOverrideThatBreaksCanonicalFee(t *testing.T) {
	w := &pb.FrostWithdrawState{
		WithdrawId: "w1",
		Seq:        7,
		Chain:      "eth",
		Asset:      "native",
		To:         "0x1111111111111111111111111111111111111111",
		Amount:     "100",
		Status:     "QUEUED",
	}
	cfg := &pb.FrostVaultConfig{AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: accountPlanningGuardianKey(),
		Chain:     "eth",
		VaultRefs: []string{"0x00000000000000000000000000000000000000aa"},
	}
	wData := canonicalAccountTemplateFixture(t, w, 0)
	cfgData, _ := proto.Marshal(cfg)

	reader := &testStateReader{
		kvs: map[string][]byte{
			keys.KeyFrostWithdraw("w1"):        wData,
			keys.KeyFrostVaultConfig("eth", 0): cfgData,
			keys.KeyToken("native"):            accountTemplateToken(t, "native", "eth", ""),
		},
	}

	if _, _, err := BuildAccountTemplateParams(reader, "eth", "native", 0, 1, []string{"w1"}, map[string]string{"w1": "55"}); err == nil {
		t.Fatal("non-canonical amount override accepted")
	}
}

func TestBuildAccountTemplateParams_PreservesUint256Amount(t *testing.T) {
	const amount = "115792089237316195423570985008687907853269984665640564039457584007913129639935"
	w := &pb.FrostWithdrawState{
		WithdrawId: "w1", Seq: 1, Chain: "eth", Asset: "native",
		To: "0x1111111111111111111111111111111111111111", Amount: amount, Status: "QUEUED",
	}
	cfg := &pb.FrostVaultConfig{AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: accountPlanningGuardianKey(),
		Chain: "eth", VaultRefs: []string{"0x00000000000000000000000000000000000000aa"},
	}
	wData := canonicalAccountTemplateFixture(t, w, 0)
	cfgData, _ := proto.Marshal(cfg)
	reader := &testStateReader{kvs: map[string][]byte{
		keys.KeyFrostWithdraw("w1"):        wData,
		keys.KeyFrostVaultConfig("eth", 0): cfgData,
		keys.KeyToken("native"):            accountTemplateToken(t, "native", "eth", ""),
	}}

	params, _, err := BuildAccountTemplateParams(reader, "eth", "native", 0, 1, []string{"w1"}, nil)
	if err != nil {
		t.Fatalf("BuildAccountTemplateParams failed: %v", err)
	}
	if len(params.Outputs) != 1 || params.Outputs[0].Amount != amount {
		t.Fatalf("uint256 amount was not preserved: %+v", params.Outputs)
	}
}

func TestBuildAccountTemplateParams_RejectsUint256Overflow(t *testing.T) {
	w := &pb.FrostWithdrawState{
		WithdrawId: "w1", Seq: 1, Chain: "eth", Asset: "native",
		To:     "0x1111111111111111111111111111111111111111",
		Amount: "115792089237316195423570985008687907853269984665640564039457584007913129639936",
		Status: "QUEUED",
	}
	wData := canonicalAccountTemplateFixture(t, w, 0)
	reader := &testStateReader{kvs: map[string][]byte{keys.KeyFrostWithdraw("w1"): wData}}
	if _, _, err := BuildAccountTemplateParams(reader, "eth", "native", 0, 1, []string{"w1"}, nil); err == nil {
		t.Fatal("uint256 overflow was accepted")
	}
}

func TestBuildAccountTemplateParams_BindsTokenContract(t *testing.T) {
	contract := "0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d"
	w := &pb.FrostWithdrawState{
		WithdrawId: "w1",
		Seq:        1,
		Chain:      "bnb",
		Asset:      "BSC.USDC",
		To:         "0x1111111111111111111111111111111111111111",
		Amount:     "100",
		Status:     "QUEUED",
	}
	cfg := &pb.FrostVaultConfig{AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: accountPlanningGuardianKey(),
		Chain:     "bnb",
		VaultRefs: []string{"0x00000000000000000000000000000000000000aa"},
	}
	token := &pb.Token{
		Address:        "BSC.USDC",
		Type:           pb.TokenType_TOKEN_BRIDGED,
		NativeChain:    "BNB",
		NativeContract: contract,
	}
	wData := canonicalAccountTemplateFixture(t, w, 0)
	cfgData, _ := proto.Marshal(cfg)
	tokenData, _ := proto.Marshal(token)

	reader := &testStateReader{
		kvs: map[string][]byte{
			keys.KeyFrostWithdraw("w1"):        wData,
			keys.KeyFrostVaultConfig("bnb", 0): cfgData,
			keys.KeyToken("BSC.USDC"):          tokenData,
		},
	}

	params, _, err := BuildAccountTemplateParams(reader, "BSC", "BSC.USDC", 0, 1, []string{"w1"}, nil)
	if err != nil {
		t.Fatalf("BuildAccountTemplateParams failed: %v", err)
	}
	if len(params.Outputs) != 1 || params.Outputs[0].Token != contract {
		t.Fatalf("token contract not bound into output: %+v", params.Outputs)
	}
}

func TestBuildAccountTemplateParamsReauthorizesQueuedRequestAtCurrentVaultEpoch(t *testing.T) {
	w := &pb.FrostWithdrawState{
		WithdrawId: "queued-before-rotation", Seq: 1, Chain: "eth", Asset: "native",
		To: "0x1111111111111111111111111111111111111111", Amount: "100", Status: "QUEUED", KeyEpoch: 1,
	}
	cfgData, _ := proto.Marshal(&pb.FrostVaultConfig{AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: accountPlanningGuardianKey(), Chain: "eth", VaultRefs: []string{"0x00000000000000000000000000000000000000aa"}})
	reader := &testStateReader{kvs: map[string][]byte{
		keys.KeyFrostWithdraw(w.WithdrawId): canonicalAccountTemplateFixture(t, w, 0),
		keys.KeyFrostVaultConfig("eth", 0):  cfgData,
		keys.KeyToken("native"):             accountTemplateToken(t, "native", "eth", ""),
	}}
	params, _, err := BuildAccountTemplateParams(reader, "eth", "", 0, 2, []string{w.WithdrawId}, nil)
	if err != nil {
		t.Fatalf("queued request was not reauthorized by current epoch: %v", err)
	}
	if params.KeyEpoch != 2 {
		t.Fatalf("template epoch=%d want current epoch 2", params.KeyEpoch)
	}
}

func accountPlanningGuardianKey() []byte {
	g := curve.NewBN256Group()
	key, _ := evm.CanonicalBN128PublicKey(g.SerializePoint(g.ScalarBaseMult(big.NewInt(29))))
	return key
}
