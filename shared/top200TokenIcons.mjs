                                                                                            
                                                                                   
import { nativeKey } from './assetMetadata.mjs'

const ICONS = import.meta.glob ? import.meta.glob('./assets/top200-token-icons/*.png', { eager: true, query: '?url', import: 'default' }) : {}

export const TOP_200_TOKEN_ICONS = [
  {
    "rank": 3,
    "id": "tether",
    "symbol": "USDT",
    "chain": "eth",
    "contract": "0xdac17f958d2ee523a2206206994597c13d831ec7",
    "icon": "tether.png"
  },
  {
    "rank": 3,
    "id": "tether",
    "symbol": "USDT",
    "chain": "trx",
    "contract": "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
    "icon": "tether.png"
  },
  {
    "rank": 4,
    "id": "binancecoin",
    "symbol": "BNB",
    "chain": "eth",
    "contract": "0xb8c77482e45f1f44de1745f52c74426c631bdd52",
    "icon": "binancecoin.png"
  },
  {
    "rank": 6,
    "id": "usd-coin",
    "symbol": "USDC",
    "chain": "arb",
    "contract": "0xaf88d065e77c8cc2239327c5edb3a432268e5831",
    "icon": "usd-coin.png"
  },
  {
    "rank": 6,
    "id": "usd-coin",
    "symbol": "USDC",
    "chain": "base",
    "contract": "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913",
    "icon": "usd-coin.png"
  },
  {
    "rank": 6,
    "id": "usd-coin",
    "symbol": "USDC",
    "chain": "eth",
    "contract": "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48",
    "icon": "usd-coin.png"
  },
  {
    "rank": 6,
    "id": "usd-coin",
    "symbol": "USDC",
    "chain": "trx",
    "contract": "TEkxiTehnzSmSe2XqrBj4w32RUN966rdz8",
    "icon": "usd-coin.png"
  },
  {
    "rank": 15,
    "id": "usds",
    "symbol": "USDS",
    "chain": "arb",
    "contract": "0x6491c05a82219b8d1479057361ff1654749b876b",
    "icon": "usds.png"
  },
  {
    "rank": 15,
    "id": "usds",
    "symbol": "USDS",
    "chain": "base",
    "contract": "0x820c137fa70c8691f0e44dc420a5e53c168921dc",
    "icon": "usds.png"
  },
  {
    "rank": 15,
    "id": "usds",
    "symbol": "USDS",
    "chain": "eth",
    "contract": "0xdc035d45d973e3ec169d2276ddab16f1e407384f",
    "icon": "usds.png"
  },
  {
    "rank": 16,
    "id": "chainlink",
    "symbol": "LINK",
    "chain": "arb",
    "contract": "0xf97f4df75117a78c1a5a0dbb814af92458539fb4",
    "icon": "chainlink.png"
  },
  {
    "rank": 16,
    "id": "chainlink",
    "symbol": "LINK",
    "chain": "bnb",
    "contract": "0xf8a0bf9cf54bb92f17374d9e9a321e6a111a51bd",
    "icon": "chainlink.png"
  },
  {
    "rank": 16,
    "id": "chainlink",
    "symbol": "LINK",
    "chain": "eth",
    "contract": "0x514910771af9ca656af840dff83e8264ecf986ca",
    "icon": "chainlink.png"
  },
  {
    "rank": 19,
    "id": "leo-token",
    "symbol": "LEO",
    "chain": "eth",
    "contract": "0x2af5d2ad76741191d15dfe7bf6ac92d4bd912ca3",
    "icon": "leo-token.png"
  },
  {
    "rank": 22,
    "id": "uniswap",
    "symbol": "UNI",
    "chain": "arb",
    "contract": "0xfa7f8980b0f1e64a2062791cc3b0871572f1f7f0",
    "icon": "uniswap.png"
  },
  {
    "rank": 22,
    "id": "uniswap",
    "symbol": "UNI",
    "chain": "base",
    "contract": "0xc3de830ea07524a0761646a6a4e4be0e114a3c83",
    "icon": "uniswap.png"
  },
  {
    "rank": 22,
    "id": "uniswap",
    "symbol": "UNI",
    "chain": "bnb",
    "contract": "0x06e8bde95be4ce5cb1134bd47ad18a79ffb35822",
    "icon": "uniswap.png"
  },
  {
    "rank": 22,
    "id": "uniswap",
    "symbol": "UNI",
    "chain": "bnb",
    "contract": "0xbf5140a22578168fd562dccf235e5d43a02ce9b1",
    "icon": "uniswap.png"
  },
  {
    "rank": 22,
    "id": "uniswap",
    "symbol": "UNI",
    "chain": "eth",
    "contract": "0x1f9840a85d5af5bf1d1762f925bdaddc4201f984",
    "icon": "uniswap.png"
  },
  {
    "rank": 25,
    "id": "ethena-usde",
    "symbol": "USDE",
    "chain": "eth",
    "contract": "0x4c9edd5852cd905f086c759e8383e09bff1e68b3",
    "icon": "ethena-usde.png"
  },
  {
    "rank": 26,
    "id": "dai",
    "symbol": "DAI",
    "chain": "eth",
    "contract": "0x6b175474e89094c44da98b954eedeac495271d0f",
    "icon": "dai.png"
  },
  {
    "rank": 28,
    "id": "usd1-wlfi",
    "symbol": "USD1",
    "chain": "bnb",
    "contract": "0x8d0d000ee44948fc98c9b98a4fa4921476f08b0d",
    "icon": "usd1-wlfi.png"
  },
  {
    "rank": 28,
    "id": "usd1-wlfi",
    "symbol": "USD1",
    "chain": "eth",
    "contract": "0x8d0d000ee44948fc98c9b98a4fa4921476f08b0d",
    "icon": "usd1-wlfi.png"
  },
  {
    "rank": 28,
    "id": "usd1-wlfi",
    "symbol": "USD1",
    "chain": "trx",
    "contract": "TPFqcBAaaUMCSVRCqPaQ9QnzKhmuoLR6Rc",
    "icon": "usd1-wlfi.png"
  },
  {
    "rank": 33,
    "id": "shiba-inu",
    "symbol": "SHIB",
    "chain": "eth",
    "contract": "0x95ad61b0a150d79219dcf64e1e6cc01f0b64c4ce",
    "icon": "shiba-inu.png"
  },
  {
    "rank": 34,
    "id": "global-dollar",
    "symbol": "USDG",
    "chain": "eth",
    "contract": "0xe343167631d89b6ffc58b88d6b7fb0228795491d",
    "icon": "global-dollar.png"
  },
  {
    "rank": 36,
    "id": "crypto-com-chain",
    "symbol": "CRO",
    "chain": "eth",
    "contract": "0xa0b73e1ff0b80914ab6fe0444e65848c4c34450b",
    "icon": "crypto-com-chain.png"
  },
  {
    "rank": 39,
    "id": "paypal-usd",
    "symbol": "PYUSD",
    "chain": "eth",
    "contract": "0x6c3ea9036406852006290770bedfcaba0e23a0e8",
    "icon": "paypal-usd.png"
  },
  {
    "rank": 40,
    "id": "tether-gold",
    "symbol": "XAUT",
    "chain": "eth",
    "contract": "0x68749665ff8d2d112fa859aa293f07a622782f38",
    "icon": "tether-gold.png"
  },
  {
    "rank": 41,
    "id": "okb",
    "symbol": "OKB",
    "chain": "eth",
    "contract": "0x75231f58b43240c9718dd58b4967c5114342a86c",
    "icon": "okb.png"
  },
  {
    "rank": 45,
    "id": "ondo-us-dollar-yield",
    "symbol": "USDY",
    "chain": "arb",
    "contract": "0x35e050d3c0ec2d29d269a8ecea763a183bdf9a9d",
    "icon": "ondo-us-dollar-yield.png"
  },
  {
    "rank": 45,
    "id": "ondo-us-dollar-yield",
    "symbol": "USDY",
    "chain": "bnb",
    "contract": "0x608593d17a2decbbc4399e4185be4922f97ed32e",
    "icon": "ondo-us-dollar-yield.png"
  },
  {
    "rank": 45,
    "id": "ondo-us-dollar-yield",
    "symbol": "USDY",
    "chain": "eth",
    "contract": "0x96f6ef951840721adbf46ac996b59e0235cb985c",
    "icon": "ondo-us-dollar-yield.png"
  },
  {
    "rank": 46,
    "id": "mantle",
    "symbol": "MNT",
    "chain": "eth",
    "contract": "0x3c3a81e81dc49a522a592e7622a7e711c06bf354",
    "icon": "mantle.png"
  },
  {
    "rank": 47,
    "id": "aave",
    "symbol": "AAVE",
    "chain": "arb",
    "contract": "0xba5ddd1f9d7f570dc94a51479a000e3bce967196",
    "icon": "aave.png"
  },
  {
    "rank": 47,
    "id": "aave",
    "symbol": "AAVE",
    "chain": "bnb",
    "contract": "0xfb6115445bff7b52feb98650c87f44907e58f802",
    "icon": "aave.png"
  },
  {
    "rank": 47,
    "id": "aave",
    "symbol": "AAVE",
    "chain": "eth",
    "contract": "0x7fc66500c84a76ad7e9c93437bfc5ac33e2ddae9",
    "icon": "aave.png"
  },
  {
    "rank": 48,
    "id": "ondo-finance",
    "symbol": "ONDO",
    "chain": "eth",
    "contract": "0xfaba6f8e4a5e8ab82f62fe7c39859fa577269be3",
    "icon": "ondo-finance.png"
  },
  {
    "rank": 49,
    "id": "ethena",
    "symbol": "ENA",
    "chain": "eth",
    "contract": "0x57e114b691db790c35207b2e685d4a43181e6061",
    "icon": "ethena.png"
  },
  {
    "rank": 50,
    "id": "aster-2",
    "symbol": "ASTER",
    "chain": "bnb",
    "contract": "0x000ae314e2a2172a039b26378814c252734f556a",
    "icon": "aster-2.png"
  },
  {
    "rank": 52,
    "id": "morpho",
    "symbol": "MORPHO",
    "chain": "base",
    "contract": "0xbaa5cc21fd487b8fcc2f632f3f4e8d37262a0842",
    "icon": "morpho.png"
  },
  {
    "rank": 52,
    "id": "morpho",
    "symbol": "MORPHO",
    "chain": "eth",
    "contract": "0x58d97b57bb95320f9a05dc918aef65434969c2b2",
    "icon": "morpho.png"
  },
  {
    "rank": 53,
    "id": "pax-gold",
    "symbol": "PAXG",
    "chain": "eth",
    "contract": "0x45804880de22913dafe09f4980848ece6ecbaf78",
    "icon": "pax-gold.png"
  },
  {
    "rank": 54,
    "id": "pepe",
    "symbol": "PEPE",
    "chain": "eth",
    "contract": "0x6982508145454ce325ddbe47a25d4ec3d2311933",
    "icon": "pepe.png"
  },
  {
    "rank": 56,
    "id": "world-liberty-financial",
    "symbol": "WLFI",
    "chain": "eth",
    "contract": "0xda5e1988097297dcdc1f90d4dfe7909e847cbef6",
    "icon": "world-liberty-financial.png"
  },
  {
    "rank": 58,
    "id": "sky",
    "symbol": "SKY",
    "chain": "eth",
    "contract": "0x56072c95faa701256059aa122697b133aded9279",
    "icon": "sky.png"
  },
  {
    "rank": 59,
    "id": "htx-dao",
    "symbol": "HTX",
    "chain": "trx",
    "contract": "TUPM7K8REVzD2UdV4R5fe5M8XbnR2DdoJ6",
    "icon": "htx-dao.png"
  },
  {
    "rank": 60,
    "id": "usdd",
    "symbol": "USDD",
    "chain": "eth",
    "contract": "0x4f8e5de400de08b164e7421b3ee387f461becd1a",
    "icon": "usdd.png"
  },
  {
    "rank": 60,
    "id": "usdd",
    "symbol": "USDD",
    "chain": "trx",
    "contract": "TXDk8mbtRbXeYuMNS83CfKPaYYT8XWv9Hz",
    "icon": "usdd.png"
  },
  {
    "rank": 61,
    "id": "worldcoin-wld",
    "symbol": "WLD",
    "chain": "eth",
    "contract": "0x163f8c2467924be0ae7b5347228cabf260318753",
    "icon": "worldcoin-wld.png"
  },
  {
    "rank": 63,
    "id": "united-stables",
    "symbol": "U",
    "chain": "bnb",
    "contract": "0xce24439f2d9c6a2289f741120fe202248b666666",
    "icon": "united-stables.png"
  },
  {
    "rank": 63,
    "id": "united-stables",
    "symbol": "U",
    "chain": "eth",
    "contract": "0xce24439f2d9c6a2289f741120fe202248b666666",
    "icon": "united-stables.png"
  },
  {
    "rank": 63,
    "id": "united-stables",
    "symbol": "U",
    "chain": "trx",
    "contract": "TFNirp6PbqYE1ZTtWuCMUKJWLNZkoCoeFJ",
    "icon": "united-stables.png"
  },
  {
    "rank": 65,
    "id": "arbitrum",
    "symbol": "ARB",
    "chain": "arb",
    "contract": "0x912ce59144191c1204e64559fe8253a0e49e6548",
    "icon": "arbitrum.png"
  },
  {
    "rank": 65,
    "id": "arbitrum",
    "symbol": "ARB",
    "chain": "eth",
    "contract": "0xb50721bcf8d664c30412cfbc6cf7a15145234ad1",
    "icon": "arbitrum.png"
  },
  {
    "rank": 68,
    "id": "venice-token",
    "symbol": "VVV",
    "chain": "base",
    "contract": "0xacfe6019ed1a7dc6f7b508c02d1b04ec88cc21bf",
    "icon": "venice-token.png"
  },
  {
    "rank": 69,
    "id": "falcon-finance",
    "symbol": "USDF",
    "chain": "eth",
    "contract": "0xfa2b947eec368f42195f24f36d2af29f7c24cec2",
    "icon": "falcon-finance.png"
  },
  {
    "rank": 72,
    "id": "gatechain-token",
    "symbol": "GT",
    "chain": "eth",
    "contract": "0xe66747a101bff2dba3697199dcce5b743b454759",
    "icon": "gatechain-token.png"
  },
  {
    "rank": 73,
    "id": "polygon-ecosystem-token",
    "symbol": "POL",
    "chain": "eth",
    "contract": "0x455e53cbb86018ac2b8092fdcd39d8444affc3f6",
    "icon": "polygon-ecosystem-token.png"
  },
  {
    "rank": 75,
    "id": "quant-network",
    "symbol": "QNT",
    "chain": "eth",
    "contract": "0x4a220e6096b25eadb88358cb44068a3248254675",
    "icon": "quant-network.png"
  },
  {
    "rank": 83,
    "id": "just",
    "symbol": "JST",
    "chain": "trx",
    "contract": "TCFLL5dx5ZJdKnWuesXxi1VPwjLVmWZZy9",
    "icon": "just.png"
  },
  {
    "rank": 84,
    "id": "cosmos",
    "symbol": "ATOM",
    "chain": "bnb",
    "contract": "0x0eb3a705fc54725037cc9e008bdede697f62f335",
    "icon": "cosmos.png"
  },
  {
    "rank": 85,
    "id": "nexo",
    "symbol": "NEXO",
    "chain": "eth",
    "contract": "0xb62132e35a6c13ee1ee0f84dc5d40bad8d815206",
    "icon": "nexo.png"
  },
  {
    "rank": 86,
    "id": "pancakeswap-token",
    "symbol": "CAKE",
    "chain": "base",
    "contract": "0x3055913c90fcc1a6ce9a358911721eeb942013a1",
    "icon": "pancakeswap-token.png"
  },
  {
    "rank": 86,
    "id": "pancakeswap-token",
    "symbol": "CAKE",
    "chain": "bnb",
    "contract": "0x0e09fabb73bd3ade0a17ecc321fd13a19e81ce82",
    "icon": "pancakeswap-token.png"
  },
  {
    "rank": 86,
    "id": "pancakeswap-token",
    "symbol": "CAKE",
    "chain": "eth",
    "contract": "0x152649ea73beab28c5b49b26eb48f7ead6d4c898",
    "icon": "pancakeswap-token.png"
  },
  {
    "rank": 88,
    "id": "injective-protocol",
    "symbol": "INJ",
    "chain": "bnb",
    "contract": "0xa2b726b1145a4773f68593cf171187d8ebe4d495",
    "icon": "injective-protocol.png"
  },
  {
    "rank": 88,
    "id": "injective-protocol",
    "symbol": "INJ",
    "chain": "eth",
    "contract": "0xe28b3b32b6c345a34ff64674606124dd5aceca30",
    "icon": "injective-protocol.png"
  },
  {
    "rank": 93,
    "id": "gho",
    "symbol": "GHO",
    "chain": "eth",
    "contract": "0x40d16fc0246ad3160ccc09b8d0d3a2cd28ae6c2f",
    "icon": "gho.png"
  },
  {
    "rank": 95,
    "id": "aerodrome-finance",
    "symbol": "AERO",
    "chain": "base",
    "contract": "0x940181a94a35a4569e4529a3cdfb74e38fd98631",
    "icon": "aerodrome-finance.png"
  },
  {
    "rank": 97,
    "id": "ether-fi",
    "symbol": "ETHFI",
    "chain": "eth",
    "contract": "0xfe0c30065b384f05761f15d0cc899d4f9f9cc0eb",
    "icon": "ether-fi.png"
  },
  {
    "rank": 105,
    "id": "usual-usd",
    "symbol": "USD0",
    "chain": "eth",
    "contract": "0x73a15fed60bf67631dc6cd7bc5b6e8da8190acf5",
    "icon": "usual-usd.png"
  },
  {
    "rank": 106,
    "id": "layerzero",
    "symbol": "ZRO",
    "chain": "arb",
    "contract": "0x6985884c4392d348587b19cb9eaaf157f13271cd",
    "icon": "layerzero.png"
  },
  {
    "rank": 106,
    "id": "layerzero",
    "symbol": "ZRO",
    "chain": "base",
    "contract": "0x6985884c4392d348587b19cb9eaaf157f13271cd",
    "icon": "layerzero.png"
  },
  {
    "rank": 106,
    "id": "layerzero",
    "symbol": "ZRO",
    "chain": "bnb",
    "contract": "0x6985884c4392d348587b19cb9eaaf157f13271cd",
    "icon": "layerzero.png"
  },
  {
    "rank": 106,
    "id": "layerzero",
    "symbol": "ZRO",
    "chain": "eth",
    "contract": "0x6985884c4392d348587b19cb9eaaf157f13271cd",
    "icon": "layerzero.png"
  },
  {
    "rank": 108,
    "id": "curve-dao-token",
    "symbol": "CRV",
    "chain": "arb",
    "contract": "0x11cdb42b0eb46d95f990bedd4695a6e3fa034978",
    "icon": "curve-dao-token.png"
  },
  {
    "rank": 108,
    "id": "curve-dao-token",
    "symbol": "CRV",
    "chain": "base",
    "contract": "0x8ee73c484a26e0a5df2ee2a4960b789967dd0415",
    "icon": "curve-dao-token.png"
  },
  {
    "rank": 108,
    "id": "curve-dao-token",
    "symbol": "CRV",
    "chain": "eth",
    "contract": "0xd533a949740bb3306d119cc777fa900ba034cd52",
    "icon": "curve-dao-token.png"
  },
  {
    "rank": 112,
    "id": "true-usd",
    "symbol": "TUSD",
    "chain": "bnb",
    "contract": "0x40af3827f39d0eacbf4a168f8d4ee67c121d11c9",
    "icon": "true-usd.png"
  },
  {
    "rank": 112,
    "id": "true-usd",
    "symbol": "TUSD",
    "chain": "eth",
    "contract": "0x0000000000085d4780b73119b644ae5ecd22b376",
    "icon": "true-usd.png"
  },
  {
    "rank": 113,
    "id": "usdtb",
    "symbol": "USDTB",
    "chain": "eth",
    "contract": "0xc139190f447e929f090edeb554d95abb8b18ac1c",
    "icon": "usdtb.png"
  },
  {
    "rank": 114,
    "id": "a7a5",
    "symbol": "A7A5",
    "chain": "eth",
    "contract": "0x6fa0be17e4bea2fcfa22ef89bf8ac9aab0ab0fc9",
    "icon": "a7a5.png"
  },
  {
    "rank": 114,
    "id": "a7a5",
    "symbol": "A7A5",
    "chain": "trx",
    "contract": "TLeVfrdym8RoJreJ23dAGyfJDygRtiWKBZ",
    "icon": "a7a5.png"
  },
  {
    "rank": 115,
    "id": "euro-coin",
    "symbol": "EURC",
    "chain": "base",
    "contract": "0x60a3e35cc302bfa44cb288bc5a4f316fdb1adb42",
    "icon": "euro-coin.png"
  },
  {
    "rank": 115,
    "id": "euro-coin",
    "symbol": "EURC",
    "chain": "eth",
    "contract": "0x1abaea1f7c830bd89acc67ec4af516284b1bc33c",
    "icon": "euro-coin.png"
  },
  {
    "rank": 116,
    "id": "virtual-protocol",
    "symbol": "VIRTUAL",
    "chain": "base",
    "contract": "0x0b3e328455c4059eeb9e3f84b5543f74e24e7e1b",
    "icon": "virtual-protocol.png"
  },
  {
    "rank": 116,
    "id": "virtual-protocol",
    "symbol": "VIRTUAL",
    "chain": "eth",
    "contract": "0x44ff8620b8ca30902395a7bd3f2407e1a091bf73",
    "icon": "virtual-protocol.png"
  },
  {
    "rank": 117,
    "id": "fetch-ai",
    "symbol": "FET",
    "chain": "bnb",
    "contract": "0x031b41e504677879370e9dbcf937283a8691fa7f",
    "icon": "fetch-ai.png"
  },
  {
    "rank": 117,
    "id": "fetch-ai",
    "symbol": "FET",
    "chain": "eth",
    "contract": "0xaea46a60368a7bd060eec7df8cba43b7ef41ad85",
    "icon": "fetch-ai.png"
  },
  {
    "rank": 123,
    "id": "pendle",
    "symbol": "PENDLE",
    "chain": "arb",
    "contract": "0x0c880f6761f1af8d9aa9c466984b80dab9a8c9e8",
    "icon": "pendle.png"
  },
  {
    "rank": 123,
    "id": "pendle",
    "symbol": "PENDLE",
    "chain": "bnb",
    "contract": "0xb3ed0a426155b79b898849803e3b36552f7ed507",
    "icon": "pendle.png"
  },
  {
    "rank": 123,
    "id": "pendle",
    "symbol": "PENDLE",
    "chain": "eth",
    "contract": "0x808507121b80c02388fad14726482e061b8da827",
    "icon": "pendle.png"
  },
  {
    "rank": 125,
    "id": "spx6900",
    "symbol": "SPX",
    "chain": "base",
    "contract": "0x50da645f148798f68ef2d7db7c1cb22a6819bb2c",
    "icon": "spx6900.png"
  },
  {
    "rank": 125,
    "id": "spx6900",
    "symbol": "SPX",
    "chain": "eth",
    "contract": "0xe0f63a424a4439cbe457d80e4f4b51ad25b2c56c",
    "icon": "spx6900.png"
  },
  {
    "rank": 129,
    "id": "bittorrent",
    "symbol": "BTT",
    "chain": "bnb",
    "contract": "0x352cb5e19b12fc216548a2677bd0fce83bae434b",
    "icon": "bittorrent.png"
  },
  {
    "rank": 129,
    "id": "bittorrent",
    "symbol": "BTT",
    "chain": "eth",
    "contract": "0xc669928185dbce49d2230cc9b0979be6dc797957",
    "icon": "bittorrent.png"
  },
  {
    "rank": 129,
    "id": "bittorrent",
    "symbol": "BTT",
    "chain": "trx",
    "contract": "TAFjULxiVgT4qWk6UZwjqwZXTSaGaqnVp4",
    "icon": "bittorrent.png"
  },
  {
    "rank": 132,
    "id": "sun-token",
    "symbol": "SUN",
    "chain": "trx",
    "contract": "TSSMHYeV2uE9qYH95DqyoCuNCzEL1NvU3S",
    "icon": "sun-token.png"
  },
  {
    "rank": 135,
    "id": "first-digital-usd",
    "symbol": "FDUSD",
    "chain": "bnb",
    "contract": "0xc5f0f7b66764f6ec8c8dff7ba683102295e16409",
    "icon": "first-digital-usd.png"
  },
  {
    "rank": 135,
    "id": "first-digital-usd",
    "symbol": "FDUSD",
    "chain": "eth",
    "contract": "0xc5f0f7b66764f6ec8c8dff7ba683102295e16409",
    "icon": "first-digital-usd.png"
  },
  {
    "rank": 136,
    "id": "lido-dao",
    "symbol": "LDO",
    "chain": "arb",
    "contract": "0x13ad51ed4f1b7e9dc168d8a00cb3f4ddd85efa60",
    "icon": "lido-dao.png"
  },
  {
    "rank": 136,
    "id": "lido-dao",
    "symbol": "LDO",
    "chain": "eth",
    "contract": "0x5a98fcbea516cf06857215779fd812ca3bef1b32",
    "icon": "lido-dao.png"
  },
  {
    "rank": 142,
    "id": "bedrock-token",
    "symbol": "BR",
    "chain": "bnb",
    "contract": "0xff7d6a96ae471bbcd7713af9cb1feeb16cf56b41",
    "icon": "bedrock-token.png"
  },
  {
    "rank": 143,
    "id": "bonk",
    "symbol": "BONK",
    "chain": "eth",
    "contract": "0x1151cb3d861920e07a38e03eead12c32178567f6",
    "icon": "bonk.png"
  },
  {
    "rank": 145,
    "id": "gnosis",
    "symbol": "GNO",
    "chain": "arb",
    "contract": "0xa0b862f60edef4452f25b4160f177db44deb6cf1",
    "icon": "gnosis.png"
  },
  {
    "rank": 145,
    "id": "gnosis",
    "symbol": "GNO",
    "chain": "eth",
    "contract": "0x6810e776880c02933d47db1b9fc05908e5386b96",
    "icon": "gnosis.png"
  },
  {
    "rank": 150,
    "id": "starknet",
    "symbol": "STRK",
    "chain": "eth",
    "contract": "0xca14007eff0db1f8135f4c25b34de49ab0d42766",
    "icon": "starknet.png"
  },
  {
    "rank": 151,
    "id": "ethereum-name-service",
    "symbol": "ENS",
    "chain": "eth",
    "contract": "0xc18360217d8f7ab5e7c516566761ea12ce7f9d72",
    "icon": "ethereum-name-service.png"
  },
  {
    "rank": 156,
    "id": "the-graph",
    "symbol": "GRT",
    "chain": "arb",
    "contract": "0x9623063377ad1b27544c965ccd7342f7ea7e88c7",
    "icon": "the-graph.png"
  },
  {
    "rank": 156,
    "id": "the-graph",
    "symbol": "GRT",
    "chain": "eth",
    "contract": "0xc944e90c64b2c07662a292be6244bdf05cda44a7",
    "icon": "the-graph.png"
  },
  {
    "rank": 158,
    "id": "floki",
    "symbol": "FLOKI",
    "chain": "bnb",
    "contract": "0xfb5b838b6cfeedc2873ab27866079ac55363d37e",
    "icon": "floki.png"
  },
  {
    "rank": 158,
    "id": "floki",
    "symbol": "FLOKI",
    "chain": "eth",
    "contract": "0xcf0c122c6b73ff809c693db761e7baebe62b6a2e",
    "icon": "floki.png"
  },
  {
    "rank": 162,
    "id": "apenft",
    "symbol": "NFT",
    "chain": "bnb",
    "contract": "0x20ee7b720f4e4c4ffcb00c4065cdae55271aecca",
    "icon": "apenft.png"
  },
  {
    "rank": 162,
    "id": "apenft",
    "symbol": "NFT",
    "chain": "eth",
    "contract": "0x198d14f2ad9ce69e76ea330b374de4957c3f850a",
    "icon": "apenft.png"
  },
  {
    "rank": 162,
    "id": "apenft",
    "symbol": "NFT",
    "chain": "trx",
    "contract": "TFczxzPhnThNSqr5by8tvxsdCFRRz6cPNq",
    "icon": "apenft.png"
  },
  {
    "rank": 165,
    "id": "syrup",
    "symbol": "SYRUP",
    "chain": "eth",
    "contract": "0x643c4e15d7d62ad0abec4a9bd4b001aa3ef52d66",
    "icon": "syrup.png"
  },
  {
    "rank": 166,
    "id": "compound-governance-token",
    "symbol": "COMP",
    "chain": "bnb",
    "contract": "0x52ce071bd9b1c4b00a0b92d298c512478cad67e8",
    "icon": "compound-governance-token.png"
  },
  {
    "rank": 166,
    "id": "compound-governance-token",
    "symbol": "COMP",
    "chain": "eth",
    "contract": "0xc00e94cb662c3520282e6f5717214004a7f26888",
    "icon": "compound-governance-token.png"
  },
  {
    "rank": 167,
    "id": "bnb48-club-token",
    "symbol": "KOGE",
    "chain": "bnb",
    "contract": "0xe6df05ce8c8301223373cf5b969afcb1498c5528",
    "icon": "bnb48-club-token.png"
  },
  {
    "rank": 169,
    "id": "trust-wallet-token",
    "symbol": "TWT",
    "chain": "bnb",
    "contract": "0x4b0f1812e5df2a09796481ff14017e6005508003",
    "icon": "trust-wallet-token.png"
  },
  {
    "rank": 171,
    "id": "crvusd",
    "symbol": "CRVUSD",
    "chain": "eth",
    "contract": "0xf939e0a03fb07f59a73314e73794be0e57ac1b4e",
    "icon": "crvusd.png"
  },
  {
    "rank": 172,
    "id": "agora-dollar",
    "symbol": "AUSD",
    "chain": "arb",
    "contract": "0x00000000efe302beaa2b3e6e1b18d08d69a9012a",
    "icon": "agora-dollar.png"
  },
  {
    "rank": 172,
    "id": "agora-dollar",
    "symbol": "AUSD",
    "chain": "bnb",
    "contract": "0x00000000efe302beaa2b3e6e1b18d08d69a9012a",
    "icon": "agora-dollar.png"
  },
  {
    "rank": 172,
    "id": "agora-dollar",
    "symbol": "AUSD",
    "chain": "eth",
    "contract": "0x00000000efe302beaa2b3e6e1b18d08d69a9012a",
    "icon": "agora-dollar.png"
  },
  {
    "rank": 175,
    "id": "frax",
    "symbol": "FRAX",
    "chain": "arb",
    "contract": "0x17fc002b466eec40dae837fc4be5c67993ddbd6f",
    "icon": "frax.png"
  },
  {
    "rank": 175,
    "id": "frax",
    "symbol": "FRAX",
    "chain": "eth",
    "contract": "0x853d955acef822db058eb8505911ed77f175b99e",
    "icon": "frax.png"
  },
  {
    "rank": 176,
    "id": "jasmycoin",
    "symbol": "JASMY",
    "chain": "eth",
    "contract": "0x7420b4b9a0110cdc71fb720908340c03f9bc03ec",
    "icon": "jasmycoin.png"
  },
  {
    "rank": 177,
    "id": "eigenlayer",
    "symbol": "EIGEN",
    "chain": "eth",
    "contract": "0xec53bf9167f50cdeb3ae105f56099aaab9061f83",
    "icon": "eigenlayer.png"
  },
  {
    "rank": 193,
    "id": "chain-2",
    "symbol": "XCN",
    "chain": "base",
    "contract": "0x9c632e6aaa3ea73f91554f8a3cb2ed2f29605e0c",
    "icon": "chain-2.png"
  },
  {
    "rank": 193,
    "id": "chain-2",
    "symbol": "XCN",
    "chain": "bnb",
    "contract": "0x7324c7c0d95cebc73eea7e85cbaac0dbdf88a05b",
    "icon": "chain-2.png"
  },
  {
    "rank": 193,
    "id": "chain-2",
    "symbol": "XCN",
    "chain": "eth",
    "contract": "0xa2cd3d43c775978a96bdbf12d733d5a1ed94fb18",
    "icon": "chain-2.png"
  },
  {
    "rank": 196,
    "id": "non-playable-coin",
    "symbol": "NPC",
    "chain": "base",
    "contract": "0xb166e8b140d35d9d8226e40c09f757bac5a4d87d",
    "icon": "non-playable-coin.png"
  },
  {
    "rank": 196,
    "id": "non-playable-coin",
    "symbol": "NPC",
    "chain": "bnb",
    "contract": "0xfebfa339e44c28e2aa9e62ea1027c9cb4e378605",
    "icon": "non-playable-coin.png"
  },
  {
    "rank": 196,
    "id": "non-playable-coin",
    "symbol": "NPC",
    "chain": "eth",
    "contract": "0x8ed97a637a790be1feff5e888d43629dc05408f6",
    "icon": "non-playable-coin.png"
  },
  {
    "rank": 198,
    "id": "convex-finance",
    "symbol": "CVX",
    "chain": "eth",
    "contract": "0x4e3fbd56cd56c3e72c1403e103b45db9da5b9d2b",
    "icon": "convex-finance.png"
  },
  {
    "rank": 200,
    "id": "axie-infinity",
    "symbol": "AXS",
    "chain": "bnb",
    "contract": "0x715d400f88c167884bbcc41c5fea407ed4d2f8a0",
    "icon": "axie-infinity.png"
  },
  {
    "rank": 200,
    "id": "axie-infinity",
    "symbol": "AXS",
    "chain": "eth",
    "contract": "0xbb0e17ef65f82ab018d8edd776e8dd940327b28b",
    "icon": "axie-infinity.png"
  }
]

const BY_NATIVE = new Map(TOP_200_TOKEN_ICONS.map(asset => [nativeKey(asset.chain, asset.contract), asset]))
export function top200AssetForNative(chain, contract) {
  return BY_NATIVE.get(nativeKey(chain, contract))
}
export function top200IconForNative(chain, contract) {
  const asset = top200AssetForNative(chain, contract)
  return asset ? ICONS[`./assets/top200-token-icons/${asset.icon}`] || '' : ''
}
