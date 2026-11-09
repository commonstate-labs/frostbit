package txpool

import (
	"dex/config"
	"dex/formal/itftrace"
	"dex/formal/refinement"
	"dex/pb"
	"dex/utils"
	"errors"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

type formalAdmissionHarness struct {
	pool         *TxPool
	identity     *utils.TestIdentity
	txs          map[int64]*pb.AnyTx
	accepted     int64
	rejected     int64
	saveFailures int64
}

func TestFormalAccountAdmissionITFDifferential(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want int
	}{
		{name: "nonce-gap-conflict-finalize", env: "DEX_FORMAL_ITF_TXPOOL_WINDOW", want: 7},
		{name: "fee-reservation-release", env: "DEX_FORMAL_ITF_TXPOOL_FEE", want: 7},
		{name: "local-persistence-failure", env: "DEX_FORMAL_ITF_TXPOOL_LOCAL_FAILURE", want: 3},
		{name: "restart-revalidation", env: "DEX_FORMAL_ITF_TXPOOL_RESTART", want: 3},
	}
	if os.Getenv(cases[0].env) == "" {
		t.Skip("TxPool ITF paths are set by formal/run-go-differential.ps1")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFormalAccountAdmissionTrace(t, os.Getenv(tc.env), tc.want)
		})
	}
}

func runFormalAccountAdmissionTrace(t *testing.T, path string, wantStates int) {
	t.Helper()
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatalf("load Quint TxPool ITF trace: %v", err)
	}
	if len(trace.States) != wantStates {
		t.Fatalf("TxPool differential trace states=%d want=%d", len(trace.States), wantStates)
	}
	initial, err := refinement.TxPoolAdmissionFromITF(trace.States[0])
	if err != nil {
		t.Fatal(err)
	}
	h := newFormalAdmissionHarness(t, initial)
	h.assertStateRelation(t, trace.States[0], 0)
	for i := 1; i < len(trace.States); i++ {
		before, err := refinement.TxPoolAdmissionFromITF(trace.States[i-1])
		if err != nil {
			t.Fatal(err)
		}
		after, err := refinement.TxPoolAdmissionFromITF(trace.States[i])
		if err != nil {
			t.Fatal(err)
		}
		h.applyModelStep(t, before, after)
		h.assertStateRelation(t, trace.States[i], i)
	}
}

func newFormalAdmissionHarness(t *testing.T, initial refinement.TxPoolAdmissionState) *formalAdmissionHarness {
	t.Helper()
	identity := utils.GenerateTestIdentity(46100)
	localAddress := ""
	if initial.Phase == "LocalStart" {
		localAddress = identity.Address
	}
	pool := newAdmissionPool(t, func(c *config.Config) {
		c.TxPool.MaxFutureNonceGap = 3
		c.TxPool.MaxPendingPerAccount = 8
		c.TxPool.MaxPendingAccountBytes = 1 << 20
		c.TxPool.MaxPendingBytes = 1 << 21
	}, localAddress)
	setAdmissionAccount(t, pool, identity.Address, uint64(initial.FinalizedNonce), intString(initial.Balance))
	txs := make(map[int64]*pb.AnyTx, 4)
	for nonce := int64(0); nonce <= 3; nonce++ {
		txs[nonce] = admissionTransfer(t, identity, uint64(nonce), "1")
	}
	h := &formalAdmissionHarness{pool: pool, identity: identity, txs: txs}
	if initial.Phase == "RestartSeed" {
		for _, nonce := range initial.Persisted {
			if err := pool.dbManager.SavePendingAnyTx(txs[nonce]); err != nil {
				t.Fatal(err)
			}
		}
		if err := pool.dbManager.ForceFlush(); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func (h *formalAdmissionHarness) applyModelStep(t *testing.T, before, after refinement.TxPoolAdmissionState) {
	t.Helper()
	switch {
	case before.Phase == "RestartSeed" && after.Phase == "Restarted":
		restored, err := NewTxPoolWithConfig(h.pool.dbManager, realAdmissionValidator{}, "", h.pool.Logger, h.pool.cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = restored.Stop() })
		h.pool = restored
		h.accepted, h.rejected = after.Accepted, after.Rejected

	case after.SaveFailures == before.SaveFailures+1:
		h.pool.validator = localStopDuringValidation{pool: h.pool}
		h.pool.pendingSaveQueue = nil
		accepted, err := h.pool.SubmitLocalTx(h.txs[0], nil)
		                                                                        
		                                                      
		h.pool.stopChan = make(chan struct{})
		if !accepted || err == nil || !strings.Contains(err.Error(), "stopping") {
			t.Fatalf("modeled admitted/save-failed result=%v/%v", accepted, err)
		}
		h.accepted++
		h.saveFailures++

	case after.FinalizedNonce > before.FinalizedNonce:
		setAdmissionAccount(t, h.pool, h.identity.Address, uint64(after.FinalizedNonce), intString(after.Balance))
		h.pool.maintainPending(time.Now())

	case after.Rejected == before.Rejected+1:
		nonce := int64(1)
		if before.Phase == "FeeFull" {
			nonce = 2
		}
		conflict := admissionTransfer(t, h.identity, uint64(nonce), "2")
		if err := h.pool.StoreAnyTx(conflict); !errors.Is(err, ErrPendingAdmission) {
			t.Fatalf("modeled rejection nonce=%d err=%v", nonce, err)
		}
		h.rejected++

	case after.Accepted == before.Accepted+1:
		nonce, ok := addedNonce(before.Pending, after.Pending)
		if !ok {
			t.Fatalf("modeled admission did not add one nonce: before=%v after=%v", before.Pending, after.Pending)
		}
		if err := h.pool.StoreAnyTx(h.txs[nonce]); err != nil {
			t.Fatalf("production admission nonce=%d: %v", nonce, err)
		}
		h.accepted++

	case len(after.Pending) == len(before.Pending)-1:
		nonce, ok := removedNonce(before.Pending, after.Pending)
		if !ok {
			t.Fatalf("modeled removal did not remove one nonce: before=%v after=%v", before.Pending, after.Pending)
		}
		h.pool.RemoveAnyTx(h.txs[nonce].GetTxId())

	case reflect.DeepEqual(before, after):
		                                                                  

	default:
		t.Fatalf("unsupported TxPool model edge: before=%+v after=%+v", before, after)
	}
	if err := h.pool.dbManager.ForceFlush(); err != nil {
		t.Fatal(err)
	}
}

func (h *formalAdmissionHarness) assertStateRelation(t *testing.T, model itftrace.State, step int) {
	t.Helper()
	expected, err := refinement.TxPoolAdmissionFromITF(model)
	if err != nil {
		t.Fatal(err)
	}
	actual := h.productionState(t, expected.Phase)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("TxPool state relation failed step=%d action=%s\nactual:   %+v\nexpected: %+v",
			step, model.Action, actual, expected)
	}
}

