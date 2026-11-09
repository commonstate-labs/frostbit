package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"dex/keys"
	"dex/pb"
)

func TestWitnessChallengeBatchPathIsBoundedToRequestPage(t *testing.T) {
	requests := make([]*pb.RechargeRequest, 0, 106)
	for i := 0; i < 105; i++ {
		requests = append(requests, &pb.RechargeRequest{ChallengeId: fmt.Sprintf("challenge-%03d", i)})
	}
	requests = append(requests, &pb.RechargeRequest{ChallengeId: "challenge-000"})
	path := witnessChallengeBatchPath(requests)
	req := httptest.NewRequest("GET", path, nil)
	ids := strings.Split(req.URL.Query().Get("challenge_ids"), ",")
	if len(ids) != 100 || ids[0] != "challenge-000" || ids[99] != "challenge-099" {
		t.Fatalf("ids=%d %q..%q", len(ids), ids[0], ids[len(ids)-1])
	}
}

func TestDeriveWithdrawDebugStatusLatestSelectVaultFailureOverridesSigning(t *testing.T) {
	logs := []*pb.FrostPlanningLog{
		{Step: "StartSigning", Status: "OK", Message: "Signing session old started", Timestamp: 1000},
		{Step: "SelectVault", Status: "FAILED", Message: "Failed to select vault: select vault failed: insufficient balance across ACTIVE vaults for btc/BTC need=2470 max_available=0 max_vault=0 active_count=1", Timestamp: 2000},
	}

	status, reason := deriveWithdrawDebugStatus("SIGNING", logs)
	if status != "WAIT_VAULT_BALANCE" {
		t.Fatalf("status = %s want WAIT_VAULT_BALANCE, reason=%q", status, reason)
	}
}

func TestResolveJobStatusLatestPlanningBlockerOverridesSigning(t *testing.T) {
	logs := []*pb.FrostPlanningLog{
		{Step: "StartSigning", Status: "OK", Message: "Signing session old started", Timestamp: 1000},
		{Step: "SelectVault", Status: "FAILED", Message: "Failed to select vault: select vault failed: insufficient balance across ACTIVE vaults for btc/BTC need=2470 max_available=0 max_vault=0 active_count=1", Timestamp: 2000},
	}
	status := resolveJobStatus(map[string]struct{}{"SIGNING": {}}, logs)
	if status != "WAIT_VAULT_BALANCE" {
		t.Fatalf("status = %s want WAIT_VAULT_BALANCE", status)
	}
}

func TestDeriveWithdrawDebugStatusSigningLogStillWins(t *testing.T) {
	logs := []*pb.FrostPlanningLog{
		{Step: "SelectVault", Status: "FAILED", Message: "Failed to select vault: no active vault", Timestamp: 1000},
		{Step: "StartSigning", Status: "OK", Message: "Signing session new started", Timestamp: 2000},
	}

	status, reason := deriveWithdrawDebugStatus("SIGNING", logs)
	if status != "SIGNING" {
		t.Fatalf("status = %s want SIGNING, reason=%q", status, reason)
	}
}

func TestWithdrawVaultIDForExplorerKeepsZeroWhenAssigned(t *testing.T) {
	vaultID := withdrawVaultIDForExplorer(&pb.FrostWithdrawState{Status: "SIGNED", VaultId: 0})
	if vaultID == nil || *vaultID != 0 {
		t.Fatalf("vaultID = %v want pointer to 0", vaultID)
	}
}

func TestWithdrawVaultIDForExplorerOmitsQueuedAuto(t *testing.T) {
	if vaultID := withdrawVaultIDForExplorer(&pb.FrostWithdrawState{Status: "QUEUED", VaultId: 0}); vaultID != nil {
		t.Fatalf("vaultID = %v want nil", *vaultID)
	}
}

