                                                                           
                                                                         
                                                    
package refinement

import (
	"dex/formal/itftrace"
	"fmt"
	"sort"
)

type ConservationState struct {
	Alice            int64
	Bob              int64
	RuntimeFeePool   int64
	RecycleFund      int64
	PositionMargin   int64
	InsuranceFund    int64
	ChallengeStake   int64
	TotalSupply      int64
	PositionOpen     bool
	InsuranceCredits int64
	InsuranceDebits  int64
	ADLShortfall     int64
}

type FrontierState struct {
	Responded        []string
	SupportA         []string
	SupportB         []string
	AnchorMismatches []string
	Phase            string
	SelectedTarget   string
}

type SnowballState struct {
	Preference           string
	ConfidencePreference string
	Confidence           int64
	StrengthA            int64
	StrengthB            int64
	TrackedA             bool
	TrackedB             bool
	Finalized            bool
	FinalCandidate       string
}

type DkgState struct {
	Epoch                int64
	Attempt              int64
	Session              string
	Status               string
	Lifecycle            string
	Eligible             []string
	Commits              []string
	Shares               []string
	UnresolvedComplaints []string
	ValidationAcks       []string
	TempSession          string
	TempStatePresent     bool
	KeyMaterialReady     bool
}

type AuthorityHandoverState struct {
	Chain                  string
	Phase                  string
	Authority              string
	CurrentEpoch           int64
	TargetEpoch            int64
	OldWithdrawPending     bool
	OldRechargePending     bool
	IntentBound            bool
	SignatureBoundToIntent bool
	OldTransitionRetired   bool
}

type VaultRotationOperationsState struct {
	Phase             string
	Authority         string
	CurrentEpoch      int64
	Withdraw          string
	VaultAvailable    int64
	VaultReserved     int64
	AuthorizedOutflow int64
	ArtifactPublished bool
	Outcome           string
}

type ExternalAuthorityState struct {
	Phase                 string
	DexAuthority          string
	ExternalAuthority     string
	StandaloneAttempts    int64
	StandaloneBroadcasts  int64
	BatchAttempts         int64
	FirstRequestConsumed  bool
	SecondRequestConsumed bool
}

type RentLifecycleState struct {
	Phase          string
	Height         int64
	PrimaryFB      int64
	ClaimFB        int64
	SourceFB       int64
	RecycleFB      int64
	ExternalAsset  int64
	RentState      bool
	ExpiryIndex    bool
	ExpireHeight   int64
	LeaseNominal   int64
	AccountPresent bool
	ClaimPresent   bool
}

type RechargeRentActivationState struct {
	Mode            string
	Phase           string
	Height          int64
	DepositAmount   int64
	OwnerAsset      int64
	OwnerFB         int64
	MakerAsset      int64
	MakerFB         int64
	RecycleAsset    int64
	FBTotal         int64
	TotalSupply     int64
	VaultAvailable  int64
	Credited        bool
	AccountPresent  bool
	Nonce           int64
	RentState       bool
	ExpiryIndex     bool
	ExpireHeight    int64
	MarketOrderOpen bool
}

type TxPoolAdmissionState struct {
	Phase          string
	FinalizedNonce int64
	Balance        int64
	Pending        []int64
	Persisted      []int64
	ReservedFees   int64
	NextNonce      int64
	Accepted       int64
	Rejected       int64
	SaveFailures   int64
}

type AccountNonceExecutionState struct {
	Phase      string
	Nonce      int64
	Balance    int64
	FeePool    int64
	Recipient  int64
	Total      int64
	Marker     bool
	BlockValid bool
	Succeeded  int64
	Failed     int64
	LastFee    int64
	Outcome    string
}

type RechargeChallengeLifecycleState struct {
	Phase            string
	Height           int64
	RequestStatus    string
	ChallengeStatus  string
	ArbitrationRound int64
	RequestPass      int64
	RequestFail      int64
	ArbitrationPass  int64
	ArbitrationFail  int64
	ChallengeSuccess bool
	Credited         bool
	UserBalance      int64
	VaultAvailable   int64
	TotalSupply      int64
	ChallengeDue     int64
	TimeoutDue       int64
	FinalizeHeight   int64
}

