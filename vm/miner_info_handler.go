package vm

import (
	"dex/keys"
	"dex/pb"
	"fmt"
	"net"
	"strconv"
	"strings"
)

                                                                                                     
                                                                                               
                                 
type MinerInfoTxHandler struct{}

const maxEndpointLen = 253 + 6                               

func (h *MinerInfoTxHandler) Kind() string            { return "miner_info" }
func (h *MinerInfoTxHandler) Apply(_ *pb.AnyTx) error { return ErrNotImplemented }

func (h *MinerInfoTxHandler) DryRun(tx *pb.AnyTx, sv StateView) ([]WriteOp, *Receipt, error) {
	mtx := tx.GetMinerInfoTx()
	if mtx == nil || mtx.Base == nil {
		return nil, nil, fmt.Errorf("not a MinerInfoTx")
	}
	base := mtx.Base
	fail := func(msg string) ([]WriteOp, *Receipt, error) {
		return nil, &Receipt{TxID: base.TxId, Status: "FAILED", Error: msg}, fmt.Errorf("%s", msg)
	}

	accountKey := keys.KeyAccount(base.FromAddress)
	data, exists, err := sv.Get(accountKey)
	if err != nil || !exists {
		return fail("miner account not found")
	}
	var account pb.Account
	if err := unmarshalProtoCompat(data, &account); err != nil {
		return fail("failed to parse miner account")
	}
	endpoint := strings.TrimSpace(mtx.Endpoint)
	if endpoint == "" {
		if account.IsMiner {
			return fail("active or probation miner endpoint cannot be cleared; exit mining first")
		}
	} else if err := validateMinerEndpoint(endpoint); err != nil {
		return fail(err.Error())
	}
	if account.Ip == endpoint {
		                                                                     
		return nil, &Receipt{TxID: base.TxId, Status: "SUCCEED", BlockHeight: base.ExecutedHeight}, nil
	}
	account.Ip = endpoint
	out, err := MarshalProto(&account)
	if err != nil {
		return fail("failed to marshal account")
	}
	ws := []WriteOp{{Key: accountKey, Value: out, Category: "account"}}
	return ws, &Receipt{
		TxID: base.TxId, Status: "SUCCEED", BlockHeight: base.ExecutedHeight,
		WriteCount: len(ws),
		Logs:       []string{fmt.Sprintf("miner_info: %s → %q", base.FromAddress, endpoint)},
	}, nil
}

                                                                                  
                                                                                 
func validateMinerEndpoint(ep string) error {
	if len(ep) > maxEndpointLen {
		return fmt.Errorf("endpoint too long (%d > %d)", len(ep), maxEndpointLen)
	}
	host, portStr, err := net.SplitHostPort(ep)
	if err != nil {
		return fmt.Errorf("endpoint must be host:port: %v", err)
	}
	if host == "" {
		return fmt.Errorf("endpoint host is empty")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("endpoint port invalid")
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsUnspecified() {
			return fmt.Errorf("endpoint IP not routable")
		}
		return nil
	}
	if !isValidDomain(host) {
		return fmt.Errorf("endpoint host is not a valid IP or domain")
	}
	return nil
}

                                                                                                                  
                                                               
func isValidDomain(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	if strings.EqualFold(s, "localhost") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') || c == '-'
			if !ok {
				return false
			}
		}
	}
	return true
}
