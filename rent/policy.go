package rent

import (
	"dex/config"
	"fmt"
	"math/big"
)

type ExemptAsset struct {
	Token   string
	Minimum *big.Int
}

func defaultExemptAssets() []ExemptAsset {
	defaults := config.DefaultRentExemptAssets()
	assets := make([]ExemptAsset, 0, len(defaults))
	for _, asset := range defaults {
		assets = append(assets, ExemptAsset{Token: asset.Token, Minimum: mustAmount(asset.MinimumRaw)})
	}
	return assets
}

                                                                            
                                                                      
type RentEvaluation struct {
	NominalFB        *AssetSummary
	ExemptAsset      string
	ExemptAssetMin   string
	ExemptAssetTotal string
	ExpireHeight     uint64
	NeedsFunding     bool
}

                                                                                
                                                                           
func (s *Service) IsExempt(owner string, policy Policy) (*RentEvaluation, error) {
	return s.isExempt(owner, normalizePolicy(policy))
}

                                                                             
                                                                          
func (s *Service) isExempt(owner string, policy Policy) (*RentEvaluation, error) {
	summary, err := s.NominalAsset(owner, policy.Token)
	if err != nil {
		return nil, err
	}
	evaluation := &RentEvaluation{NominalFB: summary}
	nominal, ok := new(big.Int).SetString(summary.Total, 10)
	if !ok {
		return nil, fmt.Errorf("rent: invalid nominal total %q", summary.Total)
	}
	if nominal.Cmp(policy.ExemptMin) >= 0 {
		evaluation.ExemptAsset = policy.Token
		evaluation.ExemptAssetMin = policy.ExemptMin.String()
		evaluation.ExemptAssetTotal = summary.Total
		return evaluation, nil
	}
	for _, asset := range policy.ExemptAssets {
		if asset.Token == "" || asset.Minimum == nil || asset.Minimum.Sign() <= 0 {
			return nil, fmt.Errorf("rent: invalid exempt asset policy")
		}
		summary, err := s.NominalAsset(owner, asset.Token)
		if err != nil {
			return nil, err
		}
		total, ok := new(big.Int).SetString(summary.Total, 10)
		if !ok {
			return nil, fmt.Errorf("rent: invalid nominal total %q", summary.Total)
		}
		if total.Cmp(asset.Minimum) >= 0 {
			evaluation.ExemptAsset = asset.Token
			evaluation.ExemptAssetMin = asset.Minimum.String()
			evaluation.ExemptAssetTotal = total.String()
			break
		}
	}
	return evaluation, nil
}

func (s *Service) EvaluateAccountRent(owner string, height uint64, policy Policy) (*RentEvaluation, error) {
	state, err := s.rentState(owner)
	if err != nil {
		return nil, err
	}
	return s.evaluateAccountRent(owner, height, normalizePolicy(policy), state)
}

func (s *Service) evaluateAccountRent(owner string, height uint64, policy Policy, state *RentState) (*RentEvaluation, error) {
	evaluation, err := s.isExempt(owner, policy)
	if err != nil || evaluation.ExemptAsset != "" {
		return evaluation, err
	}
	nominal := parseAmountString(evaluation.NominalFB.Total)
	if nominal.Sign() <= 0 {
		evaluation.ExpireHeight = height
		evaluation.NeedsFunding = true
		return evaluation, nil
	}
	if state != nil && state.ExpireHeight != 0 {
		evaluation.ExpireHeight = state.ExpireHeight
	} else {
		evaluation.ExpireHeight = height + ceilDivUint64(nominal, policy.RentPerBlock)
	}
	evaluation.NeedsFunding = evaluation.ExpireHeight <= height
	return evaluation, nil
}
