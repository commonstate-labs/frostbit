package securenode

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

const publicTestMinerAddress = "fbc1qw508d6qejxtdg4y5r3zarvary0c5xw7kng2fs8"

func readAsset(t *testing.T, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("read asset %v: %v", parts, err)
	}
	return string(data)
}

func requireText(t *testing.T, body string, required ...string) {
	t.Helper()
	for _, value := range required {
		if !strings.Contains(body, value) {
			t.Errorf("required text %q is missing", value)
		}
	}
}

func forbidText(t *testing.T, body string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if strings.Contains(body, value) {
			t.Errorf("forbidden text %q is present", value)
		}
	}
}

func TestSecretControlFetchSecurityContract(t *testing.T) {
	body := readAsset(t, "trusted-server", "dex-secretctl")
	requireText(t, body,
		"set -eu",
		"umask 077",
		"cmd_fetch()",
		"SSH_ORIGINAL_COMMAND",
		`[ "$(id -un)" = "$SERVICE_USER" ]`,
		`[ -n "${SSH_CONNECTION:-}" ]`,
		"private)",
		"wrap)",
		"require_canonical_miner_address",
		`secret_file="$SECRET_ROOT/$miner_address/private_key.hex"`,
		`grant_file="$GRANT_ROOT/$miner_address.expires"`,
		`[ ! -L "$SECRET_ROOT/$miner_address" ]`,
		"/run/dex-secret-grants",
		"date +%s",
		"${#secret}",
		"printf '%s\\n' \"$secret\"",
	)
	forbidText(t, body,
		"eval ",
		"sh -c",
		"logger -t dex-secretctl -- \"$secret",
		"audit_node=",
		`secret_file="$SECRET_ROOT/$node/`,
	)
	requireText(t, body, "audit_kind=invalid", "audit_miner=invalid", `"miner=$audit_miner kind=$audit_kind`)
}

func TestSecretControlGrantSecurityContract(t *testing.T) {
	body := readAsset(t, "trusted-server", "dex-secretctl")
	requireText(t, body,
		"MIN_TTL=10",
		"MAX_TTL=600",
		"/run/dex-secret-grants",
		"-m 0750",
		"chmod 0640",
		"chown root:",
		"mktemp",
		"mv -f",
		"cmd_revoke()",
		"revoke) cmd_revoke",
		`mv -f -- "$tmp" "$GRANT_ROOT/$miner_address.expires"`,
		`rm -f -- "$GRANT_ROOT/$miner_address.expires"`,
		"LOCK_ROOT=/run/dex-secretctl-lock",
		`-m 0700 "$LOCK_ROOT"`,
	)
	forbidText(t, body, "install -d -o root -g root -m 0755 /run/lock", "exec 8>")
}

func TestVPSWrapperSecurityContract(t *testing.T) {
	body := readAsset(t, "vps", "start-dex-secure")
	requireText(t, body,
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"ulimit -c 0",
		"BatchMode=yes",
		"StrictHostKeyChecking=yes",
		"UserKnownHostsFile=",
		"GlobalKnownHostsFile=/dev/null",
		"IdentitiesOnly=yes",
		"IdentityAgent=none",
		"ClearAllForwardings=yes",
		"/usr/bin/jq",
		"DEX_MINER_ADDRESS",
		`--arg miner "$DEX_MINER_ADDRESS"`,
		`.reward_address | fbc_address`,
		`.secp_public_key`,
		`.bls_public_key`,
		`.is_miner == true`,
		`def only_keys($allowed):`,
		"exec 3< <(",
		"exec 4< <(",
		"--bootstrap-mode=$DEX_BOOTSTRAP_MODE",
		"--expected-address=$DEX_MINER_ADDRESS",
		"--private-key-fd=3",
		"--frost-wrap-key-fd=4",
		"--no-simulator",
		"requires FROST enabled=true",
		"exec \"$DEX_BIN\"",
	)
	forbidText(t, body,
		"--private-key=",
		"--private-key ",
		"--enable-debug-api",
		"--enable-pprof",
		"export private_key",
		"export wrap_key",
		"DEX_NODE_ID",
		"--node-id",
	)
	validationAt := strings.Index(body, `/usr/bin/jq -e --arg miner "$DEX_MINER_ADDRESS"`)
	fetchAt := strings.Index(body, `private_key=$(fetch_secret private)`)
	if validationAt < 0 || fetchAt < 0 || validationAt > fetchAt {
		t.Error("complete public manifest validation must happen before any signing secret is fetched")
	}
}

