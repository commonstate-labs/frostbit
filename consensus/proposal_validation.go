package consensus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"dex/config"
	"dex/keys"
	"dex/liveness"
	"dex/pb"
	"dex/protocol"
	"dex/types"
	"dex/utils"

	"google.golang.org/protobuf/proto"
)

                                                                          
                                                                  
const MaxStakeWeightRatio = 0.20

                                                                           
                                                                 
                                                                              
                                                    
type ProposalEligibilityInput struct {
	Probability       float64
	ExpectedProposers float64
	ProposerStake     float64
	TotalStake        float64
	VRFOutput         []byte
}

type proposalStateReader interface {
	Get(key string) ([]byte, error)
}

                                                                              
                                                                             
                             
func validatePushQueryProposerBinding(msg types.Message) error {
	if msg.Block == nil {
		return fmt.Errorf("missing PushQuery block")
	}
	proposer := msg.Block.Header.Proposer
	if proposer == "" {
		return fmt.Errorf("missing block proposer")
	}
	if string(msg.From) != proposer {
		return fmt.Errorf("PushQuery sender %s does not match proposer %s", msg.From, proposer)
	}
	return validateHeaderMinerBinding(msg.Block.Header)
}

func validateHeaderMinerBinding(header types.BlockHeader) error {
	if header.Proposer == "" {
		return fmt.Errorf("missing block proposer")
	}
	if header.Miner == "" {
		return fmt.Errorf("missing block miner")
	}
	if miner := keys.MinerAddress(header.Miner); miner != header.Proposer {
		return fmt.Errorf("block miner %s does not match proposer %s", miner, header.Proposer)
	}
	if err := validateLivenessReportShape(header); err != nil {
		return err
	}
	return nil
}

func validateLivenessReportShape(header types.BlockHeader) error {
	if len(header.SampledReports) > protocol.MaxLivenessReportsPerBlock {
		return fmt.Errorf("sampled liveness reports exceed protocol limit %d", protocol.MaxLivenessReportsPerBlock)
	}
	if len(header.OfflineReports) > protocol.MaxLivenessReportsPerBlock {
		return fmt.Errorf("offline liveness reports exceed protocol limit %d", protocol.MaxLivenessReportsPerBlock)
	}
	if len(header.OnlineReports) > protocol.MaxLivenessReportsPerBlock {
		return fmt.Errorf("online liveness reports exceed protocol limit %d", protocol.MaxLivenessReportsPerBlock)
	}
	strictlyIncreasing := func(values []uint32) bool {
		for i := 1; i < len(values); i++ {
			if values[i] <= values[i-1] {
				return false
			}
		}
		return true
	}
	if !strictlyIncreasing(header.SampledReports) {
		return fmt.Errorf("sampled liveness reports must be sorted and unique")
	}
	if !strictlyIncreasing(header.OfflineReports) {
		return fmt.Errorf("offline liveness reports must be sorted and unique")
	}
	if !strictlyIncreasing(header.OnlineReports) {
		return fmt.Errorf("online liveness reports must be sorted and unique")
	}
	sampled := make(map[uint32]struct{}, len(header.SampledReports))
	for _, index := range header.SampledReports {
		sampled[index] = struct{}{}
	}
	offline := make(map[uint32]struct{}, len(header.OfflineReports))
	for _, index := range header.OfflineReports {
		if _, exists := sampled[index]; !exists {
			return fmt.Errorf("offline liveness report %d is absent from sampled reports", index)
		}
		offline[index] = struct{}{}
	}
	for _, index := range header.OnlineReports {
		if _, exists := sampled[index]; !exists {
			return fmt.Errorf("online liveness report %d is absent from sampled reports", index)
		}
		if _, exists := offline[index]; exists {
			return fmt.Errorf("online liveness report %d conflicts with offline reports", index)
		}
	}
	return nil
}

