package deploy

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func readDeploymentFile(t *testing.T, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("read %v: %v", parts, err)
	}
	return string(data)
}

func TestContainerEntrypointUsesInheritedDescriptors(t *testing.T) {
	body := readDeploymentFile(t, "container-node-entrypoint.sh")
	for _, required := range []string{
		"set -eu",
		"umask 077",
		"ulimit -c 0",
		`exec 3<"$private_key_file"`,
		`exec 4<"$frost_wrap_key_file"`,
		`exec 5<"$arbitrage_private_key_file"`,
		"--private-key-fd=3",
		"--frost-wrap-key-fd=4",
		"--arbitrage-private-key-fd=5",
		"--expected-address=*",
		"--expected-address=<address> is required",
		"--expected-arbitrage-address=<address> is required when the arbitrage secret is mounted",
		"check-config|identity|config|node|register",
		`exec "$node_bin" --check-config`,
		`exec "$register_bin" --private-key-fd=3 --print-address`,
		"register requires --confirm-ready as the first argument",
		"register journal path is fixed by the container entrypoint",
		`exec "$register_bin" --private-key-fd=3 "$@" --state-file=/state/registration.json`,
		"config requires exactly one print mode",
		"/run/secrets/dex-miner/private_key",
		"/run/secrets/dex-arbitrage/private_key",
		`exec "$node_bin"`,
	} {
		if !strings.Contains(body, required) {
			t.Errorf("container entrypoint is missing %q", required)
		}
	}
	for _, forbidden := range []string{"export private", "export frost", "$(cat ", "`cat "} {
		if strings.Contains(body, forbidden) {
			t.Errorf("container entrypoint contains forbidden secret handling %q", forbidden)
		}
	}
}

func TestReleaseToolFailsClosedForWindowsNodeBundles(t *testing.T) {
	body := readDeploymentFile(t, "..", "cmd", "release", "main.go")
	for _, required := range []string{
		"validateReleaseTarget(*targetOS)",
		"Windows node releases are disabled",
		"container-node-entrypoint.sh",
		"start-dex-node",
		"quick-node.sh",
		"frostbit-node",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("release tool is missing %q", required)
		}
	}
}

func TestContainerManifestsUsePerNodeSecretMounts(t *testing.T) {
	prod := readDeploymentFile(t, "k8s", "prod.yaml")
	testManifest := readDeploymentFile(t, "k8s", "test.yaml")
	for name, body := range map[string]string{"prod": prod, "test": testManifest} {
		for _, required := range []string{
			"/usr/local/bin/dex-node-entrypoint",
			"/run/secrets/dex-miner",
			"private_key",
			"frost_wrap_key",
			"DEX_MINER_ADDRESS",
			"--expected-address=$(DEX_MINER_ADDRESS)",
			"--data-dir=/data/$(DEX_MINER_ADDRESS)",
			"--resume=true",
			"dex-miner-address-secrets",
		} {
			if !strings.Contains(body, required) {
				t.Errorf("%s manifest is missing %q", name, required)
			}
		}
		if strings.Contains(body, "DEX_NODE_PRIVATE_KEY") || strings.Contains(body, "DEX_PRIVATE_KEY_FILE") || strings.Contains(body, "DEX_FROST_WRAP_KEY_FILE") {
			t.Errorf("%s manifest still maps secret bytes into an environment variable", name)
		}
		for _, forbidden := range []string{"/data/node0", "dex-node-0-secrets", "/run/secrets/dex-node"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s manifest still uses positional identity path %q", name, forbidden)
			}
		}
	}
	for _, required := range []string{"DEX_EXPLORER_SERVICE_TOKEN", "explorer_service_token", "secretKeyRef:"} {
		if !strings.Contains(testManifest, required) {
			t.Errorf("test manifest is missing service-token secret wiring %q", required)
		}
	}

	local20 := readDeploymentFile(t, "docker-compose.local20.yml")
	if got := strings.Count(local20, "target: /run/secrets/dex-miner"); got != 20 {
		t.Fatalf("local20 has %d node secret mounts; want 20", got)
	}
	if got := strings.Count(local20, "--expected-address=${DEX_MINER_ADDRESS_"); got != 20 {
		t.Fatalf("local20 has %d address-bound node commands; want 20", got)
	}
	if got := strings.Count(local20, "--resume=true"); got != 20 {
		t.Fatalf("local20 has %d explicit persistent-data resume flags; want 20", got)
	}
	for _, required := range []string{"DEX_EXPLORER_SERVICE_TOKEN", "EXPLORER_ENABLE_OPERATOR_NODE_CONTROL", "EXPLORER_SIMULATOR_OPERATOR_ADDRESSES"} {
		if !strings.Contains(local20, required) {
			t.Errorf("local20 is missing authenticated diagnostics setting %q", required)
		}
	}
	if strings.Contains(local20, ":7000") {
		t.Error("local20 still publishes disabled pprof ports")
	}
	for _, forbidden := range []string{"/node00", "/node01", "/node02", "/node03", "/node04", "/node05", "/node06", "/node07", "/node08", "/node09", "/node10", "/node11", "/node12", "/node13", "/node14", "/node15", "/node16", "/node17", "/node18", "/node19"} {
		if strings.Contains(local20, forbidden) {
			t.Errorf("local20 still uses positional secret/data slot %q", forbidden)
		}
	}
}

