package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"

	"dex/config"
	"dex/frost/ceremony"
	"dex/utils"
)

                                                                           
                                                                             
                                                              
func runKeyReuse(args []string) error {
	fs := flag.NewFlagSet("key-reuse", flag.ContinueOnError)
	var action, source, peers, manifest, deployments, rpc, artifacts, approvals, bundle, nodeID, out string
	var identityFD, wrapFD int
	fs.StringVar(&action, "action", "verify", "verify, attest, or assemble")
	fs.StringVar(&source, "source-frost-config", "", "original signed configuration, or an already verified key-reuse configuration")
	fs.StringVar(&peers, "peers", "", "original peers.json")
	fs.StringVar(&manifest, "manifest", "", "original ceremony manifest")
	fs.StringVar(&deployments, "deployments", "", "original deployment receipts")
	fs.StringVar(&rpc, "rpc-config", "", "online verification RPC configuration")
	fs.StringVar(&artifacts, "solidity-artifacts-dir", "", "trusted original contract artifacts")
	fs.StringVar(&approvals, "deployment-approvals-dir", "", "complete new participant approvals")
	fs.StringVar(&bundle, "bundle", "", "this participant's original sealed bundle")
	fs.StringVar(&nodeID, "node-id", "", "participant DEX address")
	fs.IntVar(&identityFD, "identity-key-fd", -1, "inherited identity key FD >= 3")
	fs.IntVar(&wrapFD, "wrap-key-fd", -1, "inherited wrap key FD >= 3")
	fs.StringVar(&out, "out", "", "new approval or assembled configuration")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if action != "verify" && action != "attest" && action != "assemble" {
		return errors.New("invalid key-reuse action")
	}
	if source == "" || peers == "" || manifest == "" || deployments == "" || rpc == "" || artifacts == "" {
		return errors.New("key-reuse requires all original public inputs and online RPC verification")
	}
	if action == "attest" && (identityFD < 3 || wrapFD < 3 || identityFD == wrapFD || nodeID == "" || bundle == "") {
		return errors.New("key-reuse attest requires bundle, node-id, and distinct identity/wrap FDs >= 3")
	}
	if action == "assemble" && approvals == "" {
		return errors.New("key-reuse assemble requires complete participant approvals")
	}
	if action != "verify" {
		if out == "" {
			return errors.New("key-reuse requires --out")
		}
		var err error
		out, err = checkedNewPath(out)
		if err != nil {
			return err
		}
	}
	prepared, err := prepareKeyReuse(source, peers, manifest, deployments, rpc, artifacts)
	if err != nil {
		return err
	}
	if action == "verify" {
		fmt.Println("Original custody and all five live Vaults verified; no configuration installed.")
		return nil
	}
	if action == "attest" {
		wrap, err := readWrapKey(wrapFD)
		if err != nil {
			return err
		}
		defer wipe(wrap)
		localManifest, err := ceremony.VerifyNodeBundle(bundle, nodeID, wrap, utils.VerifyECDSASignature)
		if err != nil {
			return err
		}
		a, err := ceremony.ManifestDigest(localManifest, utils.VerifyECDSASignature)
		if err != nil {
			return err
		}
		b, err := ceremony.ManifestDigest(prepared.manifest, utils.VerifyECDSASignature)
		if err != nil || !bytes.Equal(a, b) {
			return errors.New("local sealed bundle belongs to a different ceremony")
		}
		identity, raw, err := readIdentityKey(identityFD)
		if err != nil {
			return err
		}
		defer identity.Zero()
		defer wipe(raw)
		if err := verifyLocalIdentity(&prepared.manifest.Plan, nodeID, identity); err != nil {
			return err
		}
		digest, err := ceremony.GenesisDeploymentStatementDigest(prepared.statement)
		if err != nil {
			return err
		}
		approval := &ceremony.GenesisDeploymentApproval{Version: ceremony.GenesisDeploymentVersion, StatementDigest: digest, ParticipantID: nodeID}
		digest, err = ceremony.GenesisDeploymentApprovalDigest(approval)
		if err != nil {
			return err
		}
		approval.Signature, err = signIdentityDigest(identity, digest)
		if err != nil {
			return err
		}
		return writeExclusiveJSON(out, approval, 0o644)
	}
	all, err := loadGenesisDeploymentApprovals(approvals, len(prepared.manifest.Plan.Participants))
	if err != nil {
		return err
	}
	proof, err := ceremony.AssembleGenesisDeploymentManifest(prepared.statement, all, prepared.manifest.Plan.Participants, utils.VerifyECDSASignature)
	if err != nil {
		return err
	}
	if err := ceremony.ValidateGenesisDeploymentManifest(proof, prepared.finalConfig, prepared.manifest, nil, prepared.manifest.Plan.NetworkChainID, prepared.manifest.Plan.PeerManifestDigest, utils.VerifyECDSASignature); err != nil {
		return err
	}
	prepared.finalConfig.GenesisDeploymentManifest, err = json.Marshal(proof)
	if err != nil {
		return err
	}
	return writeExclusiveJSON(out, &prepared.finalConfig, 0o644)
}