func TestCancelledWithdrawIsCompletedWithoutSyntheticJob(t *testing.T) {
	snapshot := buildFrostWithdrawMonitorSnapshot("revision", []*pb.FrostWithdrawState{{
		WithdrawId: "withdraw-cancelled", Requester: "fbc-alice", Chain: "btc", Asset: "BTC", Amount: "10000", Status: "CANCELLED", RequestHeight: 42,
	}})
	if len(snapshot.Pending) != 0 || len(snapshot.Jobs) != 0 || len(snapshot.UnassignedTasks) != 0 {
		t.Fatalf("cancelled withdrawal leaked into active monitor: pending=%d jobs=%d tasks=%d", len(snapshot.Pending), len(snapshot.Jobs), len(snapshot.UnassignedTasks))
	}
	if len(snapshot.Completed) != 1 || snapshot.Completed[0].Status != "CANCELLED" || snapshot.Completed[0].DerivedStatus != "CANCELLED" {
		t.Fatalf("completed=%+v", snapshot.Completed)
	}
	if snapshot.Completed[0].Requester != "fbc-alice" {
		t.Fatalf("requester=%q want fbc-alice", snapshot.Completed[0].Requester)
	}
}

func TestBuildFrostWithdrawAvailabilityPausesChainWideHandover(t *testing.T) {
	response := buildFrostWithdrawAvailability(100, 5, []*pb.VaultTransitionState{{
		Chain: "trx", VaultId: 0, EpochId: 2, TriggerHeight: 100,
		DkgStatus: "SHARING", DkgDisputeDeadline: 140, Lifecycle: "DRAINING",
	}}, []*pb.FrostVaultState{{
		Chain: "trx", VaultId: 0, KeyEpoch: 1, Status: "ACTIVE",
	}})
	if len(response.Chains) != 1 {
		t.Fatalf("chains=%+v", response.Chains)
	}
	got := response.Chains[0]
	if got.Status != "ROTATING" || got.WithdrawEnabled || got.Phase != "SHARING" {
		t.Fatalf("availability=%+v", got)
	}
	if got.EstimatedResumeHeight != 140 || got.RemainingBlocks != 40 || got.EstimatedSeconds != 200 {
		t.Fatalf("estimate=%+v", got)
	}
}

func TestBuildFrostWithdrawAvailabilityKeepsAlternateVaultAvailable(t *testing.T) {
	response := buildFrostWithdrawAvailability(99, 5, []*pb.VaultTransitionState{{
		Chain: "trx", VaultId: 0, EpochId: 2, TriggerHeight: 100,
		DkgStatus: "NOT_STARTED", DkgDisputeDeadline: 140, Lifecycle: "DRAINING",
	}}, []*pb.FrostVaultState{
		{Chain: "trx", VaultId: 0, KeyEpoch: 1, Status: "ACTIVE"},
		{Chain: "trx", VaultId: 1, KeyEpoch: 4, Status: "ACTIVE"},
	})
	got := response.Chains[0]
	if got.Status != "PLANNED" || !got.WithdrawEnabled || got.Reason != "alternate_active_vault_available" {
		t.Fatalf("availability=%+v", got)
	}
}

func TestBuildFrostWithdrawAvailabilityRestoresCompletedHandover(t *testing.T) {
	response := buildFrostWithdrawAvailability(150, 5, []*pb.VaultTransitionState{{
		Chain: "trx", VaultId: 0, EpochId: 2, DkgStatus: "KEY_READY",
		Lifecycle: "ACTIVE", MigrationStatus: "SIGNED",
	}}, []*pb.FrostVaultState{{
		Chain: "trx", VaultId: 0, KeyEpoch: 2, Status: "ACTIVE",
	}})
	got := response.Chains[0]
	if got.Status != "AVAILABLE" || !got.WithdrawEnabled {
		t.Fatalf("availability=%+v", got)
	}
}

