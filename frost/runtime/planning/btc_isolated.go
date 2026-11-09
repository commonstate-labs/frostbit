package planning

import (
	"bytes"
	"container/heap"
	"fmt"
	"sort"
	"strconv"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/utils"
	"google.golang.org/protobuf/proto"
)

const btcIsolationPage = 64
const btcIsolationParentCache = 64
const btcIsolationPlanByteBudget = 32 << 20

type btcIsolatedParentPlan struct {
	parent  *pb.FrostWithdrawState
	feeRate uint64
	jobs    []*Job
	bytes   int
}

type btcIsolatedInput struct {
	utxo   *pb.FrostUtxo
	vault  *pb.FrostVaultState
	amount uint64
}

type btcIsolatedTick struct {
	planner  *JobWindowPlanner
	asset    string
	cfg      *pb.FrostVaultConfig
	vaults   []*pb.FrostVaultState
	used     map[string]bool
	inputs   []btcIsolatedInput
	cursor   map[uint32]string
	done     map[uint32]bool
	heap     btcIsolatedInputHeap
	pending  map[uint32]int
	loaded   map[string]bool
	known    map[string]btcIsolatedInput
	minSpend uint64
}

type btcIsolatedInputHeap []btcIsolatedInput

func (h btcIsolatedInputHeap) Len() int { return len(h) }
func (h btcIsolatedInputHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.amount != b.amount {
		return a.amount < b.amount
	}
	return btcIsolatedOutpoint(a.vault.VaultId, a.utxo.Txid, a.utxo.Vout) < btcIsolatedOutpoint(b.vault.VaultId, b.utxo.Txid, b.utxo.Vout)
}
func (h btcIsolatedInputHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *btcIsolatedInputHeap) Push(x any)   { *h = append(*h, x.(btcIsolatedInput)) }
func (h *btcIsolatedInputHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	old[n-1] = btcIsolatedInput{}
	*h = old[:n-1]
	return x
}

func (p *JobWindowPlanner) planBTCIsolatedWindow(asset string) ([]*Job, error) {
	if p.btcPlans == nil {
		p.btcPlans = make(map[string]*btcIsolatedParentPlan)
		p.btcReadyCursor = make(map[string]string)
		p.btcActiveCursor = make(map[string]string)
		p.btcReadyTurn = make(map[string]bool)
		p.btcReserved = make(map[string]bool)
	}
	windowLimit := p.maxInFlight
	if windowLimit > btcIsolationPage {
		windowLimit = btcIsolationPage
	}
	previousActiveCursor, previousReadyCursor := p.btcActiveCursor[asset], p.btcReadyCursor[asset]
	active, nextActive, err := p.btcIndexJobs(keys.KeyFrostBtcWithdrawActiveChildPrefix(asset), p.btcActiveCursor[asset], windowLimit)
	if err != nil {
		return nil, err
	}
	p.btcActiveCursor[asset] = nextActive
	readyPrefix := keys.KeyFrostBtcWithdrawReadyPrefix(asset)
	readyIDs, nextReady, err := p.btcIndexIDs(readyPrefix, p.btcReadyCursor[asset], windowLimit)
	if err != nil {
		return nil, err
	}
	p.btcReadyCursor[asset] = nextReady
	parentKeys := make([]string, len(readyIDs))
	for i, id := range readyIDs {
		parentKeys[i] = keys.KeyFrostWithdraw(id)
	}
	parents, err := getMany(p.stateReader, parentKeys)
	if err != nil {
		return nil, err
	}
	if len(readyIDs) == 0 {
		return active, nil
	}
	var build pb.FrostBtcIsolationIndexState
	indexRaw, found, indexErr := p.stateReader.Get(keys.KeyFrostBtcUtxoAmountBuildState())
	if indexErr != nil {
		return nil, indexErr
	}
	if !found || proto.Unmarshal(indexRaw, &build) != nil || !build.Done {
		for _, id := range readyIDs {
			p.reportTransientLog(id, "BTCIsolation", "BUILDING", "BTC amount index construction is still in progress")
		}
		return active, nil
	}
	tick, err := p.newBTCIsolatedTick(asset)
	if err != nil {
		return nil, err
	}
	ready := make([]*Job, 0, len(readyIDs))
	for i, id := range readyIDs {
		var parent pb.FrostWithdrawState
		if len(parents[parentKeys[i]]) == 0 || proto.Unmarshal(parents[parentKeys[i]], &parent) != nil || parent.WithdrawId != id || parent.Chain != chain.ChainBTC || parent.Asset != asset {
			return nil, fmt.Errorf("invalid BTC ready parent %s", id)
		}
		if parent.BtcProtocolVersion != 3 {
			p.reportTransientLog(id, "BTCIsolation", "WAITING", "Legacy queued request requires owner cancellation and a new request with explicit fee and part budgets")
			continue
		}
		u, err := parseNonNegativeAmount(parent.BtcUnallocatedAmount)
		if err != nil {
			return nil, err
		}
		if u.Sign() == 0 {
			p.dropBTCPlan(id)
			continue
		}
		job, err := tick.parentJob(&parent)
		if err != nil {
			p.reportTransientLog(id, "BTCIsolation", "WAITING", err.Error())
			continue
		}
		if job != nil {
			ready = append(ready, job)
		}
	}
	                                                                         
	                                                                          
	jobs := make([]*Job, 0, windowLimit)
	turn := p.btcReadyTurn[asset]
	consumedActiveCursor, consumedReadyCursor := previousActiveCursor, previousReadyCursor
	for len(jobs) < windowLimit && (len(active) > 0 || len(ready) > 0) {
		if (turn && len(ready) > 0) || len(active) == 0 {
			jobs = append(jobs, ready[0])
			consumedReadyCursor = keys.KeyFrostBtcWithdrawReady(asset, ready[0].FirstSeq, ready[0].WithdrawIDs[0])
			ready = ready[1:]
		} else {
			jobs = append(jobs, active[0])
			consumedActiveCursor = keys.KeyFrostBtcWithdrawActiveChild(asset, active[0].JobID)
			active = active[1:]
		}
		turn = !turn
	}
	                                                                          
	                                                                        
	                  
	if len(active) > 0 {
		p.btcActiveCursor[asset] = consumedActiveCursor
	}
	if len(ready) > 0 {
		p.btcReadyCursor[asset] = consumedReadyCursor
	}
	p.btcReadyTurn[asset] = !p.btcReadyTurn[asset]
	return jobs, nil
}

                                                                              
                                                                  
