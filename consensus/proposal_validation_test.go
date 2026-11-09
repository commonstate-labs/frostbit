package consensus

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"dex/config"
	"dex/keys"
	"dex/liveness"
	"dex/pb"
	"dex/protocol"
	"dex/types"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

func TestValidateHeaderMinerBindingRejectsUnboundedOrMalformedLivenessReports(t *testing.T) {
	base := types.BlockHeader{Proposer: "miner-a", Miner: keys.KeyNode() + "miner-a"}

	tooMany := base
	tooMany.SampledReports = make([]uint32, protocol.MaxLivenessReportsPerBlock+1)
	for i := range tooMany.SampledReports {
		tooMany.SampledReports[i] = uint32(i)
	}
	if err := validateHeaderMinerBinding(tooMany); err == nil || !strings.Contains(err.Error(), "protocol limit") {
		t.Fatalf("oversized report list was accepted: %v", err)
	}

	duplicate := base
	duplicate.SampledReports = []uint32{1, 1}
	if err := validateHeaderMinerBinding(duplicate); err == nil || !strings.Contains(err.Error(), "sorted and unique") {
		t.Fatalf("duplicate report list was accepted: %v", err)
	}

	notSampled := base
	notSampled.SampledReports = []uint32{1}
	notSampled.OfflineReports = []uint32{2}
	if err := validateHeaderMinerBinding(notSampled); err == nil || !strings.Contains(err.Error(), "absent from sampled") {
		t.Fatalf("offline report outside sampled set was accepted: %v", err)
	}
}

type proposalValidationState map[string][]byte

func (s proposalValidationState) Get(key string) ([]byte, error) {
	value, ok := s[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), value...), nil
}

func proposalValidationPutProto(t *testing.T, state proposalValidationState, key string, value proto.Message) {
	t.Helper()
	raw, err := proto.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	state[key] = raw
}

func proposalValidationBLSKey(t *testing.T, keyManager *utils.KeyManager) []byte {
	t.Helper()
	raw, err := keyManager.BLSPublicKeyBytes()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func proposalValidationStateForMiner(
	t *testing.T,
	proposer string,
	blsKey []byte,
	minerState liveness.MinerState,
) proposalValidationState {
	t.Helper()
	state := proposalValidationState{}
	proposalValidationPutProto(t, state, keys.KeyAccount(proposer), &pb.Account{
		Address: proposer,
		Index:   7,
		IsMiner: true,
		PublicKeys: &pb.PublicKeys{Keys: map[int32][]byte{
			int32(pb.SignAlgo_SIGN_ALGO_BLS_BN256_G2): append([]byte(nil), blsKey...),
		}},
	})
	livenessRaw, err := json.Marshal(&liveness.MinerInfo{
		Index:   7,
		Address: proposer,
		State:   minerState,
	})
	if err != nil {
		t.Fatal(err)
	}
	state[keys.KeyLivenessMiner(7)] = livenessRaw
	return state
}

func TestValidateProposerVRFRejectsEphemeralGrindingKey(t *testing.T) {
	registered := utils.NewKeyManager()
	if err := registered.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	attacker := utils.NewKeyManager()
	if err := attacker.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}

	proposer := registered.GetAddress()
	output, proof, err := attacker.GenerateVRF(1,
		0,
		"genesis",
		types.NodeID(proposer),
	)
	if err != nil {
		t.Fatal(err)
	}
	attackerBLS := proposalValidationBLSKey(t, attacker)
	attackerPublicKey, err := utils.DeserializePublicKey(attackerBLS)
	if err != nil {
		t.Fatal(err)
	}
	if err := utils.NewVRFProvider().VerifyVRFWithPublicKey(
		attackerPublicKey, 1, 0, "genesis", types.NodeID(proposer), proof, output,
	); err != nil {
		t.Fatalf("attack fixture must carry a cryptographically valid VRF: %v", err)
	}

	block := &types.Block{ID: "attacker-block", Header: types.BlockHeader{
		Height:    1,
		ParentID:  "genesis",
		Proposer:  proposer,
		Miner:     keys.KeyNode() + proposer,
		Window:    0,
		VRFProof:  proof,
		VRFOutput: output,
		PublicKey: attackerBLS,
	}}
	state := proposalValidationStateForMiner(t, proposer, proposalValidationBLSKey(t, registered), liveness.StateActive)
	cfg := config.WindowConfig{Enabled: true, Stages: []config.WindowStage{{Probability: 1}}}

	err = validateProposerVRF(state, cfg, block, true)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("ephemeral VRF key was not rejected: %v", err)
	}
}

