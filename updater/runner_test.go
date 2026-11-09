package updater

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestExecRunnerHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "updater-dump-environment" {
		return
	}
	json.NewEncoder(os.Stdout).Encode(os.Environ())
	os.Exit(0)
}

func TestExecRunnerSeparatesGitHubTokenFromNodeHelpers(t *testing.T) {
	path, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("GH_TOKEN", "readonly-token")
	t.Setenv("GITHUB_TOKEN", "another-readonly-token")
	t.Setenv("GH_HOST", "evil.invalid")
	t.Setenv("GH_REPO", "evil/repo")
	t.Setenv("LD_PRELOAD", "secret.so")
	t.Setenv("DEX_PRIVATE_KEY", "do-not-forward")
	for _, gh := range []bool{false, true} {
		r := ExecRunner{GHPath: path + "-not-this", GHConfigDir: "/controlled/config"}
		if gh {
			r.GHPath = path
		}
		b, e := r.Run(context.Background(), path, "-test.run=^TestExecRunnerHelper$", "--", "updater-dump-environment")
		if e != nil {
			t.Fatal(e)
		}
		var env []string
		if e = json.Unmarshal(b, &env); e != nil {
			t.Fatalf("helper output %q: %v", b, e)
		}
		joined := strings.Join(env, "\n")
		for _, forbidden := range []string{"evil.invalid", "evil/repo", "secret.so", "do-not-forward"} {
			if strings.Contains(joined, forbidden) {
				t.Fatal("unsafe helper environment", forbidden)
			}
		}
		if strings.Contains(joined, "readonly-token") != gh {
			t.Fatalf("token isolation failed: gh=%t env=%s", gh, joined)
		}
	}
}