func TestHandleFrostDKGSessionsExposesHandoverDiagnostics(t *testing.T) {
	const node = "node.example:443"
	srv := newFrostWithdrawV2TestServer([]string{node}, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/frost/dkg/list":
			return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.VaultTransitionStateList{States: []*pb.VaultTransitionState{{
				Chain: "btc", VaultId: 0, EpochId: 2, DkgSessionId: "btc_0_2_3",
				DkgStatus: "KEY_READY", DkgAttempt: 3, DkgDisqualifiedMembers: []string{"miner-bad"},
				Lifecycle: "DRAINING", MigrationStatus: "INTENT_BLOCKED", MigrationError: "missing vault config",
			}}})
		case "/heightquery":
			return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.HeightResponse{LastAcceptedHeight: 321})
		case "/frost/vault/list":
			return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.FrostVaultStateList{})
		case "/frost/vault/funds":
			body := `{"funds":[]}`
			return &http.Response{StatusCode: http.StatusOK, ContentLength: int64(len(body)), Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
		default:
			t.Fatalf("unexpected node path %q", req.URL.Path)
			return nil, nil
		}
	})

	recorder := httptest.NewRecorder()
	srv.handleFrostDKGSessions(recorder, httptest.NewRequest(http.MethodGet, "/api/frost/dkg/list?node="+node, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var items []DKGSessionItem
	if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%d want 1", len(items))
	}
	item := items[0]
	if item.DkgAttempt != 3 || len(item.DkgDisqualified) != 1 || item.CurrentHeight != 321 || item.EstimatedBlockSecs == 0 {
		t.Fatalf("missing DKG diagnostics: %+v", item)
	}
	if item.MigrationStatus != "INTENT_BLOCKED" || item.MigrationError != "missing vault config" {
		t.Fatalf("missing migration diagnostics: %+v", item)
	}
}

func TestParseExplorerVaultAvailableKey(t *testing.T) {
	key := keys.KeyFrostVaultAvailableBalance("btc", "BTC", 0)
	chain, asset, vaultID, ok := parseExplorerVaultAvailableKey(key, "")
	if !ok || chain != "btc" || asset != "BTC" || vaultID != 0 {
		t.Fatalf("parse = (%q,%q,%d,%v), want (btc,BTC,0,true)", chain, asset, vaultID, ok)
	}
}

func TestAugmentWithdrawPlanningLogsAddsSignedMilestones(t *testing.T) {
	logs := augmentWithdrawPlanningLogs(&pb.FrostWithdrawState{
		Status:     "SIGNED",
		VaultId:    0,
		JobId:      "job_1",
		SignedTxId: "signed_tx_1",
		PlanningLogs: []*pb.FrostPlanningLog{
			{Step: "SelectVault", Status: "FAILED", Message: "old blocker", Timestamp: 100},
		},
	})
	if len(logs) != 4 {
		t.Fatalf("len(logs) = %d want 4", len(logs))
	}
	if logs[1].Step != "Claim" || logs[2].Step != "StartSigning" || logs[3].Step != "Signing" {
		t.Fatalf("unexpected synthetic steps: %s, %s, %s", logs[1].Step, logs[2].Step, logs[3].Step)
	}
}

func TestParseVaultActivityPageClampsPageSize(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/frost/vault/activity?page=2&page_size=999", nil)
	page, pageSize, offset := parseVaultActivityPage(req)
	if page != 2 || pageSize != 50 || offset != 50 {
		t.Fatalf("page=%d pageSize=%d offset=%d, want 2/50/50", page, pageSize, offset)
	}
}

func TestParseVaultActivityPageClampsOffset(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/frost/vault/activity?page=999999&page_size=50", nil)
	_, pageSize, offset := parseVaultActivityPage(req)
	if pageSize != 50 || offset != maxExplorerVaultActivityOffset {
		t.Fatalf("pageSize=%d offset=%d, want 50/%d", pageSize, offset, maxExplorerVaultActivityOffset)
	}
}

func TestFrostWithdrawLogNodesHasBoundedFanout(t *testing.T) {
	defaults := make([]string, explorerMaxFrostMonitorNodes+8)
	for i := range defaults {
		defaults[i] = string(rune('a' + i))
	}
	srv := &server{defaultNodes: defaults}
	nodes := srv.frostWithdrawLogNodes("primary")
	if len(nodes) != explorerMaxFrostMonitorNodes {
		t.Fatalf("node count = %d, want %d", len(nodes), explorerMaxFrostMonitorNodes)
	}
	if nodes[0] != "primary" {
		t.Fatalf("first node = %q, want primary", nodes[0])
	}
}

