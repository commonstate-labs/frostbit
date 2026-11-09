package services

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"dex/frost/chain"
	frostcrypto "dex/frost/core/frost"
	"dex/frost/guardian"
	"dex/frost/runtime/types"
	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

                                                                                
                                                                               
                                             
type DualCustodySigningService struct {
	inner     types.SigningService
	reader    types.ChainStateReader
	client    guardian.Client
	mu        sync.Mutex
	params    map[string]*types.SigningSessionParams
	completed map[string]*types.SignedPackage
}

func NewDualCustodySigningService(inner types.SigningService, reader types.ChainStateReader, client guardian.Client) *DualCustodySigningService {
	return &DualCustodySigningService{inner: inner, reader: reader, client: client, params: make(map[string]*types.SigningSessionParams), completed: make(map[string]*types.SignedPackage)}
}
func (s *DualCustodySigningService) StartSigningSession(ctx context.Context, p *types.SigningSessionParams) (string, error) {
	if p == nil {
		return "", fmt.Errorf("missing signing parameters")
	}
	base, err := dualMigrationBase(p)
	if err != nil {
		return "", err
	}
	var replaced []string
	s.mu.Lock()
	                                                                           
	defer func() {
		s.mu.Unlock()
		for _, oldID := range replaced {
			_ = s.inner.CancelSession(oldID)
		}
	}()
	if existing := s.params[p.JobID]; existing != nil && !sameDualIntent(existing, p) {
		return "", fmt.Errorf("signing session identity is bound to another intent")
	}
	var superseded []string
	if base != "" {
		for id, existing := range s.params {
			oldBase, parseErr := dualMigrationBase(existing)
			if parseErr != nil || oldBase != base {
				continue
			}
			candidate := *p
			candidate.JobID = existing.JobID
			if !sameDualIntent(existing, &candidate) {
				return "", fmt.Errorf("migration signing rounds disagree on the canonical intent")
			}
			if existing.JobID != p.JobID {
				superseded = append(superseded, id)
			}
		}
	}
	if len(s.params)-len(superseded) >= 256 && s.params[p.JobID] == nil {
		return "", fmt.Errorf("dual custody signing session capacity reached")
	}
	id, err := s.inner.StartSigningSession(ctx, p)
	if err != nil {
		return "", err
	}
	if existing := s.params[id]; existing != nil {
		if !sameDualIntent(existing, p) {
			return "", fmt.Errorf("returned signing session is bound to another intent")
		}
		return id, nil
	}
	copyParams := *p
	copyParams.Messages = copySignatureMatrix(p.Messages)
	copyParams.Tweaks = copySignatureMatrix(p.Tweaks)
	for _, oldID := range superseded {
		delete(s.params, oldID)
		delete(s.completed, oldID)
	}
	replaced = superseded
	s.params[id] = &copyParams
	return id, nil
}

                                                                                 
func dualMigrationBase(p *types.SigningSessionParams) (string, error) {
	if !strings.HasPrefix(p.JobID, "migration_") {
		return "", nil
	}
	if p.KeyEpoch == ^uint64(0) {
		return "", fmt.Errorf("invalid migration epoch")
	}
	base := chain.MigrationID(p.Chain, p.VaultID, p.KeyEpoch+1)
	if strings.HasPrefix(p.JobID, "migration_part_") {
		separator := strings.LastIndex(p.JobID, "_r")
		if separator < 0 {
			return "", fmt.Errorf("invalid migration child round")
		}
		base = p.JobID[:separator]
		hash := strings.TrimPrefix(base, "migration_part_")
		decoded, e := hex.DecodeString(hash)
		if e != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != hash {
			return "", fmt.Errorf("invalid migration child identity")
		}
	}
	prefix := base + "_r"
	if !strings.HasPrefix(p.JobID, prefix) {
		return "", fmt.Errorf("migration session identity disagrees with its context")
	}
	suffix := strings.TrimPrefix(p.JobID, prefix)
	round, err := strconv.ParseUint(suffix, 10, 64)
	if err != nil || strconv.FormatUint(round, 10) != suffix {
		return "", fmt.Errorf("invalid migration signing round")
	}
	return base, nil
}

