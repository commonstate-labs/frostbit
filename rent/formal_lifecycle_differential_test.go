package rent

import (
	"dex/formal/itftrace"
	"dex/formal/refinement"
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

const (
	formalRentOwner         = "fbc1qformalrent"
	formalRentExternalToken = "USDT"
)

type formalRentClaimCleaner struct {
	store memStore
}

func (c formalRentClaimCleaner) PrepareAccountSweep(owner string, height uint64) error {
	return nil
}

                                                                        
                                                                             
                                                              
func (c formalRentClaimCleaner) PrepareAccountSweepWithClaims(owner string, height uint64, claims map[string][]byte) error {
	total := new(big.Int)
	for _, raw := range claims {
		amount, err := parseAmountBytes(raw)
		if err != nil {
			return err
		}
		total.Add(total, amount)
	}
	if total.Sign() == 0 {
		return nil
	}
	current, err := formalRentBalance(c.store, owner, FeeToken)
	if err != nil {
		return err
	}
	return writeFormalRentBalance(c.store, owner, FeeToken, new(big.Int).Add(current, total))
}

func TestFormalRentLifecycleITFDifferential(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want int
	}{
		{name: "expiry-and-idempotence", env: "DEX_FORMAL_ITF_RENT_EXPIRY", want: 4},
		{name: "top-up-renews-lease", env: "DEX_FORMAL_ITF_RENT_TOPUP", want: 4},
		{name: "external-exemption", env: "DEX_FORMAL_ITF_RENT_EXEMPT", want: 3},
	}
	if os.Getenv(cases[0].env) == "" {
		t.Skip("rent ITF paths are set by formal/run-go-differential.ps1")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFormalRentTrace(t, os.Getenv(tc.env), tc.want)
		})
	}
}

func runFormalRentTrace(t *testing.T, path string, wantStates int) {
	t.Helper()
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatalf("load Quint rent ITF trace: %v", err)
	}
	if len(trace.States) != wantStates {
		t.Fatalf("rent differential trace states=%d want=%d", len(trace.States), wantStates)
	}

	initial, err := refinement.RentLifecycleFromITF(trace.States[0])
	if err != nil {
		t.Fatal(err)
	}
	store := memStore{keys.KeyAccount(formalRentOwner): []byte{1}}
	if err := writeFormalRentBalance(store, formalRentOwner, FeeToken, big.NewInt(initial.PrimaryFB)); err != nil {
		t.Fatal(err)
	}
	if initial.ClaimFB > 0 {
		if err := SetClaim(store, formalRentOwner, FeeToken, "future_margin", "formal-position", big.NewInt(initial.ClaimFB)); err != nil {
			t.Fatal(err)
		}
	}
	policy := Policy{
		Token:          FeeToken,
		ExemptMin:      big.NewInt(10),
		ExemptAssets:   []ExemptAsset{{Token: formalRentExternalToken, Minimum: big.NewInt(10)}},
		RentPerBlock:   big.NewInt(1),
		RecycleAccount: RecycleFundAccount,
	}
	runtime := NewRuntime(store,
		WithPolicy(policy),
		WithCleaner(formalRentClaimCleaner{store: store}),
	)
	sourceFB := initial.SourceFB
	assertFormalRentStateRelation(t, store, trace.States[0], sourceFB, 0)

	for i := 1; i < len(trace.States); i++ {
		before, err := refinement.RentLifecycleFromITF(trace.States[i-1])
		if err != nil {
			t.Fatal(err)
		}
		after, err := refinement.RentLifecycleFromITF(trace.States[i])
		if err != nil {
			t.Fatal(err)
		}
		if after.SourceFB < before.SourceFB {
			moved := before.SourceFB - after.SourceFB
			balance, err := formalRentBalance(store, formalRentOwner, FeeToken)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeFormalRentBalance(store, formalRentOwner, FeeToken, new(big.Int).Add(balance, big.NewInt(moved))); err != nil {
				t.Fatal(err)
			}
			sourceFB -= moved
		}
		if after.ExternalAsset != before.ExternalAsset {
			if err := writeFormalRentBalance(store, formalRentOwner, formalRentExternalToken, big.NewInt(after.ExternalAsset)); err != nil {
				t.Fatal(err)
			}
		}
		if err := runtime.ProcessBlock(uint64(after.Height), []string{formalRentOwner}); err != nil {
			t.Fatalf("production rent transition %d action=%s: %v", i, trace.States[i].Action, err)
		}
		assertFormalRentStateRelation(t, store, trace.States[i], sourceFB, i)
	}
}

