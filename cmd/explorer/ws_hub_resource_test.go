package main

import (
	"net/http"
	"testing"
)

func TestWSClientIdentityUsesAdmissionGuardResolver(t *testing.T) {
	hub := &wsHub{clientIP: func(*http.Request) string { return "203.0.113.8" }}
	req := &http.Request{RemoteAddr: "10.0.0.2:443"}
	if got := hub.requestClientIP(req); got != "203.0.113.8" {
		t.Fatalf("requestClientIP=%q, want trusted resolver identity", got)
	}
}

func TestWSOutboundBudgetIsReleasedForClosedSlowClient(t *testing.T) {
	hub := &wsHub{outboundSlots: make(chan struct{}, 1)}
	client := &wsClient{send: make(chan wsOutboundMessage, 1)}

	release, ok := hub.reserveOutbound(wsOutboundBudgetUnitBytes)
	if !ok {
		t.Fatal("reserve initial outbound slot")
	}
	if !client.enqueueOutbound(wsOutboundMessage{data: []byte("payload"), release: release}) {
		t.Fatal("queue initial outbound message")
	}
	if _, ok := hub.reserveOutbound(1); ok {
		t.Fatal("outbound budget accepted data while queued message held its only slot")
	}

	client.closeOutbound()
	release, ok = hub.reserveOutbound(1)
	if !ok {
		t.Fatal("closed client did not release its queued outbound budget")
	}
	release()
}

func TestWSPayloadWeightBoundsBroadcastCacheEntries(t *testing.T) {
	if _, ok := wsPayloadWeight(topicPayload{data: make([]byte, wsMaxOutboundMessageBytes+1)}); ok {
		t.Fatal("oversized data unexpectedly admitted to broadcast cache")
	}
	if _, ok := wsPayloadWeight(topicPayload{data: []byte("{}"), stable: make([]byte, wsMaxOutboundMessageBytes+1)}); ok {
		t.Fatal("oversized stable payload unexpectedly admitted to broadcast cache")
	}
	weight, ok := wsPayloadWeight(topicPayload{data: []byte("{}"), stable: []byte("stable")})
	if !ok || weight != int64(len("{}")+len("stable")) {
		t.Fatalf("payload weight = %d, ok=%v", weight, ok)
	}
}

func TestWSTopicTriggerCoalescesCatchUpRefreshes(t *testing.T) {
	hub := &wsHub{
		trigger:      make(chan struct{}, 1),
		topicTrigger: make(chan string, 1),
	}
	hub.TriggerTopic("my_closed_positions")
	hub.TriggerTopic("my_closed_positions")

	select {
	case topic := <-hub.topicTrigger:
		if topic != "my_closed_positions" {
			t.Fatalf("topic trigger = %q", topic)
		}
	default:
		t.Fatal("missing topic refresh trigger")
	}
	select {
	case topic := <-hub.topicTrigger:
		t.Fatalf("duplicate catch-up trigger was not coalesced: %q", topic)
	default:
	}
}
