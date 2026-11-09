                                                                                                   
                                                           
package main

import (
	"archive/zip"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	dexconfig "dex/config"
	"dex/frost/ceremony"
	"dex/updater"
	"dex/utils"

	"github.com/btcsuite/btcd/btcec/v2"
)

var (
	version      = flag.String("version", "0.8.2", "版本号，留空则 git describe；--publish 时留空则 GitHub 最新 tag 自增")
	outputFlag   = flag.String("output", "", "输出 zip 路径，留空则 dist/dex-<ver>-<os>-<arch>.zip")
	skipFrontend = flag.Bool("skip-frontend", false, "跳过 npm run build")
	skipBackend  = flag.Bool("skip-backend", false, "跳过 go build")
	keepStaging  = flag.Bool("keep-staging", false, "保留 staging 目录，便于排查")
	targetOS     = flag.String("goos", runtime.GOOS, "目标 OS: linux/darwin；Windows 节点包在安全 launcher 完成前禁用")
	targetArch   = flag.String("goarch", runtime.GOARCH, "目标架构: amd64/arm64")
	publish      = flag.Bool("publish", false, "完成后发布到 GitHub Releases（需 GITHUB_TOKEN 环境变量）")
	publishRepo  = flag.String("repo", updater.Repository, "固定的官方 GitHub 发布仓库")
	releaseProxy = flag.String("proxy", strings.TrimSpace(os.Getenv("RELEASE_PROXY")), "GitHub 发布代理；默认读取 RELEASE_PROXY，留空则不设置")
	configMode   = flag.String("config-mode", "template", "配置模式: template（默认，明确不可运行）或 runnable（严格验证完整网络配置）")
	upgradeMode  = flag.String("upgrade-mode", "compatible", "升级模式: compatible（兼容修复）或 coordinated（需要协调部署，禁止自主安装）")
)

var releaseEnv = flag.String("env", "dev", "release config env to package from config/env/<env>")

