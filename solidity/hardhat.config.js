require("@nomicfoundation/hardhat-toolbox");

const accounts = process.env.DEPLOYER_PRIVATE_KEY
  ? [process.env.DEPLOYER_PRIVATE_KEY]
  : [];

                                                       
module.exports = {
  solidity: {
    compilers: [{ version: "0.8.20" }, { version: "0.8.18" }],
  },
  networks: {
    ethereum: {
      url: process.env.RPC_URL_ETHEREUM || "",
      chainId: 1,
      accounts,
    },
    base: {
      url: process.env.RPC_URL_BASE || "",
      chainId: 8453,
      accounts,
    },
    bnb: {
      url: process.env.RPC_URL_BNB || "",
      chainId: 56,
      accounts,
    },
    arbitrum: {
      url: process.env.RPC_URL_ARBITRUM || "",
      chainId: 42161,
      accounts,
    },
    sepolia: {
      url: process.env.RPC_URL_SEPOLIA || "",
      chainId: 11155111,
      accounts,
    },
    baseSepolia: {
      url: process.env.RPC_URL_BASE_SEPOLIA || "",
      chainId: 84532,
      accounts,
    },
    bnbTestnet: {
      url: process.env.RPC_URL_BNB_TESTNET || "",
      chainId: 97,
      accounts,
    },
    arbSepolia: {
      url: process.env.RPC_URL_ARB_SEPOLIA || "",
      chainId: 421614,
      accounts,
    },
  },
  paths: {
    sources: "./contracts",
    tests: "./test",
    cache: "./cache",
    artifacts: "./artifacts",
  },
};
