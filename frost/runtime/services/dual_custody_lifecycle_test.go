package services

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"dex/frost/chain"
	"dex/frost/guardian"
	"dex/frost/runtime/types"
	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

                                                                               
                                                                              
type lifecycleDualInner struct {
	mu        sync.Mutex
	active    map[string]bool
	starts    []string
	cancelled []string
	fail      map[string]error
	onCancel  func(string)
}

func (i *lifecycleDualInner) StartSigningSession(_ context.Context, p *types.SigningSessionParams) (string, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.starts = append(i.starts, p.JobID)
	if err := i.fail[p.JobID]; err != nil {
		return "", err
	}
	i.active[p.JobID] = true
	return p.JobID, nil
}
func (i *lifecycleDualInner) GetSessionStatus(id string) (*types.SessionStatus, error) {
	return &types.SessionStatus{SessionID: id, JobID: id, State: "COMPLETE", Progress: 1}, nil
}
func (i *lifecycleDualInner) CancelSession(id string) error {
	i.mu.Lock()
	delete(i.active, id)
	i.cancelled = append(i.cancelled, id)
	hook := i.onCancel
	i.mu.Unlock()
	if hook != nil {
		hook(id)
	}
	return nil
}
func (i *lifecycleDualInner) WaitForCompletion(_ context.Context, id string, _ time.Duration) (*types.SignedPackage, error) {
	return &types.SignedPackage{JobID: id, SessionID: id, Signatures: [][]byte{{1}}}, nil
}
func (i *lifecycleDualInner) counts() (int, int, int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.active), len(i.starts), len(i.cancelled)
}
func lifecycleParams(p *types.SigningSessionParams) *types.SigningSessionParams {
	out := *p
	out.Messages = copySignatureMatrix(p.Messages)
	out.Tweaks = copySignatureMatrix(p.Tweaks)
	return &out
}
func lifecycleRound(p *types.SigningSessionParams, round int) *types.SigningSessionParams {
	out := lifecycleParams(p)
	out.JobID = fmt.Sprintf("%s_r%d", chain.MigrationID(p.Chain, p.VaultID, p.KeyEpoch+1), round)
	return out
}
func lifecycleFixture(t *testing.T) (*DualCustodySigningService, *types.SigningSessionParams, *lifecycleDualInner, *formalDualClient, []byte) {
	t.Helper()
	old, p, _, sig := formalDualServiceFixture(t)
	reader := old.reader.(formalDualReader)
	hash := bytes.Repeat([]byte{7}, 32)
	p = lifecycleRound(p, 1)
	marker := &pb.FrostVaultMigrationState{MigrationId: chain.MigrationID(p.Chain, p.VaultID, p.KeyEpoch+1), Chain: p.Chain, VaultId: p.VaultID, OldEpoch: p.KeyEpoch, NewEpoch: p.KeyEpoch + 1, TemplateHash: hash}
	raw, err := proto.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	reader[keys.KeyFrostVaultMigration(p.Chain, p.VaultID, p.KeyEpoch+1)] = raw
	base := marker.MigrationId
	client := &formalDualClient{sign: func(_ context.Context, r guardian.Request) ([][]byte, error) {
		if !strings.HasPrefix(r.JobID, base+"_r") || r.Chain != p.Chain || r.Epoch != p.KeyEpoch || r.VaultID != p.VaultID || !bytes.Equal(r.TemplateHash, hash) {
			return nil, fmt.Errorf("incorrect migration scope")
		}
		return [][]byte{sig}, nil
	}}
	inner := &lifecycleDualInner{active: map[string]bool{}, fail: map[string]error{}}
	return NewDualCustodySigningService(inner, reader, client), p, inner, client, sig
}
func lifecycleSizes(s *DualCustodySigningService) (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.params), len(s.completed)
}
func lifecycleAssertRegistered(t *testing.T, s *DualCustodySigningService, id string, cached bool) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.params[id] == nil || (s.completed[id] != nil) != cached {
		t.Fatalf("context lost or cache changed: id=%s params=%v cached=%v", id, s.params[id] != nil, s.completed[id] != nil)
	}
}

