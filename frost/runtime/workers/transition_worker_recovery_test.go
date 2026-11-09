package workers

import (
	"context"
	"errors"
	"testing"

	"dex/keys"
	"dex/pb"
)

type recoveryLocalShareStore struct {
	LocalShareStore
	refs       []DkgLocalSessionRef
	listErr    error
	promoteErr error
	deleteErr  error
	hasSession bool
	hasErr     error
	canonical  []byte
	promoteTo  []byte
	promoted   []DkgLocalSessionRef
	deleted    []DkgLocalSessionRef
}

func (s *recoveryLocalShareStore) HasDkgLocalSession(string, uint32, uint64, string) (bool, error) {
	return s.hasSession, s.hasErr
}

func (s *recoveryLocalShareStore) LoadLocalShare(string, uint32, uint64) ([]byte, error) {
	return append([]byte(nil), s.canonical...), nil
}

func (s *recoveryLocalShareStore) ListDkgLocalSessions() ([]DkgLocalSessionRef, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]DkgLocalSessionRef(nil), s.refs...), nil
}

func (s *recoveryLocalShareStore) PromotePendingLocalShare(chain string, vaultID uint32, epoch uint64, sessionID string) error {
	if s.promoteErr != nil {
		return s.promoteErr
	}
	s.promoted = append(s.promoted, DkgLocalSessionRef{Chain: chain, VaultID: vaultID, Epoch: epoch, SessionID: sessionID})
	if len(s.promoteTo) > 0 {
		s.canonical = append([]byte(nil), s.promoteTo...)
	}
	return nil
}

func (s *recoveryLocalShareStore) DeleteDkgSessionArtifacts(chain string, vaultID uint32, epoch uint64, sessionID string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deleted = append(s.deleted, DkgLocalSessionRef{Chain: chain, VaultID: vaultID, Epoch: epoch, SessionID: sessionID})
	return nil
}

func TestStartPendingSessionsRecoversKeyReadyShareWithoutActiveIndex(t *testing.T) {
	ref := DkgLocalSessionRef{Chain: "btc", VaultID: 1, Epoch: 7, SessionID: "btc_1_7_1"}
	transition := &pb.VaultTransitionState{
		Chain:        ref.Chain,
		VaultId:      ref.VaultID,
		EpochId:      ref.Epoch,
		DkgSessionId: ref.SessionID,
		DkgStatus:    "KEY_READY",
	}
	reader := &complaintTestStateReader{data: map[string][]byte{
		keys.KeyFrostVaultTransition(ref.Chain, ref.VaultID, ref.Epoch): mustSessionProto(t, transition),
	}}
	store := &recoveryLocalShareStore{refs: []DkgLocalSessionRef{ref}, hasSession: true}
	worker := &TransitionWorker{
		stateReader:     reader,
		localShareStore: store,
		Logger:          complaintTestLogger{},
	}

	if err := worker.StartPendingSessions(context.Background()); err != nil {
		t.Fatalf("StartPendingSessions failed: %v", err)
	}
	if len(store.promoted) != 1 || store.promoted[0] != ref {
		t.Fatalf("KEY_READY share was not recovered from local marker: %+v", store.promoted)
	}
	if len(store.deleted) != 1 || store.deleted[0] != ref {
		t.Fatalf("completed KEY_READY recovery marker was not cleaned up: %+v", store.deleted)
	}
}

func TestRecoverDkgLocalSessionsGarbageCollectsOnlyTerminalOrSuperseded(t *testing.T) {
	const (
		chain   = "btc"
		vaultID = uint32(1)
		epoch   = uint64(7)
	)
	tests := []struct {
		name       string
		refSession string
		onChain    string
		status     string
		wantDelete bool
	}{
		{name: "same active session retained", refSession: "s1", onChain: "s1", status: "SHARING"},
		{name: "failed session deleted", refSession: "s1", onChain: "s1", status: "FAILED", wantDelete: true},
		{name: "superseded session deleted", refSession: "s1", onChain: "s2", status: "COMMITTING", wantDelete: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ref := DkgLocalSessionRef{Chain: chain, VaultID: vaultID, Epoch: epoch, SessionID: test.refSession}
			transition := &pb.VaultTransitionState{
				Chain: chain, VaultId: vaultID, EpochId: epoch,
				DkgSessionId: test.onChain, DkgStatus: test.status,
			}
			reader := &complaintTestStateReader{data: map[string][]byte{
				keys.KeyFrostVaultTransition(chain, vaultID, epoch): mustSessionProto(t, transition),
			}}
			store := &recoveryLocalShareStore{refs: []DkgLocalSessionRef{ref}}
			worker := &TransitionWorker{stateReader: reader, localShareStore: store, Logger: complaintTestLogger{}}

			worker.recoverDkgLocalSessions()
			if got := len(store.deleted) == 1; got != test.wantDelete {
				t.Fatalf("deleted=%v want=%v records=%+v", got, test.wantDelete, store.deleted)
			}
			if len(store.promoted) != 0 {
				t.Fatalf("non-KEY_READY session unexpectedly promoted: %+v", store.promoted)
			}
		})
	}
}

