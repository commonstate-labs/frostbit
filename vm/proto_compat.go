package vm

import (
	"bytes"
	"fmt"

	"google.golang.org/protobuf/proto"
)

                                                                             
                                                                    
func unmarshalProtoCompat(data []byte, msg proto.Message) error {
	if err := proto.Unmarshal(data, msg); err == nil {
		return nil
	} else {
		trimmed := bytes.TrimRight(data, "\x00")
		if len(trimmed) == 0 || len(trimmed) == len(data) {
			return err
		}
		proto.Reset(msg)
		if err2 := proto.Unmarshal(trimmed, msg); err2 == nil {
			return nil
		} else {
			return fmt.Errorf("proto unmarshal failed (raw=%v, trimmed=%v)", err, err2)
		}
	}
}

                                                                                                                  
                                                                            
func MarshalProto(msg proto.Message) ([]byte, error) {
	opts := proto.MarshalOptions{Deterministic: true}
	return opts.Marshal(msg)
}