func (s *DualCustodySigningService) GetSessionStatus(id string) (*types.SessionStatus, error) {
	status, err := s.inner.GetSessionStatus(id)
	if err != nil || status == nil {
		return status, err
	}
	s.mu.Lock()
	p := s.params[id]
	done := s.completed[id] != nil
	s.mu.Unlock()
	if p != nil && p.RequireGuardian && !done && status.State == "COMPLETE" {
		copyStatus := *status
		copyStatus.State = "WAITING_GUARDIAN"
		copyStatus.Progress = .9
		return &copyStatus, nil
	}
	return status, nil
}
func (s *DualCustodySigningService) CancelSession(id string) error {
	s.mu.Lock()
	delete(s.params, id)
	delete(s.completed, id)
	s.mu.Unlock()
	return s.inner.CancelSession(id)
}
func (s *DualCustodySigningService) WaitForCompletion(ctx context.Context, id string, timeout time.Duration) (*types.SignedPackage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	p := s.params[id]
	cached := s.completed[id]
	s.mu.Unlock()
	if cached != nil {
		return cloneDualPackage(cached), nil
	}
	result, err := s.inner.WaitForCompletion(ctx, id, timeout)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("operator signature result unavailable")
	}
	if p == nil {
		return nil, fmt.Errorf("unregistered signing session context")
	}
	if !p.RequireGuardian {
		if err := s.cacheResult(id, p, result); err != nil {
			return nil, err
		}
		return cloneDualPackage(result), nil
	}
	if s.client == nil {
		return nil, fmt.Errorf("WAITING_GUARDIAN: configure guardian.endpoint")
	}
	if s.reader == nil {
		return nil, fmt.Errorf("guardian finalized state unavailable")
	}
	var cfg pb.FrostVaultConfig
	raw, found, err := s.reader.Get(keys.KeyFrostVaultConfig(chain.NormalizeChain(p.Chain), 0))
	if err != nil || !found || proto.Unmarshal(raw, &cfg) != nil {
		return nil, fmt.Errorf("guardian consensus configuration unavailable")
	}
	request := guardian.Request{Chain: chain.NormalizeChain(p.Chain), VaultID: p.VaultID, Epoch: p.KeyEpoch, JobID: p.JobID}
	if strings.HasPrefix(p.JobID, "migration_") {
		var migration pb.FrostVaultMigrationState
		key := keys.KeyFrostVaultMigration(request.Chain, p.VaultID, p.KeyEpoch+1)
		if strings.HasPrefix(p.JobID, "migration_part_") {
			base, e := dualMigrationBase(p)
			if e != nil {
				return nil, e
			}
			key = keys.KeyFrostBtcMigrationChildState(base)
		}
		raw, found, err = s.reader.Get(key)
		if err != nil || !found || proto.Unmarshal(raw, &migration) != nil {
			return nil, fmt.Errorf("guardian migration intent unavailable")
		}
		request.TemplateHash = migration.TemplateHash
	} else {
		var job pb.FrostWithdrawJobState
		raw, found, err = s.reader.Get(keys.KeyFrostWithdrawJob(p.JobID))
		if err != nil || !found || proto.Unmarshal(raw, &job) != nil {
			return nil, fmt.Errorf("guardian withdrawal intent unavailable")
		}
		request.TemplateHash = job.TemplateHash
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	signatures, err := s.client.Sign(waitCtx, request)
	if err != nil {
		return nil, fmt.Errorf("WAITING_GUARDIAN: %w", err)
	}
	if len(signatures) != len(p.Messages) {
		return nil, fmt.Errorf("guardian signature count mismatch")
	}
	for i, m := range p.Messages {
		valid, e := frostcrypto.Verify(p.SignAlgo, cfg.GuardianPubkey, m, signatures[i])
		if e != nil || !valid {
			return nil, fmt.Errorf("guardian signature %d failed verification", i)
		}
	}
	copyResult := cloneDualPackage(result)
	copyResult.GuardianSignatures = copySignatureMatrix(signatures)
	if err := s.cacheResult(id, p, copyResult); err != nil {
		return nil, err
	}
	return cloneDualPackage(copyResult), nil
}
func (s *DualCustodySigningService) cacheResult(id string, token *types.SigningSessionParams, result *types.SignedPackage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == nil || s.params[id] != token {
		return fmt.Errorf("signing session was cancelled or superseded")
	}
	                                                                            
	                                                                            
	if len(s.completed) >= 128 && s.completed[id] == nil {
		for key := range s.completed {
			delete(s.completed, key)
			delete(s.params, key)
			break
		}
	}
	s.completed[id] = cloneDualPackage(result)
	return nil
}

func sameDualIntent(a, b *types.SigningSessionParams) bool {
	if a.JobID != b.JobID || a.Chain != b.Chain || a.VaultID != b.VaultID || a.KeyEpoch != b.KeyEpoch || a.SignAlgo != b.SignAlgo || a.Threshold != b.Threshold || a.RequireGuardian != b.RequireGuardian || len(a.Messages) != len(b.Messages) || len(a.Tweaks) != len(b.Tweaks) {
		return false
	}
	for i := range a.Messages {
		if !bytes.Equal(a.Messages[i], b.Messages[i]) {
			return false
		}
	}
	for i := range a.Tweaks {
		if !bytes.Equal(a.Tweaks[i], b.Tweaks[i]) {
			return false
		}
	}
	return true
}
func copySignatureMatrix(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i := range in {
		out[i] = append([]byte(nil), in[i]...)
	}
	return out
}
func cloneDualPackage(p *types.SignedPackage) *types.SignedPackage {
	if p == nil {
		return nil
	}
	out := *p
	out.Signature = append([]byte(nil), p.Signature...)
	out.Signatures = copySignatureMatrix(p.Signatures)
	out.GuardianSignatures = copySignatureMatrix(p.GuardianSignatures)
	out.RawTx = append([]byte(nil), p.RawTx...)
	out.TemplateHash = append([]byte(nil), p.TemplateHash...)
	return &out
}
