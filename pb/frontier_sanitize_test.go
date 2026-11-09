package pb

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestAcceptedFrontierRequestReservesRemovedDeadlineField(t *testing.T) {
	desc := (&AcceptedFrontierRequest{}).ProtoReflect().Descriptor()
	const deadlineField = protoreflect.FieldNumber(5)
	if field := desc.Fields().ByNumber(deadlineField); field != nil {
		t.Fatalf("removed deadline field 5 is still active as %q", field.Name())
	}
	if !desc.ReservedRanges().Has(deadlineField) {
		t.Fatal("removed deadline field 5 is not reserved")
	}

	deadlineName := protoreflect.Name("deadline")
	reservedName := false
	for i := 0; i < desc.ReservedNames().Len(); i++ {
		if desc.ReservedNames().Get(i) == deadlineName {
			reservedName = true
			break
		}
	}
	if !reservedName {
		t.Fatal("removed deadline field name is not reserved")
	}
}

func TestAcceptedFrontierSanitizeBoundsControlBytes(t *testing.T) {
	tests := []struct {
		name string
		msg  proto.Message
	}{
		{
			name: "request nonce",
			msg:  &AcceptedFrontierRequest{Nonce: make([]byte, 33)},
		},
		{
			name: "request signature",
			msg:  &AcceptedFrontierRequest{Signature: make([]byte, 129)},
		},
		{
			name: "response request digest",
			msg:  &AcceptedFrontierResponse{RequestDigest: make([]byte, 33)},
		},
		{
			name: "response signature",
			msg:  &AcceptedFrontierResponse{Signature: make([]byte, 129)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := proto.Marshal(tt.msg)
			if err != nil {
				t.Fatal(err)
			}
			clone := tt.msg.ProtoReflect().Type().New().Interface()
			if err := UnmarshalSanitize(encoded, clone); err == nil {
				t.Fatal("oversized accepted-frontier field unexpectedly accepted")
			}
		})
	}
}
