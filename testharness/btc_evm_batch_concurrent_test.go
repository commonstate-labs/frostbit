package testharness

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dex/config"
	"dex/db"
	"dex/frost/artifact"
	bridgeasset "dex/frost/asset"
	chainpkg "dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/frost/core/dkg"
	frostruntime "dex/frost/runtime"
	"dex/frost/runtime/adapters"
	"dex/frost/runtime/planning"
	"dex/frost/runtime/services"
	sessionpkg "dex/frost/runtime/session"
	"dex/frost/runtime/types"
	"dex/frost/runtime/workers"
	"dex/keys"
	"dex/logs"
	"dex/pb"
	"dex/protocol"
	"dex/txverify"
	"dex/utils"
	"dex/vm"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	"golang.org/x/crypto/sha3"
	"google.golang.org/protobuf/proto"
)

type batchReadStats struct{ Points, Batches, Keys, Scans, Pages int }
type batchCountedReader struct {
	*adapters.StateDBReader
	mu    sync.Mutex
	stats batchReadStats
}

func (r *batchCountedReader) Get(key string) ([]byte, bool, error) {
	r.mu.Lock()
	r.stats.Points++
	r.mu.Unlock()
	return r.StateDBReader.Get(key)
}
func (r *batchCountedReader) GetMany(k []string) (map[string][]byte, error) {
	r.mu.Lock()
	r.stats.Batches++
	r.stats.Keys += len(k)
	r.mu.Unlock()
	return r.StateDBReader.GetMany(k)
}
func (r *batchCountedReader) Scan(prefix string, fn func(string, []byte) bool) error {
	r.mu.Lock()
	r.stats.Scans++
	r.mu.Unlock()
	return r.StateDBReader.Scan(prefix, fn)
}
func (r *batchCountedReader) ScanOrderedAfter(prefix, cursor string, limit int, reverse bool, fn func(string, []byte) bool) error {
	r.mu.Lock()
	r.stats.Pages++
	r.mu.Unlock()
	return r.StateDBReader.ScanOrderedAfter(prefix, cursor, limit, reverse, fn)
}
func (r *batchCountedReader) snapshot() batchReadStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

type batchSubmitter struct {
	types.TxSubmitter
	                                                                    
	pendingDB               *db.Manager
	claims                  chan *pb.FrostWithdrawClaimTx
	signed                  chan *pb.FrostWithdrawSignedTx
	mu                      sync.Mutex
	claimCount, signedCount int
}