func main() {
	flag.Parse()
	configureHTTPProxy(*releaseProxy)
	root := findRepoRoot()
	envName := normalizeReleaseEnv(*releaseEnv)
	mode, err := parseReleaseConfigMode(*configMode)
	if err != nil {
		fatal(err.Error())
	}
	if err := validateReleaseOptions(*publish, *skipBackend, *skipFrontend); err != nil {
		fatal(err.Error())
	}
	repo := strings.TrimSpace(*publishRepo)
	githubToken := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))

	if err := validateReleaseTarget(*targetOS); err != nil {
		fatal(err.Error())
	}

	var token string
	if *publish {
		validatePublishRepo(repo)
		token = githubToken
		if token == "" {
			fatal("--publish requires GITHUB_TOKEN in the runtime environment")
		}
		must(requireCleanPublishTree(root), "verify release source")
	}

	ver := *version
	if ver == "" {
		if *publish {
			ver, err = newGitHubPublisher(token).NextVersion()
			must(err, "resolve next release version")
			fmt.Printf("Next release version: %s\n", ver)
		} else {
			ver = gitDescribe(root)
		}
	}
	if normalized, normalizeErr := canonicalReleaseVersion(ver); normalizeErr == nil {
		ver = normalized
	} else if !*publish && *version == "" {
		ver = "dev"
	} else {
		fatal(normalizeErr.Error())
	}
	if err := validateBuildPlatform(*targetOS, *targetArch); err != nil {
		fatal(err.Error())
	}
	if err := validateUpgradeMode(*upgradeMode); err != nil {
		fatal(err.Error())
	}
	commit := gitFullCommit(root)
	if *publish {
		must(newGitHubPublisher(token).Preflight(ver), "verify immutable release configuration")
	}

	dist := filepath.Join(root, "dist")
	staging := filepath.Join(dist, "staging", "dex")
	buildWorkspace := filepath.Join(dist, "staging", "build")
	must(os.RemoveAll(filepath.Join(dist, "staging")), "clean staging")
	mkdir(staging)
	must(stageReleaseLegalFiles(root, staging), "stage release legal files")

	if !*skipBackend {
		buildBackend(root, staging, envName, ver, commit)
	}
	copyFile(filepath.Join(root, "bin", "README.md"), filepath.Join(staging, "README.md"))
	copyFile(filepath.Join(root, "bin", "README.en.md"), filepath.Join(staging, "README.en.md"))
	launcherPath := filepath.Join(staging, "start-dex-node")
	copyFile(filepath.Join(root, "deploy", "container-node-entrypoint.sh"), launcherPath)
	must(os.Chmod(launcherPath, 0o555), "make node FD launcher executable")
	quickNodePath := filepath.Join(staging, "frostbit-node")
	copyFile(filepath.Join(root, "deploy", "quick-node.sh"), quickNodePath)
	must(os.Chmod(quickNodePath, 0o555), "make published-image launcher executable")
	copyReleaseConfig(root, staging, envName, mode)
	if !*skipFrontend {
		must(stageFrontendBuildInputs(root, buildWorkspace), "stage shared frontend build inputs")
		// Explorer imports the wallet icon from the staged wallet project.
		walletDist := buildWallet(root, buildWorkspace)
		explorerDist := buildFrontend(root, buildWorkspace)
		must(stageFrontendAssets(staging, explorerDist, walletDist), "stage frontend assets")
	}

	out := *outputFlag
	if out == "" {
		out = filepath.Join(dist, fmt.Sprintf("dex-%s-%s-%s.zip", ver, *targetOS, *targetArch))
	}
	mkdir(filepath.Dir(out))
	must(zipTree(staging, out, "dex"), "package release archive")
	assets := []string{out}
	if !*skipBackend && ver != "dev" {
		updateAssets, assetErr := stageUpdateAssets(staging, filepath.Dir(out), filepath.Join(root, "config", "env", envName), ver, commit, *targetOS, *targetArch, *upgradeMode)
		must(assetErr, "stage automatic update assets")
		assets = append(assets, updateAssets...)
	}

	if !*keepStaging {
		must(os.RemoveAll(filepath.Join(dist, "staging")), "remove staging")
	}

	info, _ := os.Stat(out)
	fmt.Printf("Done: %s (%.2f MB)\n", out, float64(info.Size())/1024/1024)

	if *publish {
		must(requireCleanPublishTree(root), "verify release source after build")
		if gitFullCommit(root) != commit {
			fatal("HEAD changed during the release build")
		}
		must(newGitHubPublisher(token).Publish(ver, commit, assets), "publish immutable release")
		fmt.Printf("Released %s → https://github.com/%s/releases/tag/%s\n", ver, repo, ver)
	}
}

func configureHTTPProxy(proxyRaw string) {
	proxyRaw = strings.TrimSpace(proxyRaw)
	if proxyRaw == "" {
		return
	}
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		_ = os.Setenv(k, proxyRaw)
	}
	u, err := url.Parse(proxyRaw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		fatal("parse --proxy: invalid proxy URL")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = http.ProxyURL(u)
	http.DefaultClient = &http.Client{Transport: tr}
}

func findRepoRoot() string {
	dir, _ := os.Getwd()
	for {
		if b, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if strings.TrimSpace(line) == "module dex" {
					return dir
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			fatal("repo root not found (no go.mod containing 'module dex')")
		}
		dir = parent
	}
}

func gitDescribe(root string) string {
	out, err := cleanCommandOutput(root, "git", "describe", "--tags", "--always", "--dirty")
	if err != nil {
		return "dev"
	}
	return strings.TrimSpace(string(out))
}

type backendBuildTarget struct{ name, pkg string }

func backendBuildTargets() []backendBuildTarget {
	return []backendBuildTarget{
		{"dex", "./cmd/main"},
		{"explorer", "./cmd/explorer"},
		{"frost-ceremony", "./cmd/frost-ceremony"},
		{"miner-register", "./cmd/miner-register"},
		{"node-key", "./cmd/node-key"},
	}
}

