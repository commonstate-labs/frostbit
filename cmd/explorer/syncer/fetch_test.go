package syncer

import (
	"dex/config"
	"net/http"
	"testing"
)

func TestSetExplorerServiceHeadersIncludesConfiguredToken(t *testing.T) {
	t.Setenv(config.ExplorerServiceTokenEnv, "syncer-service-secret")
	header := make(http.Header)
	setExplorerServiceHeaders(header)
	if got := header.Get(config.ExplorerServiceHeader); got != "explorer" {
		t.Fatalf("service header=%q, want explorer", got)
	}
	if got := header.Get(config.ExplorerServiceTokenHeader); got != "syncer-service-secret" {
		t.Fatalf("service token=%q, want configured token", got)
	}
}