func TestHandleFrostVaultsShowsConfiguredGenesisVaultWithoutDKGSession(t *testing.T) {
	const node = "node.example:443"
	const vaultRef = "0x74a56D510426fAd9Ad5a149f5AAFE65B0924f3CB"
	members := []string{"member-1", "member-2", "member-3", "member-4"}
	configReads := 0
	srv := newFrostWithdrawV2TestServer([]string{node}, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/frost/vault/list":
			return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.FrostVaultStateList{States: []*pb.FrostVaultState{{
				Chain:             "base",
				VaultId:           0,
				KeyEpoch:          1,
				GroupPubkey:       []byte{1, 2, 3, 4},
				SignAlgo:          pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
				Status:            "ACTIVE",
				ActiveSinceHeight: 1,
				CommitteeMembers:  members,
			}}})
		case "/frost/vault/config":
			configReads++
			if req.URL.Query().Get("chain") != "base" {
				t.Fatalf("unexpected config chain %q", req.URL.Query().Get("chain"))
			}
			return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.FrostVaultConfig{
				Chain:             "base",
				SignAlgo:          pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
				VaultCount:        1,
				CommitteeSize:     4,
				ThresholdRatio:    0.75,
				VaultRefs:         []string{vaultRef},
				EvmChainId:        8453,
				AuthorityProtocol: 3,
				GuardianPubkey:    []byte{5, 6, 7, 8},
			})
		case "/frost/vault/funds":
			body := `{"funds":[{"chain":"base","asset":"BASE.USDC","vault_id":0,"total_balance":"1000000","available_balance":"1000000"}]}`
			return &http.Response{
				StatusCode:    http.StatusOK,
				ContentLength: int64(len(body)),
				Header:        http.Header{"Content-Type": []string{"application/json"}},
				Body:          io.NopCloser(strings.NewReader(body)),
				Request:       req,
			}, nil
		default:
			t.Fatalf("unexpected node path %q", req.URL.Path)
			return nil, nil
		}
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/frost/vault/list?node="+node, nil)
	srv.handleFrostVaults(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var items []FrostVaultOverviewItem
	if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items=%d want 1", len(items))
	}
	item := items[0]
	if item.Chain != "base" || item.Status != "ACTIVE" || item.KeyEpoch != 1 || item.VaultRef != vaultRef || item.ChainID != "8453" {
		t.Fatalf("unexpected vault overview: %+v", item)
	}
	if item.CommitteeThreshold != 3 || len(item.CommitteeMembers) != 4 {
		t.Fatalf("committee threshold/members=%d/%d want 3/4", item.CommitteeThreshold, len(item.CommitteeMembers))
	}
	if item.AuthorityProtocol != 3 || item.GuardianPubkey != "05060708" || item.GroupPubkey != "01020304" {
		t.Fatalf("unexpected dual custody authorities: %+v", item)
	}
	if configReads != 1 {
		t.Fatalf("config reads=%d want 1", configReads)
	}
	if len(item.VaultFunds) != 1 || item.ManagedAssets[0] != "BASE.USDC" {
		t.Fatalf("unexpected vault funds/assets: %+v / %+v", item.VaultFunds, item.ManagedAssets)
	}
}