func TestDualCustodyMigrationOfflineRoundsRemainBoundedAndRecover(t *testing.T) {
	s, p, inner, client, signature := lifecycleFixture(t)
	online := client.sign
	client.sign = func(context.Context, guardian.Request) ([][]byte, error) { return nil, fmt.Errorf("guardian offline") }
	inner.onCancel = func(string) {
		if !s.mu.TryLock() {
			t.Error("inner cancellation ran while wrapper lock was held")
			return
		}
		s.mu.Unlock()
	}
	var latest string
	for round := 1; round <= 300; round++ {
		next := lifecycleRound(p, round)
		id, err := s.StartSigningSession(context.Background(), next)
		if err != nil {
			t.Fatalf("round%d start: %v", round, err)
		}
		latest = id
		if result, err := s.WaitForCompletion(context.Background(), id, time.Second); err == nil || result != nil {
			t.Fatalf("round%d delivered partial authorization", round)
		}
		contexts, completed := lifecycleSizes(s)
		active, _, _ := inner.counts()
		if contexts != 1 || completed != 0 || active != 1 {
			t.Fatalf("round%d contexts=%d completed=%d inner=%d", round, contexts, completed, active)
		}
	}
	_, starts, cancels := inner.counts()
	if starts != 300 || cancels != 299 {
		t.Fatalf("starts=%d cancellations=%d", starts, cancels)
	}
	client.sign = online
	result, err := s.WaitForCompletion(context.Background(), latest, time.Second)
	if err != nil || result == nil || !bytes.Equal(result.GuardianSignatures[0], signature) {
		t.Fatalf("guardian recovery failed: result=%+v err=%v", result, err)
	}
	lifecycleAssertRegistered(t, s, latest, true)
}

func TestDualCustodyFullCapacityAllowsSameMigrationReplacement(t *testing.T) {
	s, p, inner, _, _ := lifecycleFixture(t)
	var original string
	for vault := uint32(0); vault < 256; vault++ {
		next := lifecycleParams(p)
		next.VaultID = vault
		next = lifecycleRound(next, 1)
		id, err := s.StartSigningSession(context.Background(), next)
		if err != nil {
			t.Fatalf("fill vault%d: %v", vault, err)
		}
		if vault == 0 {
			original = id
		}
	}
	replacement := lifecycleRound(p, 2)
	id, err := s.StartSigningSession(context.Background(), replacement)
	if err != nil {
		t.Fatalf("same intent rejected at capacity: %v", err)
	}
	if n, _ := lifecycleSizes(s); n != 256 {
		t.Fatalf("replacement changed occupancy: %d", n)
	}
	lifecycleAssertRegistered(t, s, id, false)
	s.mu.Lock()
	oldStillPresent := s.params[original] != nil
	s.mu.Unlock()
	if oldStillPresent {
		t.Fatal("superseded round remains registered")
	}
	next := lifecycleParams(p)
	next.VaultID = 256
	next = lifecycleRound(next, 1)
	_, beforeStarts, _ := inner.counts()
	if _, err := s.StartSigningSession(context.Background(), next); err == nil {
		t.Fatal("different migration bypassed capacity")
	}
	active, afterStarts, _ := inner.counts()
	if active != 256 || beforeStarts != afterStarts {
		t.Fatalf("rejected capacity changed inner: active=%d starts=%d/%d", active, beforeStarts, afterStarts)
	}
}