func loadProposerAccount(reader proposalStateReader, proposer string) (*pb.Account, error) {
	if reader == nil {
		return nil, fmt.Errorf("proposal state is unavailable")
	}
	if proposer == "" {
		return nil, fmt.Errorf("missing block proposer")
	}

	raw, err := reader.Get(keys.KeyAccount(proposer))
	if err != nil {
		return nil, fmt.Errorf("load proposer account %s: %w", proposer, err)
	}
	var account pb.Account
	if err := proto.Unmarshal(raw, &account); err != nil {
		return nil, fmt.Errorf("decode proposer account %s: %w", proposer, err)
	}
	if account.Address != proposer {
		return nil, fmt.Errorf("proposer account address mismatch: key=%s account=%s", proposer, account.Address)
	}
	return &account, nil
}

                                                                             
                                                                             
                                      
func validateRegisteredProposerKey(
	reader proposalStateReader,
	header types.BlockHeader,
) (*pb.Account, []byte, error) {
	if err := validateHeaderMinerBinding(header); err != nil {
		return nil, nil, err
	}
	account, err := loadProposerAccount(reader, header.Proposer)
	if err != nil {
		return nil, nil, err
	}
	if account.PublicKeys == nil {
		return nil, nil, fmt.Errorf("proposer %s has no registered public keys", header.Proposer)
	}
	registered := account.PublicKeys.Keys[int32(pb.SignAlgo_SIGN_ALGO_BLS_BN256_G2)]
	if len(registered) == 0 {
		return nil, nil, fmt.Errorf("proposer %s has no registered BLS key", header.Proposer)
	}
	if !bytes.Equal(header.PublicKey, registered) {
		return nil, nil, fmt.Errorf("block BLS key does not match proposer %s registered key", header.Proposer)
	}
	return account, registered, nil
}

func validateActiveProposer(
	reader proposalStateReader,
	account *pb.Account,
) error {
	if account == nil {
		return fmt.Errorf("missing proposer account")
	}
	if !account.IsMiner {
		return fmt.Errorf("proposer %s is not a miner", account.Address)
	}
	if account.Index > math.MaxUint32 {
		return fmt.Errorf("proposer %s has invalid miner index %d", account.Address, account.Index)
	}

	raw, err := reader.Get(keys.KeyLivenessMiner(uint32(account.Index)))
	if err != nil {
		return fmt.Errorf("load proposer %s liveness state: %w", account.Address, err)
	}
	var miner liveness.MinerInfo
	if err := json.Unmarshal(raw, &miner); err != nil {
		return fmt.Errorf("decode proposer %s liveness state: %w", account.Address, err)
	}
	if miner.Index != uint32(account.Index) || miner.Address != account.Address {
		return fmt.Errorf("proposer %s liveness identity mismatch", account.Address)
	}
	if miner.State != liveness.StateActive {
		return fmt.Errorf("proposer %s is not active (state=%d)", account.Address, miner.State)
	}
	return nil
}

func evaluateProposalEligibility(
	reader proposalStateReader,
	windowConfig config.WindowConfig,
	proposer string,
	nodeID types.NodeID,
	window int,
	vrfOutput []byte,
) (bool, float64, error) {
	if err := validateProposalWindow(windowConfig, window); err != nil {
		return false, 0, err
	}
	if !windowConfig.Enabled {
		return int(nodeID.Last2Mod100())%100 < 5, 0.05, nil
	}

	stage := windowConfig.Stages[window]
	threshold := stage.Probability
	if stage.ExpectedProposers > 0 {
		account, err := loadProposerAccount(reader, proposer)
		if err != nil {
			return false, 0, err
		}
		return evaluateProposalEligibilityWithAccount(reader, stage, account, vrfOutput)
	}
	return EvaluateProposalEligibility(ProposalEligibilityInput{
		Probability:       threshold,
		ExpectedProposers: stage.ExpectedProposers,
		VRFOutput:         vrfOutput,
	})
}