func TestSystemdSecurityContract(t *testing.T) {
	body := readAsset(t, "vps", "dex-node-secure.service")
	requireText(t, body,
		"User=dex",
		"Group=dex",
		"UMask=0077",
		"Restart=no",
		"LimitCORE=0",
		"NoNewPrivileges=true",
		"ExecStart=/usr/local/sbin/start-dex-secure",
	)
	forbidText(t, strings.ToLower(body), "private_key", "wrap_key", "frost_share")
}

func TestAuthorizedKeyExampleIsRestricted(t *testing.T) {
	body := readAsset(t, "trusted-server", "authorized_keys")
	requireText(t, body,
		"PUBLIC EXAMPLE ONLY",
		"from=\"",
		"restrict",
		"command=\"/usr/local/sbin/dex-secretctl fetch "+publicTestMinerAddress+"\"",
		"dex-secret-miner-"+publicTestMinerAddress,
	)
	forbidText(t, body, "dex-secret-node-", "fetch node-")
}

func TestTrustedSSHAccountContract(t *testing.T) {
	installScript := readAsset(t, "trusted-server", "install.sh")
	controlScript := readAsset(t, "trusted-server", "dex-secretctl")
	requireText(t, installScript,
		`-g "$SERVICE_GROUP" -m 0750 "$SERVICE_HOME/.ssh"`,
		`-g "$SERVICE_GROUP" -m 0640 /dev/null "$SERVICE_HOME/.ssh/authorized_keys"`,
		"SSHD_CONFIG=/etc/ssh/sshd_config",
		"sshd -t -f \"$candidate\"",
		"openssl rand -base64 48",
		"chpasswd",
		`id -gn "$SERVICE_USER"`,
		`must not belong to additional groups`,
		`UID is shared by another account`,
		`authorized_keys must not be a symlink`,
		"authorizedkeyscommand none",
		"trustedusercakeys none",
		"forcecommand none",
		`mv -f -- "$candidate" "$SSHD_CONFIG"`,
		`-g "$SERVICE_GROUP" -m 0750 "$SCRIPT_DIR/dex-secretctl"`,
		"dex-secretctl validate-install-state",
		`/srv/dex-secrets /run/dex-secret-grants`,
	)
	forbidText(t, installScript, "passwd -d")
	sshdBlock := readAsset(t, "trusted-server", "sshd-dex-secrets.conf")
	forbidText(t, strings.ToLower(sshdBlock), "match all")
	requireText(t, controlScript,
		`chown root:"$SERVICE_GROUP" "$auth_tmp"`,
		`chmod 0640 "$auth_tmp"`,
		`$NF != marker`,
		"already assigned to another miner address",
		"authorized_keys contains an unmanaged or unrestricted entry",
		`forced_command="$CONTROL fetch $miner_address"`,
		`marker="dex-secret-miner-$miner_address"`,
		"validate-install-state",
		"legacy node-N secret authorization detected",
		"automatic identity mapping is forbidden",
	)
	forbidText(t, controlScript,
		"index($0, marker)",
		`forced_command="$CONTROL fetch $node"`,
		"migrate-authorized-keys",
		"dex-secret-node-$node",
	)
	validationAt := strings.Index(controlScript, `validate_managed_authorized_keys "$AUTH_KEYS"`)
	secretCreationAt := strings.Index(controlScript, `slot_dir="$SECRET_ROOT/$miner_address"`)
	if validationAt < 0 || secretCreationAt < 0 || validationAt > secretCreationAt {
		t.Error("authorized_keys validation must happen before creating miner secrets")
	}
}

func TestTrustedServerUsesSingleControlScript(t *testing.T) {
	for _, name := range []string{"dex-secret-broker", "dex-secret-allow", "dex-secret-provision"} {
		if _, err := os.Stat(filepath.Join("trusted-server", name)); !os.IsNotExist(err) {
			t.Errorf("legacy trusted-server script %s should not remain in the package", name)
		}
	}
}

