package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type expectedContractArtifact struct {
	contractName     string
	creationCode     []byte
	runtimeCode      []byte
	immutableRanges  []byteRange
	solcVersion      string
	compilerSettings []byte
	vaultImmutables  map[string][]byteRange
	unifiedVault     bool
}

type byteRange struct {
	start  int
	length int
}

type hardhatArtifact struct {
	Format                 string          `json:"_format"`
	ContractName           string          `json:"contractName"`
	SourceName             string          `json:"sourceName"`
	ABI                    json.RawMessage `json:"abi"`
	Bytecode               string          `json:"bytecode"`
	DeployedBytecode       string          `json:"deployedBytecode"`
	LinkReferences         json.RawMessage `json:"linkReferences"`
	DeployedLinkReferences json.RawMessage `json:"deployedLinkReferences"`
}

type hardhatDebug struct {
	Format    string `json:"_format"`
	BuildInfo string `json:"buildInfo"`
}

type byteRangeJSON struct {
	Start  int `json:"start"`
	Length int `json:"length"`
}

type hardhatBuildInfo struct {
	SolcVersion string `json:"solcVersion"`
	Input       struct {
		Settings json.RawMessage `json:"settings"`
	} `json:"input"`
	Output struct {
		Sources map[string]struct {
			AST json.RawMessage `json:"ast"`
		} `json:"sources"`
		Contracts map[string]map[string]struct {
			EVM struct {
				Bytecode struct {
					Object         string          `json:"object"`
					LinkReferences json.RawMessage `json:"linkReferences"`
				} `json:"bytecode"`
				DeployedBytecode struct {
					Object              string                     `json:"object"`
					ImmutableReferences map[string][]byteRangeJSON `json:"immutableReferences"`
					LinkReferences      json.RawMessage            `json:"linkReferences"`
				} `json:"deployedBytecode"`
			} `json:"evm"`
		} `json:"contracts"`
	} `json:"output"`
}