func RechargeChallengeLifecycleFromITF(state itftrace.State) (RechargeChallengeLifecycleState, error) {
	readTag := func(name string) (string, error) {
		value, err := state.Tag(name)
		if err != nil {
			return "", fmt.Errorf("recharge challenge lifecycle %s: %w", name, err)
		}
		return value, nil
	}
	readInt := func(name string) (int64, error) {
		value, err := state.Int(name)
		if err != nil {
			return 0, fmt.Errorf("recharge challenge lifecycle %s: %w", name, err)
		}
		return value, nil
	}
	readBool := func(name string) (bool, error) {
		value, err := state.Bool(name)
		if err != nil {
			return false, fmt.Errorf("recharge challenge lifecycle %s: %w", name, err)
		}
		return value, nil
	}

	var result RechargeChallengeLifecycleState
	var err error
	if result.Phase, err = readTag("phase"); err != nil {
		return RechargeChallengeLifecycleState{}, err
	}
	if result.RequestStatus, err = readTag("requestStatus"); err != nil {
		return RechargeChallengeLifecycleState{}, err
	}
	if result.ChallengeStatus, err = readTag("challengeStatus"); err != nil {
		return RechargeChallengeLifecycleState{}, err
	}
	for name, target := range map[string]*int64{
		"height":           &result.Height,
		"arbitrationRound": &result.ArbitrationRound,
		"requestPass":      &result.RequestPass,
		"requestFail":      &result.RequestFail,
		"arbitrationPass":  &result.ArbitrationPass,
		"arbitrationFail":  &result.ArbitrationFail,
		"userBalance":      &result.UserBalance,
		"vaultAvailable":   &result.VaultAvailable,
		"totalSupply":      &result.TotalSupply,
		"challengeDue":     &result.ChallengeDue,
		"timeoutDue":       &result.TimeoutDue,
		"finalizeHeight":   &result.FinalizeHeight,
	} {
		if *target, err = readInt(name); err != nil {
			return RechargeChallengeLifecycleState{}, err
		}
	}
	if result.ChallengeSuccess, err = readBool("challengeSuccess"); err != nil {
		return RechargeChallengeLifecycleState{}, err
	}
	if result.Credited, err = readBool("credited"); err != nil {
		return RechargeChallengeLifecycleState{}, err
	}
	return result, nil
}

func AccountNonceExecutionFromITF(state itftrace.State) (AccountNonceExecutionState, error) {
	readTag := func(name string) (string, error) {
		value, err := state.Tag(name)
		if err != nil {
			return "", fmt.Errorf("account nonce execution %s: %w", name, err)
		}
		return value, nil
	}
	readInt := func(name string) (int64, error) {
		value, err := state.Int(name)
		if err != nil {
			return 0, fmt.Errorf("account nonce execution %s: %w", name, err)
		}
		return value, nil
	}
	readBool := func(name string) (bool, error) {
		value, err := state.Bool(name)
		if err != nil {
			return false, fmt.Errorf("account nonce execution %s: %w", name, err)
		}
		return value, nil
	}

	var result AccountNonceExecutionState
	var err error
	if result.Phase, err = readTag("phase"); err != nil {
		return AccountNonceExecutionState{}, err
	}
	if result.Outcome, err = readTag("outcome"); err != nil {
		return AccountNonceExecutionState{}, err
	}
	for name, target := range map[string]*int64{
		"nonce":     &result.Nonce,
		"balance":   &result.Balance,
		"feePool":   &result.FeePool,
		"recipient": &result.Recipient,
		"total":     &result.Total,
		"succeeded": &result.Succeeded,
		"failed":    &result.Failed,
		"lastFee":   &result.LastFee,
	} {
		if *target, err = readInt(name); err != nil {
			return AccountNonceExecutionState{}, err
		}
	}
	if result.Marker, err = readBool("marker"); err != nil {
		return AccountNonceExecutionState{}, err
	}
	if result.BlockValid, err = readBool("blockValid"); err != nil {
		return AccountNonceExecutionState{}, err
	}
	return result, nil
}

func TxPoolAdmissionFromITF(state itftrace.State) (TxPoolAdmissionState, error) {
	phase, err := state.Tag("phase")
	if err != nil {
		return TxPoolAdmissionState{}, fmt.Errorf("txpool admission phase: %w", err)
	}
	result := TxPoolAdmissionState{Phase: phase}
	for name, target := range map[string]*int64{
		"finalizedNonce": &result.FinalizedNonce,
		"balance":        &result.Balance,
		"reservedFees":   &result.ReservedFees,
		"nextNonce":      &result.NextNonce,
		"accepted":       &result.Accepted,
		"rejected":       &result.Rejected,
		"saveFailures":   &result.SaveFailures,
	} {
		if *target, err = state.Int(name); err != nil {
			return TxPoolAdmissionState{}, fmt.Errorf("txpool admission %s: %w", name, err)
		}
	}
	if result.Pending, err = sortedITFIntSet(state, "pending"); err != nil {
		return TxPoolAdmissionState{}, err
	}
	if result.Persisted, err = sortedITFIntSet(state, "persisted"); err != nil {
		return TxPoolAdmissionState{}, err
	}
	return result, nil
}