func TestVPSInstallerPinsExpectedHostAndRejectsPrivateKey(t *testing.T) {
	body := readAsset(t, "vps", "install.sh")
	requireText(t, body,
		"known_hosts source appears to contain a private key",
		"--miner-address",
		"miner_address",
		`ssh-keygen -F "$known_host_name"`,
		"ssh-ed25519",
		"this secure miner template requires FROST enabled=true",
		"/usr/bin/jq",
		`(.kind == "public-peer-manifest")`,
		`(.genesis_validators | type == "array" and length > 0)`,
		`(.bootstrap_endpoints | type == "array" and length > 0)`,
		`ascii_downcase | gsub("[_ -]"; "")`,
		`. == "privatekey"`,
		`.reward_address | fbc_address`,
		`def only_keys($allowed):`,
		`([.bootstrap_endpoints[].address] | sort == ($validator_addresses | sort))`,
		`DEX_MINER_ADDRESS=%s`,
		`/var/lib/dex/miners/$miner_address`,
		`$miner_address.secret_fetch`,
		"legacy unbound or node-N fetch key found",
		"legacy unbound node runtime directory found",
		"must not belong to additional groups",
		"primary group of unexpected users",
	)
	forbidText(t, body,
		"DEX_NODE_ID",
		"--node-id",
		`printf 'DEX_CONFIG_DIR=/etc/dex/node`,
		`data_dir="/var/lib/dex/node`,
	)
	validationAt := strings.Index(body, `/usr/bin/jq -e --arg miner "$miner_address"`)
	keyCreationAt := strings.Index(body, `ssh-keygen -q -t ed25519`)
	if validationAt < 0 || keyCreationAt < 0 || validationAt > keyCreationAt {
		t.Error("public manifest validation must happen before the VPS fetch identity is created")
	}
}

func TestDeploymentTextFilesUseLF(t *testing.T) {
	paths := []string{
		filepath.Join("trusted-server", "dex-secretctl"),
		filepath.Join("trusted-server", "install.sh"),
		filepath.Join("trusted-server", "sshd-dex-secrets.conf"),
		filepath.Join("trusted-server", "authorized_keys"),
		filepath.Join("vps", "start-dex-secure"),
		filepath.Join("vps", "install.sh"),
		filepath.Join("vps", "dex-node-secure.service"),
		filepath.Join("vps", "secure-node.defaults"),
		filepath.Join("vps", "trusted-server-known-hosts.defaults"),
	}
	attributes := readAsset(t, ".gitattributes")
	for _, path := range paths {
		body := readAsset(t, path)
		if strings.Contains(body, "\r") {
			t.Errorf("%s contains CR/CRLF line endings", path)
		}
		requireText(t, attributes, filepath.ToSlash(path)+" text eol=lf")
	}
}

func TestPublicDefaultsUseMinerAddressIdentity(t *testing.T) {
	body := readAsset(t, "vps", "secure-node.defaults")
	requireText(t, body,
		"PUBLIC EXAMPLE ONLY",
		"DEX_MINER_ADDRESS="+publicTestMinerAddress,
		"/var/lib/dex/miners/"+publicTestMinerAddress,
		publicTestMinerAddress+".secret_fetch",
		"/var/log/dex/"+publicTestMinerAddress+".log",
	)
	forbidText(t, body, "DEX_NODE_ID", "node-0", "private_key", "wrap_key")
	ignore := readAsset(t, ".gitignore")
	requireText(t, ignore, "*.secret_fetch", "*.secret_fetch.pub", "*.hex", "*.key")
}

