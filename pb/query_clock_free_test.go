package pb

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func assertQueryDeadlineReserved(t *testing.T, message protoreflect.Message) {
	t.Helper()
	descriptor := message.Descriptor()
	if field := descriptor.Fields().ByNumber(3); field != nil {
		t.Fatalf("%s field #3 is active as %q", descriptor.FullName(), field.Name())
	}
	if field := descriptor.Fields().ByName("deadline"); field != nil {
		t.Fatalf("%s still exposes wall-clock field %q", descriptor.FullName(), field.Name())
	}
	numberReserved := false
	ranges := descriptor.ReservedRanges()
	for i := 0; i < ranges.Len(); i++ {
		rangeAt := ranges.Get(i)
		if rangeAt[0] <= 3 && 3 < rangeAt[1] {
			numberReserved = true
			break
		}
	}
	if !numberReserved {
		t.Fatalf("%s must reserve removed deadline field #3", descriptor.FullName())
	}
	nameReserved := false
	names := descriptor.ReservedNames()
	for i := 0; i < names.Len(); i++ {
		if names.Get(i) == "deadline" {
			nameReserved = true
			break
		}
	}
	if !nameReserved {
		t.Fatalf("%s must reserve removed field name deadline", descriptor.FullName())
	}
}

func TestSnowmanQueriesHaveNoWallClockDeadline(t *testing.T) {
	assertQueryDeadlineReserved(t, (&PushQuery{}).ProtoReflect())
	assertQueryDeadlineReserved(t, (&PullQuery{}).ProtoReflect())
}

func TestSnowmanQuerySanitizeBoundsAuthenticationFields(t *testing.T) {
	tests := []struct {
		name string
		msg  proto.Message
	}{
		{name: "PushQuery signature", msg: &PushQuery{Signature: make([]byte, 129)}},
		{name: "PushQuery VRF seed", msg: &PushQuery{VrfSeed: make([]byte, 65)}},
		{name: "PushQuery nonce", msg: &PushQuery{Nonce: make([]byte, 33)}},
		{name: "PullQuery signature", msg: &PullQuery{Signature: make([]byte, 129)}},
		{name: "PullQuery VRF seed", msg: &PullQuery{VrfSeed: make([]byte, 65)}},
		{name: "PullQuery nonce", msg: &PullQuery{Nonce: make([]byte, 33)}},
		{name: "Chits signature", msg: &Chits{ChitSignature: make([]byte, 129)}},
		{name: "Chits query nonce", msg: &Chits{QueryNonce: make([]byte, 33)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := proto.Marshal(tt.msg)
			if err != nil {
				t.Fatal(err)
			}
			clone := tt.msg.ProtoReflect().Type().New().Interface()
			if err := UnmarshalSanitize(encoded, clone); err == nil {
				t.Fatal("oversized Snowman query authentication field unexpectedly accepted")
			}
		})
	}
}
