                 
                                                              
                                                                       
                                                                                      
                                                                        
package pb

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

                        

const (
	MaxStringLen    = 1024                                                                              
	MaxBytesLen     = 128 * 1024                                                                    
	MaxRepeatedLen  = 5000                                               
	MaxMessageDepth = 8                                     
	MaxTotalSize    = 2 << 20                                               
	                                                                                               
	MaxPushQuerySize = MaxTotalSize + (16 << 10)
)

                                             

type fieldLimit struct {
	maxBytes    int                                   
	maxRepeated int                                   
}

                                                                                
var overrides = map[string]fieldLimit{
	"pb.PushQuery.container": {maxBytes: MaxTotalSize},
	                                                                         
	                                                                
	"pb.PushQuery.signature":  {maxBytes: 128},
	"pb.PushQuery.vrf_seed":   {maxBytes: 64},
	"pb.PushQuery.nonce":      {maxBytes: 32},
	"pb.PullQuery.signature":  {maxBytes: 128},
	"pb.PullQuery.vrf_seed":   {maxBytes: 64},
	"pb.PullQuery.nonce":      {maxBytes: 32},
	"pb.Chits.chit_signature": {maxBytes: 128},
	"pb.Chits.query_nonce":    {maxBytes: 32},
	                                
	"pb.Block.body":                  {maxRepeated: 10000},
	"pb.BlockHeader.sampled_reports": {maxRepeated: 256},
	"pb.BlockHeader.offline_reports": {maxRepeated: 256},
	"pb.BlockHeader.online_reports":  {maxRepeated: 256},
	                                                 
	"pb.Block.short_txs": {maxBytes: 512 * 1024},
	                                                                                  
	                                    
	"pb.AcceptedFrontierRequest.nonce":           {maxBytes: 32},
	"pb.AcceptedFrontierRequest.signature":       {maxBytes: 128},
	"pb.AcceptedFrontierResponse.request_digest": {maxBytes: 32},
	"pb.AcceptedFrontierResponse.signature":      {maxBytes: 128},
	                                                             
	"pb.FrostWithdrawSignedTx.signed_package_bytes": {maxBytes: 512 * 1024},
	                                                   
	"pb.FrostWithdrawSignedTx.template_data": {maxBytes: 256 * 1024},
	                                                                   
	"pb.FrostWithdrawSignedTx.withdraw_ids":        {maxRepeated: 500},
	"pb.FrostWithdrawSignedTx.input_sigs":          {maxRepeated: 500},
	"pb.FrostWithdrawSignedTx.script_pubkeys":      {maxRepeated: 500},
	"pb.FrostWithdrawCancelTx.original_request_tx": {maxBytes: 16 * 1024},
	                                                                         
	                                                                    
	"pb.FrostVaultTransitionSignedTx.template_data":    {maxBytes: 256 * 1024},
	"pb.FrostVaultTransitionSignedTx.raw_tx":           {maxBytes: 512 * 1024},
	"pb.FrostVaultTransitionSignedTx.input_sigs":       {maxRepeated: 500},
	"pb.FrostVaultTransitionSignedTx.script_pubkeys":   {maxRepeated: 500},
	"pb.FrostVaultMigrationState.template_data":        {maxBytes: 256 * 1024},
	"pb.FrostVaultMigrationArchive.input_sigs":         {maxRepeated: 500},
	"pb.FrostVaultMigrationArchive.script_pubkeys":     {maxRepeated: 500},
	"pb.FrostVaultMigrationArchive.raw_tx":             {maxBytes: 512 * 1024},
	"pb.FrostVaultMigrationArchive.signature":          {maxBytes: 128},
	"pb.FrostVaultMigrationArchive.dependency_raw_txs": {maxBytes: 512 * 1024, maxRepeated: 500},
	                        
	"pb.FrostVaultDkgCommitTx.commitment_points":   {maxRepeated: 200},
	"pb.FrostVaultDkgCommitment.commitment_points": {maxRepeated: 200},
	                                                                          
	                                                                   
	"pb.FrostActiveMiners.indices":              {maxRepeated: 175},
	"pb.FrostActiveMiners.addresses":            {maxRepeated: 175},
	"pb.FrostActiveMiners.public_keys":          {maxRepeated: 175},
	"pb.FrostActiveMiners.matured_stakes":       {maxRepeated: 175},
	"pb.FrostActiveMiners.active_since_heights": {maxRepeated: 175},
	                        
	"pb.FrostEnvelope.payload": {maxBytes: 256 * 1024},
	                              
	"pb.WitnessChallengeTx.evidence": {maxBytes: 4096},
	                  
	"pb.RechargeRequest.selected_witnesses": {maxRepeated: 200},
	"pb.RechargeRequest.votes":              {maxRepeated: 200},
	                         
	"pb.BatchGetShortTxRequest.short_hashes": {maxRepeated: 5000},
	                          
	"pb.BatchGetShortTxResponse.transactions": {maxRepeated: 5000},
}

