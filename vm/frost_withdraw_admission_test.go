package vm

import (
	"bytes"
	"fmt"
	"math/big"
	"strings"
	"testing"

	frostchain "dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/keys"
	"dex/pb"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"google.golang.org/protobuf/proto"
)

const (
	admissionMaxUint256 = "115792089237316195423570985008687907853269984665640564039457584007913129639935"
	admissionEVMTo      = "0xde709f2102306220921060314715629080e2fb77"
	admissionEVMVault   = "0x00000000000000000000000000000000000000aa"
	admissionEVMToken   = "0x00000000000000000000000000000000000000cc"
)

type frostWithdrawAdmissionFixture struct {
	base          map[string][]byte
	sv            StateView
	chain         string
	tokenAddr     string
	user          string
	destination   string
	amount        string
	supply        string
	txID          string
	requestHeight uint64
}

func mustAdmissionProto(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal admission fixture: %v", err)
	}
	return data
}

func btcAdmissionAddressForTest(t *testing.T, params *chaincfg.Params, seed byte) string {
	t.Helper()
	priv, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{seed}, 32))
	address, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(priv.PubKey()), params)
	if err != nil {
		t.Fatalf("build %s taproot address: %v", params.Name, err)
	}
	return address.EncodeAddress()
}

func corruptBTCAddressChecksum(address string) string {
	if address == "" {
		return address
	}
	corrupted := []byte(address)
	if corrupted[len(corrupted)-1] == 'q' {
		corrupted[len(corrupted)-1] = 'p'
	} else {
		corrupted[len(corrupted)-1] = 'q'
	}
	return string(corrupted)
}

func newFrostWithdrawAdmissionFixture(
	t *testing.T,
	token *pb.Token,
	cfg *pb.FrostVaultConfig,
	destination string,
	amount string,
) *frostWithdrawAdmissionFixture {
	t.Helper()
	const (
		user          = "admission-user"
		txID          = "admission-tx"
		requestHeight = uint64(100)
	)

	chainName := frostchain.NormalizeChain(token.NativeChain)
	identity := testBridgeAsset(t, chainName, token.NativeContract)
	token.Address = identity.ID
	token.NativeChain = identity.Chain
	token.NativeContract = identity.Contract
	asset := token.Address
	token.TotalSupply = admissionMaxUint256
	base := map[string][]byte{
		keys.KeyToken(token.Address):                                mustAdmissionProto(t, token),
		keys.KeyNativeTokenIndex(identity.Chain, identity.Contract): []byte(identity.ID),
		keys.KeyBalance(user, token.Address): mustAdmissionProto(t, &pb.TokenBalanceRecord{
			Balance: &pb.TokenBalance{Balance: admissionMaxUint256},
		}),
		keys.KeyFrostWithdrawFIFOSeq(chainName, asset):      []byte("7"),
		keys.KeyFrostWithdrawFIFOHead(chainName, asset):     []byte("3"),
		keys.KeyFrostWithdrawFIFOIndex(chainName, asset, 7): []byte("existing-withdraw"),
	}
	if cfg != nil {
		base[keys.KeyFrostVaultConfig(chainName, 0)] = mustAdmissionProto(t, cfg)
	}

	return &frostWithdrawAdmissionFixture{
		base:          base,
		sv:            newMemoryStateView(base),
		chain:         chainName,
		tokenAddr:     token.Address,
		user:          user,
		destination:   destination,
		amount:        amount,
		supply:        token.TotalSupply,
		txID:          txID,
		requestHeight: requestHeight,
	}
}

