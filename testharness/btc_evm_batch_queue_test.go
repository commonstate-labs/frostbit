package testharness

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
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

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	"google.golang.org/protobuf/proto"
)

type batchQueueFixture struct {
	h               *Harness
	ctx             context.Context
	cancel          context.CancelFunc
	assets          map[string]string
	requestIDs      map[string][]string
	reader          *batchCountedReader
	factory         chainpkg.ChainAdapterFactory
	net             *batchRoastNetwork
	submitter       *batchSubmitter
	workers         map[string]*workers.WithdrawWorker
	jobs            map[string]*planning.Job
	btcScript       []byte
	requestTime     time.Duration
	requestDiffKeys int
}

func newBatchQueueFixture(t *testing.T, background int) *batchQueueFixture {
	t.Helper()
	h := New(t, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
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
	counts := map[string]int{"btc": 1000, "eth": 500}
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
			available = "110000000"
		}
		h.RawSet(keys.KeyFrostVaultAvailableBalance(name, identity.ID, 0), []byte(available))
	}
	for i, amount := range []string{"110000000"} {
		txid := fmt.Sprintf("%064x", i+1)
		u := batchMustProto(t, &pb.FrostUtxo{Txid: txid, Amount: amount, VaultId: 0, FinalizeHeight: 1, ScriptPubkey: script})
		h.RawSet(keys.KeyFrostBtcUtxo(0, txid, 0), u)
		h.RawSet(keys.KeyFrostBtcUtxoFIFOIndex(0, uint64(i+1)), u)
		h.RawSet(keys.KeyFrostBtcSpendableUtxoIndex(0, uint64(i+1)), u)
		h.RawSet(keys.KeyFrostBtcUtxoSeqRef(0, txid, 0), []byte(strconv.Itoa(i+1)))
	}
	h.RawSet(keys.KeyFrostBtcUtxoFIFOSeq(0), []byte("1"))
	                                                               
	                                                                         
	for i := 0; i < background; i++ {
		h.DB.EnqueueSet(keys.KeyFrostWithdraw(fmt.Sprintf("unrelated-%d", i)), string(batchMustProto(t, &pb.FrostWithdrawState{WithdrawId: fmt.Sprintf("unrelated-%d", i), Status: "CANCELLED", Chain: "bnb", Asset: "other"})))
	}
	if err := h.DB.ForceFlush(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
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
	                                                                           
	requestIDs := map[string][]string{"btc": {}, "eth": {}}
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
		req := tx.GetFrostWithdrawRequestTx()
		requestIDs[req.Chain] = append(requestIDs[req.Chain], tx.GetTxId())
	}
	h.mu.Unlock()
	requestStart := time.Now()
	requestSpec := batchMineOK(t, h)
	requestTime := time.Since(requestStart)
	if len(requestSpec.Receipts) != 1500 {
		t.Fatalf("request receipts=%d", len(requestSpec.Receipts))
	}
	for _, name := range []string{"btc", "eth"} {
		h.AssertBalance(user, assets[name], "0")
		var token pb.Token
		if err := proto.Unmarshal(batchMustRead(t, h, keys.KeyToken(assets[name])), &token); err != nil || token.TotalSupply != "0" {
			t.Fatalf("supply %s: %+v %v", name, &token, err)
		}
	}
	                                                                     
	                                                                              
	for name, txIDs := range requestIDs {
		for i, txID := range txIDs {
			id := string(batchMustRead(t, h, keys.KeyFrostWithdrawTxRef(txID)))
			var state pb.FrostWithdrawState
			if err := proto.Unmarshal(batchMustRead(t, h, keys.KeyFrostWithdraw(id)), &state); err != nil || state.Seq != uint64(i+1) || state.Status != "QUEUED" || state.Chain != name {
				t.Fatalf("request mapping disagrees with submission order: %+v %v", &state, err)
			}
			requestIDs[name][i] = id
		}
	}
	var userAccount pb.Account
	if err := proto.Unmarshal(batchMustRead(t, h, keys.KeyAccount(user)), &userAccount); err != nil || userAccount.Nonce != 1500 {
		t.Fatalf("request nonce=%d err=%v", userAccount.Nonce, err)
	}

	reader := &batchCountedReader{StateDBReader: adapters.NewStateDBReader(h.DB)}
	factory := chainpkg.NewDefaultAdapterFactory()
	factory.RegisterAdapter(btc.NewBTCAdapter("testnet"))
	factory.RegisterAdapter(evm.NewETHAdapter())

	f := &batchQueueFixture{h: h, ctx: ctx, cancel: cancel, assets: assets, requestIDs: requestIDs, reader: reader, factory: factory, net: newBatchRoastNetwork(), workers: map[string]*workers.WithdrawWorker{}, jobs: map[string]*planning.Job{}, btcScript: script, requestTime: requestTime, requestDiffKeys: len(requestSpec.Diff)}
	f.submitter = &batchSubmitter{TxSubmitter: h.TxSubmitter(), pendingDB: h.DB, claims: make(chan *pb.FrostWithdrawClaimTx, 2), signed: make(chan *pb.FrostWithdrawSignedTx, 2)}
	signers := map[string]types.SigningService{}
	for i, member := range h.Committee {
		localDB, err := db.NewManager(t.TempDir(), logs.NewNodeLogger("queue-nonce", 0))
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
		manager := frostruntime.NewManager(frostruntime.ManagerConfig{NodeID: types.NodeID(member), MaxInFlightPerChainAsset: 1}, frostruntime.ManagerDeps{StateReader: reader, TxSubmitter: f.submitter, RoastMessenger: f.net, VaultProvider: provider, AdapterFactory: factory, CryptoFactory: adapters.NewDefaultCryptoExecutorFactory(), LocalShareStore: store, NonceStore: adapters.NewDBNonceStore(localDB), LocalIdentity: h.KeyManagers[member], LogReporter: batchQuietLogs{}, Logger: logs.NewNodeLogger("queue-roast", 0)})
		f.net.nodes[types.NodeID(member)] = manager
		service := services.NewDualCustodySigningService(services.NewRoastSigningService(manager.GetCoordinator(), manager.GetParticipant(), provider), reader, batchGuardianSigner(t, reader))
		signers[member] = service
		f.workers[member] = workers.NewWithdrawWorker(reader, factory, f.submitter, batchQuietLogs{}, service, provider, 1, member, h.KeyManagers[member], logs.NewNodeLogger("queue-worker", 0))
	}
	t.Cleanup(func() {
		cancel()
		for _, service := range signers {
			for id := range f.jobs {
				_ = service.CancelSession(id)
			}
		}
		for _, manager := range f.net.nodes {
			for id := range f.jobs {
				manager.GetCoordinator().CloseSession(id)
				manager.GetParticipant().CloseSession(id)
			}
		}
		f.net.wg.Wait()
	})
	f.net.run(ctx)
	return f
}

                                                                          
func (f *batchQueueFixture) agreedJob(t *testing.T, name string, want int) *planning.Job {
	t.Helper()
	var job *planning.Job
	for range f.h.Committee {
		planned, _, err := planning.NewJobWindowPlanner(f.reader, f.factory, 1).PlanJobWindow(name, f.assets[name])
		if err != nil {
			t.Fatal(err)
		}
		if want == 0 {
			if len(planned) != 0 {
				t.Fatalf("%s should wait, got %d jobs", name, len(planned))
			}
			continue
		}
		if len(planned) != 1 || len(planned[0].WithdrawIDs) != want {
			t.Fatalf("%s batch size: jobs=%+v want=%d", name, planned, want)
		}
		if job != nil && (planned[0].JobID != job.JobID || !slices.Equal(planned[0].WithdrawIDs, job.WithdrawIDs) || !bytes.Equal(planned[0].TemplateHash, job.TemplateHash) || !bytes.Equal(planned[0].TemplateData, job.TemplateData)) {
			t.Fatal("planning views disagree")
		}
		job = planned[0]
	}
	return job
}
func (f *batchQueueFixture) tick(t *testing.T, name string) []*planning.Job {
	t.Helper()
	var started []*planning.Job
	for _, member := range f.h.Committee {
		jobs, err := f.workers[member].ProcessWindow(f.ctx, name, f.assets[name])
		if err != nil {
			t.Fatal(err)
		}
		started = append(started, jobs...)
	}
	if len(started) > 1 {
		t.Fatalf("%s spawned multiple active pipelines", name)
	}
	return started
}

