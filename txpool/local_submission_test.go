package txpool

import (
	"dex/config"
	"dex/interfaces"
	"dex/lifecycle"
	"dex/pb"
	"dex/utils"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLocalSubmissionPreservesAdmissionLimits(t *testing.T) {
	identity := utils.GenerateTestIdentity(45000)
	pool := newAdmissionPool(t, func(c *config.Config) { c.TxPool.MaxFutureNonceGap = 1; c.TxPool.MaxPendingPerAccount = 2 }, identity.Address)
	setAdmissionAccount(t, pool, identity.Address, 0, "10000000")
	added := 0
	first := admissionTransfer(t, identity, 0, "1")
	if ok, err := pool.SubmitLocalTx(first, func(string) { added++ }); !ok || err != nil {
		t.Fatalf("admit: %v/%v", ok, err)
	}
	if !pool.HasTransaction(first.GetTxId()) {
		t.Fatal("returned before admission")
	}
	if ok, err := pool.SubmitLocalTx(first, func(string) { added++ }); !ok || err != nil || added != 1 {
		t.Fatalf("duplicate: %v/%v callbacks=%d", ok, err, added)
	}
	if ok, err := pool.SubmitLocalTx(admissionTransfer(t, identity, 2, "1"), nil); ok || !errors.Is(err, ErrPendingAdmission) {
		t.Fatalf("future window bypass: %v/%v", ok, err)
	}
	if ok, err := pool.SubmitLocalTx(admissionTransfer(t, identity, 1, "1"), nil); !ok || err != nil {
		t.Fatal(err)
	}
	status, err := pool.AccountPendingStatus(identity.Address)
	if err != nil || !status.Full() || status.NextNonce != 2 || status.Count != 2 {
		t.Fatalf("status = %+v/%v", status, err)
	}
	pool.RemoveTx(first.GetTxId())
	status, _ = pool.AccountPendingStatus(identity.Address)
	if status.NextNonce != 0 {
		t.Fatalf("gap ignored: %+v", status)
	}
	if ok, err := pool.SubmitLocalTx(first, nil); !ok || err != nil {
		t.Fatalf("refill gap: %v/%v", ok, err)
	}
}

func TestLocalSubmissionRejectsForgedForeignUnfundedAndPaused(t *testing.T) {
	local, remote := utils.GenerateTestIdentity(45001), utils.GenerateTestIdentity(45002)
	pool := newAdmissionPool(t, nil, local.Address)
	setAdmissionAccount(t, pool, local.Address, 0, "0")
	if ok, err := pool.SubmitLocalTx(admissionTransfer(t, local, 0, "1"), nil); ok || !errors.Is(err, ErrPendingAdmission) {
		t.Fatalf("unfunded: %v/%v", ok, err)
	}
	setAdmissionAccount(t, pool, local.Address, 0, "10000000")
	if ok, _ := pool.SubmitLocalTx(admissionTransfer(t, remote, 0, "1"), nil); ok {
		t.Fatal("foreign sender accepted")
	}
	forged := admissionTransfer(t, local, 0, "1")
	forged.GetBase().Signature[0] ^= 1
	if ok, _ := pool.SubmitLocalTx(forged, nil); ok {
		t.Fatal("forged local transaction accepted")
	}
	pool.SetSyncPressureReader(stubSyncPressureReader{state: interfaces.SyncPressureState{CatchUp: true}})
	if ok, err := pool.SubmitLocalTx(admissionTransfer(t, local, 0, "1"), nil); ok || !errors.Is(err, ErrSyncCatchUpBackpressure) {
		t.Fatalf("catch-up bypass: %v/%v", ok, err)
	}
	pool.SetSyncPressureReader(nil)
	emergency := lifecycle.New()
	pool.SetEmergencyStop(emergency)
	emergency.Stop("test")
	if ok, err := pool.SubmitLocalTx(admissionTransfer(t, local, 0, "1"), nil); ok || !errors.Is(err, lifecycle.ErrEmergencyStop) {
		t.Fatalf("emergency bypass: %v/%v", ok, err)
	}
	assertAdmissionEmpty(t, pool)
}

func TestBootstrapMinerSubmissionAloneBypassesCatchUpBackpressure(t *testing.T) {
	identity := utils.GenerateTestIdentity(45006)
	pool := newAdmissionPool(t, nil, identity.Address)
	setAdmissionAccount(t, pool, identity.Address, 0, "10000000")
	pool.SetSyncPressureReader(stubSyncPressureReader{state: interfaces.SyncPressureState{CatchUp: true, Reason: "syncing"}})

	registration := signAdmissionTx(t, &pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: &pb.MinerTx{
		Base:          &pb.BaseMessage{Nonce: 0, Fee: "1000"},
		Op:            pb.OrderOp_ADD,
		RewardAddress: identity.Address,
		BlsPublicKey:  []byte("local-bls-public-key"),
		BlsProof:      []byte("local-bls-proof"),
		Endpoint:      "192.0.2.6:6000",
	}}}, identity)
	if ok, err := pool.SubmitLocalBootstrapMinerTx(registration, nil); !ok || err != nil {
		t.Fatalf("bootstrap registration rejected: %v/%v", ok, err)
	}
	if !pool.HasTransaction(registration.GetTxId()) {
		t.Fatal("bootstrap registration was not admitted")
	}

	pool.RemoveTx(registration.GetTxId())
	if ok, err := pool.SubmitLocalBootstrapMinerTx(admissionTransfer(t, identity, 0, "1"), nil); ok || err == nil {
		t.Fatalf("ordinary transfer used bootstrap escape hatch: %v/%v", ok, err)
	}
	remove := signAdmissionTx(t, &pb.AnyTx{Content: &pb.AnyTx_MinerTx{MinerTx: &pb.MinerTx{
		Base: &pb.BaseMessage{Nonce: 0, Fee: "1000"}, Op: pb.OrderOp_REMOVE,
	}}}, identity)
	if ok, err := pool.SubmitLocalBootstrapMinerTx(remove, nil); ok || err == nil {
		t.Fatalf("REMOVE used bootstrap escape hatch: %v/%v", ok, err)
	}
}

