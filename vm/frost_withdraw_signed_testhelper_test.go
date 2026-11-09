package vm

import (
	"math/big"
	"sort"
	"testing"

	"dex/frost/chain"
	"dex/frost/chain/evm"
	"dex/frost/core/curve"
	"dex/keys"
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                                                                
                                                                            
                                                       
func normalizeMixedAccountWithdrawFixtures(t *testing.T, base map[string][]byte, chainName, asset string, vaultID uint32, withdrawIDs []string) {
	t.Helper()
	type queued struct {
		id      string
		liveSeq uint64
	}
	live := make([]queued, 0, len(withdrawIDs))
	total := new(big.Int)
	for i, id := range withdrawIDs {
		var state pb.FrostWithdrawState
		if err := unmarshalProtoCompat(base[keys.KeyFrostWithdraw(id)], &state); err != nil {
			t.Fatalf("decode account withdraw %s: %v", id, err)
		}
		state.VaultId = vaultID
		if state.LiveSeq == 0 {
			state.LiveSeq = state.Seq
			if state.LiveSeq == 0 {
				state.LiveSeq = uint64(i + 1)
			}
		}
		grossRaw := state.GrossAmount
		if grossRaw == "" {
			grossRaw = state.Amount
		}
		gross, err := chain.ParseWithdrawAmount(grossRaw)
		if err != nil {
			t.Fatalf("invalid account withdraw gross %s: %v", id, err)
		}
		fee := evm.CalculateBroadcastFee(gross)
		state.Amount = gross.String()
		state.GrossAmount = gross.String()
		state.FeeAmount = fee.String()
		state.NetAmount = new(big.Int).Sub(new(big.Int).Set(gross), fee).String()
		state.FeeVersion = 1
		state.BroadcastFee = ""
		if state.RequestHeight == 0 {
			state.RequestHeight = 1
		}
		base[keys.KeyFrostWithdraw(id)] = mustMarshalProto(t, &state)
		live = append(live, queued{id: id, liveSeq: state.LiveSeq})
		total.Add(total, gross)
	}
	if _, exists := base[keys.KeyToken(asset)]; !exists {
		base[keys.KeyToken(asset)] = mustMarshalProto(t, &pb.Token{
			Address: asset, Type: pb.TokenType_TOKEN_BRIDGED, NativeChain: chainName,
		})
	}
	sort.Slice(live, func(i, j int) bool { return live[i].liveSeq < live[j].liveSeq })
	if len(live) > 0 {
		base[keys.KeyFrostWithdrawLiveHead(chainName, vaultID)] = []byte(new(big.Int).SetUint64(live[0].liveSeq).String())
		base[keys.KeyFrostWithdrawLiveTail(chainName, vaultID)] = []byte(new(big.Int).SetUint64(live[len(live)-1].liveSeq).String())
		base[keys.KeyFrostWithdrawLivePending(chainName, vaultID)] = []byte(new(big.Int).SetUint64(uint64(len(live))).String())
		for _, entry := range live {
			base[keys.KeyFrostWithdrawLiveIndex(chainName, vaultID, entry.liveSeq)] = []byte(entry.id)
		}
	}
	reservedKey := keys.KeyFrostVaultReservedBalance(chainName, asset, vaultID)
	if _, exists := base[reservedKey]; !exists {
		base[reservedKey] = []byte(total.String())
		if availableRaw, ok := base[keys.KeyFrostVaultAvailableBalance(chainName, asset, vaultID)]; ok {
			available, err := ParseBalance(string(availableRaw))
			if err == nil && available.Cmp(total) >= 0 {
				base[keys.KeyFrostVaultAvailableBalance(chainName, asset, vaultID)] = []byte(new(big.Int).Sub(available, total).String())
			}
		}
	}
}

                                                                                                               
                                                      
func StubVerifySignatureForTest(t *testing.T) {
	stubVerifySignature(t)
}

                                                                                
func SeedAccountChainSignatureStubForTest(t *testing.T, base map[string][]byte, chainName, asset string, vaultID uint32, withdrawIDs []string) []byte {
	return seedAccountChainSignatureStub(t, base, chainName, asset, vaultID, withdrawIDs)
}

                                                                                   
func SeedAccountChainClaimStubForTest(t *testing.T, base map[string][]byte, chainName, asset string, vaultID uint32, withdrawIDs []string) ([]byte, string) {
	return seedAccountChainClaimStub(t, base, chainName, asset, vaultID, withdrawIDs)
}

                                                                          
                                                                          
func SignAccountClaimForTest(t *testing.T, base map[string][]byte, chainName, asset string, vaultID uint32, withdrawIDs []string) []byte {
	t.Helper()
	secret := accountTestSecret(true)
	var vault pb.FrostVaultState
	key := keys.KeyFrostVaultState(chainName, vaultID)
	if err := proto.Unmarshal(base[key], &vault); err != nil {
		t.Fatal(err)
	}
	vault.GroupPubkey = curve.NewBN256Group().SerializePoint(curve.NewBN256Group().ScalarBaseMult(secret))
	base[key] = mustMarshalProto(t, &vault)
	tpl, _, err := buildCanonicalAccountTemplate(newReadOnlyBaseStateView(base), chainName, asset, vaultID, vault.KeyEpoch, withdrawIDs)
	if err != nil {
		t.Fatal(err)
	}
	previous := verifySignatureFn
	verifySignatureFn = verifySignatureImpl
	t.Cleanup(func() { verifySignatureFn = previous })
	return signAccountThresholdTest(t, secret, tpl.SigHashes[0])
}

                                                                                                                        
func stubVerifySignature(t *testing.T) {
	t.Helper()
	prev := verifySignatureFn
	verifySignatureFn = func(signAlgo pb.SignAlgo, pubKey, msg, sig []byte) (bool, error) {
		return true, nil
	}
	t.Cleanup(func() { verifySignatureFn = prev })
}

                                                                                               
func seedAccountChainVaultState(t *testing.T, base map[string][]byte, chainName string, vaultIDs ...uint32) {
	t.Helper()
	for _, id := range vaultIDs {
		vs := &pb.FrostVaultState{
			VaultId:     id,
			Chain:       chainName,
			KeyEpoch:    1,
			GroupPubkey: make([]byte, 64),
			SignAlgo:    pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
			Status:      "ACTIVE",
		}
		data, err := proto.Marshal(vs)
		if err != nil {
			t.Fatalf("marshal vault state failed: %v", err)
		}
		base[keys.KeyFrostVaultState(chainName, id)] = data
	}
}

                                                                                           
                                                                                                                
                                                                                                      
                                             
func seedAccountChainSignatureStub(t *testing.T, base map[string][]byte, chainName, asset string, vaultID uint32, withdrawIDs []string) []byte {
	t.Helper()

	                                                          
	refs := make([]string, int(vaultID)+1)
	for i := range refs {
		refs[i] = "0x0000000000000000000000000000000000000000"
	}
	refs[vaultID] = "0x000000000000000000000000000000000000dead"
	cfg := &pb.FrostVaultConfig{AuthorityProtocol: 3, DepositDexChainId: 51025, GuardianPubkey: accountGuardianTestKey(t),
		Chain:          chainName,
		SignAlgo:       pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		VaultCount:     uint32(len(refs)),
		VaultRefs:      refs,
		ThresholdRatio: 0.67,
	}
	cfgData, err := proto.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal vault config failed: %v", err)
	}
	base[keys.KeyFrostVaultConfig(chainName, 0)] = cfgData

	             
	vs := &pb.FrostVaultState{
		VaultId:     vaultID,
		Chain:       chainName,
		KeyEpoch:    1,
		GroupPubkey: make([]byte, 64),
		SignAlgo:    pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128,
		Status:      "ACTIVE",
	}
	vsData, err := proto.Marshal(vs)
	if err != nil {
		t.Fatalf("marshal vault state failed: %v", err)
	}
	base[keys.KeyFrostVaultState(chainName, vaultID)] = vsData
	normalizeMixedAccountWithdrawFixtures(t, base, chainName, asset, vaultID, withdrawIDs)

	                                 
	sv := newReadOnlyBaseStateView(base)
	tpl, _, err := buildCanonicalAccountTemplate(sv, chainName, asset, vaultID, 1, withdrawIDs)
	if err != nil {
		t.Fatalf("build canonical template failed: %v", err)
	}

	stubVerifySignature(t)
	return tpl.TemplateHash
}