func TestFrostWithdrawAdmissionRejectsChainWideVaultHandoverWithoutMutation(t *testing.T) {
	f := newEVMWithdrawAdmissionFixture(t, "10")
	f.base[keys.KeyFrostVaultState(f.chain, 0)] = mustAdmissionProto(t, &pb.FrostVaultState{
		Chain: f.chain, VaultId: 0, KeyEpoch: 1, Status: VaultStatusActive,
	})
	f.base[keys.KeyFrostVaultTransition(f.chain, 0, 2)] = mustAdmissionProto(t, &pb.VaultTransitionState{
		Chain: f.chain, VaultId: 0, EpochId: 2, DkgStatus: DKGStatusSharing, Lifecycle: VaultLifecycleDraining,
	})
	f.sv = newMemoryStateView(f.base)

	ops, receipt, err := (&FrostWithdrawRequestTxHandler{}).DryRun(f.tx(), f.sv)
	if err == nil || receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("handover withdrawal receipt=%+v err=%v", receipt, err)
	}
	if receipt.Error != "no ACTIVE Vault available for "+f.chain {
		t.Fatalf("handover error=%q", receipt.Error)
	}
	if ops != nil || len(f.sv.Diff()) != 0 {
		t.Fatalf("handover rejection mutated state: ops=%+v diff=%+v", ops, f.sv.Diff())
	}
}

func TestFrostWithdrawAdmissionUsesAnotherActiveVaultDuringHandover(t *testing.T) {
	f := newEVMWithdrawAdmissionFixture(t, "10")
	cfg := &pb.FrostVaultConfig{
		Chain: f.chain, SignAlgo: frostchain.SignAlgoETH, VaultCount: 2,
		VaultRefs: []string{admissionEVMVault, "0x00000000000000000000000000000000000000bb"}, EvmChainId: 1,
	}
	f.base[keys.KeyFrostVaultConfig(f.chain, 0)] = mustAdmissionProto(t, cfg)
	f.base[keys.KeyFrostVaultState(f.chain, 0)] = mustAdmissionProto(t, &pb.FrostVaultState{
		Chain: f.chain, VaultId: 0, KeyEpoch: 1, Status: VaultStatusActive,
	})
	f.base[keys.KeyFrostVaultState(f.chain, 1)] = mustAdmissionProto(t, &pb.FrostVaultState{
		Chain: f.chain, VaultId: 1, KeyEpoch: 4, Status: VaultStatusActive,
	})
	f.base[keys.KeyFrostVaultAvailableBalance(f.chain, f.tokenAddr, 1)] = []byte(admissionMaxUint256)
	f.base[keys.KeyFrostVaultTransition(f.chain, 0, 2)] = mustAdmissionProto(t, &pb.VaultTransitionState{
		Chain: f.chain, VaultId: 0, EpochId: 2, DkgStatus: DKGStatusSharing, Lifecycle: VaultLifecycleDraining,
	})
	f.sv = newMemoryStateView(f.base)

	assertFrostWithdrawAdmissionAccepted(t, f)
}

func TestFrostWithdrawAdmissionRoundRobinAndSameBlockReservationContention(t *testing.T) {
	f := newEVMWithdrawAdmissionFixture(t, "10")
	f.base[keys.KeyFrostVaultConfig(f.chain, 0)] = mustAdmissionProto(t, &pb.FrostVaultConfig{
		Chain: f.chain, SignAlgo: frostchain.SignAlgoETH, VaultCount: 2,
		VaultRefs: []string{admissionEVMVault, "0x00000000000000000000000000000000000000bb"}, EvmChainId: 1,
	})
	for id := uint32(0); id < 2; id++ {
		f.base[keys.KeyFrostVaultState(f.chain, id)] = mustAdmissionProto(t, &pb.FrostVaultState{
			Chain: f.chain, VaultId: id, KeyEpoch: uint64(id) + 1, Status: VaultStatusActive,
		})
		f.base[keys.KeyFrostVaultAvailableBalance(f.chain, f.tokenAddr, id)] = []byte("10")
	}
	f.sv = newMemoryStateView(f.base)

	first := assertFrostWithdrawAdmissionAccepted(t, f)
	if first.VaultID != 0 {
		t.Fatalf("first round-robin assignment=%d want 0", first.VaultID)
	}
	f.supply = new(big.Int).Sub(amountFromDecimal(t, admissionMaxUint256), big.NewInt(10)).String()
	f.txID = "admission-tx-2"
	second := assertFrostWithdrawAdmissionAccepted(t, f)
	if second.VaultID != 1 {
		t.Fatalf("second round-robin assignment=%d want 1", second.VaultID)
	}
	f.txID = "admission-tx-3"
	if ops, receipt, err := (&FrostWithdrawRequestTxHandler{}).DryRun(f.tx(), f.sv); err == nil || receipt == nil || receipt.Status != "FAILED" || ops != nil || !strings.Contains(receipt.Error, "insufficient available Vault balance") {
		t.Fatalf("same-block over-allocation succeeded: receipt=%+v err=%v ops=%+v", receipt, err, ops)
	}
}