func assertFormalRentStateRelation(t *testing.T, store memStore, model itftrace.State, sourceFB int64, step int) {
	t.Helper()
	expected, err := refinement.RentLifecycleFromITF(model)
	if err != nil {
		t.Fatal(err)
	}
	actual := formalRentStateFromStore(t, store, expected.Height, sourceFB)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("rent state relation failed at step=%d action=%s\nactual:   %+v\nexpected: %+v",
			step, model.Action, actual, expected)
	}
}

func formalRentStateFromStore(t *testing.T, store memStore, height, sourceFB int64) refinement.RentLifecycleState {
	t.Helper()
	primary, err := formalRentBalance(store, formalRentOwner, FeeToken)
	if err != nil {
		t.Fatal(err)
	}
	recycle, err := formalRentBalance(store, RecycleFundAccount, FeeToken)
	if err != nil {
		t.Fatal(err)
	}
	external, err := formalRentBalance(store, formalRentOwner, formalRentExternalToken)
	if err != nil {
		t.Fatal(err)
	}
	claim := new(big.Int)
	if raw, ok := store[keys.KeyAssetClaimTotal(formalRentOwner, FeeToken)]; ok {
		claim, err = parseAmountBytes(raw)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, accountPresent := store[keys.KeyAccount(formalRentOwner)]
	_, claimPresent := store[keys.KeyAssetClaim(formalRentOwner, FeeToken, "future_margin", "formal-position")]

	var rentState *RentState
	if raw, ok := store[keys.KeyRentState(formalRentOwner)]; ok {
		var parsed RentState
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("decode formal rent state: %v", err)
		}
		rentState = &parsed
	}
	expiryKeys := 0
	for key := range store {
		if strings.Contains(key, "rent_expiry_") && strings.HasSuffix(key, "_"+formalRentOwner) {
			expiryKeys++
		}
	}
	if expiryKeys > 1 {
		t.Fatalf("multiple live rent expiry indexes: %d", expiryKeys)
	}

	phase := "New"
	switch {
	case !accountPresent:
		phase = "Swept"
	case external.Cmp(big.NewInt(10)) >= 0 && rentState == nil:
		phase = "Exempt"
	case rentState != nil:
		phase = "Renting"
	}
	result := refinement.RentLifecycleState{
		Phase:          phase,
		Height:         height,
		PrimaryFB:      formalRentInt64(t, primary, "primary FB"),
		ClaimFB:        formalRentInt64(t, claim, "claim FB"),
		SourceFB:       sourceFB,
		RecycleFB:      formalRentInt64(t, recycle, "recycle FB"),
		ExternalAsset:  formalRentInt64(t, external, "external asset"),
		RentState:      rentState != nil,
		ExpiryIndex:    expiryKeys == 1,
		AccountPresent: accountPresent,
		ClaimPresent:   claimPresent,
	}
	if rentState != nil {
		result.ExpireHeight = int64(rentState.ExpireHeight)
		lease, ok := new(big.Int).SetString(rentState.NominalFB, 10)
		if !ok {
			t.Fatalf("invalid lease nominal %q", rentState.NominalFB)
		}
		result.LeaseNominal = formalRentInt64(t, lease, "lease nominal")
	}
	return result
}

func writeFormalRentBalance(store memStore, owner, token string, amount *big.Int) error {
	if amount == nil || amount.Sign() == 0 {
		store.Del(keys.KeyBalance(owner, token))
		return nil
	}
	raw, err := proto.Marshal(&pb.TokenBalanceRecord{
		Address: owner,
		Token:   token,
		Balance: &pb.TokenBalance{Balance: amount.String()},
	})
	if err != nil {
		return err
	}
	store.Set(keys.KeyBalance(owner, token), raw)
	return nil
}

func formalRentBalance(store memStore, owner, token string) (*big.Int, error) {
	raw, ok := store[keys.KeyBalance(owner, token)]
	if !ok {
		return new(big.Int), nil
	}
	var record pb.TokenBalanceRecord
	if err := proto.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	return TokenBalanceTotal(record.Balance), nil
}

func formalRentInt64(t *testing.T, value *big.Int, field string) int64 {
	t.Helper()
	if value == nil || !value.IsInt64() {
		t.Fatalf("formal rent %s is not int64: %v", field, value)
	}
	return value.Int64()
}