func seedAccountChainClaimStub(t *testing.T, base map[string][]byte, chainName, asset string, vaultID uint32, withdrawIDs []string) ([]byte, string) {
	t.Helper()
	seedAccountChainSignatureStub(t, base, chainName, asset, vaultID, withdrawIDs)
	sv := newReadOnlyBaseStateView(base)
	tpl, firstSeq, err := buildCanonicalAccountTemplate(sv, chainName, asset, vaultID, 1, withdrawIDs)
	if err != nil {
		t.Fatalf("build claimed account template failed: %v", err)
	}
	jobID := chain.ComputeWithdrawJobID(chain.NormalizeChain(chainName), "", vaultID, firstSeq, tpl.TemplateHash, 1)
	lastSeq := firstSeq
	totalAmount := big.NewInt(0)
	for _, withdrawID := range withdrawIDs {
		data := base[keys.KeyFrostWithdraw(withdrawID)]
		var withdraw pb.FrostWithdrawState
		if err := unmarshalProtoCompat(data, &withdraw); err != nil {
			t.Fatalf("decode claimed withdraw %s: %v", withdrawID, err)
		}
		withdraw.Status = string(WithdrawStatusSigning)
		withdraw.JobId = jobID
		if withdraw.LiveSeq > lastSeq {
			lastSeq = withdraw.LiveSeq
		}
		amount, err := ParseBalance(withdraw.Amount)
		if err != nil || amount.Sign() <= 0 {
			t.Fatalf("invalid claimed withdraw amount %s: %q", withdrawID, withdraw.Amount)
		}
		totalAmount, err = SafeAdd(totalAmount, amount)
		if err != nil {
			t.Fatalf("sum claimed withdraw amount: %v", err)
		}
		base[keys.KeyFrostWithdraw(withdrawID)] = mustMarshalProto(t, &withdraw)
	}
	reservedKey := keys.KeyFrostVaultReservedBalance(chainName, asset, vaultID)
	if reservedData, exists := base[reservedKey]; !exists || string(reservedData) != totalAmount.String() {
		t.Fatalf("mixed request reservation mismatch: got=%q want=%s", string(reservedData), totalAmount)
	}
	template, err := evm.FromJSON(tpl.TemplateData)
	if err != nil {
		t.Fatal(err)
	}
	base[keys.KeyFrostWithdrawJob(jobID)] = mustMarshalProto(t, &pb.FrostWithdrawJobState{
		JobId: jobID, Chain: chain.NormalizeChain(chainName), Asset: "", VaultId: vaultID, KeyEpoch: 1,
		WithdrawIds: append([]string(nil), withdrawIDs...), FirstSeq: firstSeq, LastSeq: lastSeq,
		TemplateHash: append([]byte(nil), tpl.TemplateHash...), TemplateData: append([]byte(nil), tpl.TemplateData...), Status: frostWithdrawJobStatusClaimed,
		ReservedAmount: totalAmount.String(), BatchSeq: template.BatchSeq, RewardTokens: append([]string(nil), template.RewardTokens...),
	})
	base[keys.KeyFrostWithdrawActiveVaultJob(chainName, vaultID)] = []byte(jobID)
	return tpl.TemplateHash, jobID
}

