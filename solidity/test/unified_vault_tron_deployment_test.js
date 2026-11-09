const { expect } = require("chai");
const fs = require("fs");
const os = require("os");
const path = require("path");
const { execFileSync } = require("child_process");
const hre = require("hardhat");
const {G,guardianEnvironment}=require("./helpers/guardian-deployment");
const { TronWeb } = require("tronweb");
const { loadVaultArtifact, expectedVaultRuntime, vaultIdentity, loadVaultJournal } = require("../scripts/vault_deployment");
const {
  parseOptions, run, deployTronVault, previewTronEnergyPlan,
  ZERO_TRON_ADDRESS, TRON_MAINNET_CHAIN_ID, TRON_MAINNET_USDT,
} = require("../scripts/deploy_schnorr_vault_tron");

describe("Unified TRON Vault deployment (offline)", function () {
  this.timeout(30000);
  let artifact, v2Artifact;
  const directories = [];
  before(async function () { artifact = await loadVaultArtifact(hre, "SchnorrExampleTronV3"); v2Artifact = await loadVaultArtifact(hre, "SchnorrExampleTronV3"); });
  afterEach(function () {
    for (const directory of directories.splice(0)) {
      const target = path.resolve(directory);
      if (path.dirname(target) !== path.resolve(os.tmpdir()) || !path.basename(target).startsWith("dex-unified-tron-")) throw new Error("unexpected test cleanup target");
      fs.rmSync(target, { recursive: true, force: true });
    }
  });

  function fixture(protocol = 3) {
    const selectedArtifact = protocol === 3 ? v2Artifact : artifact;
    const directory = fs.mkdtempSync(path.join(os.tmpdir(), "dex-unified-tron-"));
    directories.push(directory);
    const options = {
      authorityProtocol: protocol, network: "tron", chainId: 99n, dexChainId: 51025n, epoch: 7n, vaultId: 0n,
      px: 1n, py: 2n, rawPy: 2n, guardianPx:G.x, guardianPy:G.y, nonstandardToken: ZERO_TRON_ADDRESS,
      jsonRPCURL: "http://json.invalid/jsonrpc", fullHost: "http://native.invalid",
      privateKey: "1".padStart(64, "0"), feeLimit: 2_000_000_000,
      originEnergyLimit: 10_000_000, timeoutMs: 30000, output: path.join(directory, "vault.json"),
    };
    const state = {
      nowMs: Date.now(), builds: 0, signs: 0, broadcasts: 0, constructions: [], pending: [], onchain: null,
      funded: true, receiptResult: "SUCCESS", domain: options.dexChainId, token: options.nonstandardToken,
      runtime: expectedVaultRuntime(selectedArtifact, options.dexChainId, options.nonstandardToken),
      mutateUnsigned: null, mutateSigned: null, broadcastError: null, rpcCalls: 0,
      estimateEnergy: 4_800_000, estimateError: false, estimateRequests: [], delegatedEnergy: 10_000_000,
      balanceSun: 500_000_000, energyFee: 420, transactionFee: 1000, rentalSigns: 0, rentalBroadcasts: 0, payments: new Map(),
      delegationDelayMs: 0, paymentBroadcastError: null, dropEnergyAtDeploymentBuild: false,
      rentalBandwidthSun: 0,
      raiseBandwidthOnDelivery: false,
    };
    const tw = new TronWeb({ fullHost: options.fullHost, privateKey: options.privateKey });
    const build = tw.transactionBuilder.createSmartContract.bind(tw.transactionBuilder);
    const sign = tw.trx.sign.bind(tw.trx);
    tw.transactionBuilder.createSmartContract = async (...args) => {
      state.builds++;
      if (state.dropEnergyAtDeploymentBuild) state.delegatedEnergy = 0;
      const unsigned = await build(...args);
      if (state.mutateUnsigned) state.mutateUnsigned(unsigned);
      return unsigned;
    };
    tw.trx.sign = async (...args) => {
      if (args[0]?.raw_data?.contract?.[0]?.type === "TransferContract") state.rentalSigns++;
      else state.signs++;
      const signed = await sign(...args);
      if (state.mutateSigned) state.mutateSigned(signed);
      return signed;
    };
    tw.trx.sendRawTransaction = async signed => {
      if (signed.raw_data.contract[0].type === "TransferContract") {
        expect(fs.existsSync(path.join(options.output + ".energy", "plan.json"))).to.equal(true);
        state.rentalBroadcasts++;
        const onchain = JSON.parse(JSON.stringify(signed));
        delete onchain.visible; onchain.ret = [{ contractRet: "SUCCESS" }];
        state.payments.set(signed.txID, { onchain, deliversAt: state.nowMs + state.delegationDelayMs, delivered: false });
        state.balanceSun -= 6_000_000 + state.rentalBandwidthSun;
        if (state.paymentBroadcastError) throw new Error(state.paymentBroadcastError);
        return { result: true, txid: signed.txID };
      }
                                                                           
      state.pending.push(JSON.parse(fs.readFileSync(options.output, "utf8")));
      expect(state.pending.at(-1).status).to.equal("pending");
      state.broadcasts++;
      state.onchain = JSON.parse(JSON.stringify(signed));
      delete state.onchain.visible;
      state.onchain.ret = [{ contractRet: "SUCCESS" }];
      if (state.broadcastError) throw new Error(state.broadcastError);
      return { result: true, txid: signed.txID };
    };
    tw.trx.getContractInfo = async () => ({ runtimecode: state.runtime.slice(2) });
    tw.contract = async () => ({
      G: () => ({call:async()=>[G.x,G.y]}),
      P: () => ({ call: async () => [1n, 2n] }),
      authorityNonce: () => ({ call: async () => options.epoch }),
      dexChainId: () => ({ call: async () => state.domain }),
      AUTHORITY_PROTOCOL: () => ({ call: async () => state.authorityProtocol ?? protocol }),
      MAX_HANDOVERS_PER_CALL: () => ({ call: async () => 8 }),
      nonstandardToken: () => ({ call: async () => state.token }),
    });
    const genesisID = "0".repeat(56) + options.chainId.toString(16).padStart(8, "0");
    const anchorID = "0000000000000064" + "ab".repeat(24);
    const block = number => ({ blockID: number === 0 ? genesisID : anchorID, block_header: { raw_data: number === 0 ? {} : { number, timestamp: state.nowMs - 3000 } } });
    const deps = {
      artifact: selectedArtifact, now: () => state.nowMs,
      createTronWeb: config => { state.constructions.push(config); return tw; },
      jsonRPC: async (_url, method, params) => {
        state.rpcCalls++;
        if (method === "eth_chainId") return "0x63";
        if (method === "eth_blockNumber") return "0x64";
        if (method === "eth_getBlockByNumber") return { number: params[0], hash: `0x${params[0] === "0x0" ? genesisID : anchorID}` };
        throw new Error(`unexpected JSON RPC ${method}`);
      },
      nativeRPC: async (_host, route, payload) => {
        state.rpcCalls++;
        if (route === "wallet/getnowblock") return block(100);
        if (route === "wallet/getnodeinfo") return { block: `Num:100,ID:${anchorID}`, solidityBlock: `Num:100,ID:${anchorID}`, configNodeInfo: { codeVersion: "4.8.2" } };
        if (route === "wallet/getblockbynum") return block(payload.num);
        if (route === "wallet/getchainparameters") return { chainParameter: [
          { key: "getMaintenanceTimeInterval", value: 21_600_000 }, { key: "getEnergyFee", value: state.energyFee }, { key: "getTransactionFee", value: state.transactionFee },
        ] };
        if (route === "wallet/estimateenergy") {
          state.estimateRequests.push(payload);
          if (state.estimateError) throw new Error("creation estimation not supported");
          return { result: { result: true }, energy_required: state.estimateEnergy };
        }
        if (route === "wallet/getaccount") return { balance: state.funded ? state.balanceSun : 0 };
        if (route === "wallet/getaccountresource") {
          for (const payment of state.payments.values()) {
            if (!payment.delivered && state.nowMs >= payment.deliversAt) {
              payment.delivered = true; state.delegatedEnergy += 130_000;
              if (state.raiseBandwidthOnDelivery) state.transactionFee++;
            }
          }
          return state.funded ? { EnergyLimit: state.delegatedEnergy, freeNetLimit: 10000 } : { freeNetLimit: 100000 };
        }
        if (route === "wallet/gettransactionbyid") return state.payments.get(payload.value)?.onchain || state.onchain || {};
        if (route === "wallet/gettransactioninfobyid" && state.payments.has(payload.value)) return { id: payload.value, blockNumber: 99, receipt: { result: "SUCCESS" } };
        if (route === "wallet/gettransactioninfobyid") return {
          id: payload.value, blockNumber: 99, contract_address: state.onchain.contract_address,
          receipt: { result: state.receiptResult },
        };
        throw new Error(`unexpected native RPC ${route}`);
      },
      sleep: async ms => { state.nowMs += ms; },
    };
    return { options, state, tw, deps };
  }

  it("deploys and recovers explicit V3 TRON without another build or payment", async function () {
    const f = fixture(3), deployed = await run(f.options, f.deps);
    expect(deployed.authorityProtocol).to.equal(3); expect(deployed.contract).to.equal("SchnorrExampleTronV3");
    expect(f.state.onchain.raw_data.contract[0].parameter.value.new_contract.name).to.equal("SchnorrExampleTronV3");
    denyKey(f.options); expect(await run(f.options, f.deps)).to.deep.equal(deployed);
    expect(f.state.builds).to.equal(1); expect(f.state.broadcasts).to.equal(1);
  });
  it("recovers V3 TRON using its frozen artifact pair without reading private keys or sending", async function () {
    const f=fixture(), deployed=await run(f.options,f.deps);
    const frozen=path.join(path.dirname(f.options.output),"old frozen artifacts");
    require('../../dev/server5/prepare-unified-vaults').captureArtifacts(path.join(__dirname,'../artifacts'),frozen,['SchnorrExampleTronV3']);
    f.options.artifactsDir=frozen;denyKey(f.options);
    const deps={...f.deps};delete deps.artifact;
    expect(await run(f.options,deps)).to.deep.equal(deployed);
    expect(f.state.builds).to.equal(1);expect(f.state.signs).to.equal(1);expect(f.state.broadcasts).to.equal(1);
  });
  it("keeps V3 TRON pending when runtime-matched capability differs", async function () {
    const f = fixture(3); f.state.authorityProtocol = 1;
    await expect(run(f.options, f.deps)).to.be.rejectedWith("protocol/capabilities");
    expect(JSON.parse(fs.readFileSync(f.options.output)).status).to.equal("pending"); expect(f.state.broadcasts).to.equal(1);
  });
  function denyKey(options) {
    Object.defineProperty(options, "privateKey", { configurable: true, get() { throw new Error("recovery read privateKey"); } });
  }

  it("requires an explicit nonzero uint64 DEX ID and uses an isolated stable output", function () {
    const env = { VAULT_CHAIN_ID: "99", VAULT_DEX_CHAIN_ID: "51025", VAULT_EPOCH: "7", VAULT_ID: "0", VAULT_PX: "1", VAULT_PY: "2", TRON_JSON_RPC_URL: "http://json.invalid", TRON_FULL_HOST: "http://native.invalid" };
    Object.defineProperty(env, "DEPLOYER_PRIVATE_KEY", { get() { throw new Error("parse accessed key"); } });
    const keyDirectory=fs.mkdtempSync(path.join(os.tmpdir(),"dex-unified-tron-")); directories.push(keyDirectory); Object.assign(env,guardianEnvironment(keyDirectory));
    const options = parseOptions(env);
    expect(options.output).to.equal(path.resolve(__dirname, "../vault-deployments/tron-99-51025-0.json"));
    expect(options.dexChainId).to.equal(51025n);
    expect(options.energyTimeoutMs).to.equal(900_000);
    for (const value of [undefined, "0", "01", "-1", "0x123", "18446744073709551616"]) {
      expect(() => parseOptions({ ...env, VAULT_DEX_CHAIN_ID: value })).to.throw();
    }
    expect(parseOptions({ ...env, VAULT_OUTPUT: "custom-vault.json" }).output).to.equal(path.resolve("custom-vault.json"));
    expect(() => parseOptions({ ...env, VAULT_TRON_ENERGY_PREVIEW: "TRUE" })).to.throw("exactly true or false");
  });

  it("loads the standalone script without evaluating network account configuration", function () {
    const script = `process.env = new Proxy(process.env, { get(target, key) {
      if (key === 'DEPLOYER_PRIVATE_KEY') throw new Error('script import read private key');
      return Reflect.get(target, key);
    } }); require('./scripts/deploy_schnorr_vault_tron');
    const { Artifacts } = require('hardhat/internal/artifacts');
    require('./scripts/vault_deployment').loadVaultArtifact({ artifacts: new Artifacts(process.cwd() + '/artifacts') }, 'SchnorrExampleTronV3')
      .catch(error => { console.error(error.message); process.exitCode = 1; });`;
    expect(() => execFileSync(process.execPath, ["-e", script], { cwd: path.resolve(__dirname, ".."), stdio: "pipe" })).not.to.throw();
  });

  it("saves the unified identity before send and verifies exact runtime plus all getters", async function () {
    const f = fixture();
    delete f.deps.artifact;                                                      
    const result = await run(f.options, f.deps);
    expect([f.state.builds, f.state.signs, f.state.broadcasts]).to.deep.equal([1, 1, 1]);
    expect(result).to.include({ status: "verified", depositProtocol: "DEX_VAULT_DEPOSIT_V1", protocol: "DEX_VAULT_MIXED_WITHDRAW_BATCH_V1", dexChainId: "51025", blockNumber: 99 });
    expect(f.state.pending[0]).to.include({ txHash: result.txHash, address: result.address, deployer: result.deployer, depositProtocol: result.depositProtocol });
    expect(loadVaultJournal(f.options.output, vaultIdentity(f.options, artifact))).to.deep.equal(result);
    expect(JSON.stringify(result)).not.to.include(f.options.privateKey);
    expect(f.state.estimateRequests).to.have.length(1);
    expect(f.state.estimateRequests[0].data.length).to.be.greaterThan(artifact.bytecode.length - 2);
    expect(f.state.estimateRequests[0].call_value).to.equal(0);
    expect(f.state.onchain.raw_data.fee_limit).to.equal(2_419_200_000);
  });

  it("recovers pending, verified and explicit transaction hashes without key access or any new send", async function () {
    const f = fixture(); f.state.broadcastError = "connection lost after send";
    await expect(run(f.options, f.deps)).to.be.rejectedWith("connection lost");
    const pending = JSON.parse(fs.readFileSync(f.options.output, "utf8"));
    expect(pending.status).to.equal("pending");
    denyKey(f.options);
    f.options.feeLimit = 350_000_000;
    f.options.originEnergyLimit = 5_000_000;
    const verified = await run(f.options, f.deps);
    expect(verified.txHash).to.equal(pending.txHash);
    await run(f.options, f.deps);
    f.options.output = path.join(path.dirname(f.options.output), "explicit.json");
    f.options.recoverTxHash = verified.txHash;
    await run(f.options, f.deps);
    expect([f.state.builds, f.state.signs, f.state.broadcasts]).to.deep.equal([1, 1, 1]);
    expect(f.state.constructions.slice(1)).to.have.length(3);
    for (const config of f.state.constructions.slice(1)) expect(config).not.to.have.property("privateKey");
  });

  it("rejects legacy or conflicting saved identities before RPC or key access", async function () {
    const f = fixture();
    const result = await run(f.options, f.deps);
    const calls = f.state.rpcCalls;
    denyKey(f.options);
    for (const replacement of [
      { ...result, dexChainId: "51026" }, { ...result, depositProtocol: "INVALID_DEPOSIT_PROTOCOL" },
      { ...result, initialEpoch: "8" }, { ...result, status: undefined },
    ]) {
      fs.writeFileSync(f.options.output, JSON.stringify(replacement));
      await expect(run(f.options, f.deps)).to.be.rejected;
    }
    expect(f.state.rpcCalls).to.equal(calls);
    expect(f.state.broadcasts).to.equal(1);
  });

  it("refuses new mainnet deployment, resource shortage, malformed unsigned data and signer changes", async function () {
    const mainnet = fixture();
    mainnet.options.chainId = TRON_MAINNET_CHAIN_ID;
    mainnet.options.network = "tron-mainnet";
    mainnet.options.nonstandardToken = TRON_MAINNET_USDT;
    denyKey(mainnet.options);
    await expect(run(mainnet.options, mainnet.deps)).to.be.rejectedWith("explicit VAULT_ALLOW_MAINNET_DEPLOY");
    expect(mainnet.state.rpcCalls).to.equal(0);
    const poor = fixture(); poor.state.funded = false;
    await expect(run(poor.options, poor.deps)).to.be.rejected;
    expect(poor.state.signs).to.equal(0);
    const substituted = fixture();
    substituted.state.mutateUnsigned = tx => { tx.raw_data.contract[0].parameter.value.new_contract.bytecode += "00"; };
    await expect(run(substituted.options, substituted.deps)).to.be.rejectedWith("constructor arguments");
    expect(substituted.state.signs).to.equal(0);
    const signer = fixture(); signer.state.mutateSigned = tx => { tx.raw_data.fee_limit++; };
    await expect(run(signer.options, signer.deps)).to.be.rejectedWith("signer changed");
    expect(signer.state.broadcasts).to.equal(0);
    const failedSave = fixture();
    await expect(deployTronVault(failedSave.options, artifact, null, async () => { throw new Error("journal save failed"); }, failedSave.deps)).to.be.rejectedWith("journal save failed");
    expect(failedSave.state.broadcasts).to.equal(0);
  });

  it("previews the exact creation Energy without reading a key, building, signing or writing journals", async function () {
    const f = fixture();
    f.options.deployerAddress = f.tw.defaultAddress.base58;
    denyKey(f.options);
    const plan = await previewTronEnergyPlan(f.options, artifact, f.deps);
    expect(plan.targetEnergy).to.equal("5760000");
    expect(plan.estimatedEnergy).to.equal("4800000");
    expect(plan.estimateSource).to.equal("wallet/estimateenergy");
    expect(plan.purchaseUnits).to.equal(0);
    expect(plan.currentRequiredBalanceSun).to.equal(plan.requiredBalanceSun);
    expect([f.state.builds, f.state.signs, f.state.broadcasts]).to.deep.equal([0, 0, 0]);
    expect(fs.existsSync(f.options.output)).to.equal(false);
    expect(fs.existsSync(f.options.output + ".energy")).to.equal(false);
    expect(f.state.constructions[0]).not.to.have.property("privateKey");
    expect(f.state.rpcCalls).to.equal(12);
  });

  it("fails closed on unsupported creation estimates unless a manual reviewed target is explicit", async function () {
    const unsupported = fixture(); unsupported.state.estimateError = true;
    await expect(run(unsupported.options, unsupported.deps)).to.be.rejectedWith("VAULT_TRON_ENERGY_TARGET");
    expect([unsupported.state.builds, unsupported.state.signs, unsupported.state.broadcasts]).to.deep.equal([0, 0, 0]);
    const manual = fixture(); manual.state.estimateError = true; manual.options.energyTarget = "6000000";
    await run(manual.options, manual.deps);
    expect(manual.state.onchain.raw_data.fee_limit).to.equal(2_520_000_000);
  });

  it("does not replace missing delegated Energy with a liquid-balance burn or unapproved purchase", async function () {
    const f = fixture(); f.state.delegatedEnergy = 0;
    await expect(run(f.options, f.deps)).to.be.rejectedWith("VAULT_TRON_ENERGY_PLAN_JSON");
    expect([f.state.builds, f.state.signs, f.state.broadcasts]).to.deep.equal([0, 0, 0]);
    expect(fs.existsSync(f.options.output + ".energy")).to.equal(false);
  });

  it("rejects a live estimate above the approved target before any payment or deployment", async function () {
    const f = fixture(); f.options.deployerAddress = f.tw.defaultAddress.base58;
    const approved = await previewTronEnergyPlan(f.options, artifact, f.deps);
    f.options.energyPlanJSON = JSON.stringify(approved);
    f.state.estimateEnergy = 7_000_000;
    await expect(run(f.options, f.deps)).to.be.rejectedWith("targetEnergy exceeds the approved plan");
    expect([f.state.builds, f.state.signs, f.state.broadcasts]).to.deep.equal([0, 0, 0]);
  });

  it("buys only the approved missing unit and rebuilds the deployment anchor after delayed delegation", async function () {
    const f = fixture();
    f.options.deployerAddress = f.tw.defaultAddress.base58;
    f.state.delegatedEnergy = 5_630_000;
    f.state.delegationDelayMs = 75_000;
    const previewTime = f.state.nowMs;
    const plan = await previewTronEnergyPlan(f.options, artifact, f.deps);
    expect(plan.purchaseUnits).to.equal(1);
    f.options.energyPlanJSON = JSON.stringify(plan);
    await run(f.options, f.deps);
    expect([f.state.rentalSigns, f.state.rentalBroadcasts, f.state.builds, f.state.signs, f.state.broadcasts]).to.deep.equal([1, 1, 1, 1, 1]);
    expect(f.state.onchain.raw_data.timestamp).to.be.greaterThan(previewTime + 60_000);
    expect(f.state.onchain.raw_data.fee_limit).to.equal(Number(plan.requiredFeeLimitSun));
    denyKey(f.options);
    await run(f.options, f.deps);
    expect([f.state.rentalBroadcasts, f.state.builds, f.state.signs, f.state.broadcasts]).to.deep.equal([1, 1, 1, 1]);
  });

  it("reuses the original approved energy plan and payment after an unknown purchase broadcast", async function () {
    const f = fixture(); f.options.deployerAddress = f.tw.defaultAddress.base58;
    f.state.delegatedEnergy = 5_630_000;
    const initialQuote = await previewTronEnergyPlan(f.options, artifact, f.deps);
    f.state.balanceSun = Number(initialQuote.requiredBalanceSun);
    f.state.rentalBandwidthSun = 256_000;
    const plan = await previewTronEnergyPlan(f.options, artifact, f.deps);
    f.options.energyPlanJSON = JSON.stringify(plan);
    f.state.paymentBroadcastError = "response lost after accepting energy payment";
    await expect(run(f.options, f.deps)).to.be.rejectedWith("broadcast result is unknown");
    expect([f.state.rentalBroadcasts, f.state.builds, f.state.signs]).to.deep.equal([1, 0, 0]);
    f.state.paymentBroadcastError = null;
    delete f.options.energyPlanJSON;
    const resumedPreview = await previewTronEnergyPlan(f.options, artifact, f.deps);
    expect(resumedPreview.planId).to.equal(plan.planId);
    expect(resumedPreview.purchaseUnits).to.equal(1);
    expect(resumedPreview.availableEnergy).to.equal(plan.availableEnergy);
    expect(resumedPreview.availableBalanceSun).to.equal(plan.availableBalanceSun);
    expect(resumedPreview.plannedPurchaseSun).to.equal(plan.plannedPurchaseSun);
    expect(resumedPreview.currentAvailableEnergy).to.equal("5760000");
    expect(resumedPreview.currentBalanceSun).to.equal(String(Number(plan.availableBalanceSun) - 6_000_000 - f.state.rentalBandwidthSun));
    expect(BigInt(plan.paymentBandwidthReserveSunPerUnit)).to.equal(512_000n);
    expect(BigInt(resumedPreview.currentBalanceSun) < BigInt(plan.requiredBalanceSun)).to.equal(true);
    expect(BigInt(resumedPreview.currentRequiredBalanceSun) < BigInt(plan.requiredBalanceSun)).to.equal(true);
    expect(BigInt(resumedPreview.currentBalanceSun) >= BigInt(resumedPreview.currentRequiredBalanceSun)).to.equal(true);
    await run(f.options, f.deps);
    expect([f.state.rentalBroadcasts, f.state.builds, f.state.signs, f.state.broadcasts]).to.deep.equal([1, 1, 1, 1]);
  });

  it("fails before signing if delegated Energy disappears after procurement", async function () {
    const f = fixture(); f.options.deployerAddress = f.tw.defaultAddress.base58;
    const plan = await previewTronEnergyPlan(f.options, artifact, f.deps);
    f.options.energyPlanJSON = JSON.stringify(plan);
    f.state.dropEnergyAtDeploymentBuild = true;
    await expect(run(f.options, f.deps)).to.be.rejectedWith("liquid TRX cannot replace missing Energy");
    expect([f.state.rentalBroadcasts, f.state.signs, f.state.broadcasts]).to.deep.equal([0, 0, 0]);
  });

  it("rejects native Energy price increases and targets beyond the fee cap without purchases", async function () {
    const f = fixture(); f.options.deployerAddress = f.tw.defaultAddress.base58;
    const plan = await previewTronEnergyPlan(f.options, artifact, f.deps);
    f.options.energyPlanJSON = JSON.stringify(plan);
    f.state.energyFee++;
    await expect(run(f.options, f.deps)).to.be.rejectedWith("requiredFeeLimitSun exceeds the approved plan");
    expect([f.state.rentalBroadcasts, f.state.builds, f.state.signs]).to.deep.equal([0, 0, 0]);
    const huge = fixture(); huge.state.estimateEnergy = 11_000_000;
    await expect(run(huge.options, huge.deps)).to.be.rejectedWith("maximum fee limit");
    expect([huge.state.rentalBroadcasts, huge.state.builds, huge.state.signs]).to.deep.equal([0, 0, 0]);
  });

  it("contracts the deployment fee limit when the live Energy price drops within approval", async function () {
    const f = fixture(); f.options.deployerAddress = f.tw.defaultAddress.base58;
    const plan = await previewTronEnergyPlan(f.options, artifact, f.deps);
    f.options.energyPlanJSON = JSON.stringify(plan);
    f.state.energyFee = 400;
    await run(f.options, f.deps);
    expect(f.state.onchain.raw_data.fee_limit).to.equal(2_304_000_000);
    expect(f.state.onchain.raw_data.fee_limit).to.be.lessThan(Number(plan.requiredFeeLimitSun));
    expect(f.state.rentalBroadcasts).to.equal(0);
  });

  it("does not build or sign deployment when Bandwidth price rises after procurement", async function () {
    const f = fixture(); f.options.deployerAddress = f.tw.defaultAddress.base58;
    f.state.delegatedEnergy = 5_630_000;
    const plan = await previewTronEnergyPlan(f.options, artifact, f.deps);
    f.options.energyPlanJSON = JSON.stringify(plan);
    f.state.raiseBandwidthOnDelivery = true;
    await expect(run(f.options, f.deps)).to.be.rejectedWith("Bandwidth price");
    expect(f.state.energyFee).to.equal(420);
    expect([f.state.rentalBroadcasts, f.state.builds, f.state.signs, f.state.broadcasts]).to.deep.equal([1, 0, 0, 0]);
  });

  it("keeps a pending record when receipts, immutable slots or getters fail verification", async function () {
    for (const mutate of [
      state => { state.receiptResult = "OUT_OF_ENERGY"; },
      state => { state.runtime = artifact.runtimeTemplate; },
      state => { state.runtime = expectedVaultRuntime(artifact, 51025n, TRON_MAINNET_USDT); },
      state => { state.domain = 51026n; },
      state => { state.token = TRON_MAINNET_USDT; },
    ]) {
      const f = fixture(); mutate(f.state);
      await expect(run(f.options, f.deps)).to.be.rejected;
      expect(JSON.parse(fs.readFileSync(f.options.output, "utf8")).status).to.equal("pending");
    }
  });

  it("rejects recovered constructor substitutions or conflicting recovery hashes without signing", async function () {
    const f = fixture();
    await run(f.options, f.deps);
    denyKey(f.options);
    f.options.recoverTxHash = "ab".repeat(32);
    await expect(run(f.options, f.deps)).to.be.rejectedWith("differs from saved deployment");
    delete f.options.recoverTxHash;
    f.state.onchain.raw_data.contract[0].parameter.value.new_contract.bytecode += "00";
    await expect(run(f.options, f.deps)).to.be.rejectedWith("constructor arguments");
    expect([f.state.builds, f.state.signs, f.state.broadcasts]).to.deep.equal([1, 1, 1]);
  });
});
