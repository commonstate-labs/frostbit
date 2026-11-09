package deploy

import (
	"os/exec"
	"strings"
	"testing"
)

func TestQuickNodeKeepsSecretsOffArgvAndRequiresExplicitReadyGate(t *testing.T) {
	body := readDeploymentFile(t, "quick-node.sh")
	for _, required := range []string{
		"set -eu",
		"umask 077",
		"ulimit -c 0",
		"--image REGISTRY/IMAGE:VERSION@sha256:DIGEST",
		"require_digest_image",
		"docker pull",
		`"$quick_image_id" identity`,
		`"$quick_image" register --confirm-ready`,
		"dst=/run/secrets/dex-miner/private_key,readonly",
		"--name \"frostbit-miner-register-$quick_address\"",
		"--resubmit-unresolved",
		"--confirm-ready",
		"same-height block ID",
		"external TCP/UDP reachability",
		`"$quick_image_id" check-config`,
		"--bootstrap-mode=latejoin",
		"--resume=true",
		"--listen-addr=0.0.0.0:$quick_port",
		"org.frostbit.miner-address",
		"org.frostbit.config-digest",
		"published image digest or container image no longer matches the immutable deployment intent",
		"could not select exactly one public IPv4 address",
		"--container-network publish|host",
		"org.frostbit.container-network",
		"set -- --network host",
		"host-network container unexpectedly has a different network mode or published ports",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("quick-node.sh is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"export DEX_PRIVATE",
		"export PRIVATE",
		"--private-key=",
		"docker rm -v",
		"docker volume rm",
		"cat $quick_private",
		"cat \"$quick_private\"",
		"--allow-dirty",
		"docker build",
		"git -C",
		"--entrypoint /bin/sh",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("quick-node.sh contains forbidden secret/destructive handling %q", forbidden)
		}
	}
}

func TestQuickNodeRegistrationMountsOnlyThePrivateKey(t *testing.T) {
	body := readDeploymentFile(t, "quick-node.sh")
	start := strings.Index(body, "command_register()")
	if start < 0 {
		t.Fatal("cannot isolate command_register")
	}
	register := body[start:]
	if strings.Contains(register, `src=$quick_secret_dir,dst=/run/secrets/dex-miner`) {
		t.Fatal("registration container can see the complete node secret directory")
	}
	if !strings.Contains(register, `src=$quick_secret_dir/private_key,dst=/run/secrets/dex-miner/private_key,readonly`) {
		t.Fatal("registration container does not mount only the private key")
	}
}

func TestQuickNodePreflightsBeforeInstallingOrStarting(t *testing.T) {
	body := readDeploymentFile(t, "quick-node.sh")
	start := strings.Index(body, "command_up()")
	end := strings.Index(body, "read_metadata()")
	if start < 0 || end <= start {
		t.Fatal("cannot isolate command_up")
	}
	up := body[start:end]
	pull := strings.Index(up, "docker pull")
	check := strings.Index(up, `"$quick_image_id" check-config`)
	derive := strings.Index(up, `"$quick_image_id" identity`)
	installConfig := strings.Index(up, "install_public_config")
	startContainer := strings.Index(up, "docker run -d")
	if pull < 0 || check <= pull || derive <= check || installConfig <= derive || startContainer <= installConfig {
		t.Fatalf("unsafe up ordering: pull=%d check=%d derive=%d install=%d start=%d", pull, check, derive, installConfig, startContainer)
	}
}

func TestQuickNodeShellSyntax(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is unavailable on this platform")
	}
	if output, err := exec.Command(sh, "-n", "quick-node.sh").CombinedOutput(); err != nil {
		t.Fatalf("sh -n quick-node.sh: %v\n%s", err, output)
	}
}

func TestQuickNodeUsesLF(t *testing.T) {
	body := readDeploymentFile(t, "quick-node.sh")
	if strings.Contains(body, "\r") {
		t.Fatal("quick-node.sh contains CR/CRLF line endings")
	}
}
