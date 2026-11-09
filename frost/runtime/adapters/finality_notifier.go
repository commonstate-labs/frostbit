                                              
                                                        
package adapters

import (
	"dex/frost/runtime"
	"dex/interfaces"
	"dex/types"
)

                                                   
type EventBus interface {
	Subscribe(eventType types.EventType, handler interfaces.EventHandler)
}

                                                                       
type EventBusFinalityNotifier struct {
	events EventBus
}

                                                                    
func NewEventBusFinalityNotifier(events EventBus) *EventBusFinalityNotifier {
	return &EventBusFinalityNotifier{
		events: events,
	}
}

                                                      
func (n *EventBusFinalityNotifier) SubscribeBlockFinalized(fn func(height uint64)) {
	if n.events == nil {
		return
	}

	                                                          
	handler := func(event interfaces.Event) {
		if event.Type() != types.EventBlockFinalized {
			return
		}

		                                                                     
		var height uint64
		switch data := event.Data().(type) {
		case *types.BlockFinalizedData:
			if data != nil && data.Block != nil {
				height = data.Block.Header.Height
			}
		case *types.Block:
			if data != nil {
				height = data.Header.Height
			}
		}
		if height > 0 {
			fn(height)
		}
	}

	n.events.Subscribe(types.EventBlockFinalized, handler)
}

                                                                      
var _ runtime.FinalityNotifier = (*EventBusFinalityNotifier)(nil)