func RentLifecycleFromITF(state itftrace.State) (RentLifecycleState, error) {
	readTag := func(name string) (string, error) {
		value, err := state.Tag(name)
		if err != nil {
			return "", fmt.Errorf("rent lifecycle %s: %w", name, err)
		}
		return value, nil
	}
	readBool := func(name string) (bool, error) {
		value, err := state.Bool(name)
		if err != nil {
			return false, fmt.Errorf("rent lifecycle %s: %w", name, err)
		}
		return value, nil
	}
	readInt := func(name string) (int64, error) {
		value, err := state.Int(name)
		if err != nil {
			return 0, fmt.Errorf("rent lifecycle %s: %w", name, err)
		}
		return value, nil
	}

	var result RentLifecycleState
	var err error
	if result.Phase, err = readTag("phase"); err != nil {
		return RentLifecycleState{}, err
	}
	for name, target := range map[string]*int64{
		"height":        &result.Height,
		"primaryFB":     &result.PrimaryFB,
		"claimFB":       &result.ClaimFB,
		"sourceFB":      &result.SourceFB,
		"recycleFB":     &result.RecycleFB,
		"externalAsset": &result.ExternalAsset,
		"expireHeight":  &result.ExpireHeight,
		"leaseNominal":  &result.LeaseNominal,
	} {
		if *target, err = readInt(name); err != nil {
			return RentLifecycleState{}, err
		}
	}
	for name, target := range map[string]*bool{
		"rentState":      &result.RentState,
		"expiryIndex":    &result.ExpiryIndex,
		"accountPresent": &result.AccountPresent,
		"claimPresent":   &result.ClaimPresent,
	} {
		if *target, err = readBool(name); err != nil {
			return RentLifecycleState{}, err
		}
	}
	return result, nil
}

func RechargeRentActivationFromITF(state itftrace.State) (RechargeRentActivationState, error) {
	readTag := func(name string) (string, error) {
		value, err := state.Tag(name)
		if err != nil {
			return "", fmt.Errorf("recharge rent activation %s: %w", name, err)
		}
		return value, nil
	}
	readBool := func(name string) (bool, error) {
		value, err := state.Bool(name)
		if err != nil {
			return false, fmt.Errorf("recharge rent activation %s: %w", name, err)
		}
		return value, nil
	}
	readInt := func(name string) (int64, error) {
		value, err := state.Int(name)
		if err != nil {
			return 0, fmt.Errorf("recharge rent activation %s: %w", name, err)
		}
		return value, nil
	}

	var result RechargeRentActivationState
	var err error
	if result.Mode, err = readTag("mode"); err != nil {
		return RechargeRentActivationState{}, err
	}
	if result.Phase, err = readTag("phase"); err != nil {
		return RechargeRentActivationState{}, err
	}
	for name, target := range map[string]*int64{
		"height":         &result.Height,
		"depositAmount":  &result.DepositAmount,
		"ownerAsset":     &result.OwnerAsset,
		"ownerFB":        &result.OwnerFB,
		"makerAsset":     &result.MakerAsset,
		"makerFB":        &result.MakerFB,
		"recycleAsset":   &result.RecycleAsset,
		"fbTotal":        &result.FBTotal,
		"totalSupply":    &result.TotalSupply,
		"vaultAvailable": &result.VaultAvailable,
		"nonce":          &result.Nonce,
		"expireHeight":   &result.ExpireHeight,
	} {
		if *target, err = readInt(name); err != nil {
			return RechargeRentActivationState{}, err
		}
	}
	for name, target := range map[string]*bool{
		"credited":        &result.Credited,
		"accountPresent":  &result.AccountPresent,
		"rentState":       &result.RentState,
		"expiryIndex":     &result.ExpiryIndex,
		"marketOrderOpen": &result.MarketOrderOpen,
	} {
		if *target, err = readBool(name); err != nil {
			return RechargeRentActivationState{}, err
		}
	}
	return result, nil
}