func (s *batchSubmitter) SubmitWithdrawClaimTx(ctx context.Context, tx *pb.FrostWithdrawClaimTx) error {
	if err := txverify.Verify(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: tx}}, config.DefaultChainID); err != nil {
		return fmt.Errorf("claim envelope: %w", err)
	}
	if err := s.TxSubmitter.SubmitWithdrawClaimTx(ctx, tx); err != nil {
		return err
	}
	s.mu.Lock()
	s.claimCount++
	s.mu.Unlock()
	select {
	case s.claims <- proto.Clone(tx).(*pb.FrostWithdrawClaimTx):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *batchSubmitter) SubmitWithdrawSignedTx(ctx context.Context, tx *pb.FrostWithdrawSignedTx) error {
	if err := txverify.Verify(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: tx}}, config.DefaultChainID); err != nil {
		return fmt.Errorf("signed envelope: %w", err)
	}
	if s.pendingDB != nil {
		anyTx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: tx}}
		raw, err := proto.Marshal(anyTx)
		if err != nil {
			return err
		}
		s.pendingDB.EnqueueSet(keys.KeyPendingAnyTx(tx.GetBase().GetTxId()), string(raw))
		if err := s.pendingDB.ForceFlush(); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.signedCount++
	s.mu.Unlock()
	select {
	case s.signed <- proto.Clone(tx).(*pb.FrostWithdrawSignedTx):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type batchQuietLogs struct{}

func (batchQuietLogs) ReportWithdrawPlanningLog(context.Context, *pb.FrostWithdrawPlanningLogTx) error {
	return nil
}

func batchMustProto(t *testing.T, m proto.Message) []byte {
	t.Helper()
	raw, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func batchMustRead(t *testing.T, h *Harness, key string) []byte {
	t.Helper()
	raw, ok := h.RawGet(key)
	if !ok {
		t.Fatalf("missing %s", key)
	}
	return raw
}
func batchReadUint(t *testing.T, h *Harness, key string) uint64 {
	t.Helper()
	n, err := strconv.ParseUint(string(batchMustRead(t, h, key)), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func batchMineOK(t *testing.T, h *Harness, txs ...*pb.AnyTx) *vm.SpecResult {
	t.Helper()
	spec, err := h.MineBlock(txs...)
	if err != nil {
		t.Fatal(err)
	}
	for _, rc := range spec.Receipts {
		if rc.Status != "SUCCEED" {
			t.Fatalf("receipt %s: %s %s", rc.TxID, rc.Status, rc.Error)
		}
	}
	return spec
}
func batchReceiveSigned(t *testing.T, ctx context.Context, s *batchSubmitter, n *batchRoastNetwork) map[string]*pb.FrostWithdrawSignedTx {
	t.Helper()
	out := map[string]*pb.FrostWithdrawSignedTx{}
	for len(out) < 2 {
		select {
		case tx := <-s.signed:
			if out[tx.Chain] != nil {
				t.Fatal("duplicate signed submission")
			}
			out[tx.Chain] = tx
		case err := <-n.errors:
			t.Fatal(err)
		case <-ctx.Done():
			t.Fatal("timed out waiting for signed batches")
		}
	}
	return out
}

                                                                         
                                                                             
                                                                                
                                                                             
func TestConcurrentBTC256EVM64WithdrawalFlow(t *testing.T) {
	                                                                            
	previousChainID := pb.GetDefaultChainID()
	pb.SetDefaultChainID(config.DefaultChainID)
	defer pb.SetDefaultChainID(previousChainID)
	for _, background := range []int{0, 20000} {
		t.Run(fmt.Sprintf("unrelated_%d", background), func(t *testing.T) {
			h := New(t, 4)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			user := h.Committee[0]
			for _, member := range h.Committee {
				h.SeedAccount(member, "FB", "10000000000")
			}
			provider := &batchVaultProvider{pubkeys: map[string][]byte{}}
			for i, member := range h.Committee {
				provider.committee = append(provider.committee, types.SignerInfo{ID: types.NodeID(member), Index: uint32(i)})
			}
			polys := map[string]*dkg.Polynomial{}
			groups := map[string]curve.Group{"btc": curve.NewSecp256k1Group(), "eth": curve.NewBN256Group()}
			for name, group := range groups {
				polys[name] = &dkg.Polynomial{Coefficients: []*big.Int{big.NewInt(7), big.NewInt(11), big.NewInt(13)}}
				provider.pubkeys[name] = group.SerializePoint(group.ScalarBaseMult(polys[name].Coefficients[0]))
			}
			btcPub := groups["btc"].ScalarBaseMult(polys["btc"].Coefficients[0])
			btcAddress, err := utils.DualCustodyTaprootAddress(btcPub.X.FillBytes(make([]byte, 32)), harnessGuardianPubkey("btc"), &chaincfg.TestNet3Params)
			if err != nil {
				t.Fatal(err)
			}
			script, err := btc.AddressScript(btcAddress)
			if err != nil {
				t.Fatal(err)
			}
			btcAsset, err := bridgeasset.Resolve("btc", "")
			if err != nil {
				t.Fatal(err)
			}
			ethAsset, err := bridgeasset.Resolve("eth", "0x3333333333333333333333333333333333333333")
			if err != nil {
				t.Fatal(err)
			}
			assets := map[string]string{"btc": btcAsset.ID, "eth": ethAsset.ID}
			counts := map[string]int{"btc": 256, "eth": 64}
			const gross = 100000
			for _, name := range []string{"btc", "eth"} {
				identity := btcAsset
				if name == "eth" {
					identity = ethAsset
				}
				total := strconv.Itoa(counts[name] * gross)
				h.RawSet(keys.KeyToken(identity.ID), batchMustProto(t, &pb.Token{Address: identity.ID, Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: name, NativeContract: identity.Contract, TotalSupply: total}))
				h.RawSet(keys.KeyNativeTokenIndex(name, identity.Contract), []byte(identity.ID))
				h.SeedAccount(user, identity.ID, total)
				algo := chainpkg.SignAlgoBTC
				if name == "eth" {
					algo = chainpkg.SignAlgoETH
				}
				cfg := localVaultConfig(name)
				cfg.SignAlgo, cfg.BtcNetwork, cfg.BtcFeeSatsPerVbyte = algo, "testnet3", 25
				cfg.EvmChainId, cfg.VaultRefs = 1, []string{"0x1111111111111111111111111111111111111111"}
				if err := config.ValidateOnChainVaultCommitteePolicy(cfg); err != nil {
					t.Fatalf("%s vault policy fixture: %v", name, err)
				}
				h.RawSet(keys.KeyFrostVaultConfig(name, 0), batchMustProto(t, cfg))
				h.RawSet(keys.KeyFrostVaultState(name, 0), batchMustProto(t, &pb.FrostVaultState{Chain: name, VaultId: 0, KeyEpoch: 1, Status: "ACTIVE", SignAlgo: algo, GroupPubkey: provider.pubkeys[name], CommitteeMembers: h.Committee, CommitteeThreshold: 3, ActiveSinceHeight: 1, NextCommitteeReviewHeight: 1 + protocol.VaultCommitteeTermBlocks}))
				available := total
				if name == "btc" {
					available = "30000000"
				}
				h.RawSet(keys.KeyFrostVaultAvailableBalance(name, identity.ID, 0), []byte(available))
			}
			for i, amount := range []string{"10000000", "20000000"} {
				txid := fmt.Sprintf("%064x", i+1)
				u := batchMustProto(t, &pb.FrostUtxo{Txid: txid, Amount: amount, VaultId: 0, FinalizeHeight: 1, ScriptPubkey: script})
				h.RawSet(keys.KeyFrostBtcUtxo(0, txid, 0), u)
				h.RawSet(keys.KeyFrostBtcUtxoFIFOIndex(0, uint64(i+1)), u)
				h.RawSet(keys.KeyFrostBtcSpendableUtxoIndex(0, uint64(i+1)), u)
				h.RawSet(keys.KeyFrostBtcUtxoSeqRef(0, txid, 0), []byte(strconv.Itoa(i+1)))
			}
			h.RawSet(keys.KeyFrostBtcUtxoFIFOSeq(0), []byte("2"))
			                                                               
			                                                                         
			for i := 0; i < background; i++ {
				h.DB.EnqueueSet(keys.KeyFrostWithdraw(fmt.Sprintf("unrelated-%d", i)), string(batchMustProto(t, &pb.FrostWithdrawState{WithdrawId: fmt.Sprintf("unrelated-%d", i), Status: "CANCELLED", Chain: "bnb", Asset: "other"})))
			}
			if err := h.DB.ForceFlush(); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 256; i++ {
				for _, name := range []string{"btc", "eth"} {
					if i >= counts[name] {
						continue
					}
					recipientPub := groups["btc"].ScalarBaseMult(big.NewInt(int64(1000 + i)))
					to, err := utils.TaprootAddressFromXOnly(recipientPub.X.FillBytes(make([]byte, 32)), &chaincfg.TestNet3Params)
					if err != nil {
						t.Fatal(err)
					}
					if name == "eth" {
						to = fmt.Sprintf("0x%040x", 3000+i)
					}
					tx := &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawRequestTx{FrostWithdrawRequestTx: &pb.FrostWithdrawRequestTx{Base: &pb.BaseMessage{TxId: fmt.Sprintf("%s-request-%d", name, i), FromAddress: user, Fee: "1000", ChainId: config.DefaultChainID}, Chain: name, Asset: assets[name], To: to, Amount: "100000"}}}
					if err := h.QueueTx(tx); err != nil {
						t.Fatal(err)
					}
				}
			}
			                                                                           
			h.mu.Lock()
			for _, tx := range h.queue {
				if _, err := txverify.SignWithPrivateKey(tx, h.PrivateKeys[user]); err != nil {
					h.mu.Unlock()
					t.Fatal(err)
				}
				if err := txverify.Verify(tx, config.DefaultChainID); err != nil {
					h.mu.Unlock()
					t.Fatal(err)
				}
			}
			h.mu.Unlock()
			requestStart := time.Now()
			requestSpec := batchMineOK(t, h)
			requestTime := time.Since(requestStart)
			if len(requestSpec.Receipts) != 320 {
				t.Fatalf("request receipts=%d", len(requestSpec.Receipts))
			}
			for _, name := range []string{"btc", "eth"} {
				h.AssertBalance(user, assets[name], "0")
				var token pb.Token
				if err := proto.Unmarshal(batchMustRead(t, h, keys.KeyToken(assets[name])), &token); err != nil || token.TotalSupply != "0" {
					t.Fatalf("supply %s: %+v %v", name, &token, err)
				}
			}
			var userAccount pb.Account
			if err := proto.Unmarshal(batchMustRead(t, h, keys.KeyAccount(user)), &userAccount); err != nil || userAccount.Nonce != 320 {
				t.Fatalf("request nonce=%d err=%v", userAccount.Nonce, err)
			}

			reader := &batchCountedReader{StateDBReader: adapters.NewStateDBReader(h.DB)}
			factory := chainpkg.NewDefaultAdapterFactory()
			factory.RegisterAdapter(btc.NewBTCAdapter("testnet"))
			factory.RegisterAdapter(evm.NewETHAdapter())
			jobs := map[string]*planning.Job{}
			planStart := time.Now()
			for _, name := range []string{"btc", "eth"} {
				planner := planning.NewJobWindowPlanner(reader, factory, 1)
				planned, planningLogs, err := planner.PlanJobWindow(name, assets[name])
				if err != nil || len(planned) != 1 || len(planned[0].WithdrawIDs) != counts[name] {
					t.Fatalf("%s full fresh batch: jobs=%+v logs=%+v err=%v", name, planned, planningLogs, err)
				}
				jobs[name] = planned[0]
			}
			planTime := time.Since(planStart)
			planStats := reader.snapshot()
			if planStats.Points > 32 || planStats.Batches > 10 || planStats.Keys > 800 || planStats.Scans != 0 || planStats.Pages > 2 {
				t.Fatalf("planner scales with backlog: %+v", planStats)
			}
			net := newBatchRoastNetwork()
			submitter := &batchSubmitter{TxSubmitter: h.TxSubmitter(), claims: make(chan *pb.FrostWithdrawClaimTx, 2), signed: make(chan *pb.FrostWithdrawSignedTx, 2)}
			signers := map[string]types.SigningService{}
			for i, member := range h.Committee {
				localDB, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("batch-nonce", 0))
				if err != nil {
					t.Fatal(err)
				}
				localDB.InitWriteQueue(128, time.Hour)
				t.Cleanup(func() { localDB.Close() })
				store, err := adapters.NewDBLocalShareStoreWithExternalWrapKey(localDB, bytes.Repeat([]byte{byte(i + 1)}, 32))
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"btc", "eth"} {
					share := polys[name].Evaluate(big.NewInt(int64(i+1)), groups[name])
					if err := store.SaveLocalShare(name, 0, 1, share.FillBytes(make([]byte, 32))); err != nil {
						t.Fatal(err)
					}
				}
				manager := frostruntime.NewManager(frostruntime.ManagerConfig{NodeID: types.NodeID(member), MaxInFlightPerChainAsset: 1}, frostruntime.ManagerDeps{StateReader: reader, TxSubmitter: submitter, RoastMessenger: net, VaultProvider: provider, AdapterFactory: factory, CryptoFactory: adapters.NewDefaultCryptoExecutorFactory(), LocalShareStore: store, NonceStore: adapters.NewDBNonceStore(localDB), LocalIdentity: h.KeyManagers[member], LogReporter: batchQuietLogs{}, Logger: logs.NewNodeLogger("batch-roast", 0)})
				net.nodes[types.NodeID(member)] = manager
				signers[member] = services.NewDualCustodySigningService(services.NewRoastSigningService(manager.GetCoordinator(), manager.GetParticipant(), provider), reader, batchGuardianSigner(t, reader))
			}
			t.Cleanup(func() {
				cancel()
				for _, service := range signers {
					for _, job := range jobs {
						_ = service.CancelSession(job.JobID)
					}
				}
				for _, manager := range net.nodes {
					for _, job := range jobs {
						manager.GetCoordinator().CloseSession(job.JobID)
						manager.GetParticipant().CloseSession(job.JobID)
					}
				}
				net.wg.Wait()
			})
			net.run(ctx)
			activeWorkers := map[string]*workers.WithdrawWorker{}
			flowStart := time.Now()
			for _, name := range []string{"btc", "eth"} {
				coord, err := chainpkg.WithdrawCoordinator(jobs[name].JobID, 1, h.Committee)
				if err != nil {
					t.Fatal(err)
				}
				worker := workers.NewWithdrawWorker(reader, factory, submitter, batchQuietLogs{}, signers[coord], provider, 1, coord, h.KeyManagers[coord], logs.NewNodeLogger("batch-worker", 0))
				activeWorkers[name] = worker
				planned, err := worker.ProcessWindow(ctx, name, assets[name])
				if err != nil || len(planned) != 1 || planned[0].JobID != jobs[name].JobID {
					t.Fatalf("worker %s: %+v %v", name, planned, err)
				}
			}
			claims := map[string]*pb.FrostWithdrawClaimTx{}
			for len(claims) < 2 {
				select {
				case tx := <-submitter.claims:
					claims[tx.Chain] = tx
				case <-ctx.Done():
					t.Fatal("claim submission timeout")
				}
			}
			batchMineOK(t, h)
			                                                                    
			batchMineOK(t, h, &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: claims["btc"]}}, &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawClaimTx{FrostWithdrawClaimTx: claims["eth"]}})
			for i := 1; i <= 2; i++ {
				if raw := batchMustRead(t, h, keys.KeyFrostBtcLockedUtxo(0, fmt.Sprintf("%064x", i), 0)); string(raw) != jobs["btc"].JobID {
					t.Fatal("BTC input not reserved by exact job")
				}
			}
			if string(batchMustRead(t, h, keys.KeyFrostVaultReservedBalance("eth", assets["eth"], 0))) != "6400000" {
				t.Fatal("EVM gross reservation missing")
			}
			select {
			case <-net.bothStarted:
			case err := <-net.errors:
				t.Fatal(err)
			case <-ctx.Done():
				t.Fatal("two signing sessions were not simultaneously live")
			}
			for name, job := range jobs {
				coord, _ := chainpkg.WithdrawCoordinator(job.JobID, 1, h.Committee)
				session := net.nodes[types.NodeID(coord)].GetCoordinator().GetSession(job.JobID)
				if session == nil || session.GetState() == sessionpkg.SignSessionStateComplete {
					t.Fatalf("%s session was not in flight behind barrier", name)
				}
			}
			close(net.gate)
			signed := batchReceiveSigned(t, ctx, submitter, net)
			net.mu.Lock()
			for _, name := range []string{"btc", "eth"} {
				if len(net.shares[name]) < 3 {
					net.mu.Unlock()
					t.Fatalf("%s did not use real 3-of-4 partial shares", name)
				}
			}
			net.mu.Unlock()
			                                                                        
			protected := map[string][]byte{}
			for _, job := range jobs {
				protected[keys.KeyFrostWithdrawJob(job.JobID)] = batchMustRead(t, h, keys.KeyFrostWithdrawJob(job.JobID))
				for _, id := range job.WithdrawIDs {
					protected[keys.KeyFrostWithdraw(id)] = batchMustRead(t, h, keys.KeyFrostWithdraw(id))
				}
			}
			for _, key := range []string{
				keys.KeyFrostBtcLockedUtxo(0, fmt.Sprintf("%064x", 1), 0),
				keys.KeyFrostBtcLockedUtxo(0, fmt.Sprintf("%064x", 2), 0),
				keys.KeyFrostVaultReservedBalance("eth", assets["eth"], 0),
				keys.KeyFrostVaultAvailableBalance("btc", assets["btc"], 0),
				keys.KeyFrostVaultAvailableBalance("eth", assets["eth"], 0),
				keys.KeyToken(assets["btc"]), keys.KeyToken(assets["eth"]),
			} {
				protected[key] = batchMustRead(t, h, key)
			}
			                                                                      
			for _, name := range []string{"btc", "eth"} {
				bad := proto.Clone(signed[name]).(*pb.FrostWithdrawSignedTx)
				bad.Base.TxId = "bad-" + name
				if name == "btc" {
					bad.InputSigs[1][0] ^= 1
					bad.SignedPackageBytes = bytes.Join(bad.InputSigs, nil)
				} else {
					bad.SignedPackageBytes[95] ^= 1
				}
				spec, err := h.MineBlock(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: bad}})
				if err != nil {
					t.Fatal(err)
				}
				if len(spec.Receipts) != 1 || spec.Receipts[0].Status != "FAILED" || !strings.Contains(spec.Receipts[0].Error, "signature verification failed") {
					t.Fatalf("tampered %s batch did not reach cryptographic rejection: %+v", name, spec.Receipts)
				}
				for key, raw := range protected {
					if !bytes.Equal(raw, batchMustRead(t, h, key)) {
						t.Fatalf("bad %s signature changed %s", name, key)
					}
				}
				for _, job := range jobs {
					for _, id := range job.WithdrawIDs {
						if _, ok := h.RawGet(keys.KeyFrostPermanentWithdrawArtifact(id)); ok {
							t.Fatal("invalid signature published withdrawal")
						}
					}
				}
			}
			signedSpec := batchMineOK(t, h, &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed["btc"]}}, &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed["eth"]}})
			flowTime := time.Since(flowStart)
			if len(signedSpec.Receipts) != 2 {
				t.Fatalf("Signed receipts=%d", len(signedSpec.Receipts))
			}
			submitter.mu.Lock()
			claimCount, signedCount := submitter.claimCount, submitter.signedCount
			submitter.mu.Unlock()
			if claimCount != 2 || signedCount != 2 {
				t.Fatalf("runtime submissions: Claims=%d Signed=%d", claimCount, signedCount)
			}
			records := map[string]*artifact.Record{}
			btcRecipients := make([]string, 0, 256)
			seenIDs := map[string]bool{}
			for _, name := range []string{"btc", "eth"} {
				var artifactID string
				for i, id := range jobs[name].WithdrawIDs {
					if seenIDs[id] {
						t.Fatal("cross-chain/duplicate withdrawal ID")
					}
					seenIDs[id] = true
					var state pb.FrostWithdrawState
					if err := proto.Unmarshal(batchMustRead(t, h, keys.KeyFrostPermanentWithdraw(id)), &state); err != nil {
						t.Fatal(err)
					}
					if state.Status != "SIGNED" || state.Chain != name || state.Asset != assets[name] || state.Seq != uint64(i+1) {
						t.Fatalf("wrong permanent withdrawal %+v", &state)
					}
					if name == "eth" && (state.GrossAmount != "100000" || state.FeeAmount != "10" || state.NetAmount != "99990") {
						t.Fatalf("EVM gross/fee/net %+v", &state)
					}
					if name == "btc" {
						btcRecipients = append(btcRecipients, state.To)
					}
					ref := string(batchMustRead(t, h, keys.KeyFrostPermanentWithdrawArtifact(id)))
					if i == 0 {
						artifactID = ref
					} else if ref != artifactID {
						t.Fatal("batch members have different artifacts")
					}
					if _, ok := h.RawGet(keys.KeyFrostWithdraw(id)); ok {
						t.Fatal("finalized withdrawal remained hot")
					}
				}
				record, err := artifact.UnmarshalRecord(batchMustRead(t, h, keys.KeyFrostPermanentArtifact(artifactID)))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.EqualFold(record.Metadata.Chain, name) || record.Metadata.SourceID != jobs[name].JobID || record.Metadata.Epoch != 1 {
					t.Fatalf("wrong artifact %+v", record.Metadata)
				}
				records[name] = record
			}
			if len(seenIDs) != 320 {
				t.Fatalf("completed IDs=%d", len(seenIDs))
			}
			var rawBTC wire.MsgTx
			if err := rawBTC.Deserialize(bytes.NewReader(records["btc"].RawTx)); err != nil {
				t.Fatal(err)
			}
			if len(rawBTC.TxOut) != 257 || len(rawBTC.TxIn) != 2 {
				t.Fatalf("BTC inputs=%d outputs=%d", len(rawBTC.TxIn), len(rawBTC.TxOut))
			}
			for i, input := range rawBTC.TxIn {
				if len(input.Witness) != 4 || !bytes.Equal(input.Witness[1], signed["btc"].InputSigs[i]) || !bytes.Equal(input.Witness[0], signed["btc"].GuardianSigs[i]) || input.PreviousOutPoint.Hash.String() != fmt.Sprintf("%064x", i+1) || input.PreviousOutPoint.Index != 0 {
					t.Fatalf("BTC input %d did not preserve its verified signature/outpoint", i)
				}
			}
			var recipientTotal int64
			for i, out := range rawBTC.TxOut[:256] {
				wantScript, err := btc.AddressScript(btcRecipients[i])
				if err != nil || !bytes.Equal(wantScript, out.PkScript) || out.Value <= 0 {
					t.Fatalf("BTC recipient %d changed: %v", i, err)
				}
				recipientTotal += out.Value
			}
			fee := int64(30000000) - recipientTotal - rawBTC.TxOut[256].Value
			if recipientTotal+fee != 256*gross || rawBTC.TxOut[256].Value != 4400000 || fee <= 0 {
				t.Fatal("BTC gross/fee/change conservation")
			}
			for i := 1; i <= 2; i++ {
				if _, ok := h.RawGet(keys.KeyFrostBtcLockedUtxo(0, fmt.Sprintf("%064x", i), 0)); ok {
					t.Fatal("BTC input lock survived signed")
				}
				if _, ok := h.RawGet(keys.KeyFrostBtcUtxo(0, fmt.Sprintf("%064x", i), 0)); ok {
					t.Fatal("spent input remains available")
				}
			}
			change := &pb.FrostUtxo{}
			if err := proto.Unmarshal(batchMustRead(t, h, keys.KeyFrostBtcUtxo(0, rawBTC.TxHash().String(), 256)), change); err != nil || change.Amount != "4400000" {
				t.Fatalf("change not indexed: %+v %v", change, err)
			}
			if batchReadUint(t, h, keys.KeyFrostWithdrawFIFOHead("btc", assets["btc"])) != 257 || batchReadUint(t, h, keys.KeyFrostWithdrawLiveHead("eth", 0)) != 65 || batchReadUint(t, h, keys.KeyFrostWithdrawLivePending("eth", 0)) != 0 {
				t.Fatal("FIFO/pending did not fully advance")
			}
			if string(batchMustRead(t, h, keys.KeyFrostVaultReservedBalance("eth", assets["eth"], 0))) != "0" {
				t.Fatal("EVM reservation not consumed")
			}
			if _, ok := h.RawGet(keys.KeyFrostWithdrawActiveVaultJob("eth", 0)); ok {
				t.Fatal("EVM active job retained")
			}
			ethTemplate, err := evm.FromJSON(jobs["eth"].TemplateData)
			if err != nil {
				t.Fatal(err)
			}
			if len(ethTemplate.Outputs) != 64 || ethTemplate.BatchSeq != "1" {
				t.Fatal("EVM template wrong batch")
			}
			for _, out := range ethTemplate.Outputs {
				if out.GrossAmount != "100000" || out.RewardIndex != 0 {
					t.Fatalf("EVM fee/net mismatch %+v", out)
				}
			}
			batchCheckEVMCalldata(t, records["eth"].RawTx, signed["eth"].SignedPackageBytes, signed["eth"].GuardianSigs[0], jobs["eth"].WithdrawIDs, 1, 0)
			if batchReadUint(t, h, keys.KeyFrostVaultDEXNextBatchSeq("eth", 0)) != 2 {
				t.Fatal("EVM sequence not consumed")
			}
			                                                                            
			before := make(map[string][]byte)
			for _, record := range records {
				before[record.Metadata.ArtifactID] = append([]byte(nil), batchMustRead(t, h, keys.KeyFrostPermanentArtifact(record.Metadata.ArtifactID))...)
			}
			replay, err := h.MineBlock(&pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed["btc"]}}, &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed["eth"]}})
			if err != nil {
				t.Fatal(err)
			}
			if len(replay.Receipts) != 2 {
				t.Fatalf("replay receipts=%d", len(replay.Receipts))
			}
			                                                                  
			                                                                   
			for _, rc := range replay.Receipts {
				if rc.Status != "FAILED" || !strings.Contains(rc.Error, "withdraw not found") {
					t.Fatalf("late Signed replay: %+v", rc)
				}
			}
			for id, raw := range before {
				if !bytes.Equal(raw, batchMustRead(t, h, keys.KeyFrostPermanentArtifact(id))) {
					t.Fatal("protocol replay changed artifact")
				}
			}
			if batchReadUint(t, h, keys.KeyFrostWithdrawFIFOHead("btc", assets["btc"])) != 257 || batchReadUint(t, h, keys.KeyFrostWithdrawLiveHead("eth", 0)) != 65 || string(batchMustRead(t, h, keys.KeyFrostVaultReservedBalance("eth", assets["eth"], 0))) != "0" {
				t.Fatal("late replay changed queue or reservation")
			}
			if !bytes.Equal(batchMustRead(t, h, keys.KeyFrostBtcUtxo(0, rawBTC.TxHash().String(), 256)), batchMustProto(t, change)) {
				t.Fatal("late replay changed BTC change UTXO")
			}
			for _, name := range []string{"btc", "eth"} {
				again, err := activeWorkers[name].ProcessWindow(ctx, name, assets[name])
				if err != nil || len(again) != 0 {
					t.Fatalf("completed batch replanned: %s %+v %v", name, again, err)
				}
				h.AssertBalance(user, assets[name], "0")
				var token pb.Token
				if err := proto.Unmarshal(batchMustRead(t, h, keys.KeyToken(assets[name])), &token); err != nil || token.TotalSupply != "0" {
					t.Fatal("replay changed supply")
				}
			}
			stats := reader.snapshot()
			if stats.Scans > 2 || stats.Points > 200 || stats.Batches > 50 || stats.Keys > 6000 || stats.Pages > 4 {
				t.Fatalf("runtime read amplification: %+v", stats)
			}
			                                                                        
			                                                                        
			if err := h.DB.ForceFlush(); err != nil {
				t.Fatal(err)
			}
			for _, job := range jobs {
				for _, id := range job.WithdrawIDs {
					for _, key := range []string{keys.KeyFrostPermanentWithdraw(id), keys.KeyFrostPermanentWithdrawArtifact(id)} {
						before[key] = append([]byte(nil), batchMustRead(t, h, key)...)
					}
				}
			}
			                                                                
			for _, record := range records {
				id := record.Metadata.ArtifactID
				before[keys.KeyFrostPermanentArtifact(id)] = before[id]
				delete(before, id)
			}
			changeKey := keys.KeyFrostBtcUtxo(0, rawBTC.TxHash().String(), 256)
			before[changeKey] = batchMustRead(t, h, changeKey)
			cancel()
			net.wg.Wait()
			h.WitnessService.Stop()
			h.DB.Close()
			reopened, err := db.NewManager(h.tmpDir, logs.NewNodeLogger("batch-reopen", 0))
			if err != nil {
				t.Fatal(err)
			}
			h.DB = reopened
			for key, raw := range before {
				if !bytes.Equal(raw, batchMustRead(t, h, key)) {
					t.Fatalf("reopened database changed %s", key)
				}
			}
			                                                                       
			                                                                        
			t.Logf("background=%d requests=320 request=%s requestDiffKeys=%d planner=%s plannerIO=%+v worker/ROAST/Signed=%s signedDiffKeys=%d runtimeIO=%+v BTCtasks=2 EVMtasks=1", background, requestTime, len(requestSpec.Diff), planTime, planStats, flowTime, len(signedSpec.Diff), stats)
		})
	}
}

                                                                            
                                                                         