func TestFrostWithdrawAdmissionResumesAfterHandoverCompletes(t *testing.T) {
	f := newEVMWithdrawAdmissionFixture(t, "10")
	f.base[keys.KeyFrostVaultState(f.chain, 0)] = mustAdmissionProto(t, &pb.FrostVaultState{
		Chain: f.chain, VaultId: 0, KeyEpoch: 2, Status: VaultStatusActive,
	})
	f.base[keys.KeyFrostVaultTransition(f.chain, 0, 2)] = mustAdmissionProto(t, &pb.VaultTransitionState{
		Chain: f.chain, VaultId: 0, EpochId: 2, DkgStatus: DKGStatusKeyReady,
		Lifecycle: VaultLifecycleActive, MigrationStatus: migrationStatusSigned,
	})
	f.sv = newMemoryStateView(f.base)

	assertFrostWithdrawAdmissionAccepted(t, f)
}

func newBTCWithdrawAdmissionFixture(t *testing.T, amount string) *frostWithdrawAdmissionFixture {
	t.Helper()
	return newFrostWithdrawAdmissionFixture(t, &pb.Token{
		Type:        pb.TokenType_TOKEN_BRIDGED,
		NativeChain: frostchain.ChainBTC,
	}, &pb.FrostVaultConfig{
		Chain:      frostchain.ChainBTC,
		SignAlgo:   frostchain.SignAlgoBTC,
		VaultCount: 1,
		BtcNetwork: "mainnet",
	}, btcAdmissionAddressForTest(t, &chaincfg.MainNetParams, 1), amount)
}

func newEVMWithdrawAdmissionFixture(t *testing.T, amount string) *frostWithdrawAdmissionFixture {
	t.Helper()
	fixture := newFrostWithdrawAdmissionFixture(t, &pb.Token{
		Type:           pb.TokenType_TOKEN_BRIDGED,
		NativeChain:    frostchain.ChainETH,
		NativeContract: admissionEVMToken,
	}, &pb.FrostVaultConfig{
		Chain:      frostchain.ChainETH,
		SignAlgo:   frostchain.SignAlgoETH,
		VaultCount: 1,
		VaultRefs:  []string{admissionEVMVault},
		EvmChainId: 1,
	}, admissionEVMTo, amount)
	fixture.base[keys.KeyFrostVaultState(fixture.chain, 0)] = mustAdmissionProto(t, &pb.FrostVaultState{
		Chain: fixture.chain, VaultId: 0, KeyEpoch: 1, Status: VaultStatusActive,
	})
	fixture.base[keys.KeyFrostVaultAvailableBalance(fixture.chain, fixture.tokenAddr, 0)] = []byte(admissionMaxUint256)
	fixture.sv = newMemoryStateView(fixture.base)
	return fixture
}

func (f *frostWithdrawAdmissionFixture) tx() *pb.AnyTx {
	return &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawRequestTx{
		FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{
			Base: &pb.BaseMessage{
				TxId:           f.txID,
				FromAddress:    f.user,
				ExecutedHeight: f.requestHeight,
			},
			Chain:  f.chain,
			Asset:  f.tokenAddr,
			To:     f.destination,
			Amount: f.amount,
		},
	}}
}