func TestRecoverDkgLocalSessionsRetainsArtifactsOnUncertainState(t *testing.T) {
	ref := DkgLocalSessionRef{Chain: "btc", VaultID: 1, Epoch: 7, SessionID: "s1"}
	store := &recoveryLocalShareStore{refs: []DkgLocalSessionRef{ref}, promoteErr: errors.New("disk unavailable")}
	worker := &TransitionWorker{
		stateReader:     &complaintTestStateReader{data: map[string][]byte{}},
		localShareStore: store,
		Logger:          complaintTestLogger{},
	}

	worker.recoverDkgLocalSessions()
	if len(store.promoted) != 0 || len(store.deleted) != 0 {
		t.Fatalf("missing transition must retain local artifacts: promoted=%+v deleted=%+v", store.promoted, store.deleted)
	}
}

func TestRecoverDkgLocalSessionsKeepsDrainingMarkerForActiveIndexPlanner(t *testing.T) {
	ref := DkgLocalSessionRef{Chain: "btc", VaultID: 1, Epoch: 7, SessionID: "s1"}
	transition := &pb.VaultTransitionState{
		Chain: ref.Chain, VaultId: ref.VaultID, EpochId: ref.Epoch,
		DkgSessionId: ref.SessionID, DkgStatus: "KEY_READY", Lifecycle: "DRAINING",
		OldGroupPubkey: []byte{0x01},
	}
	reader := &complaintTestStateReader{data: map[string][]byte{
		keys.KeyFrostVaultTransition(ref.Chain, ref.VaultID, ref.Epoch): mustSessionProto(t, transition),
	}}
	store := &recoveryLocalShareStore{refs: []DkgLocalSessionRef{ref}, hasSession: true}
	worker := &TransitionWorker{stateReader: reader, localShareStore: store, Logger: complaintTestLogger{}}

	worker.recoverDkgLocalSessions()
	if len(store.promoted) != 1 {
		t.Fatalf("KEY_READY local share should be promoted before planning: %+v", store.promoted)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("DRAINING recovery marker must survive until active-index planning succeeds: %+v", store.deleted)
	}
}

func TestStartPendingSessionsWaitsForConsensusMigrationIntent(t *testing.T) {
	const (
		chain   = "btc"
		vaultID = uint32(1)
		epoch   = uint64(7)
	)
	transition := &pb.VaultTransitionState{
		Chain: chain, VaultId: vaultID, EpochId: epoch,
		DkgSessionId: "s1", DkgStatus: "KEY_READY", Lifecycle: "DRAINING",
		OldGroupPubkey: []byte{0x01}, NewCommitteeMembers: []string{"miner-b"},
	}
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epoch)
	reader := &complaintTestStateReader{data: map[string][]byte{
		transitionKey: keysMustProto(t, transition),
		keys.KeyFrostVaultTransitionActive(chain, vaultID, epoch): []byte(transitionKey),
	}}
	store := &recoveryLocalShareStore{}
	worker := &TransitionWorker{stateReader: reader, localShareStore: store, localAddress: "miner-a", Logger: complaintTestLogger{}}

	if err := worker.StartPendingSessions(context.Background()); err != nil {
		t.Fatalf("StartPendingSessions failed: %v", err)
	}
	migrationKey := "btc_1_7"
	if worker.plannedMigrations[migrationKey] {
		t.Fatalf("runtime planned before consensus finalized an intent: %+v", worker.plannedMigrations)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("marker-free KEY_READY scan must not force-flush artifact deletion: %+v", store.deleted)
	}
}

