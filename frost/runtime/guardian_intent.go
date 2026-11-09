package runtime

import (
	"bytes"
	"fmt"
	"strings"

	"dex/frost/chain"
	"dex/frost/chain/btc"
	"dex/frost/chain/evm"
	"dex/frost/guardian"
	"dex/frost/runtime/roast"
	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
)

                                                                                
                                                                                  
func ResolveGuardianIntent(reader ChainStateReader, request guardian.Request) (*guardian.Intent, error) {
	name := chain.NormalizeChain(request.Chain)
	if name != request.Chain || !chain.IsValidChain(name) || request.Epoch == 0 || len(request.TemplateHash) != 32 {
		return nil, fmt.Errorf("invalid guardian intent identity")
	}
	var cfg pb.FrostVaultConfig
	var vault pb.FrostVaultState
	for _, record := range []struct {
		key   string
		value proto.Message
	}{{keys.KeyFrostVaultConfig(name, 0), &cfg}, {keys.KeyFrostVaultState(name, request.VaultID), &vault}} {
		raw, found, err := reader.Get(record.key)
		if err != nil || !found {
			return nil, fmt.Errorf("finalized guardian context unavailable")
		}
		if err := proto.Unmarshal(raw, record.value); err != nil {
			return nil, err
		}
	}
	if vault.KeyEpoch != request.Epoch || vault.VaultId != request.VaultID || chain.NormalizeChain(vault.Chain) != name || len(cfg.GuardianPubkey) == 0 {
		return nil, fmt.Errorf("guardian vault context mismatch")
	}
	env := &roast.Envelope{SessionID: request.JobID, Chain: name, VaultID: request.VaultID, Epoch: request.Epoch, SignAlgo: vault.SignAlgo}
	var messages [][]byte
	if strings.HasPrefix(request.JobID, "migration_") {
		var intent pb.FrostVaultMigrationState
		var transition pb.VaultTransitionState
		for _, record := range []struct {
			key   string
			value proto.Message
		}{{keys.KeyFrostVaultMigration(name, request.VaultID, request.Epoch+1), &intent}, {keys.KeyFrostVaultTransition(name, request.VaultID, request.Epoch+1), &transition}} {
			raw, found, err := reader.Get(record.key)
			if err != nil || !found {
				return nil, fmt.Errorf("finalized guardian migration unavailable")
			}
			if err := proto.Unmarshal(raw, record.value); err != nil {
				return nil, err
			}
		}
		parentIntentHash := append([]byte(nil), intent.IntentHash...)
		if strings.HasPrefix(request.JobID, "migration_part_") {
			childID, e := migrationChildSessionBase(request.JobID)
			if e != nil {
				return nil, e
			}
			raw, found, e := reader.Get(keys.KeyFrostBtcMigrationChildState(childID))
			if e != nil || !found || proto.Unmarshal(raw, &intent) != nil {
				return nil, fmt.Errorf("finalized guardian migration child unavailable")
			}
		}
		if !bytes.Equal(request.TemplateHash, intent.TemplateHash) {
			return nil, fmt.Errorf("guardian migration template conflict")
		}
		if name == chain.ChainBTC && !intent.NoFunds {
			template, err := btc.FromJSON(intent.TemplateData)
			if err != nil {
				return nil, err
			}
			messages, env.Tweaks, err = guardianBTCMessages(reader, request.VaultID, template)
			if err != nil {
				return nil, err
			}
		}
		messages = append(messages, parentIntentHash)
		if name == chain.ChainBTC {
			env.Tweaks = append(env.Tweaks, nil)
		}
		coordinator, err := chain.WithdrawCoordinator(request.JobID, request.Epoch, transition.OldCommitteeMembers)
		if err != nil {
			return nil, err
		}
		env.From = roast.NodeID(coordinator)
		if err := validateFinalizedMigrationSigningRequest(reader, env, messages); err != nil {
			return nil, err
		}
	} else {
		var job pb.FrostWithdrawJobState
		raw, found, err := reader.Get(keys.KeyFrostWithdrawJob(request.JobID))
		if err != nil || !found {
			return nil, fmt.Errorf("finalized guardian withdrawal unavailable")
		}
		if err := proto.Unmarshal(raw, &job); err != nil {
			return nil, err
		}
		if !bytes.Equal(job.TemplateHash, request.TemplateHash) {
			return nil, fmt.Errorf("guardian withdrawal template conflict")
		}
		if name == chain.ChainBTC {
			template, err := btc.FromJSON(job.TemplateData)
			if err != nil {
				return nil, err
			}
			messages, env.Tweaks, err = guardianBTCMessages(reader, request.VaultID, template)
			if err != nil {
				return nil, err
			}
			if err := validateBTCWithdrawSigningRequest(reader, env, messages, &job); err != nil {
				return nil, err
			}
		} else {
			template, err := evm.FromJSON(job.TemplateData)
			if err != nil {
				return nil, err
			}
			m, err := template.SigningHash()
			if err != nil {
				return nil, err
			}
			messages = [][]byte{m}
			if err := validateEVMWithdrawSigningRequest(reader, env, messages, &job); err != nil {
				return nil, err
			}
		}
	}
	return &guardian.Intent{Request: request, SignAlgo: int32(vault.SignAlgo), PublicKey: append([]byte(nil), cfg.GuardianPubkey...), Messages: messages}, nil
}

func guardianBTCMessages(reader ChainStateReader, vaultID uint32, t *btc.BTCTemplate) ([][]byte, [][]byte, error) {
	stateKeys := make([]string, len(t.Inputs))
	for i, in := range t.Inputs {
		stateKeys[i] = keys.KeyFrostBtcUtxo(vaultID, in.TxID, in.Vout)
	}
	values, err := readSigningState(reader, stateKeys)
	if err != nil {
		return nil, nil, err
	}
	scripts, tweaks := make([][]byte, len(t.Inputs)), make([][]byte, len(t.Inputs))
	for i, key := range stateKeys {
		var u pb.FrostUtxo
		if len(values[key]) == 0 || proto.Unmarshal(values[key], &u) != nil {
			return nil, nil, fmt.Errorf("guardian input unavailable")
		}
		scripts[i] = u.ScriptPubkey
		tweaks[i] = u.Tweak
	}
	messages, err := t.ComputeTaprootSighash(scripts, btc.SighashDefault)
	return messages, tweaks, err
}