func assertFrostWithdrawAdmissionRejectedWithoutMutation(t *testing.T, f *frostWithdrawAdmissionFixture) {
	t.Helper()
	wantBase := make(map[string][]byte, len(f.base))
	for key, value := range f.base {
		wantBase[key] = append([]byte(nil), value...)
	}

	ops, receipt, err := (&FrostWithdrawRequestTxHandler{}).DryRun(f.tx(), f.sv)
	if err == nil {
		t.Fatal("invalid withdrawal admission unexpectedly succeeded")
	}
	if ops != nil {
		t.Fatalf("rejected admission returned write ops: %+v", ops)
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("rejected admission receipt=%+v", receipt)
	}
	if diff := f.sv.Diff(); len(diff) != 0 {
		t.Fatalf("rejected admission changed StateView: %+v", diff)
	}

	for key, want := range wantBase {
		got, exists, getErr := f.sv.Get(key)
		if getErr != nil || !exists || !bytes.Equal(got, want) {
			t.Fatalf("state changed at %s: exists=%v err=%v\n got: %x\nwant: %x", key, exists, getErr, got, want)
		}
	}

	asset := f.tokenAddr
	nextSeq := uint64(8)
	withdrawID := generateWithdrawID(f.chain, asset, nextSeq, f.requestHeight)
	for _, key := range []string{
		keys.KeyFrostWithdrawTxRef(f.txID),
		keys.KeyFrostWithdraw(withdrawID),
		keys.KeyFrostWithdrawFIFOIndex(f.chain, asset, nextSeq),
	} {
		if value, exists, getErr := f.sv.Get(key); getErr != nil || exists || len(value) != 0 {
			t.Fatalf("rejected admission created %s: exists=%v err=%v value=%x", key, exists, getErr, value)
		}
	}
}

func assertFrostWithdrawAdmissionAccepted(t *testing.T, f *frostWithdrawAdmissionFixture) *FrostWithdrawRequest {
	t.Helper()
	ops, receipt, err := (&FrostWithdrawRequestTxHandler{}).DryRun(f.tx(), f.sv)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("valid admission failed: receipt=%+v err=%v", receipt, err)
	}
	if len(ops) == 0 {
		t.Fatal("valid admission returned no write ops")
	}

	asset := f.tokenAddr
	ref, refExists, refErr := f.sv.Get(keys.KeyFrostWithdrawTxRef(f.txID))
	if refErr != nil || !refExists || len(ref) == 0 {
		t.Fatalf("admitted withdraw tx ref missing: exists=%v err=%v", refExists, refErr)
	}
	withdrawID := string(ref)
	withdrawData, exists, err := f.sv.Get(keys.KeyFrostWithdraw(withdrawID))
	if err != nil || !exists {
		t.Fatalf("admitted withdraw missing: exists=%v err=%v", exists, err)
	}
	withdraw, err := unmarshalWithdrawRequest(withdrawData)
	if err != nil {
		t.Fatalf("unmarshal admitted withdraw: %v", err)
	}
	if withdraw.Status != WithdrawStatusQueued || withdraw.Amount != f.amount {
		t.Fatalf("unexpected admitted withdraw: %+v", withdraw)
	}
	if f.chain == frostchain.ChainBTC {
		if seq, _, _ := f.sv.Get(keys.KeyFrostWithdrawFIFOSeq(f.chain, asset)); string(seq) != "8" {
			t.Fatalf("FIFO seq=%q want 8", seq)
		}
		if index, _, _ := f.sv.Get(keys.KeyFrostWithdrawFIFOIndex(f.chain, asset, 8)); string(index) != withdrawID {
			t.Fatalf("FIFO index=%q want %q", index, withdrawID)
		}
	} else {
		if withdraw.LiveSeq != 1 || withdraw.FeeVersion != 1 || withdraw.BroadcastFee != evm.CalculateBroadcastFee(amountFromDecimal(t, f.amount)).String() {
			t.Fatalf("unexpected mixed withdrawal assignment/fee: %+v", withdraw)
		}
		if index, _, _ := f.sv.Get(keys.KeyFrostWithdrawLiveIndex(f.chain, withdraw.VaultID, withdraw.LiveSeq)); string(index) != withdrawID {
			t.Fatalf("mixed FIFO index=%q want %q", index, withdrawID)
		}
		if pending, _, _ := f.sv.Get(keys.KeyFrostWithdrawLivePending(f.chain, withdraw.VaultID)); string(pending) != "1" {
			t.Fatalf("mixed pending=%q want 1", pending)
		}
	}
	if string(ref) != withdrawID {
		t.Fatalf("tx ref=%q want %q", ref, withdrawID)
	}

	amount, ok := new(big.Int).SetString(f.amount, 10)
	if !ok {
		t.Fatalf("invalid fixture amount %q", f.amount)
	}
	wantRemaining, ok := new(big.Int).SetString(f.supply, 10)
	if !ok {
		t.Fatalf("invalid fixture supply %q", f.supply)
	}
	wantRemaining.Sub(wantRemaining, amount)

	balance := GetBalance(f.sv, f.user, f.tokenAddr)
	if balance.GetBalance() != wantRemaining.String() {
		t.Fatalf("balance=%s want %s", balance.GetBalance(), wantRemaining)
	}

	tokenData, exists, err := f.sv.Get(keys.KeyToken(f.tokenAddr))
	if err != nil || !exists {
		t.Fatalf("token missing after admission: exists=%v err=%v", exists, err)
	}
	var token pb.Token
	if err := proto.Unmarshal(tokenData, &token); err != nil {
		t.Fatalf("unmarshal token: %v", err)
	}
	if token.TotalSupply != wantRemaining.String() {
		t.Fatalf("TotalSupply=%s want %s", token.TotalSupply, wantRemaining)
	}
	return withdraw
}