func TestFrostVaultOverviewNetworkMetadata(t *testing.T) {
	for _, tc := range []struct {
		name    string
		chainID uint64
		want    string
	}{
		{"mainnet", 728126428, "728126428"},
		{"shasta", 2494104990, "2494104990"},
		{"nile", 3448148188, "3448148188"},
		{"unknown", 0, ""},
		{"full_uint64", ^uint64(0), "18446744073709551615"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const node = "node.example:443"
			var configReads atomic.Int32
			srv := newFrostWithdrawV2TestServer([]string{node}, func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/frost/vault/list":
					return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.FrostVaultStateList{States: []*pb.FrostVaultState{{Chain: "trx", VaultId: 0, Status: "ACTIVE"}}})
				case "/frost/vault/config":
					configReads.Add(1)
					return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.FrostVaultConfig{Chain: "trx", EvmChainId: tc.chainID})
				case "/frost/vault/funds":
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"funds":[]}`)), Header: make(http.Header), Request: req}, nil
				default:
					return nil, fmt.Errorf("unexpected node path %s", req.URL.Path)
				}
			})
			recorder := httptest.NewRecorder()
			srv.handleFrostVaults(recorder, httptest.NewRequest(http.MethodGet, "/api/frost/vault/list?node="+node, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var items []FrostVaultOverviewItem
			if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].ChainID != tc.want || configReads.Load() != 1 {
				t.Fatalf("items=%+v config reads=%d, want chain_id=%q with one config read", items, configReads.Load(), tc.want)
			}
			if tc.want == "" && strings.Contains(recorder.Body.String(), `"chain_id"`) {
				t.Fatalf("unknown network must omit chain_id: %s", recorder.Body.String())
			}
		})
	}
}

func TestFrostVaultGuardianOverviewReusesChainConfigAtScale(t *testing.T) {
	for _, count := range []int{19, 20, 21, 20000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			const node = "node.example:443"
			var configReads, totalReads atomic.Int32
			states := make([]*pb.FrostVaultState, count)
			for i := range states {
				states[i] = &pb.FrostVaultState{Chain: "base", VaultId: uint32(i), KeyEpoch: 1, Status: "ACTIVE", GroupPubkey: []byte{1, 2, 3, 4}}
			}
			srv := newFrostWithdrawV2TestServer([]string{node}, func(req *http.Request) (*http.Response, error) {
				totalReads.Add(1)
				switch req.URL.Path {
				case "/frost/vault/list":
					return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.FrostVaultStateList{States: states})
				case "/frost/vault/config":
					configReads.Add(1)
					return frostWithdrawV2ProtoResponse(req, http.StatusOK, &pb.FrostVaultConfig{Chain: "base", EvmChainId: 84532, AuthorityProtocol: 3, GuardianPubkey: []byte{5, 6, 7, 8}})
				case "/frost/vault/funds":
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"funds":[]}`)), Header: make(http.Header), Request: req}, nil
				default:
					return nil, fmt.Errorf("unexpected node path %s", req.URL.Path)
				}
			})
			recorder := httptest.NewRecorder()
			srv.handleFrostVaults(recorder, httptest.NewRequest(http.MethodGet, "/api/frost/vault/list?node="+node, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var items []FrostVaultOverviewItem
			if err := json.Unmarshal(recorder.Body.Bytes(), &items); err != nil {
				t.Fatal(err)
			}
			if len(items) != count || configReads.Load() != 1 || totalReads.Load() != 3 {
				t.Fatalf("vaults/config reads/total reads=%d/%d/%d want %d/1/3", len(items), configReads.Load(), totalReads.Load(), count)
			}
			for _, item := range items {
				if item.AuthorityProtocol != 3 || item.GuardianPubkey != "05060708" || item.ChainID != "84532" {
					t.Fatalf("missing guardian or network for vault %d", item.VaultID)
				}
			}
		})
	}
}

func TestExplorerVaultRefUsesNativeTRONAddress(t *testing.T) {
	const abiRef = "0xf920a6e6d583182d59b1f195e32bd9709606d48b"
	const tronRef = "TYgUNXGeg9orBcVi1npF3Rr2XZq3btr793"
	if got := explorerVaultRef("trx", abiRef); got != tronRef {
		t.Fatalf("TRON vault ref=%q want %q", got, tronRef)
	}
	if got := explorerVaultRef("trx", tronRef); got != tronRef {
		t.Fatalf("native TRON vault ref changed to %q", got)
	}
	if got := explorerVaultRef("base", abiRef); got != abiRef {
		t.Fatalf("non-TRON vault ref changed to %q", got)
	}
}