var ErrDataBloat = errors.New("data bloat rejected")

                                                                   
                                                                                    
func Sanitize(msg proto.Message) error {
	if limit := messageSizeLimit(msg); proto.Size(msg) > limit {
		return fmt.Errorf("%w: total size %d > %d", ErrDataBloat, proto.Size(msg), limit)
	}
	return sanitizeReflect(msg.ProtoReflect(), 0)
}

func sanitizeReflect(m protoreflect.Message, depth int) error {
	if depth > MaxMessageDepth {
		return fmt.Errorf("%w: nesting depth > %d", ErrDataBloat, MaxMessageDepth)
	}
	msgName := string(m.Descriptor().FullName())
	var firstErr error
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		key := msgName + "." + string(fd.Name())
		lim := getLimit(key, fd)

		switch {
		case fd.IsList():
			list := v.List()
			if list.Len() > lim.maxRepeated {
				firstErr = fmt.Errorf("%w: %s repeated len %d > %d", ErrDataBloat, key, list.Len(), lim.maxRepeated)
				return false
			}
			                                           
			if fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind {
				for i := 0; i < list.Len(); i++ {
					if err := sanitizeReflect(list.Get(i).Message(), depth+1); err != nil {
						firstErr = err
						return false
					}
				}
			} else if fd.Kind() == protoreflect.BytesKind {
				for i := 0; i < list.Len(); i++ {
					if len(list.Get(i).Bytes()) > lim.maxBytes {
						firstErr = fmt.Errorf("%w: %s[%d] bytes len %d > %d", ErrDataBloat, key, i, len(list.Get(i).Bytes()), lim.maxBytes)
						return false
					}
				}
			} else if fd.Kind() == protoreflect.StringKind {
				for i := 0; i < list.Len(); i++ {
					if len(list.Get(i).String()) > lim.maxBytes {
						firstErr = fmt.Errorf("%w: %s[%d] string len %d > %d", ErrDataBloat, key, i, len(list.Get(i).String()), lim.maxBytes)
						return false
					}
				}
			}

		case fd.IsMap():
			mp := v.Map()
			if mp.Len() > lim.maxRepeated {
				firstErr = fmt.Errorf("%w: %s map len %d > %d", ErrDataBloat, key, mp.Len(), lim.maxRepeated)
				return false
			}
			if fd.MapValue().Kind() == protoreflect.MessageKind {
				mp.Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
					if err := sanitizeReflect(mv.Message(), depth+1); err != nil {
						firstErr = err
						return false
					}
					return true
				})
				if firstErr != nil {
					return false
				}
			}

		case fd.Kind() == protoreflect.StringKind:
			if len(v.String()) > lim.maxBytes {
				firstErr = fmt.Errorf("%w: %s string len %d > %d", ErrDataBloat, key, len(v.String()), lim.maxBytes)
				return false
			}

		case fd.Kind() == protoreflect.BytesKind:
			if len(v.Bytes()) > lim.maxBytes {
				firstErr = fmt.Errorf("%w: %s bytes len %d > %d", ErrDataBloat, key, len(v.Bytes()), lim.maxBytes)
				return false
			}

		case fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind:
			if err := sanitizeReflect(v.Message(), depth+1); err != nil {
				firstErr = err
				return false
			}
		}
		return true
	})
	return firstErr
}

                                                                  
                                                                               
func UnmarshalSanitize(b []byte, msg proto.Message) error {
	if limit := messageSizeLimit(msg); len(b) > limit {
		return fmt.Errorf("%w: raw size %d > %d", ErrDataBloat, len(b), limit)
	}
	if err := proto.Unmarshal(b, msg); err != nil {
		return err
	}
	return Sanitize(msg)
}

func messageSizeLimit(msg proto.Message) int {
	if _, ok := msg.(*PushQuery); ok {
		return MaxPushQuerySize
	}
	return MaxTotalSize
}

func getLimit(key string, fd protoreflect.FieldDescriptor) fieldLimit {
	if ov, ok := overrides[key]; ok {
		lim := fieldLimit{maxBytes: MaxStringLen, maxRepeated: MaxRepeatedLen}
		if fd.Kind() == protoreflect.BytesKind {
			lim.maxBytes = MaxBytesLen
		}
		if ov.maxBytes > 0 {
			lim.maxBytes = ov.maxBytes
		}
		if ov.maxRepeated > 0 {
			lim.maxRepeated = ov.maxRepeated
		}
		return lim
	}
	               
	switch {
	case fd.Kind() == protoreflect.BytesKind:
		return fieldLimit{maxBytes: MaxBytesLen, maxRepeated: MaxRepeatedLen}
	default:
		return fieldLimit{maxBytes: MaxStringLen, maxRepeated: MaxRepeatedLen}
	}
}