func loadExpectedContractArtifacts(root string) (map[string]expectedContractArtifact, error) {
	rootPath, err := checkedExistingPath(root, true)
	if err != nil {
		return nil, err
	}
	targets := []struct {
		name     string
		source   string
		relative string
	}{
		{"SchnorrExampleV3", "contracts/SchnorrVaultV3.sol", filepath.Join("contracts", "SchnorrVaultV3.sol", "SchnorrExampleV3.json")},
		{"SchnorrExampleTronV3", "contracts/SchnorrVaultV3.sol", filepath.Join("contracts", "SchnorrVaultV3.sol", "SchnorrExampleTronV3.json")},
	}
	result := make(map[string]expectedContractArtifact, len(targets))
	for _, target := range targets {
		artifactPath := filepath.Join(rootPath, target.relative)
		                                                             
		                                                                             
		if _, err := os.Lstat(artifactPath); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		artifactData, err := readRegularFile(artifactPath, maxLargeJSONBytes)
		if err != nil {
			return nil, err
		}
		var artifact hardhatArtifact
		if err := strictJSON(artifactData, &artifact); err != nil {
			return nil, fmt.Errorf("decode Solidity artifact %s: %w", target.name, err)
		}
		if artifact.Format != "hh-sol-artifact-1" || artifact.ContractName != target.name || artifact.SourceName != target.source {
			return nil, fmt.Errorf("unexpected Solidity artifact identity for %s", target.name)
		}
		if !isEmptyJSONObject(artifact.LinkReferences) || !isEmptyJSONObject(artifact.DeployedLinkReferences) {
			return nil, fmt.Errorf("linked libraries are forbidden in trusted artifact %s", target.name)
		}
		creationCode, err := decodeHexData(artifact.Bytecode)
		if err != nil || len(creationCode) == 0 {
			return nil, fmt.Errorf("invalid creation bytecode for %s", target.name)
		}
		runtimeCode, err := decodeHexData(artifact.DeployedBytecode)
		if err != nil || len(runtimeCode) == 0 {
			return nil, fmt.Errorf("invalid runtime bytecode for %s", target.name)
		}
		debugPath := strings.TrimSuffix(artifactPath, ".json") + ".dbg.json"
		debugData, err := readRegularFile(debugPath, maxSmallJSONBytes)
		if err != nil {
			return nil, err
		}
		var debug hardhatDebug
		if err := strictJSON(debugData, &debug); err != nil || debug.Format != "hh-sol-dbg-1" || debug.BuildInfo == "" {
			return nil, fmt.Errorf("invalid Hardhat debug metadata for %s", target.name)
		}
		buildRelative := filepath.Clean(strings.ReplaceAll(debug.BuildInfo, "\\", string(filepath.Separator)))
		buildPath, err := filepath.Abs(filepath.Join(filepath.Dir(debugPath), buildRelative))
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(rootPath, buildPath)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("Hardhat build-info for %s escapes the artifacts directory", target.name)
		}
		buildData, err := readRegularFile(buildPath, maxLargeJSONBytes)
		if err != nil {
			return nil, err
		}
		if err := validateJSONStructure(buildData); err != nil {
			return nil, fmt.Errorf("decode Hardhat build-info for %s: %w", target.name, err)
		}
		var build hardhatBuildInfo
		if err := json.Unmarshal(buildData, &build); err != nil {
			return nil, fmt.Errorf("decode Hardhat build-info for %s: %w", target.name, err)
		}
		contractOutput, ok := build.Output.Contracts[artifact.SourceName][target.name]
		if !ok || build.SolcVersion == "" {
			return nil, fmt.Errorf("Hardhat build-info does not contain %s", target.name)
		}
		objectCreationCode, err := decodeHexData("0x" + contractOutput.EVM.Bytecode.Object)
		if err != nil || !bytes.Equal(objectCreationCode, creationCode) {
			return nil, fmt.Errorf("artifact/build-info creation mismatch for %s", target.name)
		}
		objectCode, err := decodeHexData("0x" + contractOutput.EVM.DeployedBytecode.Object)
		if err != nil || !bytes.Equal(objectCode, runtimeCode) {
			return nil, fmt.Errorf("artifact/build-info runtime mismatch for %s", target.name)
		}
		if !isEmptyJSONObject(contractOutput.EVM.Bytecode.LinkReferences) || !isEmptyJSONObject(contractOutput.EVM.DeployedBytecode.LinkReferences) {
			return nil, fmt.Errorf("build-info contains linked libraries for %s", target.name)
		}
		settings, err := canonicalJSON(build.Input.Settings)
		if err != nil {
			return nil, fmt.Errorf("invalid compiler settings for %s", target.name)
		}
		expected := expectedContractArtifact{
			contractName: target.name, creationCode: creationCode, runtimeCode: runtimeCode,
			solcVersion: build.SolcVersion, compilerSettings: settings,
		}
		for _, ranges := range contractOutput.EVM.DeployedBytecode.ImmutableReferences {
			for _, item := range ranges {
				if item.Start < 0 || item.Length <= 0 || item.Start+item.Length > len(runtimeCode) {
					return nil, fmt.Errorf("invalid immutable range in %s", target.name)
				}
				expected.immutableRanges = append(expected.immutableRanges, byteRange{start: item.Start, length: item.Length})
			}
		}
		sort.Slice(expected.immutableRanges, func(i, j int) bool { return expected.immutableRanges[i].start < expected.immutableRanges[j].start })
		for i := 1; i < len(expected.immutableRanges); i++ {
			if expected.immutableRanges[i-1].start+expected.immutableRanges[i-1].length > expected.immutableRanges[i].start {
				return nil, fmt.Errorf("overlapping immutable ranges in %s", target.name)
			}
		}
		if err := identifyVaultImmutables(&expected, artifact.ABI, build, contractOutput.EVM.DeployedBytecode.ImmutableReferences); err != nil {
			return nil, fmt.Errorf("%s: %w", target.name, err)
		}
		result[target.name] = expected
	}
	if len(result) == 0 {
		return nil, errors.New("no supported Vault artifact pair")
	}
	for _, pair := range [][2]string{{"SchnorrExampleV3", "SchnorrExampleTronV3"}} {
		_, evmPresent := result[pair[0]]
		_, tronPresent := result[pair[1]]
		if evmPresent != tronPresent {
			return nil, fmt.Errorf("incomplete trusted Vault artifact pair: %s/%s", pair[0], pair[1])
		}
	}
	return result, nil
}

func runtimeMatchesArtifact(actual, expected []byte, immutableRanges []byteRange, immutableWord []byte) bool {
	if len(actual) != len(expected) {
		return false
	}
	patched := bytes.Clone(expected)
	for _, item := range immutableRanges {
		if item.start < 0 || item.length <= 0 || item.start+item.length > len(patched) || item.length != len(immutableWord) {
			return false
		}
		copy(patched[item.start:item.start+item.length], immutableWord)
	}
	if len(immutableRanges) == 0 && len(immutableWord) != 0 {
		return false
	}
	return bytes.Equal(actual, patched)
}

func canonicalJSON(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("JSON is empty")
	}
	if err := validateJSONStructure(data); err != nil {
		return nil, err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("trailing JSON")
		}
		return nil, err
	}
	return json.Marshal(value)
}

func isEmptyJSONObject(data []byte) bool {
	canonical, err := canonicalJSON(data)
	return err == nil && bytes.Equal(canonical, []byte("{}"))
}