func TestRuntimeImageDoesNotEmbedEnvironmentConfig(t *testing.T) {
	dockerfile := readDeploymentFile(t, "..", "Dockerfile")
	for _, forbidden := range []string{"COPY . .", "COPY . ./", "COPY explorer ./", "COPY config ./config"} {
		if strings.Contains(dockerfile, forbidden) {
			t.Errorf("Dockerfile contains broad build-context copy %q", forbidden)
		}
	}
	if !strings.Contains(dockerfile, "COPY config/*.go ./config/") {
		t.Fatal("Dockerfile does not explicitly whitelist config Go sources")
	}
	if !strings.Contains(dockerfile, "COPY protocol ./protocol") {
		t.Fatal("Dockerfile omits the protocol package imported by runtime code")
	}
	runtimeMarker := "FROM debian:bookworm-slim"
	runtimeAt := strings.Index(dockerfile, runtimeMarker)
	if runtimeAt < 0 {
		t.Fatalf("Dockerfile is missing runtime stage %q", runtimeMarker)
	}
	if strings.Contains(dockerfile[runtimeAt:], "COPY config ") {
		t.Fatal("runtime image still copies configuration into an image layer")
	}
	for _, required := range []string{
		"USER 65532:65532",
		"dex-node-entrypoint",
		"/usr/share/licenses/frostbit-dex/LICENSE",
		"/usr/share/licenses/frostbit-dex/NOTICE",
		"COPY cmd/miner-register ./cmd/miner-register",
		"/out/miner-register /app/miner-register",
		`org.opencontainers.image.licenses="GPL-3.0-only"`,
		"test -f /usr/share/licenses/frostbit-dex/LICENSE",
		"test ! -L /usr/share/licenses/frostbit-dex/LICENSE",
		"test -f /usr/share/licenses/frostbit-dex/NOTICE",
		"test ! -L /usr/share/licenses/frostbit-dex/NOTICE",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("Dockerfile is missing %q", required)
		}
	}
}

func TestDockerContextExcludesLocalAndSensitiveMaterial(t *testing.T) {
	body := readDeploymentFile(t, "..", ".dockerignore")
	for _, required := range []string{
		".local/",
		".gocache*/",
		".tmp-go*/",
		"config/env",
		"deploy/physical20",
		".env*",
		"*.crt",
		"miner_keys.txt",
		"tmp/",
		"tem/",
		"*.pprof",
		"*.pb.gz",
		"coverage/",
		"*.csv",
		"sender/tx_data.jsonl",
		"*.jsonl",
		"**/*.jsonl",
		"!protocol/**",
		"!LICENSE",
		"!NOTICE",
		"!cmd/miner-register/**",
	} {
		if !strings.Contains(body, required) {
			t.Errorf(".dockerignore is missing %q", required)
		}
	}
	defaultDeny := strings.Index(body, "**")
	senderAllow := strings.LastIndex(body, "!sender/**")
	jsonlDeny := strings.LastIndex(body, "**/*.jsonl")
	if defaultDeny < 0 || senderAllow < 0 || jsonlDeny <= senderAllow {
		t.Fatal(".dockerignore must default-deny and re-exclude JSONL captures after sender is allowlisted")
	}
}

func TestRootFrostTemplateDefaultsDisabled(t *testing.T) {
	body := readDeploymentFile(t, "..", "config", "frost_default.json")
	var config struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal([]byte(body), &config); err != nil {
		t.Fatalf("parse root FROST template: %v", err)
	}
	if config.Enabled {
		t.Fatal("root public FROST template is enabled; custody must require an explicit operator profile")
	}
}