func TestStartPendingSessionsRequiresCanonicalShareForLocalNewCommitteeMember(t *testing.T) {
	const (
		chain        = "btc"
		vaultID      = uint32(1)
		epoch        = uint64(7)
		localAddress = "miner-a"
	)
	transition := &pb.VaultTransitionState{
		Chain: chain, VaultId: vaultID, EpochId: epoch,
		DkgSessionId: "s1", DkgStatus: "KEY_READY", Lifecycle: "DRAINING",
		OldGroupPubkey: []byte{0x01}, NewCommitteeMembers: []string{localAddress},
	}
	transitionKey := keys.KeyFrostVaultTransition(chain, vaultID, epoch)
	reader := &complaintTestStateReader{data: map[string][]byte{
		transitionKey: mustSessionProto(t, transition),
		keys.KeyFrostVaultTransitionActive(chain, vaultID, epoch): []byte(transitionKey),
	}}
	store := &recoveryLocalShareStore{}
	worker := &TransitionWorker{
		stateReader: reader, localShareStore: store,
		localAddress: localAddress, Logger: complaintTestLogger{},
	}

	if err := worker.StartPendingSessions(context.Background()); err != nil {
		t.Fatalf("StartPendingSessions failed: %v", err)
	}
	if worker.plannedMigrations["btc_1_7"] {
		t.Fatal("local new-committee member must not plan migration without its canonical share")
	}
	if len(store.deleted) != 0 {
		t.Fatalf("missing canonical share must not trigger cleanup: %+v", store.deleted)
	}
}

func TestStartPendingSessionsPromotesBeforePlanningDrainingTransition(t *testing.T) {
	ref := DkgLocalSessionRef{Chain: "btc", VaultID: 1, Epoch: 7, SessionID: "s1"}
	transition := &pb.VaultTransitionState{
		Chain: ref.Chain, VaultId: ref.VaultID, EpochId: ref.Epoch,
		DkgSessionId: ref.SessionID, DkgStatus: "KEY_READY", Lifecycle: "DRAINING",
		OldGroupPubkey: []byte{0x01}, NewCommitteeMembers: []string{"miner-a"},
	}
	transitionKey := keys.KeyFrostVaultTransition(ref.Chain, ref.VaultID, ref.Epoch)
	reader := &complaintTestStateReader{data: map[string][]byte{
		transitionKey: mustSessionProto(t, transition),
		keys.KeyFrostVaultTransitionActive(ref.Chain, ref.VaultID, ref.Epoch): []byte(transitionKey),
	}}
	store := &recoveryLocalShareStore{refs: []DkgLocalSessionRef{ref}, hasSession: true, promoteTo: []byte{0x09}}
	worker := &TransitionWorker{stateReader: reader, localShareStore: store, localAddress: "miner-a", Logger: complaintTestLogger{}}

	if err := worker.StartPendingSessions(context.Background()); err != nil {
		t.Fatalf("StartPendingSessions failed: %v", err)
	}
	if len(store.promoted) != 1 || store.promoted[0] != ref {
		t.Fatalf("pending share was not promoted before active-index processing: %+v", store.promoted)
	}
	if worker.plannedMigrations["btc_1_7"] {
		t.Fatal("runtime planned before consensus finalized an intent")
	}
	if len(store.deleted) != 0 {
		t.Fatalf("DKG recovery marker was deleted before a migration intent existed: %+v", store.deleted)
	}
}

func TestStartPendingSessionsPromotesPendingOnlyKeyReadySession(t *testing.T) {
	ref := DkgLocalSessionRef{Chain: "btc", VaultID: 1, Epoch: 7, SessionID: "s1"}
	transition := &pb.VaultTransitionState{
		Chain: ref.Chain, VaultId: ref.VaultID, EpochId: ref.Epoch,
		DkgSessionId: ref.SessionID, DkgStatus: "KEY_READY", Lifecycle: "DRAINING",
		OldGroupPubkey: []byte{0x01}, NewCommitteeMembers: []string{"miner-a"},
	}
	transitionKey := keys.KeyFrostVaultTransition(ref.Chain, ref.VaultID, ref.Epoch)
	reader := &complaintTestStateReader{data: map[string][]byte{
		transitionKey: mustSessionProto(t, transition),
		keys.KeyFrostVaultTransitionActive(ref.Chain, ref.VaultID, ref.Epoch): []byte(transitionKey),
	}}
	                                                                          
	                                                                              
	              
	store := &recoveryLocalShareStore{refs: []DkgLocalSessionRef{ref}, promoteTo: []byte{0x09}}
	worker := &TransitionWorker{
		stateReader: reader, localShareStore: store,
		localAddress: "miner-a", Logger: complaintTestLogger{},
	}

	if err := worker.StartPendingSessions(context.Background()); err != nil {
		t.Fatalf("StartPendingSessions failed: %v", err)
	}
	if len(store.promoted) != 1 || worker.plannedMigrations["btc_1_7"] {
		t.Fatalf("pending-only KEY_READY session promotion/planning boundary is wrong: promoted=%+v planned=%+v",
			store.promoted, worker.plannedMigrations)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("pending-only recovery must not issue marker cleanup flush: %+v", store.deleted)
	}
}