func buildBackend(root, staging, envName, ver, commit string) {
	ext := ""
	if *targetOS == "windows" {
		ext = ".exe"
	}
	buildTime := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	ldflags := fmt.Sprintf("-s -w -X main.BuildCommit=%s -X main.BuildTime=%s -X main.BuildTag=%s -X main.BuildVersion=%s", commit, buildTime, envName, ver)
	env := environmentWithOverrides(scrubBuildEnvironment(os.Environ()),
		"GOOS="+*targetOS,
		"GOARCH="+*targetArch,
		"CGO_ENABLED=0",
	)
	for _, t := range backendBuildTargets() {
		runEnv(root, env, "go", "build", "-mod=readonly", "-trimpath", "-ldflags="+ldflags,
			"-o", filepath.Join(staging, t.name+ext), t.pkg)
	}
}

type releaseConfigMode string

const (
	releaseConfigTemplate releaseConfigMode = "template"
	releaseConfigRunnable releaseConfigMode = "runnable"
)

func parseReleaseConfigMode(raw string) (releaseConfigMode, error) {
	switch releaseConfigMode(strings.ToLower(strings.TrimSpace(raw))) {
	case releaseConfigTemplate:
		return releaseConfigTemplate, nil
	case releaseConfigRunnable:
		return releaseConfigRunnable, nil
	default:
		return "", fmt.Errorf("--config-mode must be template or runnable")
	}
}

func validateReleaseOptions(publish, skipBackend, skipFrontend bool) error {
	if publish && (skipBackend || skipFrontend) {
		return fmt.Errorf("--publish requires a clean complete build; --skip-backend and --skip-frontend are forbidden")
	}
	return nil
}

                                                                      
                                                                              
                                                                        
func scrubBuildEnvironment(environment []string) []string {
	clean := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || isSensitiveBuildEnvironmentKey(key) || containsCredentialBearingURL(value) {
			continue
		}
		clean = append(clean, entry)
	}
	return clean
}

var credentialBearingURL = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^/@\s,|]+@`)

func containsCredentialBearingURL(value string) bool {
	return credentialBearingURL.MatchString(value)
}

func isSensitiveBuildEnvironmentKey(key string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(key))
	if normalized == "" {
		return true
	}
	for _, suffix := range []string{"_PROXY", "_TOKEN", "_SECRET", "_PASSWORD", "_PASSWD", "_CREDENTIALS", "_PRIVATE_KEY", "_AUTH"} {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	for _, fragment := range []string{"TOKEN", "SECRET", "PASSWORD", "PASSPHRASE", "CREDENTIAL", "PRIVATE_KEY"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	for _, prefix := range []string{"GITHUB_", "GH_", "TELEGRAM_", "ACTIONS_", "AWS_", "AZURE_", "GOOGLE_", "GCLOUD_"} {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	switch normalized {
	case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "RELEASE_PROXY",
		"GIT_ASKPASS", "GIT_CONFIG_COUNT", "GIT_TERMINAL_PROMPT", "GIT_SSH_COMMAND",
		"SSH_ASKPASS", "SSH_AUTH_SOCK", "SSH_AGENT_PID", "DOCKER_AUTH_CONFIG", "NPM_CONFIG_USERCONFIG":
		return true
	default:
		return strings.HasPrefix(normalized, "GIT_CONFIG_KEY_") || strings.HasPrefix(normalized, "GIT_CONFIG_VALUE_")
	}
}

func environmentWithOverrides(environment []string, overrides ...string) []string {
	overrideKeys := make(map[string]struct{}, len(overrides))
	for _, entry := range overrides {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			overrideKeys[strings.ToUpper(key)] = struct{}{}
		}
	}
	result := make([]string, 0, len(environment)+len(overrides))
	for _, entry := range environment {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, overridden := overrideKeys[strings.ToUpper(key)]; overridden {
				continue
			}
		}
		result = append(result, entry)
	}
	return append(result, overrides...)
}

func normalizeReleaseEnv(env string) string {
	env = strings.TrimSpace(env)
	if env == "" {
		return "dev"
	}
	if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`).MatchString(env) {
		fatal(fmt.Sprintf("invalid release env %q", env))
	}
	return env
}