func prepareKeyReuse(sourcePath, peersPath, manifestPath, deploymentsDir, rpcPath, artifactsDir string) (*preparedGenesisDeployments, error) {
	raw, err := readRegularFile(sourcePath, maxLargeJSONBytes)
	if err != nil {
		return nil, err
	}
	peers, err := loadPeerManifestStrict(peersPath)
	if err != nil {
		return nil, err
	}
	var existing config.FrostConfig
	if err := strictJSON(raw, &existing); err != nil {
		return nil, err
	}
	if len(existing.GenesisKeyReuse) != 0 {
		                                                                        
		if err := ceremony.ValidateConfiguredGenesisDeploymentEnvironment(existing, peers, peers.ChainID, utils.VerifyECDSASignature); err != nil {
			return nil, err
		}
		var reuse ceremony.GenesisKeyReuse
		if err := strictJSON(existing.GenesisKeyReuse, &reuse); err != nil {
			return nil, err
		}
		raw = reuse.SourceConfig
	}
	target, err := ceremony.PrepareGenesisKeyReuse(raw)
	if err != nil {
		return nil, err
	}
	manifestRaw, err := readRegularFile(manifestPath, maxLargeJSONBytes)
	if err != nil {
		return nil, err
	}
	manifest, err := ceremony.StrictDecodeManifest(manifestRaw)
	if err != nil {
		return nil, err
	}
	peerDigest, err := ceremony.ComputePeerManifestDigest(peers)
	if err != nil {
		return nil, err
	}
	if err := ceremony.ValidateGenesisKeyReuse(target, nil, manifest, nil, peers.ChainID, peerDigest, utils.VerifyECDSASignature); err != nil {
		return nil, err
	}
	if err := requireSixChains(target.Chains); err != nil {
		return nil, err
	}
	deployments, err := loadDeploymentReceipts(deploymentsDir, manifest, target)
	if err != nil {
		return nil, err
	}
	if err := requireUnifiedVaultReceipts(deployments, peers.ChainID); err != nil {
		return nil, err
	}
	rpc, err := loadRPCConfig(rpcPath)
	if err != nil {
		return nil, err
	}
	artifacts, err := loadExpectedContractArtifacts(artifactsDir)
	if err != nil {
		return nil, err
	}
	                                                                        
	                                                                        
	                                                                        
	                                                                      
	var original config.FrostConfig
	if err := json.Unmarshal(raw, &original); err != nil {
		return nil, err
	}
	originalProof, err := ceremony.StrictDecodeGenesisDeploymentManifest(original.GenesisDeploymentManifest)
	if err != nil {
		return nil, err
	}
	for i := range deployments {
		d := &deployments[i]
		for _, v := range originalProof.Statement.Vaults {
			if v.Chain == d.chain && v.VaultID == d.vaultID {
				if d.receipt.BlockNumber != v.DeploymentBlockNumber {
					return nil, errors.New("receipt height differs from original signed deployment")
				}
				d.blockNumber, d.blockHash = v.DeploymentBlockNumber, "0x"+hex.EncodeToString(v.DeploymentBlockHash)
				d.finalityPolicy, d.minConfirmations = v.FinalityPolicy, v.MinimumConfirmations
				d.finalityBlockNumber, d.finalityBlockHash = v.FinalityBlockNumber, "0x"+hex.EncodeToString(v.FinalityBlockHash)
			}
		}
	}
	statement, err := buildGenesisDeploymentStatement(manifest, target, deployments, artifacts)
	if err != nil {
		return nil, err
	}
	if err := ceremony.ValidateGenesisKeyReuse(target, &ceremony.SignedGenesisDeploymentManifest{Statement: *statement}, manifest, nil, peers.ChainID, peerDigest, utils.VerifyECDSASignature); err != nil {
		return nil, err
	}
	if err := verifyReusedVaultsOnline(rpc, deployments, target, manifest.Plan.Epoch, artifacts); err != nil {
		return nil, err
	}
	return &preparedGenesisDeployments{manifest: manifest, deployments: deployments, artifacts: artifacts, finalConfig: target, statement: statement}, nil
}

