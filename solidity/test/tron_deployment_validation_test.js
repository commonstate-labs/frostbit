const {G}=require("./helpers/guardian-deployment");
const { expect } = require("chai");
const tronModule = require("tronweb");
const { TronWeb } = tronModule;
const {
  FIELD,
  TRON_MAINNET_CHAIN_ID,
  TRON_MAINNET_USDT,
  ZERO_TRON_ADDRESS,
  MAX_VAULT_FEE_LIMIT_SUN,
  DEFAULT_VAULT_FEE_LIMIT_SUN,
  TX_EXPIRATION_MS,
  validatePubkey,
  validateLiveDeployment,
  resolveNonstandardToken,
  validateRPCURL,
  validateChainParameters,
  verifyTronNetworkIdentity,
  verifyRuntimeCode,
  asBigInt,
  validateDeploymentResources,
  waitForConfirmedReceipt,
  deriveContractAddressHex,
  validateUnsignedCreateSmartContract,
  normalizeRecoveryTxHash,
  validateRecoveredDeploymentTransaction,
} = require("../scripts/deploy_schnorr_vault_tron");

describe("TRON Vault deployment validation", function () {
  const restore = (name, value) => {
    if (value === undefined) delete process.env[name];
    else process.env[name] = value;
  };

  it("validates and canonicalizes the raw DKG BN128 public key", function () {
    expect(validatePubkey(1n, FIELD - 2n)).to.deep.equal({ px: 1n, rawPy: FIELD - 2n, py: 2n });
    for (const pair of [[0n, 0n], [1n, 1n], [FIELD, 2n]]) {
      expect(() => validatePubkey(...pair)).to.throw("BN128 curve point");
    }
  });

  it("requires explicit intent and pins the mainnet nonstandard token", function () {
    const allow = process.env.VAULT_ALLOW_MAINNET_DEPLOY;
    const token = process.env.VAULT_NONSTANDARD_TOKEN;
    try {
      delete process.env.VAULT_ALLOW_MAINNET_DEPLOY;
      delete process.env.VAULT_NONSTANDARD_TOKEN;
      expect(() => validateLiveDeployment(TRON_MAINNET_CHAIN_ID)).to.throw("explicit");
      expect(resolveNonstandardToken(TRON_MAINNET_CHAIN_ID)).to.equal(TRON_MAINNET_USDT);
      process.env.VAULT_NONSTANDARD_TOKEN = ZERO_TRON_ADDRESS;
      expect(() => resolveNonstandardToken(TRON_MAINNET_CHAIN_ID)).to.throw("mainnet nonstandard token");
      process.env.VAULT_ALLOW_MAINNET_DEPLOY = "true";
      expect(() => validateLiveDeployment(TRON_MAINNET_CHAIN_ID)).not.to.throw();
    } finally {
      restore("VAULT_ALLOW_MAINNET_DEPLOY", allow);
      restore("VAULT_NONSTANDARD_TOKEN", token);
    }
  });

  it("accepts only absolute HTTP(S) JSON-RPC URLs", function () {
    expect(validateRPCURL("https://api.trongrid.io/jsonrpc")).to.equal("https://api.trongrid.io/jsonrpc");
    expect(() => validateRPCURL("file:///tmp/node")).to.throw("HTTP(S)");
    expect(() => validateRPCURL("localhost:8545")).to.throw("HTTP(S)");
    expect(() => validateRPCURL("https://user:secret@example.test/#fragment")).to.throw("credentials");
  });

  it("rejects an empty recovery identity while canonicalizing a valid hash", function () {
    expect(normalizeRecoveryTxHash(` 0x${"AB".repeat(32)} `)).to.equal("ab".repeat(32));
    expect(() => normalizeRecoveryTxHash("00".repeat(32))).to.throw("nonzero");
  });

  it("cross-binds native and JSON-RPC endpoints using genesis and a recent anchor", async function () {
    const nowMs = 1_800_000_000_000;
    const height = 123456;
    const blockID = (number, suffix) =>
      `${number.toString(16).padStart(16, "0")}${suffix.repeat(48).slice(0, 48)}`;
    const genesisID = `${"0".repeat(56)}${TRON_MAINNET_CHAIN_ID.toString(16).padStart(8, "0")}`;
    const anchorID = blockID(height, "b");
    const nativeBlock = (number, id, timestamp) => ({
      blockID: id,
      block_header: { raw_data: { number, timestamp } },
    });
    const callJSON = async (_url, method, params) => {
      if (method === "eth_chainId") return "0x2b6653dc";
      if (method === "eth_blockNumber") return `0x${height.toString(16)}`;
      if (method === "eth_getBlockByNumber") {
        const number = Number(BigInt(params[0]));
        const id = number === 0 ? genesisID : anchorID;
        return { number: params[0], hash: `0x${id}` };
      }
      throw new Error(`unexpected JSON-RPC method ${method}`);
    };
    const callNative = async (_url, route, payload) => {
      if (route === "wallet/getnowblock") return nativeBlock(height, anchorID, nowMs - 3_000);
      if (route === "wallet/getnodeinfo") {
        return {
          block: `Num:${height},ID:${anchorID}`,
          solidityBlock: `Num:${height - 1},ID:${blockID(height - 1, "a")}`,
          configNodeInfo: { codeVersion: "4.8.0" },
        };
      }
      if (route === "wallet/getchainparameters") {
        return { chainParameter: [
                                                                              
          { key: "getAllowUpdateAccountName" },
          { key: "getMaintenanceTimeInterval", value: 21_600_000 },
          { key: "getEnergyFee", value: 420 },
          { key: "getTransactionFee", value: 1_000 },
        ] };
      }
      if (route === "wallet/getblockbynum") {
        return payload.num === 0
                                                                         
          ? { blockID: genesisID, block_header: { raw_data: {} } }
          : nativeBlock(height, anchorID, nowMs - 3_000);
      }
      throw new Error(`unexpected native RPC route ${route}`);
    };

    const verified = await verifyTronNetworkIdentity(
      "https://json.example.test/", "https://native.example.test/", TRON_MAINNET_CHAIN_ID,
      { jsonRPC: callJSON, nativeRPC: callNative, nowMs },
    );
    expect(verified.anchorBlockID).to.equal(anchorID);
    expect(verified.blockHeader).to.deep.equal({
      ref_block_bytes: height.toString(16).slice(-4).padStart(4, "0"),
      ref_block_hash: anchorID.slice(16, 32),
      timestamp: nowMs - 3_000,
      expiration: nowMs - 3_000 + TX_EXPIRATION_MS,
    });

    const mismatchedJSON = async (url, method, params) => {
      const result = await callJSON(url, method, params);
      if (method === "eth_getBlockByNumber" && params[0] !== "0x0") {
        return { ...result, hash: `0x${blockID(height, "c")}` };
      }
      return result;
    };
    await expect(verifyTronNetworkIdentity(
      "https://json.example.test/", "https://native.example.test/", TRON_MAINNET_CHAIN_ID,
      { jsonRPC: mismatchedJSON, nativeRPC: callNative, nowMs },
    )).to.be.rejectedWith("do not agree on the anchor block");
  });

  it("accepts omitted zero chain-parameter values but rejects malformed entries", function () {
    expect(() => validateChainParameters({ chainParameter: [
      { key: "getAllowUpdateAccountName" },
      { key: "getMaintenanceTimeInterval", value: 21_600_000 },
      { key: "getEnergyFee", value: 100 },
      { key: "getTransactionFee", value: 1_000 },
    ] })).not.to.throw();

    for (const malformed of [
      { key: "getEnergyFee", value: "100" },
      { key: "getEnergyFee", value: 1.5 },
    ]) {
      expect(() => validateChainParameters({ chainParameter: [
        { key: "getMaintenanceTimeInterval", value: 21_600_000 },
        malformed,
      ] })).to.throw("malformed chain parameters");
    }

    expect(() => validateChainParameters({ chainParameter: [
      { key: "getMaintenanceTimeInterval", value: 21_600_000 },
      { key: "getEnergyFee", value: 100 },
      { key: "getEnergyFee" },
    ] })).to.throw("malformed chain parameters");
  });

  it("requires actual delegated Energy and liquid bandwidth reserve without allowing burn substitution", function () {
    const validated = { transaction: { raw_data_hex: "00".repeat(13_000) } };
    const chainParameters = { energyFee: 100, transactionFee: 1_000 };
    const funded = validateDeploymentResources(
      { balance: 12_656_000 },
      { EnergyLimit: 2_500_000, EnergyUsed: 100_000, freeNetLimit: 600, freeNetUsed: 0 },
      validated,
      DEFAULT_VAULT_FEE_LIMIT_SUN,
      chainParameters,
      2_400_000n,
    );
    expect(funded.usableEnergy).to.equal(2_400_000n);
    expect(funded.paidBandwidthSun).to.equal(12_656_000n);

    expect(() => validateDeploymentResources(
      { balance: 50_000_000 },
      { EnergyLimit: 2_000_000, EnergyUsed: 0 },
      validated,
      DEFAULT_VAULT_FEE_LIMIT_SUN,
      chainParameters,
      2_400_000n,
    )).to.throw("at least 2400000 is required before signing");
    expect(() => validateDeploymentResources(
      { balance: 5_000_000_000 }, { EnergyLimit: 0, freeNetLimit: 100000 },
      validated, DEFAULT_VAULT_FEE_LIMIT_SUN, chainParameters, 2_400_000n,
    )).to.throw("liquid TRX cannot replace missing Energy");
  });

  it("waits for FullNode solidity height without calling walletsolidity", async function () {
    const txHash = "ab".repeat(32);
    const routes = [];
    const result = await waitForConfirmedReceipt("https://native.example.test", txHash, 10_000, {
      nativeRPC: async (_host, route) => {
        routes.push(route);
        if (route === "wallet/gettransactioninfobyid") {
          return { id: txHash, blockNumber: 123, receipt: { result: "SUCCESS" } };
        }
        if (route === "wallet/getnodeinfo") {
          return {
            block: `Num:130,ID:${"1".repeat(64)}`,
            solidityBlock: `Num:123,ID:${"2".repeat(64)}`,
            configNodeInfo: { codeVersion: "4.8.2" },
          };
        }
        throw new Error(`unexpected route ${route}`);
      },
    });
    expect(result.receipt.result).to.equal("SUCCESS");
    expect(routes).to.deep.equal(["wallet/gettransactioninfobyid", "wallet/getnodeinfo"]);
  });

  it("validates the complete unsigned CreateSmartContract before signing", async function () {
    const privateKey = "0000000000000000000000000000000000000000000000000000000000000001";
    const tronWeb = new TronWeb({ fullHost: "http://127.0.0.1:8090", privateKey });
    const artifact = await artifacts.readArtifact("SchnorrExampleTronV3");
    const nowMs = Date.now();
    const blockHeader = {
      ref_block_bytes: "1234",
      ref_block_hash: "0123456789abcdef",
      timestamp: nowMs - 3_000,
      expiration: nowMs - 3_000 + TX_EXPIRATION_MS,
    };
    const request = {
      artifact,
      deployer: tronWeb.defaultAddress.base58,
      px: 1n,
      rawPy: 2n, guardianPx:G.x, guardianPy:G.y,
      epoch: 7n,
      dexChainId: 51025n,
      nonstandardToken: ZERO_TRON_ADDRESS,
      feeLimit: MAX_VAULT_FEE_LIMIT_SUN,
      originEnergyLimit: 10_000_000,
      blockHeader,
      nowMs,
    };
    const unsigned = await tronWeb.transactionBuilder.createSmartContract({
      abi: artifact.abi,
      bytecode: artifact.bytecode,
      name: "SchnorrExampleTronV3",
      feeLimit: request.feeLimit,
      callValue: 0,
      userFeePercentage: 100,
      originEnergyLimit: request.originEnergyLimit,
      parameters: ["1", "2", G.x.toString(), G.y.toString(), "7", "51025", ZERO_TRON_ADDRESS],
      blockHeader,
    }, request.deployer);
    const validated = validateUnsignedCreateSmartContract(tronWeb, unsigned, request);
    expect(validated.txID).to.equal(unsigned.txID.toLowerCase());
    expect(validated.contractAddressHex).to.equal(unsigned.contract_address.toLowerCase());
    expect(() => validateUnsignedCreateSmartContract(tronWeb, unsigned, { ...request, dexChainId: 51026n })).to.throw("constructor arguments");
    expect(() => validateUnsignedCreateSmartContract(tronWeb, unsigned, { ...request, dexChainId: 0n })).to.throw("nonzero uint64");

    const onchain = JSON.parse(JSON.stringify(unsigned));
    delete onchain.visible;
    delete onchain.raw_data.contract[0].parameter.value.new_contract.abi.entrys[0].inputs[0].internalType;
    onchain.raw_data.contract[0].parameter.value.new_contract.abi.entrys[0].type = "Constructor";
    onchain.raw_data.contract[0].parameter.value.new_contract.abi.entrys[0].stateMutability = "Nonpayable";
    onchain.signature = ["1".repeat(130)];
    onchain.ret = [{ contractRet: "SUCCESS" }];
    const recovered = validateRecoveredDeploymentTransaction(
      tronWeb, onchain, normalizeRecoveryTxHash(onchain.txID), request,
    );
    expect(recovered.validated.txID).to.equal(unsigned.txID.toLowerCase());
    expect(recovered.deployedAt).to.equal(new Date(blockHeader.timestamp).toISOString());
    expect(() => validateRecoveredDeploymentTransaction(
      tronWeb, { ...onchain, ret: [{ contractRet: "OUT_OF_ENERGY" }] }, onchain.txID, request,
    )).to.throw("successful deployment");
    const changedTail = onchain.raw_data_hex.endsWith("00") ? "01" : "00";
    expect(() => validateRecoveredDeploymentTransaction(
      tronWeb, { ...onchain, raw_data_hex: `${onchain.raw_data_hex.slice(0, -2)}${changedTail}` }, onchain.txID, request,
    )).to.throw("txID/raw_data_hex");

    const recanonicalize = (transaction) => {
      const protobuf = tronModule.utils.transaction.txJsonToPb(transaction);
      transaction.txID = tronModule.utils.transaction.txPbToTxID(protobuf).replace(/^0x/, "").toLowerCase();
      transaction.raw_data_hex = tronModule.utils.transaction.txPbToRawDataHex(protobuf).toLowerCase();
      const owner = transaction.raw_data.contract[0].parameter.value.owner_address;
      transaction.contract_address = deriveContractAddressHex(transaction.txID, owner);
      return transaction;
    };
    const tampered = (mutate, canonicalize = true) => {
      const transaction = JSON.parse(JSON.stringify(unsigned));
      mutate(transaction);
      return canonicalize ? recanonicalize(transaction) : transaction;
    };
    const alternateOwner = new TronWeb({
      fullHost: "http://127.0.0.1:8090",
      privateKey: "0000000000000000000000000000000000000000000000000000000000000002",
    }).defaultAddress.hex.toLowerCase();
    const maliciousBuilderResults = [
      tampered((tx) => tx.raw_data.contract.push(tx.raw_data.contract[0])),
      tampered((tx) => { tx.raw_data.contract[0].type = "TransferContract"; }),
      tampered((tx) => { tx.raw_data.contract[0].parameter.value.owner_address = alternateOwner; }),
      tampered((tx) => { tx.raw_data.contract[0].parameter.value.call_token_value = 1; }),
      tampered((tx) => { tx.raw_data.contract[0].parameter.value.new_contract.call_value = 1; }),
      tampered((tx) => { tx.raw_data.fee_limit = MAX_VAULT_FEE_LIMIT_SUN + 1; }),
      tampered((tx) => {
        const contract = tx.raw_data.contract[0].parameter.value.new_contract;
        const last = Number.parseInt(contract.bytecode.slice(-2), 16) ^ 1;
        contract.bytecode = `${contract.bytecode.slice(0, -2)}${last.toString(16).padStart(2, "0")}`;
      }),
      tampered((tx) => { tx.raw_data.expiration += 1; }),
      tampered((tx) => { tx.contract_address = `41${"f".repeat(40)}`; }, false),
      tampered((tx) => { tx.signature = ["0".repeat(130)]; }, false),
    ];
    for (const [index, malicious] of maliciousBuilderResults.entries()) {
      expect(() => validateUnsignedCreateSmartContract(tronWeb, malicious, request), `malicious case ${index}`).to.throw();
    }
  });

  it("compares runtime code while masking only compiler-declared immutables", function () {
    const references = { token: [{ start: 1, length: 2 }] };
    expect(verifyRuntimeCode("0x11aabb44", "0x11000044", references)).to.equal("0x11aabb44");
    expect(() => verifyRuntimeCode("0x22aabb44", "0x11000044", references)).to.throw("differs");
    expect(() => verifyRuntimeCode("0x11aabb", "0x11000044", references)).to.throw("differs");
  });

  it("loads the compiler-declared immutable locations for SchnorrExampleTronV3", async function () {
    const artifact = await ethers.getContractFactory("SchnorrExampleTronV3");
    const metadata = await artifacts.readArtifact("SchnorrExampleTronV3");
    const buildInfo = await artifacts.getBuildInfo(`${metadata.sourceName}:${metadata.contractName}`);
    const refs = buildInfo.output.contracts[metadata.sourceName][metadata.contractName]
      .evm.deployedBytecode.immutableReferences;
    const locations = Object.values(refs).flat();
    expect(artifact).to.exist;
    expect(locations.length).to.be.greaterThan(0);
    expect(() => verifyRuntimeCode(metadata.deployedBytecode, metadata.deployedBytecode, refs)).not.to.throw();

    const bytes = Buffer.from(metadata.deployedBytecode.slice(2), "hex");
    const protectedOffsets = new Set(locations.flatMap(({ start, length }) =>
      Array.from({ length }, (_, index) => start + index)));
    const outside = Array.from(bytes.keys()).find((index) => !protectedOffsets.has(index));
    bytes[outside] ^= 1;
    expect(() => verifyRuntimeCode(`0x${bytes.toString("hex")}`, metadata.deployedBytecode, refs)).to.throw("differs");
  });

  it("normalizes integer-like contract return values without accepting unsafe numbers", function () {
    expect(asBigInt("0x2a", "value")).to.equal(42n);
    expect(asBigInt({ toString: () => "42" }, "value")).to.equal(42n);
    expect(() => asBigInt(Number.MAX_SAFE_INTEGER + 1, "value")).to.throw("invalid integer");
  });
});