func validatePublishRepo(repo string) {
	if repo != updater.Repository {
		fatal("--repo must be the fixed official repository " + updater.Repository)
	}
}

func validateReleaseTarget(goos string) error {
	if strings.EqualFold(strings.TrimSpace(goos), "windows") {
		return fmt.Errorf("Windows node releases are disabled until a named-pipe or inherited-handle secret launcher is available; build Explorer separately if needed")
	}
	return nil
}

func gitFullCommit(root string) string {
	out, err := cleanCommandOutput(root, "git", "rev-parse", "HEAD")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func cleanCommandOutput(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = scrubBuildEnvironment(os.Environ())
	return cmd.Output()
}

var releaseConfigFiles = []string{
	"config.json",
	"genesis.json",
	"frost_default.json",
	"witness_default.json",
	"peers.json",
}

func copyReleaseConfig(root, staging, envName string, mode releaseConfigMode) {
	src := filepath.Join(root, "config", "env", envName)
	if err := requireRegularDirectory(src); err != nil {
		fatal(fmt.Sprintf("release env %q not found: %v", envName, err))
	}

	must(stageReleaseConfiguration(src, staging, envName, mode), "stage release configuration")
	if mode == releaseConfigTemplate {
		fmt.Println("Configuration: template (non-runnable; CONFIGURATION_REQUIRED added)")
	} else {
		fmt.Println("Configuration: runnable (strict public network validation passed)")
	}
}

func stageReleaseConfiguration(src, staging, envName string, mode releaseConfigMode) error {
	if err := validateReleaseConfiguration(src, mode); err != nil {
		return err
	}

	                                                                        
	                                                                            
	dst := filepath.Join(staging, "config", "runtime")
	for _, name := range releaseConfigFiles {
		from := filepath.Join(src, name)
		if err := copyRegularFileChecked(from, filepath.Join(dst, name)); err != nil {
			return err
		}
	}
	if mode == releaseConfigTemplate {
		marker := "THIS ARCHIVE CONTAINS A NON-RUNNABLE PUBLIC CONFIGURATION TEMPLATE.\n" +
			"It contains no validator identity and no secret. Generate a fresh, coherent network configuration,\n" +
			"replace every file under config/runtime, then validate it with --config-mode=runnable before use.\n" +
			"Never place private keys in this archive. Source profile: " + envName + "\n"
		if err := os.WriteFile(filepath.Join(staging, "CONFIGURATION_REQUIRED"), []byte(marker), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func validateReleaseConfiguration(src string, mode releaseConfigMode) error {
	if err := requireRegularDirectory(src); err != nil {
		return fmt.Errorf("release configuration directory is invalid: %w", err)
	}
	for _, name := range releaseConfigFiles {
		path := filepath.Join(src, name)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("release config %q is unavailable: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("release config %q must be a regular non-symlink file", name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read release config %q: %w", name, err)
		}
		if err := validatePublicReleaseJSON(name, data); err != nil {
			return err
		}
	}

	if _, err := dexconfig.LoadFromDir(src); err != nil {
		return fmt.Errorf("runtime config is invalid: %w", err)
	}
	frostConfig, err := dexconfig.LoadFrostConfig(filepath.Join(src, "frost_default.json"))
	if err != nil {
		return fmt.Errorf("FROST config is invalid: %w", err)
	}
	witnessConfig, err := dexconfig.LoadWitnessConfig(filepath.Join(src, "witness_default.json"))
	if err != nil {
		return fmt.Errorf("witness config is invalid: %w", err)
	}
	genesis, err := dexconfig.LoadGenesisConfig(filepath.Join(src, "genesis.json"))
	if err != nil {
		return fmt.Errorf("genesis config is invalid: %w", err)
	}
	manifest, err := dexconfig.LoadPeerManifest(filepath.Join(src, "peers.json"))
	if err != nil {
		return fmt.Errorf("peer manifest is invalid: %w", err)
	}
	if genesis.EffectiveChainID() != manifest.ChainID {
		return fmt.Errorf("genesis chain_id %d does not match peers chain_id %d", genesis.EffectiveChainID(), manifest.ChainID)
	}

	switch mode {
	case releaseConfigTemplate:
		notice := strings.ToUpper(strings.TrimSpace(manifest.Notice))
		if len(manifest.GenesisValidators) != 0 || len(manifest.BootstrapEndpoints) != 0 {
			return fmt.Errorf("template mode requires empty genesis_validators and bootstrap_endpoints; use --config-mode=runnable for a real network")
		}
		if !strings.Contains(notice, "EXAMPLE") && !strings.Contains(notice, "TEMPLATE") {
			return fmt.Errorf("template peers.json _notice must explicitly contain EXAMPLE or TEMPLATE")
		}
		return nil
	case releaseConfigRunnable:
		if err := dexconfig.ValidateFreshFrostGenesisStakePolicy(frostConfig, manifest, genesis); err != nil {
			return fmt.Errorf("runnable FROST genesis ranking is invalid: %w", err)
		}
		if err := validateRunnableManifest(manifest, frostConfig); err != nil {
			return err
		}
		return validateRunnableWitnessConfig(witnessConfig)
	default:
		return fmt.Errorf("unsupported release configuration mode %q", mode)
	}
}

func validateRunnableManifest(manifest *dexconfig.PeerManifest, frostConfig dexconfig.FrostConfig) error {
	if manifest == nil || len(manifest.GenesisValidators) == 0 {
		return fmt.Errorf("runnable mode requires at least one genesis validator")
	}
	if len(manifest.BootstrapEndpoints) == 0 {
		return fmt.Errorf("runnable mode requires at least one bootstrap endpoint")
	}

	blsOwners := make(map[string]string, len(manifest.GenesisValidators))
	minerCount := 0
	for index, validator := range manifest.GenesisValidators {
		secpBytes, err := decodePublicKeyHex(validator.SecpPublicKey)
		if err != nil {
			return fmt.Errorf("genesis_validators[%d].secp_public_key: %w", index, err)
		}
		secpPublicKey, err := btcec.ParsePubKey(secpBytes)
		if err != nil {
			return fmt.Errorf("genesis_validators[%d].secp_public_key: %w", index, err)
		}
		canonicalSecp := secpPublicKey.SerializeCompressed()
		derivedAddress, err := utils.AddressFromSecp256k1PubKey(canonicalSecp)
		if err != nil {
			return fmt.Errorf("genesis_validators[%d] derive address: %w", index, err)
		}
		if strings.TrimSpace(validator.Address) != derivedAddress {
			return fmt.Errorf("genesis_validators[%d] address does not match secp_public_key", index)
		}
		if !utils.IsDexAddress(strings.TrimSpace(validator.RewardAddress)) {
			return fmt.Errorf("genesis_validators[%d].reward_address is not a canonical DEX address", index)
		}

		if strings.TrimSpace(validator.BLSPublicKey) != "" {
			blsBytes, err := decodePublicKeyHex(validator.BLSPublicKey)
			if err != nil {
				return fmt.Errorf("genesis_validators[%d].bls_public_key: %w", index, err)
			}
			blsPoint, err := utils.DeserializePublicKey(blsBytes)
			if err != nil {
				return fmt.Errorf("genesis_validators[%d].bls_public_key: %w", index, err)
			}
			canonicalBLS, err := utils.SerializePublicKey(blsPoint)
			if err != nil {
				return fmt.Errorf("genesis_validators[%d].bls_public_key: %w", index, err)
			}
			if !bytes.Equal(canonicalBLS, blsBytes) {
				return fmt.Errorf("genesis_validators[%d].bls_public_key is not canonically encoded", index)
			}
			ownerKey := hex.EncodeToString(canonicalBLS)
			if owner, exists := blsOwners[ownerKey]; exists && owner != derivedAddress {
				return fmt.Errorf("genesis validators %s and %s share one BLS public key", owner, derivedAddress)
			}
			blsOwners[ownerKey] = derivedAddress
		}
		if validator.IsMiner {
			minerCount++
		}
	}
	if minerCount == 0 {
		return fmt.Errorf("runnable mode requires at least one genesis miner")
	}
	if frostConfig.Enabled {
		if frostConfig.Vault.DefaultK < 1 {
			return fmt.Errorf("enabled FROST config requires vault.defaultK >= 1")
		}
		if minerCount < frostConfig.Vault.DefaultK {
			return fmt.Errorf("enabled FROST config requires %d miners, manifest has %d", frostConfig.Vault.DefaultK, minerCount)
		}
		if btc, exists := frostConfig.Chains["btc"]; !exists || strings.TrimSpace(btc.BTCNetwork) == "" {
			return fmt.Errorf("enabled FROST config requires chains.btc.btcNetwork")
		}
		candidateCount := minerCount
		if frostConfig.Committee.TopN > 0 && candidateCount > frostConfig.Committee.TopN {
			candidateCount = frostConfig.Committee.TopN
		}
		for _, chainName := range []string{"btc", "eth", "bnb", "base", "arb", "trx"} {
			chainConfig, exists := frostConfig.Chains[chainName]
			if !exists {
				return fmt.Errorf("enabled FROST config requires chains.%s", chainName)
			}
			groupPubkeys, err := dexconfig.DecodeInitialFrostGroupPubkeys(chainName, chainConfig)
			if err != nil {
				return fmt.Errorf("enabled FROST config chains.%s standalone ceremony result is invalid: %w", chainName, err)
			}
			if len(groupPubkeys) == 0 {
				return fmt.Errorf("runnable release requires a completed standalone FROST ceremony for chains.%s", chainName)
			}
			                                                                     
			                                                                
			requiredCandidates := frostConfig.Vault.DefaultK
			if candidateCount < requiredCandidates {
				return fmt.Errorf("enabled FROST config chains.%s requires %d ceremony candidates, have %d after topN", chainName, requiredCandidates, candidateCount)
			}
		}
		if err := ceremony.ValidateConfiguredGenesisDeploymentEnvironment(frostConfig, manifest, manifest.ChainID, utils.VerifyECDSASignature); err != nil {
			return fmt.Errorf("runnable release signed FROST deployment proof is invalid: %w", err)
		}
	}
	return nil
}

func validateRunnableWitnessConfig(config dexconfig.WitnessConfig) error {
	if !config.Enabled {
		return nil
	}
	if config.ConsensusThreshold == 0 || config.ConsensusThreshold > 100 || config.AbstainThreshold > 100 {
		return fmt.Errorf("enabled witness config requires thresholds in 1..100")
	}
	if config.VotingPeriodBlocks == 0 || config.ChallengePeriodBlocks == 0 || config.ArbitrationPeriodBlocks == 0 || config.UnstakeLockBlocks == 0 || config.RetryIntervalBlocks == 0 {
		return fmt.Errorf("enabled witness config requires positive voting/challenge/arbitration/unstake/retry periods")
	}
	if config.InitialWitnessCount == 0 || config.MinSelectedWitnessCount == 0 || config.MinSelectedWitnessCount > config.InitialWitnessCount || config.ExpandMultiplier == 0 {
		return fmt.Errorf("enabled witness config has invalid witness selection counts")
	}
	if config.SelectionBeaconDepth == 0 || config.BeaconMixBlocks == 0 {
		return fmt.Errorf("enabled witness config requires positive beacon parameters")
	}
	return nil
}

func decodePublicKeyHex(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "0x"), "0X")
	if raw == "" {
		return nil, fmt.Errorf("public key is empty")
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	if len(decoded) == 0 {
		return nil, fmt.Errorf("public key is empty")
	}
	return decoded, nil
}

func validatePublicReleaseJSON(name string, data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	forbidden := map[string]struct{}{
		"privatekey":       {},
		"frostwrapkey":     {},
		"mnemonic":         {},
		"seed":             {},
		"seedphrase":       {},
		"password":         {},
		"passphrase":       {},
		"secret":           {},
		"secretkey":        {},
		"apitoken":         {},
		"accesstoken":      {},
		"githubtoken":      {},
		"telegrambottoken": {},
		"telegramchatid":   {},
	}
	var walk func(any) error
	walk = func(current any) error {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(key))
				if _, blocked := forbidden[normalized]; blocked {
					return fmt.Errorf("%s contains forbidden secret field %q", name, key)
				}
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range typed {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(value); err != nil {
		return err
	}
	if regexp.MustCompile(`(?i)-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----`).Match(data) {
		return fmt.Errorf("%s contains a private-key PEM block", name)
	}
	return nil
}

func stageFrontendBuildInputs(root, buildWorkspace string) error {
	iconDir := filepath.Join("shared", "assets", "top200-token-icons")
	for _, directory := range []string{"config", "shared", filepath.Dir(iconDir), iconDir} {
		if err := requireRegularDirectory(filepath.Join(root, directory)); err != nil {
			return err
		}
	}

	files := []string{
		"config/genesis.json",
		"shared/assetMetadata.mjs",
		"shared/assetMetadata.d.mts",
		"shared/top200TokenIcons.mjs",
		"shared/top200TokenIcons.d.mts",
	}
	icons, err := os.ReadDir(filepath.Join(root, iconDir))
	if err != nil {
		return err
	}
	for _, icon := range icons {
		if filepath.Ext(icon.Name()) == ".png" {
			files = append(files, filepath.Join(iconDir, icon.Name()))
		}
	}
	for _, name := range files {
		if err := copyRegularFileChecked(filepath.Join(root, name), filepath.Join(buildWorkspace, name)); err != nil {
			return fmt.Errorf("copy %s: %w", name, err)
		}
	}
	return nil
}

func buildFrontend(root, buildWorkspace string) string {
	dir, err := prepareCleanNPMProject(
		filepath.Join(root, "explorer"),
		filepath.Join(buildWorkspace, "explorer"),
		[]string{"package.json", "package-lock.json", "index.html", "style.css", "tsconfig.json", "vite.config.ts"},
		[]string{"public", "src"},
	)
	must(err, "prepare clean Explorer build workspace")
	npm := "npm"
	if runtime.GOOS == "windows" {
		npm = "npm.cmd"
	}
	runBuild(dir, npm, "ci", "--no-audit", "--no-fund")
	runBuild(dir, npm, "run", "build")
	dist := filepath.Join(dir, "dist")
	must(requireRegularDirectory(dist), "validate clean Explorer build output")
	return dist
}

func buildWallet(root, buildWorkspace string) string {
	dir, err := prepareCleanNPMProject(
		filepath.Join(root, "wallet"),
		filepath.Join(buildWorkspace, "wallet"),
		[]string{"build.js", "index.html", "manifest.json", "package.json", "package-lock.json", "vite.config.ts"},
		[]string{"background", "content", "icons", "lib", "scripts", "src"},
	)
	must(err, "prepare clean Wallet build workspace")
	npm := "npm"
	if runtime.GOOS == "windows" {
		npm = "npm.cmd"
	}
	runBuild(dir, npm, "ci", "--no-audit", "--no-fund")
	runBuild(dir, npm, "run", "build")
	dist := filepath.Join(dir, "dist")
	must(requireRegularDirectory(dist), "validate clean Wallet build output")
	return dist
}

func prepareCleanNPMProject(src, dst string, files, directories []string) (string, error) {
	if err := requireRegularDirectory(src); err != nil {
		return "", err
	}
	if err := os.RemoveAll(dst); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", err
	}
	for _, name := range files {
		if err := copyRegularFileChecked(filepath.Join(src, name), filepath.Join(dst, name)); err != nil {
			return "", fmt.Errorf("copy %s: %w", name, err)
		}
	}
	for _, name := range directories {
		if err := copyTreeChecked(filepath.Join(src, name), filepath.Join(dst, name)); err != nil {
			return "", fmt.Errorf("copy %s: %w", name, err)
		}
	}
	return dst, nil
}

func requireRegularDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s must be a real directory, not a symlink", path)
	}
	return nil
}