func verifyReusedVaultsOnline(rpc *onlineRPCConfig, deployments []verifiedDeployment, cfg config.FrostConfig, epoch uint64, artifacts map[string]expectedContractArtifact) error {
	for _, d := range deployments {
		chainRPC, ok := rpc.Chains[d.chain]
		if !ok || chainRPC.FinalityPolicy != d.finalityPolicy || chainRPC.MinConfirmations != d.minConfirmations {
			return fmt.Errorf("%s: current RPC finality policy differs from original signed policy", d.chain)
		}
		client, err := newJSONRPCClient(chainRPC.JSONRPC)
		if err != nil {
			return err
		}
		var rawID string
		if err := client.call("eth_chainId", []any{}, &rawID); err != nil {
			return fmt.Errorf("%s: %w", d.chain, err)
		}
		chainID, err := parseRPCQuantity(rawID)
		if err != nil || chainID != cfg.Chains[d.chain].ChainID {
			return fmt.Errorf("%s: RPC chain identity mismatch", d.chain)
		}
		artifact, ok := artifacts[d.receipt.Contract]
		if !ok {
			return fmt.Errorf("%s: missing trusted contract artifact", d.chain)
		}
		var height uint64
		tag := "latest"
		if d.chain == "trx" {
			head, err := fetchTronSolidHead(chainRPC.SolidityNode)
			if err != nil {
				return err
			}
			height = head.blockNumber
		} else {
			policyTag := "finalized"
			if d.finalityPolicy == ceremony.GenesisFinalityBNBConfirmations {
				policyTag = "latest"
			}
			head, err := fetchRPCBlockHeader(client, policyTag)
			if err != nil {
				return fmt.Errorf("%s: finality head: %w", d.chain, err)
			}
			height = head.blockNumber
			if d.finalityPolicy == ceremony.GenesisFinalityBNBConfirmations {
				if height < d.minConfirmations {
					return errors.New("BNB head below required confirmations")
				}
				height -= d.minConfirmations - 1
			}
			tag = fmt.Sprintf("0x%x", height)
		}
		if height < d.finalityBlockNumber {
			return fmt.Errorf("%s: current finality does not cover signed deployment", d.chain)
		}
		if err := verifyDeploymentStateAtBlock(client, d, epoch, artifact, tag); err != nil {
			return fmt.Errorf("%s: current finalized Vault: %w", d.chain, err)
		}
		if tag != "latest" {
			if err := verifyDeploymentStateAtBlock(client, d, epoch, artifact, "latest"); err != nil {
				return fmt.Errorf("%s: latest Vault: %w", d.chain, err)
			}
		}
		fmt.Printf("%s: original creation proof retained; current code, authority and epoch verified at height %d\n", d.chain, height)
	}
	return nil
}