func ExternalAuthorityFromITF(state itftrace.State) (ExternalAuthorityState, error) {
	readTag := func(name string) (string, error) {
		value, err := state.Tag(name)
		if err != nil {
			return "", fmt.Errorf("external authority %s: %w", name, err)
		}
		return value, nil
	}
	readBool := func(name string) (bool, error) {
		value, err := state.Bool(name)
		if err != nil {
			return false, fmt.Errorf("external authority %s: %w", name, err)
		}
		return value, nil
	}
	readInt := func(name string) (int64, error) {
		value, err := state.Int(name)
		if err != nil {
			return 0, fmt.Errorf("external authority %s: %w", name, err)
		}
		return value, nil
	}
	var result ExternalAuthorityState
	var err error
	if result.Phase, err = readTag("phase"); err != nil {
		return ExternalAuthorityState{}, err
	}
	if result.DexAuthority, err = readTag("dexAuthority"); err != nil {
		return ExternalAuthorityState{}, err
	}
	if result.ExternalAuthority, err = readTag("externalAuthority"); err != nil {
		return ExternalAuthorityState{}, err
	}
	for name, target := range map[string]*int64{
		"standaloneAttempts":   &result.StandaloneAttempts,
		"standaloneBroadcasts": &result.StandaloneBroadcasts,
		"batchAttempts":        &result.BatchAttempts,
	} {
		if *target, err = readInt(name); err != nil {
			return ExternalAuthorityState{}, err
		}
	}
	for name, target := range map[string]*bool{
		"firstRequestConsumed":  &result.FirstRequestConsumed,
		"secondRequestConsumed": &result.SecondRequestConsumed,
	} {
		if *target, err = readBool(name); err != nil {
			return ExternalAuthorityState{}, err
		}
	}
	return result, nil
}

func VaultRotationOperationsFromITF(state itftrace.State) (VaultRotationOperationsState, error) {
	readTag := func(name string) (string, error) {
		value, err := state.Tag(name)
		if err != nil {
			return "", fmt.Errorf("vault rotation operations %s: %w", name, err)
		}
		return value, nil
	}
	readInt := func(name string) (int64, error) {
		value, err := state.Int(name)
		if err != nil {
			return 0, fmt.Errorf("vault rotation operations %s: %w", name, err)
		}
		return value, nil
	}
	readBool := func(name string) (bool, error) {
		value, err := state.Bool(name)
		if err != nil {
			return false, fmt.Errorf("vault rotation operations %s: %w", name, err)
		}
		return value, nil
	}

	var result VaultRotationOperationsState
	var err error
	for name, target := range map[string]*string{
		"phase":     &result.Phase,
		"authority": &result.Authority,
		"withdraw":  &result.Withdraw,
		"outcome":   &result.Outcome,
	} {
		if *target, err = readTag(name); err != nil {
			return VaultRotationOperationsState{}, err
		}
	}
	for name, target := range map[string]*int64{
		"currentEpoch":      &result.CurrentEpoch,
		"vaultAvailable":    &result.VaultAvailable,
		"vaultReserved":     &result.VaultReserved,
		"authorizedOutflow": &result.AuthorizedOutflow,
	} {
		if *target, err = readInt(name); err != nil {
			return VaultRotationOperationsState{}, err
		}
	}
	if result.ArtifactPublished, err = readBool("artifactPublished"); err != nil {
		return VaultRotationOperationsState{}, err
	}
	return result, nil
}

