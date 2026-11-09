package arbitrage

import (
	"bytes"
	"fmt"

	"google.golang.org/protobuf/proto"
)

func unmarshalProtoCompat(data []byte, message proto.Message) error {
	if err := proto.Unmarshal(data, message); err == nil {
		return nil
	} else {
		trimmed := bytes.TrimRight(data, "\x00")
		if len(trimmed) == 0 || len(trimmed) == len(data) {
			return err
		}
		proto.Reset(message)
		if retryErr := proto.Unmarshal(trimmed, message); retryErr != nil {
			return fmt.Errorf("proto unmarshal failed (raw=%v, trimmed=%v)", err, retryErr)
		}
		return nil
	}
}
