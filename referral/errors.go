package referral

import "errors"

var (
	ErrSelfReferral   = errors.New("cannot refer yourself")
	ErrAlreadyBound   = errors.New("referrer already bound")
	ErrCyclicReferral = errors.New("cyclic referral chain")
)