func batchCheckEVMCalldata(t *testing.T, raw, signature, guardianSignature []byte, ids []string, batchSeq, firstRecipient int64) {
	t.Helper()
	const word = 32
	                                                                      
	                                                                  
	if len(ids) == 0 || len(ids) > 64 || len(signature) != 96 || len(guardianSignature) != 96 || len(raw) != 4+(7+7+2+1+5*len(ids))*word || !bytes.Equal(raw[:4], func() []byte {
		h := sha3.NewLegacyKeccak256()
		h.Write([]byte("withdrawBatch((bytes32,uint256,address,uint256,uint256,address[],(bytes32,address,address,uint256,uint256)[]),(uint256,uint256,uint256),(uint256,uint256,uint256))"))
		return h.Sum(nil)[:4]
	}()) {
		t.Fatalf("invalid EVM calldata: bytes=%d signature=%d items=%d", len(raw), len(signature), len(ids))
	}
	args := raw[4:]
	checkNumber := func(data []byte, index int, want int64) {
		t.Helper()
		if new(big.Int).SetBytes(data[index*word:(index+1)*word]).Cmp(big.NewInt(want)) != 0 {
			t.Fatalf("ABI word %d expected %d", index, want)
		}
	}
	checkNumber(args, 0, 7*word)
	if !bytes.Equal(args[word:4*word], signature) {
		t.Fatal("EVM calldata lost the verified Schnorr signature")
	}
	if !bytes.Equal(args[4*word:7*word], guardianSignature) {
		t.Fatal("EVM calldata lost guardian signature")
	}
	batch := args[7*word:]
	if hex.EncodeToString(batch[:word]) != "20f6a54ab25b05f8631eaed69087ed1b151fa46f2c2c6c9bd35470759a81c1dc" {
		t.Fatal("EVM calldata domain changed")
	}
	checkNumber(batch, 1, 1)                            
	checkNumber(batch, 3, 1)                    
	checkNumber(batch, 4, batchSeq)                  
	checkNumber(batch, 5, 7*word)
	checkNumber(batch, 6, 9*word)
	wantVault := append(make([]byte, 12), bytes.Repeat([]byte{0x11}, 20)...)
	wantToken := append(make([]byte, 12), bytes.Repeat([]byte{0x33}, 20)...)
	if !bytes.Equal(batch[2*word:3*word], wantVault) || !bytes.Equal(batch[8*word:9*word], wantToken) {
		t.Fatal("EVM vault/reward token changed")
	}
	checkNumber(batch, 7, 1)
	checkNumber(batch, 9, int64(len(ids)))
	for i, id := range ids {
		item := batch[(10+5*i)*word : (15+5*i)*word]
		hash := sha3.NewLegacyKeccak256()
		_, _ = hash.Write([]byte(id))
		if !bytes.Equal(item[:word], hash.Sum(nil)) || !bytes.Equal(item[word:2*word], wantToken) {
			t.Fatalf("EVM item %d request ID or token changed", i)
		}
		checkNumber(item, 2, 3000+firstRecipient+int64(i))
		checkNumber(item, 3, 100000)
		checkNumber(item, 4, 0)
	}
}
