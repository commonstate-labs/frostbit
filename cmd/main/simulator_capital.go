package main

import (
	"dex/pb"
	"math/big"
	"strings"
)

                                                                           
                                                                    
                                                            
type simCapitalHold struct {
	node    *NodeInstance
	amounts map[string]*big.Int
}

type simCapitalReservation struct {
	token  string
	amount *big.Int
}

func simCapitalAdd(total map[string]map[string]*big.Int, address, token string, amount *big.Int) {
	if amount == nil || amount.Sign() <= 0 || address == "" || token == "" {
		return
	}
	byToken := total[address]
	if byToken == nil {
		byToken = make(map[string]*big.Int)
		total[address] = byToken
	}
	if byToken[token] == nil {
		byToken[token] = new(big.Int)
	}
	byToken[token].Add(byToken[token], amount)
}

func simCapitalSub(total map[string]map[string]*big.Int, address, token string, amount *big.Int) {
	if amount == nil || amount.Sign() <= 0 || address == "" || token == "" {
		return
	}
	byToken := total[address]
	if byToken == nil || byToken[token] == nil {
		return
	}
	byToken[token].Sub(byToken[token], amount)
	if byToken[token].Sign() <= 0 {
		delete(byToken, token)
	}
	if len(byToken) == 0 {
		delete(total, address)
	}
}

func simCapitalGet(total map[string]map[string]*big.Int, address, token string) *big.Int {
	if byToken := total[address]; byToken != nil && byToken[token] != nil {
		return new(big.Int).Set(byToken[token])
	}
	return new(big.Int)
}

func (s *TxSimulator) ensureSimCapitalMapsLocked() {
	if s.capitalHolds == nil {
		s.capitalHolds = make(map[string]simCapitalHold)
	}
	if s.capitalHeldTotals == nil {
		s.capitalHeldTotals = make(map[string]map[string]*big.Int)
	}
	if s.capitalPending == nil {
		s.capitalPending = make(map[string]map[uint64]simCapitalReservation)
	}
	if s.capitalPendingTotal == nil {
		s.capitalPendingTotal = make(map[string]map[string]*big.Int)
	}
	if s.capitalFinalized == nil {
		s.capitalFinalized = make(map[string]uint64)
	}
}

func (s *TxSimulator) reconcileSimCapitalLocked(node *NodeInstance) {
	if node == nil || strings.TrimSpace(node.Address) == "" {
		return
	}
	s.ensureSimCapitalMapsLocked()
	finalizedNonce, ok := finalizedSimAccountNonce(node)
	if !ok {
		return
	}
	address := node.Address
	if previous, seen := s.capitalFinalized[address]; seen && finalizedNonce <= previous {
		return
	}
	s.capitalFinalized[address] = finalizedNonce
	pending := s.capitalPending[address]
	for nonce, reservation := range pending {
		if nonce >= finalizedNonce {
			continue
		}
		simCapitalSub(s.capitalPendingTotal, address, reservation.token, reservation.amount)
		delete(pending, nonce)
	}
	if len(pending) == 0 {
		delete(s.capitalPending, address)
	}
}

func (s *TxSimulator) availableSimCapitalLocked(node *NodeInstance, token string) (*big.Int, bool) {
	if node == nil || strings.TrimSpace(token) == "" {
		return nil, false
	}
	if s.hasRestoredSimPending(node) {
		return nil, false
	}
	s.reconcileSimCapitalLocked(node)
	available, ok := finalizedSimAvailableBalance(node, token)
	if !ok {
		return nil, false
	}
	reserved := simCapitalGet(s.capitalHeldTotals, node.Address, token)
	reserved.Add(reserved, simCapitalGet(s.capitalPendingTotal, node.Address, token))
	available.Sub(available, reserved)
	if available.Sign() < 0 {
		available.SetInt64(0)
	}
	return available, true
}

func (s *TxSimulator) availableSimCapital(node *NodeInstance, token string) (*big.Int, bool) {
	s.capitalMu.Lock()
	defer s.capitalMu.Unlock()
	s.ensureSimCapitalMapsLocked()
	return s.availableSimCapitalLocked(node, token)
}