func runBuild(dir, name string, args ...string) {
	runEnv(dir, scrubBuildEnvironment(os.Environ()), name, args...)
}

func runEnv(dir string, env []string, name string, args ...string) {
	fmt.Printf("[%s] %s %s\n", dir, name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	must(cmd.Run(), name+" "+strings.Join(args, " "))
}

func copyFile(src, dst string) {
	must(copyRegularFileChecked(src, dst), "copy "+src)
}

func copyTree(src, dst string) {
	must(copyTreeChecked(src, dst), "copyTree "+src)
}

var releaseLegalFiles = []string{
	"LICENSE",
	"NOTICE",
	"shared/assets/top200-token-icons/TRUSTWALLET-LICENSE",
}

func stageReleaseLegalFiles(root, staging string) error {
	for _, name := range releaseLegalFiles {
		if err := validateRegularSourceFile(filepath.Join(root, name)); err != nil {
			return fmt.Errorf("validate release legal file %s: %w", name, err)
		}
	}
	for _, name := range releaseLegalFiles {
		if err := copyRegularFileChecked(filepath.Join(root, name), filepath.Join(staging, name)); err != nil {
			return fmt.Errorf("stage release legal file %s: %w", name, err)
		}
	}
	return nil
}

func validateRegularSourceFile(src string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("source must be a regular non-symlink file: %s", src)
	}
	return nil
}