func (h *formalAdmissionHarness) productionState(t *testing.T, phase string) refinement.TxPoolAdmissionState {
	t.Helper()
	chain, err := h.pool.readAdmissionState(h.identity.Address)
	if err != nil {
		t.Fatal(err)
	}
	status, err := h.pool.AccountPendingStatus(h.identity.Address)
	if err != nil {
		t.Fatal(err)
	}
	pending := make([]int64, 0)
	reserved := int64(0)
	h.pool.mu.RLock()
	if account := h.pool.pendingAccounts[h.identity.Address]; account != nil {
		pending = make([]int64, 0, len(account.nonces))
		for nonce := range account.nonces {
			pending = append(pending, int64(nonce))
		}
		reserved = account.fees.Int64()
	}
	h.pool.mu.RUnlock()
	sort.Slice(pending, func(i, j int) bool { return pending[i] < pending[j] })
	persistedTxs, err := h.pool.dbManager.LoadPendingAnyTx()
	if err != nil {
		t.Fatal(err)
	}
	persisted := make([]int64, 0, len(persistedTxs))
	for _, tx := range persistedTxs {
		if tx != nil && tx.GetBase() != nil && tx.GetBase().FromAddress == h.identity.Address {
			persisted = append(persisted, int64(tx.GetBase().Nonce))
		}
	}
	sort.Slice(persisted, func(i, j int) bool { return persisted[i] < persisted[j] })
	return refinement.TxPoolAdmissionState{
		Phase:          phase,
		FinalizedNonce: int64(chain.nonce),
		Balance:        chain.balance.Int64(),
		Pending:        pending,
		Persisted:      persisted,
		ReservedFees:   reserved,
		NextNonce:      int64(status.NextNonce),
		Accepted:       h.accepted,
		Rejected:       h.rejected,
		SaveFailures:   h.saveFailures,
	}
}

func addedNonce(before, after []int64) (int64, bool) {
	old := make(map[int64]struct{}, len(before))
	for _, nonce := range before {
		old[nonce] = struct{}{}
	}
	var added []int64
	for _, nonce := range after {
		if _, exists := old[nonce]; !exists {
			added = append(added, nonce)
		}
	}
	returnSingle := len(added) == 1
	if !returnSingle {
		return 0, false
	}
	return added[0], true
}

func removedNonce(before, after []int64) (int64, bool) {
	current := make(map[int64]struct{}, len(after))
	for _, nonce := range after {
		current[nonce] = struct{}{}
	}
	var removed []int64
	for _, nonce := range before {
		if _, exists := current[nonce]; !exists {
			removed = append(removed, nonce)
		}
	}
	if len(removed) != 1 {
		return 0, false
	}
	return removed[0], true
}

func intString(value int64) string {
	return strconv.FormatInt(value, 10)
}