func populateMixedSignedTxFromClaim(t *testing.T, base map[string][]byte, signed *pb.FrostWithdrawSignedTx) {
	t.Helper()
	var job pb.FrostWithdrawJobState
	if err := unmarshalProtoCompat(base[keys.KeyFrostWithdrawJob(signed.JobId)], &job); err != nil {
		t.Fatalf("decode mixed claimed job %s: %v", signed.JobId, err)
	}
	signed.Asset = ""
	signed.TemplateHash = append([]byte(nil), job.TemplateHash...)
	signed.TemplateData = append([]byte(nil), job.TemplateData...)
	signed.BatchSeq = job.BatchSeq
	signed.IncludesHandover = job.IncludesHandover
}

func accountGuardianTestKey(t *testing.T) []byte {
	t.Helper()
	g := curve.NewBN256Group()
	key, err := evm.CanonicalBN128PublicKey(g.SerializePoint(g.ScalarBaseMult(big.NewInt(29))))
	if err != nil {
		t.Fatal(err)
	}
	return key
}
func accountGuardianTemplateSignature(t *testing.T, data []byte) [][]byte {
	t.Helper()
	tpl, err := evm.FromJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := tpl.SigningHash()
	if err != nil {
		t.Fatal(err)
	}
	return [][]byte{signAccountThresholdTest(t, big.NewInt(29), msg)}
}
func AccountGuardianClaimForTest(t *testing.T, base map[string][]byte, chainName, asset string, vaultID uint32, withdrawIDs []string) [][]byte {
	t.Helper()
	var vault pb.FrostVaultState
	if err := proto.Unmarshal(base[keys.KeyFrostVaultState(chainName, vaultID)], &vault); err != nil {
		t.Fatal(err)
	}
	tpl, _, err := buildCanonicalAccountTemplate(newReadOnlyBaseStateView(base), chainName, asset, vaultID, vault.KeyEpoch, withdrawIDs)
	if err != nil {
		t.Fatal(err)
	}
	return accountGuardianTemplateSignature(t, tpl.TemplateData)
}