func (s *TxSimulator) holdSimCapital(holdID string, node *NodeInstance, requirements map[string]*big.Int) bool {
	holdID = strings.TrimSpace(holdID)
	if holdID == "" || node == nil || len(requirements) == 0 {
		return false
	}
	s.capitalMu.Lock()
	defer s.capitalMu.Unlock()
	s.ensureSimCapitalMapsLocked()
	if _, exists := s.capitalHolds[holdID]; exists {
		return false
	}

	clean := make(map[string]*big.Int, len(requirements))
	for token, amount := range requirements {
		token = strings.TrimSpace(token)
		if token == "" || amount == nil || amount.Sign() <= 0 {
			return false
		}
		if clean[token] == nil {
			clean[token] = new(big.Int)
		}
		clean[token].Add(clean[token], amount)
	}
	for token, amount := range clean {
		available, ok := s.availableSimCapitalLocked(node, token)
		if !ok || available.Cmp(amount) < 0 {
			return false
		}
	}
	for token, amount := range clean {
		simCapitalAdd(s.capitalHeldTotals, node.Address, token, amount)
	}
	s.capitalHolds[holdID] = simCapitalHold{node: node, amounts: clean}
	return true
}

func (s *TxSimulator) releaseSimCapitalHold(holdID string) {
	s.capitalMu.Lock()
	defer s.capitalMu.Unlock()
	s.ensureSimCapitalMapsLocked()
	hold, exists := s.capitalHolds[holdID]
	if !exists {
		return
	}
	for token, amount := range hold.amounts {
		simCapitalSub(s.capitalHeldTotals, hold.node.Address, token, amount)
	}
	delete(s.capitalHolds, holdID)
}

func (s *TxSimulator) commitSimCapitalHold(holdID, token string, nonce uint64) bool {
	s.capitalMu.Lock()
	defer s.capitalMu.Unlock()
	s.ensureSimCapitalMapsLocked()
	hold, exists := s.capitalHolds[holdID]
	if !exists || hold.node == nil {
		return false
	}
	amount := hold.amounts[token]
	if amount == nil || amount.Sign() <= 0 {
		return false
	}
	address := hold.node.Address
	                                                                           
	                                                                       
	                                                                           
	s.reconcileSimCapitalLocked(hold.node)
	if finalized, known := s.capitalFinalized[address]; known && nonce < finalized {
		simCapitalSub(s.capitalHeldTotals, address, token, amount)
		delete(hold.amounts, token)
		if len(hold.amounts) == 0 {
			delete(s.capitalHolds, holdID)
		} else {
			s.capitalHolds[holdID] = hold
		}
		return true
	}
	pending := s.capitalPending[address]
	if pending == nil {
		pending = make(map[uint64]simCapitalReservation)
		s.capitalPending[address] = pending
	}
	if _, duplicate := pending[nonce]; duplicate {
		return false
	}
	cloned := new(big.Int).Set(amount)
	pending[nonce] = simCapitalReservation{token: token, amount: cloned}
	simCapitalSub(s.capitalHeldTotals, address, token, amount)
	simCapitalAdd(s.capitalPendingTotal, address, token, amount)
	delete(hold.amounts, token)
	if len(hold.amounts) == 0 {
		delete(s.capitalHolds, holdID)
	} else {
		s.capitalHolds[holdID] = hold
	}
	return true
}

func (s *TxSimulator) submitGeneratedTxWithCapital(node *NodeInstance, token string, amount *big.Int, build func(uint64) *pb.AnyTx) (uint64, *pb.AnyTx, bool) {
	if node == nil || amount == nil || amount.Sign() <= 0 || strings.TrimSpace(token) == "" {
		return 0, nil, false
	}
	s.capitalMu.Lock()
	defer s.capitalMu.Unlock()
	s.ensureSimCapitalMapsLocked()
	available, ok := s.availableSimCapitalLocked(node, token)
	if !ok || available.Cmp(amount) < 0 {
		return 0, nil, false
	}
	nonce, tx, submitted := s.submitGeneratedTx(node, build)
	if !submitted {
		return nonce, tx, false
	}
	pending := s.capitalPending[node.Address]
	if pending == nil {
		pending = make(map[uint64]simCapitalReservation)
		s.capitalPending[node.Address] = pending
	}
	cloned := new(big.Int).Set(amount)
	pending[nonce] = simCapitalReservation{token: token, amount: cloned}
	simCapitalAdd(s.capitalPendingTotal, node.Address, token, amount)
	return nonce, tx, true
}
