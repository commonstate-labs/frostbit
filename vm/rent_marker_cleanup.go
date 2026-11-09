package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/rent"
)

func syncSpotOpenOrderMarker(sv StateView, st *pb.OrderState, oldStates ...*pb.OrderState) {
	for _, old := range oldStates {
		if spotOrderIsOpen(old) && (st == nil || old.Owner != st.Owner || old.OrderId != st.OrderId || !spotOrderIsOpen(st)) {
			sv.Del(keys.KeyAccountOrderItem(old.Owner, old.OrderId))
		}
	}
	if !spotOrderIsOpen(st) {
		if st != nil && st.Owner != "" && st.OrderId != "" {
			sv.Del(keys.KeyAccountOrderItem(st.Owner, st.OrderId))
		}
		return
	}
	sv.Set(keys.KeyAccountOrderItem(st.Owner, st.OrderId), []byte{1})
	rent.MarkOwnerActivity(sv, st.Owner, rent.OwnerMarkerSpotOrder)
}
