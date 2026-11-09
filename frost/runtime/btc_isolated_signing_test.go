package runtime

import (
	"strconv"
	"testing"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/guardian"
	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

func newBTCIsolatedSigningFixture(t *testing.T) *btcSigningValidatorFixture {
	t.Helper()
	f := newBTCSigningValidatorFixture(t)
	old := f.env.SessionID
	var job pb.FrostWithdrawJobState
	proto.Unmarshal(f.reader.data[keys.KeyFrostWithdrawJob(old)], &job)
	template, _ := btc.FromJSON(job.TemplateData)
	var cfg pb.FrostVaultConfig
	proto.Unmarshal(f.reader.data[keys.KeyFrostVaultConfig("btc", 0)], &cfg)
	cfg.BtcNetwork = "testnet3"
	cfg.BtcFeeSatsPerVbyte = 1
	cfg.DepositDexChainId = 51025
	put := func(k string, m proto.Message) {
		raw, e := proto.Marshal(m)
		if e != nil {
			t.Fatal(e)
		}
		f.reader.data[k] = raw
	}
	put(keys.KeyFrostVaultConfig("btc", 0), &cfg)
	var vault pb.FrostVaultState
	proto.Unmarshal(f.reader.data[keys.KeyFrostVaultState("btc", job.VaultId)], &vault)
	vault.SignAlgo = chain.SignAlgoBTC
	put(keys.KeyFrostVaultState("btc", job.VaultId), &vault)
	in := template.Inputs[0]
	var u pb.FrostUtxo
	proto.Unmarshal(f.reader.data[keys.KeyFrostBtcUtxo(job.VaultId, in.TxID, in.Vout)], &u)
	u.OriginId = chain.BTCOriginID(51025, "testnet3", in.TxID, in.Vout, false)
	put(keys.KeyFrostBtcUtxo(job.VaultId, in.TxID, in.Vout), &u)
	parent := &pb.FrostWithdrawState{WithdrawId: job.WithdrawIds[0], Seq: 1, Chain: "btc", Asset: "BTC", To: template.Outputs[0].Address, BtcProtocolVersion: 3, BtcNextPartSeq: 2, BtcMaxParts: 4, Status: "QUEUED"}
	script, e := btc.AddressScriptForNetwork(parent.To, cfg.BtcNetwork)
	if e != nil {
		t.Fatal(e)
	}
	net, fee, _, e := btc.ExpectedUserPaidWithdrawAmounts(in.Amount, []uint64{in.Amount}, 1, [][]byte{script}, u.ScriptPubkey, 1)
	if e != nil {
		t.Fatal(e)
	}
	template.Outputs[0].Amount = net[0]
	job.TemplateData, _ = template.ToJSON()
	job.TemplateHash = template.TemplateHash()
	job.FirstSeq = 1
	job.LastSeq = 1
	job.BtcProtocolVersion = 3
	job.BtcPartSeq = 1
	job.BtcGrossAmount = strconv.FormatUint(in.Amount, 10)
	job.BtcFeeAmount = strconv.FormatUint(fee, 10)
	job.BtcOriginId = u.OriginId
	job.JobId = chain.BTCWithdrawPartJobID(chain.BTCWithdrawPartIntent{Network: 51025, BTCNetwork: "testnet3", ParentID: parent.WithdrawId, PartSeq: 1, VaultID: job.VaultId, KeyEpoch: job.KeyEpoch, Gross: in.Amount, Fee: fee, OriginID: u.OriginId, TemplateHash: job.TemplateHash})
	delete(f.reader.data, keys.KeyFrostWithdrawJob(old))
	put(keys.KeyFrostWithdrawJob(job.JobId), &job)
	put(keys.KeyFrostWithdraw(parent.WithdrawId), parent)
	f.reader.data[keys.KeyFrostBtcWithdrawPart(parent.WithdrawId, 1)] = []byte(job.JobId)
	f.reader.data[f.lockKey] = []byte(job.JobId)
	f.env.SessionID = job.JobId
	f.messages, _ = template.ComputeTaprootSighash([][]byte{u.ScriptPubkey}, btc.SighashDefault)
	return f
}

func TestBTCIsolatedSigningBindsPartWithoutParentSigningStatus(t *testing.T) {
	f := newBTCIsolatedSigningFixture(t)
	if e := newSigningRequestValidator(f.reader)(f.env, f.messages); e != nil {
		t.Fatal(e)
	}
	if f.reader.scanCount != 0 || f.reader.batchCount != 1 {
		t.Fatal("isolated signer must batch its point reads")
	}
	var job pb.FrostWithdrawJobState
	proto.Unmarshal(f.reader.data[keys.KeyFrostWithdrawJob(f.env.SessionID)], &job)
	if _, e := ResolveGuardianIntent(f.reader, guardian.Request{Chain: "btc", VaultID: f.env.VaultID, Epoch: f.env.Epoch, JobID: job.JobId, TemplateHash: job.TemplateHash}); e != nil {
		t.Fatal(e)
	}
}

func TestBTCIsolatedSigningRejectsPartFieldMutation(t *testing.T) {
	for _, field := range []string{"gross", "fee", "part", "origin", "member"} {
		t.Run(field, func(t *testing.T) {
			f := newBTCIsolatedSigningFixture(t)
			var job pb.FrostWithdrawJobState
			proto.Unmarshal(f.reader.data[keys.KeyFrostWithdrawJob(f.env.SessionID)], &job)
			switch field {
			case "gross":
				job.BtcGrossAmount = "125001"
			case "fee":
				job.BtcFeeAmount = "1"
			case "part":
				job.BtcPartSeq = 2
			case "origin":
				job.BtcOriginId = make([]byte, 32)
			case "member":
				delete(f.reader.data, keys.KeyFrostBtcWithdrawPart(job.WithdrawIds[0], job.BtcPartSeq))
			}
			f.reader.data[keys.KeyFrostWithdrawJob(job.JobId)], _ = proto.Marshal(&job)
			if e := newSigningRequestValidator(f.reader)(f.env, f.messages); e == nil {
				t.Fatal("mutated child accepted")
			}
		})
	}
}