func (p *JobWindowPlanner) btcIndexIDs(prefix, cursor string, limit int) ([]string, string, error) {
	ordered, ok := p.stateReader.(orderedStateScanner)
	if !ok {
		return nil, "", fmt.Errorf("BTC isolation requires ordered state pagination")
	}
	ids := make([]string, 0, limit)
	last := ""
	err := ordered.ScanOrderedAfter(prefix, cursor, limit, false, func(k string, v []byte) bool { last = k; ids = append(ids, string(v)); return true })
	if err != nil {
		return nil, "", err
	}
	if len(ids) == 0 && cursor != "" {
		return p.btcIndexIDs(prefix, "", limit)
	}
	if len(ids) < limit {
		last = ""
	}
	return ids, last, nil
}

func (p *JobWindowPlanner) btcIndexJobs(prefix, cursor string, limit int) ([]*Job, string, error) {
	ids, next, err := p.btcIndexIDs(prefix, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	ks := make([]string, len(ids))
	for i, id := range ids {
		ks[i] = keys.KeyFrostWithdrawJob(id)
	}
	values, err := getMany(p.stateReader, ks)
	if err != nil {
		return nil, "", err
	}
	jobs := make([]*Job, 0, len(ids))
	for i, id := range ids {
		var state pb.FrostWithdrawJobState
		if len(values[ks[i]]) == 0 || proto.Unmarshal(values[ks[i]], &state) != nil || state.JobId != id || state.Status != "CLAIMED" {
			return nil, "", fmt.Errorf("invalid BTC active child %s", id)
		}
		if state.BtcProtocolVersion != 3 {
			job, _, err := p.recoverClaimedJob(state.Chain, state.Asset, state.FirstSeq, id)
			if err != nil {
				return nil, "", err
			}
			jobs = append(jobs, job)
			continue
		}
		if len(state.WithdrawIds) != 1 || len(state.TemplateHash) != 32 || len(state.BtcOriginId) != 32 {
			return nil, "", fmt.Errorf("incomplete BTC active child %s", id)
		}
		jobs = append(jobs, btcJobFromState(&state))
	}
	return jobs, next, nil
}

func btcJobFromState(s *pb.FrostWithdrawJobState) *Job {
	return &Job{JobID: s.JobId, Chain: s.Chain, Asset: s.Asset, VaultID: s.VaultId, KeyEpoch: s.KeyEpoch, WithdrawIDs: append([]string(nil), s.WithdrawIds...), FirstSeq: s.FirstSeq, TemplateHash: append([]byte(nil), s.TemplateHash...), TemplateData: append([]byte(nil), s.TemplateData...), BTCProtocolVersion: s.BtcProtocolVersion, BTCPartSeq: s.BtcPartSeq, BTCGrossAmount: s.BtcGrossAmount, BTCFeeAmount: s.BtcFeeAmount, BTCOriginID: append([]byte(nil), s.BtcOriginId...)}
}

func (p *JobWindowPlanner) newBTCIsolatedTick(asset string) (*btcIsolatedTick, error) {
	if err := p.pruneBTCParentPlans(); err != nil {
		return nil, err
	}
	raw, ok, err := p.stateReader.Get(keys.KeyFrostVaultConfig(chain.ChainBTC, 0))
	if err != nil {
		return nil, err
	}
	var cfg pb.FrostVaultConfig
	if !ok || proto.Unmarshal(raw, &cfg) != nil || cfg.VaultCount == 0 || cfg.BtcFeeSatsPerVbyte == 0 {
		return nil, fmt.Errorf("BTC vault configuration unavailable")
	}
	if cfg.DepositDexChainId == 0 {
		return nil, fmt.Errorf("BTC immutable DEX network identity is missing")
	}
	ks := make([]string, cfg.VaultCount)
	for id := uint32(0); id < cfg.VaultCount; id++ {
		ks[id] = keys.KeyFrostVaultState(chain.ChainBTC, id)
	}
	values, err := getMany(p.stateReader, ks)
	if err != nil {
		return nil, err
	}
	t := &btcIsolatedTick{planner: p, asset: asset, cfg: &cfg, used: p.btcReserved, cursor: make(map[uint32]string), done: make(map[uint32]bool), pending: make(map[uint32]int), loaded: make(map[string]bool), known: make(map[string]btcIsolatedInput)}
	transitionKeys := []string{}
	for id := uint32(0); id < cfg.VaultCount; id++ {
		var v pb.FrostVaultState
		if len(values[ks[id]]) > 0 && proto.Unmarshal(values[ks[id]], &v) == nil && v.Status == "ACTIVE" && v.KeyEpoch > 0 && v.KeyEpoch < ^uint64(0) {
			t.vaults = append(t.vaults, &v)
			transitionKeys = append(transitionKeys, keys.KeyFrostVaultTransition(chain.ChainBTC, id, v.KeyEpoch+1))
		}
	}
	transitions, err := getMany(p.stateReader, transitionKeys)
	if err != nil {
		return nil, err
	}
	usable := t.vaults[:0]
	for _, v := range t.vaults {
		if raw := transitions[keys.KeyFrostVaultTransition(chain.ChainBTC, v.VaultId, v.KeyEpoch+1)]; len(raw) > 0 {
			var tr pb.VaultTransitionState
			if proto.Unmarshal(raw, &tr) != nil || tr.Lifecycle == "DRAINING" {
				continue
			}
		}
		usable = append(usable, v)
	}
	t.vaults = usable
	minimumFee, err := btc.EstimateWithdrawFee(1, [][]byte{make([]byte, 22)}, cfg.BtcFeeSatsPerVbyte)
	if err != nil || minimumFee > ^uint64(0)-btc.DustLimit {
		return nil, fmt.Errorf("invalid BTC minimum spend fee")
	}
	t.minSpend = minimumFee + btc.DustLimit
	for _, v := range t.vaults {
		t.cursor[v.VaultId] = fmt.Sprintf("%s%020d_", keys.KeyFrostBtcUtxoAmountPrefix(v.VaultId), minimumFee+btc.DustLimit)
	}
	return t, nil
}

                                                                             
                                                                                
func (p *JobWindowPlanner) pruneBTCParentPlans() error {
	if len(p.btcPlans) == 0 {
		return nil
	}
	ids := make([]string, 0, len(p.btcPlans))
	for id := range p.btcPlans {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	ks := make([]string, len(ids))
	for i, id := range ids {
		ks[i] = keys.KeyFrostWithdraw(id)
	}
	values, err := getMany(p.stateReader, ks)
	if err != nil {
		return err
	}
	for i, id := range ids {
		var parent pb.FrostWithdrawState
		if len(values[ks[i]]) == 0 || proto.Unmarshal(values[ks[i]], &parent) != nil || parent.BtcUnallocatedAmount == "0" || parent.BtcProtocolVersion != 3 {
			p.dropBTCPlan(id)
		}
	}
	return nil
}

func btcPlanJobBytes(job *Job) int {
	return len(job.TemplateData) + len(job.TemplateHash) + len(job.BTCOriginID) + 512
}
func btcPlanJobOutpoint(job *Job) string {
	if job.BTCReservationKey != "" {
		return job.BTCReservationKey
	}
	template, e := btc.FromJSON(job.TemplateData)
	if e != nil || len(template.Inputs) != 1 {
		return ""
	}
	return btcIsolatedOutpoint(job.VaultID, template.Inputs[0].TxID, template.Inputs[0].Vout)
}
func (p *JobWindowPlanner) dropBTCPlan(id string) {
	if plan := p.btcPlans[id]; plan != nil {
		for _, job := range plan.jobs {
			delete(p.btcReserved, btcPlanJobOutpoint(job))
		}
		p.btcPlanBytes -= plan.bytes
		delete(p.btcPlans, id)
	}
}

func btcIsolatedOutpoint(v uint32, txid string, n uint32) string {
	return fmt.Sprintf("%d/%s/%d", v, txid, n)
}

func (t *btcIsolatedTick) parentJob(parent *pb.FrostWithdrawState) (*Job, error) {
	p := t.planner
	cached := p.btcPlans[parent.WithdrawId]
	if cached != nil && cached.feeRate == t.cfg.BtcFeeSatsPerVbyte {
		for len(cached.jobs) > 0 && cached.jobs[0].BTCPartSeq < parent.BtcNextPartSeq {
			j := cached.jobs[0]
			u, _ := parseBTCAmount(cached.parent.BtcUnallocatedAmount)
			g, _ := parseBTCAmount(j.BTCGrossAmount)
			fee, _ := strconv.ParseUint(cached.parent.BtcCommittedFee, 10, 64)
			f, _ := strconv.ParseUint(j.BTCFeeAmount, 10, 64)
			if g > u {
				break
			}
			cached.parent.BtcUnallocatedAmount = strconv.FormatUint(u-g, 10)
			cached.parent.BtcCommittedFee = strconv.FormatUint(fee+f, 10)
			cached.parent.BtcNextPartSeq++
			delete(p.btcReserved, btcPlanJobOutpoint(j))
			size := btcPlanJobBytes(j)
			cached.bytes -= size
			p.btcPlanBytes -= size
			cached.jobs[0] = nil
			cached.jobs = cached.jobs[1:]
		}
		if cached.parent.BtcNextPartSeq == parent.BtcNextPartSeq && cached.parent.BtcUnallocatedAmount == parent.BtcUnallocatedAmount && cached.parent.BtcCommittedFee == parent.BtcCommittedFee && cached.parent.BtcMaxTotalFee == parent.BtcMaxTotalFee && cached.parent.BtcMaxParts == parent.BtcMaxParts && cached.parent.To == parent.To && len(cached.jobs) > 0 {
			job := cached.jobs[0]
			if err := t.validateCachedInput(job); err == nil {
				return job, nil
			}
		}
	}
	if cached != nil {
		p.dropBTCPlan(parent.WithdrawId)
	}
	jobs, err := t.buildParentPlan(parent)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, fmt.Errorf("no complete BTC allocation fits the remaining fee and part budgets")
	}
	size := 0
	for _, job := range jobs {
		size += btcPlanJobBytes(job)
	}
	if size > btcIsolationPlanByteBudget {
		return nil, fmt.Errorf("BTC candidate plan exceeds local planning byte budget")
	}
	for len(p.btcPlans) >= btcIsolationParentCache || p.btcPlanBytes+size > btcIsolationPlanByteBudget {
		ids := make([]string, 0, len(p.btcPlans))
		for id := range p.btcPlans {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		p.dropBTCPlan(ids[0])
	}
	p.btcPlans[parent.WithdrawId] = &btcIsolatedParentPlan{parent: proto.Clone(parent).(*pb.FrostWithdrawState), feeRate: t.cfg.BtcFeeSatsPerVbyte, jobs: jobs, bytes: size}
	p.btcPlanBytes += size
	for _, job := range jobs {
		template, _ := btc.FromJSON(job.TemplateData)
		t.used[btcIsolatedOutpoint(job.VaultID, template.Inputs[0].TxID, template.Inputs[0].Vout)] = true
	}
	return jobs[0], nil
}

func (t *btcIsolatedTick) validateCachedInput(job *Job) error {
	template, err := btc.FromJSON(job.TemplateData)
	if err != nil || len(template.Inputs) != 1 {
		return fmt.Errorf("invalid cached BTC child")
	}
	in := template.Inputs[0]
	ks := []string{keys.KeyFrostBtcUtxo(job.VaultID, in.TxID, in.Vout), keys.KeyFrostBtcLockedUtxo(job.VaultID, in.TxID, in.Vout)}
	values, err := getMany(t.planner.stateReader, ks)
	if err != nil {
		return err
	}
	var u pb.FrostUtxo
	if proto.Unmarshal(values[ks[0]], &u) != nil || len(values[ks[0]]) == 0 || u.Amount != strconv.FormatUint(in.Amount, 10) || !bytes.Equal(u.OriginId, job.BTCOriginID) || len(values[ks[1]]) != 0 {
		return fmt.Errorf("cached BTC input changed")
	}
	for _, v := range t.vaults {
		if v.VaultId == job.VaultID && v.KeyEpoch == job.KeyEpoch {
			return nil
		}
	}
	return fmt.Errorf("cached BTC vault changed")
}

                                                                          
                                                                       
func (t *btcIsolatedTick) loadInputPage(vault *pb.FrostVaultState) error {
	ordered, ok := t.planner.stateReader.(orderedStateScanner)
	if !ok {
		return fmt.Errorf("BTC amount index requires ordered pagination")
	}
	if t.done[vault.VaultId] {
		return nil
	}
	type ref struct {
		txid   string
		vout   uint32
		amount uint64
	}
	refs := []ref{}
	last := ""
	var parseErr error
	err := ordered.ScanOrderedAfter(keys.KeyFrostBtcUtxoAmountPrefix(vault.VaultId), t.cursor[vault.VaultId], btcIsolationPage, false, func(k string, _ []byte) bool {
		last = k
		id, amount, txid, vout, e := keys.ParseFrostBtcUtxoAmountKey(k)
		if e != nil || id != vault.VaultId {
			parseErr = fmt.Errorf("corrupt BTC amount index")
			return false
		}
		refs = append(refs, ref{txid, vout, amount})
		return true
	})
	if err != nil {
		return err
	}
	if parseErr != nil {
		return parseErr
	}
	if len(refs) < btcIsolationPage {
		t.done[vault.VaultId] = true
	}
	t.cursor[vault.VaultId] = last
	ks := make([]string, 0, len(refs)*2)
	for _, r := range refs {
		if _, cached := t.known[btcIsolatedOutpoint(vault.VaultId, r.txid, r.vout)]; cached {
			continue
		}
		ks = append(ks, keys.KeyFrostBtcUtxo(vault.VaultId, r.txid, r.vout), keys.KeyFrostBtcLockedUtxo(vault.VaultId, r.txid, r.vout))
	}
	values, err := getMany(t.planner.stateReader, ks)
	if err != nil {
		return err
	}
	for _, r := range refs {
		key := keys.KeyFrostBtcUtxo(vault.VaultId, r.txid, r.vout)
		u := &pb.FrostUtxo{}
		known, cached := t.known[btcIsolatedOutpoint(vault.VaultId, r.txid, r.vout)]
		if len(values[key]) == 0 && !cached {
			continue
		}
		if cached {
			u = proto.Clone(known.utxo).(*pb.FrostUtxo)
		} else if proto.Unmarshal(values[key], u) != nil || u.Txid != r.txid || u.Vout != r.vout || u.VaultId != vault.VaultId || u.Amount != strconv.FormatUint(r.amount, 10) || len(u.OriginId) != 32 {
			return fmt.Errorf("BTC amount index differs from primary state")
		}
		if len(values[keys.KeyFrostBtcLockedUtxo(vault.VaultId, r.txid, r.vout)]) > 0 {
			continue
		}
		id := btcIsolatedOutpoint(vault.VaultId, r.txid, r.vout)
		if !t.loaded[id] {
			t.loaded[id] = true
			input := btcIsolatedInput{utxo: u, vault: vault, amount: r.amount}
			t.known[id] = input
			heap.Push(&t.heap, input)
			t.pending[vault.VaultId]++
		}
	}
	return nil
}

func (t *btcIsolatedTick) nextSmallInput() (btcIsolatedInput, bool, error) {
	                                                                            
	for _, v := range t.vaults {
		for t.pending[v.VaultId] == 0 && !t.done[v.VaultId] {
			if err := t.loadInputPage(v); err != nil {
				return btcIsolatedInput{}, false, err
			}
		}
	}
	for len(t.heap) > 0 {
		in := heap.Pop(&t.heap).(btcIsolatedInput)
		t.pending[in.vault.VaultId]--
		if t.used[btcIsolatedOutpoint(in.vault.VaultId, in.utxo.Txid, in.utxo.Vout)] {
			for t.pending[in.vault.VaultId] == 0 && !t.done[in.vault.VaultId] {
				if err := t.loadInputPage(in.vault); err != nil {
					return btcIsolatedInput{}, false, err
				}
			}
			continue
		}
		return in, true, nil
	}
	return btcIsolatedInput{}, false, nil
}

                                                                             
                                                                              
func (t *btcIsolatedTick) seekTail(parent *pb.FrostWithdrawState, seq, gross, budget uint64) (*Job, error) {
	ordered := t.planner.stateReader.(orderedStateScanner)
	var best *Job
	var bestAmount uint64
	for _, vault := range t.vaults {
		cursor := fmt.Sprintf("%s%020d_", keys.KeyFrostBtcUtxoAmountPrefix(vault.VaultId), gross)
		for {
			keysFound := []string{}
			last := ""
			var parseErr error
			err := ordered.ScanOrderedAfter(keys.KeyFrostBtcUtxoAmountPrefix(vault.VaultId), cursor, btcIsolationPage, false, func(k string, _ []byte) bool { last = k; keysFound = append(keysFound, k); return true })
			if err != nil {
				return nil, err
			}
			if len(keysFound) == 0 {
				break
			}
			ks := make([]string, 0, len(keysFound)*2)
			for _, k := range keysFound {
				_, amount, tx, n, e := keys.ParseFrostBtcUtxoAmountKey(k)
				if e != nil {
					parseErr = e
					break
				}
				if best != nil && amount > bestAmount {
					break
				}
				id := btcIsolatedOutpoint(vault.VaultId, tx, n)
				if t.used[id] {
					continue
				}
				if _, cached := t.known[id]; cached {
					continue
				}
				ks = append(ks, keys.KeyFrostBtcUtxo(vault.VaultId, tx, n), keys.KeyFrostBtcLockedUtxo(vault.VaultId, tx, n))
			}
			if parseErr != nil {
				return nil, parseErr
			}
			values, e := getMany(t.planner.stateReader, ks)
			if e != nil {
				return nil, e
			}
			stop := false
			for _, k := range keysFound {
				_, amount, tx, n, e := keys.ParseFrostBtcUtxoAmountKey(k)
				if e != nil {
					return nil, e
				}
				if best != nil && amount > bestAmount {
					stop = true
					break
				}
				if t.used[btcIsolatedOutpoint(vault.VaultId, tx, n)] {
					continue
				}
				if amount < gross {
					continue
				}
				if amount > gross && amount-gross < btc.DustLimit {
					continue
				}
				u := &pb.FrostUtxo{}
				raw := values[keys.KeyFrostBtcUtxo(vault.VaultId, tx, n)]
				known, cached := t.known[btcIsolatedOutpoint(vault.VaultId, tx, n)]
				if len(raw) == 0 && !cached {
					continue
				}
				if cached {
					u = proto.Clone(known.utxo).(*pb.FrostUtxo)
				} else if proto.Unmarshal(raw, u) != nil || u.Txid != tx || u.Vout != n || u.VaultId != vault.VaultId || u.Amount != strconv.FormatUint(amount, 10) || len(u.OriginId) != 32 {
					return nil, fmt.Errorf("BTC tail index differs from primary")
				}
				if len(values[keys.KeyFrostBtcLockedUtxo(vault.VaultId, tx, n)]) != 0 {
					continue
				}
				t.known[btcIsolatedOutpoint(vault.VaultId, tx, n)] = btcIsolatedInput{utxo: u, vault: vault, amount: amount}
				job, e := t.buildChild(parent, seq, gross, btcIsolatedInput{utxo: u, vault: vault, amount: amount})
				if e != nil {
					continue
				}
				fee, _ := strconv.ParseUint(job.BTCFeeAmount, 10, 64)
				if fee > budget {
					stop = true
					break
				}
				if best == nil || amount < bestAmount || (amount == bestAmount && btcPlanJobOutpoint(job) < btcPlanJobOutpoint(best)) {
					best, bestAmount = job, amount
				}
				stop = true
				break
			}
			if stop || len(keysFound) < btcIsolationPage || last == cursor {
				break
			}
			cursor = last
		}
	}
	return best, nil
}

func (t *btcIsolatedTick) buildParentPlan(parent *pb.FrostWithdrawState) ([]*Job, error) {
	gross, err := parseBTCAmount(parent.BtcUnallocatedAmount)
	if err != nil {
		return nil, err
	}
	recipientScript, err := btc.AddressScriptForNetwork(parent.To, t.cfg.BtcNetwork)
	if err != nil {
		return nil, err
	}
	minimumFee, err := btc.EstimateWithdrawFee(1, [][]byte{recipientScript}, t.cfg.BtcFeeSatsPerVbyte)
	if err != nil || minimumFee > ^uint64(0)-btc.DustLimit || gross < minimumFee+btc.DustLimit {
		return nil, fmt.Errorf("remaining BTC allocation cannot independently cover fee and recipient dust")
	}
	maxFee, err := strconv.ParseUint(parent.BtcMaxTotalFee, 10, 64)
	if err != nil {
		return nil, err
	}
	usedFee, err := strconv.ParseUint(parent.BtcCommittedFee, 10, 64)
	if err != nil || usedFee > maxFee {
		return nil, fmt.Errorf("invalid BTC remaining fee budget")
	}
	if parent.BtcNextPartSeq == 0 || parent.BtcNextPartSeq > uint64(parent.BtcMaxParts) || parent.BtcMaxParts > protocol.BTCIsolationMaxParts {
		return nil, fmt.Errorf("BTC part budget exhausted")
	}
	parts := uint64(parent.BtcMaxParts) - parent.BtcNextPartSeq + 1
	remaining, budget := gross, maxFee-usedFee
	jobs := []*Job{}
	taken := []btcIsolatedInput{}
	selected := []string{}
	                                                                           
	                                                                               
	defer func() {
		for _, id := range selected {
			delete(t.used, id)
		}
		for _, in := range taken {
			heap.Push(&t.heap, in)
			t.pending[in.vault.VaultId]++
		}
	}()
	for remaining > 0 && uint64(len(jobs))+1 < parts {
		in, ok, e := t.nextSmallInput()
		if e != nil {
			return nil, e
		}
		if !ok {
			break
		}
		taken = append(taken, in)
		if in.amount >= remaining {
			break
		}
		job, e := t.buildChild(parent, parent.BtcNextPartSeq+uint64(len(jobs)), in.amount, in)
		if e != nil {
			continue
		}
		fee, _ := strconv.ParseUint(job.BTCFeeAmount, 10, 64)
		if fee > budget {
			break
		}
		jobs = append(jobs, job)
		id := btcIsolatedOutpoint(in.vault.VaultId, in.utxo.Txid, in.utxo.Vout)
		t.used[id] = true
		selected = append(selected, id)
		remaining -= in.amount
		budget -= fee
	}
	if remaining > 0 {
		tail, e := t.seekTail(parent, parent.BtcNextPartSeq+uint64(len(jobs)), remaining, budget)
		if e != nil {
			return nil, e
		}
		if tail != nil {
			return append(jobs, tail), nil
		}
	}
	                                                                           
	                                                                       
	one, e := t.seekTail(parent, parent.BtcNextPartSeq, gross, maxFee-usedFee)
	if e != nil {
		return nil, e
	}
	if one != nil {
		return []*Job{one}, nil
	}
	for _, id := range selected {
		delete(t.used, id)
	}
	selected = nil
	if parts > 1 {
		return t.buildBudgetAwarePlan(parent, gross, maxFee-usedFee, parts)
	}
	return nil, fmt.Errorf("insufficient BTC inputs within authorized fee and part budgets")
}

                                                                            
                                                                               
                                                                              
func (t *btcIsolatedTick) buildBudgetAwarePlan(parent *pb.FrostWithdrawState, gross, budget, parts uint64) ([]*Job, error) {
	pass := *t
	pass.cursor = make(map[uint32]string)
	pass.done = make(map[uint32]bool)
	pass.pending = make(map[uint32]int)
	pass.loaded = make(map[string]bool)
	pass.heap = nil
	minimum := gross / parts
	if gross%parts != 0 {
		minimum++
	}
	if minimum < pass.minSpend {
		minimum = pass.minSpend
	}
	for _, v := range pass.vaults {
		pass.cursor[v.VaultId] = fmt.Sprintf("%s%020d_", keys.KeyFrostBtcUtxoAmountPrefix(v.VaultId), minimum)
	}
	remaining := gross
	jobs := []*Job{}
	selected := []string{}
	defer func() {
		for _, id := range selected {
			delete(t.used, id)
		}
	}()
	for remaining > 0 && uint64(len(jobs))+1 < parts {
		in, ok, e := pass.nextSmallInput()
		if e != nil {
			return nil, e
		}
		if !ok {
			break
		}
		if in.amount >= remaining {
			break
		}
		job, e := pass.buildChild(parent, parent.BtcNextPartSeq+uint64(len(jobs)), in.amount, in)
		if e != nil {
			continue
		}
		fee, _ := strconv.ParseUint(job.BTCFeeAmount, 10, 64)
		if fee > budget {
			break
		}
		jobs = append(jobs, job)
		remaining -= in.amount
		budget -= fee
		id := btcIsolatedOutpoint(in.vault.VaultId, in.utxo.Txid, in.utxo.Vout)
		t.used[id] = true
		selected = append(selected, id)
	}
	tail, e := pass.seekTail(parent, parent.BtcNextPartSeq+uint64(len(jobs)), remaining, budget)
	if e != nil {
		return nil, e
	}
	if tail != nil {
		return append(jobs, tail), nil
	}
	return nil, fmt.Errorf("no complete BTC plan found by bounded deterministic fee/part strategies")
}

func (t *btcIsolatedTick) buildChild(parent *pb.FrostWithdrawState, seq, gross uint64, input btcIsolatedInput) (*Job, error) {
	u, v := input.utxo, input.vault
	script, err := btc.AddressScriptForNetwork(parent.To, t.cfg.BtcNetwork)
	if err != nil {
		return nil, err
	}
	net, fee, change, err := btc.ExpectedUserPaidWithdrawAmounts(input.amount, []uint64{gross}, 1, [][]byte{script}, u.ScriptPubkey, t.cfg.BtcFeeSatsPerVbyte)
	if err != nil {
		return nil, err
	}
	operator, err := migrationInputOperator(v.GroupPubkey, u.Tweak)
	if err != nil {
		return nil, err
	}
	changeAddress := ""
	if change > 0 {
		changeAddress = t.planner.btcAddressFromScriptPubKey(u.ScriptPubkey, chain.ChainBTC)
		if changeAddress == "" {
			return nil, fmt.Errorf("cannot derive BTC change address")
		}
	}
	a, err := t.planner.adapterFactory.Adapter(chain.ChainBTC)
	if err != nil {
		return nil, err
	}
	canonical, err := a.BuildWithdrawTemplate(chain.WithdrawTemplateParams{Chain: chain.ChainBTC, Asset: parent.Asset, VaultID: v.VaultId, KeyEpoch: v.KeyEpoch, WithdrawIDs: []string{parent.WithdrawId}, Outputs: []chain.WithdrawOutput{{WithdrawID: parent.WithdrawId, To: parent.To, Amount: strconv.FormatUint(net[0], 10)}}, Inputs: []chain.UTXO{{TxID: u.Txid, Vout: u.Vout, Amount: input.amount, ScriptPubKey: u.ScriptPubkey, ConfirmHeight: u.FinalizeHeight}}, GuardianPubkey: t.cfg.GuardianPubkey, BTCInputPubkeys: [][]byte{operator}, Fee: fee, ChangeAmount: change, ChangeAddress: changeAddress})
	if err != nil {
		return nil, err
	}
	id := chain.BTCWithdrawPartJobID(chain.BTCWithdrawPartIntent{Network: t.cfg.DepositDexChainId, BTCNetwork: t.cfg.BtcNetwork, ParentID: parent.WithdrawId, PartSeq: seq, VaultID: v.VaultId, KeyEpoch: v.KeyEpoch, Gross: gross, Fee: fee, OriginID: u.OriginId, TemplateHash: canonical.TemplateHash})
	return &Job{JobID: id, Chain: chain.ChainBTC, Asset: parent.Asset, VaultID: v.VaultId, KeyEpoch: v.KeyEpoch, WithdrawIDs: []string{parent.WithdrawId}, FirstSeq: parent.Seq, TemplateHash: canonical.TemplateHash, TemplateData: canonical.TemplateData, BTCProtocolVersion: 3, BTCPartSeq: seq, BTCGrossAmount: strconv.FormatUint(gross, 10), BTCFeeAmount: strconv.FormatUint(fee, 10), BTCOriginID: append([]byte(nil), u.OriginId...), BTCReservationKey: btcIsolatedOutpoint(v.VaultId, u.Txid, u.Vout)}, nil
}

func migrationInputOperator(group, tweak []byte) ([]byte, error) {
	if len(tweak) == 0 {
		return utils.NormalizeSecp256k1XOnlyPubKey(group)
	}
	if len(tweak) != 32 {
		return nil, fmt.Errorf("invalid BTC input tweak")
	}
	return utils.ComputeTweakedXOnlyPubkey(group, tweak)
}