func TestValidateProposerVRFAcceptsRegisteredActiveMiner(t *testing.T) {
	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	proposer := keyManager.GetAddress()
	output, proof, err := keyManager.GenerateVRF(1, 0, "genesis", types.NodeID(proposer))
	if err != nil {
		t.Fatal(err)
	}
	blsKey := proposalValidationBLSKey(t, keyManager)
	block := &types.Block{ID: "valid-block", Header: types.BlockHeader{
		Height:    1,
		ParentID:  "genesis",
		Proposer:  proposer,
		Miner:     keys.KeyNode() + proposer,
		Window:    0,
		VRFProof:  proof,
		VRFOutput: output,
		PublicKey: blsKey,
	}}
	state := proposalValidationStateForMiner(t, proposer, blsKey, liveness.StateActive)
	                                                                          
	                                                                  
	threshold := 0.5
	if !utils.CalculateBlockProbability(output, threshold) {
		threshold = 1
	}
	cfg := config.WindowConfig{Enabled: true, Stages: []config.WindowStage{{Probability: threshold}}}

	if err := validateProposerVRF(state, cfg, block, true); err != nil {
		t.Fatalf("registered active proposer was rejected: %v", err)
	}

	state = proposalValidationStateForMiner(t, proposer, blsKey, liveness.StateFrozen)
	if err := validateProposerVRF(state, cfg, block, true); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("frozen proposer was not rejected: %v", err)
	}
}

func TestValidateProposerVRFRejectsSuffixedProof(t *testing.T) {
	keyManager := utils.NewKeyManager()
	if err := keyManager.InitKeyRandom(); err != nil {
		t.Fatal(err)
	}
	proposer := keyManager.GetAddress()
	output, proof, err := keyManager.GenerateVRF(1, 0, "genesis", types.NodeID(proposer))
	if err != nil {
		t.Fatal(err)
	}
	blsKey := proposalValidationBLSKey(t, keyManager)
	state := proposalValidationStateForMiner(t, proposer, blsKey, liveness.StateActive)
	cfg := config.WindowConfig{Enabled: true, Stages: []config.WindowStage{{Probability: 1}}}
	block := &types.Block{ID: "valid-block", Header: types.BlockHeader{
		Height:    1,
		ParentID:  "genesis",
		Proposer:  proposer,
		Miner:     keys.KeyNode() + proposer,
		Window:    0,
		VRFProof:  proof,
		VRFOutput: output,
		PublicKey: blsKey,
	}}
	if err := validateProposerVRF(state, cfg, block, true); err != nil {
		t.Fatalf("valid fixture was rejected: %v", err)
	}

	block.Header.VRFProof = append(append([]byte(nil), proof...), []byte("grind")...)
	mutatedOutput := sha256.Sum256(block.Header.VRFProof)
	block.Header.VRFOutput = mutatedOutput[:]
	if err := validateProposerVRF(state, cfg, block, true); err == nil || !strings.Contains(err.Error(), "invalid VRF proof length") {
		t.Fatalf("suffixed proof was not rejected by consensus validation: %v", err)
	}
}

func TestStakeWeightedProposalEligibilityUsesHotStake(t *testing.T) {
	account := &pb.Account{Address: "hot"}
	state := proposalValidationState{}
	proposalValidationPutProto(t, state, keys.KeyBalance("hot", "FB"), &pb.TokenBalanceRecord{
		Address: "hot",
		Token:   "FB",
		Balance: &pb.TokenBalance{MinerLockedBalance: "10"},
	})
	state[keys.KeyTotalMinerStake()] = []byte("100")
	stage := config.WindowStage{ExpectedProposers: 5}

	eligible, threshold, err := evaluateProposalEligibilityWithAccount(state, stage, account, make([]byte, 8))
	if err != nil {
		t.Fatal(err)
	}
	if !eligible || threshold != 0.5 {
		t.Fatalf("eligibility=%v threshold=%v, want true/0.5", eligible, threshold)
	}
	if eligible, _, err := evaluateProposalEligibilityWithAccount(state, stage, account, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}); err != nil || eligible {
		t.Fatalf("high VRF output eligibility=%v err=%v, want false", eligible, err)
	}
}