func TestWrapperManifestModeContractWhenJQIsAvailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the production wrapper requires Linux paths")
	}
	for _, path := range []string{"/usr/bin/bash", "/usr/bin/jq", "/usr/bin/ssh", "/usr/bin/timeout"} {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			t.Skipf("required production dependency is unavailable: %s", path)
		}
	}

	const genesisAddress = "fbc1qxdr4f5mr230u2j6hu2pv4zpvr29ade8vxzvgx3"
	temp := t.TempDir()
	configDir := filepath.Join(temp, "config")
	dataDir := filepath.Join(temp, "miners", publicTestMinerAddress)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path string, body []byte, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, body, mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(configDir, "genesis.json"), []byte(`{"chain_id":1}`), 0o600)
	write(filepath.Join(configDir, "frost_default.json"), []byte(`{"enabled":true,"vault":{"defaultK":1}}`), 0o600)
	identityPath := filepath.Join(temp, publicTestMinerAddress+".secret_fetch")
	knownHostsPath := filepath.Join(temp, "known_hosts")
	binPath := filepath.Join(temp, "node")
	write(identityPath, []byte("not-a-real-fetch-key\n"), 0o600)
	write(knownHostsPath, []byte("127.0.0.1 ssh-ed25519 AAAA\n"), 0o600)
	write(binPath, []byte("#!/bin/sh\nexit 99\n"), 0o700)

	manifest := map[string]any{
		"_notice":        "PUBLIC EXAMPLE; NO SECRETS",
		"schema_version": 1,
		"kind":           "public-peer-manifest",
		"chain_id":       1,
		"genesis_validators": []map[string]any{{
			"address":         genesisAddress,
			"reward_address":  genesisAddress,
			"secp_public_key": "02" + strings.Repeat("00", 32),
			"bls_public_key":  "00",
			"is_miner":        true,
		}},
		"bootstrap_endpoints": []map[string]any{{
			"address":  genesisAddress,
			"endpoint": "genesis.example.com:6000",
		}},
	}
	writeManifest := func(value map[string]any) {
		t.Helper()
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(configDir, "peers.json"), body, 0o600)
	}
	run := func(mode string) string {
		t.Helper()
		wrapper, err := filepath.Abs(filepath.Join("vps", "start-dex-secure"))
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("/usr/bin/bash", wrapper)
		cmd.Env = []string{
			"PATH=/usr/bin:/bin",
			"DEX_MINER_ADDRESS=" + publicTestMinerAddress,
			"DEX_BIN=" + binPath,
			"DEX_CONFIG_DIR=" + configDir,
			"DEX_PEERS_FILE=" + filepath.Join(configDir, "peers.json"),
			"DEX_DATA_DIR=" + dataDir,
			"DEX_BOOTSTRAP_MODE=" + mode,
			"DEX_ADVERTISE_ADDR=latejoin.example.com:6001",
			"DEX_SECRET_HOST=127.0.0.1",
			"DEX_SECRET_PORT=1",
			"DEX_SECRET_IDENTITY_FILE=" + identityPath,
			"DEX_SECRET_KNOWN_HOSTS=" + knownHostsPath,
			"DEX_SECRET_FETCH_TIMEOUT_SECONDS=5",
		}
		output, _ := cmd.CombinedOutput()
		return string(output)
	}

	writeManifest(manifest)
	genesisOutput := run("genesis")
	if !strings.Contains(genesisOutput, "genesis mode must also contain this miner") {
		t.Fatalf("genesis mode accepted a local address absent from the genesis manifest:\n%s", genesisOutput)
	}
	latejoinOutput := run("latejoin")
	if strings.Contains(latejoinOutput, "peers.json must contain complete") ||
		!strings.Contains(latejoinOutput, "trusted-server refused or failed") {
		t.Fatalf("latejoin did not accept an unlisted local address after validating its trusted genesis routes:\n%s", latejoinOutput)
	}

	validators := manifest["genesis_validators"].([]map[string]any)
	delete(validators[0], "reward_address")
	writeManifest(manifest)
	incompleteOutput := run("latejoin")
	if !strings.Contains(incompleteOutput, "peers.json must contain complete") {
		t.Fatalf("wrapper accepted an incomplete genesis public identity:\n%s", incompleteOutput)
	}
}

func TestDeploymentAssetsContainNoApparentSecret(t *testing.T) {
	secretPattern := regexp.MustCompile(`(?i)(?:^|[^0-9a-f])[0-9a-f]{64}(?:[^0-9a-f]|$)`)
	privateKeyHeader := regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----`)
	err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || strings.HasSuffix(path, ".go") {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if secretPattern.Match(body) {
			t.Errorf("asset %s contains an apparent 64-hex secret", path)
		}
		if privateKeyHeader.Match(body) {
			t.Errorf("asset %s contains an SSH private-key header", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestShellSyntaxWhenInterpreterIsAvailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("native POSIX shell is unavailable on Windows; Linux CI performs syntax validation")
	}

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal("sh is required on Unix CI")
	}
	for _, path := range []string{
		filepath.Join("trusted-server", "dex-secretctl"),
		filepath.Join("trusted-server", "install.sh"),
		filepath.Join("vps", "install.sh"),
	} {
		if output, runErr := exec.Command(sh, "-n", path).CombinedOutput(); runErr != nil {
			t.Errorf("sh -n %s: %v\n%s", path, runErr, output)
		}
	}

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal("bash is required on Unix CI for the FD wrapper")
	}
	path := filepath.Join("vps", "start-dex-secure")
	if output, runErr := exec.Command(bash, "-n", path).CombinedOutput(); runErr != nil {
		t.Fatalf("bash -n %s: %v\n%s", path, runErr, output)
	}
}
