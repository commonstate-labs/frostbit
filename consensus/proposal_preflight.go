package consensus

import (
	"dex/interfaces"
	"dex/keys"
	"dex/pb"
	"dex/txpool"
	"dex/vm"
	"fmt"
	"strings"
)

                                                                            
                                                                               
type VMProposalPreflight struct {
	executor *vm.Executor
}

var _ interfaces.ProposalPreflight = (*VMProposalPreflight)(nil)

func NewVMProposalPreflight(executor *vm.Executor) *VMProposalPreflight {
	return &VMProposalPreflight{executor: executor}
}

func (p *VMProposalPreflight) ProjectSpotPrefix(ctx interfaces.ProposalContext, orderedPublic []*pb.AnyTx, maxOrdersPerBook int, takerAddress string) (*interfaces.ProposalSpotMarket, error) {
	if p == nil || p.executor == nil {
		return nil, fmt.Errorf("proposal preflight executor is not configured")
	}

	publicPrefixHash := txpool.ComputeTxsHash(orderedPublic)
	miner := string(ctx.Proposer)
	if miner != "" && !strings.HasPrefix(miner, keys.KeyNode()) {
		miner = keys.KeyNode() + miner
	}
	block := &pb.Block{
		Header: &pb.BlockHeader{
			Height:        ctx.Height,
			PrevBlockHash: ctx.ParentID,
			Miner:         miner,
			Window:        int32(ctx.Window),
			TxsHash:       publicPrefixHash,
		},
		Body: orderedPublic,
	}

	projection, err := p.executor.BuildProposalMarketProjection(block, maxOrdersPerBook, takerAddress)
	if err != nil {
		return nil, err
	}
	return proposalMarketFromVM(projection, publicPrefixHash, len(orderedPublic)), nil
}

func (p *VMProposalPreflight) Simulate(block *pb.Block) (*interfaces.ProposalSimulationResult, error) {
	if p == nil || p.executor == nil {
		return nil, fmt.Errorf("proposal preflight executor is not configured")
	}

	result, err := p.executor.BuildProposalSpecResult(block)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("proposal preflight returned no VM result")
	}
	return proposalSimulationResultFromVM(result), nil
}

func proposalSimulationResultFromVM(result *vm.SpecResult) *interfaces.ProposalSimulationResult {
	converted := &interfaces.ProposalSimulationResult{
		Valid:  result.Valid,
		Reason: result.Reason,
	}
	if len(result.Receipts) == 0 {
		return converted
	}

	converted.Receipts = make([]interfaces.ProposalSimulationReceipt, 0, len(result.Receipts))
	for _, receipt := range result.Receipts {
		if receipt == nil {
			continue
		}
		out := interfaces.ProposalSimulationReceipt{
			TxID:           receipt.TxID,
			Status:         receipt.Status,
			Error:          receipt.Error,
			BlockHeight:    receipt.BlockHeight,
			Fee:            receipt.Fee,
			FBBalanceAfter: receipt.FBBalanceAfter,
		}
		if len(receipt.Events) > 0 {
			out.Events = make([]interfaces.ProposalSimulationEvent, 0, len(receipt.Events))
			for _, event := range receipt.Events {
				out.Events = append(out.Events, interfaces.ProposalSimulationEvent{
					Kind:    string(event.Kind),
					Payload: cloneProposalEventPayload(event.Payload),
				})
			}
		}
		converted.Receipts = append(converted.Receipts, out)
	}
	return converted
}

func proposalSpotMarketFromVM(projection *vm.ProposalSpotProjection, publicPrefixHash string, publicPrefixCount int) *interfaces.ProposalSpotMarket {
	market := &interfaces.ProposalSpotMarket{
		PublicPrefixHash:  publicPrefixHash,
		PublicPrefixCount: publicPrefixCount,
	}
	if projection == nil {
		return market
	}

	market.Levels = proposalSpotLevelsFromVM(projection.Levels)
	market.Executions = make([]interfaces.ProposalSpotExecution, 0, len(projection.Executions))
	for _, execution := range projection.Executions {
		converted := interfaces.ProposalSpotExecution{
			PublicPosition: execution.PublicPosition,
			OrderID:        execution.OrderID,
			Owner:          execution.Owner,
			BaseToken:      execution.BaseToken,
			QuoteToken:     execution.QuoteToken,
			Side:           execution.Side,
			Price:          execution.Price,
			Amount:         execution.Amount,
			Remaining:      execution.Remaining,
			Levels:         proposalSpotLevelsFromVM(execution.Levels),
		}
		if len(execution.Matches) > 0 {
			converted.Matches = make([]interfaces.ProposalSpotMatch, 0, len(execution.Matches))
			for _, match := range execution.Matches {
				converted.Matches = append(converted.Matches, interfaces.ProposalSpotMatch{
					MakerOrderID: match.MakerOrderID,
					Quantity:     match.Quantity,
					Price:        match.Price,
				})
			}
		}
		market.Executions = append(market.Executions, converted)
	}
	return market
}