func TestConcurrentBTC1000EVM500QueuedWithdrawalFlow(t *testing.T) {
	previous := pb.GetDefaultChainID()
	pb.SetDefaultChainID(config.DefaultChainID)
	defer pb.SetDefaultChainID(previous)
	for _, background := range []int{0, 20000} {
		t.Run(fmt.Sprintf("unrelated_%d", background), func(t *testing.T) {
			f := newBatchQueueFixture(t, background)
			h := f.h
			startedAt := time.Now()
			completed := map[string]int{"btc": 0, "eth": 0}
			batches := map[string][]int{"btc": {}, "eth": {}}
			allIDs := map[string]bool{}
			durable := map[string][]byte{}
			btcPreviousTx := fmt.Sprintf("%064x", 1)
			btcPreviousVout := uint32(0)
			btcRemaining := int64(110000000)
			btcParent := ""
			var minerFees int64
			requestHeight := h.Height()
			var requestKeys []string
			for _, name := range []string{"btc", "eth"} {
				n := 1000
				if name == "eth" {
					n = 500
				}
				for i := 0; i < n; i++ {
					requestKeys = append(requestKeys, keys.KeyFrostWithdraw(f.requestIDs[name][i]))
				}
			}

			runWave := func(wantBTC, wantETH int, first bool) {
				t.Helper()
				beforeIO := f.reader.snapshot()
				wave := map[string]*planning.Job{}
				wants := map[string]int{"btc": wantBTC, "eth": wantETH}
				for _, name := range []string{"btc", "eth"} {
					job := f.agreedJob(t, name, wants[name])
					if job == nil {
						continue
					}
					if job.FirstSeq != uint64(completed[name]+1) {
						t.Fatalf("%s skipped FIFO prefix", name)
					}
					for i, id := range job.WithdrawIDs {
						if id != f.requestIDs[name][completed[name]+i] {
							t.Fatalf("%s wrong batch ID %d: %s", name, i, id)
						}
					}
					if f.jobs[job.JobID] != nil {
						t.Fatal("job ID reused across batches")
					}
					f.jobs[job.JobID] = job
					wave[name] = job
					spawned := f.tick(t, name)
					if len(spawned) != 1 || spawned[0].JobID != job.JobID {
						t.Fatalf("%s coordinator did not start exact agreed job", name)
					}
				}
				claims := map[string]*pb.FrostWithdrawClaimTx{}
				for len(claims) < len(wave) {
					select {
					case tx := <-f.submitter.claims:
						job := wave[tx.Chain]
						if job == nil || claims[tx.Chain] != nil || tx.JobId != job.JobID || !slices.Equal(tx.WithdrawIds, job.WithdrawIDs) || !bytes.Equal(tx.TemplateHash, job.TemplateHash) {
							t.Fatal("Claim crossed batch boundary")
						}
						claims[tx.Chain] = tx
					case err := <-f.net.errors:
						t.Fatal(err)
					case <-f.ctx.Done():
						t.Fatal("Claim timeout")
					}
				}
				claimSpec := batchMineOK(t, h)
				if len(claimSpec.Receipts) != len(wave) {
					t.Fatal("wrong Claim receipt count")
				}
				if first {
					select {
					case <-f.net.bothStarted:
					case err := <-f.net.errors:
						t.Fatal(err)
					case <-f.ctx.Done():
						t.Fatal("both first sessions did not start")
					}
					for name, job := range wave {
						coord, err := chainpkg.WithdrawCoordinator(job.JobID, 1, h.Committee)
						if err != nil {
							t.Fatal(err)
						}
						session := f.net.nodes[types.NodeID(coord)].GetCoordinator().GetSession(job.JobID)
						if session == nil || session.GetState() == sessionpkg.SignSessionStateComplete {
							t.Fatalf("%s first session not simultaneously in flight", name)
						}
					}
				}
				                                                                         
				current, err := h.DB.GetKVs(requestKeys)
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"btc", "eth"} {
					n := 1000
					if name == "eth" {
						n = 500
					}
					for i := 0; i < n; i++ {
						id := f.requestIDs[name][i]
						raw := current[keys.KeyFrostWithdraw(id)]
						if i < completed[name] {
							if len(raw) != 0 {
								t.Fatal("completed request remained hot")
							}
							continue
						}
						var state pb.FrostWithdrawState
						if proto.Unmarshal(raw, &state) != nil || state.WithdrawId != id {
							t.Fatal("queued request missing")
						}
						if i < completed[name]+wants[name] {
							if state.Status != "SIGNING" || state.JobId != wave[name].JobID {
								t.Fatal("current prefix not reserved")
							}
						} else if state.Status != "QUEUED" || state.JobId != "" {
							t.Fatal("backlog signed out of order")
						}
					}
				}
				if string(batchMustRead(t, h, keys.KeyFrostVaultReservedBalance("eth", f.assets["eth"], 0))) != strconv.Itoa((500-completed["eth"])*100000) {
					t.Fatal("Claim changed existing EVM queue reservation")
				}
				for name, job := range wave {
					if name == "eth" && string(batchMustRead(t, h, keys.KeyFrostWithdrawActiveVaultJob("eth", 0))) != job.JobID {
						t.Fatal("EVM active job does not match current FIFO batch")
					}
					if name == "btc" && string(batchMustRead(t, h, keys.KeyFrostBtcLockedUtxo(0, btcPreviousTx, btcPreviousVout))) != job.JobID {
						t.Fatal("BTC current change not locked by exact job")
					}
					if recovered := f.agreedJob(t, name, len(job.WithdrawIDs)); recovered.JobID != job.JobID {
						t.Fatal("Claim recovery changed job")
					}
					if len(f.tick(t, name)) != 0 {
						t.Fatal("repeated tick spawned a second pipeline")
					}
				}
				if first {
					close(f.net.gate)
				}
				signed := map[string]*pb.FrostWithdrawSignedTx{}
				for len(signed) < len(wave) {
					select {
					case tx := <-f.submitter.signed:
						job := wave[tx.Chain]
						if job == nil || signed[tx.Chain] != nil || tx.JobId != job.JobID || !slices.Equal(tx.WithdrawIds, job.WithdrawIDs) || !bytes.Equal(tx.TemplateHash, job.TemplateHash) {
							t.Fatal("Signed crossed batch boundary")
						}
						signed[tx.Chain] = tx
					case err := <-f.net.errors:
						t.Fatal(err)
					case <-f.ctx.Done():
						t.Fatal("Signed timeout")
					}
				}
				var txs []*pb.AnyTx
				for _, name := range []string{"btc", "eth"} {
					job := wave[name]
					if job == nil {
						continue
					}
					f.net.mu.Lock()
					actualChain := f.net.sessionChains[job.JobID]
					signerCount := len(f.net.sessionShares[job.JobID])
					f.net.mu.Unlock()
					if actualChain != name || signerCount < 3 {
						t.Fatalf("job %s did not use three real shares on %s", job.JobID, name)
					}
					                                                                       
					                                                                          
					if len(f.tick(t, name)) != 0 {
						t.Fatal("pending Signed spawned another pipeline")
					}
					txs = append(txs, &pb.AnyTx{Content: &pb.AnyTx_FrostWithdrawSignedTx{FrostWithdrawSignedTx: signed[name]}})
				}
				spec := batchMineOK(t, h, txs...)
				if len(spec.Receipts) != len(wave) {
					t.Fatal("wrong Signed receipt count")
				}
				for _, tx := range txs {
					h.DB.EnqueueDelete(keys.KeyPendingAnyTx(tx.GetTxId()))
				}
				if err := h.DB.ForceFlush(); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"btc", "eth"} {
					job := wave[name]
					if job == nil {
						continue
					}
					firstIndex := completed[name]
					var artifactID string
					for i, id := range job.WithdrawIDs {
						if allIDs[id] {
							t.Fatal("request completed twice")
						}
						allIDs[id] = true
						var state pb.FrostWithdrawState
						key := keys.KeyFrostPermanentWithdraw(id)
						raw := batchMustRead(t, h, key)
						if err := proto.Unmarshal(raw, &state); err != nil {
							t.Fatal(err)
						}
						if state.Status != "SIGNED" || state.Chain != name || state.Asset != f.assets[name] || state.Seq != uint64(firstIndex+i+1) || state.JobId != job.JobID || state.GrossAmount != "100000" {
							t.Fatalf("wrong completed request: %+v", &state)
						}
						if name == "eth" && (state.FeeAmount != "10" || state.NetAmount != "99990" || !strings.EqualFold(state.To, fmt.Sprintf("0x%040x", 3000+firstIndex+i))) {
							t.Fatalf("wrong EVM gross/fee/net/recipient: %+v", &state)
						}
						refKey := keys.KeyFrostPermanentWithdrawArtifact(id)
						ref := batchMustRead(t, h, refKey)
						if i == 0 {
							artifactID = string(ref)
						} else if artifactID != string(ref) {
							t.Fatal("one batch produced different artifacts")
						}
						durable[key] = raw
						durable[refKey] = ref
						if _, ok := h.RawGet(keys.KeyFrostWithdraw(id)); ok {
							t.Fatal("completed request remained hot")
						}
					}
					artifactKey := keys.KeyFrostPermanentArtifact(artifactID)
					rawRecord := batchMustRead(t, h, artifactKey)
					record, err := artifact.UnmarshalRecord(rawRecord)
					if err != nil {
						t.Fatal(err)
					}
					if record.Metadata.SourceID != job.JobID || !strings.EqualFold(record.Metadata.Chain, name) || record.Metadata.Epoch != 1 {
						t.Fatal("artifact metadata crossed jobs")
					}
					durable[artifactKey] = rawRecord
					var archivedJob pb.FrostWithdrawJobState
					jobKey := keys.KeyFrostPermanentWithdrawJob(job.JobID)
					jobRaw := batchMustRead(t, h, jobKey)
					if proto.Unmarshal(jobRaw, &archivedJob) != nil || archivedJob.Status != "SIGNED" || !slices.Equal(archivedJob.WithdrawIds, job.WithdrawIDs) || !bytes.Equal(archivedJob.TemplateHash, job.TemplateHash) {
						t.Fatal("job archive lost agreed batch")
					}
					durable[jobKey] = jobRaw
					if _, ok := h.RawGet(keys.KeyFrostWithdrawJob(job.JobID)); ok {
						t.Fatal("completed job remained active")
					}
					if name == "btc" {
						var tx wire.MsgTx
						if err := tx.Deserialize(bytes.NewReader(record.RawTx)); err != nil {
							t.Fatal(err)
						}
						if len(tx.TxIn) != 1 || len(tx.TxOut) != len(job.WithdrawIDs)+1 || tx.TxIn[0].PreviousOutPoint.Hash.String() != btcPreviousTx || tx.TxIn[0].PreviousOutPoint.Index != btcPreviousVout || len(tx.TxIn[0].Witness) != 4 || len(signed[name].InputSigs) != 1 || !bytes.Equal(tx.TxIn[0].Witness[1], signed[name].InputSigs[0]) || !bytes.Equal(tx.TxIn[0].Witness[0], signed[name].GuardianSigs[0]) {
							t.Fatal("BTC did not consume exact previous change with verified signature")
						}
						if btcParent == "" {
							if len(record.Metadata.ParentIDs) != 0 {
								t.Fatal("unexpected initial BTC parent")
							}
						} else if !slices.Equal(record.Metadata.ParentIDs, []string{btcParent}) {
							t.Fatal("BTC artifact dependency chain broken")
						}
						var recipientTotal int64
						for i, out := range tx.TxOut[:len(job.WithdrawIDs)] {
							pub := curve.NewSecp256k1Group().ScalarBaseMult(big.NewInt(int64(1000 + firstIndex + i)))
							address, err := utils.TaprootAddressFromXOnly(pub.X.FillBytes(make([]byte, 32)), &chaincfg.TestNet3Params)
							if err != nil {
								t.Fatal(err)
							}
							script, err := btc.AddressScript(address)
							if err != nil || !bytes.Equal(out.PkScript, script) || out.Value <= 0 {
								t.Fatal("BTC recipient order or output changed")
							}
							recipientTotal += out.Value
						}
						change := tx.TxOut[len(job.WithdrawIDs)]
						fee := btcRemaining - recipientTotal - change.Value
						btcRemaining -= int64(len(job.WithdrawIDs) * 100000)
						if fee <= 0 || recipientTotal+fee != int64(len(job.WithdrawIDs)*100000) || change.Value != btcRemaining || !bytes.Equal(change.PkScript, f.btcScript) {
							t.Fatal("BTC batch gross/fee/change conservation")
						}
						minerFees += fee
						if _, ok := h.RawGet(keys.KeyFrostBtcUtxo(0, btcPreviousTx, btcPreviousVout)); ok {
							t.Fatal("spent BTC input retained")
						}
						if _, ok := h.RawGet(keys.KeyFrostBtcLockedUtxo(0, btcPreviousTx, btcPreviousVout)); ok {
							t.Fatal("spent BTC lock retained")
						}
						btcPreviousTx = tx.TxHash().String()
						btcPreviousVout = uint32(len(job.WithdrawIDs))
						btcParent = artifactID
						var utxo pb.FrostUtxo
						changeKey := keys.KeyFrostBtcUtxo(0, btcPreviousTx, btcPreviousVout)
						if proto.Unmarshal(batchMustRead(t, h, changeKey), &utxo) != nil || utxo.Amount != strconv.FormatInt(btcRemaining, 10) {
							t.Fatal("new change missing")
						}
						seq := uint64(len(batches[name]) + 2)
						if batchReadUint(t, h, keys.KeyFrostBtcUtxoSeqRef(0, btcPreviousTx, btcPreviousVout)) != seq {
							t.Fatal("change seq index wrong")
						}
						for _, key := range []string{keys.KeyFrostBtcUtxoFIFOIndex(0, seq), keys.KeyFrostBtcSpendableUtxoIndex(0, seq)} {
							var indexed pb.FrostUtxo
							if proto.Unmarshal(batchMustRead(t, h, key), &indexed) != nil || indexed.Txid != btcPreviousTx || indexed.Vout != btcPreviousVout || indexed.Amount != utxo.Amount {
								t.Fatal("change FIFO/spendable index wrong")
							}
						}
					} else {
						batchCheckEVMCalldata(t, record.RawTx, signed[name].SignedPackageBytes, signed[name].GuardianSigs[0], job.WithdrawIDs, int64(len(batches[name])+1), int64(firstIndex))
					}
					completed[name] += len(job.WithdrawIDs)
					batches[name] = append(batches[name], len(job.WithdrawIDs))
				}
				if batchReadUint(t, h, keys.KeyFrostWithdrawFIFOHead("btc", f.assets["btc"])) != uint64(completed["btc"]+1) || batchReadUint(t, h, keys.KeyFrostWithdrawLiveHead("eth", 0)) != uint64(completed["eth"]+1) || batchReadUint(t, h, keys.KeyFrostWithdrawLivePending("eth", 0)) != uint64(500-completed["eth"]) {
					t.Fatal("queue heads/pending diverged")
				}
				if string(batchMustRead(t, h, keys.KeyFrostVaultReservedBalance("eth", f.assets["eth"], 0))) != strconv.Itoa((500-completed["eth"])*100000) || batchReadUint(t, h, keys.KeyFrostVaultDEXNextBatchSeq("eth", 0)) != uint64(len(batches["eth"])+1) {
					t.Fatalf("EVM reservation/sequence: reserved=%s seq=%d expectedSeq=%d", batchMustRead(t, h, keys.KeyFrostVaultReservedBalance("eth", f.assets["eth"], 0)), batchReadUint(t, h, keys.KeyFrostVaultDEXNextBatchSeq("eth", 0)), len(batches["eth"])+1)
				}
				stats := f.reader.snapshot()
				delta := batchReadStats{Points: stats.Points - beforeIO.Points, Batches: stats.Batches - beforeIO.Batches, Keys: stats.Keys - beforeIO.Keys, Scans: stats.Scans - beforeIO.Scans, Pages: stats.Pages - beforeIO.Pages}
				if delta.Points > 500 || delta.Batches > 200 || delta.Keys > 20000 || delta.Scans > 4 || delta.Pages > 12 {
					t.Fatalf("per-wave read amplification: %+v", delta)
				}
				t.Logf("background=%d completedBTC=%d completedEVM=%d batches=%d/%d waveIO=%+v signedDiffKeys=%d", background, completed["btc"], completed["eth"], len(batches["btc"]), len(batches["eth"]), delta, len(spec.Diff))
			}
			for i := 0; i < 3; i++ {
				runWave(256, 64, i == 0)
			}
			for i := 0; i < 4; i++ {
				runWave(0, 64, false)
			}
			                                                                             
			tailHeight := requestHeight + chainpkg.BTCWithdrawBatchTimeoutBlocks
			if chainpkg.BTCWithdrawBatchTimeoutBlocks != chainpkg.AccountWithdrawBatchTimeoutBlocks || h.Height() >= tailHeight {
				t.Fatal("fixture passed tail timeout before checking boundary")
			}
			advanceStart := time.Now()
			h.AdvanceHeight(tailHeight - 1 - h.Height())
			advanceTime := time.Since(advanceStart)
			waitingBefore := f.reader.snapshot()
			f.agreedJob(t, "btc", 0)
			f.agreedJob(t, "eth", 0)
			if len(f.tick(t, "btc")) != 0 || len(f.tick(t, "eth")) != 0 {
				t.Fatal("underfull tail started one height early")
			}
			waitingAfter := f.reader.snapshot()
			if waitingAfter.Keys-waitingBefore.Keys > 6000 || waitingAfter.Pages-waitingBefore.Pages > 8 {
				t.Fatal("waiting tail scanned unbounded backlog")
			}
			if _, ok := h.RawGet(keys.KeyFrostBtcLockedUtxo(0, btcPreviousTx, btcPreviousVout)); ok {
				t.Fatal("waiting tail locked BTC change")
			}
			if string(batchMustRead(t, h, keys.KeyFrostVaultReservedBalance("eth", f.assets["eth"], 0))) != "5200000" {
				t.Fatal("waiting tail changed existing EVM queue reservation")
			}
			batchMineOK(t, h)
			if h.Height() != tailHeight {
				t.Fatal("timeout height mismatch")
			}
			runWave(232, 52, false)
			if !slices.Equal(batches["btc"], []int{256, 256, 256, 232}) || !slices.Equal(batches["eth"], []int{64, 64, 64, 64, 64, 64, 64, 52}) || len(allIDs) != 1500 || len(f.jobs) != 12 || btcRemaining != 10000000 || minerFees <= 0 {
				t.Fatal("final batch partition incomplete")
			}
			for _, name := range []string{"btc", "eth"} {
				f.agreedJob(t, name, 0)
				if len(f.tick(t, name)) != 0 {
					t.Fatal("completed queue replanned")
				}
				h.AssertBalance(h.Committee[0], f.assets[name], "0")
				var token pb.Token
				if proto.Unmarshal(batchMustRead(t, h, keys.KeyToken(f.assets[name])), &token) != nil || token.TotalSupply != "0" {
					t.Fatal("completed supply changed")
				}
			}
			f.submitter.mu.Lock()
			claims, signed := f.submitter.claimCount, f.submitter.signedCount
			f.submitter.mu.Unlock()
			if claims != 12 || signed != 12 {
				t.Fatalf("duplicate/missing submission Claims=%d Signed=%d", claims, signed)
			}
			f.net.mu.Lock()
			sessions := len(f.net.sessionChains)
			f.net.mu.Unlock()
			if sessions != 12 {
				t.Fatalf("real sessions=%d", sessions)
			}
			stats := f.reader.snapshot()
			if stats.Keys > 120000 || stats.Batches > 1500 || stats.Points > 4000 || stats.Pages > 100 || stats.Scans > 30 {
				t.Fatalf("queue runtime read amplification: %+v", stats)
			}
			changeKey := keys.KeyFrostBtcUtxo(0, btcPreviousTx, btcPreviousVout)
			durable[changeKey] = batchMustRead(t, h, changeKey)
			if err := h.DB.ForceFlush(); err != nil {
				t.Fatal(err)
			}
			f.cancel()
			f.net.wg.Wait()
			h.WitnessService.Stop()
			h.DB.Close()
			reopened, err := db.NewManager(h.tmpDir, logs.NewNodeLogger("queue-reopen", 0))
			if err != nil {
				t.Fatal(err)
			}
			h.DB = reopened
			var durableKeys []string
			for key := range durable {
				durableKeys = append(durableKeys, key)
			}
			actual, err := h.DB.GetKVs(durableKeys)
			if err != nil {
				t.Fatal(err)
			}
			for key, expected := range durable {
				if !bytes.Equal(actual[key], expected) {
					t.Fatalf("reopened queue result changed %s", key)
				}
			}
			t.Logf("background=%d requests=1500 BTC=%v EVM=%v jobs=12 request=%s requestDiffKeys=%d flow=%s advanceHeight=%s runtimeIO=%+v minerFees=%d", background, batches["btc"], batches["eth"], f.requestTime, f.requestDiffKeys, time.Since(startedAt), advanceTime, stats, minerFees)
		})
	}
}
