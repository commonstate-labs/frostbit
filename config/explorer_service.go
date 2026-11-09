package config

import (
	"fmt"
	"os"
	"strings"
)

                                                                            
                                                                                 
                                                                                
                                                                             
                                                                                 
const ExplorerServiceTokenEnv = "DEX_EXPLORER_SERVICE_TOKEN"

const (
	ExplorerServiceHeader      = "X-Explorer-Service"
	ExplorerServiceTokenHeader = "X-Explorer-Service-Token"
	ExplorerServiceTokenMinLen = 32
	ExplorerServiceTokenMaxLen = 512
)

                                                                              
                                                                             
                                                    
func ExplorerServiceToken() string {
	return strings.TrimSpace(os.Getenv(ExplorerServiceTokenEnv))
}

                                                                           
                                                                              
                        
func ValidateExplorerServiceToken(token string) error {
	if len(token) < ExplorerServiceTokenMinLen || len(token) > ExplorerServiceTokenMaxLen {
		return fmt.Errorf("%s must be %d..%d characters", ExplorerServiceTokenEnv, ExplorerServiceTokenMinLen, ExplorerServiceTokenMaxLen)
	}
	if strings.TrimSpace(token) != token || strings.ContainsAny(token, "\r\n\x00") {
		return fmt.Errorf("%s must be a trimmed single-line value", ExplorerServiceTokenEnv)
	}
	return nil
}