func proposalMarketFromVM(projection *vm.ProposalMarketProjection, publicPrefixHash string, publicPrefixCount int) *interfaces.ProposalSpotMarket {
	if projection == nil {
		return proposalSpotMarketFromVM(nil, publicPrefixHash, publicPrefixCount)
	}
	market := proposalSpotMarketFromVM(projection.Spot, publicPrefixHash, publicPrefixCount)
	if projection.Future == nil {
		return market
	}
	market.FutureLevels = proposalFutureLevelsFromVM(projection.Future.Levels)
	market.FutureExecutions = make([]interfaces.ProposalFutureExecution, 0, len(projection.Future.Executions))
	for _, execution := range projection.Future.Executions {
		converted := interfaces.ProposalFutureExecution{
			PublicPosition: execution.PublicPosition,
			OrderID:        execution.OrderID,
			Owner:          execution.Owner,
			Pair:           execution.Pair,
			QuoteToken:     execution.QuoteToken,
			Side:           execution.Side,
			Price:          execution.Price,
			Quantity:       execution.Quantity,
			Remaining:      execution.Remaining,
			Leverage:       execution.Leverage,
			Levels:         proposalFutureLevelsFromVM(execution.Levels),
		}
		if len(execution.TakerAvailable) > 0 {
			converted.TakerAvailable = make(map[string]string, len(execution.TakerAvailable))
			for token, amount := range execution.TakerAvailable {
				converted.TakerAvailable[token] = amount
			}
		}
		if len(execution.Matches) > 0 {
			converted.Matches = make([]interfaces.ProposalFutureMatch, 0, len(execution.Matches))
			for _, match := range execution.Matches {
				converted.Matches = append(converted.Matches, interfaces.ProposalFutureMatch{
					MakerOrderID: match.MakerOrderID,
					Quantity:     match.Quantity,
					Price:        match.Price,
				})
			}
		}
		market.FutureExecutions = append(market.FutureExecutions, converted)
	}
	if len(projection.Future.TakerAvailable) > 0 {
		market.FutureAvailable = make(map[string]string, len(projection.Future.TakerAvailable))
		for token, amount := range projection.Future.TakerAvailable {
			market.FutureAvailable[token] = amount
		}
	}
	return market
}

func proposalFutureLevelsFromVM(levels []vm.ProposalFutureLevelProjection) []interfaces.ProposalFutureLevel {
	if len(levels) == 0 {
		return nil
	}
	converted := make([]interfaces.ProposalFutureLevel, 0, len(levels))
	for _, level := range levels {
		out := interfaces.ProposalFutureLevel{
			Pair: level.Pair, QuoteToken: level.QuoteToken, TakerSide: level.TakerSide, Price: level.Price,
		}
		if len(level.Makers) > 0 {
			out.Makers = make([]interfaces.ProposalFutureMaker, 0, len(level.Makers))
			for _, maker := range level.Makers {
				out.Makers = append(out.Makers, interfaces.ProposalFutureMaker{
					OrderID: maker.OrderID, Owner: maker.Owner, Remaining: maker.Remaining, Leverage: maker.Leverage,
				})
			}
		}
		converted = append(converted, out)
	}
	return converted
}

func proposalSpotLevelsFromVM(levels []vm.ProposalSpotLevelProjection) []interfaces.ProposalSpotLevel {
	if len(levels) == 0 {
		return nil
	}
	convertedLevels := make([]interfaces.ProposalSpotLevel, 0, len(levels))
	for _, level := range levels {
		converted := interfaces.ProposalSpotLevel{
			BaseToken:  level.BaseToken,
			QuoteToken: level.QuoteToken,
			TakerSide:  level.TakerSide,
			Price:      level.Price,
		}
		if len(level.Makers) > 0 {
			converted.Makers = make([]interfaces.ProposalSpotMaker, 0, len(level.Makers))
			for _, maker := range level.Makers {
				converted.Makers = append(converted.Makers, interfaces.ProposalSpotMaker{
					OrderID:   maker.OrderID,
					Owner:     maker.Owner,
					Remaining: maker.Remaining,
				})
			}
		}
		convertedLevels = append(convertedLevels, converted)
	}
	return convertedLevels
}

func cloneProposalEventPayload(payload map[string]string) map[string]string {
	if payload == nil {
		return nil
	}
	cloned := make(map[string]string, len(payload))
	for key, value := range payload {
		cloned[key] = value
	}
	return cloned
}
