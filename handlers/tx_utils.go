package handlers

import (
	"dex/db"
	"dex/logs"
	"dex/pb"
	"fmt"
	"net/http"
	"strings"
)

                                                      
func ExtractTxId(a *pb.AnyTx) (string, error) {
	if b := a.GetBase(); b != nil {
		return b.TxId, nil
	}
	return "", fmt.Errorf("unrecognized tx type or nil base")
}

var AUTH_ENABLED = false                                                                                       

func CheckAuth(r *http.Request) bool {
	if !AUTH_ENABLED {
		return true
	}
	clientIP := strings.Split(r.RemoteAddr, ":")[0]
	dbPath := ""
	dbMgr, err := db.NewManager(dbPath, logs.NewNodeLogger("test", 0))
	if err != nil {
		logs.Error("CheckAuth GetInstance err : %v", err)
		return false
	}
	                                                                        
	                                                                                             
	                                                                           
	                      
	info, err := dbMgr.GetClientInfo(clientIP)
	if err != nil {
		                                          
		return false
	}
	return info.GetAuthed()
}