func TestEvaluateProposalEligibilityPure(t *testing.T) {
	lowOutput := make([]byte, 8)
	highOutput := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

	tests := []struct {
		name          string
		input         ProposalEligibilityInput
		wantEligible  bool
		wantThreshold float64
		wantErr       bool
	}{
		{
			name: "fixed probability eligible",
			input: ProposalEligibilityInput{
				Probability: 0.20,
				VRFOutput:   lowOutput,
			},
			wantEligible:  true,
			wantThreshold: 0.20,
		},
		{
			name: "fixed probability ineligible",
			input: ProposalEligibilityInput{
				Probability: 0.20,
				VRFOutput:   highOutput,
			},
			wantThreshold: 0.20,
		},
		{
			name: "stake weight is below cap",
			input: ProposalEligibilityInput{
				ExpectedProposers: 1,
				ProposerStake:     19,
				TotalStake:        100,
				VRFOutput:         lowOutput,
			},
			wantEligible:  true,
			wantThreshold: 0.19,
		},
		{
			name: "stake weight is at cap",
			input: ProposalEligibilityInput{
				ExpectedProposers: 1,
				ProposerStake:     20,
				TotalStake:        100,
				VRFOutput:         lowOutput,
			},
			wantEligible:  true,
			wantThreshold: 0.20,
		},
		{
			name: "stake weight is above cap",
			input: ProposalEligibilityInput{
				ExpectedProposers: 1,
				ProposerStake:     21,
				TotalStake:        100,
				VRFOutput:         lowOutput,
			},
			wantEligible:  true,
			wantThreshold: 0.20,
		},
		{
			name: "threshold is capped at one",
			input: ProposalEligibilityInput{
				ExpectedProposers: 50,
				ProposerStake:     5,
				TotalStake:        100,
				VRFOutput:         highOutput,
			},
			wantEligible:  true,
			wantThreshold: 1,
		},
		{
			name: "zero proposer stake is invalid",
			input: ProposalEligibilityInput{
				ExpectedProposers: 5,
				TotalStake:        100,
				VRFOutput:         lowOutput,
			},
			wantErr: true,
		},
		{
			name: "proposer stake above total is invalid",
			input: ProposalEligibilityInput{
				ExpectedProposers: 5,
				ProposerStake:     101,
				TotalStake:        100,
				VRFOutput:         lowOutput,
			},
			wantErr: true,
		},
		{
			name: "non finite probability is invalid",
			input: ProposalEligibilityInput{
				Probability: math.NaN(),
				VRFOutput:   lowOutput,
			},
			wantErr: true,
		},
		{
			name: "non finite expected proposer count is invalid",
			input: ProposalEligibilityInput{
				ExpectedProposers: math.Inf(1),
				ProposerStake:     1,
				TotalStake:        100,
				VRFOutput:         lowOutput,
			},
			wantErr: true,
		},
		{
			name: "nan expected proposer count is invalid",
			input: ProposalEligibilityInput{
				Probability:       0.20,
				ExpectedProposers: math.NaN(),
				VRFOutput:         lowOutput,
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			eligible, threshold, err := EvaluateProposalEligibility(test.input)
			if (err != nil) != test.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, test.wantErr)
			}
			if test.wantErr {
				return
			}
			if eligible != test.wantEligible || threshold != test.wantThreshold {
				t.Fatalf(
					"eligibility=%v threshold=%v, want %v/%v",
					eligible,
					threshold,
					test.wantEligible,
					test.wantThreshold,
				)
			}
		})
	}
}

func TestValidateProposerVRFRejectsNonzeroWindowWhenDisabled(t *testing.T) {
	block := &types.Block{
		ID: "disabled-window-proof",
		Header: types.BlockHeader{
			Height:    1,
			ParentID:  "genesis",
			Proposer:  "proposer",
			Window:    1,
			VRFProof:  make([]byte, utils.VRFProofSize),
			VRFOutput: make([]byte, utils.VRFOutputSize),
			PublicKey: []byte{1},
		},
	}
	err := validateProposerVRF(nil, config.WindowConfig{Enabled: false}, block, false)
	if err == nil || !strings.Contains(err.Error(), "window mechanism is disabled") {
		t.Fatalf("disabled configuration accepted window 1: %v", err)
	}
}

func TestPushQuerySenderMustBeBlockProposer(t *testing.T) {
	block := &types.Block{ID: "block-1", Header: types.BlockHeader{
		Proposer: "miner-a",
		Miner:    keys.KeyNode() + "miner-a",
	}}
	if err := validatePushQueryProposerBinding(types.Message{From: "miner-a", Block: block}); err != nil {
		t.Fatalf("valid PushQuery binding rejected: %v", err)
	}
	if err := validatePushQueryProposerBinding(types.Message{From: "relay", Block: block}); err == nil {
		t.Fatal("non-proposer PushQuery sender was accepted")
	}

	block.Header.Miner = keys.KeyNode() + "miner-b"
	if err := validatePushQueryProposerBinding(types.Message{From: "miner-a", Block: block}); err == nil {
		t.Fatal("Header.Miner/proposer mismatch was accepted")
	}
}
