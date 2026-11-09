package rent

import (
	"dex/keys"
	"strings"
)

const (
	OwnerMarkerInit         = "init"
	OwnerMarkerSpotOrder    = "spot_order"
	OwnerMarkerFutureOrder  = "future_order"
	OwnerMarkerClaim        = "claim"
	OwnerMarkerNonFBBalance = "nonfb_balance"
)

var ownerMarkerKinds = []string{
	OwnerMarkerInit,
	OwnerMarkerSpotOrder,
	OwnerMarkerFutureOrder,
	OwnerMarkerClaim,
	OwnerMarkerNonFBBalance,
}

func MarkOwnerInitialized(w Writer, owner string) {
	markOwner(w, owner, OwnerMarkerInit)
}

func MarkOwnerActivity(w Writer, owner, kind string) {
	markOwner(w, owner, kind)
}

func markOwner(w Writer, owner, kind string) {
	owner = strings.TrimSpace(owner)
	kind = strings.TrimSpace(kind)
	if w == nil || !IsRentManagedAccount(owner) || kind == "" {
		return
	}
	w.Set(keys.KeyRentOwnerMarker(owner, kind), []byte{1})
}

func ClearOwnerMarkers(w Writer, owner string) {
	if w == nil || strings.TrimSpace(owner) == "" {
		return
	}
	for _, kind := range ownerMarkerKinds {
		w.Del(keys.KeyRentOwnerMarker(owner, kind))
	}
}