func AuthorityHandoverFromITF(state itftrace.State) (AuthorityHandoverState, error) {
	chain, err := state.Tag("chain")
	if err != nil {
		return AuthorityHandoverState{}, fmt.Errorf("authority handover chain: %w", err)
	}
	phase, err := state.Tag("phase")
	if err != nil {
		return AuthorityHandoverState{}, fmt.Errorf("authority handover phase: %w", err)
	}
	authority, err := state.Tag("authority")
	if err != nil {
		return AuthorityHandoverState{}, fmt.Errorf("authority handover authority: %w", err)
	}
	currentEpoch, err := state.Int("currentEpoch")
	if err != nil {
		return AuthorityHandoverState{}, fmt.Errorf("authority handover currentEpoch: %w", err)
	}
	targetEpoch, err := state.Int("targetEpoch")
	if err != nil {
		return AuthorityHandoverState{}, fmt.Errorf("authority handover targetEpoch: %w", err)
	}
	oldWithdrawPending, err := state.Bool("oldWithdrawPending")
	if err != nil {
		return AuthorityHandoverState{}, fmt.Errorf("authority handover oldWithdrawPending: %w", err)
	}
	oldRechargePending, err := state.Bool("oldRechargePending")
	if err != nil {
		return AuthorityHandoverState{}, fmt.Errorf("authority handover oldRechargePending: %w", err)
	}
	intentBound, err := state.Bool("intentBound")
	if err != nil {
		return AuthorityHandoverState{}, fmt.Errorf("authority handover intentBound: %w", err)
	}
	signatureBoundToIntent, err := state.Bool("signatureBoundToIntent")
	if err != nil {
		return AuthorityHandoverState{}, fmt.Errorf("authority handover signatureBoundToIntent: %w", err)
	}
	oldTransitionRetired, err := state.Bool("oldTransitionRetired")
	if err != nil {
		return AuthorityHandoverState{}, fmt.Errorf("authority handover oldTransitionRetired: %w", err)
	}
	return AuthorityHandoverState{
		Chain:                  chain,
		Phase:                  phase,
		Authority:              authority,
		CurrentEpoch:           currentEpoch,
		TargetEpoch:            targetEpoch,
		OldWithdrawPending:     oldWithdrawPending,
		OldRechargePending:     oldRechargePending,
		IntentBound:            intentBound,
		SignatureBoundToIntent: signatureBoundToIntent,
		OldTransitionRetired:   oldTransitionRetired,
	}, nil
}

func SnowballFromITF(state itftrace.State) (SnowballState, error) {
	preference, err := state.Tag("preference")
	if err != nil {
		return SnowballState{}, fmt.Errorf("snowball preference: %w", err)
	}
	confidencePreference, err := state.Tag("confidencePreference")
	if err != nil {
		return SnowballState{}, fmt.Errorf("snowball confidencePreference: %w", err)
	}
	confidence, err := state.Int("confidence")
	if err != nil {
		return SnowballState{}, fmt.Errorf("snowball confidence: %w", err)
	}
	strengthA, err := state.Int("strengthA")
	if err != nil {
		return SnowballState{}, fmt.Errorf("snowball strengthA: %w", err)
	}
	strengthB, err := state.Int("strengthB")
	if err != nil {
		return SnowballState{}, fmt.Errorf("snowball strengthB: %w", err)
	}
	trackedA, err := state.Bool("trackedA")
	if err != nil {
		return SnowballState{}, fmt.Errorf("snowball trackedA: %w", err)
	}
	trackedB, err := state.Bool("trackedB")
	if err != nil {
		return SnowballState{}, fmt.Errorf("snowball trackedB: %w", err)
	}
	finalized, err := state.Bool("finalized")
	if err != nil {
		return SnowballState{}, fmt.Errorf("snowball finalized: %w", err)
	}
	finalCandidate, err := state.Tag("finalCandidate")
	if err != nil {
		return SnowballState{}, fmt.Errorf("snowball finalCandidate: %w", err)
	}
	return SnowballState{
		Preference:           preference,
		ConfidencePreference: confidencePreference,
		Confidence:           confidence,
		StrengthA:            strengthA,
		StrengthB:            strengthB,
		TrackedA:             trackedA,
		TrackedB:             trackedB,
		Finalized:            finalized,
		FinalCandidate:       finalCandidate,
	}, nil
}

func ConservationFromITF(state itftrace.State) (ConservationState, error) {
	readInt := func(name string) (int64, error) {
		value, err := state.Int(name)
		if err != nil {
			return 0, fmt.Errorf("conservation %s: %w", name, err)
		}
		return value, nil
	}
	var result ConservationState
	fields := []struct {
		name string
		dst  *int64
	}{
		{name: "alice", dst: &result.Alice},
		{name: "bob", dst: &result.Bob},
		{name: "runtimeFeePool", dst: &result.RuntimeFeePool},
		{name: "recycleFund", dst: &result.RecycleFund},
		{name: "positionMargin", dst: &result.PositionMargin},
		{name: "insuranceFund", dst: &result.InsuranceFund},
		{name: "challengeStake", dst: &result.ChallengeStake},
		{name: "totalSupply", dst: &result.TotalSupply},
		{name: "insuranceCredits", dst: &result.InsuranceCredits},
		{name: "insuranceDebits", dst: &result.InsuranceDebits},
		{name: "adlShortfall", dst: &result.ADLShortfall},
	}
	for _, field := range fields {
		value, err := readInt(field.name)
		if err != nil {
			return ConservationState{}, err
		}
		*field.dst = value
	}
	positionOpen, err := state.Bool("positionOpen")
	if err != nil {
		return ConservationState{}, fmt.Errorf("conservation positionOpen: %w", err)
	}
	result.PositionOpen = positionOpen
	return result, nil
}