func TestDualCustodyRoundPayloadConflictPreservesExistingCompletion(t *testing.T) {
	for _, field := range []string{"message", "tweak", "chain", "vault", "epoch", "algorithm", "threshold", "guardian"} {
		t.Run(field, func(t *testing.T) {
			s, p, inner, _, _ := lifecycleFixture(t)
			id, err := s.StartSigningSession(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.WaitForCompletion(context.Background(), id, time.Second); err != nil {
				t.Fatal(err)
			}
			next := lifecycleRound(p, 2)
			switch field {
			case "message":
				next.Messages[0][0] ^= 1
			case "tweak":
				next.Tweaks = [][]byte{{1}}
			case "chain":
				next.Chain = "eth"
			case "vault":
				next.VaultID++
			case "epoch":
				next.KeyEpoch++
			case "algorithm":
				next.SignAlgo = pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128
			case "threshold":
				next.Threshold++
			case "guardian":
				next.RequireGuardian = false
			}
			if _, err := s.StartSigningSession(context.Background(), next); err == nil {
				t.Fatal("conflicting round payload accepted")
			}
			lifecycleAssertRegistered(t, s, id, true)
			n, c := lifecycleSizes(s)
			active, starts, cancels := inner.counts()
			if n != 1 || c != 1 || active != 1 || starts != 1 || cancels != 0 {
				t.Fatalf("conflict removed valid state: contexts=%d cache=%d active=%d starts=%d cancels=%d", n, c, active, starts, cancels)
			}
		})
	}
}

func TestDualCustodyNewRoundStartFailurePreservesPriorContext(t *testing.T) {
	s, p, inner, _, _ := lifecycleFixture(t)
	id, err := s.StartSigningSession(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	next := lifecycleRound(p, 2)
	inner.mu.Lock()
	inner.fail[next.JobID] = fmt.Errorf("inner unavailable")
	inner.mu.Unlock()
	if _, err = s.StartSigningSession(context.Background(), next); err == nil {
		t.Fatal("expected inner start failure")
	}
	lifecycleAssertRegistered(t, s, id, false)
	active, _, cancels := inner.counts()
	if active != 1 || cancels != 0 {
		t.Fatalf("failed replacement removed prior round: active=%d cancels=%d", active, cancels)
	}
}

func TestDualCustodyLateGuardianCannotDeliverOrResurrectCancelledGeneration(t *testing.T) {
	for _, action := range []string{"replace-round", "cancel", "restart-same-id"} {
		t.Run(action, func(t *testing.T) {
			s, p, _, client, signature := lifecycleFixture(t)
			entered := make(chan struct{})
			release := make(chan struct{})
			client.sign = func(context.Context, guardian.Request) ([][]byte, error) {
				close(entered)
				<-release
				return [][]byte{signature}, nil
			}
			id, err := s.StartSigningSession(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			type outcome struct {
				result *types.SignedPackage
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := s.WaitForCompletion(context.Background(), id, 5*time.Second)
				done <- outcome{result, err}
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("guardian request did not start")
			}
			var current string
			if action == "replace-round" {
				current, err = s.StartSigningSession(context.Background(), lifecycleRound(p, 2))
			} else {
				err = s.CancelSession(id)
				if err == nil && action == "restart-same-id" {
					current, err = s.StartSigningSession(context.Background(), p)
				}
			}
			if err != nil {
				close(release)
				t.Fatal(err)
			}
			close(release)
			select {
			case got := <-done:
				if got.err == nil || got.result != nil {
					t.Fatalf("late generation delivered: result=%+v err=%v", got.result, got.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("old wait did not return")
			}
			contexts, cached := lifecycleSizes(s)
			expected := 0
			if action != "cancel" {
				expected = 1
			}
			if contexts != expected || cached != 0 {
				t.Fatalf("late wait resurrected state: contexts=%d cached=%d", contexts, cached)
			}
			if current != "" {
				client.sign = func(context.Context, guardian.Request) ([][]byte, error) { return [][]byte{signature}, nil }
				result, err := s.WaitForCompletion(context.Background(), current, time.Second)
				if err != nil || result == nil {
					t.Fatalf("replacement failed to recover: %v", err)
				}
			}
		})
	}
}

func TestDualCustodySameRoundRetryPreservesInflightGeneration(t *testing.T) {
	s, p, inner, client, signature := lifecycleFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	client.sign = func(context.Context, guardian.Request) ([][]byte, error) {
		close(entered)
		<-release
		return [][]byte{signature}, nil
	}
	id, err := s.StartSigningSession(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	token := s.params[id]
	s.mu.Unlock()
	type outcome struct {
		result *types.SignedPackage
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := s.WaitForCompletion(context.Background(), id, 5*time.Second)
		done <- outcome{result, err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("guardian did not start")
	}
	again, err := s.StartSigningSession(context.Background(), lifecycleParams(p))
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	s.mu.Lock()
	same := s.params[id] == token
	s.mu.Unlock()
	if again != id || !same {
		close(release)
		t.Fatal("identical round reset the generation")
	}
	close(release)
	select {
	case got := <-done:
		if got.err != nil || got.result == nil {
			t.Fatalf("idempotent retry rejected valid in-flight result: %v", got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completion stuck")
	}
	_, _, cancels := inner.counts()
	if cancels != 0 {
		t.Fatal("identical round was cancelled")
	}
	lifecycleAssertRegistered(t, s, id, true)
}

func TestDualCustodyFullCompletionCacheRefreshPreservesContexts(t *testing.T) {
	s, p, _, _, _ := lifecycleFixture(t)
	tokens := map[string]*types.SigningSessionParams{}
	for i := 0; i < 128; i++ {
		next := lifecycleParams(p)
		next.JobID = fmt.Sprintf("completed-withdraw-%d", i)
		id, err := s.StartSigningSession(context.Background(), next)
		if err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		tokens[id] = s.params[id]
		s.mu.Unlock()
		if err := s.cacheResult(id, tokens[id], &types.SignedPackage{JobID: id, Signature: []byte{1}}); err != nil {
			t.Fatal(err)
		}
	}
	const id = "completed-withdraw-0"
	token := tokens[id]
	var waits sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		waits.Add(1)
		go func() {
			defer waits.Done()
			errs <- s.cacheResult(id, token, &types.SignedPackage{JobID: id, Signature: []byte{2}})
		}()
	}
	waits.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("same-generation cache refresh failed: %v", err)
		}
	}
	contexts, completed := lifecycleSizes(s)
	if contexts != 128 || completed != 128 {
		t.Fatalf("cache refresh evicted registered context: contexts=%d completed=%d", contexts, completed)
	}
	s.mu.Lock()
	for key, want := range tokens {
		if s.params[key] != want || s.completed[key] == nil {
			s.mu.Unlock()
			t.Fatalf("cache refresh deleted or replaced context %s", key)
		}
	}
	s.mu.Unlock()
	result, err := s.WaitForCompletion(context.Background(), id, time.Second)
	if err != nil || result == nil || !bytes.Equal(result.Signature, []byte{2}) {
		t.Fatalf("refreshed cache unavailable: result=%+v err=%v", result, err)
	}
}