func copyRegularFileChecked(src, dst string) error {
	if err := validateRegularSourceFile(src); err != nil {
		return err
	}
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if dstInfo, err := os.Lstat(dst); err == nil && dstInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("destination must not be a symlink: %s", dst)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	openedInfo, err := in.Stat()
	if err != nil {
		_ = in.Close()
		return err
	}
	if !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() {
		_ = in.Close()
		return fmt.Errorf("source changed while opening: %s", src)
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		_ = in.Close()
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeOutErr := out.Close()
	closeInErr := in.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeOutErr != nil {
		return closeOutErr
	}
	return closeInErr
}

func copyTreeChecked(src, dst string) error {
	if err := requireRegularDirectory(src); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is forbidden in release input: %s", p)
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular file is forbidden in release input: %s", p)
		}
		return copyRegularFileChecked(p, target)
	})
}

func zipTree(srcDir, zipPath, prefix string) error {
	f, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)

	walkErr := filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(srcDir, p)
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink is forbidden in release staging: %s", p)
		}
		if !d.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular file is forbidden in release staging: %s", p)
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(filepath.Join(prefix, rel))
		h.Method = zip.Deflate
		if d.IsDir() {
			h.Name += "/"
			_, err := zw.CreateHeader(h)
			return err
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(w, in)
		closeErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	zipErr := zw.Close()
	fileErr := f.Close()
	if walkErr != nil {
		return walkErr
	}
	if zipErr != nil {
		return zipErr
	}
	return fileErr
}

func mkdir(dir string) { must(os.MkdirAll(dir, 0o755), "mkdir "+dir) }

func must(err error, what string) {
	if err != nil {
		fatal(what + ": " + err.Error())
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