func FrontierFromITF(state itftrace.State) (FrontierState, error) {
	responded, err := sortedITFSet(state, "responded")
	if err != nil {
		return FrontierState{}, err
	}
	supportA, err := sortedITFSet(state, "supportA")
	if err != nil {
		return FrontierState{}, err
	}
	supportB, err := sortedITFSet(state, "supportB")
	if err != nil {
		return FrontierState{}, err
	}
	mismatches, err := sortedITFSet(state, "anchorMismatches")
	if err != nil {
		return FrontierState{}, err
	}
	phase, err := state.Tag("phase")
	if err != nil {
		return FrontierState{}, fmt.Errorf("frontier phase: %w", err)
	}
	target, err := state.Tag("selectedTarget")
	if err != nil {
		return FrontierState{}, fmt.Errorf("frontier selectedTarget: %w", err)
	}
	return FrontierState{
		Responded:        responded,
		SupportA:         supportA,
		SupportB:         supportB,
		AnchorMismatches: mismatches,
		Phase:            phase,
		SelectedTarget:   target,
	}, nil
}

func DkgFromITF(state itftrace.State) (DkgState, error) {
	epoch, err := state.Int("epoch")
	if err != nil {
		return DkgState{}, fmt.Errorf("DKG epoch: %w", err)
	}
	attempt, err := state.Int("attempt")
	if err != nil {
		return DkgState{}, fmt.Errorf("DKG attempt: %w", err)
	}
	session, err := state.Tag("session")
	if err != nil {
		return DkgState{}, fmt.Errorf("DKG session: %w", err)
	}
	status, err := state.Tag("status")
	if err != nil {
		return DkgState{}, fmt.Errorf("DKG status: %w", err)
	}
	lifecycle, err := state.Tag("lifecycle")
	if err != nil {
		return DkgState{}, fmt.Errorf("DKG lifecycle: %w", err)
	}
	tempSession, err := state.Tag("tempSession")
	if err != nil {
		return DkgState{}, fmt.Errorf("DKG tempSession: %w", err)
	}
	eligible, err := sortedITFSet(state, "eligible")
	if err != nil {
		return DkgState{}, err
	}
	commits, err := sortedITFSet(state, "commits")
	if err != nil {
		return DkgState{}, err
	}
	shares, err := sortedITFSet(state, "shares")
	if err != nil {
		return DkgState{}, err
	}
	complaints, err := sortedITFSet(state, "unresolvedComplaints")
	if err != nil {
		return DkgState{}, err
	}
	acks, err := sortedITFSet(state, "validationAcks")
	if err != nil {
		return DkgState{}, err
	}
	tempStatePresent, err := state.Bool("tempStatePresent")
	if err != nil {
		return DkgState{}, fmt.Errorf("DKG tempStatePresent: %w", err)
	}
	keyReady, err := state.Bool("keyMaterialReady")
	if err != nil {
		return DkgState{}, fmt.Errorf("DKG keyMaterialReady: %w", err)
	}
	return DkgState{
		Epoch:                epoch,
		Attempt:              attempt,
		Session:              session,
		Status:               status,
		Lifecycle:            lifecycle,
		Eligible:             eligible,
		Commits:              commits,
		Shares:               shares,
		UnresolvedComplaints: complaints,
		ValidationAcks:       acks,
		TempSession:          tempSession,
		TempStatePresent:     tempStatePresent,
		KeyMaterialReady:     keyReady,
	}, nil
}

func SortedStrings(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortedITFSet(state itftrace.State, name string) ([]string, error) {
	values, err := state.SetTags(name)
	if err != nil {
		return nil, fmt.Errorf("ITF set %s: %w", name, err)
	}
	return SortedStrings(values), nil
}

func sortedITFIntSet(state itftrace.State, name string) ([]int64, error) {
	values, err := state.SetInts(name)
	if err != nil {
		return nil, fmt.Errorf("txpool admission %s: %w", name, err)
	}
	result := make([]int64, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}
