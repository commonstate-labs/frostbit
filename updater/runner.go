package updater

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const maxCommandOutput = 64 << 10

type boundedOutput struct{ data []byte }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if len(b.data) < maxCommandOutput {
		left := maxCommandOutput - len(b.data)
		if len(p) > left {
			p = p[:left]
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

type ExecRunner struct{ GHPath, GHConfigDir string }

func (r ExecRunner) Run(ctx context.Context, program string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	configureCommand(cmd)
	                                                                                 
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C", "GH_HOST=github.com", "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0"}
	if filepath.Clean(program) == r.GHPath {
		if r.GHConfigDir != "" {
			cmd.Env = append(cmd.Env, "GH_CONFIG_DIR="+r.GHConfigDir)
		}
		for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
			if v := os.Getenv(key); v != "" {
				cmd.Env = append(cmd.Env, key+"="+v)
			}
		}
	}
	var out boundedOutput
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.data, fmt.Errorf("%s failed: %w", program, err)
	}
	return out.data, nil
}

func (a *Agent) command(ctx context.Context, program string, args ...string) ([]byte, error) {
	child, cancel := context.WithTimeout(ctx, a.cfg.CommandTimeout)
	defer cancel()
	return a.dep.Runner.Run(child, program, args...)
}