func evaluateProposalEligibilityWithAccount(
	reader proposalStateReader,
	stage config.WindowStage,
	account *pb.Account,
	vrfOutput []byte,
) (bool, float64, error) {
	input := ProposalEligibilityInput{
		Probability:       stage.Probability,
		ExpectedProposers: stage.ExpectedProposers,
		VRFOutput:         vrfOutput,
	}
	if stage.ExpectedProposers > 0 {
		if math.IsNaN(stage.ExpectedProposers) || math.IsInf(stage.ExpectedProposers, 0) {
			return false, 0, fmt.Errorf("invalid expected proposer count %.6f", stage.ExpectedProposers)
		}
		proposerStake, totalStake, err := loadProposerStakeAmounts(reader, account)
		if err != nil {
			return false, 0, err
		}
		input.ProposerStake = proposerStake
		input.TotalStake = totalStake
	}
	return EvaluateProposalEligibility(input)
}

                                                                               
                                                                             
                                                                              
                                                                          
func EvaluateProposalEligibility(input ProposalEligibilityInput) (bool, float64, error) {
	if math.IsNaN(input.ExpectedProposers) || math.IsInf(input.ExpectedProposers, 0) {
		return false, 0, fmt.Errorf("invalid expected proposer count %.6f", input.ExpectedProposers)
	}
	threshold := input.Probability
	if input.ExpectedProposers > 0 {
		if input.ProposerStake <= 0 ||
			math.IsNaN(input.ProposerStake) || math.IsInf(input.ProposerStake, 0) {
			return false, 0, fmt.Errorf("invalid proposer stake %.6f", input.ProposerStake)
		}
		if input.TotalStake <= 0 ||
			math.IsNaN(input.TotalStake) || math.IsInf(input.TotalStake, 0) {
			return false, 0, fmt.Errorf("invalid total stake %.6f", input.TotalStake)
		}
		if input.ProposerStake > input.TotalStake {
			return false, 0, fmt.Errorf(
				"proposer stake %.6f exceeds total stake %.6f",
				input.ProposerStake,
				input.TotalStake,
			)
		}
		stakeRatio := input.ProposerStake / input.TotalStake
		if stakeRatio > MaxStakeWeightRatio {
			stakeRatio = MaxStakeWeightRatio
		}
		threshold = input.ExpectedProposers * stakeRatio
		if threshold > 1 {
			threshold = 1
		}
	}
	if !validProposalThreshold(threshold) {
		return false, 0, fmt.Errorf("invalid proposal threshold %.6f", threshold)
	}
	return utils.CalculateBlockProbability(input.VRFOutput, threshold), threshold, nil
}

func loadProposerStakeAmounts(
	reader proposalStateReader,
	account *pb.Account,
) (float64, float64, error) {
	if reader == nil || account == nil {
		return 0, 0, fmt.Errorf("proposal stake state is unavailable")
	}

	balanceRaw, err := reader.Get(keys.KeyBalance(account.Address, "FB"))
	if err != nil {
		return 0, 0, fmt.Errorf("load proposer %s stake balance: %w", account.Address, err)
	}
	var balance pb.TokenBalanceRecord
	if err := proto.Unmarshal(balanceRaw, &balance); err != nil {
		return 0, 0, fmt.Errorf("decode proposer %s stake balance: %w", account.Address, err)
	}
	if balance.Balance == nil {
		return 0, 0, fmt.Errorf("proposer %s stake balance is missing", account.Address)
	}
	myStake, err := strconv.ParseFloat(balance.Balance.MinerLockedBalance, 64)
	if err != nil || myStake <= 0 || math.IsNaN(myStake) || math.IsInf(myStake, 0) {
		return 0, 0, fmt.Errorf("proposer %s has invalid locked stake %q", account.Address, balance.Balance.MinerLockedBalance)
	}

	totalRaw, err := reader.Get(keys.KeyTotalMinerStake())
	if err != nil {
		return 0, 0, fmt.Errorf("load total miner stake: %w", err)
	}
	totalStake, err := strconv.ParseFloat(string(totalRaw), 64)
	if err != nil || totalStake <= 0 || math.IsNaN(totalStake) || math.IsInf(totalStake, 0) {
		return 0, 0, fmt.Errorf("invalid total miner stake %q", string(totalRaw))
	}
	if myStake > totalStake {
		return 0, 0, fmt.Errorf("proposer %s stake exceeds total miner stake", account.Address)
	}
	return myStake, totalStake, nil
}