func amountFromDecimal(t *testing.T, raw string) *big.Int {
	t.Helper()
	value, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		t.Fatalf("invalid decimal %q", raw)
	}
	return value
}

func TestFrostWithdrawAdmissionRejectsStaticInvalidWithoutMutation(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T) *frostWithdrawAdmissionFixture
	}{
		{
			name: "BTC wrong network",
			build: func(t *testing.T) *frostWithdrawAdmissionFixture {
				f := newBTCWithdrawAdmissionFixture(t, "10000")
				f.destination = btcAdmissionAddressForTest(t, &chaincfg.TestNet3Params, 2)
				return f
			},
		},
		{
			name: "BTC bad checksum",
			build: func(t *testing.T) *frostWithdrawAdmissionFixture {
				f := newBTCWithdrawAdmissionFixture(t, "10000")
				f.destination = corruptBTCAddressChecksum(f.destination)
				return f
			},
		},
		{
			name: "BTC below minimum",
			build: func(t *testing.T) *frostWithdrawAdmissionFixture {
				return newBTCWithdrawAdmissionFixture(t, fmt.Sprintf("%d", btc.MinimumWithdrawAmountSats-1))
			},
		},
		{
			name: "BTC above max money",
			build: func(t *testing.T) *frostWithdrawAdmissionFixture {
				return newBTCWithdrawAdmissionFixture(t, fmt.Sprintf("%d", int64(btcutil.MaxSatoshi)+1))
			},
		},
		{
			name: "EVM short address",
			build: func(t *testing.T) *frostWithdrawAdmissionFixture {
				f := newEVMWithdrawAdmissionFixture(t, "1000")
				f.destination = "0x1234"
				return f
			},
		},
		{
			name: "EVM non-hex address",
			build: func(t *testing.T) *frostWithdrawAdmissionFixture {
				f := newEVMWithdrawAdmissionFixture(t, "1000")
				f.destination = "0x" + strings.Repeat("g", 40)
				return f
			},
		},
		{
			name: "EVM zero address",
			build: func(t *testing.T) *frostWithdrawAdmissionFixture {
				f := newEVMWithdrawAdmissionFixture(t, "1000")
				f.destination = "0x" + strings.Repeat("0", 40)
				return f
			},
		},
		{
			name: "EVM bad checksum",
			build: func(t *testing.T) *frostWithdrawAdmissionFixture {
				f := newEVMWithdrawAdmissionFixture(t, "1000")
				f.destination = "0x52908400098527886E0F7030069857D2E4169Ee7"
				return f
			},
		},
		{
			name: "missing vault config",
			build: func(t *testing.T) *frostWithdrawAdmissionFixture {
				f := newEVMWithdrawAdmissionFixture(t, "1000")
				delete(f.base, keys.KeyFrostVaultConfig(f.chain, 0))
				return f
			},
		},
		{
			name: "TRX missing configuration and invalid destination",
			build: func(t *testing.T) *frostWithdrawAdmissionFixture {
				return newFrostWithdrawAdmissionFixture(t, &pb.Token{
					Address: "TRX.USDT", Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: frostchain.ChainTRX,
				}, &pb.FrostVaultConfig{
					Chain: frostchain.ChainTRX, SignAlgo: frostchain.SignAlgoTRX, VaultCount: 1,
				}, "TUnsupportedUntilAdapterExists", "1000")
			},
		},
		{
			name: "invalid EVM vault ref",
			build: func(t *testing.T) *frostWithdrawAdmissionFixture {
				f := newEVMWithdrawAdmissionFixture(t, "1000")
				f.base[keys.KeyFrostVaultConfig(f.chain, 0)] = mustAdmissionProto(t, &pb.FrostVaultConfig{
					Chain: f.chain, SignAlgo: frostchain.SignAlgoETH, VaultCount: 1,
					VaultRefs: []string{"0x1234"}, EvmChainId: 1,
				})
				return f
			},
		},
		{
			name: "invalid EVM token contract",
			build: func(t *testing.T) *frostWithdrawAdmissionFixture {
				f := newEVMWithdrawAdmissionFixture(t, "1000")
				f.base[keys.KeyToken(f.tokenAddr)] = mustAdmissionProto(t, &pb.Token{
					Address: f.tokenAddr, Type: pb.TokenType_TOKEN_BRIDGED,
					NativeChain: f.chain, NativeContract: "0x1234", TotalSupply: admissionMaxUint256,
				})
				return f
			},
		},
		{
			name: "EVM above uint256",
			build: func(t *testing.T) *frostWithdrawAdmissionFixture {
				return newEVMWithdrawAdmissionFixture(t, "115792089237316195423570985008687907853269984665640564039457584007913129639936")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertFrostWithdrawAdmissionRejectedWithoutMutation(t, tt.build(t))
		})
	}
}