func TestStartPendingSessionsBlocksMigrationWhenLocalPromotionFails(t *testing.T) {
	ref := DkgLocalSessionRef{Chain: "btc", VaultID: 1, Epoch: 7, SessionID: "s1"}
	transition := &pb.VaultTransitionState{
		Chain: ref.Chain, VaultId: ref.VaultID, EpochId: ref.Epoch,
		DkgSessionId: ref.SessionID, DkgStatus: "KEY_READY", Lifecycle: "DRAINING",
		OldGroupPubkey: []byte{0x01},
	}
	transitionKey := keys.KeyFrostVaultTransition(ref.Chain, ref.VaultID, ref.Epoch)
	reader := &complaintTestStateReader{data: map[string][]byte{
		transitionKey: keysMustProto(t, transition),
		keys.KeyFrostVaultTransitionActive(ref.Chain, ref.VaultID, ref.Epoch): []byte(transitionKey),
	}}
	store := &recoveryLocalShareStore{refs: []DkgLocalSessionRef{ref}, promoteErr: errors.New("disk unavailable"), hasSession: true}
	worker := &TransitionWorker{stateReader: reader, localShareStore: store, Logger: complaintTestLogger{}}

	if err := worker.StartPendingSessions(context.Background()); err != nil {
		t.Fatalf("StartPendingSessions failed: %v", err)
	}
	if worker.plannedMigrations["btc_1_7"] {
		t.Fatal("migration must not start before this node's KEY_READY share is promoted")
	}
	if len(store.deleted) != 0 {
		t.Fatalf("failed promotion marker must be retained: %+v", store.deleted)
	}
}

func TestStartPendingSessionsFailsSafeWhenLocalRecoveryCannotVerifyMarker(t *testing.T) {
	ref := DkgLocalSessionRef{Chain: "btc", VaultID: 1, Epoch: 7, SessionID: "s1"}
	transition := &pb.VaultTransitionState{
		Chain: ref.Chain, VaultId: ref.VaultID, EpochId: ref.Epoch,
		DkgSessionId: ref.SessionID, DkgStatus: "KEY_READY", Lifecycle: "DRAINING",
		OldGroupPubkey: []byte{0x01},
	}
	transitionKey := keys.KeyFrostVaultTransition(ref.Chain, ref.VaultID, ref.Epoch)
	readerData := map[string][]byte{
		transitionKey: mustSessionProto(t, transition),
		keys.KeyFrostVaultTransitionActive(ref.Chain, ref.VaultID, ref.Epoch): []byte(transitionKey),
	}
	tests := []struct {
		name  string
		store *recoveryLocalShareStore
	}{
		{
			name:  "marker list scan fails",
			store: &recoveryLocalShareStore{listErr: errors.New("local db unavailable")},
		},
		{
			name:  "corrupt marker skipped by list remains discoverable by exact get",
			store: &recoveryLocalShareStore{hasSession: true},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			worker := &TransitionWorker{
				stateReader:     &complaintTestStateReader{data: readerData},
				localShareStore: test.store, Logger: complaintTestLogger{},
			}
			if err := worker.StartPendingSessions(context.Background()); err != nil {
				t.Fatalf("StartPendingSessions failed: %v", err)
			}
			if worker.plannedMigrations["btc_1_7"] {
				t.Fatal("unverified local marker must block KEY_READY migration planning")
			}
			if len(test.store.deleted) != 0 {
				t.Fatalf("unverified local marker must not be deleted: %+v", test.store.deleted)
			}
		})
	}
}

func keysMustProto(t *testing.T, message *pb.VaultTransitionState) []byte {
	return mustSessionProto(t, message)
}