func validProposalThreshold(threshold float64) bool {
	return threshold >= 0 && threshold <= 1 && !math.IsNaN(threshold) && !math.IsInf(threshold, 0)
}

func validateWindowConfiguration(windowConfig config.WindowConfig) error {
	return config.ValidateWindowConfig(windowConfig)
}

func validateProposalWindow(windowConfig config.WindowConfig, window int) error {
	if window < 0 {
		return fmt.Errorf("invalid proposal window %d", window)
	}
	if !windowConfig.Enabled {
		if window != 0 {
			return fmt.Errorf("invalid proposal window %d: window mechanism is disabled", window)
		}
		return nil
	}
	if len(windowConfig.Stages) == 0 {
		return fmt.Errorf("invalid proposal window configuration: enabled with no stages")
	}
	if window >= len(windowConfig.Stages) {
		return fmt.Errorf("invalid proposal window %d: configured range is 0..%d", window, len(windowConfig.Stages)-1)
	}
	return nil
}

func validateProposalStateAndEligibility(
	reader proposalStateReader,
	windowConfig config.WindowConfig,
	header types.BlockHeader,
	account *pb.Account,
) error {
	if err := validateProposalWindow(windowConfig, header.Window); err != nil {
		return err
	}
	if err := validateActiveProposer(reader, account); err != nil {
		return err
	}
	if !windowConfig.Enabled {
		eligible, _, err := evaluateProposalEligibility(
			reader, windowConfig, header.Proposer, types.NodeID(header.Proposer), header.Window, header.VRFOutput,
		)
		if err != nil {
			return err
		}
		if !eligible {
			return fmt.Errorf("proposer %s is not eligible in window %d", header.Proposer, header.Window)
		}
		return nil
	}
	eligible, threshold, err := evaluateProposalEligibilityWithAccount(
		reader, windowConfig.Stages[header.Window], account, header.VRFOutput,
	)
	if err != nil {
		return err
	}
	if !eligible {
		return fmt.Errorf(
			"proposer %s is not eligible in window %d (threshold=%.6f)",
			header.Proposer, header.Window, threshold,
		)
	}
	return nil
}

                                                                            
                                                                         
                                                                             
                      
func validateProposerVRF(
	reader proposalStateReader,
	windowConfig config.WindowConfig,
	block *types.Block,
	requireEligibility bool,
) error {
	if block == nil {
		return fmt.Errorf("missing block")
	}
	if block.Header.Height == 0 {
		return nil
	}
	if err := validateProposalWindow(windowConfig, block.Header.Window); err != nil {
		return err
	}
	if len(block.Header.VRFProof) == 0 || len(block.Header.VRFOutput) == 0 {
		return fmt.Errorf("missing VRF proof or output")
	}
	if len(block.Header.PublicKey) == 0 {
		return fmt.Errorf("missing public key for VRF verification")
	}

	account, registeredKey, err := validateRegisteredProposerKey(reader, block.Header)
	if err != nil {
		return err
	}
	publicKey, err := utils.DeserializePublicKey(registeredKey)
	if err != nil {
		return fmt.Errorf("invalid registered BLS key for proposer %s: %w", block.Header.Proposer, err)
	}
	if err := utils.NewVRFProvider().VerifyVRFWithPublicKey(
		publicKey,
		block.Header.Height,
		block.Header.Window,
		block.Header.ParentID,
		types.NodeID(block.Header.Proposer),
		block.Header.VRFProof,
		block.Header.VRFOutput,
	); err != nil {
		return fmt.Errorf("verify proposer %s VRF: %w", block.Header.Proposer, err)
	}
	if requireEligibility {
		if err := validateProposalStateAndEligibility(reader, windowConfig, block.Header, account); err != nil {
			return err
		}
	}
	return nil
}