func TestFrostWithdrawAdmissionAcceptsBTCBoundariesWithoutDynamicResources(t *testing.T) {
	tests := []struct {
		name   string
		amount string
	}{
		{name: "minimum withdrawal", amount: fmt.Sprintf("%d", btc.MinimumWithdrawAmountSats)},
		{name: "max satoshi", amount: fmt.Sprintf("%d", int64(btcutil.MaxSatoshi))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newBTCWithdrawAdmissionFixture(t, tt.amount)
			withdraw := assertFrostWithdrawAdmissionAccepted(t, f)
			if withdraw.To != f.destination {
				t.Fatalf("BTC destination=%q want %q", withdraw.To, f.destination)
			}
			for _, key := range []string{
				keys.KeyFrostVaultState(f.chain, 0),
				keys.KeyFrostVaultAvailableBalance(f.chain, f.tokenAddr, 0),
				keys.KeyFrostBtcUtxoFIFOSeq(0),
			} {
				if value, exists, err := f.sv.Get(key); err != nil || exists || len(value) != 0 {
					t.Fatalf("admission unexpectedly required/created dynamic resource %s: exists=%v err=%v value=%x", key, exists, err, value)
				}
			}
		})
	}
}

func TestFrostWithdrawAdmissionAcceptsUint256BoundariesAndReservesGross(t *testing.T) {
	amounts := []string{
		"9223372036854775808",          
		"18446744073709551616",         
		admissionMaxUint256,
	}

	for _, amount := range amounts {
		t.Run(amount, func(t *testing.T) {
			f := newEVMWithdrawAdmissionFixture(t, amount)
			withdraw := assertFrostWithdrawAdmissionAccepted(t, f)
			wantTo, err := evm.NormalizeAddress(f.destination, false)
			if err != nil {
				t.Fatalf("normalize fixture destination: %v", err)
			}
			if withdraw.To != wantTo {
				t.Fatalf("EVM destination=%q want %q", withdraw.To, wantTo)
			}
			reserved, _, _ := f.sv.Get(keys.KeyFrostVaultReservedBalance(f.chain, f.tokenAddr, withdraw.VaultID))
			if string(reserved) != amount {
				t.Fatalf("reserved gross=%q want %s", reserved, amount)
			}
			max, _ := new(big.Int).SetString(admissionMaxUint256, 10)
			gross, _ := new(big.Int).SetString(amount, 10)
			available, _, _ := f.sv.Get(keys.KeyFrostVaultAvailableBalance(f.chain, f.tokenAddr, withdraw.VaultID))
			if string(available) != new(big.Int).Sub(max, gross).String() {
				t.Fatalf("available=%q does not conserve gross=%s", available, amount)
			}
		})
	}
}
