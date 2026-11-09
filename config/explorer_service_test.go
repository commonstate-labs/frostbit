package config

import (
	"strings"
	"testing"
)

func TestExplorerServiceTokenTrimsEnvironmentValue(t *testing.T) {
	t.Setenv(ExplorerServiceTokenEnv, "  service-secret  \n")
	if got := ExplorerServiceToken(); got != "service-secret" {
		t.Fatalf("ExplorerServiceToken()=%q, want trimmed token", got)
	}
}

func TestValidateExplorerServiceToken(t *testing.T) {
	valid := strings.Repeat("x", ExplorerServiceTokenMinLen)
	if err := ValidateExplorerServiceToken(valid); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	for _, token := range []string{"short", valid + "\n", strings.Repeat("x", ExplorerServiceTokenMaxLen+1)} {
		if err := ValidateExplorerServiceToken(token); err == nil {
			t.Fatalf("invalid token of length %d was accepted", len(token))
		}
	}
}