func TestReleaseInventoryUsesExplicitValidatedCopies(t *testing.T) {
	body := readDeploymentFile(t, "..", "cmd", "release", "main.go")
	for _, required := range []string{
		"var releaseConfigFiles = []string{",
		`"config.json"`,
		`"genesis.json"`,
		`"frost_default.json"`,
		`"witness_default.json"`,
		`"peers.json"`,
		"validateReleaseConfiguration(src, mode)",
		"validatePublicReleaseJSON(name, data)",
		"copyRegularFileChecked",
		"stageReleaseLegalFiles(root, staging)",
		`[]string{"LICENSE", "NOTICE"}`,
		"prepareCleanNPMProject",
		"copyTreeChecked",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("release inventory contract is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		`copyTree(filepath.Join(root, "config")`,
		`copyTree(filepath.Join(root, "explorer")`,
		`copyTree(filepath.Join(root, "wallet")`,
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("release tool contains broad inventory copy %q", forbidden)
		}
	}
}

func TestPublicCIVerifiesBothSourceAndExportedTrees(t *testing.T) {
	body := readDeploymentFile(t, "..", ".github", "workflows", "public-ci.yml")
	for _, required := range []string{
		"actions/setup-node@v4",
		"explorer/package-lock.json",
		"wallet/package-lock.json",
		"bash scripts/verify-public.sh",
		"GOFLAGS: -buildvcs=false",
		"-f scripts/package/main.go && -f scripts/publicguard/main.go",
		"go -C scripts run ./package -root .. -out \"$export_root\"",
		"go -C scripts run ./publicguard -root \"$export_root\"",
		"bash \"$export_root/scripts/verify-public.sh\"",
		"FAILED: incomplete publication tooling",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("public CI contract is missing %q", required)
		}
	}
	for _, privateTool := range []string{"scripts/precommit", "consistency_check"} {
		if strings.Contains(body, privateTool) {
			t.Errorf("public CI exposes private tooling %q", privateTool)
		}
	}
}

func TestPublicRepositoryIgnoreContract(t *testing.T) {
	gitignore := readDeploymentFile(t, "..", ".gitignore")
	for _, required := range []string{".local/", "deploy/physical20/"} {
		if !strings.Contains(gitignore, required) {
			t.Errorf(".gitignore is missing %q", required)
		}
	}
	if strings.Contains(gitignore, "explorer/package-lock.json") {
		t.Fatal("explorer/package-lock.json is still ignored; npm ci would not be reproducible")
	}
	for _, required := range []string{"!bin/README.md", "!bin/README.en.md"} {
		if !strings.Contains(gitignore, required) {
			t.Errorf("release documentation would be lost in a fresh repository: missing %q", required)
		}
	}
	attributes := readDeploymentFile(t, "..", ".gitattributes")
	if !strings.Contains(attributes, "deploy/physical20/** export-ignore") {
		t.Fatal("deploy/physical20 is not excluded from git archives")
	}
}

func TestPublicTemplatesDoNotLinkOperatorAccountsOrInfrastructure(t *testing.T) {
	bodies := []string{
		readDeploymentFile(t, "..", "bin", "README.md"),
		readDeploymentFile(t, "..", "bin", "README.en.md"),
		readDeploymentFile(t, "k8s", "prod.yaml"),
	}
	for _, body := range bodies {
		lower := strings.ToLower(body)
		for _, forbidden := range []string{"github.com/", "t.me/", "telegram.me/", "mailto:", ".io"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("public template still links an operator account or non-neutral infrastructure %q", forbidden)
			}
		}
	}
}

func TestDeploymentYAMLParses(t *testing.T) {
	paths := [][]string{
		{"..", ".github", "workflows", "public-ci.yml"},
		{"docker-compose.dev.yml"},
		{"docker-compose.local20.yml"},
		{"k8s", "prod.yaml"},
		{"k8s", "test.yaml"},
	}
	for _, parts := range paths {
		name := filepath.Join(parts...)
		t.Run(name, func(t *testing.T) {
			decoder := yaml.NewDecoder(strings.NewReader(readDeploymentFile(t, parts...)))
			for document := 1; ; document++ {
				var value any
				err := decoder.Decode(&value)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("decode YAML document %d: %v", document, err)
				}
			}
		})
	}
}

func TestContainerEntrypointUsesLF(t *testing.T) {
	body := readDeploymentFile(t, "container-node-entrypoint.sh")
	if strings.Contains(body, "\r") {
		t.Fatal("container-node-entrypoint.sh contains CR/CRLF line endings")
	}
	attributes := readDeploymentFile(t, "..", ".gitattributes")
	if !strings.Contains(attributes, "deploy/container-node-entrypoint.sh text eol=lf") {
		t.Fatal("root .gitattributes does not enforce LF for the container entrypoint")
	}
}