func TestLocalSubmissionTTLReleasesWindow(t *testing.T) {
	identity := utils.GenerateTestIdentity(45003)
	pool := newAdmissionPool(t, func(c *config.Config) { c.TxPool.TxExpirationTime = time.Minute }, identity.Address)
	setAdmissionAccount(t, pool, identity.Address, 0, "10000000")
	tx := admissionTransfer(t, identity, 0, "1")
	if ok, err := pool.SubmitLocalTx(tx, nil); !ok || err != nil {
		t.Fatal(err)
	}
	pool.maintainPending(time.Now().Add(2 * time.Minute))
	status, err := pool.AccountPendingStatus(identity.Address)
	if err != nil || status.Count != 0 || status.NextNonce != 0 {
		t.Fatalf("expired state=%+v/%v", status, err)
	}
	if ok, err := pool.SubmitLocalTx(tx, nil); !ok || err != nil {
		t.Fatalf("resubmit: %v/%v", ok, err)
	}
}

func TestLocalSubmissionAlreadyAppliedIsAccepted(t *testing.T) {
	identity := utils.GenerateTestIdentity(45004)
	pool := newAdmissionPool(t, nil, identity.Address)
	tx := admissionTransfer(t, identity, 0, "1")
	pool.markTxAppliedInMemory(tx.GetTxId())
	if ok, err := pool.SubmitLocalTx(tx, func(string) { t.Fatal("applied transaction broadcast again") }); !ok || err != nil {
		t.Fatalf("already applied=%v/%v", ok, err)
	}
	if pool.PendingLen() != 0 {
		t.Fatal("already applied entered pending")
	}
}

func TestLocalSubmissionPersistenceFailureDoesNotMeanRejected(t *testing.T) {
	identity := utils.GenerateTestIdentity(45005)
	pool := newAdmissionPool(t, nil, identity.Address)
	setAdmissionAccount(t, pool, identity.Address, 0, "10000000")
	                                                                                    
	pool.validator = localStopDuringValidation{pool: pool}
	pool.pendingSaveQueue = nil
	accepted, err := pool.SubmitLocalTx(admissionTransfer(t, identity, 0, "1"), nil)
	                                                                                    
	pool.stopChan = make(chan struct{})
	if !accepted || err == nil || !strings.Contains(err.Error(), "stopping") || pool.PendingLen() != 1 {
		t.Fatalf("admission/persistence confused: %v/%v pending=%d", accepted, err, pool.PendingLen())
	}
}

type localStopDuringValidation struct{ pool *TxPool }

func (v localStopDuringValidation) CheckAnyTx(tx *pb.AnyTx) error {
	if err := (realAdmissionValidator{}).CheckAnyTx(tx); err != nil {
		return err
	}
	close(v.pool.stopChan)
	return nil
}
